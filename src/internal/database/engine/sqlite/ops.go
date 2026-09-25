// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package sqlite

import (
	"bufio"
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

	err = restoreStream(r, func(stmt string) error {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" || strings.HasPrefix(stmt, "--") {
			return nil
		}
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("exec: %w (%.80s)", err, stmt)
		}
		return nil
	})
	if err != nil {
		return err
	}

	db.Exec("PRAGMA foreign_keys = ON;")
	return nil
}

func restoreStream(r io.Reader, yield func(stmt string) error) error {
	var cur strings.Builder
	flush := func() error {
		if cur.Len() == 0 {
			return nil
		}
		stmt := cur.String()
		cur.Reset()
		return yield(stmt)
	}

	br := bufio.NewReader(r)
	for {
		line, err := br.ReadString('\n')
		if line != "" {
			noline := strings.TrimSuffix(line, "\n")
			if trimmed := strings.TrimSpace(noline); !strings.HasPrefix(trimmed, "--") {
				cur.WriteString(noline)
				cur.WriteByte('\n')
				if strings.HasSuffix(trimmed, ";") {
					if err := flush(); err != nil {
						return err
					}
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("read dump: %w", err)
		}
	}
	if cur.Len() > 0 {
		return yield(cur.String())
	}
	return nil
}
