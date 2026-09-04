// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package registry

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database/common"
)

type Engine interface {
	Open() error
	Close() error
	Dump(w io.Writer, names []string) error
	SetConnectivity(cfg *config.ConnectivityConfig)
}

type Options struct {
	Type       string
	Host       string
	Port       int
	User       string
	Pass       string
	DB         string
	Version    int
	TLS        *config.TLSConfig
	AuthSource string
	Objects    config.MysqlObjects
	HasObjects bool
}

type EngineSpec struct {
	Name        string
	Aliases     []string
	Label       string
	DefaultPort int

	New             func(o Options) (Engine, error)
	ListDatabases   func(host string, port int, user, pass, authSource string, tlsCfg *config.TLSConfig) ([]string, error)
	Maintain        func(host string, port int, user, pass, dbName, authSource string, cfg *common.MaintenanceCfg, tlsCfg *config.TLSConfig) ([]common.OpResult, error)
	Restore         func(r io.Reader, host string, port int, user, pass, dbName, authSource string, tlsCfg *config.TLSConfig) error
	IncrementalDump func(ctx context.Context, w io.Writer, host string, port int, user, pass, dbName, strategy, since, authSource string, tlsCfg *config.TLSConfig) error
}

type IncrementalSpec struct {
	Engine       string
	CheckSupport func(ctx context.Context, host string, port int, user, pass, dbName, authSource string, tlsCfg *config.TLSConfig) error
	GetPosition  func(ctx context.Context, host string, port int, user, pass, dbName, authSource string, tlsCfg *config.TLSConfig) (string, error)
	Dump         func(ctx context.Context, w io.Writer, host string, port int, user, pass, dbName, strategy, since, authSource string, tlsCfg *config.TLSConfig) error
	EnsureSlots  func(ctx context.Context, host string, port int, user, pass, dbName string) error
}

type MaintainSpec struct {
	Engine string
	Run    func(host string, port int, user, pass, dbName, authSource string, cfg *common.MaintenanceCfg, tlsCfg *config.TLSConfig) ([]common.OpResult, error)
}

var (
	registryMu sync.RWMutex
	engines    = map[string]*EngineSpec{}
	aliases    = map[string]string{}
	increments = map[string]*IncrementalSpec{}
	maintains  = map[string]*MaintainSpec{}
)

func Aliases() map[string]string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make(map[string]string, len(aliases))
	for a, c := range aliases {
		out[a] = c
	}
	return out
}
func CanonicalName(name string) string {
	if spec := LookupEngine(name); spec != nil {
		return spec.Name
	}
	return ""
}

func EngineSpecs() []*EngineSpec {
	registryMu.RLock()
	defer registryMu.RUnlock()
	specs := make([]*EngineSpec, 0, len(engines))
	for _, spec := range engines {
		specs = append(specs, spec)
	}
	return specs
}
func Engines() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	names := make([]string, 0, len(engines))
	for name := range engines {
		names = append(names, name)
	}

	out := make([]string, len(names))
	copy(out, names)
	sort.Strings(out)
	return out
}
func Label(name string) string {
	if spec := LookupEngine(name); spec != nil {
		return spec.Label
	}
	return ""
}
func LookupEngine(name string) *EngineSpec {
	if name == "" {
		return nil
	}
	name = strings.ToLower(name)
	registryMu.RLock()
	defer registryMu.RUnlock()
	if c, ok := aliases[name]; ok {
		name = c
	}
	return engines[name]
}

func LookupIncremental(name string) *IncrementalSpec {
	if name == "" {
		return nil
	}
	name = strings.ToLower(name)
	registryMu.RLock()
	defer registryMu.RUnlock()
	if c, ok := aliases[name]; ok {
		name = c
	}
	return increments[name]
}

func LookupMaintain(name string) *MaintainSpec {
	if name == "" {
		return nil
	}
	name = strings.ToLower(name)
	registryMu.RLock()
	defer registryMu.RUnlock()
	if c, ok := aliases[name]; ok {
		name = c
	}
	return maintains[name]
}
func RegisterEngine(spec EngineSpec) {
	name := strings.ToLower(spec.Name)
	if name == "" {
		panic("registry: engine registered with empty name")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := engines[name]; exists {
		panic(fmt.Sprintf("registry: duplicate engine %q", name))
	}
	engines[name] = &spec
	for _, a := range spec.Aliases {
		a = strings.ToLower(a)
		if a == "" || a == name {
			continue
		}
		if _, exists := aliases[a]; exists {
			panic(fmt.Sprintf("registry: duplicate engine alias %q", a))
		}
		aliases[a] = name
	}
}

func RegisterIncremental(spec IncrementalSpec) {
	name := strings.ToLower(spec.Engine)
	if name == "" {
		panic("registry: incremental registered with empty engine name")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := increments[name]; exists {
		panic(fmt.Sprintf("registry: duplicate incremental registration for %q", name))
	}
	increments[name] = &spec
}

func RegisterMaintain(spec MaintainSpec) {
	name := strings.ToLower(spec.Engine)
	if name == "" {
		panic("registry: maintain registered with empty engine name")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := maintains[name]; exists {
		panic(fmt.Sprintf("registry: duplicate maintain registration for %q", name))
	}
	maintains[name] = &spec
}
