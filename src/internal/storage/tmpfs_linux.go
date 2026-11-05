// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package storage

import (
	"golang.org/x/sys/unix"
)

func inspectSpoolFS() (TempFSInfo, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(SpoolDir(), &st); err != nil {
		return TempFSInfo{}, err
	}
	return TempFSInfo{
		Tmpfs:     st.Type == unix.TMPFS_MAGIC,
		FreeBytes: uint64(st.Bavail) * uint64(st.Bsize),
	}, nil
}
