// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	maxSecretFileSize = 64 * 1024
	maxSecretDepth    = 8
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

func ResolveSecret(val string) (string, error) {
	return resolveSecret(val, 0, map[string]bool{})
}

func resolveSecretField(dst *string, field string) error {
	v, err := ResolveSecret(*dst)
	if err != nil {
		return fmt.Errorf("%s: %w", field, err)
	}
	*dst = v
	return nil
}

func resolveSecret(val string, depth int, seen map[string]bool) (string, error) {
	if depth >= maxSecretDepth {
		return "", fmt.Errorf("secret reference nesting exceeds %d levels", maxSecretDepth)
	}

	if strings.HasPrefix(val, "file://") {
		path := strings.TrimPrefix(val, "file://")
		if path == "" {
			return "", fmt.Errorf("file:// secret path is empty")
		}
		return readSecretFile(path)
	}

	if strings.HasPrefix(val, "env://") {
		key := strings.TrimSpace(strings.TrimPrefix(val, "env://"))
		if key == "" {
			return "", fmt.Errorf("env:// secret key is empty")
		}
		if seen[key] {
			return "", fmt.Errorf("cyclic secret reference via environment variable %q", key)
		}
		v, ok := os.LookupEnv(key)
		if !ok || strings.TrimSpace(v) == "" {
			return "", fmt.Errorf("environment variable %q is empty or unset", key)
		}
		v = strings.TrimSpace(v)
		if strings.HasPrefix(v, "file://") || strings.HasPrefix(v, "env://") {
			seen[key] = true
			defer delete(seen, key)
			return resolveSecret(v, depth+1, seen)
		}
		return v, nil
	}

	return val, nil
}

func readSecretFile(path string) (string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("read secret file %q: %w", path, err)
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("secret file %q is not a regular file", path)
	}
	if fi.Size() > maxSecretFileSize {
		return "", fmt.Errorf("secret file %q exceeds %d-byte limit", path, maxSecretFileSize)
	}

	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("read secret file %q: %w", path, err)
	}
	defer f.Close()
	fi, err = f.Stat()
	if err != nil {
		return "", fmt.Errorf("read secret file %q: %w", path, err)
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("secret file %q is not a regular file", path)
	}
	if fi.Size() > maxSecretFileSize {
		return "", fmt.Errorf("secret file %q exceeds %d-byte limit", path, maxSecretFileSize)
	}

	b, err := io.ReadAll(io.LimitReader(f, maxSecretFileSize+1))
	if err != nil {
		return "", fmt.Errorf("read secret file %q: %w", path, err)
	}
	if len(b) > maxSecretFileSize {
		return "", fmt.Errorf("secret file %q exceeds %d-byte limit", path, maxSecretFileSize)
	}
	v := strings.TrimSpace(string(b))
	if v == "" {
		return "", fmt.Errorf("secret file %q is empty", path)
	}
	return v, nil
}
