// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nfrastack/db-backup/internal/license"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Concurrency     int                    `yaml:"concurrency,omitempty"`
	Defaults        *DefaultsConfig        `yaml:"defaults"`
	Log             *LogConfig             `yaml:"log,omitempty"`
	Jobs            []JobConfig            `yaml:"jobs"`
	Restore         *RestoreConfig         `yaml:"restore,omitempty"`
	License         *LicenseConfig         `yaml:"license,omitempty"`
	Profiles        *ProfilesConfig        `yaml:"profiles,omitempty"`
	State           *StateConfig           `yaml:"state,omitempty"`
	Stats           *StatsConfig           `yaml:"stats,omitempty"`
	CheckNewVersion *CheckNewVersionConfig `yaml:"check_new_version,omitempty"`
	TempDir         string                 `yaml:"temp_dir,omitempty"`

	Connections         map[string]ConnConfig        `yaml:"-"`
	Databases           map[string]DbProfile         `yaml:"-"`
	StorageProfiles     map[string]StorageConfig     `yaml:"-"`
	BackupProfiles      map[string]BackupProfile     `yaml:"-"`
	MaintenanceProfiles map[string]MaintenanceConfig `yaml:"-"`
	EncryptionProfiles  map[string]EncryptionConfig  `yaml:"-"`
	ArchiveProfiles     map[string]ArchiveConfig     `yaml:"-"`
	RestoreProfiles     map[string]RestoreConfig     `yaml:"-"`
}

var SupporterResolveRestore = func(c *Config, r *RestoreConfig) {}

type ProfilesConfig struct {
	Connections map[string]ConnConfig        `yaml:"connections,omitempty"`
	Databases   map[string]DbProfile         `yaml:"databases,omitempty"`
	Backup      map[string]BackupProfile     `yaml:"backup,omitempty"`
	Storage     map[string]StorageConfig     `yaml:"storage,omitempty"`
	Encryption  map[string]EncryptionConfig  `yaml:"encryption,omitempty"`
	Maintenance map[string]MaintenanceConfig `yaml:"maintenance,omitempty"`
	Archive     map[string]ArchiveConfig     `yaml:"archive,omitempty"`
	Restore     map[string]RestoreConfig     `yaml:"restore,omitempty"`
}

type LogConfig struct {
	Level        string `yaml:"level,omitempty"`
	Format       string `yaml:"format,omitempty"`
	Type         string `yaml:"type,omitempty"`
	Path         string `yaml:"path,omitempty"`
	User         string `yaml:"user,omitempty"`
	Group        string `yaml:"group,omitempty"`
	FileMode     string `yaml:"file_mode,omitempty"`
	DirMode      string `yaml:"dir_mode,omitempty"`
	Colour       *bool  `yaml:"colour,omitempty"`
	Prefix       *bool  `yaml:"prefix,omitempty"`
	PrefixFormat string `yaml:"prefix_format,omitempty"`
	SessionID    *bool  `yaml:"session_id,omitempty"`
	RunID        *bool  `yaml:"run_id,omitempty"`
	Timings      *bool  `yaml:"timings,omitempty"`
	SizeFormat   string `yaml:"size_format,omitempty"`
	Timezone     string `yaml:"timezone,omitempty"`
	UTC          *bool  `yaml:"utc,omitempty"`
	LevelStyle   string `yaml:"level_style,omitempty"`
	TimeQuoted   *bool  `yaml:"time_quoted,omitempty"`
}

type ConnConfig struct {
	Type         string              `yaml:"type"`
	Host         string              `yaml:"host"`
	Port         int                 `yaml:"port"`
	User         string              `yaml:"user"`
	Pass         string              `yaml:"pass"`
	Version      int                 `yaml:"version,omitempty"`
	Connectivity *ConnectivityConfig `yaml:"connectivity,omitempty"`
	TLS          *TLSConfig          `yaml:"tls,omitempty"`
	AuthSource   string              `yaml:"auth_source,omitempty"`
}

type RestoreConfig struct {
	File       string `yaml:"file"`
	Base       string `yaml:"base,omitempty"`
	Connection string `yaml:"connection"`
	StorageRef string `yaml:"storage"`
	ProfileRef string `yaml:"profile,omitempty"`

	Identity   string `yaml:"identity,omitempty"`
	Passphrase string `yaml:"passphrase,omitempty"`

	Type       string         `yaml:"-"`
	Host       string         `yaml:"-"`
	Port       int            `yaml:"-"`
	User       string         `yaml:"-"`
	Pass       string         `yaml:"-"`
	AuthSource string         `yaml:"-"`
	TLS        *TLSConfig     `yaml:"-"`
	Storage    *StorageConfig `yaml:"-"`
}

type LicenseConfig struct {
	File       string `yaml:"file,omitempty"`
	InstallDir string `yaml:"install_dir,omitempty"`
	WarnDays   int    `yaml:"warn_days,omitempty"`
}

type StateConfig struct {
	Dir string `yaml:"dir,omitempty"`
}

// usage statistics
const DefaultStatsFrequency = "daily"

type StatsConfig struct {
	Enabled       bool   `yaml:"enabled"`
	ServerURL     string `yaml:"server_url,omitempty"`
	DumpPath      string `yaml:"dump_path,omitempty"`
	DumpRetention string `yaml:"dump_retention,omitempty"`
}

// state directory
// linux /var/lib/dbb
// freebsd /var/lib/dbb
// macos ~/Library/Application Support/dbb
// windows %APPDATA%\dbb
var stateDirHome = os.UserHomeDir
var stateDirProbe = dirWritable

// check for new version
// hardcoded default interval - needs to be enabled to be effective
const DefaultCheckNewVersionFrequency = "daily"

// whether version checking should run. unset defaults to true
type CheckNewVersionConfig struct {
	Enabled   *bool  `yaml:"enabled,omitempty"`
	Frequency string `yaml:"frequency,omitempty"`
}

// runtime identity for usage stats written to <state.dir>/stats.json
type StatsState struct {
	InstanceID   string `json:"instance_id"`
	LastReportAt string `json:"last_report_at,omitempty"`
}

// version check state written to <state.dir>/version.json
type VersionState struct {
	PreviousVersion string `json:"previous_version"`
	LastCheckAt     string `json:"last_check_at,omitempty"`
	EventAckAt      string `json:"event_ack_at,omitempty"`
}

type DefaultsConfig struct {
	Backup       *BackupConfig       `yaml:"backup"`
	Compression  *CompressionConfig  `yaml:"compression"`
	Checksum     string              `yaml:"checksum"`
	Storage      *StorageConfig      `yaml:"storage"`
	TLS          *TLSConfig          `yaml:"tls"`
	Retention    *RetentionConfig    `yaml:"retention"`
	Archive      *ArchiveConfig      `yaml:"archive"`
	Hooks        *HooksConfig        `yaml:"hooks"`
	Connectivity *ConnectivityConfig `yaml:"connectivity,omitempty"`
	Progress     *bool               `yaml:"progress,omitempty"`
}

type HooksConfig struct {
	Pre  []string `yaml:"pre"`
	Post []string `yaml:"post"`
}

type ConnectivityConfig struct {
	Enabled       bool   `yaml:"enabled"`
	Method        string `yaml:"method"`
	RetryInterval int    `yaml:"retry_interval"`
	Timeout       int    `yaml:"timeout"`
}

const (
	MethodNone   = "none"
	MethodSimple = "simple"
	MethodFull   = "full"
)

type StartupConfig struct {
	State           *StateConfig           `yaml:"state,omitempty"`
	Stats           *StatsConfig           `yaml:"stats,omitempty"`
	CheckNewVersion *CheckNewVersionConfig `yaml:"check_new_version,omitempty"`
	TempDir         string                 `yaml:"temp_dir,omitempty"`
}

func (c *Config) EffectiveRestore(profileName string) *RestoreConfig {
	if c == nil {
		return nil
	}
	if profileName != "" {
		prof, ok := c.RestoreProfiles[profileName]
		if !ok {
			return nil
		}
		r := prof
		c.ResolveRestore(&r)
		return &r
	}
	return c.Restore
}

func (v *CheckNewVersionConfig) IsEnabled() bool {
	if v == nil || v.Enabled == nil {
		return true
	}
	return *v.Enabled
}

func LoadConfig(paths ...string) (*Config, error) {
	var cfg Config

	b, err := loadMerged(paths)
	if err != nil {
		return nil, err
	}
	if b != nil {
		if err := yaml.Unmarshal(b, &cfg); err != nil {
			return nil, fmt.Errorf("parsing config: %w", err)
		}
	}

	cfg.mergeProfiles()

	if cfg.Stats != nil && cfg.Stats.DumpRetention != "" {
		if _, err := ParseRetention(cfg.Stats.DumpRetention); err != nil {
			return nil, fmt.Errorf("stats.dump_retention: %w", err)
		}
	}
	if err := cfg.CheckNewVersion.Validate(); err != nil {
		return nil, err
	}

	for i := range cfg.Jobs {
		cfg.resolveJob(&cfg.Jobs[i])
		if err := cfg.Jobs[i].Validate(); err != nil {
			return nil, err
		}
	}

	for i := range cfg.Jobs {
		resolveSecrets(&cfg.Jobs[i])
	}

	for _, job := range cfg.Jobs {
		if job.Backup != nil && (job.Backup.FullEvery > 0 || job.Backup.FullAfter != "") {
			if err := license.AllowIncremental(); err != nil {
				if job.Backup.FullEvery > 0 {
					fmt.Fprintf(os.Stderr, "WARN: job %q backup.full_every=%d requires Supporter license (incremental) - ignored\n", job.Name, job.Backup.FullEvery)
					job.Backup.FullEvery = 0
				}
				if job.Backup.FullAfter != "" {
					fmt.Fprintf(os.Stderr, "WARN: job %q backup.full_after=%q requires Supporter license (incremental) - ignored\n", job.Name, job.Backup.FullAfter)
					job.Backup.FullAfter = ""
				}
			}
		}
	}

	if len(cfg.Jobs) == 0 && cfg.Restore == nil && len(cfg.RestoreProfiles) == 0 {
		return nil, fmt.Errorf("no backup jobs or restore target configured")
	}

	cfg.ResolveRestore(cfg.Restore)

	return &cfg, nil
}

func LoadLicenseConfig(paths ...string) (*LicenseConfig, error) {
	var cfg Config
	b, err := loadMerged(paths)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, nil
	}
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	return cfg.License, nil
}
func LoadLogConfig(paths ...string) (*LogConfig, error) {
	var cfg Config
	b, err := loadMerged(paths)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, nil
	}
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	return cfg.Log, nil
}

// reads just the state, stats and check_new_version sections of the config file without validating jobs or performing env/file secret resolution
func LoadStartupConfig(paths ...string) (*StartupConfig, error) {
	var cfg Config
	b, err := loadMerged(paths)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, nil
	}
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	if cfg.Stats != nil && cfg.Stats.DumpRetention != "" {
		if _, err := ParseRetention(cfg.Stats.DumpRetention); err != nil {
			return nil, fmt.Errorf("stats.dump_retention: %w", err)
		}
	}
	if err := cfg.CheckNewVersion.Validate(); err != nil {
		return nil, err
	}
	return &StartupConfig{State: cfg.State, Stats: cfg.Stats, CheckNewVersion: cfg.CheckNewVersion, TempDir: cfg.TempDir}, nil
}

func LoadStorageProfile(paths []string, name string) (*StorageConfig, error) {
	var cfg Config
	b, err := loadMerged(paths)
	if err != nil {
		return nil, err
	}
	if b != nil {
		if err := yaml.Unmarshal(b, &cfg); err != nil {
			return nil, fmt.Errorf("parsing config: %w", err)
		}
	}
	cfg.mergeProfiles()
	prof, ok := cfg.StorageProfiles[name]
	if !ok {
		return nil, fmt.Errorf("storage profile %q not found in %s", name, strings.Join(paths, ", "))
	}
	prof.resolveSecrets()
	return &prof, nil
}
func ParseRetention(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	switch s {
	case "0":
		return 0, nil
	case "-1":
		return -1, nil
	}

	num := s
	mult := time.Second
	switch {
	case strings.HasSuffix(s, "s"):
		num = strings.TrimSuffix(s, "s")
		mult = time.Second
	case strings.HasSuffix(s, "m"):
		num = strings.TrimSuffix(s, "m")
		mult = time.Minute
	case strings.HasSuffix(s, "h"):
		num = strings.TrimSuffix(s, "h")
		mult = time.Hour
	case strings.HasSuffix(s, "d"):
		num = strings.TrimSuffix(s, "d")
		mult = 24 * time.Hour
	default:
		return 0, fmt.Errorf("invalid %q: expected 0, -1, or a duration like 30d, 24h, 30m, 90s", s)
	}
	n, err := strconv.Atoi(num)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid %q: expected 0, -1, or a duration like 30d, 24h, 30m, 90s", s)
	}
	return time.Duration(n) * mult, nil
}

func ReadStatsState(stateDir string) (*StatsState, error) {
	p := StatsStatePath(stateDir)
	if p == "" {
		return &StatsState{}, nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return &StatsState{}, nil
		}
		return nil, fmt.Errorf("reading stats state: %w", err)
	}
	var st StatsState
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, fmt.Errorf("parsing stats state: %w", err)
	}
	return &st, nil
}
func (c *Config) ResolveRestore(r *RestoreConfig) {
	if r == nil {
		return
	}
	SupporterResolveRestore(c, r)
}

func (c *Config) ResolveRestoreProfile(name string) *RestoreConfig {
	if c == nil || c.RestoreProfiles == nil {
		return nil
	}
	prof, ok := c.RestoreProfiles[name]
	if !ok {
		return nil
	}
	r := prof
	c.ResolveRestore(&r)
	return &r
}

func ResolveStateDir(configured string) string {
	if configured != "" {
		return configured
	}
	if d := os.Getenv("DBBACKUP_HOME"); d != "" {
		return d
	}
	dir := DefaultStateDir()
	if stateDirProbe(dir) {
		return dir
	}
	if home, err := stateDirHome(); err == nil && home != "" {
		return filepath.Join(home, ".local", "state", "dbb")
	}
	return dir
}

func ResolveStorageArg(configPaths []string, profile, path string) (*StorageConfig, error) {
	if profile != "" {
		if len(configPaths) == 0 {
			return nil, fmt.Errorf("--storage-profile requires -c <config>")
		}
		return LoadStorageProfile(configPaths, profile)
	}
	return &StorageConfig{Backend: "filesystem", Path: path}, nil
}

func ResolveTempDir(configured string) string {
	if configured != "" {
		return configured
	}
	if d := os.Getenv("TMPDIR"); d != "" {
		return d
	}
	return os.TempDir()
}

func StatsStatePath(stateDir string) string {
	if stateDir == "" {
		return ""
	}
	return filepath.Join(stateDir, "stats.json")
}
func (v *CheckNewVersionConfig) Validate() error {
	if v == nil {
		return nil
	}
	switch v.Frequency {
	case "", "daily", "weekly", "monthly":
		return nil
	}
	return fmt.Errorf("check_new_version.frequency must be daily, weekly or monthly (got %q)", v.Frequency)
}

func (c *ConnectivityConfig) Validate() error {
	if c == nil {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(c.Method)) {
	case "", MethodFull:
		c.Method = MethodFull
	case MethodNone, MethodSimple:

	default:
		return fmt.Errorf("connectivity.method: invalid value %q (want none|simple|full)", c.Method)
	}
	return nil
}

func WriteStatsState(stateDir string, st *StatsState) error {
	p := StatsStatePath(stateDir)
	if p == "" {
		return nil
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("creating state dir: %w", err)
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("serializing usage stats state: %w", err)
	}
	b = append(b, '\n')
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("writing usage stats state: %w", err)
	}
	return os.Rename(tmp, p)
}

func defaultBool(val, def bool) bool {
	if val {
		return val
	}
	return def
}

func defaultInt(val, def int) int {
	if val != 0 {
		return val
	}
	return def
}

func defaultString(val, def string) string {
	if val != "" {
		return val
	}
	return def
}

func ReadVersionState(stateDir string) (*VersionState, error) {
	p := VersionStatePath(stateDir)
	if p == "" {
		return &VersionState{}, nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return &VersionState{}, nil
		}
		return nil, fmt.Errorf("reading version state: %w", err)
	}
	var st VersionState
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, fmt.Errorf("parsing version state: %w", err)
	}
	return &st, nil
}

func VersionStatePath(stateDir string) string {
	if stateDir == "" {
		return ""
	}
	return filepath.Join(stateDir, "version.json")
}

func WriteVersionState(stateDir string, st *VersionState) error {
	p := VersionStatePath(stateDir)
	if p == "" {
		return nil
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("creating state dir: %w", err)
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("serializing version state: %w", err)
	}
	b = append(b, '\n')
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("writing version state: %w", err)
	}
	return os.Rename(tmp, p)
}

func dirWritable(dir string) bool {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false
	}
	probe := filepath.Join(dir, ".write-test")
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return false
	}
	if err := f.Close(); err != nil {
		return false
	}
	return os.Remove(probe) == nil
}

func globMatchAny(patterns []string, s string) bool {
	for _, p := range patterns {
		if p == s || p == "*" {
			return true
		}
		if ok, _ := path.Match(p, s); ok {
			return true
		}
	}
	return false
}

func mergeProfileMaps[V any](flat, nested map[string]V) map[string]V {
	if len(nested) == 0 {
		return flat
	}
	if flat == nil {
		flat = make(map[string]V, len(nested))
	}
	for k, v := range nested {
		if _, exists := flat[k]; !exists {
			flat[k] = v
		}
	}
	return flat
}
func (c *Config) mergeProfiles() {
	if c.Profiles == nil {
		return
	}
	p := c.Profiles
	c.Connections = mergeProfileMaps(c.Connections, p.Connections)
	c.Databases = mergeProfileMaps(c.Databases, p.Databases)
	c.BackupProfiles = mergeProfileMaps(c.BackupProfiles, p.Backup)
	c.StorageProfiles = mergeProfileMaps(c.StorageProfiles, p.Storage)
	c.EncryptionProfiles = mergeProfileMaps(c.EncryptionProfiles, p.Encryption)
	c.MaintenanceProfiles = mergeProfileMaps(c.MaintenanceProfiles, p.Maintenance)
	c.ArchiveProfiles = mergeProfileMaps(c.ArchiveProfiles, p.Archive)
	c.RestoreProfiles = mergeProfileMaps(c.RestoreProfiles, p.Restore)
}

func (c *Config) resolveJob(job *JobConfig) {

	var connConnectivity *ConnectivityConfig

	if job.DatabaseRef != "" {
		if c != nil && c.Databases != nil {
			if prof, ok := c.Databases[job.DatabaseRef]; ok {
				if job.Databases == nil {
					job.Databases = &DatabaseList{}
				}
				if len(job.Databases.Include) == 0 && len(prof.Include) > 0 && !job.unsetKey("databases") {
					job.Databases.Include = prof.Include
				}
				if len(job.Databases.Include) == 0 && prof.Name != "" && !job.unsetKey("databases") {
					job.Databases.Include = []string{prof.Name}
				}
				if len(job.Databases.Exclude) == 0 && len(prof.Exclude) > 0 && !job.unsetKey("databases") {
					job.Databases.Exclude = prof.Exclude
				}
				if job.Databases.Tables == nil && prof.Tables != nil && !job.unsetKey("tables") {
					t := *prof.Tables
					job.Databases.Tables = &t
				}

				if prof.Connection != "" && c.Connections != nil {
					if conn, ok := c.Connections[prof.Connection]; ok {
						if job.Type == "" && !job.unsetKey("type") {
							job.Type = conn.Type
						}
						if job.Host == "" && !job.unsetKey("host") {
							job.Host = conn.Host
						}
						if job.Port == 0 && !job.unsetKey("port") {
							job.Port = conn.Port
						}
						if job.User == "" && !job.unsetKey("user") {
							job.User = conn.User
						}
						if job.Pass == "" && !job.unsetKey("pass") {
							job.Pass = conn.Pass
						}
						if job.Version == 0 && !job.unsetKey("version") {
							job.Version = conn.Version
						}
						if job.AuthSource == "" && !job.unsetKey("auth_source") {
							job.AuthSource = conn.AuthSource
						}
						if job.TLS == nil && conn.TLS != nil && !job.unsetKey("tls") {
							cp := *conn.TLS
							job.TLS = &cp
						}
						if conn.Connectivity != nil && !job.unsetKey("connectivity") {
							cp := *conn.Connectivity
							connConnectivity = &cp
						}
					} else {
						fmt.Fprintf(os.Stderr, "WARN: connection %q for database profile %q not found\n", prof.Connection, job.DatabaseRef)
					}
				}
			} else {
				if job.Databases == nil {
					job.Databases = &DatabaseList{}
				}
				if len(job.Databases.Include) == 0 {
					job.Databases.Include = []string{job.DatabaseRef}
				}
			}
		} else if job.Databases == nil {
			job.Databases = &DatabaseList{Include: []string{job.DatabaseRef}}
		}
	}

	if job.Blackout != nil && len(*job.Blackout) > 0 {
		if job.Schedule == nil {
			job.Schedule = &Schedule{}
		}
		if job.Schedule.Blackout == nil || len(*job.Schedule.Blackout) == 0 {
			job.Schedule.Blackout = job.Blackout
		}
	}

	if job.Encryption != "" && c != nil && c.EncryptionProfiles != nil {
		if prof, ok := c.EncryptionProfiles[job.Encryption]; ok {
			job.EncryptionType = prof.Type
			if prof.Passphrase != "" {
				job.EncryptionIdentity = prof.Passphrase
				job.EncryptionIdentities = []string{prof.Passphrase}
			} else {
				job.EncryptionIdentities = prof.Recipients
			}
		} else {
			fmt.Fprintf(os.Stderr, "WARN: encryption profile %q not found\n", job.Encryption)
		}
	}

	if job.BackupRef != "" && c != nil && c.BackupProfiles != nil {
		if prof, ok := c.BackupProfiles[job.BackupRef]; ok {
			if job.Backup == nil {
				job.Backup = &BackupConfig{}
			}
			if !job.Backup.unsetKey("strategy") && job.Backup.Strategy == "" && prof.Strategy != "" {
				job.Backup.Strategy = prof.Strategy
			}
			if !job.Backup.unsetKey("filename") && job.Backup.Filename == "" {
				job.Backup.Filename = prof.Filename
			}
			if job.Compression == nil && prof.Compression != nil && !job.unsetKey("compression") {
				cp := *prof.Compression
				job.Compression = &cp
			}
			if job.Checksum == "" && prof.Checksum != "" && !job.unsetKey("checksum") {
				job.Checksum = prof.Checksum
			}
			if job.Retention == nil && prof.Retention != nil && !job.unsetKey("retention") {
				r := *prof.Retention
				job.Retention = &r
			}
		} else {
			fmt.Fprintf(os.Stderr, "WARN: backup profile %q not found\n", job.BackupRef)
		}
	}

	if job.StorageRef != "" && c != nil && c.StorageProfiles != nil {
		if prof, ok := c.StorageProfiles[job.StorageRef]; ok {
			if job.Storage == nil {
				sp := prof
				job.Storage = &sp
			} else {
				mergeStorage(job.Storage, &prof)
			}
		} else {
			fmt.Fprintf(os.Stderr, "WARN: storage profile %q not found\n", job.StorageRef)
		}
	}

	if job.Compression == nil {
		if c != nil && c.Defaults != nil && c.Defaults.Compression != nil {
			cp := *c.Defaults.Compression
			job.Compression = &cp
		} else {
			job.Compression = &CompressionConfig{Type: "zstd", Level: 3, Threads: 1}
		}
	}
	if job.unsetKey("compression") {
		job.Compression = &CompressionConfig{}
	}

	if job.Checksum == "" {
		if !job.unsetKey("checksum") && c != nil && c.Defaults != nil && c.Defaults.Checksum != "" {
			job.Checksum = c.Defaults.Checksum
		} else {
			job.Checksum = "md5"
		}
	}

	if job.Storage == nil {
		if c != nil && c.Defaults != nil && c.Defaults.Storage != nil && !job.unsetKey("storage") {
			cp := *c.Defaults.Storage
			job.Storage = &cp
		} else {
			job.Storage = &StorageConfig{Backend: "filesystem"}
		}
	}

	if job.Storage.Backend == "" && job.Storage.unsetKey("backend") {
		job.Storage.Backend = "filesystem"
	}
	if job.Storage.FileMode == "" {
		job.Storage.FileMode = "600"
	}
	if job.Storage.DirMode == "" {
		job.Storage.DirMode = "700"
	}

	if job.TLS == nil {
		if c != nil && c.Defaults != nil && c.Defaults.TLS != nil && !job.unsetKey("tls") {
			cp := *c.Defaults.TLS
			job.TLS = &cp
		} else {
			job.TLS = &TLSConfig{}
		}
	}

	if job.Backup == nil && c != nil && c.Defaults != nil && c.Defaults.Backup != nil && !job.unsetKey("backup") {
		cp := *c.Defaults.Backup
		job.Backup = &cp
	}
	if job.Backup == nil {
		job.Backup = &BackupConfig{}
	}

	if job.Connectivity == nil {
		switch {
		case !job.unsetKey("connectivity") && connConnectivity != nil:
			job.Connectivity = connConnectivity
		case !job.unsetKey("connectivity") && c != nil && c.Defaults != nil && c.Defaults.Connectivity != nil:
			cp := *c.Defaults.Connectivity
			job.Connectivity = &cp
		default:
			job.Connectivity = &ConnectivityConfig{
				Enabled:       true,
				Method:        MethodFull,
				RetryInterval: 5,
				Timeout:       30,
			}
		}
	}
	if err := job.Connectivity.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "WARN: job %q: %v\n", job.Name, err)
	}

	if job.Maintenance != "" && c != nil && c.MaintenanceProfiles != nil {
		if prof, ok := c.MaintenanceProfiles[job.Maintenance]; ok {
			mc := prof
			job.MaintenanceCfg = &mc
		} else {
			fmt.Fprintf(os.Stderr, "WARN: maintenance profile %q not found\n", job.Maintenance)
		}
	}

	if job.ArchiveRef != "" && c != nil && c.ArchiveProfiles != nil {
		if prof, ok := c.ArchiveProfiles[job.ArchiveRef]; ok {
			a := prof
			job.Archive = &a
		} else {
			fmt.Fprintf(os.Stderr, "WARN: archive profile %q not found\n", job.ArchiveRef)
		}
	}

	if job.Retention == nil && c != nil && c.Defaults != nil && c.Defaults.Retention != nil && !job.unsetKey("retention") {
		r := *c.Defaults.Retention
		job.Retention = &r
	}
	if job.Archive == nil && c != nil && c.Defaults != nil && c.Defaults.Archive != nil && !job.unsetKey("archive") {
		a := *c.Defaults.Archive
		job.Archive = &a
	}
	if job.Archive != nil {
		job.Archive.resolveStorage(c)
	}
	if job.Hooks == nil && c != nil && c.Defaults != nil && c.Defaults.Hooks != nil && !job.unsetKey("hooks") {
		h := *c.Defaults.Hooks
		job.Hooks = &h
	}
	if job.Progress == nil && c != nil && c.Defaults != nil && c.Defaults.Progress != nil && !job.unsetKey("progress") {
		p := *c.Defaults.Progress
		job.Progress = &p
	}
}

func validateRetentionWithin(s string) error {
	if err := license.AllowRetention(); err == nil {
		return nil
	}
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return nil
	}
	if strings.HasSuffix(trimmed, "m") {
		num := strings.TrimSuffix(trimmed, "m")
		if _, err := strconv.Atoi(strings.TrimSpace(num)); err != nil || strings.TrimSpace(num) == "" {
			return fmt.Errorf("invalid keep_within %q", s)
		}
		if n, _ := strconv.Atoi(strings.TrimSpace(num)); n <= 0 {
			return fmt.Errorf("invalid keep_within %q", s)
		}
		return nil
	}
	return fmt.Errorf("keep_within %q requires Supporter license (community supports minutes only, eg \"1440m\")", s)
}
