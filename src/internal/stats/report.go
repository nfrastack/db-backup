// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package stats

import (
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/nfrastack/db-backup/internal/config"
)

type RuntimeInfo struct {
	Container bool
	Systemd   bool
}
type LogOptions struct {
	Level  string // debug|info|warn|error|none
	Format string // text|json
	Type   string // console|file|both
}
type Options struct {
	Version      string
	ImageVersion string // parsed from /container/build/build.log when container
	LicenseID    string // attached to reports so usage per license is measurable
	Channel      string // stable | beta | edge
	GitCommit    string // short sha this binary was built from
	Runtime      RuntimeInfo
	Log          LogOptions
}

// success/failure sum, total run duration and retention activity in the window
type JobOutcome struct {
	Type     string
	Success  int
	Failed   int
	Duration time.Duration // total run time (success + failure) in the window
	Pruned   int           // backup files deleted by retention in the window
	Archived int           // backup files moved to archive storage in the window
}
type ReportBuilder struct {
	opts       Options
	instanceID string
	prevVers   string // previous version from last time
	uptime     time.Duration
	reported   time.Time
	outcomes   []JobOutcome
	ops        string // aggregated o= token from the local ops journal
}

func (b *ReportBuilder) Build(cfg *config.Config) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("nil config")
	}

	outcomes := make(map[string]*JobOutcome, len(b.outcomes))
	for i := range b.outcomes {
		o := b.outcomes[i]
		if o.Type == "" {
			continue
		}
		key := normalize(o.Type)
		if _, ok := outcomes[key]; !ok {
			c := o
			outcomes[key] = &c
		} else {
			outcomes[key].Success += o.Success
			outcomes[key].Failed += o.Failed
		}
	}

	var jobs []string
	for _, job := range cfg.Jobs {
		jobs = append(jobs, b.encodeJob(job, outcomes))
	}

	vs := []string{
		"sv=" + itoa(SchemaVersion),
		"i=" + b.instanceID,
		"t=" + ToolDBBackup,
		"v=" + b.opts.Version,
		"d=" + b.reported.Format(time.RFC3339),
	}
	if b.prevVers != "" {
		vs = append(vs, "p="+b.prevVers)
	}
	if b.opts.LicenseID != "" {
		vs = append(vs, "e="+b.opts.LicenseID)
	}
	if b.opts.Channel != "" {
		vs = append(vs, "h="+b.opts.Channel)
	}
	if b.opts.GitCommit != "" {
		vs = append(vs, "g="+b.opts.GitCommit)
	}
	if b.ops != "" {
		vs = append(vs, "o="+b.ops)
	}
	vs = append(vs,
		"r="+b.runtimeCode(),
		"u="+itoa(int(b.uptime.Hours())),
		"n="+itoa(len(cfg.Jobs)),
		"l="+b.logCode(),
	)
	if b.opts.Runtime.Container {
		vs = append(vs, "m="+b.imageVersionCode())
	}
	if len(jobs) > 0 {
		vs = append(vs, "j="+strings.Join(jobs, "|"))
	}
	return strings.Join(vs, " "), nil
}
func NewReportBuilder(opts Options, instanceID, prevVers string, uptime time.Duration, outcomes []JobOutcome) *ReportBuilder {
	return &ReportBuilder{
		opts:       opts,
		instanceID: instanceID,
		prevVers:   prevVers,
		uptime:     uptime,
		reported:   time.Now().UTC(),
		outcomes:   outcomes,
	}
}

// attachrs journal ops aggregate for report window
func (b *ReportBuilder) SetOps(token string) { b.ops = token }
func (b *ReportBuilder) encodeJob(job config.JobConfig, outcomes map[string]*JobOutcome) string {
	compression := "0"
	if job.Compression != nil {
		compression = compressionCode(job.Compression.Type)
	}

	strategy := stratFull
	if job.Backup != nil {
		strategy = strategyCode(job.Backup.EffectiveStrategy())
	}

	enc := "0"
	encMode := encModeNone
	if job.EncryptionType != "" {
		enc = encryptionCode(job.EncryptionType)
		encMode = encryptionModeCode(job.EncryptionType, job.EncryptionIdentity, job.EncryptionIdentities)
	}

	storage := storeOther
	if job.Storage != nil {
		storage = storageCode(job.Storage.Backend)
	}

	sched := scheduleCode(job.Schedule)

	maint := flagNo
	if job.MaintenanceCfg != nil {
		maint = flagYes
	}
	archive := flagNo
	if job.Archive != nil {
		archive = flagYes
	}

	succ, fail := 0, 0
	dur := int64(0)
	pruned, archived := 0, 0
	if o, ok := outcomes[normalize(job.Type)]; ok {
		succ, fail = o.Success, o.Failed
		dur = o.Duration.Milliseconds()
		pruned, archived = o.Pruned, o.Archived
	}

	return strings.Join([]string{
		dbTypeCode(job.Type),
		strategy,
		compression,
		enc,
		encMode,
		storage,
		sched,
		maint,
		archive,
		itoa(succ),
		itoa(fail),
		retentionCode(job.Retention),
		itoa64(dur),
		itoa(pruned),
		itoa(archived),
	}, ":")
}

func (b *ReportBuilder) imageVersionCode() string {
	if v := b.opts.ImageVersion; v != "" {
		return v
	}
	return "0"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func (b *ReportBuilder) logCode() string {
	level := logLevelInfo
	switch normalize(b.opts.Log.Level) {
	case "debug", "trace":
		level = logLevelDebug
	case "warn", "warning":
		level = logLevelWarn
	case "error":
		level = logLevelError
	case "none":
		level = logLevelInfo
	}

	format := logFmtText
	if normalize(b.opts.Log.Format) == "json" {
		format = logFmtJSON
	}

	dest := logDestConsole
	switch normalize(b.opts.Log.Type) {
	case "file":
		dest = logDestFile
	case "both":
		dest = logDestBoth
	}
	return level + format + dest
}

func (b *ReportBuilder) runtimeCode() string {
	osCode := osLinux
	switch runtime.GOOS {
	case "windows":
		osCode = osWindows
	case "darwin":
		osCode = osDarwin
	case "freebsd":
		osCode = osFreeBSD
	case "openbsd":
		osCode = osOpenBSD
	}

	archCode := archAmd64
	switch runtime.GOARCH {
	case "arm64":
		archCode = archArm64
	case "arm":
		archCode = archArm
	}

	c := flagNo
	if b.opts.Runtime.Container {
		c = flagYes
	}
	sd := flagNo
	if b.opts.Runtime.Systemd {
		sd = flagYes
	}
	return osCode + archCode + c + sd
}
