// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package couch

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database/common"
)

func ListDatabases(host string, port int, user, pass string, tlsCfg *config.TLSConfig) ([]string, error) {
	d := NewDumper(host, port, user, pass, tlsCfg)
	return d.listDatabases()
}

func Maintain() ([]common.OpResult, error) {
	return nil, fmt.Errorf("maintenance not supported for CouchDB")
}

func Restore(r io.Reader, host string, port int, user, pass, dbName, authSource string, tlsCfg *config.TLSConfig) error {
	if strings.TrimSpace(dbName) == "" || strings.EqualFold(dbName, "all") {
		return fmt.Errorf("couchdb restore requires a single target --name (got %q)", dbName)
	}

	d := NewDumper(host, port, user, pass, tlsCfg)
	if err := d.OpenContext(context.Background()); err != nil {
		return err
	}
	defer d.Close()

	if err := d.ensureDatabase(dbName); err != nil {
		return err
	}

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	var docs []json.RawMessage
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		idx := strings.Index(line, ".insert(")
		if idx < 0 {
			continue
		}

		if !strings.HasPrefix(line, "db.") || !strings.HasSuffix(line, ")") {
			continue
		}
		jsonStart := idx + len(".insert(")
		raw := strings.TrimSpace(line[jsonStart : len(line)-1])
		if !json.Valid([]byte(raw)) {
			continue
		}
		cleaned := stripRev(raw)
		docs = append(docs, json.RawMessage(cleaned))
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read dump: %w", err)
	}

	if len(docs) == 0 {
		return nil
	}

	const chunk = 500
	for start := 0; start < len(docs); start += chunk {
		end := start + chunk
		if end > len(docs) {
			end = len(docs)
		}
		if err := d.bulkInsert(dbName, docs[start:end]); err != nil {
			return err
		}
	}
	return nil
}

// 412: database already exists. Acceptable.

func (d *Dumper) bulkInsert(dbName string, docs []json.RawMessage) error {
	u := fmt.Sprintf("http://%s:%d/%s/_bulk_docs", d.host, d.port, url.PathEscape(dbName))
	body, err := json.Marshal(map[string]json.RawMessage{"docs": mergeRawArray(docs)})
	if err != nil {
		return fmt.Errorf("encode bulk_docs: %w", err)
	}
	req, err := http.NewRequestWithContext(d.ctxOrBg(), "POST", u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if d.user != "" {
		req.SetBasicAuth(d.user, d.pass)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("bulk_docs %s: %w", dbName, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("bulk_docs %s: HTTP %d: %s", dbName, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}
func (d *Dumper) ensureDatabase(dbName string) error {
	u := fmt.Sprintf("http://%s:%d/%s", d.host, d.port, url.PathEscape(dbName))
	req, err := http.NewRequestWithContext(d.ctxOrBg(), "PUT", u, nil)
	if err != nil {
		return err
	}
	if d.user != "" {
		req.SetBasicAuth(d.user, d.pass)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("create %s: %w", dbName, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 201 || resp.StatusCode == 202 || resp.StatusCode == 412 {

		return nil
	}
	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("create %s: HTTP %d: %s", dbName, resp.StatusCode, strings.TrimSpace(string(body)))
}

func mergeRawArray(parts []json.RawMessage) json.RawMessage {
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, p := range parts {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(p)
	}
	buf.WriteByte(']')
	return json.RawMessage(buf.Bytes())
}
func stripRev(raw string) string {
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return raw
	}
	delete(doc, "_rev")
	b, err := json.Marshal(doc)
	if err != nil {
		return raw
	}
	return string(b)
}
