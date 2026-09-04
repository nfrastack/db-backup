// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package common

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/nfrastack/db-backup/internal/config"
)

func BuildTLSConfig(cfg *config.TLSConfig) (*tls.Config, error) {
	if cfg == nil || !cfg.Enable {
		return nil, nil
	}
	tc := &tls.Config{MinVersion: tls.VersionTLS12}

	if cfg.CAFile != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read CA file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no valid CA certificates in %s", cfg.CAFile)
		}
		tc.RootCAs = pool
	}

	if cfg.CertFile != "" && cfg.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("load client certificate: %w", err)
		}
		tc.Certificates = []tls.Certificate{cert}
	}

	tc.InsecureSkipVerify = !cfg.VerifyCerts()

	switch strings.ToUpper(cfg.Version) {
	case "TLS1.3", "TLSV1.3":
		tc.MinVersion = tls.VersionTLS13
		tc.MaxVersion = tls.VersionTLS13
	case "TLS1.2", "TLSV1.2", "":
	default:
		return nil, fmt.Errorf("unsupported TLS version %q", cfg.Version)
	}

	return tc, nil
}

func HTTPClient(tlsCfg *config.TLSConfig, timeout time.Duration) *http.Client {
	tr := &http.Transport{}
	if tc, err := BuildTLSConfig(tlsCfg); err == nil && tc != nil {
		tr.TLSClientConfig = tc
	}
	return &http.Client{Timeout: timeout, Transport: tr}
}
