// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package license

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/klauspost/compress/zstd"
)

const maxLicenseSize = 1 << 20

var (
	magicGzip = []byte{0x1f, 0x8b}
	magicZstd = []byte{0x28, 0xb5, 0x2f, 0xfd}
)

func NormalizeArtifact(value string) (string, error) {
	value = strings.Join(strings.Fields(value), "")
	if value == "" {
		return "", errors.New("empty license value")
	}
	if looksLikeArtifact(value) {
		if _, _, err := artifact(value); err == nil {
			return value, nil
		}
	}

	decoded, err := decodeAnyBase64(value)
	if err != nil {
		return "", fmt.Errorf("unrecognized license encoding: not an artifact and not base64")
	}

	switch {
	case bytes.HasPrefix(decoded, magicGzip):
		return inflateGzip(decoded)
	case bytes.HasPrefix(decoded, magicZstd):
		return inflateZstd(decoded)
	default:
		return "", fmt.Errorf("unrecognized license payload: expected gzip or zstd content after base64")
	}
}

func decodeAnyBase64(s string) ([]byte, error) {
	s = strings.Join(strings.Fields(s), "")
	for _, enc := range []*base64.Encoding{
		base64.RawURLEncoding,
		base64.URLEncoding,
		base64.RawStdEncoding,
		base64.StdEncoding,
	} {
		if b, err := enc.DecodeString(s); err == nil && len(b) > 0 {
			return b, nil
		}
	}
	return nil, errors.New("invalid base64")
}

func inflateGzip(data []byte) (string, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("gzip license: %w", err)
	}
	defer zr.Close()
	out, err := io.ReadAll(io.LimitReader(zr, maxLicenseSize+1))
	if err != nil {
		return "", fmt.Errorf("gzip license: %w", err)
	}
	if len(out) > maxLicenseSize {
		return "", errors.New("decompressed license exceeds size limit")
	}
	s := strings.TrimSpace(string(out))
	if !looksLikeArtifact(s) {
		return "", errors.New("decompressed license is not a valid artifact")
	}
	return s, nil
}

func inflateZstd(data []byte) (string, error) {
	dec, err := zstd.NewReader(bytes.NewReader(data),
		zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderMaxMemory(8<<20),
	)
	if err != nil {
		return "", fmt.Errorf("zstd license: %w", err)
	}
	defer dec.Close()
	out, err := io.ReadAll(io.LimitReader(dec, maxLicenseSize+1))
	if err != nil {
		return "", fmt.Errorf("zstd license: %w", err)
	}
	if len(out) > maxLicenseSize {
		return "", errors.New("decompressed license exceeds size limit")
	}
	s := strings.TrimSpace(string(out))
	if !looksLikeArtifact(s) {
		return "", errors.New("decompressed license is not a valid artifact")
	}
	return s, nil
}
