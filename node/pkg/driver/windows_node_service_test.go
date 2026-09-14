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

package driver_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/golang/mock/gomock"
	"github.com/ibm/ibm-block-csi-driver/node/mocks"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	winVolumeId    = "SVC:71;60050764008581B6A800000000000392"
	winVolumeUuid  = "60050764008581B6A800000000000392"
	winStagingPath = `E:\kubelet\plugins\kubernetes.io\csi\block.csi.ibm.com\abc\globalmount`
	winTargetPath  = `E:\kubelet\pods\uid\volumes\kubernetes.io~csi\pvc\mount`
	winArrayIqn    = "iqn.1986-03.com.ibm:2145.tera.node1"
)

// fakePowershell records every script and answers from a table of
// substring matches, in order.
type fakePowershell struct {
	scripts   []string
	responses []fakeResponse
}

type fakeResponse struct {
	contains string
	out      string
	err      error
	limit    int // answer at most this many times; 0 means unlimited
	calls    int
}

func (f *fakePowershell) Run(script string) (string, error) {
	f.scripts = append(f.scripts, script)
	for i := range f.responses {
		r := &f.responses[i]
		if r.limit > 0 && r.calls >= r.limit {
			continue
		}
		if strings.Contains(script, r.contains) {
			r.calls++
			return f.responses[i].out, f.responses[i].err
		}
	}
	return "", errors.New("unexpected script: " + script)
}

func (f *fakePowershell) count(contains string) int {
	n := 0
	for _, s := range f.scripts {
		if strings.Contains(s, contains) {
			n++
		}
	}
	return n
}

func (f *fakePowershell) last(contains string) string {
	for i := len(f.scripts) - 1; i >= 0; i-- {
		if strings.Contains(f.scripts[i], contains) {
			return f.scripts[i]
		}
	}
	return ""
}

// winConfigYaml carries the publish-context keys the real parser needs.
var winConfigYaml = driver.ConfigFile{
	Controller: driver.Controller{
		Publish_context_lun_parameter:          PublishContextParamLun,
		Publish_context_connectivity_parameter: PublishContextParamConnectivity,
		Publish_context_separator:              ",",
		Publish_context_array_iqn:              PublishContextParamArrayIqn,
		Publish_context_fc_initiators:          "PUBLISH_CONTEXT_ARRAY_FC_INITIATORS",
		Publish_context_nvme_initiators:        "PUBLISH_CONTEXT_ARRAY_NVME_INITIATORS",
	},
	Parameters:        ConfigYaml.Parameters,
	Connectivity_type: ConfigYaml.Connectivity_type,
}

func newTestWindowsNodeService(nodeUtils driver.NodeUtilsInterface, mounter driver.NodeMounter, ps *fakePowershell) *driver.WindowsNodeService {
	service := driver.NewWindowsNodeService(winConfigYaml, "test-host", nodeUtils, mounter, driver.NewSyncLock(1000, true), ps)
	service.Sleep = func(time.Duration) {}
	return service
}

func winMountCap(fsType string) *csi.VolumeCapability {
	return &csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Mount{Mount: &csi.VolumeCapability_MountVolume{FsType: fsType}},
		AccessMode: &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER},
	}
}

func winPublishContext() map[string]string {
	return map[string]string{
		PublishContextParamLun:          "10",
		PublishContextParamConnectivity: "iscsi",
		PublishContextParamArrayIqn:     winArrayIqn,
		winArrayIqn:                     "[fd00:7000:0000:0000:0000:0000:0000:0011],[fd00:7000::17]:3260,10.0.0.5:3260",
	}
}

func assertCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if want == codes.OK {
		if err != nil {
			t.Fatalf("expected success, got %v", err)
		}
		return
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected a gRPC status error with code %v, got %v", want, err)
	}
	if st.Code() != want {
		t.Fatalf("expected code %v, got %v: %v", want, st.Code(), err)
	}
}

func TestWindowsNodeGetInfo(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	nodeUtils := mocks.NewMockNodeUtilsInterface(mockCtrl)
	ps := &fakePowershell{responses: []fakeResponse{{contains: "Get-InitiatorPort", out: "iqn.2026-04.link.aquinas:k8s-2\r\n"}}}
	service := newTestWindowsNodeService(nodeUtils, nil, ps)

	labels := map[string]string{"topology.block.csi.ibm.com/zone": "a"}
	nodeUtils.EXPECT().GetTopologyLabels(gomock.Any(), "test-host").Return(labels, nil)
	nodeUtils.EXPECT().UpdateNodeInitiatorsAnnotation(gomock.Any(), "test-host", "", nil, "iqn.2026-04.link.aquinas:k8s-2").Return(nil)

	resp, err := service.NodeGetInfo(context.TODO(), &csi.NodeGetInfoRequest{})
	assertCode(t, err, codes.OK)
	if resp.NodeId != "test-host" || resp.AccessibleTopology.Segments["topology.block.csi.ibm.com/zone"] != "a" {
		t.Fatalf("unexpected response %v", resp)
	}
}

func TestWindowsNodeGetInfoWithoutInitiator(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	nodeUtils := mocks.NewMockNodeUtilsInterface(mockCtrl)
	ps := &fakePowershell{responses: []fakeResponse{{contains: "Get-InitiatorPort", out: ""}}}
	service := newTestWindowsNodeService(nodeUtils, nil, ps)
	nodeUtils.EXPECT().GetTopologyLabels(gomock.Any(), "test-host").Return(nil, nil)

	_, err := service.NodeGetInfo(context.TODO(), &csi.NodeGetInfoRequest{})
	assertCode(t, err, codes.Internal)
}

func TestWindowsNodeStageVolume(t *testing.T) {
	rawDisk := `{"Number":3,"UniqueId":"` + winVolumeUuid + `","PartitionStyle":"RAW","IsOffline":true,"IsReadOnly":false,"IsBoot":false,"IsSystem":false,"Size":10737418240,"OperationalStatus":"Offline"}`
	systemDisk := `{"Number":0,"UniqueId":"` + winVolumeUuid + `","PartitionStyle":"GPT","IsBoot":true,"IsSystem":true,"Size":10737418240}`
	freshPartition := `{"PartitionNumber":2,"FileSystem":"NTFS","AccessPaths":["\\\\?\\Volume{39289ba9-4a9f-40fc-8121-35284a3443e0}\\"],"DriveLetter":"\u0000","Size":10720641024}`
	stagedPartition := `{"PartitionNumber":2,"FileSystem":"NTFS","AccessPaths":["` + strings.ReplaceAll(winStagingPath, `\`, `\\`) + `\\","\\\\?\\Volume{39289ba9}\\"],"DriveLetter":"","Size":10720641024}`
	hostPartition := `{"PartitionNumber":2,"FileSystem":"NTFS","AccessPaths":["E:\\"],"DriveLetter":"E","Size":10720641024}`
	refsPartition := `{"PartitionNumber":2,"FileSystem":"ReFS","AccessPaths":[],"DriveLetter":"","Size":10720641024}`

	testCases := []struct {
		name          string
		fsType        string
		volCap        *csi.VolumeCapability
		context       map[string]string
		stagingExists bool
		responses     []fakeResponse
		expCode       codes.Code
		expAccessPath bool
		expRescans    int
	}{
		{
			name:          "raw disk is brought online, formatted and mounted",
			fsType:        "ntfs",
			stagingExists: true,
			responses: []fakeResponse{
				{contains: "Connect-IscsiTarget", out: winArrayIqn + " via fd00:7000::11: connected"},
				{contains: "Update-HostStorageCache", out: ""},
				{contains: "Get-Disk | Where-Object", out: rawDisk},
				{contains: "Initialize-Disk", out: freshPartition},
				{contains: "Add-PartitionAccessPath", out: ""},
				{contains: "icacls.exe", out: ""},
			},
			expCode:       codes.OK,
			expAccessPath: true,
			expRescans:    1,
		},
		{
			name:          "default file system is ntfs",
			fsType:        "",
			stagingExists: true,
			responses: []fakeResponse{
				{contains: "Connect-IscsiTarget", out: ""},
				{contains: "Update-HostStorageCache", out: ""},
				{contains: "Get-Disk | Where-Object", out: rawDisk},
				{contains: "Initialize-Disk", out: freshPartition},
				{contains: "Add-PartitionAccessPath", out: ""},
				{contains: "icacls.exe", out: ""},
			},
			expCode:       codes.OK,
			expAccessPath: true,
			expRescans:    1,
		},
		{
			name:          "provisioner default ext4 means ntfs on windows",
			fsType:        "ext4",
			stagingExists: true,
			responses: []fakeResponse{
				{contains: "Connect-IscsiTarget", out: ""},
				{contains: "Update-HostStorageCache", out: ""},
				{contains: "Get-Disk | Where-Object", out: rawDisk},
				{contains: "Initialize-Disk", out: freshPartition},
				{contains: "Add-PartitionAccessPath", out: ""},
				{contains: "icacls.exe", out: ""},
			},
			expCode:       codes.OK,
			expAccessPath: true,
			expRescans:    1,
		},
		{
			name:          "disk appears after a few rescans",
			fsType:        "ntfs",
			stagingExists: true,
			responses: []fakeResponse{
				{contains: "Connect-IscsiTarget", out: ""},
				{contains: "Update-HostStorageCache", out: ""},
				{contains: "Get-Disk | Where-Object", out: "", limit: 2},
				{contains: "Get-Disk | Where-Object", out: rawDisk},
				{contains: "Initialize-Disk", out: freshPartition},
				{contains: "Add-PartitionAccessPath", out: ""},
				{contains: "icacls.exe", out: ""},
			},
			expCode:       codes.OK,
			expAccessPath: true,
			expRescans:    3,
		},
		{
			name:          "already staged is idempotent",
			fsType:        "ntfs",
			stagingExists: true,
			responses: []fakeResponse{
				{contains: "Connect-IscsiTarget", out: ""},
				{contains: "Update-HostStorageCache", out: ""},
				{contains: "Get-Disk | Where-Object", out: rawDisk},
				{contains: "Initialize-Disk", out: stagedPartition},
			},
			expCode:       codes.OK,
			expAccessPath: false,
			expRescans:    1,
		},
		{
			name:          "disk never shows up",
			fsType:        "ntfs",
			stagingExists: true,
			responses: []fakeResponse{
				{contains: "Connect-IscsiTarget", out: ""},
				{contains: "Update-HostStorageCache", out: ""},
				{contains: "Get-Disk | Where-Object", out: ""},
			},
			expCode:    codes.Internal,
			expRescans: 10,
		},
		{
			name:          "disk is the host's system disk",
			fsType:        "ntfs",
			stagingExists: true,
			responses: []fakeResponse{
				{contains: "Connect-IscsiTarget", out: ""},
				{contains: "Update-HostStorageCache", out: ""},
				{contains: "Get-Disk | Where-Object", out: systemDisk},
			},
			expCode:    codes.Internal,
			expRescans: 1,
		},
		{
			name:          "disk is in use by the host",
			fsType:        "ntfs",
			stagingExists: true,
			responses: []fakeResponse{
				{contains: "Connect-IscsiTarget", out: ""},
				{contains: "Update-HostStorageCache", out: ""},
				{contains: "Get-Disk | Where-Object", out: rawDisk},
				{contains: "Initialize-Disk", out: hostPartition},
			},
			expCode:    codes.FailedPrecondition,
			expRescans: 1,
		},
		{
			name:          "existing file system does not match",
			fsType:        "ntfs",
			stagingExists: true,
			responses: []fakeResponse{
				{contains: "Connect-IscsiTarget", out: ""},
				{contains: "Update-HostStorageCache", out: ""},
				{contains: "Get-Disk | Where-Object", out: rawDisk},
				{contains: "Initialize-Disk", out: refsPartition},
			},
			expCode:    codes.AlreadyExists,
			expRescans: 1,
		},
		{
			name:          "unsupported file system",
			fsType:        "xfs",
			stagingExists: true,
			expCode:       codes.InvalidArgument,
		},
		{
			name:          "raw block volume",
			stagingExists: true,
			volCap: &csi.VolumeCapability{
				AccessType: &csi.VolumeCapability_Block{Block: &csi.VolumeCapability_BlockVolume{}},
				AccessMode: &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER},
			},
			expCode: codes.InvalidArgument,
		},
		{
			name:          "fibre channel connectivity",
			fsType:        "ntfs",
			stagingExists: true,
			context: map[string]string{
				PublishContextParamLun:                "10",
				PublishContextParamConnectivity:       "fc",
				"PUBLISH_CONTEXT_ARRAY_FC_INITIATORS": "500507680b21e2be",
			},
			expCode: codes.InvalidArgument,
		},
		{
			name:          "staging path missing",
			fsType:        "ntfs",
			stagingExists: false,
			expCode:       codes.InvalidArgument,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()
			nodeUtils := mocks.NewMockNodeUtilsInterface(mockCtrl)
			ps := &fakePowershell{responses: tc.responses}
			service := newTestWindowsNodeService(nodeUtils, nil, ps)

			realNodeUtils := driver.NewNodeUtils(nil, nil, winConfigYaml, nil)
			nodeUtils.EXPECT().IsPathExists(winStagingPath).Return(tc.stagingExists).AnyTimes()
			nodeUtils.EXPECT().GetInfoFromPublishContext(gomock.Any()).DoAndReturn(realNodeUtils.GetInfoFromPublishContext).AnyTimes()
			nodeUtils.EXPECT().GetVolumeUuid(winVolumeId).Return(winVolumeUuid).AnyTimes()

			volCap := tc.volCap
			if volCap == nil {
				volCap = winMountCap(tc.fsType)
			}
			publishContext := tc.context
			if publishContext == nil {
				publishContext = winPublishContext()
			}
			req := &csi.NodeStageVolumeRequest{
				VolumeId:          winVolumeId,
				StagingTargetPath: winStagingPath,
				VolumeCapability:  volCap,
				PublishContext:    publishContext,
			}

			_, err := service.NodeStageVolume(context.TODO(), req)
			assertCode(t, err, tc.expCode)

			if got := ps.count("Update-HostStorageCache"); got != tc.expRescans {
				t.Fatalf("expected %d rescans, got %d", tc.expRescans, got)
			}
			if tc.expRescans > 0 {
				login := ps.last("Connect-IscsiTarget")
				if !strings.Contains(login, "'"+winArrayIqn+"'") || !strings.Contains(login, "'fd00:7000::11'") ||
					!strings.Contains(login, "'fd00:7000::17'") || !strings.Contains(login, "'10.0.0.5'") || strings.Contains(login, "[") {
					t.Fatalf("login script does not target every portal: %s", login)
				}
				if find := ps.last("Get-Disk | Where-Object"); !strings.Contains(find, "'"+winVolumeUuid+"'") {
					t.Fatalf("disk lookup does not use the volume uid: %s", find)
				}
			}
			mountScript := ps.last("Add-PartitionAccessPath")
			if tc.expAccessPath {
				if !strings.Contains(mountScript, "-DiskNumber 3 -PartitionNumber 2 -AccessPath '"+winStagingPath+`\'`) {
					t.Fatalf("unexpected mount script: %s", mountScript)
				}
				if !strings.Contains(ps.last("Initialize-Disk"), "Format-Volume -FileSystem NTFS") {
					t.Fatalf("prepare script does not format NTFS: %s", ps.last("Initialize-Disk"))
				}
				if acl := ps.last("icacls.exe"); !strings.Contains(acl, "'"+winStagingPath+`\' /grant '*S-1-5-11:(OI)(CI)M'`) {
					t.Fatalf("unexpected access grant script: %s", acl)
				}
			} else if mountScript != "" {
				t.Fatalf("did not expect an access path to be added: %s", mountScript)
			}
		})
	}
}

func TestWindowsNodeUnstageVolume(t *testing.T) {
	disk := `{"Number":3,"UniqueId":"` + winVolumeUuid + `","PartitionStyle":"GPT","Size":10737418240}`

	t.Run("releases the disk", func(t *testing.T) {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()
		nodeUtils := mocks.NewMockNodeUtilsInterface(mockCtrl)
		ps := &fakePowershell{responses: []fakeResponse{
			{contains: "Get-Disk | Where-Object", out: disk},
			{contains: "Remove-PartitionAccessPath", out: ""},
		}}
		service := newTestWindowsNodeService(nodeUtils, nil, ps)
		nodeUtils.EXPECT().GetVolumeUuid(winVolumeId).Return(winVolumeUuid)

		_, err := service.NodeUnstageVolume(context.TODO(), &csi.NodeUnstageVolumeRequest{VolumeId: winVolumeId, StagingTargetPath: winStagingPath})
		assertCode(t, err, codes.OK)
		release := ps.last("Remove-PartitionAccessPath")
		if !strings.Contains(release, "$n = 3\n") || !strings.Contains(release, "'"+winStagingPath+`\'`) || !strings.Contains(release, "Set-Disk -Number $n -IsOffline $true") {
			t.Fatalf("unexpected release script: %s", release)
		}
	})

	t.Run("disk already gone is idempotent", func(t *testing.T) {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()
		nodeUtils := mocks.NewMockNodeUtilsInterface(mockCtrl)
		ps := &fakePowershell{responses: []fakeResponse{{contains: "Get-Disk | Where-Object", out: ""}}}
		service := newTestWindowsNodeService(nodeUtils, nil, ps)
		nodeUtils.EXPECT().GetVolumeUuid(winVolumeId).Return(winVolumeUuid)

		_, err := service.NodeUnstageVolume(context.TODO(), &csi.NodeUnstageVolumeRequest{VolumeId: winVolumeId, StagingTargetPath: winStagingPath})
		assertCode(t, err, codes.OK)
		if ps.count("Remove-PartitionAccessPath") != 0 {
			t.Fatalf("did not expect a release script")
		}
	})

	t.Run("release failure is reported", func(t *testing.T) {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()
		nodeUtils := mocks.NewMockNodeUtilsInterface(mockCtrl)
		ps := &fakePowershell{responses: []fakeResponse{
			{contains: "Get-Disk | Where-Object", out: disk},
			{contains: "Remove-PartitionAccessPath", err: errors.New("disk 3 is still in use through E:\\")},
		}}
		service := newTestWindowsNodeService(nodeUtils, nil, ps)
		nodeUtils.EXPECT().GetVolumeUuid(winVolumeId).Return(winVolumeUuid)

		_, err := service.NodeUnstageVolume(context.TODO(), &csi.NodeUnstageVolumeRequest{VolumeId: winVolumeId, StagingTargetPath: winStagingPath})
		assertCode(t, err, codes.Internal)
	})
}

func TestWindowsNodePublishVolume(t *testing.T) {
	req := &csi.NodePublishVolumeRequest{
		VolumeId:          winVolumeId,
		StagingTargetPath: winStagingPath,
		TargetPath:        winTargetPath,
		VolumeCapability:  winMountCap("ntfs"),
	}

	t.Run("links target to staging path", func(t *testing.T) {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()
		nodeUtils := mocks.NewMockNodeUtilsInterface(mockCtrl)
		mounter := mocks.NewMockNodeMounter(mockCtrl)
		service := newTestWindowsNodeService(nodeUtils, mounter, &fakePowershell{})
		nodeUtils.EXPECT().IsNotMountPoint(winStagingPath).Return(false, nil)
		nodeUtils.EXPECT().IsPathExists(winTargetPath).Return(false)
		mounter.EXPECT().Mount(winStagingPath, winTargetPath, "", []string{"bind"}).Return(nil)

		_, err := service.NodePublishVolume(context.TODO(), req)
		assertCode(t, err, codes.OK)
	})

	t.Run("replaces an empty target directory", func(t *testing.T) {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()
		nodeUtils := mocks.NewMockNodeUtilsInterface(mockCtrl)
		mounter := mocks.NewMockNodeMounter(mockCtrl)
		service := newTestWindowsNodeService(nodeUtils, mounter, &fakePowershell{})
		nodeUtils.EXPECT().IsNotMountPoint(winStagingPath).Return(false, nil)
		nodeUtils.EXPECT().IsPathExists(winTargetPath).Return(true)
		nodeUtils.EXPECT().IsNotMountPoint(winTargetPath).Return(true, nil)
		nodeUtils.EXPECT().RemoveFileOrDirectory(winTargetPath).Return(nil)
		mounter.EXPECT().Mount(winStagingPath, winTargetPath, "", []string{"bind"}).Return(nil)

		_, err := service.NodePublishVolume(context.TODO(), req)
		assertCode(t, err, codes.OK)
	})

	t.Run("already published is idempotent", func(t *testing.T) {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()
		nodeUtils := mocks.NewMockNodeUtilsInterface(mockCtrl)
		mounter := mocks.NewMockNodeMounter(mockCtrl)
		service := newTestWindowsNodeService(nodeUtils, mounter, &fakePowershell{})
		nodeUtils.EXPECT().IsNotMountPoint(winStagingPath).Return(false, nil)
		nodeUtils.EXPECT().IsPathExists(winTargetPath).Return(true)
		nodeUtils.EXPECT().IsNotMountPoint(winTargetPath).Return(false, nil)

		_, err := service.NodePublishVolume(context.TODO(), req)
		assertCode(t, err, codes.OK)
	})

	t.Run("staging path is not mounted", func(t *testing.T) {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()
		nodeUtils := mocks.NewMockNodeUtilsInterface(mockCtrl)
		mounter := mocks.NewMockNodeMounter(mockCtrl)
		service := newTestWindowsNodeService(nodeUtils, mounter, &fakePowershell{})
		nodeUtils.EXPECT().IsNotMountPoint(winStagingPath).Return(true, nil)

		_, err := service.NodePublishVolume(context.TODO(), req)
		assertCode(t, err, codes.InvalidArgument)
	})
}

func TestWindowsNodeUnpublishVolume(t *testing.T) {
	req := &csi.NodeUnpublishVolumeRequest{VolumeId: winVolumeId, TargetPath: winTargetPath}

	t.Run("removes the link", func(t *testing.T) {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()
		nodeUtils := mocks.NewMockNodeUtilsInterface(mockCtrl)
		mounter := mocks.NewMockNodeMounter(mockCtrl)
		service := newTestWindowsNodeService(nodeUtils, mounter, &fakePowershell{})
		nodeUtils.EXPECT().IsPathExists(winTargetPath).Return(true)
		nodeUtils.EXPECT().IsNotMountPoint(winTargetPath).Return(false, nil)
		mounter.EXPECT().Unmount(winTargetPath).Return(nil)

		_, err := service.NodeUnpublishVolume(context.TODO(), req)
		assertCode(t, err, codes.OK)
	})

	t.Run("removes a stale directory", func(t *testing.T) {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()
		nodeUtils := mocks.NewMockNodeUtilsInterface(mockCtrl)
		mounter := mocks.NewMockNodeMounter(mockCtrl)
		service := newTestWindowsNodeService(nodeUtils, mounter, &fakePowershell{})
		nodeUtils.EXPECT().IsPathExists(winTargetPath).Return(true)
		nodeUtils.EXPECT().IsNotMountPoint(winTargetPath).Return(true, nil)
		nodeUtils.EXPECT().RemoveFileOrDirectory(winTargetPath).Return(nil)

		_, err := service.NodeUnpublishVolume(context.TODO(), req)
		assertCode(t, err, codes.OK)
	})

	t.Run("missing target is idempotent", func(t *testing.T) {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()
		nodeUtils := mocks.NewMockNodeUtilsInterface(mockCtrl)
		service := newTestWindowsNodeService(nodeUtils, nil, &fakePowershell{})
		nodeUtils.EXPECT().IsPathExists(winTargetPath).Return(false)

		_, err := service.NodeUnpublishVolume(context.TODO(), req)
		assertCode(t, err, codes.OK)
	})
}

func TestWindowsNodeExpandVolume(t *testing.T) {
	disk := `{"Number":3,"UniqueId":"` + winVolumeUuid + `","PartitionStyle":"GPT","Size":21474836480}`
	req := &csi.NodeExpandVolumeRequest{VolumeId: winVolumeId, VolumePath: winStagingPath, VolumeCapability: winMountCap("ntfs")}

	t.Run("grows the partition", func(t *testing.T) {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()
		nodeUtils := mocks.NewMockNodeUtilsInterface(mockCtrl)
		ps := &fakePowershell{responses: []fakeResponse{
			{contains: "Get-Disk | Where-Object", out: disk},
			{contains: "Resize-Partition", out: "21458059264\r\n"},
		}}
		service := newTestWindowsNodeService(nodeUtils, nil, ps)
		nodeUtils.EXPECT().GetVolumeUuid(winVolumeId).Return(winVolumeUuid)

		resp, err := service.NodeExpandVolume(context.TODO(), req)
		assertCode(t, err, codes.OK)
		if resp.CapacityBytes != 21458059264 {
			t.Fatalf("unexpected capacity %d", resp.CapacityBytes)
		}
		if !strings.Contains(ps.last("Resize-Partition"), "$n = 3\n") {
			t.Fatalf("unexpected expand script: %s", ps.last("Resize-Partition"))
		}
	})

	t.Run("disk not present", func(t *testing.T) {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()
		nodeUtils := mocks.NewMockNodeUtilsInterface(mockCtrl)
		ps := &fakePowershell{responses: []fakeResponse{{contains: "Get-Disk | Where-Object", out: ""}}}
		service := newTestWindowsNodeService(nodeUtils, nil, ps)
		nodeUtils.EXPECT().GetVolumeUuid(winVolumeId).Return(winVolumeUuid)

		_, err := service.NodeExpandVolume(context.TODO(), req)
		assertCode(t, err, codes.NotFound)
	})
}

func TestWindowsNodeGetVolumeStats(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	nodeUtils := mocks.NewMockNodeUtilsInterface(mockCtrl)
	service := newTestWindowsNodeService(nodeUtils, nil, &fakePowershell{})
	nodeUtils.EXPECT().IsPathExists(winTargetPath).Return(true)
	nodeUtils.EXPECT().GetFileSystemVolumeStats(winTargetPath).Return(driver.VolumeStatistics{AvailableBytes: 1, TotalBytes: 3, UsedBytes: 2}, nil)

	resp, err := service.NodeGetVolumeStats(context.TODO(), &csi.NodeGetVolumeStatsRequest{VolumeId: winVolumeId, VolumePath: winTargetPath})
	assertCode(t, err, codes.OK)
	if len(resp.Usage) != 1 || resp.Usage[0].Total != 3 || resp.Usage[0].Available != 1 || resp.Usage[0].Used != 2 {
		t.Fatalf("unexpected usage %v", resp.Usage)
	}
}
