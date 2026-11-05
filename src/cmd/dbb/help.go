// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import "flag"

func cmdHelp(args []string) int {
	flag.Usage()
	return 0
}
