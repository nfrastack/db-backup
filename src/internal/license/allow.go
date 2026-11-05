// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package license

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"sync"
)

type FeatureState struct {
	Allowed bool
	Reason  string
	License *License
}

var (
	loadMu    sync.Mutex
	loadGen   uint64
	loadedGen uint64
	loadValid bool
	loadLic   *License
	loadErr   error
)

func AllowArchive() error {
	lic, err := installedLicense()
	if err != nil {
		return err
	}
	if !hasFeature(lic, FeatureArchive, saltArchive) {
		return errors.New("this license does not include the archive feature")
	}
	return nil
}

func AllowEncryption() error {
	lic, err := installedLicense()
	if err != nil {
		return err
	}
	if !hasFeature(lic, FeatureEncryption, saltEncryption) {
		return errors.New("this license does not include the encryption feature")
	}
	return nil
}

func AllowIncremental() error {
	lic, err := installedLicense()
	if err != nil {
		return err
	}
	if !hasFeature(lic, FeatureIncremental, saltIncremental) {
		return errors.New("this license does not include the incremental feature")
	}
	return nil
}

func AllowMaintenance() error {
	lic, err := installedLicense()
	if err != nil {
		return err
	}
	if !hasFeature(lic, FeatureMaintenance, saltMaintenance) {
		return errors.New("this license does not include the maintenance feature")
	}
	return nil
}

func AllowNotifications() error {
	lic, err := installedLicense()
	if err != nil {
		return err
	}
	if !hasFeature(lic, FeatureNotifications, saltNotif) {
		return errors.New("this license does not include the notifications feature")
	}
	return nil
}
func AllowRestore() error {
	lic, err := installedLicense()
	if err != nil {
		return err
	}
	if !hasFeature(lic, FeatureRestore, saltRestore) {
		return errors.New("this license does not include the restore-profile feature")
	}
	return nil
}
func AllowRetention() error {
	lic, err := installedLicense()
	if err != nil {
		return err
	}
	if !hasFeature(lic, FeatureRetention, saltRetention) {
		return errors.New("this license does not include the retention feature")
	}
	return nil
}

func AllowSchedule() error {
	lic, err := installedLicense()
	if err != nil {
		return err
	}
	if !hasFeature(lic, FeatureSchedule, saltSchedule) {
		return errors.New("this license does not include the advanced scheduling feature (natural-language, multi-time, advanced blackout/days)")
	}
	return nil
}
func AllowStorage() error {
	lic, err := installedLicense()
	if err != nil {
		return err
	}
	if !hasFeature(lic, FeatureStorage, saltStorage) {
		return errors.New("this license does not include the storage feature")
	}
	return nil
}

var IsRevoked func(licenseID string) bool

func Community() bool {
	lic, err := Installed()
	if err != nil || lic == nil {
		return true
	}
	return IsExpired(lic) || revoked(lic)
}

func Edition() string {
	if Community() {
		return "community"
	}
	return "supported"
}

func Enabled(feature string) FeatureState {
	lic, err := installedLicense()
	if err != nil {
		return FeatureState{Reason: err.Error()}
	}
	if !hasFeature(lic, feature, featureSalt(feature)) {
		return FeatureState{Reason: "this license does not include the " + feature + " feature", License: lic}
	}
	return FeatureState{Allowed: true, License: lic}
}

func hasFeature(lic *License, feature string, salt []byte) bool {
	if lic == nil {
		return false
	}
	want, ok := lic.tokens[feature]
	if !ok {
		return false
	}
	got := deriveToken(lic.sig, salt)
	return subtle.ConstantTimeCompare(want, got) == 1
}

func installedLicense() (*License, error) {
	lic, err := Installed()
	switch {
	case err != nil:
		return nil, fmt.Errorf("license invalid: %w", err)
	case lic == nil:
		return nil, errors.New("no license installed")
	}
	if revoked(lic) {
		return nil, errors.New("license has been revoked - supporter features are disabled")
	}
	if err := checkWindow(lic); err != nil {
		return nil, err
	}
	return lic, nil
}

func Installed() (*License, error) {
	loadMu.Lock()
	defer loadMu.Unlock()
	if !loadValid || loadedGen != loadGen {
		loadLic, loadErr = Load()
		loadedGen = loadGen
		loadValid = true
	}
	return loadLic, loadErr
}

func revoked(lic *License) bool {
	return lic != nil && lic.LicenseID != "" && IsRevoked != nil && IsRevoked(lic.LicenseID)
}

func Reload() {
	loadMu.Lock()
	loadGen++
	loadMu.Unlock()
}

func (c FeatureState) OK() bool { return c.Allowed }
