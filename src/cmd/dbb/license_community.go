// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build community

package main

import (
	"fmt"
	"os"
)

func cmdLicense(args []string) int {
	fmt.Fprintln(os.Stderr, "The license command requires the Supported build.")
	fmt.Fprintln(os.Stderr, "Community builds contain no license tooling whatsoever.")
	fmt.Fprintln(os.Stderr, "See https://nfrastack.com/db-backup")
	return 1
}

func setupLicenseWatch() {}
