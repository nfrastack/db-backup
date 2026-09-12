// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package influx

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"encoding/json"
	"errors"
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

// influxBackupMode selects the backup protocol for v2:
//
//	"physical": TSM shard snapshots (CLI protocol; needs operator token).
//	"logical":  InfluxQL export to line protocol (bucket tokens, v1).
//
// v1 always uses logical. On v2, a 403 from the physical path falls back
// to logical with a warning. A var (not const) so tests can flip it;
// treat as a build-time toggle.
var influxBackupMode = "physical"

// errOperatorRequired marks physical-protocol 403s eligible for fallback.
var errOperatorRequired = errors.New("influx: operator token required")

// IsOperatorError reports 401/403-style failures eligible for fallback.
// Exported for incremental orchestration outside this package.
func IsOperatorError(err error) bool {
	return errors.Is(err, errOperatorRequired)
}

const (
	physicalManifestVersion   = 2
	physicalManifestExtension = "manifest"
	physicalBackupTimeFormat  = "20060102T150405Z"

	physicalCompressionNone = 0
	physicalCompressionGzip = 1
)

type physicalFileEntry struct {
	FileName    string `json:"fileName"`
	Size        int64  `json:"size"`
	Compression int    `json:"compression"`
}

type physicalManifest struct {
	Version int                   `json:"manifestVersion"`
	KV      physicalFileEntry     `json:"kv"`
	SQL     *physicalFileEntry    `json:"sql,omitempty"`
	Buckets []physicalBucketEntry `json:"buckets"`
}

type physicalBucketEntry struct {
	OrganizationID         string                    `json:"organizationID"`
	OrganizationName       string                    `json:"organizationName"`
	BucketID               string                    `json:"bucketID"`
	BucketName             string                    `json:"bucketName"`
	Description            *string                   `json:"description,omitempty"`
	DefaultRetentionPolicy string                    `json:"defaultRetentionPolicy"`
	RetentionPolicies      []physicalRetentionPolicy `json:"retentionPolicies"`
}

type physicalRetentionPolicy struct {
	Name               string                 `json:"name"`
	ReplicaN           int32                  `json:"replicaN"`
	Duration           int64                  `json:"duration"`
	ShardGroupDuration int64                  `json:"shardGroupDuration"`
	ShardGroups        []physicalShardGroup   `json:"shardGroups"`
	Subscriptions      []physicalSubscription `json:"subscriptions"`
}

type physicalShardGroup struct {
	ID          int64                `json:"id"`
	StartTime   time.Time            `json:"startTime"`
	EndTime     time.Time            `json:"endTime"`
	DeletedAt   *time.Time           `json:"deletedAt,omitempty"`
	TruncatedAt *time.Time           `json:"truncatedAt,omitempty"`
	Shards      []physicalShardEntry `json:"shards"`
}

type physicalShardEntry struct {
	ID          int64                `json:"id"`
	ShardOwners []physicalShardOwner `json:"shardOwners"`
	physicalFileEntry
}

type physicalShardOwner struct {
	NodeID int64 `json:"nodeID"`
}

type physicalSubscription struct {
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
	ID          int64                `json:"id"`
	ShardOwners []physicalShardOwner `json:"shardOwners"`
}

type serverSub struct {
	Name         string   `json:"name"`
	Mode         string   `json:"mode"`
	Destinations []string `json:"destinations"`
}

func (d *Dumper) PhysicalBackup(w io.Writer, dbNames []string) error {
	return d.physicalBackupRange(w, dbNames, "", "")
}

// PhysicalBackupRange writes one bounded slice. Empty since yields a full
// backup. Non-empty since fetches metadata with ?since= and skips shards
// fully predating since-overlap (shard-granular incremental; boundary
// shards re-fetch fully and converge idempotently on replay).
func (d *Dumper) PhysicalBackupRange(w io.Writer, dbNames []string, since, end string) error {
	return d.physicalBackupRange(w, dbNames, since, end)
}

func (d *Dumper) physicalBackupRange(w io.Writer, dbNames []string, since, end string) error {
	if d.Version() == 1 {
		return fmt.Errorf("influx: physical backup requires InfluxDB v2 (2.1+)")
	}
	if strings.TrimSpace(d.user) == "" || strings.TrimSpace(d.pass) == "" {
		return fmt.Errorf("influx v2 backup requires org (user) and token (pass) - got org %q", d.user)
	}
	if ok, err := d.ServerAtLeast21(); err != nil || !ok {
		if err != nil {
			return fmt.Errorf("influx: version probe: %w", err)
		}
		return fmt.Errorf("influx: physical backup requires server 2.1+")
	}

	cutoff, upper, bounded := sliceCutoff(since, end)
	label := "full"
	if bounded {
		label = "slice"
		log.Debug("influx", "physical slice",
			"since", since, "cutoff", cutoff.Format(time.RFC3339), "upper", upper.Format(time.RFC3339))
	}

	base := time.Now().UTC().Format(physicalBackupTimeFormat)
	bw := bufio.NewWriterSize(w, 1<<20)
	tw := tar.NewWriter(bw)

	manifest := physicalManifest{Version: physicalManifestVersion}
	tmpDir, err := os.MkdirTemp("", "influx-native-")
	if err != nil {
		return fmt.Errorf("influx: temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	buckets, kvEntry, sqlEntry, err := d.downloadMetadata(tw, tmpDir, base, since)
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
		return fmt.Errorf("influx: physical backup matched no buckets")
	}
	log.Debug("influx", "physical buckets matched", "count", len(matched))

	manifest.Buckets = make([]physicalBucketEntry, 0, len(matched))
	for _, b := range matched {
		be, skipped, err := d.downloadBucket(tw, tmpDir, base, b, cutoff, upper, bounded)
		if err != nil {
			return err
		}
		manifest.Buckets = append(manifest.Buckets, be)
		if skipped > 0 {
			log.Debug("influx", "physical shards skipped (predate slice)",
				"bucket", b.BucketName, "skipped", skipped)
		}
	}

	mName := base + "." + physicalManifestExtension
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
	log.Debug("influx", "physical manifest written", "file", mName, "buckets", len(manifest.Buckets))

	if err := tw.Close(); err != nil {
		return fmt.Errorf("influx: close tar: %w", err)
	}
	if err := bw.Flush(); err != nil {
		return fmt.Errorf("influx: flush: %w", err)
	}
	log.Debug("influx", "physical backup done",
		"buckets", len(manifest.Buckets), "mode", label)
	return nil
}

// sliceCutoff resolves the shard-skip cutoff (since minus overlap) and the
// exclusive upper bound for a physical slice. ok=false means full backup.
func sliceCutoff(since, end string) (cutoff, upper time.Time, ok bool) {
	if strings.TrimSpace(since) == "" {
		return time.Time{}, time.Time{}, false
	}
	sinceT, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(since))
	if err != nil {
		if sinceT, err = time.Parse(time.RFC3339, strings.TrimSpace(since)); err != nil {
			return time.Time{}, time.Time{}, false
		}
	}
	upper = time.Now().UTC()
	if strings.TrimSpace(end) != "" {
		if endT, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(end)); err == nil {
			upper = endT
		} else if endT, err := time.Parse(time.RFC3339, strings.TrimSpace(end)); err == nil {
			upper = endT
		}
	}
	return sinceT.Add(-incrementalOverlap).UTC(), upper.UTC(), true
}

func containsName(names []string, want string) bool {
	for _, n := range names {
		if strings.EqualFold(strings.TrimSpace(n), want) {
			return true
		}
	}
	return false
}

func (d *Dumper) ServerAtLeast21() (bool, error) {
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

func (d *Dumper) downloadMetadata(tw *tar.Writer, tmpDir, base, since string) ([]serverBucketManifest, physicalFileEntry, *physicalFileEntry, error) {
	var buckets []serverBucketManifest
	var kvEntry physicalFileEntry
	var sqlEntry *physicalFileEntry

	u := d.baseURL() + "/api/v2/backup/metadata"
	start := time.Now()
	req, err := http.NewRequestWithContext(d.ctxOrBg(), "GET", u, nil)
	if err != nil {
		return nil, kvEntry, nil, err
	}
	if strings.TrimSpace(since) != "" {
		// The spec carries `since` as a header; send it as a query param
		// too — servers ignore unknown query params, and either or both
		// may be honored depending on version.
		sinceT := strings.TrimSpace(since)
		q := req.URL.Query()
		q.Set("since", sinceT)
		req.URL.RawQuery = q.Encode()
		req.Header.Set("Since", sinceT)
	}
	req.Header.Set("Accept-Encoding", "gzip")
	d.setAuth(req)
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, kvEntry, nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, kvEntry, nil, fmt.Errorf("influx: backup metadata: HTTP %d: %w", resp.StatusCode, errOperatorRequired)
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
	log.Debug("influx", "physical metadata done",
		"buckets", len(buckets), "has_sql", sqlEntry != nil,
		"elapsed", time.Since(start).Round(time.Millisecond).String())
	if len(buckets) == 0 {
		return nil, kvEntry, nil, fmt.Errorf("influx: metadata contained no buckets")
	}
	return buckets, kvEntry, sqlEntry, nil
}

// downloadBucket downloads all shards of one bucket in range and builds its
// manifest entry. Shards fully predating cutoff (and shards starting after
// upper, when bounded) are skipped; the skip count is returned.
func (d *Dumper) downloadBucket(tw *tar.Writer, tmpDir, base string, b serverBucketManifest, cutoff, upper time.Time, bounded bool) (physicalBucketEntry, int, error) {
	be := physicalBucketEntry{
		OrganizationID:         b.OrganizationID,
		OrganizationName:       b.OrganizationName,
		BucketID:               b.BucketID,
		BucketName:             b.BucketName,
		Description:            b.Description,
		DefaultRetentionPolicy: b.DefaultRetentionPolicy,
		RetentionPolicies:      make([]physicalRetentionPolicy, len(b.RetentionPolicies)),
	}
	skipped := 0
	for i, rp := range b.RetentionPolicies {
		nrp := physicalRetentionPolicy{
			Name:               rp.Name,
			ReplicaN:           rp.ReplicaN,
			Duration:           rp.Duration,
			ShardGroupDuration: rp.ShardGroupDuration,
			ShardGroups:        make([]physicalShardGroup, len(rp.ShardGroups)),
			Subscriptions:      make([]physicalSubscription, len(rp.Subscriptions)),
		}
		for j, sub := range rp.Subscriptions {
			nrp.Subscriptions[j] = physicalSubscription{Name: sub.Name, Mode: sub.Mode, Destinations: sub.Destinations}
		}
		for k, sg := range rp.ShardGroups {
			nsg := physicalShardGroup{
				ID: sg.ID, StartTime: sg.StartTime, EndTime: sg.EndTime,
				DeletedAt: sg.DeletedAt, TruncatedAt: sg.TruncatedAt,
				Shards: make([]physicalShardEntry, 0, len(sg.Shards)),
			}
			if bounded && !sg.EndTime.IsZero() && !sg.EndTime.After(cutoff) {
				skipped++
				nrp.ShardGroups[k] = nsg
				continue
			}
			if bounded && !sg.StartTime.IsZero() && sg.StartTime.After(upper) {
				skipped++
				nrp.ShardGroups[k] = nsg
				continue
			}
			for _, sh := range sg.Shards {
				entry, err := d.downloadShard(tw, tmpDir, base, sh.ID)
				if err != nil {
					return physicalBucketEntry{}, skipped, err
				}
				if entry == nil {
					continue
				}
				owners := make([]physicalShardOwner, len(sh.ShardOwners))
				for o, owner := range sh.ShardOwners {
					owners[o] = physicalShardOwner{NodeID: owner.NodeID}
				}
				nsg.Shards = append(nsg.Shards, physicalShardEntry{
					ID: sh.ID, ShardOwners: owners, physicalFileEntry: *entry,
				})
			}
			nrp.ShardGroups[k] = nsg
		}
		be.RetentionPolicies[i] = nrp
	}
	return be, skipped, nil
}

func (d *Dumper) downloadShard(tw *tar.Writer, tmpDir, base string, shardID int64) (*physicalFileEntry, error) {
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
		return nil, fmt.Errorf("shard %d: HTTP %d: %w", shardID, resp.StatusCode, errOperatorRequired)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("shard %d: HTTP %d: %s", shardID, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	name := fmt.Sprintf("%s.%d.tar", base, shardID)
	compression := physicalCompressionNone
	body := io.Reader(resp.Body)
	if resp.Header.Get("Content-Encoding") == "gzip" {
		name += ".gz"
		compression = physicalCompressionGzip
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
	return &physicalFileEntry{FileName: name, Size: size, Compression: compression}, nil
}

func (d *Dumper) tarStagedEntry(tw *tar.Writer, tmpDir, name string, r io.Reader) (physicalFileEntry, error) {
	_ = d
	staged, err := stageToTemp(tmpDir, r)
	if err != nil {
		return physicalFileEntry{}, err
	}
	size, err := tarFileEntry(tw, name, staged)
	os.Remove(staged)
	if err != nil {
		return physicalFileEntry{}, err
	}
	log.Trace("influx", "metadata file stored", "file", name, "bytes", size)
	return physicalFileEntry{FileName: name, Size: size, Compression: physicalCompressionNone}, nil
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
