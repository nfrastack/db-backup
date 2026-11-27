// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/nfrastack/db-backup/internal/checksum"
	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/retention"
	"github.com/nfrastack/db-backup/internal/scheduler/runner"
	"github.com/nfrastack/db-backup/internal/storage"
)

func cmdVerify(args []string) int {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	storagePath := fs.String("storage-path", config.StoragePath(), "Storage path/prefix (filesystem)")
	storageProfile := fs.String("storage-profile", "", "Storage profile (resolved from -c <config>)")
	filePath := fs.String("file", "", "Backup file to verify")
	encryptionType := fs.String("encryption", "auto", "Encryption type (auto|age|gpg|openssl) - auto detects from file or fallsback to sidecar")
	agePass := fs.String("age-passphrase", "", "Age passphrase for decryption")
	gpgPass := fs.String("gpg-passphrase", "", "OpenPGP/GPG passphrase for decryption")
	identity := fs.String("identity", "", "Age/OpenPGP identity file path or key text for decryption")
	opensslPass := fs.String("openssl-passphrase", "", "OpenSSL passphrase for decryption")
	fs.Parse(args)

	if *filePath == "" {
		fmt.Fprintf(os.Stderr, "ERROR: --file is required\n")
		return 1
	}

	storageCfg, err := config.ResolveStorageArg(globalConfigPaths, *storageProfile, *storagePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	st, err := storage.New(storage.Backend(storageCfg.Backend), runner.StorageOpts(storageCfg))
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: storage: %v\n", err)
		return 1
	}

	n, csType, err := verifyBackup(st, *filePath, retention.DecryptOpts{
		EncryptionType: *encryptionType,
		AgePass:        *agePass,
		GPGPass:        *gpgPass,
		OpenSSLPAss:    *opensslPass,
		Identity:       *identity,
	}, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL %s: %v\n", *filePath, err)
		return 1
	}
	fmt.Fprintf(os.Stdout, "OK  %s (%d bytes, %s)\n", *filePath, n, string(csType))
	return 0
}

func verifyRestoreOrder(st storage.Storage, order []string, opts retention.DecryptOpts, compressType string) error {
	for i, filePath := range order {
		hint := ""
		if i == len(order)-1 {
			hint = compressType
		}
		n, csType, err := verifyBackup(st, filePath, opts, hint)
		if err != nil {
			return fmt.Errorf("verify %s: %w", filePath, err)
		}
		fmt.Fprintf(os.Stderr, "Verified %s (%d bytes, %s)\n", filePath, n, csType)
	}
	return nil
}

func verifyBackup(st storage.Storage, filePath string, opts retention.DecryptOpts, compressType string) (int64, checksum.Type, error) {
	sc, err := retention.ReadSidecar(st, filePath)
	if err != nil {
		return 0, checksum.None, fmt.Errorf("read sidecar: %w", err)
	}
	if len(sc.Checksums) == 0 {
		return 0, checksum.None, fmt.Errorf("no checksums in sidecar for %s", filePath)
	}

	var csType checksum.Type
	var expected string
	uncompressed := false
	for ct, hash := range sc.Checksums {
		if after, ok := strings.CutPrefix(ct, "uncompressed_"); ok {
			csType = checksum.Parse(after)
			expected = hash
			uncompressed = true
			break
		}
	}
	if expected == "" {
		for ct, hash := range sc.Checksums {
			csType = checksum.Parse(strings.TrimPrefix(ct, "compressed_"))
			expected = hash
			break
		}
	}
	if csType == checksum.None {
		return 0, checksum.None, fmt.Errorf("unsupported checksum in sidecar for %s", filePath)
	}

	rc, n, err := st.Download(context.Background(), filePath)
	if err != nil {
		return 0, checksum.None, fmt.Errorf("download: %w", err)
	}
	defer rc.Close()

	hasher, err := checksum.New(csType, io.Discard)
	if err != nil {
		return 0, checksum.None, fmt.Errorf("checksum: %w", err)
	}
	r := io.Reader(rc)
	if uncompressed {
		decoded, err := retention.OpenBackup(r, sc.Encryption, opts, filePath, compressType)
		if err != nil {
			return 0, checksum.None, fmt.Errorf("decrypt: %w", err)
		}
		r = decoded
	}
	if _, err := io.Copy(hasher, r); err != nil {
		return 0, checksum.None, fmt.Errorf("read: %w", err)
	}
	got := hasher.Sum()
	if got != expected {
		return 0, checksum.None, fmt.Errorf("checksum mismatch: expected %s, got %s", expected, got)
	}
	return n, csType, nil
}
