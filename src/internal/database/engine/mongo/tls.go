// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package mongo

import (
	"fmt"
	"net"
	"net/url"

	"github.com/nfrastack/db-backup/internal/config"
)

func URI(user, pass, host string, port int, authSource string, tlsCfg *config.TLSConfig) string {
	auth := ""
	if user != "" && pass != "" {
		auth = fmt.Sprintf("%s:%s@", url.QueryEscape(user), url.QueryEscape(pass))
	}
	hostPort := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	uri := fmt.Sprintf("mongodb://%s%s/?directConnection=true", auth, hostPort)
	if authSource != "" {
		uri += "&authSource=" + url.QueryEscape(authSource)
	}
	if tlsCfg != nil && tlsCfg.Enable {
		uri += "&tls=true"
		if !tlsCfg.VerifyCerts() {
			uri += "&tlsInsecure=true"
		}
		if tlsCfg.CAFile != "" {
			uri += "&tlsCAFile=" + tlsCfg.CAFile
		}
		if tlsCfg.CertFile != "" && tlsCfg.KeyFile != "" {
			uri += "&tlsCertificateKeyFile=" + tlsCfg.CertFile
		}
	}
	return uri
}
