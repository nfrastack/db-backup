// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: LicenseRef-NSLv1

// This file is part of the Supported (Supporter) edition of db-backup and is
// licensed under the Nfrastack Supporter License v1 (NSLv1).
// It is excluded from the Community build. !community.

//go:build !community

package license

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/license"
	"github.com/nfrastack/db-backup/internal/log"
)

func licenseInstallDir(system bool, configPaths []string) string {
	if system {
		return "/etc"
	}
	if cfg, err := config.LoadLicenseConfig(configPaths...); err == nil && cfg != nil && cfg.InstallDir != "" {
		return cfg.InstallDir
	}
	return config.ResolveStateDir(license.StateDir())
}

func SetupLicenseWatch(configPaths []string) {
	const warnDefault = 14
	warnDays := warnDefault
	if cfg, err := config.LoadLicenseConfig(configPaths...); err == nil && cfg != nil && cfg.WarnDays > 0 {
		warnDays = cfg.WarnDays
	}

	state := ""
	evaluate := func() {
		license.Reload()
		lic, err := license.Installed()
		switch {
		case err != nil || lic == nil:
			state = "none"
			return
		case license.IsRevoked != nil && lic.LicenseID != "" && license.IsRevoked(lic.LicenseID):
			if state != "revoked" {
				log.Error("license", "license revoked - running in Community mode", "license_id", lic.LicenseID)
			}
			state = "revoked"
			return
		case license.IsExpired(lic):
			if state != "expired" {
				log.Error("license", "license expired - running in Community mode", "expired_at", lic.ExpiresAt)
			}
			state = "expired"
			return
		}
		state = "valid"
		if d := license.DaysRemaining(); d >= 0 && d <= warnDays {
			log.Warn("license", "license is expiring soon",
				"days_remaining", d, "expires_at", lic.ExpiresAt,
				"msg_hint", "instructions for renewal will be send to the billing address on file")
		}
	}

	evaluate()
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			evaluate()
		}
	}()
}

func RunLicense(args []string, configPaths []string, key string) int {
	fs := flag.NewFlagSet("license", flag.ExitOnError)
	install := fs.String("install", "", "Install a license file")
	check := fs.String("check", "", "Check license validity")
	system := fs.Bool("system", false, "Install system-wide to /etc (requires root)")
	showCustomer := fs.Bool("show-customer", false, "Include customer identity details")
	fs.Parse(args)
	mask := func(s string) string {
		if *showCustomer || s == "" {
			return s
		}
		return "*** (use --show-customer)"
	}

	if *install != "" {
		lic, err := license.Parse(readLicenseFile(*install))
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			return 1
		}
		dst := filepath.Join(licenseInstallDir(*system, configPaths), "db-backup.lic")
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			fmt.Fprintf(os.Stderr, "WARN: cannot create license install directory %s: %v\n", filepath.Dir(dst), err)
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			return 1
		}
		raw, _ := os.ReadFile(*install)
		if err := os.WriteFile(dst, raw, 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "WARN: cannot save license to %s: %v\n", dst, err)
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			return 1
		}
		fmt.Printf("Installed license for %s (%s) at %s\n", lic.Customer.Name, lic.Tier, dst)
		if !license.IsDiscoveryPath(dst) {
			fmt.Fprintf(os.Stderr, "WARN: %s is not a license discovery path - the binary will not load it automatically.\n", dst)
			fmt.Fprintf(os.Stderr, "WARN: set license.file in db-backup.yml or DBBACKUP_LICENSE to point at it.\n")
		}
		return 0
	}

	if *check != "" {
		res := license.Enabled(*check)
		if res.Allowed {
			return 0
		}
		fmt.Fprintf(os.Stderr, "%s\n", res.Reason)
		return 1
	}

	lic, err := license.Installed()
	switch {
	case err != nil:
		fmt.Fprintf(os.Stderr, "License: invalid\n  %v\n", err)
		return 1
	case lic == nil:
		fmt.Println("License: Community (AGPL-3.0-or-later)")
		fmt.Println("  No license installed")
		fmt.Println("  A license unlocks advanced functionality and support.")
		fmt.Println("  https://nfrastack.com")
		return 0
	default:
		fmt.Printf("License: %s\n", lic.Tier)
		fmt.Printf("  customer: %s <%s>\n", mask(lic.Customer.Name), mask(lic.Customer.Email))
		if lic.Customer.OrgID != "" {
			fmt.Printf("  org     : %s\n", mask(lic.Customer.OrgID))
		}
		fmt.Printf("  license : %s\n", lic.LicenseID)
		fmt.Printf("  expires : %s\n", lic.ExpiresAt)
		if d := license.DaysRemaining(); d >= 0 {
			if d == 0 && license.IsExpired(lic) {
				fmt.Println("  status  : EXPIRED - running in Community mode")
			} else {
				fmt.Printf("  remaining: %d day(s)\n", d)
			}
		}
		fmt.Printf("  features: %s\n", strings.Join(lic.Features, ", "))

		PrintRevocationStatus(lic, configPaths, key)
		return 0
	}
}

func readLicenseFile(arg string) string {
	if b, err := os.ReadFile(arg); err == nil {
		return string(b)
	}
	return arg
}
