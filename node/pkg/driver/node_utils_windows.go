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
	"golang.org/x/sys/windows"
)

// IsBlock always reports false: raw block volumes are not supported by the
// Windows kubelet, so no path handed to the node service is a block device.
func (n NodeUtils) IsBlock(devicePath string) (bool, error) {
	return false, nil
}

// GetFileSystemVolumeStats reports capacity for the volume mounted at path.
// NTFS has no inode concept, so the inode counters are left at zero.
func (d NodeUtils) GetFileSystemVolumeStats(path string) (VolumeStatistics, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return VolumeStatistics{}, err
	}

	var freeBytesAvailable, totalBytes, totalFreeBytes uint64
	err = windows.GetDiskFreeSpaceEx(pathPtr, &freeBytesAvailable, &totalBytes, &totalFreeBytes)
	if err != nil {
		return VolumeStatistics{}, err
	}

	return VolumeStatistics{
		AvailableBytes: int64(freeBytesAvailable),
		TotalBytes:     int64(totalBytes),
		UsedBytes:      int64(totalBytes - totalFreeBytes),
	}, nil
}
