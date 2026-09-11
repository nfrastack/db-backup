// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package influx

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nfrastack/db-backup/internal/log"
)

var influxBackupMode = "native" // logical

const (
	nativeManifestVersion   = 2
	nativeManifestExtension = "manifest"
	nativeBackupTimeFormat  = "20060102T150405Z"

	nativeCompressionNone = 0
	nativeCompressionGzip = 1
)

type nativeFileEntry struct {
	FileName    string `json:"fileName"`
	Size        int64  `json:"size"`
	Compression int    `json:"compression"`
}

type nativeManifest struct {
	Version int                 `json:"manifestVersion"`
	KV      nativeFileEntry     `json:"kv"`
	SQL     *nativeFileEntry    `json:"sql,omitempty"`
	Buckets []nativeBucketEntry `json:"buckets"`
}

type nativeBucketEntry struct {
	OrganizationID         string                  `json:"organizationID"`
	OrganizationName       string                  `json:"organizationName"`
	BucketID               string                  `json:"bucketID"`
	BucketName             string                  `json:"bucketName"`
	Description            *string                 `json:"description,omitempty"`
	DefaultRetentionPolicy string                  `json:"defaultRetentionPolicy"`
	RetentionPolicies      []nativeRetentionPolicy `json:"retentionPolicies"`
}

type nativeRetentionPolicy struct {
	Name               string               `json:"name"`
	ReplicaN           int32                `json:"replicaN"`
	Duration           int64                `json:"duration"`
	ShardGroupDuration int64                `json:"shardGroupDuration"`
	ShardGroups        []nativeShardGroup   `json:"shardGroups"`
	Subscriptions      []nativeSubscription `json:"subscriptions"`
}

type nativeShardGroup struct {
	ID          int64              `json:"id"`
	StartTime   time.Time          `json:"startTime"`
	EndTime     time.Time          `json:"endTime"`
	DeletedAt   *time.Time         `json:"deletedAt,omitempty"`
	TruncatedAt *time.Time         `json:"truncatedAt,omitempty"`
	Shards      []nativeShardEntry `json:"shards"`
}

type nativeShardEntry struct {
	ID          int64              `json:"id"`
	ShardOwners []nativeShardOwner `json:"shardOwners"`
	nativeFileEntry
}

type nativeShardOwner struct {
	NodeID int64 `json:"nodeID"`
}

type nativeSubscription struct {
	Name         string   `json:"name"`
	Mode         string   `json:"mode"`
	Destinations []string `json:"destinations"`
}

type serverBucketManifest struct {
	OrganizationID         string     `json:"organizationID"`
	OrganizationName       string     `json:"organizationName"`
	BucketID               string     `json:"bucketID"`
	BucketName             string     `json:"bucketName"`
	Description            *string    `json:"description"`
	DefaultRetentionPolicy string     `json:"defaultRetentionPolicy"`
	RetentionPolicies      []serverRP `json:"retentionPolicies"`
}

type serverRP struct {
	Name               string             `json:"name"`
	ReplicaN           int32              `json:"replicaN"`
	Duration           int64              `json:"duration"`
	ShardGroupDuration int64              `json:"shardGroupDuration"`
	ShardGroups        []serverShardGroup `json:"shardGroups"`
	Subscriptions      []serverSub        `json:"subscriptions"`
}

type serverShardGroup struct {
	ID          int64         `json:"id"`
	StartTime   time.Time     `json:"startTime"`
	EndTime     time.Time     `json:"endTime"`
	DeletedAt   *time.Time    `json:"deletedAt,omitempty"`
	TruncatedAt *time.Time    `json:"truncatedAt,omitempty"`
	Shards      []serverShard `json:"shards"`
}

type serverShard struct {
	ID          int64              `json:"id"`
	ShardOwners []nativeShardOwner `json:"shardOwners"`
}

type serverSub struct {
	Name         string   `json:"name"`
	Mode         string   `json:"mode"`
	Destinations []string `json:"destinations"`
}

func (d *Dumper) NativeBackup(w io.Writer, dbNames []string) error {
	if d.Version() == 1 {
		return fmt.Errorf("influx: native backup requires InfluxDB v2 (2.1+)")
	}
	if ok, err := d.serverAtLeast21(); err != nil || !ok {
		if err != nil {
			return fmt.Errorf("influx: version probe: %w", err)
		}
		return fmt.Errorf("influx: native backup requires server 2.1+")
	}

	base := time.Now().UTC().Format(nativeBackupTimeFormat)
	bw := bufio.NewWriterSize(w, 1<<20)
	tw := tar.NewWriter(bw)

	manifest := nativeManifest{Version: nativeManifestVersion}
	tmpDir, err := os.MkdirTemp("", "influx-native-")
	if err != nil {
		return fmt.Errorf("influx: temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	buckets, kvEntry, sqlEntry, err := d.downloadMetadata(tw, tmpDir, base)
	if err != nil {
		return err
	}
	manifest.KV = kvEntry
	manifest.SQL = sqlEntry

	wantAll := len(dbNames) == 1 && strings.EqualFold(dbNames[0], "all")
	var matched []serverBucketManifest
	for _, b := range buckets {
		if strings.HasPrefix(b.BucketName, "_") && wantAll {
			log.Trace("influx", "skipping system bucket", "bucket", b.BucketName)
			continue
		}
		if wantAll || containsName(dbNames, b.BucketName) {
			matched = append(matched, b)
		}
	}
	if len(matched) == 0 {
		return fmt.Errorf("influx: native backup matched no buckets")
	}
	log.Debug("influx", "native buckets matched", "count", len(matched))

	manifest.Buckets = make([]nativeBucketEntry, 0, len(matched))
	for _, b := range matched {
		be, err := d.downloadBucket(tw, tmpDir, base, b)
		if err != nil {
			return err
		}
		manifest.Buckets = append(manifest.Buckets, be)
	}

	mName := base + "." + nativeManifestExtension
	var mBuf strings.Builder
	enc := json.NewEncoder(&mBuf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(manifest); err != nil {
		return fmt.Errorf("influx: encode manifest: %w", err)
	}
	if err := tw.WriteHeader(&tar.Header{Name: mName, Mode: 0o600, Size: int64(mBuf.Len())}); err != nil {
		return fmt.Errorf("influx: manifest header: %w", err)
	}
	if _, err := io.WriteString(tw, mBuf.String()); err != nil {
		return fmt.Errorf("influx: manifest write: %w", err)
	}
	log.Debug("influx", "native manifest written", "file", mName, "buckets", len(manifest.Buckets))

	if err := tw.Close(); err != nil {
		return fmt.Errorf("influx: close tar: %w", err)
	}
	if err := bw.Flush(); err != nil {
		return fmt.Errorf("influx: flush: %w", err)
	}
	log.Debug("influx", "native backup done", "buckets", len(manifest.Buckets))
	return nil
}

func containsName(names []string, want string) bool {
	for _, n := range names {
		if strings.EqualFold(strings.TrimSpace(n), want) {
			return true
		}
	}
	return false
}

func (d *Dumper) serverAtLeast21() (bool, error) {
	u := d.baseURL() + "/health"
	req, err := http.NewRequestWithContext(d.ctxOrBg(), "GET", u, nil)
	if err != nil {
		return false, err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	var h struct {
		Version string `json:"version"`
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	if err := json.Unmarshal(body, &h); err != nil {
		return false, fmt.Errorf("decode health: %w", err)
	}
	v := strings.TrimPrefix(strings.TrimSpace(h.Version), "v")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return false, fmt.Errorf("unparseable version %q", h.Version)
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return false, fmt.Errorf("unparseable version %q", h.Version)
	}
	return major == 2 && minor >= 1, nil
}

func (d *Dumper) downloadMetadata(tw *tar.Writer, tmpDir, base string) ([]serverBucketManifest, nativeFileEntry, *nativeFileEntry, error) {
	var buckets []serverBucketManifest
	var kvEntry nativeFileEntry
	var sqlEntry *nativeFileEntry

	u := d.baseURL() + "/api/v2/backup/metadata"
	start := time.Now()
	req, err := http.NewRequestWithContext(d.ctxOrBg(), "GET", u, nil)
	if err != nil {
		return nil, kvEntry, nil, err
	}
	req.Header.Set("Accept-Encoding", "gzip")
	d.setAuth(req)
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, kvEntry, nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, kvEntry, nil, fmt.Errorf("influx: backup metadata: HTTP %d - native backup requires an operator token", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, kvEntry, nil, fmt.Errorf("influx: backup metadata: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	body := io.Reader(resp.Body)
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gzr, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, kvEntry, nil, fmt.Errorf("influx: gunzip metadata: %w", err)
		}
		defer gzr.Close()
		body = gzr
	}
	_, params, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil {
		return nil, kvEntry, nil, fmt.Errorf("influx: metadata content-type: %w", err)
	}
	mr := multipart.NewReader(body, params["boundary"])
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, kvEntry, nil, fmt.Errorf("influx: metadata part: %w", err)
		}
		_, partParams, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
		if err != nil {
			return nil, kvEntry, nil, fmt.Errorf("influx: part disposition: %w", err)
		}
		switch name := partParams["name"]; name {
		case "kv":
			entry, err := d.tarStagedEntry(tw, tmpDir, base+".bolt", part)
			if err != nil {
				return nil, kvEntry, nil, fmt.Errorf("influx: kv snapshot: %w", err)
			}
			kvEntry = entry
		case "sql":
			entry, err := d.tarStagedEntry(tw, tmpDir, base+".sqlite", part)
			if err != nil {
				return nil, kvEntry, nil, fmt.Errorf("influx: sql snapshot: %w", err)
			}
			sqlEntry = &entry
		case "buckets":
			if err := json.NewDecoder(part).Decode(&buckets); err != nil {
				return nil, kvEntry, nil, fmt.Errorf("influx: decode bucket manifest: %w", err)
			}
		default:
			return nil, kvEntry, nil, fmt.Errorf("influx: unexpected metadata part %q", name)
		}
	}
	log.Debug("influx", "native metadata done",
		"buckets", len(buckets), "has_sql", sqlEntry != nil,
		"elapsed", time.Since(start).Round(time.Millisecond).String())
	if len(buckets) == 0 {
		return nil, kvEntry, nil, fmt.Errorf("influx: metadata contained no buckets")
	}
	return buckets, kvEntry, sqlEntry, nil
}

func (d *Dumper) downloadBucket(tw *tar.Writer, tmpDir, base string, b serverBucketManifest) (nativeBucketEntry, error) {
	be := nativeBucketEntry{
		OrganizationID:         b.OrganizationID,
		OrganizationName:       b.OrganizationName,
		BucketID:               b.BucketID,
		BucketName:             b.BucketName,
		Description:            b.Description,
		DefaultRetentionPolicy: b.DefaultRetentionPolicy,
		RetentionPolicies:      make([]nativeRetentionPolicy, len(b.RetentionPolicies)),
	}
	for i, rp := range b.RetentionPolicies {
		nrp := nativeRetentionPolicy{
			Name:               rp.Name,
			ReplicaN:           rp.ReplicaN,
			Duration:           rp.Duration,
			ShardGroupDuration: rp.ShardGroupDuration,
			ShardGroups:        make([]nativeShardGroup, len(rp.ShardGroups)),
			Subscriptions:      make([]nativeSubscription, len(rp.Subscriptions)),
		}
		for j, sub := range rp.Subscriptions {
			nrp.Subscriptions[j] = nativeSubscription{Name: sub.Name, Mode: sub.Mode, Destinations: sub.Destinations}
		}
		for k, sg := range rp.ShardGroups {
			nsg := nativeShardGroup{
				ID: sg.ID, StartTime: sg.StartTime, EndTime: sg.EndTime,
				DeletedAt: sg.DeletedAt, TruncatedAt: sg.TruncatedAt,
				Shards: make([]nativeShardEntry, 0, len(sg.Shards)),
			}
			for _, sh := range sg.Shards {
				entry, err := d.downloadShard(tw, tmpDir, base, sh.ID)
				if err != nil {
					return nativeBucketEntry{}, err
				}
				if entry == nil {
					continue
				}
				owners := make([]nativeShardOwner, len(sh.ShardOwners))
				for o, owner := range sh.ShardOwners {
					owners[o] = nativeShardOwner{NodeID: owner.NodeID}
				}
				nsg.Shards = append(nsg.Shards, nativeShardEntry{
					ID: sh.ID, ShardOwners: owners, nativeFileEntry: *entry,
				})
			}
			nrp.ShardGroups[k] = nsg
		}
		be.RetentionPolicies[i] = nrp
	}
	return be, nil
}

func (d *Dumper) downloadShard(tw *tar.Writer, tmpDir, base string, shardID int64) (*nativeFileEntry, error) {
	start := time.Now()
	u := d.baseURL() + "/api/v2/backup/shards/" + strconv.FormatInt(shardID, 10)
	req, err := http.NewRequestWithContext(d.ctxOrBg(), "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept-Encoding", "gzip")
	d.setAuth(req)
	resp, err := d.streamClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		log.Debug("influx", "shard removed during backup", "shard", shardID)
		return nil, nil
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("shard %d: HTTP %d - native backup requires an operator token", shardID, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("shard %d: HTTP %d: %s", shardID, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	name := fmt.Sprintf("%s.%d.tar", base, shardID)
	compression := nativeCompressionNone
	body := io.Reader(resp.Body)
	if resp.Header.Get("Content-Encoding") == "gzip" {
		name += ".gz"
		compression = nativeCompressionGzip
	}

	var size int64
	if resp.ContentLength > 0 {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: resp.ContentLength}); err != nil {
			return nil, fmt.Errorf("shard %d header: %w", shardID, err)
		}
		size, err = io.Copy(tw, body)
		if err != nil {
			return nil, fmt.Errorf("shard %d stream: %w", shardID, err)
		}
	} else {
		staged, err := stageToTemp(tmpDir, body)
		if err != nil {
			return nil, fmt.Errorf("shard %d stage: %w", shardID, err)
		}
		size, err = tarFileEntry(tw, name, staged)
		os.Remove(staged)
		if err != nil {
			return nil, fmt.Errorf("shard %d tar: %w", shardID, err)
		}
	}
	log.Trace("influx", "shard done",
		"shard", shardID, "file", name, "bytes", size,
		"elapsed", time.Since(start).Round(time.Millisecond).String())
	return &nativeFileEntry{FileName: name, Size: size, Compression: compression}, nil
}

func (d *Dumper) tarStagedEntry(tw *tar.Writer, tmpDir, name string, r io.Reader) (nativeFileEntry, error) {
	_ = d
	staged, err := stageToTemp(tmpDir, r)
	if err != nil {
		return nativeFileEntry{}, err
	}
	size, err := tarFileEntry(tw, name, staged)
	os.Remove(staged)
	if err != nil {
		return nativeFileEntry{}, err
	}
	log.Trace("influx", "metadata file stored", "file", name, "bytes", size)
	return nativeFileEntry{FileName: name, Size: size, Compression: nativeCompressionNone}, nil
}

func stageToTemp(tmpDir string, r io.Reader) (string, error) {
	f, err := os.CreateTemp(tmpDir, "part-")
	if err != nil {
		return "", err
	}
	_, err = io.Copy(f, r)
	cerr := f.Close()
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	if cerr != nil {
		os.Remove(f.Name())
		return "", cerr
	}
	return f.Name(), nil
}

func tarFileEntry(tw *tar.Writer, name, path string) (int64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	if err := tw.WriteHeader(&tar.Header{Name: filepath.Base(name), Mode: 0o600, Size: fi.Size()}); err != nil {
		return 0, err
	}
	n, err := io.Copy(tw, f)
	if err != nil {
		return 0, err
	}
	return n, nil
}
