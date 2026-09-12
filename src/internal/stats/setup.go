// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package stats

import (
	"path/filepath"

	"github.com/nfrastack/db-backup/internal/log"
	"github.com/nfrastack/db-backup/internal/scheduler/runner"
)

// buildOptions metadata for shared payload
func BuildOptions(version, imageVersion, licenseID, channel, gitCommit string, runtime RuntimeInfo, logOpts LogOptions) Options {
	return Options{
		Version:      version,
		ImageVersion: imageVersion,
		LicenseID:    licenseID,
		Channel:      channel,
		GitCommit:    gitCommit,
		Runtime:      runtime,
		Log:          logOpts,
	}
}

// wire reporter and journal
// journal disabled when stats are not enabled or no state_dir - reuse when moving to db 5.2.0
func Setup(configPaths []string, sharedKey string, container bool, stateDir string) (*Manager, *Tracker) {
	mgr, err := NewManager(configPaths, sharedKey)
	if err != nil {
		log.Debug("usage-stats", "manager unavailable", "error", err.Error())
		SetJournalDisabled(true)
		return nil, nil
	}
	mgr.SetContainer(container)

	SetJournalDir(filepath.Join(stateDir, "stats", "journal"))
	if mgr.Enabled() {
		tracker := NewTracker()
		runner.SetOutcomeSink(func(o runner.Outcome) {
			tracker.Mark(o.Engine, o.OK, o.Duration, o.Bytes, o.RawBytes)
			op := OpBackup
			if o.Maintenance {
				op = OpMaintenance
			}
			trig := o.Trigger
			if trig == "" {
				trig = TriggerScheduled
			}
			Record(Outcome{
				Op: op, Trigger: trig, Engine: o.Engine, OK: o.OK,
				Ms: o.Duration.Milliseconds(), Jobs: 1,
				Bytes: o.Bytes, RawBytes: o.RawBytes, Checksum: o.Checksum,
			})
		})
		return mgr, tracker
	}
	SetJournalDisabled(true)
	return mgr, nil
}
