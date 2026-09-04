// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

var overrideWarned sync.Map

func ReadConfigBytes(path string) ([]byte, error) {
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	return expandIncludes(b, path, map[string]bool{})
}
func expandIncludes(data []byte, path string, inProgress map[string]bool) ([]byte, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	if inProgress[abs] {
		return nil, fmt.Errorf("circular config include: %s", path)
	}
	inProgress[abs] = true
	defer delete(inProgress, abs)

	var root map[string]any
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parsing config %q: %w", path, err)
	}
	incVal, hasInclude := root["include"]
	if !hasInclude || incVal == nil {
		return data, nil
	}

	baseDir := filepath.Dir(path)
	incPaths, err := resolveIncludePaths(baseDir, incVal)
	if err != nil {
		return nil, err
	}
	for _, incPath := range incPaths {
		incData, err := os.ReadFile(incPath)
		if err != nil {
			return nil, fmt.Errorf("reading included config %s: %w", incPath, err)
		}
		merged, err := expandIncludes(incData, incPath, inProgress)
		if err != nil {
			return nil, err
		}
		var incMap map[string]any
		if err := yaml.Unmarshal(merged, &incMap); err != nil {
			return nil, fmt.Errorf("parsing included config %s: %w", incPath, err)
		}
		mergeYAML(root, incMap, path, incPath)
	}
	delete(root, "include")
	out, err := yaml.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("merging config %s: %w", path, err)
	}
	return out, nil
}

func hasGlobMeta(s string) bool {
	return strings.ContainsAny(s, "*?[")
}
func loadMerged(paths []string) ([]byte, error) {
	var effective []string
	for _, p := range paths {
		if p != "" {
			effective = append(effective, p)
		}
	}
	if len(effective) == 0 {
		return nil, nil
	}
	if len(effective) == 1 {
		return ReadConfigBytes(effective[0])
	}
	var merged map[string]any
	for _, p := range effective {
		b, err := ReadConfigBytes(p)
		if err != nil {
			return nil, err
		}
		var m map[string]any
		if err := yaml.Unmarshal(b, &m); err != nil {
			return nil, fmt.Errorf("parsing config %q: %w", p, err)
		}
		if merged == nil {
			merged = m
			continue
		}
		mergeYAML(merged, m, effective[0], p)
	}
	return yaml.Marshal(merged)
}

func mergeYAML(dst, src map[string]any, dstPath, srcPath string) {
	for k, sv := range src {
		dv, exists := dst[k]
		if !exists {
			dst[k] = sv
			continue
		}
		dm, dok := dv.(map[string]any)
		sm, sok := sv.(map[string]any)
		if dok && sok {
			mergeYAML(dm, sm, dstPath, srcPath)
			continue
		}
		dl, dlok := dv.([]any)
		sl, slok := sv.([]any)
		if dlok && slok {
			dst[k] = append(dl, sl...)
			continue
		}
		key := k + "\x00" + dstPath + "\x00" + srcPath
		if _, dup := overrideWarned.LoadOrStore(key, true); !dup {
			fmt.Fprintf(os.Stderr, "WARN: config option %q in %s is overridden by %s\n", k, dstPath, srcPath)
		}
		dst[k] = sv
	}
}

func resolveIncludePath(baseDir, p string) (string, error) {
	switch {
	case strings.HasPrefix(p, "env://"):
		name := strings.TrimPrefix(p, "env://")
		v := os.Getenv(name)
		if v == "" {
			return "", fmt.Errorf("config include %q: environment variable %s is empty", p, name)
		}
		p = v
	case strings.HasPrefix(p, "file://"):
		p = strings.TrimPrefix(p, "file://")
	}
	if filepath.IsAbs(p) {
		return p, nil
	}
	return filepath.Join(baseDir, p), nil
}
func resolveIncludePaths(baseDir string, incVal any) ([]string, error) {
	var entries []string
	switch v := incVal.(type) {
	case string:
		entries = []string{v}
	case []any:
		for _, e := range v {
			s, ok := e.(string)
			if !ok {
				return nil, fmt.Errorf("include entries must be strings (got %T)", e)
			}
			entries = append(entries, s)
		}
	case nil:
		return nil, nil
	default:
		return nil, fmt.Errorf("include must be a string or list of strings (got %T)", incVal)
	}

	var out []string
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		resolved, err := resolveIncludePath(baseDir, e)
		if err != nil {
			return nil, err
		}
		if hasGlobMeta(resolved) {
			matches, err := filepath.Glob(resolved)
			if err != nil {
				return nil, fmt.Errorf("config include %q: %w", e, err)
			}
			sort.Strings(matches)
			if len(matches) == 0 {
				fmt.Fprintf(os.Stderr, "WARN: config include %q matched no files\n", e)
				continue
			}
			out = append(out, matches...)
			continue
		}
		out = append(out, resolved)
	}
	return out, nil
}
