// Package tenantctx stores the tenant identifier, the cross-tenant mark and the
// tenant log field on a context.
package tenantctx

import (
	"context"
	"log/slog"
)

// LogField is the logger field the tenant identifier is bound under.
const LogField = "tenant_id"

type (
	idKey     struct{}
	crossKey  struct{}
	unbindKey struct{}
)

// WithID returns a context carrying id and no cross-tenant mark; the empty id
// reads as absent. It leaves the logger alone.
func WithID(ctx context.Context, id string) context.Context {
	return context.WithValue(context.WithValue(ctx, crossKey{}, false), idKey{}, id)
}

// Logger carries the logging module's functions for binding a field onto a
// context's logger and taking one off.
type Logger struct {
	With    func(ctx context.Context, args ...any) context.Context
	Without func(ctx context.Context, keys ...string) context.Context
}

// Bind is WithID plus the logger: it binds id under LogField, or takes the field
// off for the empty id.
func Bind(ctx context.Context, id string, logger Logger) context.Context {
	ctx = WithID(ctx, id)
	if id == "" {
		ctx = logger.Without(ctx, LogField)
	} else {
		ctx = logger.With(ctx, slog.String(LogField, id))
	}
	return context.WithValue(ctx, unbindKey{}, logger.Without)
}

// ID returns the tenant identifier the context carries; a stored empty string
// reads as absent.
func ID(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(idKey{}).(string)
	if !ok || id == "" {
		return "", false
	}
	return id, true
}

// WithCrossTenant returns a context marked as deliberately unscoped, with no
// identifier and the bound log field removed.
func WithCrossTenant(ctx context.Context) context.Context {
	if without, ok := ctx.Value(unbindKey{}).(func(context.Context, ...string) context.Context); ok {
		ctx = without(ctx, LogField)
	}
	return context.WithValue(context.WithValue(ctx, idKey{}, ""), crossKey{}, true)
}

// CrossTenant reports whether the context is marked as deliberately unscoped.
func CrossTenant(ctx context.Context) bool {
	marked, _ := ctx.Value(crossKey{}).(bool)
	return marked
}
