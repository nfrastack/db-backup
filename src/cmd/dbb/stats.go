// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/nfrastack/db-backup/internal/stats"
)

var (
	statsMgr     *stats.Manager
	statsTracker *stats.Tracker
)

// `dbb stats` command
func cmdStats(args []string) int {
	file := ""
	format := "text"
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--file" || args[i] == "-file":
			if i+1 < len(args) {
				file = args[i+1]
				i++
			}
		case args[i] == "--format" || args[i] == "-format":
			if i+1 < len(args) {
				format = args[i+1]
				i++
			}
		case args[i] == "--format=json" || args[i] == "-format=json":
			format = "json"
		default:
			if file == "" && !strings.HasPrefix(args[i], "-") {
				file = args[i]
			}
		}
	}
	return statsCmd(file, format)
}

func statsCmd(file, format string) int {
	if file == "" {
		var err error
		file, err = stats.LatestDump()
		if err != nil {
			fmt.Fprintf(os.Stderr, "stats: %v\n", err)
			return 1
		}
	}
	data, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "stats: %v\n", err)
		return 1
	}
	decoded, err := stats.DecodePayload(strings.TrimSpace(string(data)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "stats: %v\n", err)
		return 1
	}
	if format == "json" {
		b, err := decoded.JSON()
		if err != nil {
			fmt.Fprintf(os.Stderr, "stats: %v\n", err)
			return 1
		}
		fmt.Println(string(b))
		return 0
	}
	fmt.Print(decoded.String())
	return 0
}

// shared build options version, image tag (opt), license, channel, commit, runtime, log settings
func statsOpts() stats.Options {
	licenseID := editionLicenseID()
	return stats.BuildOptions(Version, stats.ImageVersion(), licenseID, resolveChannel(Version), resolveCommit(Version),
		stats.RuntimeInfo{Container: globalContainer, Systemd: globalSystemd},
		stats.LogOptions{Level: globalLogLevel, Format: globalLogFormat, Type: globalLogType})
}
