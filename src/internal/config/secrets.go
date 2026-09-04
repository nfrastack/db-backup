// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"fmt"
	"os"
	"strings"
)

func MaskPassword(s string) string {
	if s == "" {
		return ""
	}
	if len(s) < 6 {
		return "****"
	}
	visible := 2
	if len(s) > 12 {
		visible = 3
	}
	if visible*2 > len(s)/2 {
		visible = 2
	}
	return s[:visible] + strings.Repeat("*", len(s)-visible*2) + s[len(s)-visible:]
}

func ResolveSecret(val string) string {
	if strings.HasPrefix(val, "file://") {
		path := val[7:]
		if path == "" {
			fmt.Fprintf(os.Stderr, "WARN: empty file:// secret path\n")
			return val
		}
		if fi, err := os.Stat(path); err == nil && fi.Size() > 64*1024 {
			fmt.Fprintf(os.Stderr, "WARN: secret file %q too large (%d bytes, limit 64 KiB)\n", path, fi.Size())
			return val
		}
		b, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "WARN: read secret file %q: %v\n", path, err)
			return val
		}
		if len(b) > 64*1024 {
			fmt.Fprintf(os.Stderr, "WARN: secret file %q too large (%d bytes, limit 64 KiB)\n", path, len(b))
			return val
		}
		return strings.TrimSpace(string(b))
	}
	if strings.HasPrefix(val, "env://") {
		key := val[6:]
		if key == "" {
			fmt.Fprintf(os.Stderr, "WARN: empty env:// secret key\n")
			return val
		}
		v := os.Getenv(key)
		if v == "" {
			fmt.Fprintf(os.Stderr, "WARN: env var %q not set for secret\n", key)
			return val
		}
		v = strings.TrimSpace(v)
		if strings.HasPrefix(v, "file://") || strings.HasPrefix(v, "env://") {
			return ResolveSecret(v)
		}
		return v
	}
	return val
}
