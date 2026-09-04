// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package mssql

import (
	"fmt"
	"net"
	"net/url"

	"github.com/nfrastack/db-backup/internal/config"
)

func ConnStr(user, pass, host string, port int, db string, tlsCfg *config.TLSConfig) string {
	hostPort := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	if tlsCfg == nil || !tlsCfg.Enable {
		return fmt.Sprintf("sqlserver://%s:%s@%s?database=%s&encrypt=disable", url.QueryEscape(user), url.QueryEscape(pass), hostPort, url.QueryEscape(db))
	}
	p := fmt.Sprintf("sqlserver://%s:%s@%s?database=%s&encrypt=true", url.QueryEscape(user), url.QueryEscape(pass), hostPort, url.QueryEscape(db))
	if !tlsCfg.VerifyCerts() {
		p += "&TrustServerCertificate=true"
	} else if tlsCfg.CAFile != "" {
		p += "&certificate=" + tlsCfg.CAFile
	}
	return p
}
