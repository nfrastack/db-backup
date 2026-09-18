// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"regexp"
	"strings"

	versionPkg "github.com/nfrastack/db-backup/internal/version"
)

var (
	buildChannel = ""
	buildCommit  = ""
)

var betaRe = regexp.MustCompile(`(b|rc)\d+$`)

func bannerLine() string {
	s := fmt.Sprintf("db-backup %s | build=%s mode=%s", Version, buildEdition, runtimeMode())
	if c := displayCommit(); !strings.Contains(Version, c) {
		s += " commit=" + c
	}
	return s + " | © 2026 Nfrastack https://nfrastack.com"
}

func displayCommit() string {
	if c := resolveCommit(Version); c != "" {
		return c
	}
	return "unknown"
}

func resolveChannel(version string) string {
	if buildChannel != "" {
		switch strings.ToLower(buildChannel) {
		case "stable", "beta", "edge":
			return strings.ToLower(buildChannel)
		}
	}
	lower := strings.ToLower(version)
	if strings.HasPrefix(lower, "dev-") || strings.HasPrefix(lower, "dev+") ||
		strings.HasPrefix(lower, "dev_") || lower == "dev" {
		return "edge"
	}
	if strings.HasPrefix(lower, "develop-") || strings.HasPrefix(lower, "develop+") ||
		strings.HasPrefix(lower, "develop_") {
		return "edge"
	}
	if strings.Contains(version, "-g") || strings.Contains(version, "-dev") ||
		strings.Contains(version, "+") {
		return "edge"
	}
	if betaRe.MatchString(version) {
		return "beta"
	}
	return "stable"
}

func resolveCommit(version string) string {
	if buildCommit != "" {
		return buildCommit
	}
	return versionPkg.ResolveCommit(version)
}
