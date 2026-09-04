// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"strings"

	"github.com/nfrastack/db-backup/internal/config"
)

func expandAllInclude(discovered []string, list *config.DatabaseList) []string {
	var exclude []string
	var include []string
	if list != nil {
		exclude = list.Exclude
		include = list.Include
	}
	seen := map[string]bool{}
	var out []string
	add := func(name string) {
		key := strings.ToLower(name)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, name)
	}
	excl := &config.DatabaseList{Exclude: exclude}
	for _, name := range discovered {
		if excl.Excluded(name) {
			continue
		}
		add(name)
	}
	for _, inc := range include {
		if strings.EqualFold(strings.TrimSpace(inc), "ALL") {
			continue
		}
		if inc != "" {
			add(inc)
		}
	}
	return out
}

func hasAllToken(include []string) bool {
	for _, inc := range include {
		if strings.EqualFold(strings.TrimSpace(inc), "ALL") {
			return true
		}
	}
	return false
}
