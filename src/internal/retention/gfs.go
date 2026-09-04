// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package retention

import (
	"fmt"
	"sync/atomic"

	"github.com/nfrastack/db-backup/internal/license"
)

type TierHookFunc func(backups []BackupEntry, policy RetentionPolicy, kept map[string]bool)

var tierHook atomic.Pointer[TierHookFunc]

func CheckPrune(policy RetentionPolicy) error {
	if policy.Hourly > 0 || policy.Daily > 0 || policy.Weekly > 0 || policy.Monthly > 0 || policy.Yearly > 0 {
		if err := license.AllowRetention(); err != nil {
			return fmt.Errorf("GFS retention tiers (hourly/daily/weekly/monthly/yearly) are not available in the Community edition (%s)", err)
		}
	}
	return nil
}

func (p *RetentionPolicy) HasGFS() bool {
	return p.Hourly > 0 || p.Daily > 0 || p.Weekly > 0 || p.Monthly > 0 || p.Yearly > 0
}

func SetTierHook(f TierHookFunc) {
	if f == nil {
		return
	}
	tierHook.Store(&f)
}
func (p *RetentionPolicy) StripTiers() {
	p.Hourly = 0
	p.Daily = 0
	p.Weekly = 0
	p.Monthly = 0
	p.Yearly = 0
}

func TierHook(backups []BackupEntry, policy RetentionPolicy, kept map[string]bool) {
	if f := tierHook.Load(); f != nil {
		(*f)(backups, policy, kept)
	}
}

func init() {
	noOp := TierHookFunc(noopTierHook)
	tierHook.Store(&noOp)
}
func noopTierHook(_ []BackupEntry, _ RetentionPolicy, _ map[string]bool) {}
