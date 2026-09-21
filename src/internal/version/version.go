// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package version

import (
	"regexp"
	"strings"
	"time"
)

const StaleDevBuildDays = 15

var (
	BuildDate = "unknown"
	Channel   = ""
	Commit    = ""
	Version   = "dev"
)

var (
	commitHRe   = regexp.MustCompile(`(?i)-h([0-9a-f]{7,})$`)
	devCommitRe = regexp.MustCompile(`(?i)^(?:dev|develop)-([0-9a-f]{7,})(?:-dirty)?$`)
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

func IsEdgeBuild() bool {
	if Channel != "" {
		switch strings.ToLower(Channel) {
		case "edge":
			return true
		case "stable", "beta":
			return false
		}
	}
	lower := strings.ToLower(Version)
	if strings.HasPrefix(lower, "dev-") || strings.HasPrefix(lower, "dev+") ||
		strings.HasPrefix(lower, "dev_") || lower == "dev" {
		return true
	}
	if strings.HasPrefix(lower, "develop-") || strings.HasPrefix(lower, "develop+") ||
		strings.HasPrefix(lower, "develop_") {
		return true
	}
	return strings.Contains(Version, "-g") || strings.Contains(Version, "-dev") ||
		strings.Contains(Version, "+")
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

func StaleDevBuild(now time.Time) (bool, int) {
	if !IsEdgeBuild() {
		return false, 0
	}
	built, err := time.Parse(time.RFC3339, BuildDate)
	if err != nil {
		built, err = time.Parse("2006-01-02", BuildDate)
		if err != nil {
			return false, 0
		}
	}
	age := int(now.Sub(built).Hours() / 24)
	if age <= StaleDevBuildDays {
		return false, age
	}
	return true, age
}

