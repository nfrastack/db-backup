// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package stats

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"syscall"
	"time"
)

var (
	nexusSeedAX = []byte{
		0x34, 0xe5, 0xa7, 0xda, 0x34, 0x22,
		0x99, 0xcd, 0xfd, 0x30, 0x57, 0xb1,
		0x12, 0xb5, 0x19,
	}
	nexusSeedBX = []byte{
		0x3a, 0xe3, 0xb2, 0xd9, 0x33, 0x79,
		0xd5, 0x89, 0xbd, 0x36, 0x40, 0xa9,
		0x4e, 0xed, 0x46,
	}
	nexusSeedMask = []byte{
		0x5c, 0x91, 0xd3, 0xaa, 0x47, 0x18, 0xb6,
		0xe2, 0x93, 0x55, 0x2f, 0xc4, 0x61, 0x9b, 0x77,
	}
)

const (
	EndpointReport = "/ingest/report"
	EndpointCheck  = "/ingest/check"
)

type Client struct {
	baseURL string
	key     string
	http    *http.Client
}

// timeout in seconds
const clientTimeout = 10 * time.Second

type VersionResponse struct {
	Latest         string `json:"latest"`
	DateReleased   string `json:"date_released,omitempty"`
	Critical       bool   `json:"critical,omitempty"`
	DownloadURL    string `json:"download_url,omitempty"`
	ChangelogURL   string `json:"changelog_url,omitempty"`
	ImageLatest    string `json:"image_latest,omitempty"`
	LicenseRevoked bool   `json:"license_revoked,omitempty"`
}

func (c *Client) CheckVersion(ctx context.Context, body string) (*VersionResponse, error) {
	var resp VersionResponse
	ok, err := c.post(ctx, EndpointCheck, []byte(body), &resp)
	if !ok {
		return nil, err
	}
	return &resp, nil
}

// plain english
func DescribeError(err error) string {
	if err == nil {
		return ""
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		switch {
		case dnsErr.IsNotFound:
			return fmt.Sprintf("unknown host %q", dnsErr.Name)
		case dnsErr.IsTimeout:
			return fmt.Sprintf("dns lookup for %q timed out", dnsErr.Name)
		default:
			return fmt.Sprintf("dns lookup failed for %q", dnsErr.Name)
		}
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return "connection refused by server"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return fmt.Sprintf("server unreachable - no response within %s", clientTimeout)
	}
	if errors.Is(err, context.Canceled) {
		return "request cancelled"
	}
	return err.Error()
}

func NewClient(serverURL, key string) *Client {
	if serverURL == "" {
		serverURL = assembledBaseURL()
	}
	return &Client{
		baseURL: serverURL,
		key:     key,
		http: &http.Client{
			Timeout: clientTimeout,
		},
	}
}

func (c *Client) Report(ctx context.Context, body string) error {
	var resp map[string]any
	ok, err := c.post(ctx, EndpointReport, []byte(body), &resp)
	if !ok {
		return err
	}
	return nil
}

// xor seeded to limit grepping binary
func assembledBaseURL() string {
	out := make([]byte, 0, len(nexusSeedAX)+len(nexusSeedBX))
	for i := range nexusSeedAX {
		out = append(out, nexusSeedAX[i]^nexusSeedMask[i])
	}
	for i := range nexusSeedBX {
		out = append(out, nexusSeedBX[i]^nexusSeedMask[i])
	}
	return string(out)
}

func (c *Client) post(ctx context.Context, path string, body []byte, out any) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.key != "" {
		req.Header.Set("x-nfrastack-key", c.key)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return false, fmt.Errorf("reading response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, fmt.Errorf("server responded %d", resp.StatusCode)
	}
	if out != nil && len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return false, fmt.Errorf("decoding response: %w", err)
		}
	}
	return true, nil
}
