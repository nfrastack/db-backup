// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package storage

const TempLowSpaceFloor = 1 << 30 // 1 GiB

type TempFSInfo struct {
	Tmpfs     bool
	FreeBytes uint64
}

func InspectSpoolFS() TempFSInfo {
	info, err := inspectSpoolFS()
	if err != nil {
		return TempFSInfo{}
	}
	return info
}
