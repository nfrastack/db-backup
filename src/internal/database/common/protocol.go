// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package common

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

type backupModeKey struct{}

// BackupModeFromContext returns the requested backup mode ("", if unset).
func BackupModeFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(backupModeKey{}).(string); ok {
		return v
	}
	return ""
}

var (
	protocolMu sync.Mutex
	protocols  = map[string]string{}
)

func ProtocolKey(host string, port int, user, db string) string {
	var parts []string
	for _, n := range strings.Split(db, ",") {
		if n = strings.TrimSpace(n); n != "" {
			parts = append(parts, n)
		}
	}
	return fmt.Sprintf("%s|%d|%s|%s",
		strings.TrimSpace(host), port, strings.TrimSpace(user),
		strings.Join(parts, ","))
}

func RecordBackupProtocol(key, protocol string) {
	if key == "" || protocol == "" {
		return
	}
	protocolMu.Lock()
	defer protocolMu.Unlock()
	if len(protocols) > 128 {
		clear(protocols)
	}
	protocols[key] = protocol
}

func TakeBackupProtocol(key string) string {
	protocolMu.Lock()
	defer protocolMu.Unlock()
	p := protocols[key]
	delete(protocols, key)
	return p
}

func WithBackupMode(ctx context.Context, mode string) context.Context {
	return context.WithValue(ctx, backupModeKey{}, mode)
}
