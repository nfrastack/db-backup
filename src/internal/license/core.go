// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package license

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	FeatureNotifications = "notifications"
	FeatureIncremental   = "incremental"
	FeatureEncryption    = "encryption"
	FeatureStorage       = "storage"
	FeatureArchive       = "archive"
	FeatureRetention     = "retention"
	FeatureMaintenance   = "maintenance"
	FeatureSchedule      = "schedule"
	FeatureRestore       = "restore"
)

type Customer struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	OrgID string `json:"org_id,omitempty"`
}

type LicenseContact struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
	Role  string `json:"role,omitempty"`
}

type LicenseLimits struct {
	MaxInstances int `json:"max_instances,omitempty"`
}

type License struct {
	Contacts     []LicenseContact `json:"contacts,omitempty"`
	Customer     Customer         `json:"customer"`
	ExpiresAt    string           `json:"expires_at"`
	Features     []string         `json:"features"`
	Format       int              `json:"format"`
	IssuedAt     string           `json:"issued_at"`
	KeyID        string           `json:"key_id"`
	LicenseClass string           `json:"license_class,omitempty"`
	LicenseID    string           `json:"license_id,omitempty"`
	Limits       *LicenseLimits   `json:"limits,omitempty"`
	NotBefore    string           `json:"not_before,omitempty"`
	RenewalOf    string           `json:"renewal_of,omitempty"`
	Revoked      bool             `json:"revoked,omitempty"`
	Tier         string           `json:"tier"`
	Tool         string           `json:"tool,omitempty"`
	sig          []byte
	tokens       map[string][]byte
}

func DaysRemaining() int {
	lic, err := Installed()
	if err != nil || lic == nil {
		return -1
	}
	exp, ok := lic.ExpiryTime()
	if !ok {
		return -1
	}
	d := int(time.Until(exp).Hours() / 24)
	if d < 0 {
		return 0
	}
	return d
}
func (l *License) ExpiryTime() (time.Time, bool) {
	if l == nil || l.ExpiresAt == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, l.ExpiresAt)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}
func (l *License) Has(feature string) bool {
	if l == nil {
		return false
	}
	for _, f := range l.Features {
		if f == feature {
			return true
		}
	}
	return false
}

func IsExpired(lic *License) bool {
	if lic == nil {
		return false
	}
	return checkWindow(lic) != nil
}
func Parse(raw string) (*License, error) {
	return parse(raw, publicKeys)
}

func artifact(raw string) (payload, sig []byte, err error) {
	i := strings.LastIndexByte(raw, '.')
	if i <= 0 || i == len(raw)-1 {
		return nil, nil, errors.New("malformed license: expected payload.signature")
	}
	payload, err = decodeB64(raw[:i])
	if err != nil {
		return nil, nil, fmt.Errorf("malformed license payload: %w", err)
	}
	sig, err = decodeB64(raw[i+1:])
	if err != nil {
		return nil, nil, fmt.Errorf("malformed license signature: %w", err)
	}
	return payload, sig, nil
}

func checkWindow(lic *License) error {
	now := time.Now()
	if lic.NotBefore != "" {
		if t, err := time.Parse(time.RFC3339, lic.NotBefore); err == nil && now.Before(t) {
			return fmt.Errorf("license not yet valid (valid from %s)", lic.NotBefore)
		}
	}
	if lic.ExpiresAt == "" {
		return errors.New("license missing expiry")
	}
	exp, err := time.Parse(time.RFC3339, lic.ExpiresAt)
	if err != nil {
		return fmt.Errorf("license expiry: %w", err)
	}
	if now.After(exp) {
		return fmt.Errorf("license expired %s", lic.ExpiresAt)
	}
	return nil
}
func decodeB64(s string) ([]byte, error) {

	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return nil, errors.New("invalid base64")
}
func deriveToken(sig, salt []byte) []byte {
	mac := hmac.New(sha256.New, sig)
	mac.Write(keySalt())
	mac.Write(salt)
	return mac.Sum(nil)
}

func parse(raw string, keys map[string][]byte) (*License, error) {
	payload, sig, err := artifact(raw)
	if err != nil {
		return nil, err
	}

	var lic License
	if err := json.Unmarshal(payload, &lic); err != nil {
		return nil, fmt.Errorf("license payload: %w", err)
	}
	if lic.Format != 1 {
		return nil, fmt.Errorf("unsupported license format %d", lic.Format)
	}
	if lic.KeyID == "" {
		return nil, errors.New("license missing key_id")
	}

	pub, ok := keys[lic.KeyID]
	if !ok {
		return nil, fmt.Errorf("unknown signing key %q", lic.KeyID)
	}
	if len(pub) != ed25519.PublicKeySize {
		return nil, errors.New("embedded key has invalid length")
	}
	if !ed25519.Verify(pub, payload, sig) {
		return nil, errors.New("signature verification failed")
	}

	if err := checkWindow(&lic); err != nil {
		return nil, err
	}
	if lic.Revoked {
		return nil, errors.New("license revoked")
	}
	if lic.Tool != "db-backup" {
		return nil, fmt.Errorf("license is for %q, not db-backup", lic.Tool)
	}

	lic.sig = sig
	lic.tokens = make(map[string][]byte, len(lic.Features))
	for _, f := range lic.Features {
		lic.tokens[f] = deriveToken(sig, featureSalt(f))
	}

	return &lic, nil
}
func (l *License) permits(feature string) bool {
	if l == nil || len(l.sig) == 0 {
		return false
	}
	want, ok := l.tokens[feature]
	if !ok {
		return false
	}
	got := deriveToken(l.sig, featureSalt(feature))
	return subtle.ConstantTimeCompare(want, got) == 1
}
