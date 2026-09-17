// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package version

import (
	"regexp"
	"strings"
)

var (
	BuildDate = "unknown"
	Channel = ""
	Commit = ""
	Version = "dev"
)

var (
	commitHRe   = regexp.MustCompile(`(?i)-h([0-9a-f]{7,})$`)
	devCommitRe = regexp.MustCompile(`(?i)^dev-([0-9a-f]{7,})(?:-dirty)?$`)
	tagDevRe    = regexp.MustCompile(`(?i)-dev-([0-9a-f]{7,})(?:-dirty)?$`)
)

func Apply(version, channel, commit, buildDate string) {
	Version = version
	Channel = channel
	Commit = commit
	BuildDate = buildDate
}

func Display() string {
	v := Version
	if c := ResolveCommit(v); c != "" && !strings.Contains(v, c) {
		v += " (commit " + c + ")"
	}
	return v
}

func ResolveCommit(version string) string {
	if Commit != "" {
		return Commit
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
