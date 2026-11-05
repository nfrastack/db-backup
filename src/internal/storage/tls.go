// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package storage

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"os"
	"strings"
	"time"
)

func HasTLSOpts(opts map[string]string) bool {
	for _, k := range []string{"tls_ca_file", "tls_cert_file", "tls_key_file", "tls_verify"} {
		if opts[k] != "" {
			return true
		}
	}
	return false
}

func TLSHTTPClient(opts map[string]string) *http.Client {
	return &http.Client{Timeout: 3600 * time.Second, Transport: tlsTransport(opts)}
}

func tlsTransport(opts map[string]string) *http.Transport {
	tr := &http.Transport{}
	if ca := opts["tls_ca_file"]; ca != "" {
		if pem, err := os.ReadFile(ca); err == nil {
			pool := x509.NewCertPool()
			if pool.AppendCertsFromPEM(pem) {
				tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
			}
		}
	}
	if tr.TLSClientConfig == nil {
		tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	if certFile := opts["tls_cert_file"]; certFile != "" {
		if keyFile := opts["tls_key_file"]; keyFile != "" {
			if cert, err := tls.LoadX509KeyPair(certFile, keyFile); err == nil {
				tr.TLSClientConfig.Certificates = []tls.Certificate{cert}
			}
		}
	}
	if strings.EqualFold(opts["tls_verify"], "false") {
		tr.TLSClientConfig.InsecureSkipVerify = true
	}
	return tr
}
