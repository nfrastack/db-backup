// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package common

import "strings"

func ConnDB(dbName, fallback string) string {
	first := strings.Split(dbName, ",")[0]
	first = strings.TrimSpace(first)
	if strings.EqualFold(first, "all") || strings.HasPrefix(first, "__globals__") || first == "" {
		return fallback
	}
	return first
}
func ContainsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func DBNamesList(dbName string) []string {
	if dbName == "" {
		return nil
	}
	names := strings.Split(dbName, ",")
	for i := range names {
		names[i] = strings.TrimSpace(names[i])
	}
	return names
}

func EscapePGLit(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}
func FirstDBName(dbName string) string {
	name := strings.Split(dbName, ",")[0]
	name = strings.TrimSpace(name)
	if strings.EqualFold(name, "all") || name == "" {
		return ""
	}
	return name
}

func SplitRedisArgs(s string) []string {
	var parts []string
	var cur strings.Builder
	inQuote := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '"' {
			inQuote = !inQuote
			cur.WriteByte(c)
			continue
		}
		if c == ' ' && !inQuote {
			if cur.Len() > 0 {
				parts = append(parts, cur.String())
				cur.Reset()
			}
			continue
		}
		cur.WriteByte(c)
	}
	if cur.Len() > 0 {
		parts = append(parts, cur.String())
	}
	return parts
}

func SplitSQL(data string) []string {
	var stmts []string
	var cur strings.Builder
	for _, line := range strings.Split(data, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") {
			continue
		}
		cur.WriteString(line)
		cur.WriteByte('\n')
		if strings.HasSuffix(trimmed, ";") {
			stmts = append(stmts, cur.String())
			cur.Reset()
		}
	}
	if cur.Len() > 0 {
		stmts = append(stmts, cur.String())
	}
	return stmts
}
func UnquoteRedisArg(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	s = strings.ReplaceAll(s, "\\\"", "\"")
	s = strings.ReplaceAll(s, "\\\\", "\\")
	return s
}
