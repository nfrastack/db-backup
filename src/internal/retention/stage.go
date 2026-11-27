// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package retention

import (
	"fmt"
	"io"
)

func CopyWithLimit(w io.Writer, r io.Reader, limit int64) (int64, error) {
	if limit < 0 {
		return 0, fmt.Errorf("copy limit must not be negative")
	}
	n, err := io.CopyN(w, r, limit)
	if err == io.EOF {
		return n, nil
	}
	if err != nil {
		return n, err
	}

	var extra [1]byte
	if _, err := io.ReadFull(r, extra[:]); err == io.EOF {
		return n, nil
	} else if err != nil {
		return n, err
	}
	return n, fmt.Errorf("source exceeds %d-byte limit", limit)
}
