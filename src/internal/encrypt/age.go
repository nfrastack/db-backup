// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package encrypt

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"filippo.io/age"
)

type ageEncrypt struct {
	passphrase string
	recipients []string
	identities []string
}

func (e *ageEncrypt) Decrypt(r io.Reader) (io.Reader, error) {
	if e.passphrase != "" {
		id, err := age.NewScryptIdentity(e.passphrase)
		if err != nil {
			return nil, fmt.Errorf("age: scrypt identity: %w", err)
		}
		out, err := age.Decrypt(r, id)
		if err != nil {
			return nil, fmt.Errorf("age: passphrase decrypt: %w", err)
		}
		return out, nil
	}

	if len(e.identities) > 0 {
		if ids, err := identitiesFrom(e.identities); err == nil {
			out, err := age.Decrypt(r, ids...)
			if err == nil {
				return out, nil
			}
			return nil, err
		}
	}

	return nil, fmt.Errorf("age: no identity provided (use --identity or restore.identity)")
}

func DecryptAge(r io.Reader, identities ...string) (io.Reader, error) {
	if len(identities) == 0 {
		return nil, fmt.Errorf("age: no identities provided")
	}
	ids, err := identitiesFrom(identities)
	if err != nil {
		return nil, fmt.Errorf("age: %w", err)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("age: no identities provided")
	}
	return age.Decrypt(r, ids...)
}

func DecryptAgePassphrase(r io.Reader, passphrase string) (io.Reader, error) {
	if passphrase == "" {
		return nil, fmt.Errorf("age: no passphrase provided")
	}
	id, err := age.NewScryptIdentity(passphrase)
	if err != nil {
		return nil, fmt.Errorf("age: scrypt identity: %w", err)
	}
	return age.Decrypt(r, id)
}
func (e *ageEncrypt) Encrypt(w io.Writer) (io.WriteCloser, error) {
	if e.passphrase != "" {
		r, err := age.NewScryptRecipient(e.passphrase)
		if err != nil {
			return nil, fmt.Errorf("age: scrypt recipient: %w", err)
		}
		return age.Encrypt(w, r)
	}

	var recipients []age.Recipient
	for _, r := range e.recipients {
		r2, err := age.ParseX25519Recipient(r)
		if err != nil {
			return nil, fmt.Errorf("age: parse recipient %q: %w", r, err)
		}
		recipients = append(recipients, r2)
	}
	if len(recipients) == 0 {
		return nil, fmt.Errorf("age: no valid recipients")
	}
	return age.Encrypt(w, recipients...)
}

func NewAge(passphrase string, recipients ...string) (*ageEncrypt, error) {
	if passphrase == "" && len(recipients) == 0 {
		return nil, fmt.Errorf("age: at least one recipient or a passphrase required")
	}
	return &ageEncrypt{passphrase: passphrase, recipients: recipients}, nil
}

func (e *ageEncrypt) WithIdentities(ids ...string) *ageEncrypt {
	cp := *e
	cp.identities = ids
	return &cp
}

func identitiesFrom(vals []string) ([]age.Identity, error) {
	var ids []age.Identity
	for _, val := range vals {
		if strings.HasPrefix(val, "AGE-SECRET-KEY-") {
			id, err := age.ParseX25519Identity(strings.TrimSpace(val))
			if err != nil {
				return nil, err
			}
			ids = append(ids, id)
			continue
		}
		b, err := os.ReadFile(val)
		if err != nil {
			return nil, err
		}
		parsed, err := age.ParseIdentities(bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		ids = append(ids, parsed...)
	}
	return ids, nil
}
func init() {
	RegisterEncryptor(EncryptorSpec{
		Name:      "age",
		Extension: ".age",
		Magic:     []string{"age-encryption.org/v1"},
		New: func(passphrase string, recipients ...string) (Encryptor, error) {
			return NewAge(passphrase, recipients...)
		},
	})
}
