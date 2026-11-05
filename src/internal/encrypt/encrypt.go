// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package encrypt

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
)

type Type string

const (
	None    Type = ""
	Age     Type = "age"
	OpenPGP Type = "gpg"
	OpenSSL Type = "openssl"
)

type EncryptorSpec struct {
	Name      string
	Aliases   []string
	Extension string
	Magic     []string
	Packet    bool //magic
	New       func(passphrase string, recipients ...string) (Encryptor, error)
}

var (
	encryptorsMu sync.RWMutex
	encryptors   = map[string]EncryptorSpec{}
)

type Encryptor interface {
	Encrypt(w io.Writer) (io.WriteCloser, error)
	Decrypt(r io.Reader) (io.Reader, error)
}

type nilEncrypt struct{}

type nopCloser struct {
	io.Writer
}

func (nopCloser) Close() error { return nil }

func (n *nilEncrypt) Decrypt(r io.Reader) (io.Reader, error) {
	return r, nil
}

func Detect(r io.Reader) (Type, io.Reader, error) {
	br := bufio.NewReader(r)
	head, err := br.Peek(64)
	if err != nil && err != io.EOF {
		return None, br, nil
	}
	for _, spec := range encryptors {
		for _, m := range spec.Magic {
			if bytes.HasPrefix(head, []byte(m)) {
				return Type(spec.Name), br, nil
			}
		}
	}

	switch {
	case bytes.HasPrefix(head, []byte("-----BEGIN PGP")):
		return OpenPGP, br, nil
	case bytes.HasPrefix(head, []byte("Salted__")):
		return OpenSSL, br, nil
	case len(head) > 0 && head[0]&0x80 != 0:
		return OpenPGP, br, nil
	}
	return None, br, nil
}
func (n *nilEncrypt) Encrypt(w io.Writer) (io.WriteCloser, error) {
	return nopCloser{w}, nil
}
func Encryptors() []string {
	encryptorsMu.RLock()
	defer encryptorsMu.RUnlock()
	names := make([]string, 0, len(encryptors))
	for name := range encryptors {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func Extension(t Type) string {
	if spec := LookupEncryptor(string(t)); spec != nil {
		return spec.Extension
	}
	switch t {
	case OpenPGP:
		return ".gpg"
	case OpenSSL:
		return ".enc"
	}
	return ""
}
func LookupEncryptor(name string) *EncryptorSpec {
	if name == "" {
		return nil
	}
	name = strings.ToLower(name)
	encryptorsMu.RLock()
	defer encryptorsMu.RUnlock()
	for n, spec := range encryptors {
		if n == name {
			return &spec
		}
		for _, a := range spec.Aliases {
			if a == name {
				return &spec
			}
		}
	}
	return nil
}

func New(t Type, passphrase string, recipients ...string) (Encryptor, error) {
	if t == None {
		return &nilEncrypt{}, nil
	}
	spec := LookupEncryptor(string(t))
	if spec == nil || spec.New == nil {
		return nil, fmt.Errorf("unknown encryption type %s", t)
	}
	return spec.New(passphrase, recipients...)
}

func Parse(s string) Type {
	if spec := LookupEncryptor(s); spec != nil {
		return Type(spec.Name)
	}
	switch {
	case recognizesAlias(s, "gpg", []string{"pgp", "openpgp"}):
		return OpenPGP
	case recognizesAlias(s, "openssl", []string{"aes-256-cbc", "aes256"}):
		return OpenSSL
	}
	return None
}
func RegisterEncryptor(spec EncryptorSpec) {
	if spec.Name == "" {
		panic("encrypt: type registered with empty name")
	}
	encryptorsMu.Lock()
	defer encryptorsMu.Unlock()
	if _, exists := encryptors[spec.Name]; exists {
		panic("encrypt: duplicate type " + spec.Name)
	}
	encryptors[spec.Name] = spec
}

func recognizesAlias(s, canonical string, aliases []string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == canonical {
		return true
	}
	for _, a := range aliases {
		if s == a {
			return true
		}
	}
	return false
}
