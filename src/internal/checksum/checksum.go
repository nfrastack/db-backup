// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package checksum

import (
	"crypto/md5"
	"crypto/sha1"
	"fmt"
	"hash"
	"io"
	"strings"
)

type Type string

const (
	None Type = "none"
	MD5  Type = "md5"
	SHA1 Type = "sha1"
)

type Hasher struct {
	h    hash.Hash
	w    io.Writer
	Type Type
}

func New(t Type, w io.Writer) (*Hasher, error) {
	switch t {
	case MD5:
		h := md5.New()
		return &Hasher{h: h, w: io.MultiWriter(w, h), Type: t}, nil
	case SHA1:
		h := sha1.New()
		return &Hasher{h: h, w: io.MultiWriter(w, h), Type: t}, nil
	case None:
		return &Hasher{w: w, Type: t}, nil
	default:
		return nil, fmt.Errorf("unsupported checksum: %s", t)
	}
}
func Parse(s string) Type {
	switch strings.ToLower(s) {
	case "md5":
		return MD5
	case "sha1", "sha-1":
		return SHA1
	default:
		return None
	}
}

func (h *Hasher) Sum() string {
	if h.h == nil {
		return ""
	}
	return fmt.Sprintf("%x", h.h.Sum(nil))
}
func (h *Hasher) Write(p []byte) (int, error) {
	return h.w.Write(p)
}
