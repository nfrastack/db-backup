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

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database/common"
)

func ListDatabases(host string, port int, tlsCfg *config.TLSConfig) ([]string, error) {
	d := NewDumper(host, port, "", "", "", 1, tlsCfg)
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
	if err := d.OpenContext(context.Background()); err != nil {
		return err
	}
	defer d.Close()

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

	var block strings.Builder
	flush := func() error {
		if block.Len() == 0 {
			return nil
		}
		if err := d.writeLines(dbName, block.String()); err != nil {
			return err
		}
		block.Reset()
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
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read dump: %w", err)
	}
	return flush()
}

func (d *Dumper) createV2BucketIfMissing(name string) error {
	u := fmt.Sprintf("%s://%s:%d/api/v2/buckets?name=%s", d.scheme(), d.host, d.port, url.QueryEscape(name))
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
	for _, b := range bucketsResp.Buckets {
		if b.Name == name {
			return nil
		}
	}

	orgsURL := fmt.Sprintf("%s://%s:%d/api/v2/orgs", d.scheme(), d.host, d.port)
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

	body := []byte(fmt.Sprintf(`{"name":%q,"orgID":%q}`, name, orgID))
	createReq, err := http.NewRequestWithContext(d.ctxOrBg(), "POST", fmt.Sprintf("%s://%s:%d/api/v2/buckets", d.scheme(), d.host, d.port), bytes.NewReader(body))
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
		return nil
	}
	respBody, _ := io.ReadAll(io.LimitReader(createResp.Body, 4096))
	return fmt.Errorf("HTTP %d: %s", createResp.StatusCode, strings.TrimSpace(string(respBody)))
}

func (d *Dumper) ensureDBRPMapping(name string) error {
	u := fmt.Sprintf("%s://%s:%d/api/v2/buckets?name=%s", d.scheme(), d.host, d.port, url.QueryEscape(name))
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

	orgsURL := fmt.Sprintf("%s://%s:%d/api/v2/orgs", d.scheme(), d.host, d.port)
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

	dbrpURL := fmt.Sprintf("%s://%s:%d/api/v2/dbrps?orgID=%s&db=%s&bucketID=%s", d.scheme(), d.host, d.port, orgID, url.QueryEscape(name), bucketID)
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

	body := []byte(fmt.Sprintf(`{"db":%q,"orgID":%q,"bucketID":%q,"retention_policy":"autogen","default":true}`, name, orgID, bucketID))
	createReq, err := http.NewRequestWithContext(d.ctxOrBg(), "POST", fmt.Sprintf("%s://%s:%d/api/v2/dbrps", d.scheme(), d.host, d.port), bytes.NewReader(body))
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
		return nil
	}
	respBody, _ := io.ReadAll(io.LimitReader(createResp.Body, 4096))
	return fmt.Errorf("HTTP %d: %s", createResp.StatusCode, strings.TrimSpace(string(respBody)))
}
func (d *Dumper) execV1Query(q string) error {
	u := fmt.Sprintf("%s://%s:%d/query?q=%s", d.scheme(), d.host, d.port, url.QueryEscape(q))
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
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (d *Dumper) writeLines(dbName, body string) error {
	var u string
	if d.Version() == 2 {
		u = fmt.Sprintf("%s://%s:%d/api/v2/write?org=%s&bucket=%s&precision=ns", d.scheme(), d.host, d.port, url.QueryEscape(d.user), url.QueryEscape(dbName))
	} else {
		u = fmt.Sprintf("%s://%s:%d/write?db=%s&precision=ns", d.scheme(), d.host, d.port, url.QueryEscape(dbName))
	}
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
		return fmt.Errorf("write %s: HTTP %d: %s", dbName, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}
