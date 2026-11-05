// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package retention

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/nfrastack/db-backup/internal/compress"
	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/encrypt"
)

type DecryptOpts struct {
	EncryptionType string
	AgePass        string
	GPGPass        string
	OpenSSLPAss    string
	Identity       string
	FallbackPass   string
	FallbackID     string
}

func OpenBackup(r io.Reader, meta *EncryptionMeta, opts DecryptOpts, name, compressType string) (io.Reader, error) {
	t, br, err := resolveType(r, meta, opts)
	if err != nil {
		return nil, err
	}
	if t != encrypt.None {
		pass := resolveSecretLocal(passFor(opts, t))
		var ids []string
		if id := resolveSecretLocal(firstNonEmpty(opts.Identity, opts.FallbackID)); id != "" {
			ids = []string{id}
		}
		switch t {
		case encrypt.Age:
			if pass != "" {
				br, err = encrypt.DecryptAgePassphrase(br, pass)
			} else {
				br, err = encrypt.DecryptAge(br, ids...)
			}
		case encrypt.OpenPGP:
			br, err = encrypt.DecryptOpenPGP(br, ids, pass)
		case encrypt.OpenSSL:
			if pass == "" {
				return nil, fmt.Errorf("openssl-encrypted backup requires --openssl-passphrase or restore.passphrase")
			}
			br, err = encrypt.DecryptOpenSSL(br, pass)
		default:
			return nil, fmt.Errorf("unsupported encryption type: %s", t)
		}
		if err != nil {
			return nil, err
		}
	}

	base := name
	for _, ext := range []string{".age", ".gpg", ".enc"} {
		base = strings.TrimSuffix(base, ext)
	}
	if compressType == "" {
		compressType = string(compress.FromExtension(filepath.Ext(base)))
	}
	ct := compress.Parse(compressType)
	if ct == compress.None {
		return br, nil
	}
	comp, err := compress.New(ct)
	if err != nil {
		return nil, err
	}
	return comp.Decompress(br)
}
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func passFor(opts DecryptOpts, t encrypt.Type) string {
	switch t {
	case encrypt.Age:
		return firstNonEmpty(opts.AgePass, opts.FallbackPass)
	case encrypt.OpenPGP:
		return firstNonEmpty(opts.GPGPass, opts.FallbackPass)
	case encrypt.OpenSSL:
		return firstNonEmpty(opts.OpenSSLPAss, opts.FallbackPass)
	default:
		return ""
	}
}
func resolveSecretLocal(v string) string {
	return config.ResolveSecret(v)
}

func resolveType(r io.Reader, meta *EncryptionMeta, opts DecryptOpts) (encrypt.Type, io.Reader, error) {
	if opts.EncryptionType != "" && !strings.EqualFold(opts.EncryptionType, "auto") {
		t := encrypt.Parse(opts.EncryptionType)
		if t == encrypt.None {
			return encrypt.None, r, fmt.Errorf("unknown encryption type %q (auto|age|gpg|openssl)", opts.EncryptionType)
		}
		return t, r, nil
	}
	if meta != nil && meta.Type != "" {
		return encrypt.Parse(meta.Type), r, nil
	}
	return encrypt.Detect(r)
}
