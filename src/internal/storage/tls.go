// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package storage

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

func HasTLSOpts(opts map[string]string) bool {
	for _, k := range []string{"tls_ca_file", "tls_cert_file", "tls_key_file", "tls_verify", "tls_version"} {
		if opts[k] != "" {
			return true
		}
	}
	return false
}

func TLSHTTPClient(opts map[string]string) (*http.Client, error) {
	tr, err := tlsTransport(opts)
	if err != nil {
		return nil, err
	}
	return &http.Client{Timeout: 3600 * time.Second, Transport: tr}, nil
}

func tlsTransport(opts map[string]string) (*http.Transport, error) {
	tc := &tls.Config{MinVersion: tls.VersionTLS12}

	switch strings.ToUpper(strings.TrimSpace(opts["tls_version"])) {
	case "", "TLS1.2", "TLSV1.2":
	case "TLS1.3", "TLSV1.3":
		tc.MinVersion = tls.VersionTLS13
	default:
		return nil, fmt.Errorf("tls: unsupported version %q", opts["tls_version"])
	}

	switch strings.ToLower(strings.TrimSpace(opts["tls_verify"])) {
	case "", "true":
	case "false":
		tc.InsecureSkipVerify = true
	default:
		return nil, fmt.Errorf("tls: invalid verify value %q (want true or false)", opts["tls_verify"])
	}

	if ca := opts["tls_ca_file"]; ca != "" {
		pem, err := os.ReadFile(ca)
		if err != nil {
			return nil, fmt.Errorf("tls: read CA file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("tls: no valid CA certificates in %s", ca)
		}
		tc.RootCAs = pool
	}

	certFile := opts["tls_cert_file"]
	keyFile := opts["tls_key_file"]
	if (certFile == "") != (keyFile == "") {
		return nil, fmt.Errorf("tls: cert_file and key_file must be configured together")
	}
	if certFile != "" {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("tls: load client certificate: %w", err)
		}
		tc.Certificates = []tls.Certificate{cert}
	}

	return &http.Transport{TLSClientConfig: tc}, nil
}
