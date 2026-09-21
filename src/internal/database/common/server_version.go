// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package common

import (
	"regexp"
	"strconv"
	"strings"
)

type ServerVersion struct {
	Engine  string `json:"engine"`
	Version string `json:"version"`
	Arch    string `json:"arch"`
	Raw     string `json:"raw"`
}

func (s ServerVersion) Display() string {
	out := strings.TrimSpace(s.Engine + " " + s.Version)
	if s.Arch != "" {
		out += " " + s.Arch
	}
	return out
}

var (
	numPrefixRe = regexp.MustCompile(`^(\d+(?:\.\d+)*)`)
	archRe      = regexp.MustCompile(`(?i)(x86_64|amd64|x64|aarch64|arm64|ppc64le|ppc64|s390x|s390|riscv64|i386|sparc64)`)
)

func ArchFromRaw(raw string) string {
	return NormalizeArch(archRe.FindString(raw))
}

func Major(version string) string {
	num := NumericPrefix(version)
	if i := strings.Index(num, "."); i >= 0 {
		return num[:i]
	}
	return num
}

func ServerCompatWarning(dumpEngine, dumpVersion, targetEngine, targetVersion string) string {
	if dumpEngine == "" || targetEngine == "" {
		return ""
	}
	if !strings.EqualFold(dumpEngine, targetEngine) {
		return "engine mismatch: backup is from " + dumpEngine + ", target is " + targetEngine
	}
	dm, tm := Major(dumpVersion), Major(targetVersion)
	if dm == "" || tm == "" {
		return ""
	}
	di, derr := strconv.Atoi(dm)
	ti, terr := strconv.Atoi(tm)
	if derr != nil || terr != nil {
		return ""
	}
	if ti < di {
		return "version downgrade: backup is from " + dumpEngine + " " + dumpVersion + ", target is " + targetVersion
	}
	return ""
}

func NumericPrefix(s string) string {
	return numPrefixRe.FindString(strings.TrimSpace(s))
}

func NormalizeArch(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "x86_64", "amd64", "x64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	default:
		return strings.ToLower(strings.TrimSpace(s))
	}
}
