// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package common

import "context"

type logFieldsKey struct{}

func LogFieldsFromContext(ctx context.Context) []any {
	if ctx == nil {
		return nil
	}
	if v, ok := ctx.Value(logFieldsKey{}).([]any); ok {
		return v
	}
	return nil
}

func WithLogFields(ctx context.Context, fields ...any) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(fields) == 0 {
		return ctx
	}
	existing := LogFieldsFromContext(ctx)
	merged := make([]any, 0, len(existing)+len(fields))
	merged = append(merged, existing...)
	merged = append(merged, fields...)
	return context.WithValue(ctx, logFieldsKey{}, merged)
}
