// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package database

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"strings"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database/common"
	"github.com/nfrastack/db-backup/internal/database/registry"
)

type BackupInfo = common.BackupInfo
type MaintenanceCfg = common.MaintenanceCfg
type OpResult = common.OpResult
type Engine = registry.Engine
type Options = registry.Options

func BuildTLSConfig(cfg *config.TLSConfig) (*tls.Config, error) {
	return common.BuildTLSConfig(cfg)
}

func CanonicalType(dbType string) string {
	if spec := registry.LookupEngine(dbType); spec != nil {
		return spec.Name
	}
	return ""
}

func CheckIncrementalSupport(ctx context.Context, dbType, host string, port int, user, pass, dbName, authSource string, tlsCfg *config.TLSConfig) error {
	spec := registry.LookupIncremental(dbType)
	if spec == nil || spec.CheckSupport == nil {
		return fmt.Errorf("incremental not implemented for %s", dbType)
	}
	return spec.CheckSupport(ctx, host, port, user, pass, dbName, authSource, tlsCfg)
}

func DefaultPort(dbType string) int {
	if spec := registry.LookupEngine(dbType); spec != nil {
		return spec.DefaultPort
	}
	return 0
}

func EngineSpecs() []*registry.EngineSpec {
	return registry.EngineSpecs()
}

func FormatExtension(dbType string) string {
	return common.FormatExtension(dbType)
}

func GetPosition(ctx context.Context, dbType, host string, port int, user, pass, dbName, authSource string, tlsCfg *config.TLSConfig) (string, error) {
	spec := registry.LookupIncremental(dbType)
	if spec == nil || spec.GetPosition == nil {
		return "", nil
	}
	return spec.GetPosition(ctx, host, port, user, pass, dbName, authSource, tlsCfg)
}

func IncrementalDump(ctx context.Context, w io.Writer, dbType, host string, port int, user, pass, dbName, strategy, since, authSource string, tlsCfg *config.TLSConfig) error {
	spec := registry.LookupIncremental(dbType)
	if spec == nil || spec.Dump == nil {
		return fmt.Errorf("incremental not implemented for %s", dbType)
	}
	return spec.Dump(ctx, w, host, port, user, pass, dbName, strategy, since, authSource, tlsCfg)
}

func IsBackupFile(path string) bool {
	return common.IsBackupFile(path)
}

func IsSupportedType(dbType string) bool {
	return registry.LookupEngine(dbType) != nil
}

func ListDatabases(dbType, host string, port int, user, pass, authSource string, tlsCfg *config.TLSConfig) ([]string, error) {
	spec := registry.LookupEngine(dbType)
	if spec == nil {
		return nil, fmt.Errorf("unsupported type: %s", dbType)
	}
	if spec.ListDatabases == nil {
		return nil, fmt.Errorf("list databases not supported for %s", dbType)
	}
	return spec.ListDatabases(host, port, user, pass, authSource, tlsCfg)
}

func ListOps() {
	common.ListOps()
}

func LookupIncremental(dbType string) *registry.IncrementalSpec {
	return registry.LookupIncremental(dbType)
}

func Maintain(dbType, host string, port int, user, pass, dbName, authSource string, cfg *common.MaintenanceCfg, tlsCfg *config.TLSConfig) ([]common.OpResult, error) {
	if cfg == nil {
		cfg = &common.MaintenanceCfg{}
	}
	if spec := registry.LookupMaintain(dbType); spec != nil {
		return spec.Run(host, port, user, pass, dbName, authSource, cfg, tlsCfg)
	}
	eng := registry.LookupEngine(dbType)
	if eng == nil || eng.Maintain == nil {
		return nil, fmt.Errorf("maintenance not supported for %s", dbType)
	}
	return eng.Maintain(host, port, user, pass, dbName, authSource, cfg, tlsCfg)
}

func New(o Options) (Engine, error) {
	spec := registry.LookupEngine(o.Type)
	if spec == nil || spec.New == nil {
		return nil, fmt.Errorf("unsupported database type: %s", o.Type)
	}
	return spec.New(o)
}

func ParseBackupFilename(path string) (*common.BackupInfo, error) {
	return common.ParseBackupFilename(path)
}

func PgEnsureLogicalSlots(ctx context.Context, host string, port int, user, pass, dbName string) error {
	spec := registry.LookupIncremental("postgres")
	if spec == nil || spec.EnsureSlots == nil {
		return fmt.Errorf("logical slot setup is a Supporter feature")
	}
	return spec.EnsureSlots(ctx, host, port, user, pass, dbName)
}

func RestoreTo(r io.Reader, dbType, host string, port int, user, pass, dbName, authSource string, tlsCfg *config.TLSConfig) error {
	spec := registry.LookupEngine(dbType)
	if spec == nil || spec.Restore == nil {
		return fmt.Errorf("unsupported database type: %s", dbType)
	}
	return spec.Restore(r, host, port, user, pass, dbName, authSource, tlsCfg)
}

func SupportedTypes() []string {
	return registry.Engines()
}

func TypeLabel(dbType string) string {
	return registry.Label(dbType)
}

func TypeList() string {
	return strings.Join(SupportedTypes(), "|")
}
func UniqueBackupName(name string, taken func(string) bool) string {
	return common.UniqueBackupName(name, taken)
}
