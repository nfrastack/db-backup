// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package influx

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
	"time"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database/common"
	"github.com/nfrastack/db-backup/internal/log"
)

func ListDatabases(host string, port int, user, pass, authSource string, tlsCfg *config.TLSConfig) ([]string, error) {
	_ = authSource
	d := NewDumper(host, port, user, pass, "", 0, tlsCfg)
	d.SetConnectivity(&config.ConnectivityConfig{
		Enabled:       true,
		Method:        config.MethodFull,
		RetryInterval: 2,
		Timeout:       30,
	})
	if err := d.OpenContext(context.Background()); err != nil {
		return nil, err
	}
	defer d.Close()
	return d.listDatabases()
}

func Maintain() ([]common.OpResult, error) {
	return nil, fmt.Errorf("maintenance not supported for influx")
}

func Restore(r io.Reader, host string, port int, user, pass, dbName, authSource string, tlsCfg *config.TLSConfig) error {
	if strings.TrimSpace(dbName) == "" || strings.EqualFold(dbName, "all") {
		return fmt.Errorf("influx restore requires a single target --name (got %q)", dbName)
	}

	d := NewDumper(host, port, user, pass, dbName, 0, tlsCfg)
	d.SetConnectivity(&config.ConnectivityConfig{
		Enabled:       true,
		Method:        config.MethodFull,
		RetryInterval: 2,
		Timeout:       30,
	})
	if err := d.OpenContext(context.Background()); err != nil {
		return err
	}
	defer d.Close()
	restoreStart := time.Now()
	log.Debug("influx", "restore start",
		"host", host, "port", port, "scheme", d.scheme(),
		"auth", d.authMode(), "version", fmt.Sprintf("v%d", d.Version()),
		"bucket", dbName)

	peek := make([]byte, 512)
	n, err := io.ReadFull(r, peek)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return fmt.Errorf("read dump: %w", err)
	}
	r = io.MultiReader(bytes.NewReader(peek[:n]), r)
	if isTarStream(peek[:n]) {
		log.Debug("influx", "tar stream detected - physical restore",
			"bucket", dbName)
		if err := d.restorePhysical(r, dbName); err != nil {
			return err
		}
		log.Debug("influx", "restore done",
			"bucket", dbName,
			"elapsed", time.Since(restoreStart).Round(time.Millisecond).String())
		return nil
	}

	if d.Version() == 1 {
		if err := d.execV1Query("CREATE DATABASE \"" + dbName + "\""); err != nil {
			if !strings.Contains(err.Error(), "already exists") {
				return fmt.Errorf("create database %s: %w", dbName, err)
			}
		}
	} else {
		if err := d.createV2BucketIfMissing(dbName); err != nil {
			return fmt.Errorf("create bucket %s: %w", dbName, err)
		}
		if err := d.ensureDBRPMapping(dbName); err != nil {
			return fmt.Errorf("create dbrp mapping %s: %w", dbName, err)
		}
	}

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	const restoreBatchLines = 5000
	var block strings.Builder
	var blockLines, totalLines, batches int
	flush := func() error {
		if block.Len() == 0 {
			return nil
		}
		batches++
		log.Trace("influx", "restore batch", "bucket", dbName, "batch", batches, "lines", blockLines)
		if err := d.writeLines(dbName, block.String()); err != nil {
			return err
		}
		totalLines += blockLines
		block.Reset()
		blockLines = 0
		return nil
	}

	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "//") || trimmed == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(strings.ToUpper(trimmed), "CREATE DATABASE") {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		block.WriteString(line)
		block.WriteByte('\n')
		blockLines++
		if blockLines >= restoreBatchLines {
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
	log.Debug("influx", "restore done",
		"bucket", dbName, "lines", totalLines, "batches", batches,
		"elapsed", time.Since(restoreStart).Round(time.Millisecond).String())
	return nil
}

func (d *Dumper) createV2BucketIfMissing(name string) error {
	u := d.baseURL() + "/api/v2/buckets?name=" + url.QueryEscape(name)
	start := time.Now()
	req, err := http.NewRequestWithContext(d.ctxOrBg(), "GET", u, nil)
	if err != nil {
		return err
	}
	d.setAuth(req)
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		log.Trace("influx", "list buckets failed",
			"url", u, "status", resp.StatusCode,
			"latency", time.Since(start).Round(time.Millisecond).String(),
			"body", strings.TrimSpace(string(body)))
		return fmt.Errorf("list buckets: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var bucketsResp struct {
		Buckets []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"buckets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&bucketsResp); err != nil {
		return fmt.Errorf("decode buckets: %w", err)
	}
	log.Trace("influx", "list buckets ok",
		"url", u, "status", resp.StatusCode,
		"latency", time.Since(start).Round(time.Millisecond).String(),
		"count", len(bucketsResp.Buckets))
	for _, b := range bucketsResp.Buckets {
		if b.Name == name {
			log.Debug("influx", "bucket exists", "bucket", name, "id", b.ID)
			return nil
		}
	}

	orgsURL := d.baseURL() + "/api/v2/orgs"
	orgReq, err := http.NewRequestWithContext(d.ctxOrBg(), "GET", orgsURL, nil)
	if err != nil {
		return err
	}
	d.setAuth(orgReq)
	orgResp, err := d.client.Do(orgReq)
	if err != nil {
		return err
	}
	defer orgResp.Body.Close()
	if orgResp.StatusCode < 200 || orgResp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(orgResp.Body, 4096))
		return fmt.Errorf("list orgs: HTTP %d: %s", orgResp.StatusCode, strings.TrimSpace(string(body)))
	}
	var orgsResp struct {
		Orgs []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"orgs"`
	}
	if err := json.NewDecoder(orgResp.Body).Decode(&orgsResp); err != nil {
		return fmt.Errorf("decode orgs: %w", err)
	}
	var orgID string
	for _, o := range orgsResp.Orgs {
		if o.Name == d.user {
			orgID = o.ID
			break
		}
	}
	if orgID == "" {
		return fmt.Errorf("organization %q not found", d.user)
	}
	log.Trace("influx", "org resolved", "org", d.user, "org_id", orgID)

	body := []byte(fmt.Sprintf(`{"name":%q,"orgID":%q}`, name, orgID))
	createReq, err := http.NewRequestWithContext(d.ctxOrBg(), "POST", d.baseURL()+"/api/v2/buckets", bytes.NewReader(body))
	if err != nil {
		return err
	}
	createReq.Header.Set("Content-Type", "application/json")
	d.setAuth(createReq)
	createResp, err := d.client.Do(createReq)
	if err != nil {
		return err
	}
	defer createResp.Body.Close()
	if createResp.StatusCode == 409 || (createResp.StatusCode >= 200 && createResp.StatusCode < 300) {
		log.Debug("influx", "bucket ensured", "bucket", name, "status", createResp.StatusCode)
		return nil
	}
	respBody, _ := io.ReadAll(io.LimitReader(createResp.Body, 4096))
	return fmt.Errorf("HTTP %d: %s", createResp.StatusCode, strings.TrimSpace(string(respBody)))
}

func (d *Dumper) ensureDBRPMapping(name string) error {
	u := d.baseURL() + "/api/v2/buckets?name=" + url.QueryEscape(name)
	req, err := http.NewRequestWithContext(d.ctxOrBg(), "GET", u, nil)
	if err != nil {
		return err
	}
	d.setAuth(req)
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		log.Trace("influx", "list buckets failed",
			"url", u, "status", resp.StatusCode,
			"body", strings.TrimSpace(string(body)))
		return fmt.Errorf("list buckets: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var bucketsResp struct {
		Buckets []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"buckets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&bucketsResp); err != nil {
		return fmt.Errorf("decode buckets: %w", err)
	}
	var bucketID string
	for _, b := range bucketsResp.Buckets {
		if b.Name == name {
			bucketID = b.ID
			break
		}
	}
	if bucketID == "" {
		return fmt.Errorf("bucket %q not found", name)
	}

	orgsURL := d.baseURL() + "/api/v2/orgs"
	orgReq, err := http.NewRequestWithContext(d.ctxOrBg(), "GET", orgsURL, nil)
	if err != nil {
		return err
	}
	d.setAuth(orgReq)
	orgResp, err := d.client.Do(orgReq)
	if err != nil {
		return err
	}
	defer orgResp.Body.Close()
	if orgResp.StatusCode < 200 || orgResp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(orgResp.Body, 4096))
		log.Trace("influx", "list orgs failed",
			"url", orgsURL, "status", orgResp.StatusCode,
			"body", strings.TrimSpace(string(body)))
		return fmt.Errorf("list orgs: HTTP %d: %s", orgResp.StatusCode, strings.TrimSpace(string(body)))
	}
	var orgsResp struct {
		Orgs []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"orgs"`
	}
	if err := json.NewDecoder(orgResp.Body).Decode(&orgsResp); err != nil {
		return fmt.Errorf("decode orgs: %w", err)
	}
	var orgID string
	for _, o := range orgsResp.Orgs {
		if o.Name == d.user {
			orgID = o.ID
			break
		}
	}
	if orgID == "" {
		return fmt.Errorf("organization %q not found", d.user)
	}

	dbrpURL := d.baseURL() + "/api/v2/dbrps?orgID=" + orgID + "&db=" + url.QueryEscape(name) + "&bucketID=" + bucketID
	log.Trace("influx", "dbrp check", "bucket", name, "org_id", orgID, "bucket_id", bucketID)
	checkReq, err := http.NewRequestWithContext(d.ctxOrBg(), "GET", dbrpURL, nil)
	if err != nil {
		return err
	}
	d.setAuth(checkReq)
	checkResp, err := d.client.Do(checkReq)
	if err != nil {
		return err
	}
	defer checkResp.Body.Close()
	if checkResp.StatusCode < 200 || checkResp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(checkResp.Body, 4096))
		return fmt.Errorf("list dbrps: HTTP %d: %s", checkResp.StatusCode, strings.TrimSpace(string(body)))
	}
	var dbrpsResp struct {
		Content []struct {
			ID       string `json:"id"`
			Database string `json:"database"`
		} `json:"content"`
	}
	if err := json.NewDecoder(checkResp.Body).Decode(&dbrpsResp); err != nil {
		return fmt.Errorf("decode dbrps: %w", err)
	}
	for _, m := range dbrpsResp.Content {
		if m.Database == name {
			return nil
		}
	}

	body := []byte(fmt.Sprintf(`{"database":%q,"orgID":%q,"bucketID":%q,"retention_policy":"autogen","default":true}`, name, orgID, bucketID))
	createReq, err := http.NewRequestWithContext(d.ctxOrBg(), "POST", d.baseURL()+"/api/v2/dbrps", bytes.NewReader(body))
	if err != nil {
		return err
	}
	createReq.Header.Set("Content-Type", "application/json")
	d.setAuth(createReq)
	createResp, err := d.client.Do(createReq)
	if err != nil {
		return err
	}
	defer createResp.Body.Close()
	if createResp.StatusCode >= 200 && createResp.StatusCode < 300 {
		log.Debug("influx", "dbrp mapping ensured", "bucket", name, "status", createResp.StatusCode)
		return nil
	}
	respBody, _ := io.ReadAll(io.LimitReader(createResp.Body, 4096))
	return fmt.Errorf("HTTP %d: %s", createResp.StatusCode, strings.TrimSpace(string(respBody)))
}
func (d *Dumper) execV1Query(q string) error {
	u := d.baseURL() + "/query?q=" + url.QueryEscape(q)
	start := time.Now()
	req, err := http.NewRequestWithContext(d.ctxOrBg(), "GET", u, nil)
	if err != nil {
		return err
	}
	d.setAuth(req)
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		log.Trace("influx", "exec query failed",
			"query", q, "status", resp.StatusCode,
			"latency", time.Since(start).Round(time.Millisecond).String(),
			"body", strings.TrimSpace(string(body)))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	log.Trace("influx", "exec query ok",
		"query", q, "status", resp.StatusCode,
		"latency", time.Since(start).Round(time.Millisecond).String())
	return nil
}

func (d *Dumper) writeLines(dbName, body string) error {
	var u string
	if d.Version() == 2 {
		u = d.baseURL() + "/api/v2/write?org=" + url.QueryEscape(d.user) + "&bucket=" + url.QueryEscape(dbName) + "&precision=ns"
	} else {
		u = d.baseURL() + "/write?db=" + url.QueryEscape(dbName) + "&precision=ns"
	}
	lines := strings.Count(body, "\n")
	start := time.Now()
	req, err := http.NewRequestWithContext(d.ctxOrBg(), "POST", u, strings.NewReader(body))
	if err != nil {
		return err
	}
	d.setAuth(req)
	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("write %s: %w", dbName, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		log.Trace("influx", "write failed",
			"bucket", dbName, "lines", lines, "status", resp.StatusCode,
			"latency", time.Since(start).Round(time.Millisecond).String(),
			"body", strings.TrimSpace(string(respBody)))
		return fmt.Errorf("write %s: HTTP %d: %s", dbName, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	log.Trace("influx", "write ok",
		"bucket", dbName, "lines", lines, "status", resp.StatusCode,
		"latency", time.Since(start).Round(time.Millisecond).String())
	return nil
}
