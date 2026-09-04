// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: LicenseRef-NSLv1

// This file is part of the Supported (Supporter) edition of db-backup and is
// licensed under the Nfrastack Supporter License v1 (NSLv1).
// It is excluded from the Community build. !community.

//go:build !community

package backend

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nfrastack/db-backup/internal/storage"
)

const (
	azureBlobSuffix = ".blob.core.windows.net"
	azureBlockSize  = 4 << 20
)

var azureSingleLimit int64 = 5 << 30

var testAzureBase = ""

type azureStorage struct {
	account   string
	container string
	prefix    string
	key       []byte
	client    *http.Client
	endpoint  string
}

type azureListBlobs struct {
	XMLName    xml.Name   `xml:"EnumerationResults"`
	NextMarker string     `xml:"NextMarker"`
	Blobs      azureBlobs `xml:"Blobs"`
}

type azureBlobs struct {
	Blob []azureBlobItem `xml:"Blob"`
}

type azureBlobItem struct {
	Name       string         `xml:"Name"`
	Properties azureBlobProps `xml:"Properties"`
}

type azureBlobProps struct {
	ContentLength int64  `xml:"Content-Length"`
	LastModified  string `xml:"Last-Modified"`
}

func (s *azureStorage) Delete(ctx context.Context, filePath string) error {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(500*(1<<uint(attempt-1))) * time.Millisecond):
			}
		}
		resp, err := s.do(ctx, http.MethodDelete, s.blobURL(s.blobName(filePath)), nil, 0)
		if err != nil {
			if ctx.Err() != nil {
				return err
			}
			lastErr = err
			continue
		}
		if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNoContent {
			resp.Body.Close()
			return nil
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		resp.Body.Close()
		if !isRetryableStatus(resp.StatusCode) {
			return fmt.Errorf("azure: delete: %s: %s", resp.Status, strings.TrimSpace(string(b)))
		}
		lastErr = fmt.Errorf("azure: delete: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("azure: delete: failed after retries")
}

func (s *azureStorage) Download(ctx context.Context, filePath string) (io.ReadCloser, int64, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, 0, ctx.Err()
			case <-time.After(time.Duration(500*(1<<uint(attempt-1))) * time.Millisecond):
			}
		}
		resp, err := s.do(ctx, http.MethodGet, s.blobURL(s.blobName(filePath)), nil, 0)
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
			return nil, 0, fmt.Errorf("azure: download: %s: %s", resp.Status, strings.TrimSpace(string(b)))
		}
		lastErr = fmt.Errorf("azure: download: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	if lastErr != nil {
		return nil, 0, lastErr
	}
	return nil, 0, fmt.Errorf("azure: download: failed after retries")
}

func (s *azureStorage) List(ctx context.Context, prefix string) ([]storage.Entry, error) {
	searchPrefix := strings.Trim(s.prefix+"/"+strings.TrimPrefix(prefix, "/"), "/")
	searchPrefix = strings.TrimPrefix(searchPrefix, "/")

	var entries []storage.Entry
	marker := ""
	for {
		u := s.containerURL() + "?restype=container&comp=list&prefix=" + url.QueryEscape(searchPrefix)
		if marker != "" {
			u += "&marker=" + url.QueryEscape(marker)
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
			resp, lastErr = s.do(ctx, http.MethodGet, u, nil, 0)
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
				return nil, fmt.Errorf("azure: list: %s: %s", resp.Status, strings.TrimSpace(string(b)))
			}
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
			resp.Body.Close()
			lastErr = fmt.Errorf("azure: list: %s: %s", resp.Status, strings.TrimSpace(string(b)))
		}
		if lastErr != nil && resp == nil {
			return nil, lastErr
		}
		if resp == nil || resp.StatusCode != http.StatusOK {
			if lastErr != nil {
				return nil, lastErr
			}
			return nil, fmt.Errorf("azure: list: failed after retries")
		}
		var result azureListBlobs
		if err := xml.NewDecoder(resp.Body).Decode(&result); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("azure: list decode: %w", err)
		}
		resp.Body.Close()
		for _, b := range result.Blobs.Blob {
			if b.Name == searchPrefix {
				continue
			}
			entries = append(entries, storage.Entry{
				Path:    strings.TrimPrefix(b.Name, s.prefix+"/"),
				Size:    b.Properties.ContentLength,
				ModTime: parseAzureTime(b.Properties.LastModified),
			})
		}
		if result.NextMarker == "" {
			break
		}
		marker = result.NextMarker
	}
	return entries, nil
}

func (s *azureStorage) Upload(ctx context.Context, filePath string, r io.Reader) (int64, error) {
	blob := s.blobName(filePath)

	spool, err := os.CreateTemp(storage.SpoolDir(), "dbbackup-azure-*")
	if err != nil {
		return 0, fmt.Errorf("azure spool: %w", err)
	}
	spoolPath := spool.Name()
	defer func() { spool.Close(); os.Remove(spoolPath) }()

	n, err := io.Copy(spool, r)
	if err != nil {
		return 0, fmt.Errorf("azure spool: %w", err)
	}

	if n <= azureSingleLimit {
		var lastErr error
		for attempt := 0; attempt < 3; attempt++ {
			if attempt > 0 {
				select {
				case <-ctx.Done():
					return 0, ctx.Err()
				case <-time.After(time.Duration(500*(1<<uint(attempt-1))) * time.Millisecond):
				}
			}
			if _, err := spool.Seek(0, io.SeekStart); err != nil {
				return 0, fmt.Errorf("azure spool rewind: %w", err)
			}
			resp, err := s.do(ctx, http.MethodPut, s.blobURL(blob), spool, n)
			if err != nil {
				if ctx.Err() != nil {
					return 0, err
				}
				lastErr = err
				continue
			}
			if resp.StatusCode == http.StatusCreated {
				resp.Body.Close()
				return n, nil
			}
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
			resp.Body.Close()
			if !isRetryableStatus(resp.StatusCode) {
				return 0, fmt.Errorf("azure: upload: %s: %s", resp.Status, strings.TrimSpace(string(b)))
			}
			lastErr = fmt.Errorf("azure: upload: %s: %s", resp.Status, strings.TrimSpace(string(b)))
		}
		if lastErr != nil {
			return 0, lastErr
		}
		return 0, fmt.Errorf("azure: upload: failed after retries")
	}

	return s.uploadBlockBlob(ctx, blob, spool, n)
}
func (s *azureStorage) baseURL() string {
	if s.endpoint != "" {
		return s.endpoint
	}
	if testAzureBase != "" {
		return testAzureBase
	}
	return "https://" + s.account + azureBlobSuffix
}
func (s *azureStorage) blobName(filePath string) string {
	n := strings.Trim(s.prefix, "/") + "/" + strings.TrimPrefix(filePath, "/")
	return strings.Trim(n, "/")
}

func (s *azureStorage) blobURL(blob string) string {
	return s.baseURL() + "/" + s.container + "/" + url.PathEscape(blob)
}

func (s *azureStorage) containerURL() string {
	return s.baseURL() + "/" + s.container
}

func (s *azureStorage) do(ctx context.Context, method, rawURL string, body io.Reader, contentLength int64) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.ContentLength = contentLength
	}
	s.sign(req, contentLength)
	return s.client.Do(req)
}
func init() {
	storage.RegisterBackend(storage.BackendSpec{
		Name:  "azure",
		Label: "Azure Blob Storage",
		New:   newAzureStorage,
	})
}

func newAzureStorage(opts map[string]string) (storage.Storage, error) {
	account := opts["account"]
	key := opts["key"]
	container := opts["container"]
	if container == "" {
		container = "backups"
	}

	if account == "" || key == "" {
		return nil, fmt.Errorf("azure: account and key required")
	}

	decoded, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		return nil, fmt.Errorf("azure: decode key: %w", err)
	}

	client := &http.Client{Timeout: 120 * time.Second}
	if storage.HasTLSOpts(opts) {
		client = storage.TLSHTTPClient(opts)
	}

	return &azureStorage{
		account:   account,
		container: container,
		prefix:    strings.TrimPrefix(opts["path"], "/"),
		endpoint:  strings.TrimRight(opts["endpoint"], "/"),
		key:       decoded,
		client:    client,
	}, nil
}

func parseAzureTime(s string) int64 {
	t, err := time.Parse(http.TimeFormat, s)
	if err != nil {
		return 0
	}
	return t.UnixNano()
}
func (s *azureStorage) sign(req *http.Request, contentLength int64) {
	now := time.Now().UTC().Format(http.TimeFormat)
	req.Header.Set("x-ms-date", now)
	req.Header.Set("x-ms-version", "2020-10-02")
	if req.Method == http.MethodPut {
		req.Header.Set("x-ms-blob-type", "BlockBlob")
	}

	contentType := req.Header.Get("Content-Type")
	if contentType == "" {
		contentType = ""
	}

	contentLengthStr := ""
	if contentLength > 0 {
		contentLengthStr = strconv.FormatInt(contentLength, 10)
	}

	var xms []string
	for k, vs := range req.Header {
		lower := strings.ToLower(k)
		if strings.HasPrefix(lower, "x-ms-") {
			for _, v := range vs {
				xms = append(xms, lower+":"+strings.TrimSpace(v))
			}
		}
	}
	sort.Strings(xms)

	resource := "/" + s.account + req.URL.Path

	stringToSign := req.Method + "\n" +
		"\n" +
		"\n" +
		contentLengthStr + "\n" +
		"\n" +
		contentType + "\n" +
		"\n" +
		"\n" +
		"\n" +
		"\n" +
		"\n" +
		"\n" +
		strings.Join(xms, "\n") + "\n" +
		resource

	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(stringToSign))
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	req.Header.Set("Authorization", "SharedKey "+s.account+":"+sig)
}

func (s *azureStorage) uploadBlockBlob(ctx context.Context, blob string, spool *os.File, size int64) (int64, error) {
	var blockIDs []string
	blockIndex := 0
	buf := make([]byte, azureBlockSize)
	for offset := int64(0); offset < size; {
		if _, err := spool.Seek(offset, io.SeekStart); err != nil {
			return 0, fmt.Errorf("azure block seek: %w", err)
		}
		read, err := io.ReadFull(spool, buf)
		if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
			return 0, fmt.Errorf("azure block read: %w", err)
		}
		blockID := fmt.Sprintf("%08d", blockIndex)
		encoded := base64.StdEncoding.EncodeToString([]byte(blockID))
		blockIDs = append(blockIDs, encoded)

		body := buf[:read]
		var lastErr error
		for attempt := 0; attempt < 3; attempt++ {
			if attempt > 0 {
				select {
				case <-ctx.Done():
					return 0, ctx.Err()
				case <-time.After(time.Duration(500*(1<<uint(attempt-1))) * time.Millisecond):
				}
			}
			resp, err := s.do(ctx, http.MethodPut,
				s.blobURL(blob)+"?comp=block&blockid="+url.QueryEscape(encoded),
				bytes.NewReader(body), int64(read))
			if err != nil {
				if ctx.Err() != nil {
					return 0, err
				}
				lastErr = err
				continue
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusCreated {
				lastErr = nil
				break
			}
			if !isRetryableStatus(resp.StatusCode) {
				return 0, fmt.Errorf("azure: put block: %s", resp.Status)
			}
			lastErr = fmt.Errorf("azure: put block: %s", resp.Status)
		}
		if lastErr != nil {
			return 0, lastErr
		}

		offset += int64(read)
		blockIndex++
	}

	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="utf-8"?><BlockList>`)
	for _, id := range blockIDs {
		sb.WriteString(`<Latest>` + id + `</Latest>`)
	}
	sb.WriteString(`</BlockList>`)
	listBody := sb.String()

	var lastErr error
	var resp *http.Response
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			case <-time.After(time.Duration(500*(1<<uint(attempt-1))) * time.Millisecond):
			}
		}
		resp, lastErr = s.do(ctx, http.MethodPut,
			s.blobURL(blob)+"?comp=blocklist",
			bytes.NewBufferString(listBody), int64(len(listBody)))
		if lastErr != nil {
			if ctx.Err() != nil {
				return 0, lastErr
			}
			continue
		}
		if resp.StatusCode == http.StatusCreated {
			break
		}
		if !isRetryableStatus(resp.StatusCode) {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
			resp.Body.Close()
			return 0, fmt.Errorf("azure: commit block list: %s: %s", resp.Status, strings.TrimSpace(string(b)))
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		resp.Body.Close()
		lastErr = fmt.Errorf("azure: commit block list: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	if lastErr != nil && resp == nil {
		return 0, lastErr
	}
	if resp == nil {
		return 0, fmt.Errorf("azure: commit block list: failed after retries")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return 0, fmt.Errorf("azure: commit block list: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	return size, nil
}
