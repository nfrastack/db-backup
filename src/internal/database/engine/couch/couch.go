// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package couch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database/common"
	"github.com/nfrastack/db-backup/internal/log"
)

type Dumper struct {
	host    string
	port    int
	user    string
	pass    string
	client  *http.Client
	tlsCfg  *config.TLSConfig
	connCfg *config.ConnectivityConfig
	ctx     context.Context
}

func (d *Dumper) Close() error { return nil }

func (d *Dumper) Dump(w io.Writer, dbNames []string) error {
	start := time.Now()
	log.Debug("couch", "backup start",
		"host", d.host, "port", d.port, "scheme", d.scheme(),
		"auth", d.authMode(),
		"databases", strings.Join(dbNames, ","))
	fmt.Fprintf(w, "// db-backup CouchDB dump\n// Host: %s:%d\n//\n\n", d.host, d.port)

	if len(dbNames) == 1 && strings.ToLower(dbNames[0]) == "all" {
		names, err := d.listDatabases()
		if err != nil {
			log.Debug("couch", "expand ALL failed", "host", d.host, "error", err.Error())
			return err
		}
		dbNames = names
		log.Debug("couch", "expanded ALL", "count", len(dbNames), "databases", strings.Join(dbNames, ","))
	}
	if len(dbNames) == 0 {
		return fmt.Errorf("couchdb: no databases to back up")
	}

	var totalDocs int
	for _, db := range dbNames {
		common.TraceTable(d.ctxOrBg(), db, "")
		n, err := d.dumpDatabase(w, db)
		if err != nil {
			return fmt.Errorf("dump %s: %w", db, err)
		}
		totalDocs += n
	}
	log.Debug("couch", "backup done",
		"databases", len(dbNames), "docs", totalDocs,
		"elapsed", time.Since(start).Round(time.Millisecond).String())
	return nil
}
func NewDumper(host string, port int, user, pass string, tlsCfg ...*config.TLSConfig) *Dumper {
	if port == 0 {
		port = 5984
	}
	d := &Dumper{
		host:   host,
		port:   port,
		user:   user,
		pass:   pass,
		client: common.HTTPClient(nil, 30*time.Second),
	}
	if len(tlsCfg) > 0 && tlsCfg[0] != nil {
		d.tlsCfg = tlsCfg[0]
		d.client = common.HTTPClient(tlsCfg[0], 30*time.Second)
	}
	return d
}

func (d *Dumper) Open() error {
	return d.OpenContext(context.Background())
}

func (d *Dumper) OpenContext(ctx context.Context) error {
	d.ctx = ctx
	log.Debug("couch", "connect start",
		"host", d.host, "port", d.port, "scheme", d.scheme(),
		"auth", d.authMode())
	probe := func() error { return common.TCPDial(d.host, d.port) }
	ping := func() error {
		req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s://%s/", d.scheme(), net.JoinHostPort(d.host, fmt.Sprintf("%d", d.port))), nil)
		if err != nil {
			return err
		}
		resp, err := d.client.Do(req)
		if err != nil {
			return fmt.Errorf("ping: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			log.Trace("couch", "ping failed",
				"host", d.host, "port", d.port, "status", resp.StatusCode)
			return fmt.Errorf("ping: couch responded %d", resp.StatusCode)
		}
		log.Debug("couch", "connected",
			"host", d.host, "port", d.port, "scheme", d.scheme(),
			"auth", d.authMode())
		return nil
	}
	return common.WithConnectivity(ctx, "couch", d.connCfg, probe, func() error { return nil }, ping)
}

func (d *Dumper) authMode() string {
	if d.user == "" {
		return "none"
	}
	return "basic"
}

func (d *Dumper) SetConnectivity(cfg *config.ConnectivityConfig) {
	if cfg != nil {
		d.connCfg = cfg
	}
}
func (d *Dumper) baseURL() string {
	return fmt.Sprintf("%s://%s", d.scheme(), net.JoinHostPort(d.host, fmt.Sprintf("%d", d.port)))
}

func (d *Dumper) ctxOrBg() context.Context {
	if d.ctx != nil {
		return d.ctx
	}
	return context.Background()
}

const couchPageSize = 1000

func (d *Dumper) dumpDatabase(w io.Writer, dbName string) (int, error) {
	start := time.Now()
	fmt.Fprintf(w, "// Database: %s\n", dbName)
	var total int
	for {
		n, err := d.dumpPage(w, dbName, total)
		if err != nil {
			return total, err
		}
		total += n
		if n < couchPageSize {
			break
		}
	}
	log.Debug("couch", "database done",
		"database", dbName, "docs", total,
		"elapsed", time.Since(start).Round(time.Millisecond).String())
	fmt.Fprintf(w, "\n")
	return total, nil
}

func (d *Dumper) dumpPage(w io.Writer, dbName string, skip int) (int, error) {
	u := fmt.Sprintf("%s/%s/_all_docs?include_docs=true&limit=%d&skip=%d", d.baseURL(), url.PathEscape(dbName), couchPageSize, skip)
	start := time.Now()
	req, err := http.NewRequestWithContext(d.ctxOrBg(), "GET", u, nil)
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	if d.user != "" {
		req.SetBasicAuth(d.user, d.pass)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("fetch %s: %w", dbName, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		log.Trace("couch", "fetch failed",
			"database", dbName, "status", resp.StatusCode,
			"latency", time.Since(start).Round(time.Millisecond).String(),
			"body", strings.TrimSpace(string(body)))
		return 0, fmt.Errorf("fetch %s: HTTP %d: %s", dbName, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var result struct {
		Rows []struct {
			Doc json.RawMessage `json:"doc"`
		} `json:"rows"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("decode %s: %w", dbName, err)
	}

	for _, row := range result.Rows {
		var doc map[string]any
		if err := json.Unmarshal(row.Doc, &doc); err != nil {
			return 0, fmt.Errorf("unmarshal doc in %s: %w", dbName, err)
		}
		b, err := json.Marshal(doc)
		if err != nil {
			return 0, fmt.Errorf("marshal doc in %s: %w", dbName, err)
		}
		fmt.Fprintf(w, "db.%s.insert(%s)\n", dbName, string(b))
	}
	log.Trace("couch", "page dumped",
		"database", dbName, "docs", len(result.Rows), "skip", skip,
		"latency", time.Since(start).Round(time.Millisecond).String())
	return len(result.Rows), nil
}

func (d *Dumper) listDatabases() ([]string, error) {
	u := fmt.Sprintf("%s/_all_dbs", d.baseURL())
	req, err := http.NewRequestWithContext(d.ctxOrBg(), "GET", u, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if d.user != "" {
		req.SetBasicAuth(d.user, d.pass)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list databases: %w", err)
	}
	defer resp.Body.Close()

	var dbs []string
	if err := json.NewDecoder(resp.Body).Decode(&dbs); err != nil {
		return nil, fmt.Errorf("decode databases: %w", err)
	}

	var filtered []string
	for _, db := range dbs {
		if strings.HasPrefix(db, "_") {
			log.Trace("couch", "skipping system database", "database", db)
			continue
		}
		filtered = append(filtered, db)
	}
	log.Trace("couch", "databases listed", "count", len(filtered))
	return filtered, nil
}
func (d *Dumper) scheme() string {
	if d.tlsCfg != nil && d.tlsCfg.Enable {
		return "https"
	}
	return "http"
}
