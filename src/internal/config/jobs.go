// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type ArchiveConfig struct {
	Last       int              `yaml:"last"`
	Within     string           `yaml:"within"`
	Path       string           `yaml:"path"`
	Retention  *RetentionConfig `yaml:"retention,omitempty"`
	Storage    *StorageConfig   `yaml:"storage"`
	StorageRef string           `yaml:"-"`
}

type BackupConfig struct {
	Strategy     string `yaml:"strategy"`
	Type         string `yaml:"type"`
	Filename     string `yaml:"filename"`
	CreateLatest *bool  `yaml:"create_latest,omitempty"`
	SchemaOnly   bool   `yaml:"schema_only"`
	FullEvery    int    `yaml:"full_every"`
	FullAfter    string `yaml:"full_after"`
	unsetKeys    map[string]bool
}

type BackupProfile struct {
	Strategy    string             `yaml:"strategy"`
	Filename    string             `yaml:"filename,omitempty"`
	Compression *CompressionConfig `yaml:"compression,omitempty"`
	Checksum    string             `yaml:"checksum,omitempty"`
	Retention   *RetentionConfig   `yaml:"retention,omitempty"`
}

type CompressionConfig struct {
	Type      string `yaml:"type"`
	Level     int    `yaml:"level"`
	Threads   int    `yaml:"threads,omitempty"`
	Rsyncable bool   `yaml:"rsyncable,omitempty"`
}

type DatabaseList struct {
	Include  []string     `yaml:"include"`
	Exclude  []string     `yaml:"exclude"`
	Tables   *TableFilter `yaml:"tables,omitempty"`
	Routines *bool        `yaml:"routines,omitempty"`
	Events   *bool        `yaml:"events,omitempty"`
	Triggers *bool        `yaml:"triggers,omitempty"`
	Views    *bool        `yaml:"views,omitempty"`
}

type DbProfile struct {
	Connection string       `yaml:"connection"`
	Name       string       `yaml:"name,omitempty"`
	Include    []string     `yaml:"include"`
	Exclude    []string     `yaml:"exclude"`
	Tables     *TableFilter `yaml:"tables,omitempty"`
	Routines   *bool        `yaml:"routines,omitempty"`
	Events     *bool        `yaml:"events,omitempty"`
	Triggers   *bool        `yaml:"triggers,omitempty"`
	Views      *bool        `yaml:"views,omitempty"`
}

type EncryptionConfig struct {
	Type       string   `yaml:"type"`
	Recipients []string `yaml:"recipients,omitempty"`
	Passphrase string   `yaml:"passphrase,omitempty"`
}

type JobConfig struct {
	Name                 string              `yaml:"name"`
	Type                 string              `yaml:"type"`
	Host                 string              `yaml:"host"`
	Port                 int                 `yaml:"port"`
	User                 string              `yaml:"user"`
	Pass                 string              `yaml:"pass"`
	Databases            *DatabaseList       `yaml:"databases"`
	Schedule             *Schedule           `yaml:"schedule"`
	Blackout             *Blackouts          `yaml:"blackout,omitempty"`
	Compression          *CompressionConfig  `yaml:"compression"`
	Checksum             string              `yaml:"checksum"`
	Storage              *StorageConfig      `yaml:"storage"`
	TLS                  *TLSConfig          `yaml:"tls"`
	Backup               *BackupConfig       `yaml:"backup"`
	Maintenance          string              `yaml:"maintenance"`
	Encryption           string              `yaml:"encryption"`
	Version              int                 `yaml:"version,omitempty"`
	Connectivity         *ConnectivityConfig `yaml:"connectivity"`
	AuthSource           string              `yaml:"-"`
	Retention            *RetentionConfig    `yaml:"retention"`
	Archive              *ArchiveConfig      `yaml:"archive"`
	Hooks                *HooksConfig        `yaml:"hooks"`
	LogLevel             string              `yaml:"log_level"`
	LogPath              string              `yaml:"log_path,omitempty"`
	Timings              *bool               `yaml:"timings,omitempty"`
	Colour               *bool               `yaml:"colour,omitempty"`
	Progress             *bool               `yaml:"progress,omitempty"`
	SizeFormat           string              `yaml:"size_format,omitempty"`
	SplitDB              bool                `yaml:"split_db"`
	ConnectionRef        string              `yaml:"connection,omitempty"`
	DatabaseRef          string              `yaml:"database,omitempty"`
	StorageRef           string              `yaml:"storage_profile,omitempty"`
	BackupRef            string              `yaml:"backup_profile,omitempty"`
	ArchiveRef           string              `yaml:"-"`
	EncryptionType       string              `yaml:"-"`
	EncryptionIdentity   string              `yaml:"-"`
	EncryptionIdentities []string            `yaml:"-"`
	MaintenanceCfg       *MaintenanceConfig  `yaml:"-"`
	RunID                string              `yaml:"-"`
	unsetKeys            map[string]bool
}

type MaintenanceConfig struct {
	Schedule    string `yaml:"schedule"`
	Optimize    *bool  `yaml:"optimize,omitempty"`
	Vacuum      *bool  `yaml:"vacuum,omitempty"`
	Reindex     *bool  `yaml:"reindex,omitempty"`
	Analyze     *bool  `yaml:"analyze,omitempty"`
	CheckTables *bool  `yaml:"check_tables,omitempty"`
	Compact     *bool  `yaml:"compact,omitempty"`
	MemoryPurge *bool  `yaml:"memory_purge,omitempty"`
}

type MysqlObjects struct {
	Routines bool
	Events   bool
	Triggers bool
	Views    bool
}

type RetentionConfig struct {
	Last    *int   `yaml:"last,omitempty"`
	Within  string `yaml:"within,omitempty"`
	Hourly  *int   `yaml:"hourly,omitempty"`
	Daily   *int   `yaml:"daily,omitempty"`
	Weekly  *int   `yaml:"weekly,omitempty"`
	Monthly *int   `yaml:"monthly,omitempty"`
	Yearly  *int   `yaml:"yearly,omitempty"`
}

type SchemaOnly struct {
	All    bool
	Tables []string
}

type TableFilter struct {
	Include    []string   `yaml:"include"`
	Exclude    []string   `yaml:"exclude"`
	SchemaOnly SchemaOnly `yaml:"schema_only"`
}

func (f *TableFilter) Apply(table string) (included, schemaOnly bool) {
	if f == nil {
		return true, false
	}

	if globMatchAny(f.Exclude, table) {
		return false, false
	}

	if len(f.Include) > 0 && !globMatchAny(f.Include, table) {
		return false, false
	}
	return true, f.SchemaOnly.Matches(table)
}

func (m *MaintenanceConfig) DueOn(t time.Time) bool {
	if m == nil || m.Schedule == "" {
		return true
	}
	parts := strings.Fields(m.Schedule)
	if len(parts) != 5 {
		return true
	}
	dom := cronFieldMatch(parts[2], t.Day(), 1, 31)
	month := cronFieldMatch(parts[3], int(t.Month()), 1, 12)
	wd := cronFieldMatch(parts[4], int(t.Weekday()), 0, 6)
	if !month {
		return false
	}
	domRestricted := parts[2] != "*"
	wdRestricted := parts[4] != "*"
	if domRestricted && wdRestricted {
		return dom || wd
	}
	return dom && wd
}

func (b *BackupConfig) EffectiveStrategy() string {
	if b.Strategy != "" {
		return b.Strategy
	}
	if b.Type != "" {
		return b.Type
	}
	return "full"
}

func (d *DatabaseList) Excluded(name string) bool {
	if d == nil {
		return false
	}
	return globMatchAny(d.Exclude, name)
}

func (d *DatabaseList) ResolveMysqlObjects() MysqlObjects {
	o := MysqlObjects{Routines: true, Events: true, Triggers: true, Views: true}
	if d == nil {
		return o
	}
	if d.Routines != nil {
		o.Routines = *d.Routines
	}
	if d.Events != nil {
		o.Events = *d.Events
	}
	if d.Triggers != nil {
		o.Triggers = *d.Triggers
	}
	if d.Views != nil {
		o.Views = *d.Views
	}
	return o
}

func (s *SchemaOnly) Matches(table string) bool {
	if s == nil {
		return false
	}
	if s.All {
		return true
	}
	return globMatchAny(s.Tables, table)
}

func (a *ArchiveConfig) UnmarshalYAML(node *yaml.Node) error {
	type raw ArchiveConfig
	var r raw

	if node.Kind != yaml.MappingNode {
		return node.Decode(&r)
	}

	clone := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: make([]*yaml.Node, len(node.Content))}
	for i, n := range node.Content {
		cp := *n
		clone.Content[i] = &cp
	}
	for i := 0; i+1 < len(clone.Content); i += 2 {
		if clone.Content[i].Value == "storage" && clone.Content[i+1].Kind == yaml.ScalarNode {
			clone.Content[i+1] = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		}
	}
	if err := clone.Decode(&r); err != nil {
		return err
	}
	*a = ArchiveConfig(r)

	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == "storage" && node.Content[i+1].Kind == yaml.ScalarNode {
			a.StorageRef = node.Content[i+1].Value
		}
	}
	return nil
}

func (b *BackupConfig) UnmarshalYAML(value *yaml.Node) error {
	type raw BackupConfig
	var r raw
	if value.Kind != yaml.MappingNode {
		return value.Decode(&r)
	}
	clone := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: make([]*yaml.Node, len(value.Content))}
	for i, n := range value.Content {
		cp := *n
		clone.Content[i] = &cp
	}
	var unsets map[string]bool
	for i := 0; i+1 < len(clone.Content); i += 2 {
		val := clone.Content[i+1]
		if val.Kind == yaml.ScalarNode && val.Value == "unset" {
			if unsets == nil {
				unsets = make(map[string]bool)
			}
			unsets[clone.Content[i].Value] = true
			clone.Content[i+1] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
		}
	}
	if err := clone.Decode(&r); err != nil {
		return err
	}
	*b = BackupConfig(r)
	b.unsetKeys = unsets
	return nil
}

func (j *JobConfig) UnmarshalYAML(value *yaml.Node) error {
	type raw JobConfig
	var r raw

	if value.Kind != yaml.MappingNode {
		return value.Decode(&r)
	}

	clone := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: make([]*yaml.Node, len(value.Content))}
	for i, n := range value.Content {
		cp := *n
		clone.Content[i] = &cp
	}
	var unsets map[string]bool
	var backupRef, storageRef, archiveRef string
	for i := 0; i+1 < len(clone.Content); i += 2 {
		key := clone.Content[i].Value
		val := clone.Content[i+1]
		if val.Kind != yaml.ScalarNode {
			continue
		}
		if val.Value == "unset" {
			if unsets == nil {
				unsets = make(map[string]bool)
			}
			unsets[key] = true
			clone.Content[i+1] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
			continue
		}
		if key == "backup" || key == "storage" || key == "archive" {
			clone.Content[i+1] = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			switch key {
			case "backup":
				backupRef = val.Value
			case "storage":
				storageRef = val.Value
			case "archive":
				archiveRef = val.Value
			}
		}
	}
	if err := clone.Decode(&r); err != nil {
		return err
	}
	*j = JobConfig(r)
	if backupRef != "" {
		j.BackupRef = backupRef
	}
	if storageRef != "" {
		j.StorageRef = storageRef
	}
	if archiveRef != "" {
		j.ArchiveRef = archiveRef
	}
	j.unsetKeys = unsets
	return nil
}

func (s *SchemaOnly) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		var b bool
		if err := node.Decode(&b); err == nil {
			s.All = b
			return nil
		}
		var str string
		if err := node.Decode(&str); err == nil {
			s.Tables = []string{str}
			return nil
		}
		return fmt.Errorf("schema_only must be a boolean or a list of table names")
	case yaml.SequenceNode:
		var list []string
		if err := node.Decode(&list); err != nil {
			return err
		}
		for _, t := range list {
			if t == "*" {
				s.All = true
			} else {
				s.Tables = append(s.Tables, t)
			}
		}
		return nil
	}
	return fmt.Errorf("schema_only must be a boolean or a list of table names")
}

func (j *JobConfig) Validate() error {
	if j.Name == "" {
		return fmt.Errorf("job has no name (every job needs a unique 'name' field)")
	}
	if j.Type == "" {
		return fmt.Errorf("job %q has no database type (set 'type' to mysql/postgres/mongodb/etc, or reference a 'database' profile)", j.Name)
	}
	switch j.Encryption {
	case "", "none", "age", "gpg", "openpgp", "pgp", "openssl":

	default:
	}
	if j.Schedule != nil {
		if err := j.Schedule.enforceCommunityLimits(); err != nil {
			return fmt.Errorf("job %q schedule: %w", j.Name, err)
		}
	}
	if j.Retention != nil && j.Retention.Within != "" {
		if err := validateRetentionWithin(j.Retention.Within); err != nil {
			return fmt.Errorf("job %q retention.within: %w", j.Name, err)
		}
	}
	return nil
}

func resolveSecrets(job *JobConfig) {
	job.Host = ResolveSecret(job.Host)
	job.User = ResolveSecret(job.User)
	job.Pass = ResolveSecret(job.Pass)
	if job.Storage != nil {
		job.Storage.resolveSecrets()
	}
	if job.Archive != nil && job.Archive.Storage != nil {
		job.Archive.Storage.resolveSecrets()
	}
	if job.TLS != nil {
		job.TLS.CAFile = ResolveSecret(job.TLS.CAFile)
		job.TLS.CertFile = ResolveSecret(job.TLS.CertFile)
		job.TLS.KeyFile = ResolveSecret(job.TLS.KeyFile)
	}
	job.EncryptionIdentity = ResolveSecret(job.EncryptionIdentity)
	for i := range job.EncryptionIdentities {
		job.EncryptionIdentities[i] = ResolveSecret(job.EncryptionIdentities[i])
	}
}

func (a *ArchiveConfig) resolveStorage(c *Config) {
	if a == nil || a.StorageRef == "" {
		return
	}
	if c != nil && c.StorageProfiles != nil {
		if prof, ok := c.StorageProfiles[a.StorageRef]; ok {
			sp := prof
			a.Storage = &sp
		} else {
			fmt.Fprintf(os.Stderr, "WARN: archive storage profile %q not found\n", a.StorageRef)
			a.Storage = nil
		}
	} else {
		a.Storage = nil
	}
	a.StorageRef = ""
}

func (b *BackupConfig) unsetKey(key string) bool {
	return b != nil && b.unsetKeys != nil && b.unsetKeys[key]
}

func (j *JobConfig) unsetKey(key string) bool {
	return j != nil && j.unsetKeys != nil && j.unsetKeys[key]
}
