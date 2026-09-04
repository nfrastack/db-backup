// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package sqlite

import (
	"database/sql"
	"fmt"
	"io"
	"strings"

	"github.com/nfrastack/db-backup/internal/database/common"
)

func Maintain(dbPath string, cfg *common.MaintenanceCfg) ([]common.OpResult, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer db.Close()

	var results []common.OpResult

	if common.Enabled("analyze", cfg) {
		r, start := common.StartOp("PRAGMA optimize")
		if _, err := db.Exec("PRAGMA optimize"); err != nil {
			r.Status = "ERROR"
			r.Detail = err.Error()
		} else {
			r.Detail = "completed"
		}
		results = append(results, common.FinishOp(&r, start))
	}

	return results, nil
}

func Restore(r io.Reader, dbPath string) error {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer db.Close()

	db.Exec("PRAGMA foreign_keys = OFF;")
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read dump: %w", err)
	}

	for _, stmt := range common.SplitSQL(string(data)) {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" || strings.HasPrefix(stmt, "--") {
			continue
		}
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("exec: %w (%.80s)", err, stmt)
		}
	}
	db.Exec("PRAGMA foreign_keys = ON;")
	return nil
}
