// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: LicenseRef-NSLv1

// This file is part of the Supported (Supporter) edition of db-backup and is
// licensed under the Nfrastack Supporter License v1 (NSLv1).
// It is excluded from the Community build. !community.

//go:build !community

package backend

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"

	"golang.org/x/crypto/pbkdf2"

	"github.com/nfrastack/db-backup/internal/encrypt"
)

type openSSLEncrypt struct {
	passphrase string
}

type blockWriter struct {
	block   cipher.Block
	mode    cipher.BlockMode
	dst     io.Writer
	pending []byte
	closed  bool
}

func (w *blockWriter) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	bs := w.block.BlockSize()
	pad := bs - (len(w.pending) % bs)
	if pad == 0 {
		pad = bs
	}
	padded := append(w.pending, bytes.Repeat([]byte{byte(pad)}, pad)...)
	w.mode.CryptBlocks(padded, padded)
	_, err := w.dst.Write(padded)
	return err
}
func (e *openSSLEncrypt) Decrypt(r io.Reader) (io.Reader, error) {
	return encrypt.DecryptOpenSSL(r, e.passphrase)
}
func (e *openSSLEncrypt) Encrypt(w io.Writer) (io.WriteCloser, error) {
	salt := make([]byte, encrypt.OpensslSalt)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("openssl: generate salt: %w", err)
	}
	keyiv := pbkdf2.Key([]byte(e.passphrase), salt, encrypt.OpensslIter, encrypt.OpensslKeyIV, sha256.New)
	block, err := aes.NewCipher(keyiv[:32])
	if err != nil {
		return nil, fmt.Errorf("openssl: aes: %w", err)
	}
	if _, err := w.Write(append([]byte(encrypt.OpensslMagic), salt...)); err != nil {
		return nil, fmt.Errorf("openssl: write header: %w", err)
	}
	return &blockWriter{
		block: block,
		mode:  cipher.NewCBCEncrypter(block, keyiv[32:48]),
		dst:   w,
	}, nil
}

func (w *blockWriter) Write(p []byte) (int, error) {
	w.pending = append(w.pending, p...)
	bs := w.block.BlockSize()
	full := len(w.pending) - (len(w.pending) % bs)
	if full > 0 {
		chunk := w.pending[:full]
		w.mode.CryptBlocks(chunk, chunk)
		if _, err := w.dst.Write(chunk); err != nil {
			return 0, err
		}
		w.pending = w.pending[full:]
	}
	return len(p), nil
}
func init() {
	encrypt.RegisterEncryptor(encrypt.EncryptorSpec{
		Name:      "openssl",
		Aliases:   []string{"aes-256-cbc", "aes256"},
		Extension: ".enc",
		Magic:     []string{"Salted__"},
		New: func(passphrase string, recipients ...string) (encrypt.Encryptor, error) {
			return newOpenSSL(passphrase)
		},
	})
}

func newOpenSSL(passphrase string) (*openSSLEncrypt, error) {
	if passphrase == "" {
		return nil, fmt.Errorf("openssl: passphrase required")
	}
	return &openSSLEncrypt{passphrase: passphrase}, nil
}
