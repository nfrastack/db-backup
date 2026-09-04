// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: LicenseRef-NSLv1

// This file is part of the Supported (Supporter) edition of db-backup and is
// licensed under the Nfrastack Supporter License v1 (NSLv1).
// It is excluded from the Community build. !community.

//go:build !community

package backend

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/nfrastack/db-backup/internal/storage"
)

const (
	gcsTokenURI    = "https://oauth2.googleapis.com/token"
	gcsScope       = "https://www.googleapis.com/auth/devstorage.read_write"
	gcsUploadBase  = "https://storage.googleapis.com/upload/storage/v1/b/"
	gcsStorageBase = "https://storage.googleapis.com/storage/v1/b/"
	contentType    = "application/octet-stream"
)

var (
	testTokenURI    = ""
	testUploadBase  = ""
	testStorageBase = ""
)

type gcsStorage struct {
	bucket   string
	prefix   string
	tok      *gcsToken
	client   *http.Client
	endpoint string
}

type gcsToken struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	TokenType   string `json:"token_type"`
	clientEmail string
	privateKey  string
	tokenURI    string
	issued      time.Time
}

type gcsTokenJSON struct {
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri"`
}

type gcsObject struct {
	NextPageToken string          `json:"nextPageToken"`
	Items         []gcsObjectItem `json:"items"`
}

type gcsObjectItem struct {
	Name    string `json:"name"`
	Size    string `json:"size"`
	Updated string `json:"updated"`
}

func (s *gcsStorage) Delete(ctx context.Context, filePath string) error {
	u := s.storageURL(url.PathEscape(s.bucket)+"/o", url.PathEscape(s.objectName(filePath)))
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(500*(1<<uint(attempt-1))) * time.Millisecond):
			}
		}
		resp, err := s.do(ctx, http.MethodDelete, u, nil)
		if err != nil {
			if ctx.Err() != nil {
				return err
			}
			lastErr = err
			continue
		}
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent {
			resp.Body.Close()
			return nil
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		resp.Body.Close()
		if !isRetryableStatus(resp.StatusCode) {
			return fmt.Errorf("gcs: delete: %s: %s", resp.Status, strings.TrimSpace(string(b)))
		}
		lastErr = fmt.Errorf("gcs: delete: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("gcs: delete: failed after retries")
}

func (s *gcsStorage) Download(ctx context.Context, filePath string) (io.ReadCloser, int64, error) {
	u := s.storageURL(url.PathEscape(s.bucket)+"/o", url.PathEscape(s.objectName(filePath))+"?alt=media")
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, 0, ctx.Err()
			case <-time.After(time.Duration(500*(1<<uint(attempt-1))) * time.Millisecond):
			}
		}
		resp, err := s.do(ctx, http.MethodGet, u, nil)
		if err != nil {
			if ctx.Err() != nil {
				return nil, 0, err
			}
			lastErr = err
			continue
		}
		if resp.StatusCode == http.StatusOK {
			return resp.Body, resp.ContentLength, nil
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		resp.Body.Close()
		if !isRetryableStatus(resp.StatusCode) {
			return nil, 0, fmt.Errorf("gcs: download: %s: %s", resp.Status, strings.TrimSpace(string(b)))
		}
		lastErr = fmt.Errorf("gcs: download: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	if lastErr != nil {
		return nil, 0, lastErr
	}
	return nil, 0, fmt.Errorf("gcs: download: failed after retries")
}

func (s *gcsStorage) List(ctx context.Context, prefix string) ([]storage.Entry, error) {
	searchPrefix := s.objectName(prefix)
	if searchPrefix != "" && !strings.HasSuffix(searchPrefix, "/") {
		searchPrefix += "/"
	}

	var entries []storage.Entry
	pageToken := ""
	for {
		u := s.storageURL(url.PathEscape(s.bucket)+"/o") + "?prefix=" + url.QueryEscape(searchPrefix)
		if pageToken != "" {
			u += "&pageToken=" + url.QueryEscape(pageToken)
		}
		var resp *http.Response
		var lastErr error
		for attempt := 0; attempt < 3; attempt++ {
			if attempt > 0 {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(time.Duration(500*(1<<uint(attempt-1))) * time.Millisecond):
				}
			}
			resp, lastErr = s.do(ctx, http.MethodGet, u, nil)
			if lastErr != nil {
				if ctx.Err() != nil {
					return nil, lastErr
				}
				continue
			}
			if resp.StatusCode == http.StatusOK {
				break
			}
			if !isRetryableStatus(resp.StatusCode) {
				b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
				resp.Body.Close()
				return nil, fmt.Errorf("gcs: list: %s: %s", resp.Status, strings.TrimSpace(string(b)))
			}
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
			resp.Body.Close()
			lastErr = fmt.Errorf("gcs: list: %s: %s", resp.Status, strings.TrimSpace(string(b)))
		}
		if lastErr != nil && resp == nil {
			return nil, lastErr
		}
		if resp == nil || resp.StatusCode != http.StatusOK {
			if lastErr != nil {
				return nil, lastErr
			}
			return nil, fmt.Errorf("gcs: list: failed after retries")
		}
		var obj gcsObject
		if err := json.NewDecoder(resp.Body).Decode(&obj); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("gcs: list decode: %w", err)
		}
		resp.Body.Close()
		for _, it := range obj.Items {
			if it.Name == searchPrefix {
				continue
			}
			rel := strings.TrimPrefix(it.Name, searchPrefix)
			if rel == "" {
				continue
			}
			entries = append(entries, storage.Entry{
				Path:    strings.TrimPrefix(it.Name, s.prefix+"/"),
				Size:    parseSize(it.Size),
				ModTime: parseTime(it.Updated),
			})
		}
		if obj.NextPageToken == "" {
			break
		}
		pageToken = obj.NextPageToken
	}
	return entries, nil
}

func (s *gcsStorage) Upload(ctx context.Context, filePath string, r io.Reader) (int64, error) {
	name := s.objectName(filePath)
	spool, err := os.CreateTemp(storage.SpoolDir(), "dbbackup-gcs-*")
	if err != nil {
		return 0, fmt.Errorf("gcs spool: %w", err)
	}
	spoolPath := spool.Name()
	defer func() { spool.Close(); os.Remove(spoolPath) }()
	n, err := io.Copy(spool, r)
	if err != nil {
		return 0, fmt.Errorf("gcs spool: %w", err)
	}
	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		return 0, fmt.Errorf("gcs spool rewind: %w", err)
	}
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}

	initURL := s.uploadURL(url.PathEscape(s.bucket)+"/o") + "?uploadType=resumable&name=" + url.QueryEscape(name)
	var initResp *http.Response
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			case <-time.After(time.Duration(500*(1<<uint(attempt-1))) * time.Millisecond):
			}
		}
		initResp, lastErr = s.doHdr(ctx, http.MethodPost, initURL, nil, map[string]string{
			"X-Upload-Content-Type": contentType,
		})
		if lastErr != nil {
			if ctx.Err() != nil {
				return 0, lastErr
			}
			continue
		}
		if initResp.StatusCode == http.StatusOK && initResp.Header.Get("Location") != "" {
			break
		}
		if initResp.StatusCode != http.StatusOK && isRetryableStatus(initResp.StatusCode) {
			b, _ := io.ReadAll(io.LimitReader(initResp.Body, 1024))
			initResp.Body.Close()
			lastErr = fmt.Errorf("gcs: upload init: %s: %s", initResp.Status, strings.TrimSpace(string(b)))
			continue
		}
		b, _ := io.ReadAll(io.LimitReader(initResp.Body, 1024))
		initResp.Body.Close()
		return 0, fmt.Errorf("gcs: upload init: %s: %s", initResp.Status, strings.TrimSpace(string(b)))
	}
	if lastErr != nil && initResp == nil {
		return 0, lastErr
	}
	if initResp == nil {
		return 0, fmt.Errorf("gcs: upload init: failed after retries")
	}
	location := initResp.Header.Get("Location")
	initResp.Body.Close()
	if initResp.StatusCode != http.StatusOK || location == "" {
		return 0, fmt.Errorf("gcs: upload init: %s", initResp.Status)
	}

	var resp *http.Response
	lastErr = nil
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			case <-time.After(time.Duration(500*(1<<uint(attempt-1))) * time.Millisecond):
			}
			if _, err := spool.Seek(0, io.SeekStart); err != nil {
				return 0, fmt.Errorf("gcs spool rewind: %w", err)
			}
		}
		resp, lastErr = s.do(ctx, http.MethodPut, location, spool)
		if lastErr != nil {
			if ctx.Err() != nil {
				return 0, lastErr
			}
			continue
		}
		if resp.StatusCode == http.StatusOK {
			break
		}
		if !isRetryableStatus(resp.StatusCode) {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
			resp.Body.Close()
			return 0, fmt.Errorf("gcs: upload: %s: %s", resp.Status, strings.TrimSpace(string(b)))
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		resp.Body.Close()
		lastErr = fmt.Errorf("gcs: upload: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	if lastErr != nil && resp == nil {
		return 0, lastErr
	}
	if resp == nil {
		return 0, fmt.Errorf("gcs: upload: failed after retries")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return 0, fmt.Errorf("gcs: upload: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var obj gcsObjectItem
	if err := json.NewDecoder(resp.Body).Decode(&obj); err != nil {
		return 0, fmt.Errorf("gcs: upload decode: %w", err)
	}
	if obj.Size != "" {
		return parseSize(obj.Size), nil
	}
	return n, nil
}
func (s *gcsStorage) auth(ctx context.Context) (string, error) {
	if s.endpoint != "" {
		return "", nil
	}
	if s.tok.AccessToken != "" && time.Since(s.tok.issued) < time.Duration(s.tok.ExpiresIn-30)*time.Second {
		return s.tok.AccessToken, nil
	}
	tok, err := s.fetchToken(ctx)
	if err != nil {
		return "", err
	}
	s.tok.AccessToken = tok.AccessToken
	s.tok.ExpiresIn = tok.ExpiresIn
	s.tok.issued = time.Now()
	return tok.AccessToken, nil
}

func (s *gcsStorage) do(ctx context.Context, method, rawURL string, body io.Reader) (*http.Response, error) {
	return s.doHdr(ctx, method, rawURL, body, nil)
}

func (s *gcsStorage) doHdr(ctx context.Context, method, rawURL string, body io.Reader, hdr map[string]string) (*http.Response, error) {
	tok, err := s.auth(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	return s.client.Do(req)
}
func (s *gcsStorage) fetchToken(ctx context.Context) (*gcsToken, error) {
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	now := time.Now().Unix()
	claims, _ := json.Marshal(map[string]any{
		"iss":   s.tok.clientEmail,
		"scope": gcsScope,
		"aud":   s.tok.tokenURI,
		"iat":   now,
		"exp":   now + 3600,
	})
	seg := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)

	block, _ := pem.Decode([]byte(s.tok.privateKey))
	if block == nil {
		return nil, fmt.Errorf("gcs: decode private key: invalid PEM")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("gcs: parse private key: %w", err)
		}
	}
	signer, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("gcs: private key is not RSA")
	}
	digest := sha256.Sum256([]byte(seg))
	sig, err := rsa.SignPKCS1v15(rand.Reader, signer, crypto.SHA256, digest[:])
	if err != nil {
		return nil, fmt.Errorf("gcs: sign jwt: %w", err)
	}
	assertion := seg + "." + base64.RawURLEncoding.EncodeToString(sig)

	form := url.Values{}
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
	form.Set("assertion", assertion)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.tok.tokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("gcs: token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gcs: token exchange: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("gcs: token exchange: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var tok gcsToken
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return nil, fmt.Errorf("gcs: decode token: %w", err)
	}
	return &tok, nil
}
func init() {
	storage.RegisterBackend(storage.BackendSpec{
		Name:  "gcs",
		Label: "Google Cloud Storage",
		New:   newGCSStorage,
	})
}

func isRetryableStatus(code int) bool {
	return code == 429 || code >= 500
}
func newGCSStorage(opts map[string]string) (storage.Storage, error) {
	bucket := opts["bucket"]
	if bucket == "" {
		return nil, fmt.Errorf("gcs: bucket required")
	}

	key := opts["key"]
	if key == "" {
		return nil, fmt.Errorf("gcs: service-account key required (set storage.key)")
	}

	raw, err := os.ReadFile(key)
	if err != nil {
		return nil, fmt.Errorf("gcs: read service-account key: %w", err)
	}
	var k gcsTokenJSON
	if err := json.Unmarshal(raw, &k); err != nil {
		return nil, fmt.Errorf("gcs: parse service-account key: %w", err)
	}
	if k.TokenURI == "" {
		k.TokenURI = gcsTokenURI
	}
	if testTokenURI != "" {
		k.TokenURI = testTokenURI
	}

	return &gcsStorage{
		bucket:   bucket,
		prefix:   strings.TrimPrefix(opts["path"], "/"),
		endpoint: strings.TrimRight(opts["endpoint"], "/"),
		tok: &gcsToken{
			clientEmail: k.ClientEmail,
			privateKey:  k.PrivateKey,
			tokenURI:    k.TokenURI,
		},
		client: &http.Client{Timeout: 3600 * time.Second},
	}, nil
}

func (s *gcsStorage) objectName(filePath string) string {
	return path.Join(s.prefix, strings.TrimPrefix(filePath, "/"))
}

func parseSize(s string) int64 {
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int64(c-'0')
	}
	return n
}

func parseTime(s string) int64 {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return 0
	}
	return t.UnixNano()
}
func (s *gcsStorage) storageURL(parts ...string) string {
	base := gcsStorageBase
	if testStorageBase != "" {
		base = testStorageBase
	}
	if s.endpoint != "" {
		base = s.endpoint + "/storage/v1/b/"
	}
	return base + strings.Join(parts, "/")
}

func (s *gcsStorage) uploadURL(parts ...string) string {
	base := gcsUploadBase
	if testUploadBase != "" {
		base = testUploadBase
	}
	if s.endpoint != "" {
		base = s.endpoint + "/upload/storage/v1/b/"
	}
	return base + strings.Join(parts, "/")
}
