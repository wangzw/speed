// Package tenantctx carries the tenant identifier and the cross-tenant mark a
// context holds. It is internal so that only this capability's two writers,
// which read the roster before they write, can set a tenant identifier. A
// context carries a tenant or crosses tenants, never both.
package tenantctx

import (
	"context"
	"log/slog"
)

// LogField is the logger field both writers bind the tenant identifier to.
const LogField = "tenant_id"

type (
	idKey     struct{}
	crossKey  struct{}
	unbindKey struct{}
)

// WithID returns a context carrying id and no cross-tenant mark. The empty
// identifier states that the context belongs to no tenant; ID reads it as
// absent. It leaves the logger alone.
func WithID(ctx context.Context, id string) context.Context {
	return context.WithValue(context.WithValue(ctx, crossKey{}, false), idKey{}, id)
}

// Logger holds the logging module's functions for binding a field onto a
// context's logger and taking one off. The writers hand them in because the
// root package imports no module of this repository, and it imports this
// package.
type Logger struct {
	// With binds the given key-value pairs onto the context's logger.
	With func(ctx context.Context, args ...any) context.Context
	// Without removes the given keys from the context's logger.
	Without func(ctx context.Context, keys ...string) context.Context
}

// Bind writes id into the context as WithID does and binds it on the logger
// under LogField, replacing an earlier value; the empty identifier takes the
// field off. It keeps the unbind function in the context so WithCrossTenant can
// remove the field later.
func Bind(ctx context.Context, id string, logger Logger) context.Context {
	ctx = WithID(ctx, id)
	if id == "" {
		ctx = logger.Without(ctx, LogField)
	} else {
		ctx = logger.With(ctx, slog.String(LogField, id))
	}
	return context.WithValue(ctx, unbindKey{}, logger.Without)
}

// ID returns the tenant identifier this context carries. A stored empty string,
// and so a context marked to cross, reads as absent.
func ID(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(idKey{}).(string)
	if !ok || id == "" {
		return "", false
	}
	return id, true
}

// WithCrossTenant returns a context marked as deliberately unscoped, with no
// tenant identifier and without the identifier on its logger.
func WithCrossTenant(ctx context.Context) context.Context {
	if without, ok := ctx.Value(unbindKey{}).(func(context.Context, ...string) context.Context); ok {
		ctx = without(ctx, LogField)
	}
	return context.WithValue(context.WithValue(ctx, idKey{}, ""), crossKey{}, true)
}

// CrossTenant reports whether this context is marked as deliberately unscoped.
func CrossTenant(ctx context.Context) bool {
	marked, _ := ctx.Value(crossKey{}).(bool)
	return marked
}
