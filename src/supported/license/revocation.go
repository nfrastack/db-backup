// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: LicenseRef-NSLv1

// This file is part of the Supported (Supporter) edition of db-backup and is
// licensed under the Nfrastack Supporter License v1 (NSLv1).
// It is excluded from the Community build. !community.

//go:build !community

package license

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/nfrastack/db-backup/internal/license"
)

var (
	nexusSeedAX = []byte{
		0x34, 0xe5, 0xa7, 0xda, 0x34, 0x22,
		0x99, 0xcd, 0xfd, 0x30, 0x57, 0xb1,
		0x12, 0xb5, 0x19,
	}
	nexusSeedBX = []byte{
		0x3a, 0xe3, 0xb2, 0xd9, 0x33, 0x79,
		0xd5, 0x89, 0xbd, 0x36, 0x40, 0xa9,
		0x4e, 0xed, 0x46,
	}
	nexusSeedMask = []byte{
		0x5c, 0x91, 0xd3, 0xaa, 0x47, 0x18, 0xb6,
		0xe2, 0x93, 0x55, 0x2f, 0xc4, 0x61, 0x9b, 0x77,
	}
)

type RevocationState struct {
	LicenseID string `json:"license_id"`
	Revoked   bool   `json:"revoked"`
	SeenAt    string `json:"seen_at"`
}

func CheckRevocation(ctx context.Context, licenseID, serverURL, key string) (*license.LicenseStatus, error) {
	if licenseID == "" {
		return nil, fmt.Errorf("empty license_id")
	}
	if serverURL == "" {
		serverURL = defaultIngestURL()
	}
	body, err := json.Marshal(map[string]string{"license_id": licenseID})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL+"/ingest/license", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("license revocation request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("x-nfrastack-key", key)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("license revocation: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("reading revocation response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("server responded %d", resp.StatusCode)
	}
	var status license.LicenseStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return nil, fmt.Errorf("decoding revocation response: %w", err)
	}
	return &status, nil
}

func IsRevokedCached(licenseID string) bool {
	p := revocationPath()
	if p == "" || licenseID == "" {
		return false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	var st RevocationState
	if json.Unmarshal(b, &st) != nil {
		return false
	}
	return st.Revoked && st.LicenseID == licenseID
}
func MarkRevoked(licenseID string, revoked bool) error {
	p := revocationPath()
	if p == "" || licenseID == "" {
		return nil
	}
	st := RevocationState{
		LicenseID: licenseID,
		Revoked:   revoked,
		SeenAt:    time.Now().UTC().Format(time.RFC3339),
	}
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func RevokedFor(lic *license.License) bool {
	if lic == nil || lic.LicenseID == "" {
		return false
	}
	return IsRevokedCached(lic.LicenseID)
}
func assembledBaseURL() string {
	out := make([]byte, 0, len(nexusSeedAX)+len(nexusSeedBX))
	for i := range nexusSeedAX {
		out = append(out, nexusSeedAX[i]^nexusSeedMask[i])
	}
	for i := range nexusSeedBX {
		out = append(out, nexusSeedBX[i]^nexusSeedMask[i])
	}
	return string(out)
}

func defaultIngestURL() string { return assembledBaseURL() }

func revocationPath() string {
	if license.StateDir() == "" {
		return ""
	}
	return filepath.Join(license.StateDir(), "revocation.json")
}
