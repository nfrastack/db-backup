// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package influx

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
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
	dbName  string
	version int
	tlsCfg  *config.TLSConfig
	connCfg *config.ConnectivityConfig
	client  *http.Client
	ctx     context.Context
}

type influxQueryResult struct {
	Results []struct {
		Series []struct {
			Name    string   `json:"name"`
			Columns []string `json:"columns"`
			Values  [][]any  `json:"values"`
		} `json:"series"`
	} `json:"results"`
}

func (d *Dumper) Close() error { return nil }

func (d *Dumper) Dump(w io.Writer, dbNames []string) error {
	fmt.Fprintf(w, "// dbbackup InfluxDB dump\n")
	fmt.Fprintf(w, "// Host: %s:%d  Version: v%d\n//\n\n", d.host, d.port, d.Version())

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

func NewDumper(host string, port int, user, pass, dbName string, version int, tlsCfg ...*config.TLSConfig) *Dumper {
	if port == 0 {
		port = 8086
	}
	d := &Dumper{
		host:    host,
		port:    port,
		user:    user,
		pass:    pass,
		dbName:  dbName,
		version: version,
		client:  common.HTTPClient(nil, 30*time.Second),
	}
	if len(tlsCfg) > 0 && tlsCfg[0] != nil {
		d.tlsCfg = tlsCfg[0]
		d.client = common.HTTPClient(d.tlsCfg, 30*time.Second)
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
		req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s://%s/ping", d.scheme(), net.JoinHostPort(d.host, fmt.Sprintf("%d", d.port))), nil)
		if err != nil {
			return err
		}
		resp, err := d.client.Do(req)
		if err != nil {
			return fmt.Errorf("ping: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			return fmt.Errorf("ping: influx responded %d", resp.StatusCode)
		}
		if v := resp.Header.Get("X-Influxdb-Version"); v != "" && d.version == 0 {
			if n, ok := parseMajorVersion(v); ok {
				d.version = n
			}
		}
		return nil
	}
	connect := func() error { return ping() }
	return common.WithConnectivity(ctx, "influx", d.connCfg, probe, connect, ping)
}

func (d *Dumper) SetConnectivity(cfg *config.ConnectivityConfig) {
	if cfg != nil {
		d.connCfg = cfg
	}
}

func (d *Dumper) Version() int {
	if d.version == 0 {
		return 1
	}
	return d.version
}
func (d *Dumper) ctxOrBg() context.Context {
	if d.ctx != nil {
		return d.ctx
	}
	return context.Background()
}

// v2 InfluxQL compat: the org is a query param, not the user.

func (d *Dumper) dumpDatabase(w io.Writer, db string) error {
	if d.Version() == 1 {
		return d.dumpV1(w, db)
	}
	return d.dumpV2(w, db)
}

func (d *Dumper) dumpV1(w io.Writer, db string) error {
	fmt.Fprintf(w, "# INFLUXDB EXPORT: %s\n", db)
	fmt.Fprintf(w, "# DDL\n")
	fmt.Fprintf(w, "CREATE DATABASE %s\n\n", db)

	result, err := d.query(db, "SHOW MEASUREMENTS")
	if err != nil {
		return fmt.Errorf("list measurements: %w", err)
	}
	var measurements []string
	for _, r := range result.Results {
		for _, s := range r.Series {
			for _, v := range s.Values {
				if len(v) > 0 {
					if m, ok := v[0].(string); ok {
						measurements = append(measurements, m)
					}
				}
			}
		}
	}

	for _, m := range measurements {
		fmt.Fprintf(w, "\n# CONTEXT-DATABASE: %s\n# MEASUREMENT: %s\n", db, m)

		tagResult, err := d.query(db, fmt.Sprintf("SHOW TAG KEYS FROM %q", m))
		if err != nil {
			continue
		}
		tags := map[string]bool{}
		for _, r := range tagResult.Results {
			for _, s := range r.Series {
				for _, v := range s.Values {
					if len(v) > 0 {
						if k, ok := v[0].(string); ok {
							tags[k] = true
						}
					}
				}
			}
		}

		dataResult, err := d.query(db, fmt.Sprintf("SELECT * FROM %q", m))
		if err != nil {
			continue
		}

		for _, r := range dataResult.Results {
			for _, s := range r.Series {
				cols := s.Columns
				for _, row := range s.Values {
					var tagParts, fieldParts []string
					ts := ""
					for i, col := range cols {
						val := row[i]
						switch {
						case col == "time":
							if n, ok := val.(float64); ok {
								ts = strconv.FormatInt(int64(n), 10)
							} else if s, ok := val.(string); ok {
								ts = s
							}
						case tags[col]:
							if val != nil {
								tagParts = append(tagParts, escapeLineProtocolTag(col)+"="+escapeLineProtocolTag(fmt.Sprintf("%v", val)))
							}
						default:
							if val != nil {
								fieldParts = append(fieldParts, escapeLineProtocolTag(col)+"="+formatLineProtocolValue(val))
							}
						}
					}
					if len(fieldParts) == 0 {
						continue
					}
					line := m
					for _, tp := range tagParts {
						line += "," + tp
					}
					line += " " + strings.Join(fieldParts, ",")
					if ts != "" {
						line += " " + ts
					}
					fmt.Fprintln(w, line)
				}
			}
		}
	}
	return nil
}
func (d *Dumper) dumpV2(w io.Writer, bucket string) error {
	fmt.Fprintf(w, "# INFLUXDB V2 EXPORT: %s (org %s)\n", bucket, d.user)
	fmt.Fprintf(w, "# DDL\n")
	fmt.Fprintf(w, "CREATE DATABASE %s\n\n", bucket)

	result, err := d.query(bucket, "SHOW MEASUREMENTS")
	if err != nil {
		return fmt.Errorf("list measurements: %w", err)
	}
	var measurements []string
	for _, r := range result.Results {
		for _, s := range r.Series {
			for _, v := range s.Values {
				if len(v) > 0 {
					if m, ok := v[0].(string); ok {
						measurements = append(measurements, m)
					}
				}
			}
		}
	}

	for _, m := range measurements {
		fmt.Fprintf(w, "\n# CONTEXT-DATABASE: %s\n# MEASUREMENT: %s\n", bucket, m)

		tagResult, err := d.query(bucket, fmt.Sprintf("SHOW TAG KEYS FROM %q", m))
		if err != nil {
			continue
		}
		tags := map[string]bool{}
		for _, r := range tagResult.Results {
			for _, s := range r.Series {
				for _, v := range s.Values {
					if len(v) > 0 {
						if k, ok := v[0].(string); ok {
							tags[k] = true
						}
					}
				}
			}
		}

		dataResult, err := d.query(bucket, fmt.Sprintf("SELECT * FROM %q", m))
		if err != nil {
			continue
		}

		for _, r := range dataResult.Results {
			for _, s := range r.Series {
				cols := s.Columns
				for _, row := range s.Values {
					var tagParts, fieldParts []string
					ts := ""
					for i, col := range cols {
						val := row[i]
						switch {
						case col == "time":
							if n, ok := val.(float64); ok {
								ts = strconv.FormatInt(int64(n), 10)
							} else if s, ok := val.(string); ok {
								ts = s
							}
						case tags[col]:
							if val != nil {
								tagParts = append(tagParts, escapeLineProtocolTag(col)+"="+escapeLineProtocolTag(fmt.Sprintf("%v", val)))
							}
						default:
							if val != nil {
								fieldParts = append(fieldParts, escapeLineProtocolTag(col)+"="+formatLineProtocolValue(val))
							}
						}
					}
					if len(fieldParts) == 0 {
						continue
					}
					line := m
					for _, tp := range tagParts {
						line += "," + tp
					}
					line += " " + strings.Join(fieldParts, ",")
					if ts != "" {
						line += " " + ts
					}
					fmt.Fprintln(w, line)
				}
			}
		}
	}
	return nil
}
func escapeLineProtocolTag(s string) string {
	r := strings.NewReplacer(`\`, `\\`, " ", `\ `, ",", `\,`, "=", `\=`)
	return r.Replace(s)
}
func formatLineProtocolValue(v any) string {
	switch v := v.(type) {
	case bool:
		if v {
			return "true"
		}
		return "false"
	case float64:
		s := strconv.FormatFloat(v, 'f', -1, 64)
		if !strings.ContainsAny(s, ".eE") {
			s += ".0"
		}
		return s
	case string:
		r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, " ", `\ `, `,`, `\,`, "=", `\=`)
		return `"` + r.Replace(v) + `"`
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

func (d *Dumper) listDatabases() ([]string, error) {
	result, err := d.query("", "SHOW DATABASES")
	if err != nil {
		return nil, fmt.Errorf("list databases: %w", err)
	}
	var dbs []string
	for _, r := range result.Results {
		for _, s := range r.Series {
			for _, v := range s.Values {
				if len(v) > 0 {
					if name, ok := v[0].(string); ok && !strings.HasPrefix(name, "_") {
						dbs = append(dbs, name)
					}
				}
			}
		}
	}
	return dbs, nil
}
func parseMajorVersion(v string) (int, bool) {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	num := 0
	digits := 0
	for i := 0; i < len(v) && v[i] >= '0' && v[i] <= '9'; i++ {
		num = num*10 + int(v[i]-'0')
		digits++
		if num > 99 {
			return 0, false
		}
		if i+1 == len(v) || v[i+1] == '.' {
			break
		}
	}
	if digits == 0 || num < 1 || num > 2 {
		return 0, false
	}
	return num, true
}

func (d *Dumper) query(db, q string) (*influxQueryResult, error) {
	u := fmt.Sprintf("%s://%s/query?db=%s&epoch=ns&q=%s", d.scheme(), net.JoinHostPort(d.host, fmt.Sprintf("%d", d.port)), url.QueryEscape(db), url.QueryEscape(q))
	if d.Version() == 2 {

		u += "&org=" + url.QueryEscape(d.user)
	}
	req, err := http.NewRequestWithContext(d.ctxOrBg(), "GET", u, nil)
	if err != nil {
		return nil, err
	}
	d.setAuth(req)
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("influx query %q: HTTP %d: %s", q, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var result influxQueryResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("influx query %q: decode: %w", q, err)
	}
	return &result, nil
}
func (d *Dumper) scheme() string {
	if d.tlsCfg != nil {
		return "https"
	}
	return "http"
}

func (d *Dumper) setAuth(req *http.Request) {
	if d.Version() == 2 {
		if d.pass != "" {
			req.Header.Set("Authorization", "Token "+d.pass)
		}
		return
	}
	if d.user != "" {
		req.SetBasicAuth(d.user, d.pass)
	}
}
