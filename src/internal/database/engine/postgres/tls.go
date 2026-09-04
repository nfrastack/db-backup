// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package postgres

import (
	"fmt"
	"net"
	"net/url"

	"github.com/nfrastack/db-backup/internal/config"
)

func ConnStr(user, pass, host string, port int, db string, tlsCfg *config.TLSConfig) string {
	hostPort := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	return fmt.Sprintf("postgres://%s:%s@%s/%s?%s", url.QueryEscape(user), url.QueryEscape(pass), hostPort, url.PathEscape(db), SSLParams(tlsCfg))
}
func SSLParams(tlsCfg *config.TLSConfig) string {
	if tlsCfg == nil || !tlsCfg.Enable {
		return "sslmode=disable"
	}
	if !tlsCfg.VerifyCerts() {
		return "sslmode=require"
	}
	p := "sslmode=verify-full"
	if tlsCfg.CAFile != "" {
		p += "&sslrootcert=" + tlsCfg.CAFile
	}
	if tlsCfg.CertFile != "" {
		p += "&sslcert=" + tlsCfg.CertFile
	}
	if tlsCfg.KeyFile != "" {
		p += "&sslkey=" + tlsCfg.KeyFile
	}
	return p
}
