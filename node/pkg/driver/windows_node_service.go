/**
 * Copyright 2026 Christian Svensson.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package driver

// This file is intentionally not build-tagged so the Windows node service
// logic can be unit tested on any platform with a fake PowershellRunner.
// Only the wiring in node_service_windows.go and the real PowerShell runner
// in powershell_windows.go are Windows-only.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/ibm/ibm-block-csi-driver/node/goid_info"
	"github.com/ibm/ibm-block-csi-driver/node/logger"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	mount "k8s.io/mount-utils"
)

const (
	iscsiInitiatorServiceName = "MSiSCSI"

	windowsDefaultFsType = "ntfs"
	windowsNtfs          = "NTFS"

	// findDiskAttempts and findDiskInterval bound how long NodeStageVolume
	// waits for a freshly mapped LUN to show up as a disk after a rescan.
	findDiskAttempts = 10
	findDiskInterval = 3 * time.Second
)

// PowershellRunner runs a PowerShell script on the host and returns its
// standard output with surrounding whitespace removed.
type PowershellRunner interface {
	Run(script string) (string, error)
}

// WindowsNodeService is the node service for Windows worker nodes. It runs
// as a HostProcess container, so it talks to the host's iSCSI initiator and
// storage stack directly through PowerShell instead of through the chroot
// wrapper the Linux service uses.
//
// Volumes are identified on the host by their array volume UID, which the
// Windows storage stack exposes as the disk's UniqueId (the SCSI page 0x83
// NAA identifier). Only iSCSI connectivity and NTFS file systems on mount
// volumes are supported; raw block volumes are not supported by the Windows
// kubelet.
type WindowsNodeService struct {
	csi.UnimplementedNodeServer

	ConfigYaml       ConfigFile
	Hostname         string
	NodeUtils        NodeUtilsInterface
	Mounter          mount.Interface
	VolumeIdLocksMap SyncLockInterface

	powershell PowershellRunner
	Sleep      func(time.Duration)
}

func NewWindowsNodeService(configYaml ConfigFile, hostname string, nodeUtils NodeUtilsInterface,
	mounter mount.Interface, syncLock SyncLockInterface, powershell PowershellRunner) *WindowsNodeService {
	return &WindowsNodeService{
		ConfigYaml:       configYaml,
		Hostname:         hostname,
		NodeUtils:        nodeUtils,
		Mounter:          mounter,
		VolumeIdLocksMap: syncLock,
		powershell:       powershell,
		Sleep:            time.Sleep,
	}
}

// windowsDisk is the subset of Get-Disk output the service uses.
type windowsDisk struct {
	Number            int    `json:"Number"`
	UniqueId          string `json:"UniqueId"`
	PartitionStyle    string `json:"PartitionStyle"`
	IsOffline         bool   `json:"IsOffline"`
	IsReadOnly        bool   `json:"IsReadOnly"`
	IsBoot            bool   `json:"IsBoot"`
	IsSystem          bool   `json:"IsSystem"`
	Size              int64  `json:"Size"`
	OperationalStatus string `json:"OperationalStatus"`
}

// windowsPartition is the subset of Get-Partition output the service uses.
type windowsPartition struct {
	PartitionNumber int      `json:"PartitionNumber"`
	FileSystem      string   `json:"FileSystem"`
	AccessPaths     []string `json:"AccessPaths"`
	DriveLetter     string   `json:"DriveLetter"`
	Size            int64    `json:"Size"`
}

// psQuote returns s as a single-quoted PowerShell string literal.
func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// accessPath returns the form Windows uses for partition access paths: an
// absolute path with a trailing backslash.
func accessPath(p string) string {
	p = strings.ReplaceAll(p, "/", `\`)
	if !strings.HasSuffix(p, `\`) {
		p += `\`
	}
	return p
}

// decodeJSONList decodes PowerShell ConvertTo-Json output into a slice.
// ConvertTo-Json emits a bare object for a single item and nothing at all
// for no items, so both are normalised to a list first.
func decodeJSONList(out string, v interface{}) error {
	out = strings.TrimSpace(out)
	if out == "" {
		out = "[]"
	} else if !strings.HasPrefix(out, "[") {
		out = "[" + out + "]"
	}
	return json.Unmarshal([]byte(out), v)
}

func (d *WindowsNodeService) runPowershell(script string) (string, error) {
	return d.powershell.Run(script)
}

// ensureIscsiInitiatorRunning starts the Microsoft iSCSI initiator service
// and makes it start automatically, matching what iscsid provides on Linux.
func (d *WindowsNodeService) ensureIscsiInitiatorRunning() error {
	script := fmt.Sprintf("Set-Service -Name %s -StartupType Automatic; Start-Service -Name %s",
		iscsiInitiatorServiceName, iscsiInitiatorServiceName)
	_, err := d.runPowershell(script)
	return err
}

// getIscsiInitiatorName returns the host's iSCSI qualified name.
func (d *WindowsNodeService) getIscsiInitiatorName() (string, error) {
	out, err := d.runPowershell("(Get-InitiatorPort | Where-Object ConnectionType -eq 'iSCSI' | Select-Object -First 1).NodeAddress")
	if err != nil {
		return "", err
	}
	iqn := strings.TrimSpace(out)
	if iqn == "" {
		return "", fmt.Errorf("no iSCSI initiator port found")
	}
	return iqn, nil
}

// ensureIscsiLogin registers every target portal and logs in to every
// target through every portal it was published on. Like the Linux
// EnsureLogin it is best effort: an already established session, or one
// unreachable portal, must not fail staging while other paths work.
func (d *WindowsNodeService) ensureIscsiLogin(ipsByArrayIqn map[string][]string) {
	var pairs []string
	for iqn, ips := range ipsByArrayIqn {
		for _, ip := range ips {
			ip = strings.TrimSpace(ip)
			if ip == "" {
				continue
			}
			pairs = append(pairs, fmt.Sprintf("@{Iqn=%s; Ip=%s}", psQuote(iqn), psQuote(ip)))
		}
	}
	if len(pairs) == 0 {
		logger.Warningf("No iSCSI target portals to log in to")
		return
	}
	script := "$results = @()\n" +
		"foreach ($pair in @(" + strings.Join(pairs, ", ") + ")) {\n" +
		"  try {\n" +
		"    if (-not (Get-IscsiTargetPortal -TargetPortalAddress $pair.Ip -ErrorAction SilentlyContinue)) {\n" +
		"      New-IscsiTargetPortal -TargetPortalAddress $pair.Ip | Out-Null\n" +
		"    }\n" +
		"    Connect-IscsiTarget -NodeAddress $pair.Iqn -TargetPortalAddress $pair.Ip -IsMultipathEnabled $true -IsPersistent $true | Out-Null\n" +
		"    $results += \"$($pair.Iqn) via $($pair.Ip): connected\"\n" +
		"  } catch {\n" +
		"    $results += \"$($pair.Iqn) via $($pair.Ip): $($_.Exception.Message)\"\n" +
		"  }\n" +
		"}\n" +
		"$results -join \"`n\""
	out, err := d.runPowershell(script)
	if err != nil {
		logger.Warningf("iSCSI login script failed: %v", err)
		return
	}
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			logger.Debugf("iSCSI login: %s", line)
		}
	}
}

func (d *WindowsNodeService) rescanDisks() error {
	_, err := d.runPowershell("Update-HostStorageCache")
	return err
}

// findDisk looks up the disk whose UniqueId is the volume UID. It returns
// nil without an error when no such disk is present.
func (d *WindowsNodeService) findDisk(volumeUuid string) (*windowsDisk, error) {
	script := fmt.Sprintf("Get-Disk | Where-Object { $_.UniqueId -ieq %s } | "+
		"Select-Object Number,UniqueId,PartitionStyle,IsOffline,IsReadOnly,IsBoot,IsSystem,Size,OperationalStatus | "+
		"ConvertTo-Json -Compress", psQuote(volumeUuid))
	out, err := d.runPowershell(script)
	if err != nil {
		return nil, err
	}
	var disks []windowsDisk
	if err := decodeJSONList(out, &disks); err != nil {
		return nil, fmt.Errorf("could not parse Get-Disk output %q: %w", out, err)
	}
	switch len(disks) {
	case 0:
		return nil, nil
	case 1:
		return &disks[0], nil
	default:
		return nil, fmt.Errorf("found %d disks with unique id %s, expected one", len(disks), volumeUuid)
	}
}

// waitForDisk rescans until the disk for the volume shows up.
func (d *WindowsNodeService) waitForDisk(volumeUuid string) (*windowsDisk, error) {
	for attempt := 1; ; attempt++ {
		if err := d.rescanDisks(); err != nil {
			logger.Warningf("Disk rescan failed: %v", err)
		}
		disk, err := d.findDisk(volumeUuid)
		if err != nil {
			return nil, err
		}
		if disk != nil {
			return disk, nil
		}
		if attempt >= findDiskAttempts {
			return nil, fmt.Errorf("no disk with unique id %s found after %d rescans", volumeUuid, attempt)
		}
		logger.Debugf("Disk with unique id %s not found yet (attempt %d/%d)", volumeUuid, attempt, findDiskAttempts)
		d.Sleep(findDiskInterval)
	}
}

// prepareDisk brings the disk online and writable, partitions it if it is
// still RAW and formats its data partition if it has no file system yet.
// It returns the data partition.
func (d *WindowsNodeService) prepareDisk(disk *windowsDisk, fsType string) (*windowsPartition, error) {
	if disk.IsBoot || disk.IsSystem {
		return nil, fmt.Errorf("disk %d (%s) is the host's boot or system disk", disk.Number, disk.UniqueId)
	}
	script := fmt.Sprintf(
		"$n = %d\n"+
			"$d = Get-Disk -Number $n\n"+
			"if ($d.IsBoot -or $d.IsSystem) { throw \"disk $n is the boot or system disk\" }\n"+
			"if ($d.IsOffline) { Set-Disk -Number $n -IsOffline $false }\n"+
			"if ($d.IsReadOnly) { Set-Disk -Number $n -IsReadOnly $false }\n"+
			"if ($d.PartitionStyle -eq 'RAW') { Initialize-Disk -Number $n -PartitionStyle GPT | Out-Null }\n"+
			"$p = Get-Partition -DiskNumber $n -ErrorAction SilentlyContinue | Where-Object Type -eq 'Basic' | Select-Object -First 1\n"+
			"if (-not $p) { $p = New-Partition -DiskNumber $n -UseMaximumSize }\n"+
			"$v = $p | Get-Volume\n"+
			"if (-not $v.FileSystem) { $v = $p | Format-Volume -FileSystem %s -Confirm:$false }\n"+
			"$p = Get-Partition -DiskNumber $n -PartitionNumber $p.PartitionNumber\n"+
			"[pscustomobject]@{PartitionNumber=$p.PartitionNumber; FileSystem=[string]$v.FileSystem; AccessPaths=@($p.AccessPaths); DriveLetter=[string]$p.DriveLetter; Size=$p.Size} | ConvertTo-Json -Compress",
		disk.Number, fsType)
	out, err := d.runPowershell(script)
	if err != nil {
		return nil, err
	}
	var partitions []windowsPartition
	if err := decodeJSONList(out, &partitions); err != nil || len(partitions) != 1 {
		return nil, fmt.Errorf("could not parse partition output %q: %v", out, err)
	}
	return &partitions[0], nil
}

// getDataPartition returns the data partition of a prepared disk.
func (d *WindowsNodeService) getDataPartition(diskNumber int) (*windowsPartition, error) {
	script := fmt.Sprintf(
		"$p = Get-Partition -DiskNumber %d -ErrorAction SilentlyContinue | Where-Object Type -eq 'Basic' | Select-Object -First 1\n"+
			"if ($p) { [pscustomobject]@{PartitionNumber=$p.PartitionNumber; FileSystem=[string]($p | Get-Volume).FileSystem; AccessPaths=@($p.AccessPaths); DriveLetter=[string]$p.DriveLetter; Size=$p.Size} | ConvertTo-Json -Compress }",
		diskNumber)
	out, err := d.runPowershell(script)
	if err != nil {
		return nil, err
	}
	var partitions []windowsPartition
	if err := decodeJSONList(out, &partitions); err != nil {
		return nil, fmt.Errorf("could not parse partition output %q: %w", out, err)
	}
	if len(partitions) == 0 {
		return nil, nil
	}
	return &partitions[0], nil
}

func (p *windowsPartition) hasAccessPath(path string) bool {
	want := strings.ToLower(accessPath(path))
	for _, ap := range p.AccessPaths {
		if strings.ToLower(ap) == want {
			return true
		}
	}
	return false
}

func (d *WindowsNodeService) addAccessPath(diskNumber int, partitionNumber int, path string) error {
	script := fmt.Sprintf("Add-PartitionAccessPath -DiskNumber %d -PartitionNumber %d -AccessPath %s",
		diskNumber, partitionNumber, psQuote(accessPath(path)))
	_, err := d.runPowershell(script)
	return err
}

// releaseDisk removes the staging access path from the disk and, once no
// partition on it is reachable through a path any more, takes the disk
// offline so the array can safely unmap it. This is the Windows counterpart
// of flushing the multipath device and deleting the SCSI devices on Linux.
func (d *WindowsNodeService) releaseDisk(disk *windowsDisk, stagingPath string) error {
	if disk.IsBoot || disk.IsSystem {
		return fmt.Errorf("disk %d (%s) is the host's boot or system disk", disk.Number, disk.UniqueId)
	}
	script := fmt.Sprintf(
		"$n = %d\n"+
			"$path = %s\n"+
			"$d = Get-Disk -Number $n\n"+
			"if ($d.IsBoot -or $d.IsSystem) { throw \"disk $n is the boot or system disk\" }\n"+
			"foreach ($p in @(Get-Partition -DiskNumber $n -ErrorAction SilentlyContinue)) {\n"+
			"  if ($p.AccessPaths -contains $path) { Remove-PartitionAccessPath -DiskNumber $n -PartitionNumber $p.PartitionNumber -AccessPath $path }\n"+
			"}\n"+
			"$remaining = @(Get-Partition -DiskNumber $n -ErrorAction SilentlyContinue | ForEach-Object { $_.AccessPaths } | Where-Object { $_ -and -not $_.StartsWith('\\\\?\\Volume') })\n"+
			"if ($remaining.Count -eq 0) { Set-Disk -Number $n -IsOffline $true } else { throw \"disk $n is still in use through $($remaining -join ', ')\" }",
		disk.Number, psQuote(accessPath(stagingPath)))
	_, err := d.runPowershell(script)
	return err
}

// expandPartition grows the data partition to the disk's current size and
// returns the new partition size.
func (d *WindowsNodeService) expandPartition(diskNumber int) (int64, error) {
	script := fmt.Sprintf(
		"$n = %d\n"+
			"Update-HostStorageCache\n"+
			"$p = Get-Partition -DiskNumber $n | Where-Object Type -eq 'Basic' | Select-Object -First 1\n"+
			"if (-not $p) { throw \"disk $n has no data partition\" }\n"+
			"$max = (Get-PartitionSupportedSize -DiskNumber $n -PartitionNumber $p.PartitionNumber).SizeMax\n"+
			"if ($max -gt $p.Size) { Resize-Partition -DiskNumber $n -PartitionNumber $p.PartitionNumber -Size $max }\n"+
			"(Get-Partition -DiskNumber $n -PartitionNumber $p.PartitionNumber).Size",
		diskNumber)
	out, err := d.runPowershell(script)
	if err != nil {
		return 0, err
	}
	var size int64
	if _, err := fmt.Sscanf(strings.TrimSpace(out), "%d", &size); err != nil {
		return 0, fmt.Errorf("could not parse partition size %q: %w", out, err)
	}
	return size, nil
}

func resolveWindowsFsType(requested string) (string, error) {
	switch strings.ToLower(requested) {
	case "", windowsDefaultFsType:
		return windowsNtfs, nil
	default:
		return "", fmt.Errorf("file system type %q is not supported on Windows nodes, use %s", requested, windowsDefaultFsType)
	}
}

func (d *WindowsNodeService) NodeGetCapabilities(ctx context.Context, req *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	defer logger.Exit(logger.Enter(req))

	var caps []*csi.NodeServiceCapability
	for _, cap := range nodeCaps {
		caps = append(caps, &csi.NodeServiceCapability{
			Type: &csi.NodeServiceCapability_Rpc{
				Rpc: &csi.NodeServiceCapability_RPC{Type: cap},
			},
		})
	}
	return &csi.NodeGetCapabilitiesResponse{Capabilities: caps}, nil
}

func (d *WindowsNodeService) NodeGetInfo(ctx context.Context, req *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	defer logger.Exit(logger.Enter(req))

	topologyLabels, err := d.NodeUtils.GetTopologyLabels(ctx, d.Hostname)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	logger.Debugf("discovered topology labels : %v", topologyLabels)

	iscsiIQN, err := d.getIscsiInitiatorName()
	if err != nil {
		err := fmt.Errorf("Cannot find valid iscsi iqn: %w", err)
		return nil, status.Error(codes.Internal, err.Error())
	}

	err = d.NodeUtils.UpdateNodeInitiatorsAnnotation(ctx, d.Hostname, "", nil, iscsiIQN)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	logger.Debugf("node id is : %s", d.Hostname)

	return &csi.NodeGetInfoResponse{
		NodeId:             d.Hostname,
		AccessibleTopology: &csi.Topology{Segments: topologyLabels},
	}, nil
}

func (d *WindowsNodeService) nodeStageVolumeRequestValidation(req *csi.NodeStageVolumeRequest) error {
	if len(req.GetVolumeId()) == 0 {
		return &RequestValidationError{"Volume ID not provided"}
	}
	stagingPath := req.GetStagingTargetPath()
	if len(stagingPath) == 0 {
		return &RequestValidationError{"Staging path not provided"}
	}
	if !d.NodeUtils.IsPathExists(stagingPath) {
		return &RequestValidationError{fmt.Sprintf("Staging path [%s] does not exist", stagingPath)}
	}
	volCap := req.GetVolumeCapability()
	if volCap == nil {
		return &RequestValidationError{"Volume capability not provided"}
	}
	if !isValidVolumeCapabilitiesAccessMode([]*csi.VolumeCapability{volCap}) {
		return &RequestValidationError{"Volume capability AccessMode not supported"}
	}
	switch volCap.GetAccessType().(type) {
	case *csi.VolumeCapability_Mount:
	case *csi.VolumeCapability_Block:
		return &RequestValidationError{"Raw block volumes are not supported on Windows nodes"}
	default:
		return &RequestValidationError{"Volume Access Type is not supported"}
	}
	connectivityType, lun, ipsByArrayInitiator, err := d.NodeUtils.GetInfoFromPublishContext(req.PublishContext)
	if err != nil {
		return &RequestValidationError{fmt.Sprintf("Fail to parse PublishContext %v with err = %v", req.PublishContext, err)}
	}
	if connectivityType != d.ConfigYaml.Connectivity_type.Iscsi {
		return &RequestValidationError{fmt.Sprintf("PublishContext with connectivity type %s, only %s is supported on Windows nodes",
			connectivityType, d.ConfigYaml.Connectivity_type.Iscsi)}
	}
	if lun < 0 {
		return &RequestValidationError{fmt.Sprintf("PublishContext with wrong lun id %d.", lun)}
	}
	if len(ipsByArrayInitiator) == 0 {
		return &RequestValidationError{fmt.Sprintf("PublishContext with wrong arrayInitiators %v.", ipsByArrayInitiator)}
	}
	return nil
}

func (d *WindowsNodeService) NodeStageVolume(ctx context.Context, req *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	defer logger.Exit(logger.Enter(req))

	if err := d.nodeStageVolumeRequestValidation(req); err != nil {
		switch err.(type) {
		case *RequestValidationError:
			return nil, status.Error(codes.InvalidArgument, err.Error())
		default:
			return nil, status.Error(codes.Internal, err.Error())
		}
	}

	_, lun, ipsByArrayIqn, err := d.NodeUtils.GetInfoFromPublishContext(req.PublishContext)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	volumeID := req.VolumeId
	if err := d.VolumeIdLocksMap.AddVolumeAndLunLock(volumeID, lun, "NodeStageVolume"); err != nil {
		logger.Errorf("Another operation is being performed on volume : {%s}", volumeID)
		return nil, status.Error(codes.Aborted, err.Error())
	}
	defer d.VolumeIdLocksMap.RemoveVolumeAndLunLock(volumeID, lun, "NodeStageVolume")

	fsType, err := resolveWindowsFsType(req.GetVolumeCapability().GetMount().GetFsType())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	d.ensureIscsiLogin(ipsByArrayIqn)

	volumeUuid := d.NodeUtils.GetVolumeUuid(volumeID)
	disk, err := d.waitForDisk(volumeUuid)
	if err != nil {
		logger.Errorf("Error while discovering the disk : {%v}", err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}
	logger.Debugf("Discovered disk %d for volume uid %s", disk.Number, volumeUuid)

	partition, err := d.prepareDisk(disk, fsType)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if !strings.EqualFold(partition.FileSystem, fsType) {
		return nil, status.Errorf(codes.AlreadyExists, "Requested fs_type {%v} but found {%v}", fsType, partition.FileSystem)
	}

	stagingPath := req.GetStagingTargetPath()
	if partition.hasAccessPath(stagingPath) {
		logger.Warningf("Idempotent case : staging path already mounted (%s), so no need to mount again. Finish NodeStageVolume", stagingPath)
		return &csi.NodeStageVolumeResponse{}, nil
	}
	if partition.DriveLetter != "" {
		return nil, status.Errorf(codes.FailedPrecondition, "Disk %d for volume %s is in use by the host as drive %s:",
			disk.Number, volumeID, partition.DriveLetter)
	}

	if err := d.addAccessPath(disk.Number, partition.PartitionNumber, stagingPath); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	logger.Debugf("NodeStageVolume Finished: staging path [%s] is ready to be mounted by NodePublishVolume API", stagingPath)
	return &csi.NodeStageVolumeResponse{}, nil
}

func (d *WindowsNodeService) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	defer logger.Exit(logger.Enter(req))

	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID not provided")
	}
	stagingPath := req.GetStagingTargetPath()
	if len(stagingPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Staging target not provided")
	}

	if err := d.VolumeIdLocksMap.AddVolumeLock(volumeID, "NodeUnstageVolume"); err != nil {
		logger.Errorf("Another operation is being performed on volume : {%s}", volumeID)
		return nil, status.Error(codes.Aborted, err.Error())
	}
	defer d.VolumeIdLocksMap.RemoveVolumeLock(volumeID, "NodeUnstageVolume")

	volumeUuid := d.NodeUtils.GetVolumeUuid(volumeID)
	disk, err := d.findDisk(volumeUuid)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if disk == nil {
		logger.Warningf("Idempotent case: no disk with unique id %s is present", volumeUuid)
		return &csi.NodeUnstageVolumeResponse{}, nil
	}

	if err := d.releaseDisk(disk, stagingPath); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	logger.Debugf("NodeUnStageVolume Finished: disk %d released", disk.Number)
	return &csi.NodeUnstageVolumeResponse{}, nil
}

func (d *WindowsNodeService) nodePublishVolumeRequestValidation(req *csi.NodePublishVolumeRequest) (codes.Code, error) {
	if len(req.GetVolumeId()) == 0 {
		return codes.InvalidArgument, &RequestValidationError{"Volume ID not provided"}
	}
	if len(req.GetTargetPath()) == 0 {
		return codes.InvalidArgument, &RequestValidationError{"Target path not provided"}
	}
	volCap := req.GetVolumeCapability()
	if volCap == nil {
		return codes.InvalidArgument, &RequestValidationError{"Volume capability not provided"}
	}
	if len(req.GetStagingTargetPath()) == 0 {
		return codes.FailedPrecondition, &RequestValidationError{"Staging target not provided"}
	}
	if !isValidVolumeCapabilitiesAccessMode([]*csi.VolumeCapability{volCap}) {
		return codes.InvalidArgument, &RequestValidationError{"Volume capability AccessMode not supported"}
	}
	switch volCap.GetAccessType().(type) {
	case *csi.VolumeCapability_Mount:
	case *csi.VolumeCapability_Block:
		return codes.InvalidArgument, &RequestValidationError{"Raw block volumes are not supported on Windows nodes"}
	default:
		return codes.InvalidArgument, &RequestValidationError{"Volume Access Type is not supported"}
	}
	return codes.Internal, nil
}

// NodePublishVolume exposes the staged volume at the pod's target path with
// a directory symlink, which is what k8s.io/mount-utils does for a bind
// mount on Windows.
func (d *WindowsNodeService) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	defer logger.Exit(logger.Enter(req))

	if code, err := d.nodePublishVolumeRequestValidation(req); err != nil {
		return nil, status.Error(code, err.Error())
	}

	volumeID := req.GetVolumeId()
	if err := d.VolumeIdLocksMap.AddVolumeLock(volumeID, "NodePublishVolume"); err != nil {
		logger.Errorf("Another operation is being performed on volume : {%s}", volumeID)
		return nil, status.Error(codes.Aborted, err.Error())
	}
	defer d.VolumeIdLocksMap.RemoveVolumeLock(volumeID, "NodePublishVolume")

	stagingPath := req.GetStagingTargetPath()
	targetPath := req.GetTargetPath()
	logger.Debugf("stagingPath : {%v}, targetPath : {%v}", stagingPath, targetPath)

	isStagingNotMounted, err := d.NodeUtils.IsNotMountPoint(stagingPath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Existing mount check failed for staging path %s: %v", stagingPath, err)
	}
	if isStagingNotMounted {
		return nil, status.Errorf(codes.InvalidArgument, "Staging path %v is not a mount point", stagingPath)
	}

	if d.NodeUtils.IsPathExists(targetPath) {
		isTargetNotMounted, err := d.NodeUtils.IsNotMountPoint(targetPath)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "Existing mount check failed for target path %s: %v", targetPath, err)
		}
		if !isTargetNotMounted {
			logger.Warningf("Idempotent case : targetPath already mounted (%s), so no need to mount again. Finish NodePublishVolume", targetPath)
			return &csi.NodePublishVolumeResponse{}, nil
		}
		// A symlink can only be created where nothing exists yet, so an
		// empty directory the kubelet left behind has to go.
		if err := d.NodeUtils.RemoveFileOrDirectory(targetPath); err != nil {
			return nil, status.Errorf(codes.Internal, "Could not remove existing target path %q: %v", targetPath, err)
		}
	}

	if err := d.Mounter.Mount(stagingPath, targetPath, "", []string{"bind"}); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	logger.Debugf("NodePublishVolume Finished: targetPath {%v} is now a mount point", targetPath)
	return &csi.NodePublishVolumeResponse{}, nil
}

func (d *WindowsNodeService) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	defer logger.Exit(logger.Enter(req))

	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID not provided")
	}
	targetPath := req.GetTargetPath()
	if len(targetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path not provided")
	}

	if err := d.VolumeIdLocksMap.AddVolumeLock(volumeID, "NodeUnpublishVolume"); err != nil {
		logger.Errorf("Another operation is being performed on volume : {%s}", volumeID)
		return nil, status.Error(codes.Aborted, err.Error())
	}
	defer d.VolumeIdLocksMap.RemoveVolumeLock(volumeID, "NodeUnpublishVolume")

	if !d.NodeUtils.IsPathExists(targetPath) {
		logger.Warningf("Idempotent case: target path %s doesn't exist", targetPath)
		return &csi.NodeUnpublishVolumeResponse{}, nil
	}

	isNotMounted, err := d.NodeUtils.IsNotMountPoint(targetPath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Check is target mounted failed. Target : %q, err : %v", targetPath, err)
	}
	if !isNotMounted {
		if err := d.Mounter.Unmount(targetPath); err != nil {
			return nil, status.Errorf(codes.Internal, "Unmount failed. Target : %q, err : %v", targetPath, err)
		}
	} else if err := d.NodeUtils.RemoveFileOrDirectory(targetPath); err != nil {
		return nil, status.Errorf(codes.Internal, "Failed to remove target path %q: %v", targetPath, err)
	}

	logger.Debugf("NodeUnpublishVolume Finished. Target : {%s}", targetPath)
	return &csi.NodeUnpublishVolumeResponse{}, nil
}

func (d *WindowsNodeService) NodeGetVolumeStats(ctx context.Context, req *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	volumeId := req.VolumeId
	goid_info.SetAdditionalIDInfo(volumeId)
	defer goid_info.DeleteAdditionalIDInfo()

	volumePath := req.VolumePath
	if volumeId == "" {
		return nil, status.Error(codes.InvalidArgument, "NodeGetVolumeStats Volume ID must be provided")
	}
	if volumePath == "" {
		return nil, status.Error(codes.InvalidArgument, "NodeGetVolumeStats Volume Path must be provided")
	}
	if !d.NodeUtils.IsPathExists(volumePath) {
		return nil, status.Errorf(codes.NotFound, "volume path %q does not exist", volumePath)
	}

	volumeStats, err := d.NodeUtils.GetFileSystemVolumeStats(volumePath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Failed to get statistics: %s", err)
	}

	return &csi.NodeGetVolumeStatsResponse{
		Usage: []*csi.VolumeUsage{
			{
				Unit:      csi.VolumeUsage_BYTES,
				Available: volumeStats.AvailableBytes,
				Total:     volumeStats.TotalBytes,
				Used:      volumeStats.UsedBytes,
			},
		},
	}, nil
}

func (d *WindowsNodeService) NodeExpandVolume(ctx context.Context, req *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	defer logger.Exit(logger.Enter(req))

	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID not provided")
	}
	if len(req.GetVolumePath()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume path not provided")
	}
	if req.GetVolumeCapability() != nil {
		if _, isBlock := req.GetVolumeCapability().GetAccessType().(*csi.VolumeCapability_Block); isBlock {
			return nil, status.Error(codes.InvalidArgument, "Raw block volumes are not supported on Windows nodes")
		}
	}

	if err := d.VolumeIdLocksMap.AddVolumeLock(volumeID, "NodeExpandVolume"); err != nil {
		logger.Errorf("Another operation is being performed on volume : {%s}", volumeID)
		return nil, status.Error(codes.Aborted, err.Error())
	}
	defer d.VolumeIdLocksMap.RemoveVolumeLock(volumeID, "NodeExpandVolume")

	volumeUuid := d.NodeUtils.GetVolumeUuid(volumeID)
	disk, err := d.findDisk(volumeUuid)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if disk == nil {
		return nil, status.Errorf(codes.NotFound, "No disk with unique id %s is present", volumeUuid)
	}

	size, err := d.expandPartition(disk.Number)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	logger.Debugf("NodeExpandVolume Finished: partition on disk %d is %d bytes", disk.Number, size)
	return &csi.NodeExpandVolumeResponse{CapacityBytes: size}, nil
}
