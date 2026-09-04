// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package common

import "context"

type TableTracer func(db, table string)

type tracerKey struct{}

func TraceTable(ctx context.Context, db, table string) {
	if fn := TracerFromContext(ctx); fn != nil {
		fn(db, table)
	}
}
func TracerFromContext(ctx context.Context) TableTracer {
	if ctx == nil {
		return nil
	}
	if v, ok := ctx.Value(tracerKey{}).(TableTracer); ok {
		return v
	}
	return nil
}
func WithTracer(ctx context.Context, fn TableTracer) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, tracerKey{}, fn)
}
