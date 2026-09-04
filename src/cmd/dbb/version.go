// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/nfrastack/db-backup/internal/stats"
)

func cmdVersion(args []string) int {
	format := "text"
	filtered := args
	if len(args) > 0 && strings.EqualFold(args[0], "check") {
		filtered = args[1:]
	}
	for i := 0; i < len(filtered); i++ {
		switch filtered[i] {
		case "--format", "-format":
			if i+1 < len(filtered) {
				format = filtered[i+1]
				i++
			}
		case "--format=json", "-format=json":
			format = "json"
		}
	}
	if len(args) > 0 && strings.EqualFold(args[0], "check") {
		return versionCheckCmd(format)
	}
	if format == "json" {
		type versionJSON struct {
			Binary    string `json:"binary"`
			Version   string `json:"version"`
			BuildDate string `json:"build_date"`
			GOOS      string `json:"goos"`
			GOARCH    string `json:"goarch"`
			Edition   string `json:"edition"`
			Mode      string `json:"mode"`
			License   string `json:"license"`
		}
		out := versionJSON{
			Binary:    "db-backup",
			Version:   Version,
			BuildDate: buildDate,
			GOOS:      runtime.GOOS,
			GOARCH:    runtime.GOARCH,
			Edition:   buildEdition,
			Mode:      runtimeMode(),
			License:   licenseLabel(),
		}
		if note := runtimeNote(); note != "" {
			out.License = licenseLabel() + " (" + note + ")"
		}
		b, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "rendering version: %v\n", err)
			return 1
		}
		fmt.Println(string(b))
		return 0
	}
	fmt.Printf("db-backup %s (built %s; %s/%s)\n", Version, buildDate, runtime.GOOS, runtime.GOARCH)
	fmt.Printf("build:   %s\n", buildEdition)
	fmt.Printf("mode:    %s\n", runtimeMode())
	if note := runtimeNote(); note != "" {
		fmt.Printf("note:    %s\n", note)
	}
	fmt.Printf("license: %s\n", licenseLabel())
	return 0
}

func runVersionCheck() (*stats.VersionResponse, error) {
	ctx := context.Background()
	if statsMgr != nil {
		return statsMgr.CheckVersionNow(ctx, Version, statsOpts())
	}
	client := stats.NewClient("", stats.SharedKey())
	return client.CheckVersion(ctx, stats.CheckPayload(stats.ToolDBBackup, Version, statsOpts()))
}

func versionCheckCmd(format string) int {
	resp, err := runVersionCheck()
	if err != nil {
		fmt.Fprintf(os.Stderr, "version check failed: %s\n", stats.DescribeError(err))
		return 1
	}
	if resp == nil || resp.Latest == "" {
		fmt.Println("No new version information.")
		return 0
	}
	reportLicenseVerdict(resp.LicenseRevoked)
	if format == "json" {
		b, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "rendering response: %v\n", err)
			return 1
		}
		fmt.Println(string(b))
		return 0
	}
	maybePrintLicenseWarning(resp.LicenseRevoked)
	if !stats.IsNewer(Version, resp.Latest) {
		fmt.Printf("Already running the latest version (%s). Server reports: %s\n", Version, resp.Latest)
	} else {
		fmt.Printf("New version available: %s (released %s)\n", resp.Latest, resp.DateReleased)
		if resp.Critical {
			fmt.Println("This release is marked critical. Upgrade is strongly recommended.")
		}
		if resp.DownloadURL != "" {
			fmt.Printf("Download: %s\n", resp.DownloadURL)
		}
		if resp.ChangelogURL != "" {
			fmt.Printf("Changelog: %s\n", resp.ChangelogURL)
		}
	}
	if img := stats.ImageVersion(); stats.IsImageStale(img, resp.ImageLatest) {
		fmt.Printf("New container image available: %s (running %s)\n", resp.ImageLatest, img)
	}
	return 0
}
