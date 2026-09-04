// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package storage

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
)

type webdavStorage struct {
	baseURL string
	prefix  string
	user    string
	pass    string
	client  *http.Client
}

type propfindResponse struct {
	XMLName   xml.Name               `xml:"multistatus"`
	Responses []propfindResponseItem `xml:"response"`
}

type propfindResponseItem struct {
	Href string       `xml:"href"`
	Prop propfindProp `xml:"propstat>prop"`
}

type propfindProp struct {
	DisplayName string `xml:"displayname"`
	ContentLen  string `xml:"getcontentlength"`
	Modified    string `xml:"getlastmodified"`
	ContentType string `xml:"getcontenttype"`
}

func (s *webdavStorage) Delete(ctx context.Context, filePath string) error {
	url := s.davPath(filePath)
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(500*(1<<uint(attempt-1))) * time.Millisecond):
			}
		}
		req, err := http.NewRequestWithContext(ctx, "DELETE", url, nil)
		if err != nil {
			return fmt.Errorf("webdav: delete req: %w", err)
		}
		s.setAuth(req)
		resp, err := s.client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return err
			}
			lastErr = err
			continue
		}
		resp.Body.Close()
		if resp.StatusCode < 300 {
			return nil
		}
		if !isRetryableStatus(resp.StatusCode) {
			return fmt.Errorf("webdav: delete %s: %s", url, resp.Status)
		}
		lastErr = fmt.Errorf("webdav: delete %s: %s", url, resp.Status)
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("webdav: delete %s: failed after retries", url)
}

func (s *webdavStorage) Download(ctx context.Context, filePath string) (io.ReadCloser, int64, error) {
	url := s.davPath(filePath)
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, 0, ctx.Err()
			case <-time.After(time.Duration(500*(1<<uint(attempt-1))) * time.Millisecond):
			}
		}
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return nil, 0, fmt.Errorf("webdav: get req: %w", err)
		}
		s.setAuth(req)
		resp, err := s.client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, 0, err
			}
			lastErr = err
			continue
		}
		if resp.StatusCode < 300 {
			return resp.Body, resp.ContentLength, nil
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		resp.Body.Close()
		if !isRetryableStatus(resp.StatusCode) {
			return nil, 0, fmt.Errorf("webdav: get %s: %s: %s", url, resp.Status, strings.TrimSpace(string(b)))
		}
		lastErr = fmt.Errorf("webdav: get %s: %s: %s", url, resp.Status, strings.TrimSpace(string(b)))
	}
	if lastErr != nil {
		return nil, 0, lastErr
	}
	return nil, 0, fmt.Errorf("webdav: get %s: failed after retries", url)
}

func (s *webdavStorage) List(ctx context.Context, prefix string) ([]Entry, error) {
	url := s.baseURL + "/" + path.Join(s.prefix, prefix) + "/"
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
		req, err := http.NewRequestWithContext(ctx, "PROPFIND", url, nil)
		if err != nil {
			return nil, fmt.Errorf("webdav: propfind req: %w", err)
		}
		s.setAuth(req)
		req.Header.Set("Depth", "1")
		resp, err = s.client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, err
			}
			lastErr = err
			continue
		}
		if resp.StatusCode < 300 {
			break
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		resp.Body.Close()
		if !isRetryableStatus(resp.StatusCode) {
			return nil, fmt.Errorf("webdav: propfind %s: %s: %s", url, resp.Status, strings.TrimSpace(string(b)))
		}
		lastErr = fmt.Errorf("webdav: propfind %s: %s: %s", url, resp.Status, strings.TrimSpace(string(b)))
	}
	if lastErr != nil && resp == nil {
		return nil, lastErr
	}
	if resp == nil {
		return nil, fmt.Errorf("webdav: propfind %s: failed after retries", url)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("webdav: propfind %s: %s", url, resp.Status)
	}

	var pf propfindResponse
	if err := xml.NewDecoder(resp.Body).Decode(&pf); err != nil {
		return nil, fmt.Errorf("webdav: parse propfind: %w", err)
	}

	baseURL := s.baseURL + "/" + s.prefix + "/"

	var entries []Entry
	for _, item := range pf.Responses {
		href := item.Href

		if strings.TrimRight(href, "/") == strings.TrimRight(url, "/") {
			continue
		}

		rel := strings.TrimPrefix(href, baseURL)
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			continue
		}

		size, _ := strconv.ParseInt(item.Prop.ContentLen, 10, 64)

		var modTime int64
		if item.Prop.Modified != "" {
			if t, err := time.Parse(time.RFC1123, item.Prop.Modified); err == nil {
				modTime = t.UnixNano()
			}
		}

		entries = append(entries, Entry{
			Path:    rel,
			Size:    size,
			ModTime: modTime,
		})
	}
	return entries, nil
}

func (s *webdavStorage) Upload(ctx context.Context, filePath string, r io.Reader) (int64, error) {
	url := s.davPath(filePath)

	if err := s.mkcol(ctx, path.Dir(filePath)); err != nil {
	}

	spool, err := os.CreateTemp(SpoolDir(), "dbbackup-webdav-*")
	if err != nil {
		return 0, fmt.Errorf("webdav spool: %w", err)
	}
	spoolPath := spool.Name()
	defer func() { spool.Close(); os.Remove(spoolPath) }()
	n, err := io.Copy(spool, r)
	if err != nil {
		return 0, fmt.Errorf("webdav spool: %w", err)
	}
	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		return 0, fmt.Errorf("webdav spool rewind: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			case <-time.After(time.Duration(500*(1<<uint(attempt-1))) * time.Millisecond):
			}
			if _, err := spool.Seek(0, io.SeekStart); err != nil {
				return 0, fmt.Errorf("webdav spool rewind: %w", err)
			}
		}
		req, err := http.NewRequestWithContext(ctx, "PUT", url, spool)
		if err != nil {
			return 0, fmt.Errorf("webdav: put req: %w", err)
		}
		s.setAuth(req)
		req.Header.Set("Content-Type", "application/octet-stream")
		req.ContentLength = n

		resp, err := s.client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return 0, err
			}
			lastErr = err
			continue
		}
		if resp.StatusCode < 300 {
			resp.Body.Close()
			return n, nil
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if !isRetryableStatus(resp.StatusCode) {
			return 0, fmt.Errorf("webdav: put %s: %s: %s", url, resp.Status, strings.TrimSpace(string(b)))
		}
		lastErr = fmt.Errorf("webdav: put %s: %s: %s", url, resp.Status, strings.TrimSpace(string(b)))
	}
	if lastErr != nil {
		return 0, lastErr
	}
	return 0, fmt.Errorf("webdav: put %s: failed after retries", url)
}

func (s *webdavStorage) davPath(filePath string) string {
	return s.baseURL + "/" + path.Join(s.prefix, filePath)
}

func init() {
	RegisterBackend(BackendSpec{
		Name:  "webdav",
		Label: "WebDAV",
		New:   newWebDAVStorage,
	})
}

func newWebDAVStorage(opts map[string]string) (Storage, error) {
	baseURL := opts["url"]
	if baseURL == "" {
		return nil, fmt.Errorf("webdav: url required")
	}
	baseURL = strings.TrimRight(baseURL, "/")

	return &webdavStorage{
		baseURL: baseURL,
		prefix:  strings.Trim(strings.TrimPrefix(opts["path"], "/"), "/"),
		user:    opts["user"],
		pass:    opts["pass"],
		client:  webdavClient(opts),
	}, nil
}

func (s *webdavStorage) mkcol(ctx context.Context, dirPath string) error {
	if dirPath == "." || dirPath == "" {
		return nil
	}
	url := s.davPath(dirPath)
	req, err := http.NewRequestWithContext(ctx, "MKCOL", url, nil)
	if err != nil {
		return err
	}
	s.setAuth(req)
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (s *webdavStorage) setAuth(req *http.Request) {
	if s.user != "" || s.pass != "" {
		req.SetBasicAuth(s.user, s.pass)
	}
}

func webdavClient(opts map[string]string) *http.Client {
	return TLSHTTPClient(opts)
}
