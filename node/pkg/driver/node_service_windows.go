//go:build windows

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

import (
	"context"
	"fmt"
	"strings"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/ibm/ibm-block-csi-driver/node/logger"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	mount "k8s.io/mount-utils"
)

const (
	iscsiInitiatorServiceMsg = "MSiSCSI"
)

// WindowsNodeService is the node service for Windows worker nodes. It runs
// as a HostProcess container, so it talks to the host's iSCSI initiator and
// storage stack directly through PowerShell instead of through the chroot
// wrapper the Linux service uses.
//
// Volume staging and publishing are not implemented yet; the embedded
// UnimplementedNodeServer answers those calls with codes.Unimplemented.
type WindowsNodeService struct {
	csi.UnimplementedNodeServer

	ConfigYaml ConfigFile
	Hostname   string
	NodeUtils  NodeUtilsInterface
	powershell PowershellRunner
}

func newNodeService(configFile ConfigFile, hostname string, max_invocations int, clean_scsi_device bool) (csi.NodeServer, error) {
	executer := &executer.Executer{}
	nodeUtils := NewNodeUtils(executer, mount.New(""), configFile, nil)

	service := &WindowsNodeService{
		ConfigYaml: configFile,
		Hostname:   hostname,
		NodeUtils:  nodeUtils,
		powershell: &Powershell{},
	}
	if err := service.ensureIscsiInitiatorRunning(); err != nil {
		logger.Warningf("iSCSI initiator service is not available: %v", err)
	}
	return service, nil
}

// runPowershell runs a script with the host's Windows PowerShell and returns
// its standard output. Only stdout is captured: when PowerShell has no
// console, as in a HostProcess container, it serialises progress records as
// CLIXML on stderr, which would otherwise pollute the result.
func (d *WindowsNodeService) runPowershell(script string) (string, error) {
	return d.powershell.Run(script)
}

// ensureIscsiInitiatorRunning starts the Microsoft iSCSI initiator service
// and makes it start automatically, matching what iscsid provides on Linux.
func (d *WindowsNodeService) ensureIscsiInitiatorRunning() error {
	script := fmt.Sprintf("Set-Service -Name %s -StartupType Automatic; Start-Service -Name %s",
		iscsiInitiatorServiceMsg, iscsiInitiatorServiceMsg)
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
