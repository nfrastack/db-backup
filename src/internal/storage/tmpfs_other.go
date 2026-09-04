// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !linux

package storage

func inspectSpoolFS() (TempFSInfo, error) {
	return TempFSInfo{}, nil
}
