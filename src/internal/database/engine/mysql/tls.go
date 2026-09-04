// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package mysql

import (
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"sync"

	mysql "github.com/go-sql-driver/mysql"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database/common"
)

var (
	tlsLock sync.Mutex
	tlsNum  int
)

func ConnectDSN(user, pass, host string, port int, params, tlsName string) string {
	hostPort := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	dsn := fmt.Sprintf("%s:%s@tcp(%s)/", user, pass, hostPort)
	if params != "" {
		dsn += "?" + params
	}
	if tlsName != "" {
		if strings.Contains(dsn, "?") {
			dsn += "&"
		} else {
			dsn += "?"
		}
		dsn += "tls=" + tlsName
	}
	return dsn
}

func RegisterTLS(tc *tls.Config) (string, error) {
	tlsLock.Lock()
	defer tlsLock.Unlock()
	tlsNum++
	name := fmt.Sprintf("dbbackup-%d", tlsNum)
	if err := mysql.RegisterTLSConfig(name, tc); err != nil {
		return "", fmt.Errorf("register tls config: %w", err)
	}
	return name, nil
}

func TLSNameFor(cfg *config.TLSConfig) string {
	if cfg == nil || !cfg.Enable {
		return ""
	}
	tc, err := common.BuildTLSConfig(cfg)
	if err != nil || tc == nil {
		return ""
	}
	name, err := RegisterTLS(tc)
	if err != nil {
		return ""
	}
	return name
}
