// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: LicenseRef-NSLv1

// This file is part of the Supported (Supporter) edition of db-backup and is
// licensed under the Nfrastack Supporter License v1 (NSLv1).
// It is excluded from the Community build. !community.

//go:build !community

package backend

import (
	"fmt"
	"io"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/packet"

	"github.com/nfrastack/db-backup/internal/encrypt"
)

type openPGPEncrypt struct {
	passphrase string
	recipients []string
}

func (e *openPGPEncrypt) Decrypt(r io.Reader) (io.Reader, error) {
	if e.passphrase == "" {
		return nil, fmt.Errorf("openpgp: public-key decrypt requires an identity")
	}
	return encrypt.DecryptOpenPGP(r, nil, e.passphrase)
}
func (e *openPGPEncrypt) Encrypt(w io.Writer) (io.WriteCloser, error) {
	cfg := &packet.Config{}
	if e.passphrase != "" {
		return openpgp.SymmetricallyEncrypt(w, []byte(e.passphrase), nil, cfg)
	}
	ents, err := e.loadPublicKeys()
	if err != nil {
		return nil, err
	}
	if len(ents) == 0 {
		return nil, fmt.Errorf("openpgp: no valid recipients")
	}
	return openpgp.Encrypt(w, ents, nil, nil, cfg)
}
func init() {
	encrypt.RegisterEncryptor(encrypt.EncryptorSpec{
		Name:      "gpg",
		Aliases:   []string{"pgp", "openpgp"},
		Extension: ".gpg",
		Magic:     []string{"-----BEGIN PGP"},
		Packet:    true,
		New: func(passphrase string, recipients ...string) (encrypt.Encryptor, error) {
			return newOpenPGP(passphrase, recipients)
		},
	})
}

func (e *openPGPEncrypt) loadPublicKeys() (openpgp.EntityList, error) {
	var out openpgp.EntityList
	for _, rec := range e.recipients {
		ents, err := encrypt.ReadKeyRing(rec)
		if err != nil {
			return nil, fmt.Errorf("openpgp: read recipient %q: %w", rec, err)
		}
		out = append(out, ents...)
	}
	return out, nil
}
func newOpenPGP(passphrase string, recipients []string) (*openPGPEncrypt, error) {
	if passphrase == "" && len(recipients) == 0 {
		return nil, fmt.Errorf("openpgp: at least one recipient or a passphrase required")
	}
	return &openPGPEncrypt{passphrase: passphrase, recipients: recipients}, nil
}
