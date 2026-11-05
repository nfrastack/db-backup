// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package mssql

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/log"
	"github.com/nfrastack/db-backup/internal/storage"
)

const sqlpackageMode = "sqlpackage"

// dacpacMagic is the zip header every .dacpac starts with.
const dacpacMagic = "PK\x03\x04"

func (d *Dumper) dumpSqlpackage(w io.Writer, dbNames []string) error {
	if len(dbNames) != 1 || strings.EqualFold(dbNames[0], "all") {
		return fmt.Errorf("mssql_mode sqlpackage backs up one database per job, got %q", strings.Join(dbNames, ","))
	}
	dir, err := os.MkdirTemp(storage.SpoolDir(), "dbb-sqlpackage-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	path := dir + "/backup.dacpac"

	args := []string{
		"/Action:Extract",
		"/TargetFile:" + path,
		"/p:ExtractApplicationScopedObjectsOnly=false",
		"/p:ExtractAllTableData=true",
		"/p:VerifyExtraction=true",
	}
	args = append(args, sqlpackageConnArgs("Source", d.host, d.port, d.user, d.pass, dbNames[0], d.tlsCfg)...)
	if err := runSqlpackage(d.ctxOrBg(), args); err != nil {
		return err
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}

func restoreDacpac(r io.Reader, host string, port int, user, pass, dbName string, tlsCfg *config.TLSConfig) error {
	if dbName == "" {
		return fmt.Errorf("restoring a dacpac needs a target database name")
	}
	f, err := os.CreateTemp(storage.SpoolDir(), "dbb-restore-*.dacpac")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = io.Copy(f, r)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write dacpac: %w", err)
	}

	args := append([]string{"/Action:Publish", "/SourceFile:" + f.Name()},
		sqlpackageConnArgs("Target", host, port, user, pass, dbName, tlsCfg)...)
	return runSqlpackage(context.Background(), args)
}

func sqlpackageConnArgs(side, host string, port int, user, pass, dbName string, tlsCfg *config.TLSConfig) []string {
	encrypt := tlsCfg != nil && tlsCfg.Enable
	args := []string{
		"/" + side + "ServerName:" + host + "," + strconv.Itoa(port),
		"/" + side + "DatabaseName:" + dbName,
		"/" + side + "EncryptConnection:" + strconv.FormatBool(encrypt),
	}
	if encrypt && !tlsCfg.VerifyCerts() {
		args = append(args, "/"+side+"TrustServerCertificate:true")
	}
	if user != "" {
		args = append(args, "/"+side+"User:"+user, "/"+side+"Password:"+pass)
	}
	return args
}

func runSqlpackage(ctx context.Context, args []string) error {
	log.Debug("mssql", "sqlpackage start", "action", args[0])
	out, err := exec.CommandContext(ctx, "sqlpackage", args...).CombinedOutput()
	log.Debug("mssql", "sqlpackage output", "action", args[0], "output", string(out))
	if err != nil {
		return fmt.Errorf("sqlpackage %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return nil
}
