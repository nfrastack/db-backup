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
	fmt.Fprintf(w, "// db-backup CouchDB dump\n// Host: %s:%d\n//\n\n", d.host, d.port)

	if len(dbNames) == 1 && strings.ToLower(dbNames[0]) == "all" {
		names, err := d.listDatabases()
		if err != nil {
			return err
		}
		dbNames = names
	}

	for _, db := range dbNames {
		if err := d.dumpDatabase(w, db); err != nil {
			return fmt.Errorf("dump %s: %w", db, err)
		}
	}
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
			return fmt.Errorf("ping: couch responded %d", resp.StatusCode)
		}
		return nil
	}
	return common.WithConnectivity(ctx, "couch", d.connCfg, probe, func() error { return nil }, ping)
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

func (d *Dumper) dumpDatabase(w io.Writer, dbName string) error {
	u := fmt.Sprintf("%s/%s/_all_docs?include_docs=true", d.baseURL(), url.PathEscape(dbName))
	req, err := http.NewRequestWithContext(d.ctxOrBg(), "GET", u, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if d.user != "" {
		req.SetBasicAuth(d.user, d.pass)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", dbName, err)
	}
	defer resp.Body.Close()

	var result struct {
		Rows []struct {
			Doc json.RawMessage `json:"doc"`
		} `json:"rows"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode %s: %w", dbName, err)
	}

	fmt.Fprintf(w, "// Database: %s\n", dbName)
	for _, row := range result.Rows {
		var doc map[string]any
		if err := json.Unmarshal(row.Doc, &doc); err != nil {
			return fmt.Errorf("unmarshal doc in %s: %w", dbName, err)
		}
		b, err := json.Marshal(doc)
		if err != nil {
			return fmt.Errorf("marshal doc in %s: %w", dbName, err)
		}
		fmt.Fprintf(w, "db.%s.insert(%s)\n", dbName, string(b))
	}
	fmt.Fprintf(w, "\n")
	return nil
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
		if !strings.HasPrefix(db, "_") {
			filtered = append(filtered, db)
		}
	}
	return filtered, nil
}
func (d *Dumper) scheme() string {
	if d.tlsCfg != nil && d.tlsCfg.Enable {
		return "https"
	}
	return "http"
}
