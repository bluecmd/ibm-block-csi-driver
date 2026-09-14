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
	"golang.org/x/sys/unix"
)

func (n NodeUtils) IsBlock(devicePath string) (bool, error) {
	var stat unix.Stat_t
	err := unix.Stat(devicePath, &stat)
	if err != nil {
		return false, err
	}
	return (stat.Mode & unix.S_IFMT) == unix.S_IFBLK, nil
}

func (d NodeUtils) GetFileSystemVolumeStats(path string) (VolumeStatistics, error) {
	statfs := &unix.Statfs_t{}
	err := unix.Statfs(path, statfs)
	if err != nil {
		return VolumeStatistics{}, err
	}

	availableBytes := int64(statfs.Bavail) * int64(statfs.Bsize)
	totalBytes := int64(statfs.Blocks) * int64(statfs.Bsize)
	usedBytes := (int64(statfs.Blocks) - int64(statfs.Bfree)) * int64(statfs.Bsize)

	totalInodes := int64(statfs.Files)
	availableInodes := int64(statfs.Ffree)
	usedInodes := totalInodes - availableInodes

	volumeStats := VolumeStatistics{
		AvailableBytes: availableBytes,
		TotalBytes:     totalBytes,
		UsedBytes:      usedBytes,

		AvailableInodes: availableInodes,
		TotalInodes:     totalInodes,
		UsedInodes:      usedInodes,
	}

	return volumeStats, nil
}
