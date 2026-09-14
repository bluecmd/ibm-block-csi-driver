//go:build !linux

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

package mount

import (
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
	mount "k8s.io/mount-utils"
)

// New returns the platform mounter from k8s.io/mount-utils. The Linux build
// wraps it to mount from inside the container via /host; other platforms
// run directly on the host and need no such wrapper.
func New(mounterPath string) mount.Interface {
	return mount.New(mounterPath)
}

// NewWithExecutor is provided for API parity with the Linux build. The
// executer is unused on this platform.
func NewWithExecutor(mounterPath string, _ executer.ExecuterInterface) mount.Interface {
	return mount.New(mounterPath)
}
