//go:build linux

/**
 * Copyright 2019 IBM Corp.
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
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
	mountwrapper "github.com/ibm/ibm-block-csi-driver/node/pkg/driver/mount"
	mount "k8s.io/mount-utils"
	"k8s.io/utils/exec"
)

// newNodeService wires up the Linux node service: SCSI/NVMe device
// connectivity through host tools reached via the chroot wrapper, and
// mounting from inside the container via /host.
func newNodeService(configFile ConfigFile, hostname string, max_invocations int, clean_scsi_device bool) (csi.NodeServer, error) {
	mounter := &mount.SafeFormatAndMount{
		Interface: mountwrapper.New(""),
		Exec:      exec.New(),
	}
	syncLock := NewSyncLock(max_invocations, clean_scsi_device)
	executer := &executer.Executer{}
	osDeviceConnectivityMapping := map[string]device_connectivity.OsDeviceConnectivityInterface{
		configFile.Connectivity_type.Nvme_over_fc: device_connectivity.NewOsDeviceConnectivityNvmeOFc(executer, clean_scsi_device),
		configFile.Connectivity_type.Fc:           device_connectivity.NewOsDeviceConnectivityFc(executer, clean_scsi_device),
		configFile.Connectivity_type.Iscsi:        device_connectivity.NewOsDeviceConnectivityIscsi(executer, clean_scsi_device),
	}
	osDeviceConnectivityHelper := device_connectivity.NewOsDeviceConnectivityHelperScsiGeneric(executer, clean_scsi_device)
	nodeService := NewNodeService(configFile, hostname, *NewNodeUtils(executer, mounter, configFile, osDeviceConnectivityHelper),
		osDeviceConnectivityMapping, osDeviceConnectivityHelper, executer, mounter, syncLock)
	return &nodeService, nil
}
