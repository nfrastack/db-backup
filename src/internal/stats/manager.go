// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package stats

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/container"
	"github.com/nfrastack/db-backup/internal/log"
)

// first run after fresh install delay
const initialReportDelay = 5 * time.Minute

type Manager struct {
	cfg        			*config.StatsConfig
	vc         			*config.CheckNewVersionConfig
	state      			*config.StatsState
	vstate     			*config.VersionState
	client     			*Client
	key        			string
	start      			time.Time
	warnedOnce 			bool
	container  			bool
	stateDir   			string
	nextVersionRetry 	time.Time
	nextStatsRetry   	time.Time
}

// snapshot of ok/failed counts for jobs for stats
type OutcomesRecorder interface {
	Snapshot() []JobOutcome
}

type reportData struct {
	opts       Options
	instanceID string
	prevVers   string
	outcomes   []JobOutcome
}

// version checking payload
func CheckPayload(tool, version string, opts Options) string {
	parts := []string{fmt.Sprintf("t=%s", tool), "v=" + version}
	if opts.Runtime.Container {
		parts = append(parts, "c=1")
	}
	if opts.ImageVersion != "" {
		parts = append(parts, "m="+opts.ImageVersion)
	}
	if opts.LicenseID != "" {
		parts = append(parts, "l="+opts.LicenseID)
	}
	if opts.Channel != "" {
		parts = append(parts, "h="+opts.Channel)
	}
	if opts.GitCommit != "" {
		parts = append(parts, "g="+opts.GitCommit)
	}
	return strings.Join(parts, " ")
}

// run an immediate version check, bypassing the scheduler and its frequency used by dbb version check. doesn't affect states LastCheckAt field
func (m *Manager) CheckVersionNow(ctx context.Context, version string, opts Options) (*VersionResponse, error) {
	if m == nil || m.client == nil {
		return nil, errors.New("usage stats manager unavailable - cannot run a version check")
	}
	return m.checkVersion(ctx, version, opts)
}

// enable statistics usage reporting. off by default
func (m *Manager) Enabled() bool {
	return m != nil && m.cfg != nil && m.cfg.Enabled
}

// generate, write instance_id just to differentiate new vs existing instance
func (m *Manager) EnsureInstanceID() string {
	if m == nil || m.state == nil {
		return ""
	}
	if m.state.InstanceID != "" {
		return m.state.InstanceID
	}
	id := newID()
	m.state.InstanceID = id
	if err := m.persist(); err != nil {
		m.warnNotPersisted(err)
	}
	return id
}

// expose the container image tag parsed from the nfrastack only build log for checking new version
func ImageVersion() string { return imageVersion() }

// be vocal about stats and version check decisions
func (m *Manager) LogStartup() {
	if m == nil {
		return
	}
	const docsURL = "https://nfrastack.com/db-backup/recipes/usage-stats"
	if !m.Enabled() {
		if m.container {
			log.Info("usage-stats", "usage stats collection is disabled. enable via STATS_ENABLED=TRUE. details: "+docsURL)
		} else {
			log.Info("usage-stats", "usage stats collection is disabled. enable it via stats.enabled in the config. details: "+docsURL)
		}
	} else {
		state := "ephemeral"
		if m.stateDir != "" {
			state = m.stateDir
		}
		now := time.Now()
		if loc := log.Location(); loc != nil {
			now = now.In(loc)
		}
		log.Info("usage-stats", "usage stats collection is enabled - thank you for helping improve db-backup by sharing anonymous usage data. details: "+docsURL,
			"frequency", statsFrequency(m.cfg),
			"last_report", lastActivityInLoc(m.state.LastReportAt),
			"next_report", m.nextDue(m.state.LastReportAt, statsFrequency(m.cfg), now),
			"payload_inspection", dumpDirDisplay(), "state", state)
	}

	if !m.VersionCheckEnabled() {
		if m.container {
			log.Info("version-check", "version check is disabled - you will not be notified when a new version is available. re-enable it via CHECK_NEW_VERSION=TRUE in the config.")
		} else {
			log.Info("version-check", "version check is disabled - you will not be notified when a new version is available. re-enable it via check_new_version.enabled in the config.")
		}
		return
	}
	now := time.Now()
	if loc := log.Location(); loc != nil {
		now = now.In(loc)
	}
	log.Info("version-check", "version check enabled - you will be notified when a new version is available.",
		"frequency", versionCheckFrequency(m.vc),
		"last_check", lastActivityInLoc(m.vstate.LastCheckAt),
		"next_check", m.nextDue(m.vstate.LastCheckAt, versionCheckFrequency(m.vc), now))
}

// manager for stats usage and version checking. reads config and state, creates client, sets up dump dir and retention
func NewManager(configPaths []string, key string) (*Manager, error) {
	startup, err := config.LoadStartupConfig(configPaths...)
	if err != nil {
		return nil, err
	}
	var cfg *config.StatsConfig
	var vc *config.CheckNewVersionConfig
	var st *config.StateConfig
	if startup != nil {
		cfg = startup.Stats
		vc = startup.CheckNewVersion
		st = startup.State
	}
	if cfg == nil {
		cfg = &config.StatsConfig{}
	}
	if vc == nil {
		vc = &config.CheckNewVersionConfig{}
	}
	if st == nil {
		st = &config.StateConfig{}
	}
	stateDir := config.ResolveStateDir(st.Dir)
	state, err := config.ReadStatsState(stateDir)
	if err != nil {
		log.Debug("usage-stats", "state unreadable - reporting ephemeral", "state_dir", stateDir, "error", err.Error())
		state = &config.StatsState{}
	}
	vstate, err := config.ReadVersionState(stateDir)
	if err != nil {
		log.Debug("version-check", "version state unreadable - ephemeral version checks", "state_dir", stateDir, "error", err.Error())
		vstate = &config.VersionState{}
	}
	serverURL := cfg.ServerURL
	dumpPath := cfg.DumpPath
	if dumpPath == "" {
		dumpPath = filepath.Join(stateDir, "stats")
	}
	SetDumpDir(dumpPath)
	if cfg.DumpRetention != "" {
		if d, err := config.ParseRetention(cfg.DumpRetention); err == nil {
			SetDumpRetention(d)
		} else {
			log.Warn("usage-stats", "invalid stats.dump_retention - using default 30d", "dump_retention", cfg.DumpRetention, "error", err.Error())
			SetDumpRetention(DefaultDumpRetention)
		}
	} else {
		SetDumpRetention(DefaultDumpRetention)
	}
	m := &Manager{
		cfg:      cfg,
		vc:       vc,
		state:    state,
		vstate:   vstate,
		client:   NewClient(serverURL, key),
		key:      key,
		start:    time.Now(),
		stateDir: stateDir,
	}
	return m, nil
}

// version reported in the last report or ""
func (m *Manager) PreviousVersion() string {
	if m == nil || m.vstate == nil {
		return ""
	}
	return m.vstate.PreviousVersion
}

// rememberversion to populate next report previous_version
func (m *Manager) RememberVersion(v string) {
	if m == nil || m.vstate == nil || v == "" {
		return
	}
	if m.vstate.PreviousVersion == v {
		return
	}
	m.vstate.PreviousVersion = v
	if err := m.persist(); err != nil {
		m.warnNotPersisted(err)
	}
}

// are we container or not? nfrastack branded container only unless --container flag
func (m *Manager) SetContainer(inContainer bool) {
	if m != nil {
		m.container = inContainer
	}
}

// manage the reporting frequency for stats and version checks
func (m *Manager) TryReport(ctx context.Context, opts Options, cfg *config.Config, jobs OutcomesRecorder) {
	if m == nil || m.client == nil {
		return
	}

	if m.VersionCheckEnabled() && opts.Version != "" {
		now := time.Now()
		if loc := log.Location(); loc != nil {
			now = now.In(loc)
		}
		if m.versionDue(now) {
			log.Debug("version-check", "starting version check")
			if resp, err := m.checkVersion(ctx, opts.Version, opts); err != nil {
				m.nextVersionRetry = time.Now().Add(frequencyDuration(versionCheckFrequency(m.vc)))
				log.Debug("version-check", fmt.Sprintf("version check failed: %s", DescribeError(err)), "next_check", formatDue(m.nextVersionRetry))
			} else if resp != nil {
				m.notifyVersion(opts.Version, opts, resp)
				m.vstate.LastCheckAt = time.Now().UTC().Format(time.RFC3339)
				m.nextVersionRetry = time.Time{}
				if err := m.persist(); err != nil {
					m.warnNotPersisted(err)
				}
				doneAt := time.Now()
				if loc := log.Location(); loc != nil {
					doneAt = doneAt.In(loc)
				}
				log.Debug("version-check", "check complete", "next_check", m.nextDue(m.vstate.LastCheckAt, versionCheckFrequency(m.vc), doneAt))
			} else {
				m.nextVersionRetry = time.Now().Add(frequencyDuration(versionCheckFrequency(m.vc)))
				log.Debug("version-check", "no release information returned", "next_check", formatDue(m.nextVersionRetry))
			}
		} else {
			log.Trace("version-check", "not due - skipping", "next_due", m.nextDue(m.vstate.LastCheckAt, versionCheckFrequency(m.vc), now))
		}
	}

	if !m.Enabled() {
		return
	}
	now := time.Now()
	if loc := log.Location(); loc != nil {
		now = now.In(loc)
	}
	if !m.statsDue(now) {
		log.Trace("usage-stats", "report not due - skipping", "next_due", m.nextDue(m.state.LastReportAt, statsFrequency(m.cfg), now))
		return
	}
	log.Debug("usage-stats", "starting usage-stats submission")

	instanceID := m.EnsureInstanceID()
	var outcomes []JobOutcome
	if jobs != nil {
		outcomes = jobs.Snapshot()
	}
	data := &reportData{
		opts:       opts,
		instanceID: instanceID,
		prevVers:   m.PreviousVersion(),
		outcomes:   outcomes,
	}

	t0 := time.Now()
	var opsToken string
	if m.Enabled() {
		since := m.eventAckAt()
		opsToken = WireToken(Window(since))
	}
	payload, err := renderReport(m, cfg, data, opsToken)
	if err != nil {
		m.nextStatsRetry = time.Now().Add(frequencyDuration(statsFrequency(m.cfg)))
		log.Debug("usage-stats", "skipping report", "error", err.Error(), "next_report", formatDue(m.nextStatsRetry))
		return
	}

	// leave inspectable copy of payload in stats dump path
	DumpPayload(payload, time.Now())

	if err := m.client.Report(ctx, payload); err != nil {
		m.nextStatsRetry = time.Now().Add(frequencyDuration(statsFrequency(m.cfg)))
		log.Debug("usage-stats", fmt.Sprintf("report failed: %s", DescribeError(err)), "next_report", formatDue(m.nextStatsRetry))
		return
	}
	m.state.LastReportAt = time.Now().UTC().Format(time.RFC3339)
	m.vstate.EventAckAt = t0.UTC().Format(time.RFC3339Nano)
	m.nextStatsRetry = time.Time{}
	if err := m.persist(); err != nil {
		m.warnNotPersisted(err)
	}
	m.RememberVersion(opts.Version)
	// purge consumed journal entries only after accepted
	Ack(t0)
	doneAt := time.Now()
	if loc := log.Location(); loc != nil {
		doneAt = doneAt.In(loc)
	}
	log.Debug("usage-stats", "report sent", "next_report", m.nextDue(m.state.LastReportAt, statsFrequency(m.cfg), doneAt))
}

// enable version checking on by default
func (m *Manager) VersionCheckEnabled() bool {
	return m == nil || m.vc == nil || m.vc.IsEnabled()
}

// checking version on demand
func (m *Manager) checkVersion(ctx context.Context, version string, opts Options) (*VersionResponse, error) {
	payload := CheckPayload(ToolDBBackup, version, opts)
	resp, err := m.client.CheckVersion(ctx, payload)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, nil
	}
	return resp, nil
}

func currentVersion(v string) string {
	if v == "" {
		return ""
	}
	return v
}

// janky deduplication check to avoid over collection
func (m *Manager) due(last, freq string, now time.Time) bool {
	if m == nil || m.state == nil || last == "" {
		return true
	}
	interval := frequencyDuration(freq)
	if interval <= 0 {
		return true
	}
	t, ok := parseActivityTime(last)
	if !ok {
		return true
	}
	return now.Sub(t) >= interval
}

func parseActivityTime(s string) (time.Time, bool) {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, true
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	return time.Time{}, false
}

func (m *Manager) dueTime(last, freq string, now time.Time) (time.Time, bool) {
	if m == nil || last == "" {
		return now.Add(initialReportDelay), true
	}
	interval := frequencyDuration(freq)
	if interval <= 0 {
		return now, true
	}
	t, ok := parseActivityTime(last)
	if !ok {
		return now, true
	}
	return t.Add(interval), true
}

func (m *Manager) versionDue(now time.Time) bool {
	if !m.nextVersionRetry.IsZero() && now.Before(m.nextVersionRetry) {
		return false
	}
	if m == nil || m.vstate == nil {
		return true
	}
	return m.due(m.vstate.LastCheckAt, versionCheckFrequency(m.vc), now)
}

// swhether a usage stats report should run now
func (m *Manager) statsDue(now time.Time) bool {
	if !m.nextStatsRetry.IsZero() && now.Before(m.nextStatsRetry) {
		return false
	}
	if m == nil || m.state == nil {
		return true
	}
	return m.due(m.state.LastReportAt, statsFrequency(m.cfg), now)
}

// format a due instant for log fields, in the configured log location.
func formatDue(t time.Time) string {
	if loc := log.Location(); loc != nil {
		t = t.In(loc)
	}
	return t.Format(time.RFC3339)
}

// return when next version check due. ok=false when version check disabled.
func (m *Manager) NextVersionDue(now time.Time) (time.Time, bool) {
	if !m.VersionCheckEnabled() {
		return time.Time{}, false
	}
	if m == nil || m.vstate == nil {
		return now.Add(initialReportDelay), true
	}
	t, _ := m.dueTime(m.vstate.LastCheckAt, versionCheckFrequency(m.vc), now)
	if !m.nextVersionRetry.IsZero() && m.nextVersionRetry.After(t) {
		t = m.nextVersionRetry
	}
	return t, true
}

// return when next stats report due. ok=false when disabled.
func (m *Manager) NextStatsDue(now time.Time) (time.Time, bool) {
	if !m.Enabled() {
		return time.Time{}, false
	}
	if m == nil || m.state == nil {
		return now.Add(initialReportDelay), true
	}
	t, _ := m.dueTime(m.state.LastReportAt, statsFrequency(m.cfg), now)
	if !m.nextStatsRetry.IsZero() && m.nextStatsRetry.After(t) {
		t = m.nextStatsRetry
	}
	return t, true
}

// returns the earliest upcoming due. now when due.
func (m *Manager) NextDue(now time.Time) time.Time {
	var best time.Time
	if t, ok := m.NextVersionDue(now); ok {
		best = t
	}
	if t, ok := m.NextStatsDue(now); ok {
		if best.IsZero() || t.Before(best) {
			best = t
		}
	}
	return best
}

// returns the ts of the last acknowledged submit
func (m *Manager) eventAckAt() time.Time {
	if m == nil || m.vstate == nil || m.vstate.EventAckAt == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, m.vstate.EventAckAt)
	if err != nil {
		return time.Time{}
	}
	return t
}
func frequencyDuration(freq string) time.Duration {
	switch freq {
	case "weekly":
		return 7 * 24 * time.Hour
	case "monthly":
		return 30 * 24 * time.Hour
	default: // "daily" or ""
		return 24 * time.Hour
	}
}
func imageLogCandidates() []string {
	return container.BuildLogCandidates(os.Getenv)
}

// nfrastack container detection via build log
func imageVersion() string {
	// explicit override wins over build log sniffing
	if v := os.Getenv("DBBACKUP_IMAGE_VERSION"); v != "" {
		return v
	}
	for _, p := range imageLogCandidates() {
		v := parseImageTag(p)
		if v != "" {
			return v
		}
	}
	for _, env := range []string{"IMAGE_VERSION", "IMAGE_TAG"} {
		if v := os.Getenv(env); v != "" {
			return v
		}
	}
	return ""
}

// when did we last run
func lastActivity(last string) string {
	if last == "" {
		return "never"
	}
	return last
}

func lastActivityInLoc(last string) string {
	if last == "" {
		return "never"
	}
	loc := log.Location()
	if loc == nil {
		return last
	}
	t, ok := parseActivityTime(last)
	if !ok {
		return last
	}
	return t.In(loc).Format(time.RFC3339)
}

// 128bit random id for instance_id
func newID() string {
	b := make([]byte, 16)
	if _, err := randRead(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", b)
}

// return next due time or 'now'
func (m *Manager) nextDue(last, freq string, now time.Time) string {
	if m == nil {
		return "now"
	}
	if last == "" {
		return formatDue(now.Add(initialReportDelay))
	}
	interval := frequencyDuration(freq)
	if interval <= 0 {
		return "now"
	}
	t, ok := parseActivityTime(last)
	if !ok {
		return "now"
	}
	next := t.Add(interval)
	if loc := log.Location(); loc != nil {
		next = next.In(loc)
	}
	return next.Format(time.RFC3339)
}

var OnLicenseVerdict func(licenseID string, revoked bool)

// announce the version response as info when it is debug if not
func (m *Manager) notifyVersion(current string, opts Options, resp *VersionResponse) {
	if opts.LicenseID != "" && OnLicenseVerdict != nil {
		OnLicenseVerdict(opts.LicenseID, resp.LicenseRevoked)
	}
	binaryNewer := resp.Latest != "" && IsNewer(current, resp.Latest)
	imageNewer := IsImageStale(opts.ImageVersion, resp.ImageLatest)

	switch {
	case binaryNewer:
		msg := fmt.Sprintf("new version available: %s", resp.Latest)
		if resp.DateReleased != "" {
			msg += fmt.Sprintf(" (released %s)", resp.DateReleased)
		}
		if resp.Critical {
			msg += " - marked critical"
		}
		fields := []any{"current", current}
		if resp.DownloadURL != "" {
			fields = append(fields, "download_url", resp.DownloadURL)
		}
		if resp.ChangelogURL != "" {
			fields = append(fields, "changelog_url", resp.ChangelogURL)
		}
		if resp.ImageLatest != "" && resp.ImageLatest != resp.Latest {
			fields = append(fields, "image_latest", resp.ImageLatest)
		}
		log.Info("version-check", msg, fields...)
	case imageNewer:
		log.Info("version-check", fmt.Sprintf("new container image available: %s (running %s)", resp.ImageLatest, opts.ImageVersion))
	default:
		if resp.Latest == "" {
			log.Debug("version-check", "no release information returned")
			return
		}
		log.Debug("version-check", fmt.Sprintf("up to date - running %s, server reports %s", current, resp.Latest))
	}
}
func parseImageTag(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	ver := ""
	seen := false
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		idx := strings.Index(t, "IMAGE:")
		if idx < 0 {
			continue
		}
		rest := strings.TrimSpace(t[idx+len("IMAGE:"):])
		fields := strings.Fields(rest)
		if len(fields) < 1 {
			continue
		}
		seen = true
		tag := "unknown" // tag is the field after the image name
		if len(fields) >= 2 {
			tag = fields[1]
		}
		if tag == "" {
			tag = "unknown"
		}
		ver = tag
	}
	if !seen {
		return ""
	}
	if ver == "unknown" {
		return ""
	}
	return ver
}

// writes usage stats to <state.dir>/stats.json and version state to <state.dir>/version.json
func (m *Manager) persist() error {
	if m.state == nil {
		return nil
	}
	if m.stateDir == "" {
		return errors.New("no state.dir configured - state is ephemeral")
	}
	if err := config.WriteStatsState(m.stateDir, m.state); err != nil {
		return err
	}
	if m.vstate != nil {
		if err := config.WriteVersionState(m.stateDir, m.vstate); err != nil {
			return err
		}
	}
	return nil
}
func renderReport(m *Manager, cfg *config.Config, d *reportData, opsToken string) (string, error) {
	b := NewReportBuilder(
		d.opts,
		d.instanceID,
		d.prevVers,
		time.Since(m.start),
		d.outcomes,
	)
	b.SetOps(opsToken)
	return b.Build(cfg)
}

// reporting interval
func statsFrequency(cfg *config.StatsConfig) string {
	return config.DefaultStatsFrequency
}

// version check interval
func versionCheckFrequency(vc *config.CheckNewVersionConfig) string {
	if vc == nil || vc.Frequency == "" {
		return config.DefaultCheckNewVersionFrequency
	}
	return vc.Frequency
}

// don't nag - only warn once when no state dir
func (m *Manager) warnNotPersisted(err error) {
	if m.warnedOnce {
		return
	}
	m.warnedOnce = true
	if m.stateDir == "" {
		log.Warn("usage-stats", "state not persisted - set state.dir to a writable directory so the instance id survives restarts")
		return
	}
	log.Warn("usage-stats", "state not persisted - instance id will change on restart", "state_dir", m.stateDir, "error", err.Error())
}
