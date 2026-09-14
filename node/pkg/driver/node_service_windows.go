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
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/ibm/ibm-block-csi-driver/node/logger"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
	mount "k8s.io/mount-utils"
)

// newNodeService wires up the Windows node service, see WindowsNodeService.
func newNodeService(configFile ConfigFile, hostname string, max_invocations int, clean_scsi_device bool) (csi.NodeServer, error) {
	executer := &executer.Executer{}
	mounter := mount.New("")
	nodeUtils := NewNodeUtils(executer, mounter, configFile, nil)
	syncLock := NewSyncLock(max_invocations, clean_scsi_device)

	service := NewWindowsNodeService(configFile, hostname, nodeUtils, mounter, syncLock, &Powershell{})
	if err := service.ensureIscsiInitiatorRunning(); err != nil {
		logger.Warningf("iSCSI initiator service is not available: %v", err)
	}
	return service, nil
}
