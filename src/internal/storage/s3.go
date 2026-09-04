// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package storage

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	s3IMDSv2Token = "http://169.254.169.254/latest/api/token"
	s3IMDSv2Creds = "http://169.254.169.254/latest/meta-data/iam/security-credentials/"
)

var nowFunc = time.Now

type s3Storage struct {
	bucket   string
	prefix   string
	endpoint string
	region   string
	keyID    string
	keySec   string
	role     string
	client   *http.Client
}

type s3ListResult struct {
	XMLName            xml.Name    `xml:"ListBucketResult"`
	Contents           []s3ListObj `xml:"Contents"`
	NextContinuation   string      `xml:"NextContinuationToken"`
	IsTruncated        bool        `xml:"IsTruncated"`
	ContinuationTokQry string
}

type s3ListObj struct {
	Key          string `xml:"Key"`
	Size         int64  `xml:"Size"`
	LastModified string `xml:"LastModified"`
}

func (s *s3Storage) Delete(ctx context.Context, filePath string) error {
	u := s.requestURL(s.key(filePath))
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(500*(1<<uint(attempt-1))) * time.Millisecond):
			}
		}
		resp, err := s.do(ctx, http.MethodDelete, u, nil, 0, "")
		if err != nil {
			if ctx.Err() != nil {
				return err
			}
			lastErr = err
			continue
		}
		if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			return nil
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		resp.Body.Close()
		if !isRetryableStatus(resp.StatusCode) {
			return fmt.Errorf("s3: delete: %s: %s", resp.Status, strings.TrimSpace(string(b)))
		}
		lastErr = fmt.Errorf("s3: delete: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	if lastErr != nil {
		return lastErr
	}
	return nil
}

func (s *s3Storage) Download(ctx context.Context, filePath string) (io.ReadCloser, int64, error) {
	u := s.requestURL(s.key(filePath))
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, 0, ctx.Err()
			case <-time.After(time.Duration(500*(1<<uint(attempt-1))) * time.Millisecond):
			}
		}
		resp, err := s.do(ctx, http.MethodGet, u, nil, 0, "")
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
			return nil, 0, fmt.Errorf("s3: download: %s: %s", resp.Status, strings.TrimSpace(string(b)))
		}
		lastErr = fmt.Errorf("s3: download: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	if lastErr != nil {
		return nil, 0, lastErr
	}
	return nil, 0, fmt.Errorf("s3: download: failed after retries")
}

func (s *s3Storage) List(ctx context.Context, prefix string) ([]Entry, error) {
	searchPrefix := s.key(prefix)
	if searchPrefix != "" && !strings.HasSuffix(searchPrefix, "/") {
		searchPrefix += "/"
	}

	var entries []Entry
	continuation := ""
	for {
		u := s.listURL(searchPrefix, continuation)
		var resp *http.Response
		var err error
		var lastErr error
		for attempt := 0; attempt < 3; attempt++ {
			if attempt > 0 {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(time.Duration(500*(1<<uint(attempt-1))) * time.Millisecond):
				}
			}
			resp, err = s.do(ctx, http.MethodGet, u, nil, 0, "")
			if err != nil {
				if ctx.Err() != nil {
					return nil, err
				}
				lastErr = err
				continue
			}
			if resp.StatusCode != http.StatusOK {
				if isRetryableStatus(resp.StatusCode) {
					b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
					resp.Body.Close()
					lastErr = fmt.Errorf("s3: list: %s: %s", resp.Status, strings.TrimSpace(string(b)))
					continue
				}
				b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
				resp.Body.Close()
				return nil, fmt.Errorf("s3: list: %s: %s", resp.Status, strings.TrimSpace(string(b)))
			}
			lastErr = nil
			break
		}
		if lastErr != nil {
			return nil, lastErr
		}
		if resp == nil {
			return nil, fmt.Errorf("s3: list: failed after retries")
		}
		var result s3ListResult
		if err := xml.NewDecoder(resp.Body).Decode(&result); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("s3: list decode: %w", err)
		}
		resp.Body.Close()
		for _, obj := range result.Contents {
			if obj.Key == searchPrefix {
				continue
			}
			entries = append(entries, Entry{
				Path:    strings.TrimPrefix(obj.Key, s.prefix+"/"),
				Size:    obj.Size,
				ModTime: parseS3Time(obj.LastModified),
			})
		}
		if !result.IsTruncated || result.NextContinuation == "" {
			break
		}
		continuation = result.NextContinuation
	}
	return entries, nil
}

func (s *s3Storage) Upload(ctx context.Context, filePath string, r io.Reader) (int64, error) {
	key := s.key(filePath)

	spool, err := os.CreateTemp(SpoolDir(), "dbbackup-s3-*")
	if err != nil {
		return 0, fmt.Errorf("s3 spool: %w", err)
	}
	spoolPath := spool.Name()
	defer func() { spool.Close(); os.Remove(spoolPath) }()

	n, err := io.Copy(spool, r)
	if err != nil {
		return 0, fmt.Errorf("s3 spool: %w", err)
	}
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}

	h := sha256.New()
	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		return 0, fmt.Errorf("s3 spool rewind: %w", err)
	}
	if _, err := io.Copy(h, spool); err != nil {
		return 0, fmt.Errorf("s3 spool hash: %w", err)
	}
	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		return 0, fmt.Errorf("s3 spool rewind: %w", err)
	}
	payloadHash := hex.EncodeToString(h.Sum(nil))

	u := s.requestURL(key)
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			case <-time.After(time.Duration(500*(1<<uint(attempt-1))) * time.Millisecond):
			}
			if _, err := spool.Seek(0, io.SeekStart); err != nil {
				return 0, fmt.Errorf("s3 spool rewind: %w", err)
			}
		}
		resp, err := s.do(ctx, http.MethodPut, u, spool, n, payloadHash)
		if err != nil {
			if ctx.Err() != nil {
				return 0, err
			}
			lastErr = err
			continue
		}
		if resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			return n, nil
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		resp.Body.Close()
		if !isRetryableStatus(resp.StatusCode) {
			return 0, fmt.Errorf("s3: upload: %s: %s", resp.Status, strings.TrimSpace(string(b)))
		}
		lastErr = fmt.Errorf("s3: upload: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	if lastErr != nil {
		return 0, lastErr
	}
	return 0, fmt.Errorf("s3: upload: failed after retries")
}
func awsEncode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '.', c == '_', c == '~', c == '/':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func awsEncodeQuery(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '.', c == '_', c == '~':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
func canonicalQuery(raw string) string {
	vals, _ := url.ParseQuery(raw)
	keys := make([]string, 0, len(vals))
	for k := range vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		for _, v := range vals[k] {
			parts = append(parts, awsEncodeQuery(k)+"="+awsEncodeQuery(v))
		}
	}
	return strings.Join(parts, "&")
}
func (s *s3Storage) creds() (string, string, error) {
	if s.keyID != "" {
		return s.keyID, s.keySec, nil
	}
	if s.role == "" {
		return "", "", nil
	}
	role := s.role
	s.role = ""
	if _, err := s.imdsRole(); err != nil {
		return "", "", err
	}
	s.role = role
	return s.keyID, s.keySec, nil
}

func (s *s3Storage) do(ctx context.Context, method, rawURL string, body io.Reader, bodyLen int64, payloadHash string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.ContentLength = bodyLen
	}
	if payloadHash == "" {
		payloadHash = sha256Hex("")
	}
	if err := s.sign(req, payloadHash); err != nil {
		return nil, err
	}
	return s.client.Do(req)
}
func escapePath(p string) string {
	segs := strings.Split(p, "/")
	for i, seg := range segs {
		segs[i] = url.PathEscape(seg)
	}
	return strings.Join(segs, "/")
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}
func (s *s3Storage) host(key string) string {
	if s.endpoint != "" {
		u, err := url.Parse(s.endpoint)
		if err != nil {
			return s.endpoint
		}
		return u.Host
	}
	return s.bucket + ".s3." + s.region + ".amazonaws.com"
}
func (s *s3Storage) imdsRole() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, s3IMDSv2Token, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-aws-ec2-metadata-token-ttl-seconds", "60")
	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("imds token: %s", resp.Status)
	}
	token, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return "", err
	}

	req, err = http.NewRequestWithContext(ctx, http.MethodGet, s3IMDSv2Creds, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-aws-ec2-metadata-token", string(token))
	resp, err = s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	roles, err := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if err != nil || resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("imds roles: %s", resp.Status)
	}
	role := strings.TrimSpace(strings.Split(string(roles), "\n")[0])
	if role == "" {
		return "", fmt.Errorf("no iam role")
	}

	req, err = http.NewRequestWithContext(ctx, http.MethodGet, s3IMDSv2Creds+url.PathEscape(role), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-aws-ec2-metadata-token", string(token))
	resp, err = s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var creds struct {
		AccessKeyID     string `json:"AccessKeyId"`
		SecretAccessKey string `json:"SecretAccessKey"`
		Token           string `json:"Token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&creds); err != nil {
		return "", err
	}
	if creds.AccessKeyID == "" {
		return "", fmt.Errorf("empty role credentials")
	}
	s.keyID = creds.AccessKeyID
	s.keySec = creds.SecretAccessKey
	return role, nil
}
func init() {
	RegisterBackend(BackendSpec{
		Name:  "s3",
		Label: "Amazon S3",
		New:   newS3Storage,
	})
}
func isRetryableStatus(code int) bool {
	return code == 429 || code >= 500
}

func (s *s3Storage) key(filePath string) string {
	k := s.prefix + "/" + strings.TrimPrefix(filePath, "/")
	return strings.TrimPrefix(k, "/")
}

func (s *s3Storage) listURL(prefix, continuation string) string {
	var u string
	if s.endpoint != "" {
		u = s.endpoint + "/" + s.bucket
	} else {
		u = s.scheme() + "://" + s.host("") + "/" + s.bucket
	}
	q := awsEncodeQuery("list-type") + "=2&" + awsEncodeQuery("prefix") + "=" + awsEncodeQuery(prefix)
	if continuation != "" {
		q += "&" + awsEncodeQuery("continuation-token") + "=" + awsEncodeQuery(continuation)
	}
	return u + "?" + q
}
func newS3Storage(opts map[string]string) (Storage, error) {
	bucket := opts["bucket"]
	if bucket == "" {
		return nil, fmt.Errorf("s3: bucket required")
	}

	client := &http.Client{Timeout: 3600 * time.Second}
	if HasTLSOpts(opts) {
		client = TLSHTTPClient(opts)
		client.Timeout = 3600 * time.Second
	}

	s := &s3Storage{
		bucket:   bucket,
		prefix:   strings.Trim(strings.TrimPrefix(opts["path"], "/"), "/"),
		endpoint: strings.TrimRight(opts["endpoint"], "/"),
		region:   opts["region"],
		keyID:    opts["key_id"],
		keySec:   opts["key_secret"],
		client:   client,
	}
	if s.region == "" {
		return nil, fmt.Errorf("s3: region required (set storage.region)")
	}

	if s.keyID == "" {
		if role, err := s.imdsRole(); err == nil && role != "" {
			s.role = role
		}
	}
	return s, nil
}

func parseS3Time(s string) int64 {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return 0
	}
	return t.UnixNano()
}
func (s *s3Storage) requestURL(key string) string {
	pathPart := "/" + escapePath(key)
	if s.endpoint != "" {
		return s.endpoint + "/" + s.bucket + pathPart
	}
	return s.scheme() + "://" + s.host(key) + pathPart
}
func (s *s3Storage) scheme() string {
	if s.endpoint != "" && strings.HasPrefix(s.endpoint, "http://") {
		return "http"
	}
	return "https"
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
func (s *s3Storage) sign(req *http.Request, payloadHash string) error {
	accessKey, secretKey, err := s.creds()
	if err != nil {
		return err
	}

	now := nowFunc().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")

	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)

	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	canonicalHeaders := "host:" + host + "\n" +
		"x-amz-content-sha256:" + payloadHash + "\n" +
		"x-amz-date:" + amzDate + "\n"

	canonicalRequest := req.Method + "\n" +
		req.URL.EscapedPath() + "\n" +
		canonicalQuery(req.URL.RawQuery) + "\n" +
		canonicalHeaders + "\n" +
		signedHeaders + "\n" +
		payloadHash

	scope := dateStamp + "/" + s.region + "/s3/aws4_request"
	stringToSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + sha256Hex(canonicalRequest)

	signingKey := hmacSHA256([]byte("AWS4"+secretKey), dateStamp)
	signingKey = hmacSHA256(signingKey, s.region)
	signingKey = hmacSHA256(signingKey, "s3")
	signingKey = hmacSHA256(signingKey, "aws4_request")

	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))

	req.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential="+accessKey+"/"+scope+
			", SignedHeaders="+signedHeaders+
			", Signature="+signature)
	return nil
}
