// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package influx

import (
	"fmt"
	"io"
	"os"

	"github.com/nfrastack/db-backup/internal/log"
)

func RestoreChain(paths []string, names []string, restore func(name string, r io.Reader) error) (int64, error) {
	if len(paths) == 0 {
		return 0, fmt.Errorf("influx: nothing to restore")
	}
	if len(names) != len(paths) {
		return 0, fmt.Errorf("influx: paths/names length mismatch")
	}
	kinds := make([]bool, len(paths))
	nTar := 0
	var total int64
	for i, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			return 0, fmt.Errorf("influx: open chain file: %w", err)
		}
		head := make([]byte, 512)
		hn, _ := io.ReadFull(f, head)
		f.Close()
		kinds[i] = IsTarStream(head[:hn])
		if kinds[i] {
			nTar++
		}
		fi, err := os.Stat(p)
		if err != nil {
			return 0, fmt.Errorf("influx: stat chain file: %w", err)
		}
		total += fi.Size()
	}
	if nTar > 0 && nTar < len(paths) {
		return 0, fmt.Errorf("influx: mixed physical/logical chain - restore files separately")
	}
	if nTar == len(paths) {
		tmpDir, err := os.MkdirTemp("", "influx-merge-")
		if err != nil {
			return 0, fmt.Errorf("influx: temp dir: %w", err)
		}
		defer os.RemoveAll(tmpDir)
		merged, err := os.CreateTemp(tmpDir, "merged-*.tar")
		if err != nil {
			return 0, fmt.Errorf("influx: temp file: %w", err)
		}
		mergedName := merged.Name()
		if err := MergePhysicalTarFiles(paths, merged); err != nil {
			merged.Close()
			return 0, err
		}
		merged.Close()
		mf, err := os.Open(mergedName)
		if err != nil {
			return 0, fmt.Errorf("influx: open merged chain: %w", err)
		}
		defer mf.Close()
		log.Debug("influx", "restoring merged physical chain", "files", len(paths))
		if err := restore("merged chain", mf); err != nil {
			return 0, err
		}
		if fi, err := mf.Stat(); err == nil {
			total = fi.Size()
		}
		return total, nil
	}
	for i, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			return 0, fmt.Errorf("influx: open chain file: %w", err)
		}
		err = restore(names[i], f)
		f.Close()
		if err != nil {
			return 0, err
		}
	}
	return total, nil
}
