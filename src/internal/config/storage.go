// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

type StorageConfig struct {
	Backend   string     `yaml:"backend"`
	Bucket    string     `yaml:"bucket"`
	Path      string     `yaml:"path"`
	Endpoint  string     `yaml:"endpoint"`
	Region    string     `yaml:"region"`
	KeyID     string     `yaml:"key_id"`
	KeySecret string     `yaml:"key_secret"`
	Account   string     `yaml:"account,omitempty"`
	Key       string     `yaml:"key,omitempty"`
	URL       string     `yaml:"url"`
	Pass      string     `yaml:"pass,omitempty"`
	FileMode  string     `yaml:"file_mode"`
	DirMode   string     `yaml:"dir_mode"`
	User      string     `yaml:"user"`
	Group     string     `yaml:"group"`
	TLS       *TLSConfig `yaml:"tls,omitempty"`
	unsetKeys map[string]bool
}

type TLSConfig struct {
	Enable   bool   `yaml:"enable"`
	CAFile   string `yaml:"ca_file"`
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
	Verify   *bool  `yaml:"verify"`
	Version  string `yaml:"version"`
}

func (c *StorageConfig) Options() map[string]string {
	if c == nil {
		return map[string]string{}
	}
	o := map[string]string{
		"path":       c.Path,
		"file_mode":  c.FileMode,
		"dir_mode":   c.DirMode,
		"user":       c.User,
		"group":      c.Group,
		"bucket":     c.Bucket,
		"endpoint":   c.Endpoint,
		"region":     c.Region,
		"key_id":     c.KeyID,
		"key_secret": c.KeySecret,
		"account":    c.Account,
		"key":        c.Key,
		"container":  c.Bucket,
		"url":        c.URL,
		"pass":       c.Pass,
	}
	if c.TLS != nil && c.TLS.Enable {
		o["tls_ca_file"] = c.TLS.CAFile
		o["tls_cert_file"] = c.TLS.CertFile
		o["tls_key_file"] = c.TLS.KeyFile
		o["tls_verify"] = fmt.Sprintf("%t", c.TLS.VerifyCerts())
		o["tls_version"] = c.TLS.Version
	}
	return o
}
func (s *StorageConfig) UnmarshalYAML(value *yaml.Node) error {
	type raw StorageConfig
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
	*s = StorageConfig(r)
	s.unsetKeys = unsets
	return nil
}

func (t *TLSConfig) VerifyCerts() bool {
	if t == nil || t.Verify == nil {
		return true
	}
	return *t.Verify
}

func mergeStorage(job, prof *StorageConfig) {
	if !job.unsetKey("backend") && job.Backend == "" {
		job.Backend = prof.Backend
	}
	if !job.unsetKey("bucket") && job.Bucket == "" {
		job.Bucket = prof.Bucket
	}
	if !job.unsetKey("path") && job.Path == "" {
		job.Path = prof.Path
	}
	if !job.unsetKey("endpoint") && job.Endpoint == "" {
		job.Endpoint = prof.Endpoint
	}
	if !job.unsetKey("region") && job.Region == "" {
		job.Region = prof.Region
	}
	if !job.unsetKey("key_id") && job.KeyID == "" {
		job.KeyID = prof.KeyID
	}
	if !job.unsetKey("key_secret") && job.KeySecret == "" {
		job.KeySecret = prof.KeySecret
	}
	if !job.unsetKey("file_mode") && job.FileMode == "" {
		job.FileMode = prof.FileMode
	}
	if !job.unsetKey("dir_mode") && job.DirMode == "" {
		job.DirMode = prof.DirMode
	}
	if !job.unsetKey("user") && job.User == "" {
		job.User = prof.User
	}
	if !job.unsetKey("group") && job.Group == "" {
		job.Group = prof.Group
	}
}
func (s *StorageConfig) resolveSecrets() {
	s.Path = ResolveSecret(s.Path)
	s.Pass = ResolveSecret(s.Pass)
	s.Bucket = ResolveSecret(s.Bucket)
	s.Endpoint = ResolveSecret(s.Endpoint)
	s.Region = ResolveSecret(s.Region)
	s.KeyID = ResolveSecret(s.KeyID)
	s.KeySecret = ResolveSecret(s.KeySecret)
	s.Account = ResolveSecret(s.Account)
	s.Key = ResolveSecret(s.Key)
	s.URL = ResolveSecret(s.URL)
	if s.TLS != nil {
		s.TLS.CAFile = ResolveSecret(s.TLS.CAFile)
		s.TLS.CertFile = ResolveSecret(s.TLS.CertFile)
		s.TLS.KeyFile = ResolveSecret(s.TLS.KeyFile)
	}
}
func (s *StorageConfig) unsetKey(key string) bool {
	return s != nil && s.unsetKeys != nil && s.unsetKeys[key]
}
