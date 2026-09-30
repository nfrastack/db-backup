// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package storage

import (
	"bytes"
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

	"github.com/nfrastack/db-backup/internal/log"
)

const (
	s3IMDSv2Token = "http://169.254.169.254/latest/api/token"
	s3IMDSv2Creds = "http://169.254.169.254/latest/meta-data/iam/security-credentials/"
)

var (
	s3MultipartThreshold = 100 * 1024 * 1024
	s3MultipartPartSize  = 64 * 1024 * 1024
	s3MultipartMaxParts  = 10000
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

type s3InitiateMultipartResult struct {
	XMLName  xml.Name `xml:"InitiateMultipartUploadResult"`
	UploadID string   `xml:"UploadId"`
}

type s3CompleteMultipartPart struct {
	PartNumber int    `xml:"PartNumber"`
	ETag       string `xml:"ETag"`
}

type s3CompleteMultipartRequest struct {
	XMLName xml.Name                  `xml:"CompleteMultipartUpload"`
	Parts   []s3CompleteMultipartPart `xml:"Part"`
}

func (s *s3Storage) Delete(ctx context.Context, filePath string) error {
	key := s.key(filePath)
	u := s.requestURL(key)
	dst := s.host("") + "/" + s.bucket
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(500*(1<<uint(attempt-1))) * time.Millisecond):
			}
		}
		log.Trace("s3", "delete attempt",
			"host", endpointHost(s.endpoint), "bucket", s.bucket, "key", key, "attempt", attempt, "status", "trace")
		resp, err := s.do(ctx, http.MethodDelete, u, nil, 0, "")
		if err != nil {
			if ctx.Err() != nil {
				return err
			}
			lastErr = err
			log.Debug("s3", "delete attempt failed, retrying",
				"host", endpointHost(s.endpoint), "bucket", s.bucket, "key", key,
				"attempt", attempt, "error", err.Error(), "status", "debug")
			continue
		}
		if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			return nil
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		resp.Body.Close()
		if !isRetryableStatus(resp.StatusCode) {
			return fmt.Errorf("s3: delete %s: %s: %s", dst, resp.Status, strings.TrimSpace(string(b)))
		}
		lastErr = fmt.Errorf("s3: delete %s: %s: %s", dst, resp.Status, strings.TrimSpace(string(b)))
		log.Debug("s3", "delete attempt failed, retrying",
			"host", endpointHost(s.endpoint), "bucket", s.bucket, "key", key,
			"attempt", attempt, "error", lastErr.Error(), "status", "debug")
	}
	if lastErr != nil {
		return lastErr
	}
	return nil
}

func (s *s3Storage) Download(ctx context.Context, filePath string) (io.ReadCloser, int64, error) {
	key := s.key(filePath)
	u := s.requestURL(key)
	dst := s.host("") + "/" + s.bucket
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, 0, ctx.Err()
			case <-time.After(time.Duration(500*(1<<uint(attempt-1))) * time.Millisecond):
			}
		}
		log.Trace("s3", "download attempt",
			"host", endpointHost(s.endpoint), "bucket", s.bucket, "key", key, "attempt", attempt, "status", "trace")
		resp, err := s.do(ctx, http.MethodGet, u, nil, 0, "")
		if err != nil {
			if ctx.Err() != nil {
				return nil, 0, err
			}
			lastErr = err
			log.Debug("s3", "download attempt failed, retrying",
				"host", endpointHost(s.endpoint), "bucket", s.bucket, "key", key,
				"attempt", attempt, "error", err.Error(), "status", "debug")
			continue
		}
		if resp.StatusCode == http.StatusOK {
			return resp.Body, resp.ContentLength, nil
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		resp.Body.Close()
		if !isRetryableStatus(resp.StatusCode) {
			return nil, 0, fmt.Errorf("s3: download %s: %s: %s", dst, resp.Status, strings.TrimSpace(string(b)))
		}
		lastErr = fmt.Errorf("s3: download %s: %s: %s", dst, resp.Status, strings.TrimSpace(string(b)))
		log.Debug("s3", "download attempt failed, retrying",
			"host", endpointHost(s.endpoint), "bucket", s.bucket, "key", key,
			"attempt", attempt, "error", lastErr.Error(), "status", "debug")
	}
	if lastErr != nil {
		return nil, 0, lastErr
	}
	return nil, 0, fmt.Errorf("s3: download %s: failed after retries", dst)
}

func (s *s3Storage) List(ctx context.Context, prefix string) ([]Entry, error) {
	searchPrefix := s.key(prefix)
	if searchPrefix != "" && !strings.HasSuffix(searchPrefix, "/") {
		searchPrefix += "/"
	}

	var entries []Entry
	continuation := ""
	dst := s.host("") + "/" + s.bucket
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
			log.Trace("s3", "list attempt",
				"host", endpointHost(s.endpoint), "bucket", s.bucket, "prefix", searchPrefix, "attempt", attempt, "status", "trace")
			resp, err = s.do(ctx, http.MethodGet, u, nil, 0, "")
			if err != nil {
				if ctx.Err() != nil {
					return nil, err
				}
				lastErr = err
				log.Debug("s3", "list attempt failed, retrying",
					"host", endpointHost(s.endpoint), "bucket", s.bucket, "prefix", searchPrefix,
					"attempt", attempt, "error", err.Error(), "status", "debug")
				continue
			}
			if resp.StatusCode != http.StatusOK {
				if isRetryableStatus(resp.StatusCode) {
					b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
					resp.Body.Close()
					lastErr = fmt.Errorf("s3: list %s: %s: %s", dst, resp.Status, strings.TrimSpace(string(b)))
					log.Debug("s3", "list attempt failed, retrying",
						"host", endpointHost(s.endpoint), "bucket", s.bucket, "prefix", searchPrefix,
						"attempt", attempt, "error", lastErr.Error(), "status", "debug")
					continue
				}
				b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
				resp.Body.Close()
				return nil, fmt.Errorf("s3: list %s: %s: %s", dst, resp.Status, strings.TrimSpace(string(b)))
			}
			lastErr = nil
			break
		}
		if lastErr != nil {
			return nil, lastErr
		}
		if resp == nil {
			return nil, fmt.Errorf("s3: list %s: failed after retries", dst)
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
	spool.Close()

	if n >= int64(s3MultipartThreshold) {
		return s.uploadMultipart(ctx, key, spoolPath, n)
	}

	h := sha256.New()
	f, err := os.Open(spoolPath)
	if err != nil {
		return 0, fmt.Errorf("s3 spool: %w", err)
	}
	if _, err := io.Copy(h, f); err != nil {
		_ = f.Close()
		return 0, fmt.Errorf("s3 spool hash: %w", err)
	}
	_ = f.Close()
	payloadHash := hex.EncodeToString(h.Sum(nil))

	return s.uploadSingle(ctx, key, spoolPath, n, payloadHash)
}

func (s *s3Storage) abortMultipartUpload(ctx context.Context, key, uploadID string) {
	_, _, u := s.multipartURLs(key, uploadID, 0)
	resp, err := s.do(ctx, http.MethodDelete, u, nil, 0, "")
	if err != nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	resp.Body.Close()
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
func (s *s3Storage) completeMultipartUpload(ctx context.Context, key, uploadID string, etags []string) error {
	_, _, u := s.multipartURLs(key, uploadID, 0)
	dst := s.host("") + "/" + s.bucket
	req := s3CompleteMultipartRequest{}
	for i, etag := range etags {
		req.Parts = append(req.Parts, s3CompleteMultipartPart{PartNumber: i + 1, ETag: etag})
	}
	payload, err := xml.Marshal(req)
	if err != nil {
		return fmt.Errorf("s3: multipart complete encode: %w", err)
	}
	full := append([]byte(xml.Header), payload...)
	sum := sha256.Sum256(full)
	payloadHash := hex.EncodeToString(sum[:])
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(500*(1<<uint(attempt-1))) * time.Millisecond):
			}
		}
		log.Trace("s3", "multipart complete attempt",
			"host", endpointHost(s.endpoint), "bucket", s.bucket, "key", key,
			"upload_id", uploadID, "parts", len(etags), "attempt", attempt, "status", "trace")
		resp, err := s.do(ctx, http.MethodPost, u, bytes.NewReader(full), int64(len(full)), payloadHash)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			lastErr = err
			continue
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 16384))
		ok := s3UploadOK(resp.StatusCode)
		resp.Body.Close()
		if !ok {
			if !isRetryableStatus(resp.StatusCode) {
				return fmt.Errorf("s3: multipart complete %s: %s: %s", dst, resp.Status, strings.TrimSpace(string(b)))
			}
			lastErr = fmt.Errorf("s3: multipart complete %s: %s: %s", dst, resp.Status, strings.TrimSpace(string(b)))
			continue
		}
		return nil
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("s3: multipart complete %s: failed after retries", dst)
}

func (s *s3Storage) createMultipartUpload(ctx context.Context, key string) (string, error) {
	base := s.requestURL(key)
	u := base + "?uploads"
	dst := s.host("") + "/" + s.bucket
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Duration(500*(1<<uint(attempt-1))) * time.Millisecond):
			}
		}
		log.Trace("s3", "multipart create attempt",
			"host", endpointHost(s.endpoint), "bucket", s.bucket, "key", key, "attempt", attempt, "status", "trace")
		resp, err := s.do(ctx, http.MethodPost, u, nil, 0, "")
		if err != nil {
			if ctx.Err() != nil {
				return "", err
			}
			lastErr = err
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16384))
		ok := s3UploadOK(resp.StatusCode)
		resp.Body.Close()
		if !ok {
			if !isRetryableStatus(resp.StatusCode) {
				return "", fmt.Errorf("s3: multipart create %s: %s: %s", dst, resp.Status, strings.TrimSpace(string(body)))
			}
			lastErr = fmt.Errorf("s3: multipart create %s: %s: %s", dst, resp.Status, strings.TrimSpace(string(body)))
			continue
		}
		var res s3InitiateMultipartResult
		if err := xml.Unmarshal(body, &res); err != nil {
			return "", fmt.Errorf("s3: multipart create decode: %w", err)
		}
		if res.UploadID == "" {
			return "", fmt.Errorf("s3: multipart create %s: empty upload id", dst)
		}
		return res.UploadID, nil
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", fmt.Errorf("s3: multipart create %s: failed after retries", dst)
}

func (s *s3Storage) creds() (string, string, error) {
	if s.keyID != "" {
		return s.keyID, s.keySec, nil
	}
	if s.role == "" {
		return "", "", fmt.Errorf("s3: no credentials (set storage.key_id/key_secret or attach an instance role)")
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
func endpointHost(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}
	return u.Host
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
func (s *s3Storage) multipartURLs(key, uploadID string, partNumber int) (createURL, partURL, completeURL string) {
	base := s.requestURL(key)
	createURL = base + "?uploads"
	encID := url.QueryEscape(uploadID)
	completeURL = base + "?uploadId=" + encID
	partURL = base + "?partNumber=" + fmt.Sprintf("%d", partNumber) + "&uploadId=" + encID
	return createURL, partURL, completeURL
}

func newS3Storage(opts map[string]string) (Storage, error) {
	bucket := opts["bucket"]
	if bucket == "" {
		return nil, fmt.Errorf("s3: bucket required")
	}

	endpoint, err := normalizeEndpoint(opts["endpoint"])
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 3600 * time.Second}
	if HasTLSOpts(opts) {
		client = TLSHTTPClient(opts)
		client.Timeout = 3600 * time.Second
	}

	s := &s3Storage{
		bucket:   bucket,
		prefix:   strings.Trim(strings.TrimPrefix(opts["path"], "/"), "/"),
		endpoint: endpoint,
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
	log.Debug("s3", "backend initialised",
		"host", endpointHost(s.endpoint), "bucket", s.bucket, "region", s.region,
		"key_id", s.keyID, "tls_verify", opts["tls_verify"], "status", "debug")
	return s, nil
}

func normalizeEndpoint(raw string) (string, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(raw), "/")
	if endpoint == "" {
		return "", nil
	}
	if !strings.Contains(endpoint, "://") {
		endpoint = "https://" + endpoint
	}
	if u, err := url.Parse(endpoint); err != nil || u.Host == "" {
		display := raw
		if u, err := url.Parse(endpoint); err == nil {
			display = u.Redacted()
		}
		return "", fmt.Errorf("s3: invalid endpoint %q (set S3_HOST to a hostname and S3_PROTOCOL to http or https)", display)
	}
	return endpoint, nil
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
func s3UploadOK(code int) bool {
	return code == http.StatusOK || code == http.StatusCreated || code == http.StatusNoContent
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
	log.Trace("s3", "sigv4 canonical request",
		"host", req.Host, "method", req.Method, "path", req.URL.EscapedPath(),
		"scope", scope, "canonical_request", canonicalRequest, "status", "trace")

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

func (s *s3Storage) uploadMultipart(ctx context.Context, key, spoolPath string, size int64) (int64, error) {
	dst := s.host("") + "/" + s.bucket
	partSize := int64(s3MultipartPartSize)
	if minPart := (size + int64(s3MultipartMaxParts) - 1) / int64(s3MultipartMaxParts); minPart > partSize {
		partSize = minPart
	}
	numParts := int((size + partSize - 1) / partSize)
	log.Debug("s3", "uploading object via multipart",
		"host", endpointHost(s.endpoint), "bucket", s.bucket, "key", key,
		"bytes", size, "parts", numParts, "part_size", partSize, "status", "debug")

	uploadID, err := s.createMultipartUpload(ctx, key)
	if err != nil {
		return 0, err
	}
	completed := false
	defer func() {
		if !completed {
			log.Debug("s3", "aborting multipart upload",
				"host", endpointHost(s.endpoint), "bucket", s.bucket, "key", key,
				"upload_id", uploadID, "status", "debug")
			s.abortMultipartUpload(context.Background(), key, uploadID)
		}
	}()

	f, err := os.Open(spoolPath)
	if err != nil {
		return 0, fmt.Errorf("s3: upload %s: reopen spool: %w", dst, err)
	}
	defer func() { _ = f.Close() }()

	buf := make([]byte, partSize)
	etags := make([]string, numParts)
	for i := 0; i < numParts; i++ {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		default:
		}
		off := int64(i) * partSize
		curLen := partSize
		if off+curLen > size {
			curLen = size - off
		}
		if _, err := f.Seek(off, io.SeekStart); err != nil {
			return 0, fmt.Errorf("s3: multipart part %d: seek: %w", i+1, err)
		}
		if _, err := io.ReadFull(f, buf[:curLen]); err != nil {
			return 0, fmt.Errorf("s3: multipart part %d: read: %w", i+1, err)
		}
		etag, err := s.uploadMultipartPart(ctx, key, uploadID, i+1, buf[:curLen])
		if err != nil {
			return 0, err
		}
		etags[i] = etag
		if (i+1)%10 == 0 || i+1 == numParts {
			log.Debug("s3", "multipart progress",
				"host", endpointHost(s.endpoint), "bucket", s.bucket, "key", key,
				"upload_id", uploadID, "part", i+1, "parts", numParts, "status", "debug")
		}
	}

	if err := s.completeMultipartUpload(ctx, key, uploadID, etags); err != nil {
		return 0, err
	}
	completed = true
	log.Debug("s3", "multipart upload complete",
		"host", endpointHost(s.endpoint), "bucket", s.bucket, "key", key,
		"bytes", size, "parts", numParts, "upload_id", uploadID, "status", "debug")
	return size, nil
}

func (s *s3Storage) uploadMultipartPart(ctx context.Context, key, uploadID string, partNumber int, chunk []byte) (string, error) {
	_, partURL, _ := s.multipartURLs(key, uploadID, partNumber)
	dst := s.host("") + "/" + s.bucket
	sum := sha256.Sum256(chunk)
	payloadHash := hex.EncodeToString(sum[:])
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Duration(500*(1<<uint(attempt-1))) * time.Millisecond):
			}
		}
		log.Trace("s3", "multipart part attempt",
			"host", endpointHost(s.endpoint), "bucket", s.bucket, "key", key,
			"upload_id", uploadID, "part", partNumber, "bytes", len(chunk), "attempt", attempt, "status", "trace")
		resp, err := s.do(ctx, http.MethodPut, partURL, bytes.NewReader(chunk), int64(len(chunk)), payloadHash)
		if err != nil {
			if ctx.Err() != nil {
				return "", err
			}
			lastErr = err
			continue
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		etag := strings.TrimSpace(resp.Header.Get("ETag"))
		ok := s3UploadOK(resp.StatusCode)
		resp.Body.Close()
		if !ok {
			if !isRetryableStatus(resp.StatusCode) {
				return "", fmt.Errorf("s3: multipart part %d %s: %s: %s", partNumber, dst, resp.Status, strings.TrimSpace(string(b)))
			}
			lastErr = fmt.Errorf("s3: multipart part %d %s: %s: %s", partNumber, dst, resp.Status, strings.TrimSpace(string(b)))
			continue
		}
		if etag == "" {
			return "", fmt.Errorf("s3: multipart part %d %s: missing ETag", partNumber, dst)
		}
		return etag, nil
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", fmt.Errorf("s3: multipart part %d %s: failed after retries", partNumber, dst)
}

func (s *s3Storage) uploadSingle(ctx context.Context, key, spoolPath string, n int64, payloadHash string) (int64, error) {
	u := s.requestURL(key)
	dst := s.host("") + "/" + s.bucket
	log.Debug("s3", "uploading object",
		"host", endpointHost(s.endpoint), "bucket", s.bucket, "key", key, "bytes", n, "status", "debug")
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			case <-time.After(time.Duration(500*(1<<uint(attempt-1))) * time.Millisecond):
			}
		}
		body, err := os.Open(spoolPath)
		if err != nil {
			return 0, fmt.Errorf("s3: upload %s: reopen spool: %w", dst, err)
		}
		log.Trace("s3", "upload attempt",
			"host", endpointHost(s.endpoint), "bucket", s.bucket, "key", key, "attempt", attempt, "status", "trace")
		resp, err := s.do(ctx, http.MethodPut, u, body, n, payloadHash)
		_ = body.Close()
		if err != nil {
			if ctx.Err() != nil {
				return 0, err
			}
			lastErr = err
			log.Debug("s3", "upload attempt failed, retrying",
				"host", endpointHost(s.endpoint), "bucket", s.bucket, "key", key,
				"attempt", attempt, "error", err.Error(), "status", "debug")
			continue
		}
		if s3UploadOK(resp.StatusCode) {
			resp.Body.Close()
			log.Debug("s3", "upload complete",
				"host", endpointHost(s.endpoint), "bucket", s.bucket, "key", key,
				"bytes", n, "attempts", attempt+1, "status", "debug")
			return n, nil
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if !isRetryableStatus(resp.StatusCode) {
			return 0, fmt.Errorf("s3: upload %s: %s: %s", dst, resp.Status, strings.TrimSpace(string(b)))
		}
		lastErr = fmt.Errorf("s3: upload %s: %s: %s", dst, resp.Status, strings.TrimSpace(string(b)))
		log.Debug("s3", "upload attempt failed, retrying",
			"host", endpointHost(s.endpoint), "bucket", s.bucket, "key", key,
			"attempt", attempt, "error", lastErr.Error(), "status", "debug")
	}
	if lastErr != nil {
		return 0, lastErr
	}
	return 0, fmt.Errorf("s3: upload %s: failed after retries", dst)
}
