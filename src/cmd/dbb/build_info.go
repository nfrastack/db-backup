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

var (
	commitHRe   = regexp.MustCompile(`(?i)-h([0-9a-f]{7,})$`)
	devCommitRe = regexp.MustCompile(`(?i)^dev-([0-9a-f]{7,})(?:-dirty)?$`)
	tagDevRe    = regexp.MustCompile(`(?i)-dev-([0-9a-f]{7,})(?:-dirty)?$`)
)

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
	trimmed := strings.TrimSuffix(version, "-dirty")
	if m := commitHRe.FindStringSubmatch(trimmed); m != nil {
		return m[1]
	}
	if m := devCommitRe.FindStringSubmatch(trimmed); m != nil {
		return m[1]
	}
	if m := tagDevRe.FindStringSubmatch(trimmed); m != nil {
		return m[1]
	}
	return ""
}
