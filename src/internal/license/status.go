// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package license

type LicenseStatus struct {
	Valid   bool   `json:"valid"`
	Revoked bool   `json:"revoked,omitempty"`
	Expires string `json:"expires,omitempty"`
}
