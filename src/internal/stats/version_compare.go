// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package stats

import (
	"strconv"
	"strings"
)

func IsImageStale(running, available string) bool {
	if running == "" || available == "" {
		return false
	}
	return IsNewer(running, available)
}

func IsNewer(current, candidate string) bool {
	curCore, curTag, curOK := splitVersion(current)
	candCore, candTag, candOK := splitVersion(candidate)
	if !curOK || !candOK {
		return !strings.EqualFold(strings.TrimSpace(current), strings.TrimSpace(candidate))
	}
	switch compareCores(curCore, candCore) {
	case 1:
		return false
	case -1:
		return true
	}
	switch {
	case curTag == "" && candTag != "":
		return false
	case curTag != "" && candTag == "":
		return true
	default:
		return candTag > curTag
	}
}

func compareCores(a, b string) int {
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		av, bv := 0, 0
		if i < len(as) {
			av, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			bv, _ = strconv.Atoi(bs[i])
		}
		// equal cores: plain release beats any prerelease
		switch {
		case av < bv:
			return -1
		case av > bv:
			return 1
		}
	}
	return 0
}

func splitVersion(v string) (core, tag string, ok bool) {
	v = strings.ToLower(strings.TrimSpace(v))
	cut := len(v)
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c != '.' && (c < '0' || c > '9') {
			cut = i
			break
		}
	}
	core, tag = v[:cut], v[cut:]
	if core == "" || strings.HasPrefix(core, ".") || strings.HasSuffix(core, ".") || strings.Contains(core, "..") {
		return "", "", false
	}
	for _, seg := range strings.Split(core, ".") {
		if _, err := strconv.Atoi(seg); err != nil {
			return "", "", false
		}
	}
	return core, tag, true
}
