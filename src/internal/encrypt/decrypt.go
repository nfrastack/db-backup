// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package encrypt

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
	"golang.org/x/crypto/pbkdf2"
)

const (
	OpensslMagic = "Salted__"
	OpensslSalt  = 8
	OpensslIter  = 600000
	OpensslKeyIV = 48 // 32k + 16iv AES-256-CBC
)

type blockReader struct {
	block cipher.Block
	mode  cipher.BlockMode
	src   io.Reader
	cur   []byte
	next  []byte
	eof   bool
}

func DecryptOpenPGP(r io.Reader, identities []string, passphrase string) (io.Reader, error) {
	var ents openpgp.EntityList
	for _, id := range identities {
		e, err := readKeyRing(id)
		if err != nil {
			return nil, fmt.Errorf("openpgp: read identity %q: %w", id, err)
		}
		ents = append(ents, e...)
	}
	if passphrase == "" && len(ents) == 0 {
		return nil, fmt.Errorf("openpgp: no identity or passphrase provided")
	}
	attempts := 0
	prompt := func(keys []openpgp.Key, symmetric bool) ([]byte, error) {
		attempts++
		if passphrase == "" {
			return nil, fmt.Errorf("openpgp: passphrase required to unlock private key")
		}
		if attempts > 8 {
			return nil, fmt.Errorf("openpgp: incorrect passphrase")
		}
		return []byte(passphrase), nil
	}
	md, err := openpgp.ReadMessage(r, ents, prompt, &packet.Config{})
	if err != nil {
		return nil, fmt.Errorf("openpgp: decrypt: %w", err)
	}
	if md.IsSigned && md.SignatureError != nil {
		return nil, fmt.Errorf("openpgp: signature verification failed: %w", md.SignatureError)
	}
	if md.IsSigned && md.SignedBy == nil {
		return nil, fmt.Errorf("openpgp: signature verification failed: no signer")
	}
	return md.UnverifiedBody, nil
}

func DecryptOpenSSL(r io.Reader, passphrase string) (io.Reader, error) {
	if passphrase == "" {
		return nil, fmt.Errorf("openssl: passphrase required")
	}
	header := make([]byte, 8)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, fmt.Errorf("openssl: read header: %w", err)
	}
	if string(header) != OpensslMagic {
		return nil, fmt.Errorf("openssl: not an openssl enc backup (missing %q header)", OpensslMagic)
	}
	salt := make([]byte, OpensslSalt)
	if _, err := io.ReadFull(r, salt); err != nil {
		return nil, fmt.Errorf("openssl: read salt: %w", err)
	}
	keyiv := pbkdf2.Key([]byte(passphrase), salt, OpensslIter, OpensslKeyIV, sha256.New)
	block, err := aes.NewCipher(keyiv[:32])
	if err != nil {
		return nil, fmt.Errorf("openssl: aes: %w", err)
	}
	return &blockReader{
		block: block,
		mode:  cipher.NewCBCDecrypter(block, keyiv[32:48]),
		src:   r,
	}, nil
}

func (r *blockReader) Read(p []byte) (int, error) {
	for len(r.cur) == 0 {
		if r.eof {
			return 0, io.EOF
		}
		if err := r.refill(); err != nil {
			return 0, err
		}
	}
	n := copy(p, r.cur)
	r.cur = r.cur[n:]
	return n, nil
}
func ReadKeyRing(raw string) (openpgp.EntityList, error) {
	var data []byte
	if b, err := os.ReadFile(raw); err == nil {
		data = b
	} else if strings.HasPrefix(raw, "-----BEGIN PGP") {
		data = []byte(raw)
	} else {
		return nil, fmt.Errorf("no such file and not armored key text")
	}
	if bytes.HasPrefix(bytes.TrimSpace(data), []byte("-----BEGIN PGP")) {
		return openpgp.ReadArmoredKeyRing(bytes.NewReader(data))
	}
	return openpgp.ReadKeyRing(bytes.NewReader(data))
}
func readKeyRing(raw string) (openpgp.EntityList, error) {
	return ReadKeyRing(raw)
}

func (r *blockReader) refill() error {
	bs := r.block.BlockSize()
	if r.next == nil {
		b := make([]byte, bs)
		if _, err := io.ReadFull(r.src, b); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				r.eof = true
				return nil
			}
			return err
		}
		r.next = b
	}
	nb := make([]byte, bs)
	if _, err := io.ReadFull(r.src, nb); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			r.mode.CryptBlocks(r.next, r.next)
			pad := int(r.next[len(r.next)-1])
			if pad < 1 || pad > bs {
				return fmt.Errorf("openssl: invalid padding")
			}
			r.cur = r.next[:len(r.next)-pad]
			r.eof = true
			return nil
		}
		return err
	}
	r.mode.CryptBlocks(r.next, r.next)
	r.cur = r.next
	r.next = nb
	return nil
}
