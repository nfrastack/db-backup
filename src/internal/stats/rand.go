// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package stats

import "crypto/rand"

func randRead(b []byte) (int, error) {
	return rand.Read(b)
}
