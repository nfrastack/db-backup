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
	"github.com/nfrastack/db-backup/internal/log"
	"time"
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
	restoreStart := time.Now()
	log.Debug("couch", "restore start",
		"host", host, "port", port, "scheme", d.scheme(),
		"auth", d.authMode(), "database", dbName)

	if err := d.ensureDatabase(dbName); err != nil {
		return err
	}

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	const restoreBatchSize = 500
	var docs []json.RawMessage
	var batches, totalDocs, skipped int
	flush := func() error {
		if len(docs) == 0 {
			return nil
		}
		batches++
		log.Trace("couch", "restore batch",
			"database", dbName, "batch", batches, "docs", len(docs))
		if err := d.bulkInsert(dbName, docs); err != nil {
			return err
		}
		totalDocs += len(docs)
		docs = docs[:0]
		return nil
	}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		idx := strings.Index(line, ".insert(")
		if idx < 0 {
			skipped++
			log.Trace("couch", "skipping unrecognized line", "line", line)
			continue
		}

		if !strings.HasPrefix(line, "db.") || !strings.HasSuffix(line, ")") {
			skipped++
			log.Trace("couch", "skipping unrecognized line", "line", line)
			continue
		}
		jsonStart := idx + len(".insert(")
		raw := strings.TrimSpace(line[jsonStart : len(line)-1])
		if !json.Valid([]byte(raw)) {
			skipped++
			log.Warn("couch", "skipping invalid doc", "database", dbName)
			continue
		}
		cleaned := stripRev(raw)
		docs = append(docs, json.RawMessage(cleaned))
		if len(docs) >= restoreBatchSize {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read dump: %w", err)
	}
	if err := flush(); err != nil {
		return err
	}
	log.Debug("couch", "restore done",
		"database", dbName, "docs", totalDocs, "batches", batches,
		"skipped", skipped,
		"elapsed", time.Since(restoreStart).Round(time.Millisecond).String())
	if skipped > 0 {
		return fmt.Errorf("couchdb: restore incomplete - %d lines skipped", skipped)
	}
	return nil
}

func (d *Dumper) bulkInsert(dbName string, docs []json.RawMessage) error {
	u := d.baseURL() + "/" + url.PathEscape(dbName) + "/_bulk_docs"
	body, err := json.Marshal(map[string]json.RawMessage{"docs": mergeRawArray(docs)})
	if err != nil {
		return fmt.Errorf("encode bulk_docs: %w", err)
	}
	start := time.Now()
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
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 300 {
		log.Trace("couch", "bulk_docs failed",
			"database", dbName, "docs", len(docs), "status", resp.StatusCode,
			"latency", time.Since(start).Round(time.Millisecond).String(),
			"body", strings.TrimSpace(string(respBody)))
		return fmt.Errorf("bulk_docs %s: HTTP %d: %s", dbName, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	log.Trace("couch", "bulk_docs ok",
		"database", dbName, "docs", len(docs), "status", resp.StatusCode,
		"latency", time.Since(start).Round(time.Millisecond).String())
	return nil
}
func (d *Dumper) ensureDatabase(dbName string) error {
	u := d.baseURL() + "/" + url.PathEscape(dbName)
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
		log.Debug("couch", "database ensured", "database", dbName, "status", resp.StatusCode)
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
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
