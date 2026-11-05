// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package influx

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nfrastack/db-backup/internal/log"
)

func (c *closeReader) Close() error { return c.closer.Close() }

func IsTarStream(peek []byte) bool {
	return isTarStream(peek)
}

func MergePhysicalTarFiles(paths []string, w io.Writer) error {
	var manifests []physicalManifest
	byPath := map[string]string{}
	var names []string
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("influx: merge open: %w", err)
		}
		tr := tar.NewReader(f)
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				f.Close()
				return fmt.Errorf("influx: merge tar read: %w", err)
			}
			if hdr.Typeflag != tar.TypeReg {
				continue
			}
			name := filepath.Base(hdr.Name)
			if strings.HasSuffix(name, "."+physicalManifestExtension) {
				body, err := io.ReadAll(tr)
				if err != nil {
					f.Close()
					return fmt.Errorf("influx: merge manifest read: %w", err)
				}
				var pm physicalManifest
				if err := json.Unmarshal(body, &pm); err != nil {
					f.Close()
					return fmt.Errorf("influx: merge manifest decode: %w", err)
				}
				manifests = append(manifests, pm)
			} else {
				if _, ok := byPath[name]; !ok {
					names = append(names, name)
				}
				byPath[name] = path
				if _, err := io.Copy(io.Discard, tr); err != nil {
					f.Close()
					return fmt.Errorf("influx: merge tar skip: %w", err)
				}
			}
		}
		f.Close()
	}
	merged, ordered, err := mergeSets(manifests, names)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(w)
	err = writeMerged(tw, merged, func(name string) (io.Reader, int64, error) {
		f, err := os.Open(byPath[name])
		if err != nil {
			return nil, 0, err
		}
		tr := tar.NewReader(f)
		for {
			hdr, err := tr.Next()
			if err != nil {
				f.Close()
				return nil, 0, fmt.Errorf("influx: merge re-read %q: %w", name, err)
			}
			if hdr.Typeflag == tar.TypeReg && filepath.Base(hdr.Name) == name {
				return &closeReader{Reader: tr, closer: f}, hdr.Size, nil
			}
		}
	}, ordered)
	if err != nil {
		return err
	}
	return tw.Close()
}

func MergePhysicalTars(datas [][]byte) ([]byte, error) {
	files := map[string]fileBlob{}
	var manifests []physicalManifest
	for _, data := range datas {
		tr := tar.NewReader(bytes.NewReader(data))
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("influx: merge tar read: %w", err)
			}
			if hdr.Typeflag != tar.TypeReg {
				continue
			}
			body, err := io.ReadAll(tr)
			if err != nil {
				return nil, fmt.Errorf("influx: merge tar read: %w", err)
			}
			name := filepath.Base(hdr.Name)
			files[name] = fileBlob{name: name, data: body}
			if strings.HasSuffix(name, "."+physicalManifestExtension) {
				var pm physicalManifest
				if err := json.Unmarshal(body, &pm); err != nil {
					return nil, fmt.Errorf("influx: merge manifest decode: %w", err)
				}
				manifests = append(manifests, pm)
			}
		}
	}
	merged, names, err := mergeSets(manifests, fileNameList(files))
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	tw := tar.NewWriter(&out)
	if err := writeMerged(tw, merged, func(name string) (io.Reader, int64, error) {
		return bytes.NewReader(files[name].data), int64(len(files[name].data)), nil
	}, names); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func (d *Dumper) ensureBucketAbsent(name string) error {
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
	var out struct {
		Buckets []struct {
			Name string `json:"name"`
		} `json:"buckets"`
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err := json.Unmarshal(body, &out); err != nil {
		return fmt.Errorf("influx: list buckets: %w", err)
	}
	for _, b := range out.Buckets {
		if strings.EqualFold(b.Name, name) {
			return fmt.Errorf("influx: bucket %q already exists - delete it or restore under a new name (server cannot restore into existing buckets)", name)
		}
	}
	return nil
}

func extractTar(r io.Reader, dir string) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("influx: tar read: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		out, err := os.OpenFile(filepath.Join(dir, filepath.Base(hdr.Name)),
			os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			return fmt.Errorf("influx: tar extract: %w", err)
		}
		_, err = io.Copy(out, tr)
		cerr := out.Close()
		if err != nil {
			return fmt.Errorf("influx: tar extract: %w", err)
		}
		if cerr != nil {
			return fmt.Errorf("influx: tar extract: %w", cerr)
		}
	}
}

func fileNameList(files map[string]fileBlob) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	return names
}

func isGzip(b []byte) bool {
	return len(b) >= 2 && b[0] == 0x1f && b[1] == 0x8b
}

func isTarStream(peek []byte) bool {
	if len(peek) < 262 {
		return false
	}
	return string(peek[257:262]) == "ustar"
}

func mergeBucket(dst, src *physicalBucketEntry) {
	for _, srp := range src.RetentionPolicies {
		var drp *physicalRetentionPolicy
		for i := range dst.RetentionPolicies {
			if dst.RetentionPolicies[i].Name == srp.Name {
				drp = &dst.RetentionPolicies[i]
				break
			}
		}
		if drp == nil {
			dst.RetentionPolicies = append(dst.RetentionPolicies, srp)
			continue
		}
		for _, ssg := range srp.ShardGroups {
			var dsg *physicalShardGroup
			for i := range drp.ShardGroups {
				if drp.ShardGroups[i].ID == ssg.ID {
					dsg = &drp.ShardGroups[i]
					break
				}
			}
			if dsg == nil {
				drp.ShardGroups = append(drp.ShardGroups, ssg)
				continue
			}
			for _, ssh := range ssg.Shards {
				found := false
				for i := range dsg.Shards {
					if dsg.Shards[i].ID == ssh.ID {
						dsg.Shards[i] = ssh
						found = true
						break
					}
				}
				if !found {
					dsg.Shards = append(dsg.Shards, ssh)
				}
			}
		}
	}
}

type fileBlob struct {
	name string
	data []byte
}

func mergeSets(manifests []physicalManifest, names []string) (physicalManifest, []string, error) {
	if len(manifests) == 0 {
		return physicalManifest{}, nil, fmt.Errorf("influx: merge set missing manifest")
	}
	merged := physicalManifest{Version: physicalManifestVersion}
	buckets := map[string]*physicalBucketEntry{}
	var bucketOrder []string
	for _, m := range manifests {
		if m.Version != physicalManifestVersion {
			return physicalManifest{}, nil, fmt.Errorf("influx: manifest version %d (want %d)", m.Version, physicalManifestVersion)
		}
		if merged.KV.FileName == "" {
			merged.KV = m.KV
		}
		if merged.SQL == nil {
			merged.SQL = m.SQL
		}
		for _, b := range m.Buckets {
			key := b.OrganizationName + "/" + b.BucketName
			dst, ok := buckets[key]
			if !ok {
				cp := b
				dst = &cp
				buckets[key] = dst
				bucketOrder = append(bucketOrder, key)
				continue
			}
			mergeBucket(dst, &b)
		}
	}
	merged.Buckets = make([]physicalBucketEntry, 0, len(bucketOrder))
	for _, key := range bucketOrder {
		merged.Buckets = append(merged.Buckets, *buckets[key])
	}
	ordered := make([]string, 0, len(names))
	for _, name := range names {
		if !strings.HasSuffix(name, "."+physicalManifestExtension) {
			ordered = append(ordered, name)
		}
	}
	sort.Strings(ordered)
	return merged, ordered, nil
}

func (d *Dumper) postBucketMetadata(entry physicalBucketEntry) (map[int64]int64, error) {
	body, err := json.Marshal(entry)
	if err != nil {
		return nil, fmt.Errorf("influx: encode bucket metadata: %w", err)
	}
	u := d.baseURL() + "/api/v2/restore/bucketMetadata"
	req, err := http.NewRequestWithContext(d.ctxOrBg(), "POST", u, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	d.setAuth(req)
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("influx: restore bucket metadata: HTTP %d: %s",
			resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	var out struct {
		BucketID      string `json:"bucketID"`
		ShardMappings []struct {
			OldID int64 `json:"oldId"`
			NewID int64 `json:"newId"`
		} `json:"shardMappings"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("influx: decode shard mappings: %w", err)
	}
	m := make(map[int64]int64, len(out.ShardMappings))
	for _, sm := range out.ShardMappings {
		m[sm.OldID] = sm.NewID
	}
	log.Debug("influx", "bucket restored", "bucket", entry.BucketName, "shards", len(m))
	return m, nil
}

func (d *Dumper) postShard(newID int64, path string, gzipped bool) error {
	start := time.Now()
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("influx: open shard file: %w", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("influx: stat shard file: %w", err)
	}

	head := make([]byte, 2)
	if _, err := io.ReadFull(f, head); err != nil {
		return fmt.Errorf("influx: read shard file: %w", err)
	}
	body := io.MultiReader(bytes.NewReader(head), f)
	if gzipped && !isGzip(head) {
		gzipped = false
		log.Warn("influx", "shard file not gzipped despite manifest - uploading raw",
			"file", path)
	}

	u := d.baseURL() + "/api/v2/restore/shards/" + strconv.FormatInt(newID, 10)
	req, err := http.NewRequestWithContext(d.ctxOrBg(), "POST", u, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = fi.Size()
	if gzipped {
		req.Header.Set("Content-Encoding", "gzip")
	}
	d.setAuth(req)
	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("shard %d: %w", newID, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("shard %d: HTTP %d: %s", newID, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	log.Trace("influx", "shard restored",
		"shard", newID, "bytes", fi.Size(),
		"elapsed", time.Since(start).Round(time.Millisecond).String())
	return nil
}

type closeReader struct {
	io.Reader
	closer io.Closer
}

func (d *Dumper) resolveOrgID(name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		name = d.user
	}
	u := d.baseURL() + "/api/v2/orgs?org=" + url.QueryEscape(name)
	req, err := http.NewRequestWithContext(d.ctxOrBg(), "GET", u, nil)
	if err != nil {
		return "", err
	}
	d.setAuth(req)
	resp, err := d.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Orgs []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"orgs"`
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("influx: list orgs: HTTP %d", resp.StatusCode)
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("influx: decode orgs: %w", err)
	}
	for _, o := range out.Orgs {
		if strings.EqualFold(o.Name, name) {
			return o.ID, nil
		}
	}
	return "", fmt.Errorf("influx: organization %q not found - create it before restoring", name)
}

func (d *Dumper) restorePhysical(r io.Reader, target string) error {
	if d.Version() != 2 {
		return fmt.Errorf("influx: physical restore requires InfluxDB v2.1+")
	}
	restoreStart := time.Now()
	tmpDir, err := os.MkdirTemp("", "influx-restore-")
	if err != nil {
		return fmt.Errorf("influx: temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := extractTar(r, tmpDir); err != nil {
		return err
	}
	manifests, err := filepath.Glob(filepath.Join(tmpDir, "*."+physicalManifestExtension))
	if err != nil || len(manifests) == 0 {
		return fmt.Errorf("influx: no manifest in backup")
	}
	raw, err := os.ReadFile(manifests[0])
	if err != nil {
		return fmt.Errorf("influx: read manifest: %w", err)
	}
	var m physicalManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("influx: decode manifest: %w", err)
	}
	if m.Version != physicalManifestVersion {
		return fmt.Errorf("influx: manifest version %d (want %d)", m.Version, physicalManifestVersion)
	}

	entry, name, err := selectRestoreBucket(m, target)
	if err != nil {
		return err
	}
	entry.BucketName = name

	if err := d.ensureBucketAbsent(name); err != nil {
		return err
	}
	orgID, err := d.resolveOrgID(entry.OrganizationName)
	if err != nil {
		return err
	}
	entry.OrganizationID = orgID

	shardMap, err := d.postBucketMetadata(entry)
	if err != nil {
		return err
	}

	restored := 0
	for _, rp := range entry.RetentionPolicies {
		for _, sg := range rp.ShardGroups {
			for _, sh := range sg.Shards {
				newID, ok := shardMap[sh.ID]
				if !ok {
					log.Warn("influx", "server did not map shard - skipping",
						"bucket", name, "shard", sh.ID)
					continue
				}
				path := filepath.Join(tmpDir, sh.FileName)
				if err := d.postShard(newID, path, sh.Compression == physicalCompressionGzip); err != nil {
					return err
				}
				restored++
			}
		}
	}
	log.Debug("influx", "physical restore done",
		"bucket", name, "shards", restored,
		"elapsed", time.Since(restoreStart).Round(time.Millisecond).String())

	// Make the bucket immediately InfluxQL-queryable (dbb v2-compat path
	// requires a DBRP mapping, which plain restores don't create).
	if err := d.ensureDBRPMapping(name); err != nil {
		log.Warn("influx", "dbrp mapping not ensured - bucket may need manual mapping for InfluxQL",
			"bucket", name, "error", err.Error())
	}
	return nil
}

func selectRestoreBucket(m physicalManifest, target string) (physicalBucketEntry, string, error) {
	var nonSystem []physicalBucketEntry
	for _, b := range m.Buckets {
		if strings.HasPrefix(b.BucketName, "_") {
			continue
		}
		nonSystem = append(nonSystem, b)
	}
	for _, b := range nonSystem {
		if strings.EqualFold(b.BucketName, target) {
			return b, b.BucketName, nil
		}
	}
	if len(nonSystem) == 1 {
		return nonSystem[0], target, nil
	}
	names := make([]string, 0, len(nonSystem))
	for _, b := range nonSystem {
		names = append(names, b.BucketName)
	}
	return physicalBucketEntry{}, "", fmt.Errorf("influx: target %q matches no backed up bucket (%s)", target, strings.Join(names, ", "))
}

func writeMerged(tw *tar.Writer, merged physicalManifest, open func(name string) (io.Reader, int64, error), names []string) error {
	var mBuf bytes.Buffer
	enc := json.NewEncoder(&mBuf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(merged); err != nil {
		return fmt.Errorf("influx: merge manifest encode: %w", err)
	}
	mName := "merged." + physicalManifestExtension
	if err := tw.WriteHeader(&tar.Header{Name: mName, Mode: 0o600, Size: int64(mBuf.Len())}); err != nil {
		return err
	}
	if _, err := tw.Write(mBuf.Bytes()); err != nil {
		return err
	}
	for _, name := range names {
		r, size, err := open(name)
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: size}); err != nil {
			if closer, ok := r.(io.Closer); ok {
				closer.Close()
			}
			return err
		}
		_, err = io.Copy(tw, r)
		if closer, ok := r.(io.Closer); ok {
			cerr := closer.Close()
			if err == nil {
				err = cerr
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}
