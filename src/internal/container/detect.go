// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package container

import (
	"os"
	"runtime"
	"strings"
)

func BuildLogCandidates(getenv func(string) string) []string {
	var paths []string
	if img := getenv("IMAGE_NAME"); img != "" {
		paths = append(paths, "/container/build/"+strings.ReplaceAll(img, "/", "_")+"/build.log")
	}
	return paths
}

func detect(getenv func(string) string, stat func(string) (os.FileInfo, error)) (bool, string) {
	if runtime.GOOS != "linux" {
		return false, ""
	}
	if _, err := stat("/.dockerenv"); err == nil {
		return true, "/.dockerenv"
	}
	if _, err := stat("/run/.containerenv"); err == nil {
		return true, "/run/.containerenv"
	}
	if img := getenv("IMAGE_NAME"); strings.Contains(img, "nfrastack/") {
		return true, "IMAGE_NAME"
	}
	if _, err := stat("/container/build"); err == nil {
		return true, "/container/build"
	}
	if _, err := stat("/container/data/db-backup"); err == nil {
		return true, "/container/data/db-backup"
	}
	for _, p := range BuildLogCandidates(getenv) {
		if _, err := stat(p); err == nil {
			return true, p
		}
	}
	return false, ""
}

func Detect() (bool, string) {
	return detect(os.Getenv, os.Stat)
}
