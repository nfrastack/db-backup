// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"regexp"
	"strings"
)

var (
	buildChannel = ""
	buildCommit  = ""
)

var betaRe = regexp.MustCompile(`(b|rc)\d+$`)

func resolveChannel(version string) string {
	if buildChannel != "" {
		switch strings.ToLower(buildChannel) {
		case "stable", "beta", "edge":
			return strings.ToLower(buildChannel)
		}
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
	if i := strings.Index(version, "-g"); i >= 0 {
		sha := version[i+2:]
		sha = strings.TrimSuffix(sha, "-dirty")
		if sha != "" {
			return sha
		}
	}
	return ""
}
