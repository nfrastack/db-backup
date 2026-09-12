// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package influx

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database/common"
	"github.com/nfrastack/db-backup/internal/log"
)

var (
	lpTagEscaper    = strings.NewReplacer(`\`, `\\`, " ", `\ `, ",", `\,`, "=", `\=`)
	lpStrEscaper    = strings.NewReplacer(`\`, `\\`, `"`, `\"`, " ", `\ `, `,`, `\,`, "=", `\=`)
	lineBuilderPool = sync.Pool{New: func() any {
		b := &strings.Builder{}
		b.Grow(256)
		return b
	}}
)

const traceProgressRows = 500000
const influxChunkSize = 20000
const influxExportWorkers = 0
const incrementalOverlap = 2 * time.Minute

func sliceBounds(since, until string) (string, string) {
	if strings.TrimSpace(since) == "" {
		return "", ""
	}
	sinceT, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(since))
	if err != nil {
		if sinceT, err = time.Parse(time.RFC3339, strings.TrimSpace(since)); err != nil {
			return "", ""
		}
	}
	end := strings.TrimSpace(until)
	if end == "" {
		end = time.Now().UTC().Format(time.RFC3339Nano)
	}
	start := sinceT.Add(-incrementalOverlap).UTC().Format(time.RFC3339Nano)
	return start, end
}

var influxExportMode = "parallel" // legacy

func effectiveWorkers() int {
	if influxExportWorkers > 0 {
		return influxExportWorkers
	}
	n := runtime.NumCPU() - 1
	if n < 1 {
		return 1
	}
	if n > 8 {
		return 8
	}
	return n
}

type exportTask struct {
	m      string
	tags   map[string]bool
	ftypes map[string]string
	start  string
	end    string
	index  int
	total  int
}

type measState struct {
	mu      sync.Mutex
	start   time.Time
	rows    int
	lines   int
	bytes   int64
	pending int
	failed  bool
}

type Dumper struct {
	host            string
	port            int
	user            string
	pass            string
	dbName          string
	version         int
	tlsCfg          *config.TLSConfig
	client          *http.Client
	connCfg         *config.ConnectivityConfig
	streamClient    *http.Client
	ctx             context.Context
	lastPingVersion string
	mode            string
}

func resolveBackupMode(requested string) string {
	switch strings.ToLower(strings.TrimSpace(requested)) {
	case "physical":
		return "physical"
	case "logical":
		return "logical"
	case "", "auto":
		return influxBackupMode
	default:
		log.Warn("influx", "unknown backup mode - using default",
			"requested", requested, "mode", influxBackupMode)
		return influxBackupMode
	}
}

func (d *Dumper) protocolKey(dbNames []string) string {
	return common.ProtocolKey(d.host, d.port, d.user, strings.Join(dbNames, ","))
}

type influxQueryResult struct {
	Results []struct {
		Error  string `json:"error"`
		Series []struct {
			Name    string   `json:"name"`
			Columns []string `json:"columns"`
			Values  [][]any  `json:"values"`
		} `json:"series"`
	} `json:"results"`
}

func (d *Dumper) Close() error { return nil }

func (d *Dumper) Dump(w io.Writer, dbNames []string) error {
	mode := d.mode
	if mode == "" {
		mode = resolveBackupMode(common.BackupModeFromContext(d.ctxOrBg()))
		d.mode = mode
	}
	if mode == "physical" && d.Version() == 2 {
		if err := d.PhysicalBackup(w, dbNames); err != nil {
			if errors.Is(err, errOperatorRequired) {
				log.Warn("influx", "physical backup forbidden - falling back to logical export",
					"host", d.host, "error", err.Error())
			} else {
				return err
			}
		} else {
			common.RecordBackupProtocol(d.protocolKey(dbNames), "physical")
			return nil
		}
	} else if mode == "physical" {
		log.Debug("influx", "v1 has no physical protocol - using logical export",
			"host", d.host)
	}
	ver := d.Version()
	log.Debug("influx", "backup start",
		"host", d.host, "port", d.port, "scheme", d.scheme(),
		"auth", d.authMode(), "version", fmt.Sprintf("v%d", ver),
		"version_source", versionSource(d.version),
		"databases", strings.Join(dbNames, ","))
	start := time.Now()

	if len(dbNames) == 1 && strings.ToLower(dbNames[0]) == "all" {
		names, err := d.listDatabases()
		if err != nil {
			log.Debug("influx", "expand ALL failed", "host", d.host, "error", err.Error())
			return err
		}
		dbNames = names
		log.Debug("influx", "expanded ALL", "count", len(dbNames), "databases", strings.Join(dbNames, ","))
	}
	if len(dbNames) == 0 {
		return fmt.Errorf("influx: no databases to back up")
	}

	var total dumpStats
	bw := bufio.NewWriterSize(w, 1<<20)
	for _, db := range dbNames {
		st, err := d.dumpDatabase(bw, db, "", "")
		if err != nil {
			return fmt.Errorf("dump %s: %w", db, err)
		}
		total.lines += st.lines
		total.skipped += st.skipped
		total.measurements += st.measurements
		total.skippedNames = append(total.skippedNames, st.skippedNames...)
	}
	if err := bw.Flush(); err != nil {
		return fmt.Errorf("flush backup stream: %w", err)
	}
	log.Debug("influx", "backup done",
		"databases", len(dbNames), "lines", total.lines,
		"skipped_measurements", total.skipped,
		"elapsed", time.Since(start).Round(time.Millisecond).String())
	if total.skipped > 0 {
		return fmt.Errorf("influx: backup incomplete - %d of %d measurements skipped (%s)",
			total.skipped, total.measurements, strings.Join(total.skippedNames, ", "))
	}
	common.RecordBackupProtocol(d.protocolKey(dbNames), "logical")
	return nil
}

type dumpStats struct {
	measurements int
	lines        int
	skipped      int
	skippedNames []string
}

func NewDumper(host string, port int, user, pass, dbName string, version int, tlsCfg ...*config.TLSConfig) *Dumper {
	if port == 0 {
		port = 8086
	}
	d := &Dumper{
		host:         host,
		port:         port,
		user:         user,
		pass:         pass,
		dbName:       dbName,
		version:      version,
		client:       common.HTTPClient(nil, 30*time.Second),
		streamClient: common.HTTPClient(nil, 0),
	}
	if len(tlsCfg) > 0 && tlsCfg[0] != nil {
		d.tlsCfg = tlsCfg[0]
		d.client = common.HTTPClient(d.tlsCfg, 30*time.Second)
		d.streamClient = common.HTTPClient(d.tlsCfg, 0)
	}
	return d
}

func (d *Dumper) Open() error {
	return d.OpenContext(context.Background())
}

func (d *Dumper) OpenContext(ctx context.Context) error {
	d.ctx = ctx
	d.mode = resolveBackupMode(common.BackupModeFromContext(ctx))
	explicit := d.version
	log.Debug("influx", "connect start",
		"host", d.host, "port", d.port, "scheme", d.scheme(),
		"auth", d.authMode(), "version", versionString(explicit),
		"version_source", versionSource(explicit))
	probe := func() error { return common.TCPDial(d.host, d.port) }
	connect := func() error { return common.TCPDial(d.host, d.port) }
	ping := func() error { return d.detect(ctx, explicit) }
	if err := common.WithConnectivity(ctx, "influx", d.connCfg, probe, connect, ping); err != nil {
		return err
	}
	if d.version == 0 {
		if err := d.detect(ctx, 0); err != nil {
			log.Debug("influx", "auto-detect failed - proceeding with defaults",
				"host", d.host, "port", d.port, "error", err.Error())
		}
	}
	return nil
}

func (d *Dumper) detect(ctx context.Context, explicit int) error {
	switch explicit {
	case 2:
		if err := d.probeV2(ctx); err != nil {
			return err
		}
		d.version = 2
		log.Debug("influx", "connected",
			"host", d.host, "port", d.port,
			"version", "v2", "version_source", "config")
		return nil
	case 1:
		if err := d.probeV1(ctx); err != nil {
			return err
		}
		d.version = 1
		log.Debug("influx", "connected",
			"host", d.host, "port", d.port,
			"version", "v1", "version_source", "config")
		return nil
	}
	if err := d.probeV2(ctx); err != nil {
		log.Debug("influx", "v2 probe failed - trying v1",
			"host", d.host, "port", d.port, "error", err.Error())
		if err2 := d.probeV1(ctx); err2 != nil {
			return fmt.Errorf("influx: no server detected (v2 health: %v; v1 ping: %v)", err, err2)
		}
		if n, ok := parseMajorVersion(d.lastPingVersion); ok {
			d.version = n
		} else {
			d.version = 1
		}
		log.Debug("influx", "connected",
			"host", d.host, "port", d.port,
			"version", fmt.Sprintf("v%d", d.version), "version_source", "detected")
		return nil
	}
	d.version = 2
	log.Debug("influx", "connected",
		"host", d.host, "port", d.port,
		"version", "v2", "version_source", "detected")
	return nil
}

func (d *Dumper) SetConnectivity(cfg *config.ConnectivityConfig) {
	if cfg != nil {
		d.connCfg = cfg
	}
}

func (d *Dumper) Version() int {
	if d.version == 0 {
		return 2
	}
	return d.version
}

func (d *Dumper) Mode() string {
	if d.mode == "" {
		return influxBackupMode
	}
	return d.mode
}

func (d *Dumper) LogicalSlice(w io.Writer, dbs []string, since, end string) error {
	var total dumpStats
	bw := bufio.NewWriterSize(w, 1<<20)
	for _, db := range dbs {
		st, err := d.dumpDatabase(bw, db, since, end)
		if err != nil {
			return fmt.Errorf("dump %s: %w", db, err)
		}
		total.lines += st.lines
		total.skipped += st.skipped
		total.measurements += st.measurements
		total.skippedNames = append(total.skippedNames, st.skippedNames...)
	}
	if err := bw.Flush(); err != nil {
		return fmt.Errorf("flush slice: %w", err)
	}
	log.Debug("influx", "slice done",
		"databases", len(dbs), "lines", total.lines,
		"skipped_measurements", total.skipped)
	if total.skipped > 0 {
		return fmt.Errorf("influx: slice incomplete - %d of %d measurements skipped (%s)",
			total.skipped, total.measurements, strings.Join(total.skippedNames, ", "))
	}
	return nil
}

func (d *Dumper) ctxOrBg() context.Context {
	if d.ctx != nil {
		return d.ctx
	}
	return context.Background()
}

func (d *Dumper) dumpDatabase(w *bufio.Writer, db, since, until string) (*dumpStats, error) {
	if d.Version() == 1 {
		return d.dumpV1(w, db, since, until)
	}
	return d.dumpV2(w, db, since, until)
}

func (d *Dumper) dumpV1(w *bufio.Writer, db, since, until string) (*dumpStats, error) {
	fmt.Fprintf(w, "# INFLUXDB EXPORT: %s\n", db)
	fmt.Fprintf(w, "# DDL\n")
	fmt.Fprintf(w, "CREATE DATABASE %s\n", db)
	if start, end := sliceBounds(since, until); start != "" {
		fmt.Fprintf(w, "# WINDOW: %s to %s\n", start, end)
	}
	fmt.Fprintf(w, "\n")
	return d.dumpMeasurements(w, db, since, until)
}

func (d *Dumper) dumpV2(w *bufio.Writer, bucket, since, until string) (*dumpStats, error) {
	fmt.Fprintf(w, "# INFLUXDB V2 EXPORT: %s (org %s)\n", bucket, d.user)
	fmt.Fprintf(w, "# DDL\n")
	fmt.Fprintf(w, "CREATE DATABASE %s\n", bucket)
	if start, end := sliceBounds(since, until); start != "" {
		fmt.Fprintf(w, "# WINDOW: %s to %s\n", start, end)
	}
	fmt.Fprintf(w, "\n")
	return d.dumpMeasurements(w, bucket, since, until)
}

func (d *Dumper) loadTagSchemas(db string, measurements []string) map[string]map[string]bool {
	schemas := make(map[string]map[string]bool, len(measurements))
	result, err := d.query(db, "SHOW TAG KEYS")
	if err != nil {
		log.Debug("influx", "bulk tag keys failed - falling back to per-measurement queries",
			"database", db, "error", err.Error())
		return nil
	}
	for _, r := range result.Results {
		for _, s := range r.Series {
			set := make(map[string]bool)
			for _, v := range s.Values {
				if len(v) > 0 {
					if k, ok := v[0].(string); ok {
						set[k] = true
					}
				}
			}
			schemas[s.Name] = set
		}
	}
	log.Trace("influx", "bulk tag keys ok", "database", db, "measurements", len(schemas))
	return schemas
}

func (d *Dumper) tagKeysFor(db, m string, bulk map[string]map[string]bool) map[string]bool {
	if bulk != nil {
		if tags, ok := bulk[m]; ok {
			return tags
		}
		return map[string]bool{}
	}
	tagResult, err := d.query(db, fmt.Sprintf("SHOW TAG KEYS FROM %q", m))
	if err != nil {
		return nil
	}
	tags := map[string]bool{}
	for _, r := range tagResult.Results {
		for _, s := range r.Series {
			for _, v := range s.Values {
				if len(v) > 0 {
					if k, ok := v[0].(string); ok {
						tags[k] = true
					}
				}
			}
		}
	}
	return tags
}

func (d *Dumper) dumpMeasurements(w *bufio.Writer, db, since, until string) (*dumpStats, error) {
	st := &dumpStats{}
	result, err := d.query(db, "SHOW MEASUREMENTS")
	if err != nil {
		return nil, fmt.Errorf("list measurements: %w", err)
	}
	var measurements []string
	for _, r := range result.Results {
		for _, s := range r.Series {
			for _, v := range s.Values {
				if len(v) > 0 {
					if m, ok := v[0].(string); ok {
						measurements = append(measurements, m)
					}
				}
			}
		}
	}
	if len(measurements) == 0 {
		log.Debug("influx", "no measurements found", "database", db)
		return st, nil
	}
	st.measurements = len(measurements)
	log.Debug("influx", "measurements listed", "database", db, "count", len(measurements))

	bulkTags := d.loadTagSchemas(db, measurements)
	bulkFields := d.loadFieldTypes(db)

	if influxExportMode == "legacy" {
		log.Debug("influx", "export mode", "database", db, "mode", "legacy")
		return d.dumpMeasurementsLegacy(w, db, st, measurements, bulkTags, bulkFields, since, until)
	}
	log.Debug("influx", "export mode", "database", db, "mode", "parallel")
	return d.dumpMeasurementsParallel(w, db, st, measurements, bulkTags, bulkFields, since, until)
}

func (d *Dumper) dumpMeasurementsLegacy(w *bufio.Writer, db string, st *dumpStats, measurements []string, bulkTags map[string]map[string]bool, bulkFields map[string]map[string]string, since, until string) (*dumpStats, error) {
	start, end := sliceBounds(since, until)
	if start != "" {
		log.Debug("influx", "bounded export", "database", db, "from", start, "to", end)
	}
	for i, m := range measurements {
		common.TraceTable(d.ctxOrBg(), db, m)
		mStart := time.Now()
		log.Trace("influx", "measurement start", "database", db, "measurement", m,
			"index", i+1, "total", len(measurements))

		tags := d.tagKeysFor(db, m, bulkTags)
		if tags == nil {
			st.skipped++
			st.skippedNames = append(st.skippedNames, db+"."+m)
			log.Warn("influx", "skipping measurement - tag keys query failed",
				"database", db, "measurement", m)
			continue
		}

		var mLines, mRows int
		var mBytes int64
		lb := lineBuilderPool.Get().(*strings.Builder)
		q := fmt.Sprintf("SELECT * FROM %q", m)
		if start != "" {
			q = fmt.Sprintf("SELECT * FROM %q WHERE time >= '%s' AND time < '%s'", m, start, end)
		}
		queryErr := d.queryChunked(db, q, false, tags, bulkFields[m], func(cols []string, row []any) error {
			mRows++
			lb.Reset()
			if !buildLineProtocolInto(lb, m, cols, row, tags, bulkFields[m]) {
				return nil
			}
			lb.WriteByte('\n')
			n, err := w.WriteString(lb.String())
			mBytes += int64(n)
			if err != nil {
				return err
			}
			mLines++
			if mRows%traceProgressRows == 0 {
				elapsed := time.Since(mStart)
				var rowsSec float64
				if elapsed > 0 {
					rowsSec = float64(mRows) / elapsed.Seconds()
				}
				log.Trace("influx", "measurement progress",
					"database", db, "measurement", m,
					"rows", mRows, "lines", mLines, "bytes", mBytes,
					"rows_sec", int64(rowsSec),
					"elapsed", elapsed.Round(time.Millisecond).String())
			}
			return nil
		})
		lineBuilderPool.Put(lb)
		if queryErr != nil {
			st.skipped++
			st.skippedNames = append(st.skippedNames, db+"."+m)
			log.Warn("influx", "skipping measurement - data query failed",
				"database", db, "measurement", m, "error", queryErr.Error())
			continue
		}

		fmt.Fprintf(w, "\n# CONTEXT-DATABASE: %s\n# MEASUREMENT: %s\n", db, m)
		if err := w.Flush(); err != nil {
			return st, fmt.Errorf("flush measurement %s: %w", m, err)
		}
		mElapsed := time.Since(mStart)
		var rowsSec float64
		if mElapsed > 0 {
			rowsSec = float64(mRows) / mElapsed.Seconds()
		}
		log.Trace("influx", "measurement done",
			"database", db, "measurement", m, "rows", mRows, "lines", mLines,
			"bytes", mBytes, "rows_sec", int64(rowsSec),
			"elapsed", mElapsed.Round(time.Millisecond).String())
		st.lines += mLines
	}
	log.Debug("influx", "database done",
		"database", db, "measurements", len(measurements),
		"lines", st.lines, "skipped_measurements", st.skipped)
	return st, nil
}

func (d *Dumper) dumpMeasurementsParallel(w *bufio.Writer, db string, st *dumpStats, measurements []string, bulkTags map[string]map[string]bool, bulkFields map[string]map[string]string, since, until string) (*dumpStats, error) {
	workers := effectiveWorkers()
	log.Debug("influx", "export workers",
		"database", db, "workers", workers, "measurements", len(measurements))

	start, end := sliceBounds(since, until)
	if start != "" {
		log.Debug("influx", "bounded export", "database", db, "from", start, "to", end)
	}
	windows := [][2]string{{start, end}}

	var wmu sync.Mutex
	states := make(map[string]*measState, len(measurements))
	for _, m := range measurements {
		states[m] = &measState{start: time.Now(), pending: 1}
	}

	tasks := make(chan exportTask)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range tasks {
				d.runTask(w, &wmu, db, states[t.m], t)
			}
		}()
	}

	for i, m := range measurements {
		common.TraceTable(d.ctxOrBg(), db, m)
		log.Trace("influx", "measurement start", "database", db, "measurement", m,
			"index", i+1, "total", len(measurements))

		tags := d.tagKeysFor(db, m, bulkTags)
		if tags == nil {
			d.finishMeasurement(w, &wmu, db, m, states[m], true)
			st.skipped++
			st.skippedNames = append(st.skippedNames, db+"."+m)
			continue
		}

		ms := states[m]
		ms.mu.Lock()
		ms.pending = len(windows)
		ms.mu.Unlock()
		for _, win := range windows {
			tasks <- exportTask{m: m, tags: tags, ftypes: bulkFields[m], start: win[0], end: win[1], index: i + 1, total: len(measurements)}
		}
	}
	close(tasks)
	wg.Wait()

	for _, m := range measurements {
		ms := states[m]
		if ms.failed {
			st.skipped++
			st.skippedNames = append(st.skippedNames, db+"."+m)
			continue
		}
		st.lines += ms.lines
	}
	log.Debug("influx", "database done",
		"database", db, "measurements", len(measurements),
		"lines", st.lines, "skipped_measurements", st.skipped)
	return st, nil
}

func (d *Dumper) loadFieldTypes(db string) map[string]map[string]string {
	ftypes := map[string]map[string]string{}
	result, err := d.query(db, "SHOW FIELD KEYS")
	if err != nil {
		log.Debug("influx", "bulk field keys failed - legacy value rendering",
			"database", db, "error", err.Error())
		return ftypes
	}
	for _, r := range result.Results {
		for _, s := range r.Series {
			set := make(map[string]string)
			for _, v := range s.Values {
				if len(v) >= 2 {
					if k, ok := v[0].(string); ok {
						if t, ok := v[1].(string); ok {
							set[k] = t
						}
					}
				}
			}
			ftypes[s.Name] = set
		}
	}
	log.Trace("influx", "bulk field keys ok", "database", db, "measurements", len(ftypes))
	return ftypes
}

func (d *Dumper) runTask(w *bufio.Writer, wmu *sync.Mutex, db string, ms *measState, t exportTask) {
	q := fmt.Sprintf("SELECT * FROM %q", t.m)
	if t.start != "" {
		q = fmt.Sprintf("SELECT * FROM %q WHERE time >= '%s' AND time < '%s'", t.m, t.start, t.end)
	}
	failed := false
	lb := lineBuilderPool.Get().(*strings.Builder)
	emit := func(cols []string, row []any) error {
		lb.Reset()
		if !buildLineProtocolInto(lb, t.m, cols, row, t.tags, t.ftypes) {
			return nil
		}
		lb.WriteByte('\n')
		s := lb.String()
		ms.mu.Lock()
		ms.rows++
		if ms.rows%traceProgressRows == 0 {
			elapsed := time.Since(ms.start)
			var rowsSec float64
			if elapsed > 0 {
				rowsSec = float64(ms.rows) / elapsed.Seconds()
			}
			log.Trace("influx", "measurement progress",
				"database", db, "measurement", t.m,
				"rows", ms.rows, "lines", ms.lines, "bytes", ms.bytes,
				"rows_sec", int64(rowsSec),
				"elapsed", elapsed.Round(time.Millisecond).String())
		}
		ms.mu.Unlock()
		wmu.Lock()
		n, werr := w.WriteString(s)
		wmu.Unlock()
		if werr != nil {
			return werr
		}
		ms.mu.Lock()
		ms.bytes += int64(n)
		ms.lines++
		ms.mu.Unlock()
		return nil
	}
	err := d.queryChunked(db, q, true, t.tags, t.ftypes, emit)
	var csvErr *csvParseError
	if errors.As(err, &csvErr) {
		log.Debug("influx", "csv decode failed - retrying window as json",
			"database", db, "measurement", t.m,
			"start", t.start, "end", t.end, "error", err.Error())
		err = d.queryChunked(db, q, false, t.tags, t.ftypes, emit)
	}
	lineBuilderPool.Put(lb)
	if err != nil {
		failed = true
		log.Warn("influx", "window query failed",
			"database", db, "measurement", t.m,
			"start", t.start, "end", t.end, "error", err.Error())
	}
	d.finishMeasurement(w, wmu, db, t.m, ms, failed)
}

func (d *Dumper) finishMeasurement(w *bufio.Writer, wmu *sync.Mutex, db, m string, ms *measState, failed bool) {
	ms.mu.Lock()
	if failed {
		ms.failed = true
	}
	ms.pending--
	last := ms.pending == 0
	ms.mu.Unlock()
	if !last {
		return
	}
	if ms.failed {
		log.Warn("influx", "skipping measurement - queries failed",
			"database", db, "measurement", m)
		return
	}
	wmu.Lock()
	fmt.Fprintf(w, "\n# CONTEXT-DATABASE: %s\n# MEASUREMENT: %s\n", db, m)
	flushErr := w.Flush()
	wmu.Unlock()
	if flushErr != nil {
		ms.mu.Lock()
		ms.failed = true
		ms.mu.Unlock()
		log.Warn("influx", "skipping measurement - flush failed",
			"database", db, "measurement", m, "error", flushErr.Error())
		return
	}
	ms.mu.Lock()
	rows, lines, bytes := ms.rows, ms.lines, ms.bytes
	elapsed := time.Since(ms.start)
	ms.mu.Unlock()
	var rowsSec float64
	if elapsed > 0 {
		rowsSec = float64(rows) / elapsed.Seconds()
	}
	log.Trace("influx", "measurement done",
		"database", db, "measurement", m, "rows", rows, "lines", lines,
		"bytes", bytes, "rows_sec", int64(rowsSec),
		"elapsed", elapsed.Round(time.Millisecond).String())
}

func appendLineProtocolValue(b *strings.Builder, v any) {
	switch v := v.(type) {
	case bool:
		if v {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case json.Number:
		if strings.ContainsAny(v.String(), ".eE") {
			if f, err := v.Float64(); err == nil {
				b.WriteString(formatFloat(f))
				return
			}
			b.WriteString(v.String())
			return
		}
		if f, err := v.Float64(); err == nil {
			b.WriteString(formatFloat(f))
			return
		}
		b.WriteString(v.String())
	case float64:
		b.WriteString(formatFloat(v))
	case string:
		b.WriteByte('"')
		b.WriteString(lpStrEscaper.Replace(v))
		b.WriteByte('"')
	default:
		enc, _ := json.Marshal(v)
		b.Write(enc)
	}
}

func buildLineProtocolInto(b *strings.Builder, m string, cols []string, row []any, tags map[string]bool, ftypes map[string]string) bool {
	b.WriteString(m)
	n := len(cols)
	if len(row) < n {
		n = len(row)
	}
	ts := ""
	for i := 0; i < n; i++ {
		col := cols[i]
		if col == "time" {
			ts = formatTimestamp(row[i])
			continue
		}
		if !tags[col] {
			continue
		}
		if row[i] == nil {
			continue
		}
		b.WriteByte(',')
		b.WriteString(lpTagEscaper.Replace(col))
		b.WriteByte('=')
		b.WriteString(lpTagEscaper.Replace(stringifyTag(row[i])))
	}
	wroteField := false
	for i := 0; i < n; i++ {
		col := cols[i]
		if col == "time" || tags[col] {
			continue
		}
		if row[i] == nil {
			continue
		}
		if !wroteField {
			b.WriteByte(' ')
			wroteField = true
		} else {
			b.WriteByte(',')
		}
		b.WriteString(lpTagEscaper.Replace(col))
		b.WriteByte('=')
		if ftypes[col] == "integer" {
			if lit, ok := integerLiteral(row[i]); ok {
				b.WriteString(lit)
				b.WriteByte('i')
				continue
			}
		}
		appendLineProtocolValue(b, row[i])
	}
	if !wroteField {
		return false
	}
	if ts != "" {
		b.WriteByte(' ')
		b.WriteString(ts)
	}
	return true
}

func integerLiteral(v any) (string, bool) {
	switch v := v.(type) {
	case json.Number:
		s := v.String()
		if isIntLiteral(s) {
			return s, true
		}
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10), true
		}
	}
	return "", false
}

func isIntLiteral(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			if !(i == 0 && s[i] == '-') {
				return false
			}
		}
	}
	return true
}

func escapeLineProtocolTag(s string) string {
	return lpTagEscaper.Replace(s)
}

func formatFloat(f float64) string {
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}

func formatLineProtocolValue(v any) string {
	b := lineBuilderPool.Get().(*strings.Builder)
	b.Reset()
	appendLineProtocolValue(b, v)
	s := b.String()
	lineBuilderPool.Put(b)
	return s
}

func formatTimestamp(v any) string {
	switch v := v.(type) {
	case json.Number:
		return v.String()
	case float64:
		return strconv.FormatInt(int64(v), 10)
	case string:
		return v
	default:
		return fmt.Sprintf("%v", v)
	}
}

func stringifyTag(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case bool:
		if v {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprintf("%v", v)
	}
}

func (d *Dumper) listDatabases() ([]string, error) {
	if d.Version() == 2 {
		names, err := d.listV2Buckets()
		if err == nil {
			log.Debug("influx", "buckets listed", "count", len(names))
			return names, nil
		}
		if strings.Contains(err.Error(), "unauthorized") {
			return nil, err
		}
		log.Debug("influx", "bucket listing failed - falling back to SHOW DATABASES", "error", err.Error())
	}
	result, err := d.query("", "SHOW DATABASES")
	if err != nil {
		return nil, fmt.Errorf("list databases: %w", err)
	}
	var dbs []string
	for _, r := range result.Results {
		for _, s := range r.Series {
			for _, v := range s.Values {
				if len(v) > 0 {
					if name, ok := v[0].(string); ok && !strings.HasPrefix(name, "_") {
						dbs = append(dbs, name)
					}
				}
			}
		}
	}
	return dbs, nil
}

func (d *Dumper) listV2Buckets() ([]string, error) {
	u := d.baseURL() + "/api/v2/buckets"
	start := time.Now()
	req, err := http.NewRequestWithContext(d.ctxOrBg(), "GET", u, nil)
	if err != nil {
		return nil, err
	}
	d.setAuth(req)
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	elapsed := time.Since(start).Round(time.Millisecond).String()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		log.Trace("influx", "list buckets auth failed",
			"url", u, "status", resp.StatusCode, "latency", elapsed,
			"body", strings.TrimSpace(string(body)))
		return nil, fmt.Errorf("list buckets: unauthorized (HTTP %d) - check org/token", resp.StatusCode)
	}
	if resp.StatusCode == http.StatusNotFound {
		log.Trace("influx", "list buckets not found - no v2 API", "url", u, "status", resp.StatusCode, "latency", elapsed)
		return nil, fmt.Errorf("list buckets: HTTP 404 (no v2 API)")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		log.Trace("influx", "list buckets failed",
			"url", u, "status", resp.StatusCode, "latency", elapsed,
			"body", strings.TrimSpace(string(body)))
		return nil, fmt.Errorf("list buckets: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var bucketsResp struct {
		Buckets []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"buckets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&bucketsResp); err != nil {
		return nil, fmt.Errorf("decode buckets: %w", err)
	}
	var names []string
	for _, b := range bucketsResp.Buckets {
		if strings.HasPrefix(b.Name, "_") {
			log.Trace("influx", "skipping system bucket", "bucket", b.Name)
			continue
		}
		names = append(names, b.Name)
	}
	log.Trace("influx", "list buckets ok", "url", u, "status", resp.StatusCode, "latency", elapsed, "count", len(names))
	return names, nil
}

func parseMajorVersion(v string) (int, bool) {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	num := 0
	digits := 0
	for i := 0; i < len(v) && v[i] >= '0' && v[i] <= '9'; i++ {
		num = num*10 + int(v[i]-'0')
		digits++
		if num > 99 {
			return 0, false
		}
		if i+1 == len(v) || v[i+1] == '.' {
			break
		}
	}
	if digits == 0 || num < 1 || num > 2 {
		return 0, false
	}
	return num, true
}

func (d *Dumper) probeV1(ctx context.Context) error {
	u := d.baseURL() + "/ping"
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		log.Trace("influx", "v1 ping transport error", "url", u, "error", err.Error())
		return fmt.Errorf("ping: %w", err)
	}
	defer resp.Body.Close()
	elapsed := time.Since(start).Round(time.Millisecond).String()
	verHeader := resp.Header.Get("X-Influxdb-Version")
	if verHeader == "" {
		verHeader = resp.Header.Get("X-InfluxDB-Version")
	}
	log.Trace("influx", "v1 ping response",
		"url", u, "status", resp.StatusCode, "latency", elapsed,
		"version_header", verHeader)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("ping: influx responded %d", resp.StatusCode)
	}
	d.lastPingVersion = verHeader
	return nil
}

func (d *Dumper) probeV2(ctx context.Context) error {
	u := d.baseURL() + "/health"
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		log.Trace("influx", "v2 health transport error", "url", u, "error", err.Error())
		return fmt.Errorf("health: %w", err)
	}
	defer resp.Body.Close()
	elapsed := time.Since(start).Round(time.Millisecond).String()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	verHeader := resp.Header.Get("X-Influxdb-Version")
	if verHeader == "" {
		verHeader = resp.Header.Get("X-InfluxDB-Version")
	}
	log.Trace("influx", "v2 health response",
		"url", u, "status", resp.StatusCode, "latency", elapsed,
		"version_header", verHeader, "body", strings.TrimSpace(string(body)))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("health: influx responded %d", resp.StatusCode)
	}
	if n, ok := parseMajorVersion(verHeader); ok && n != 2 {
		return fmt.Errorf("health: server reports version v%d, not v2", n)
	}
	return nil
}

func (d *Dumper) query(db, q string) (*influxQueryResult, error) {
	if d.Version() == 2 && (strings.TrimSpace(d.user) == "" || strings.TrimSpace(d.pass) == "") {
		return nil, fmt.Errorf("influx v2 backup requires org (user) and token (pass) - got org %q", d.user)
	}
	u := d.baseURL() + "/query?db=" + url.QueryEscape(db) + "&epoch=ns&q=" + url.QueryEscape(q)
	if d.Version() == 2 {
		u += "&org=" + url.QueryEscape(d.user)
	}
	start := time.Now()
	req, err := http.NewRequestWithContext(d.ctxOrBg(), "GET", u, nil)
	if err != nil {
		return nil, err
	}
	d.setAuth(req)
	resp, err := d.client.Do(req)
	if err != nil {
		log.Trace("influx", "query transport error", "database", db, "query", q, "error", err.Error())
		return nil, err
	}
	defer resp.Body.Close()
	elapsed := time.Since(start).Round(time.Millisecond).String()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		msg := strings.TrimSpace(string(body))
		log.Trace("influx", "query failed",
			"database", db, "query", q, "status", resp.StatusCode,
			"latency", elapsed, "body", msg)
		if d.Version() == 2 && (resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusNotFound) {
			return nil, fmt.Errorf("influx query %q: HTTP %d: %s (v2 InfluxQL compatibility: bucket %q needs a DBRP mapping)", q, resp.StatusCode, msg, db)
		}
		return nil, fmt.Errorf("influx query %q: HTTP %d: %s", q, resp.StatusCode, msg)
	}

	var result influxQueryResult
	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	if err := dec.Decode(&result); err != nil {
		return nil, fmt.Errorf("influx query %q: decode: %w", q, err)
	}
	series := 0
	for _, r := range result.Results {
		series += len(r.Series)
	}
	log.Trace("influx", "query ok",
		"database", db, "query", q, "status", resp.StatusCode,
		"latency", elapsed, "series", series)
	return &result, nil
}

func (d *Dumper) queryChunked(db, q string, wantCSV bool, tags map[string]bool, ftypes map[string]string, fn func(cols []string, row []any) error) error {
	if d.Version() == 2 && (strings.TrimSpace(d.user) == "" || strings.TrimSpace(d.pass) == "") {
		return fmt.Errorf("influx v2 backup requires org (user) and token (pass) - got org %q", d.user)
	}
	u := d.baseURL() + "/query?db=" + url.QueryEscape(db) + "&epoch=ns&chunked=true&chunk_size=" + strconv.Itoa(influxChunkSize) + "&q=" + url.QueryEscape(q)
	if d.Version() == 2 {
		u += "&org=" + url.QueryEscape(d.user)
	}
	start := time.Now()
	req, err := http.NewRequestWithContext(d.ctxOrBg(), "GET", u, nil)
	if err != nil {
		return err
	}
	if wantCSV {
		req.Header.Set("Accept", "application/csv")
	}
	d.setAuth(req)
	resp, err := d.streamClient.Do(req)
	if err != nil {
		log.Trace("influx", "stream query transport error", "database", db, "query", q, "error", err.Error())
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		msg := strings.TrimSpace(string(body))
		log.Trace("influx", "stream query failed",
			"database", db, "query", q, "status", resp.StatusCode,
			"latency", time.Since(start).Round(time.Millisecond).String(), "body", msg)
		if d.Version() == 2 && (resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusNotFound) {
			return fmt.Errorf("influx query %q: HTTP %d: %s (v2 InfluxQL compatibility: bucket %q needs a DBRP mapping)", q, resp.StatusCode, msg, db)
		}
		return fmt.Errorf("influx query %q: HTTP %d: %s", q, resp.StatusCode, msg)
	}

	var rows int
	ttfb := time.Since(start)
	format := "json"
	if isCSVResponse(resp) {
		format = "csv"
		rows, err = decodeCSVStream(resp.Body, tags, ftypes, fn)
		if err != nil {
			return err
		}
	} else {
		dec := json.NewDecoder(resp.Body)
		dec.UseNumber()
		for {
			var chunk influxQueryResult
			if err := dec.Decode(&chunk); err != nil {
				if err == io.EOF {
					break
				}
				return fmt.Errorf("influx query %q: stream decode: %w", q, err)
			}
			for _, r := range chunk.Results {
				if r.Error != "" {
					return fmt.Errorf("influx query %q: %s", q, r.Error)
				}
				for _, s := range r.Series {
					for _, row := range s.Values {
						rows++
						if err := fn(s.Columns, row); err != nil {
							return err
						}
					}
				}
			}
		}
	}
	log.Trace("influx", "stream query ok",
		"database", db, "query", q, "status", resp.StatusCode,
		"format", format,
		"ttfb", ttfb.Round(time.Millisecond).String(),
		"latency", time.Since(start).Round(time.Millisecond).String(), "rows", rows)
	return nil
}

func isCSVResponse(resp *http.Response) bool {
	return strings.Contains(resp.Header.Get("Content-Type"), "csv")
}

type csvParseError struct{ msg string }

func (e *csvParseError) Error() string { return "csv decode: " + e.msg }

func decodeCSVStream(r io.Reader, tags map[string]bool, ftypes map[string]string, fn func(cols []string, row []any) error) (int, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	header, err := cr.Read()
	if err != nil {
		return 0, &csvParseError{msg: "read header: " + err.Error()}
	}
	timeIdx := -1
	for i, c := range header {
		if c == "time" {
			timeIdx = i
			break
		}
	}
	if timeIdx < 0 || len(header) < timeIdx+1 {
		return 0, &csvParseError{msg: fmt.Sprintf("no time column in header %q", header)}
	}
	cols := append([]string{"time"}, header[timeIdx+1:]...)
	var rows int
	for {
		rec, err := cr.Read()
		if err != nil {
			if err == io.EOF {
				break
			}
			return rows, &csvParseError{msg: "read row: " + err.Error()}
		}
		if len(rec) == 0 {
			continue
		}
		if len(rec) == len(header) {
			same := true
			for i := range rec {
				if rec[i] != header[i] {
					same = false
					break
				}
			}
			if same {
				continue
			}
		}
		if len(rec) < len(header) {
			return rows, &csvParseError{msg: fmt.Sprintf("ragged row: %d fields, want %d", len(rec), len(header))}
		}
		ts, err := csvTimestamp(rec[timeIdx])
		if err != nil {
			return rows, &csvParseError{msg: err.Error()}
		}
		row := make([]any, 0, len(cols))
		row = append(row, json.Number(ts))
		for i, col := range cols[1:] {
			raw := rec[timeIdx+1+i]
			if raw == "" {
				row = append(row, nil)
				continue
			}
			if tags[col] {
				row = append(row, raw)
				continue
			}
			v, err := csvFieldValue(col, raw, ftypes[col])
			if err != nil {
				return rows, &csvParseError{msg: err.Error()}
			}
			row = append(row, v)
		}
		rows++
		if err := fn(cols, row); err != nil {
			return rows, err
		}
	}
	return rows, nil
}

func csvTimestamp(raw string) (string, error) {
	if _, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return raw, nil
	}
	if ts, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return strconv.FormatInt(ts.UnixNano(), 10), nil
	}
	return "", fmt.Errorf("bad timestamp %q", raw)
}

func csvFieldValue(col, raw, ftype string) (any, error) {
	switch ftype {
	case "boolean":
		if raw == "true" {
			return true, nil
		}
		if raw == "false" {
			return false, nil
		}
		return nil, fmt.Errorf("column %q: not a boolean %q", col, raw)
	case "string":
		return raw, nil
	case "integer", "float", "":
		if _, err := strconv.ParseFloat(raw, 64); err != nil {
			if ftype == "" {
				return raw, nil
			}
			return nil, fmt.Errorf("column %q: not a number %q", col, raw)
		}
		return json.Number(raw), nil
	default:
		return nil, fmt.Errorf("column %q: unknown type %q", col, ftype)
	}
}

func (d *Dumper) authMode() string {
	if d.Version() == 2 {
		if d.pass == "" {
			return "v2-token-missing"
		}
		return "v2-token"
	}
	if d.user == "" {
		return "none"
	}
	return "v1-basic"
}

func (d *Dumper) baseURL() string {
	return d.scheme() + "://" + net.JoinHostPort(d.host, strconv.Itoa(d.port))
}

func (d *Dumper) scheme() string {
	if d.tlsCfg != nil {
		return "https"
	}
	return "http"
}

func (d *Dumper) setAuth(req *http.Request) {
	if d.Version() == 2 {
		if d.pass != "" {
			req.Header.Set("Authorization", "Token "+d.pass)
		}
		return
	}
	if d.user != "" {
		req.SetBasicAuth(d.user, d.pass)
	}
}

func versionSource(configured int) string {
	if configured == 0 {
		return "detected"
	}
	return "config"
}

func versionString(v int) string {
	if v == 0 {
		return "auto"
	}
	return fmt.Sprintf("v%d", v)
}
