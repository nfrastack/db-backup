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

	sc, err := retention.ReadSidecar(st, *filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: read sidecar: %v\n", err)
		return 1
	}

	if len(sc.Checksums) == 0 {
		fmt.Fprintf(os.Stderr, "ERROR: no checksums in sidecar for %s\n", *filePath)
		return 1
	}

	rc, n, err := st.Download(context.Background(), *filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: download: %v\n", err)
		return 1
	}
	defer rc.Close()

	var csType checksum.Type
	var expected string
	uncompressed := false
	for ct, hash := range sc.Checksums {
		if strings.HasPrefix(ct, "uncompressed_") {
			csType = checksum.Parse(strings.TrimPrefix(ct, "uncompressed_"))
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

	hasher, err := checksum.New(csType, io.Discard)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: checksum: %v\n", err)
		return 1
	}

	r := io.Reader(rc)
	if uncompressed {
		decoded, err := retention.OpenBackup(r, sc.Encryption, retention.DecryptOpts{
			EncryptionType: *encryptionType,
			AgePass:        *agePass,
			GPGPass:        *gpgPass,
			OpenSSLPAss:    *opensslPass,
			Identity:       *identity,
		}, *filePath, "")
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: decrypt: %v\n", err)
			return 1
		}
		r = decoded
	}

	if _, err := io.Copy(hasher, r); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: read: %v\n", err)
		return 1
	}

	got := hasher.Sum()
	if got == expected {
		fmt.Fprintf(os.Stdout, "OK  %s (%d bytes, %s)\n", *filePath, n, string(csType))
	} else {
		fmt.Fprintf(os.Stderr, "FAIL %s: expected %s, got %s\n", *filePath, expected, got)
		return 1
	}
	return 0
}
