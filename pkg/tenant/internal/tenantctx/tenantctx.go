// Package tenantctx carries the tenant a context belongs to, and the mark that a context
// deliberately crosses tenants; the two exclude each other. It is internal so that the only writers
// of a tenant identifier are in this capability directory.
package tenantctx

import (
	"context"
	"log/slog"
)

// LogField is the name the writers bind the identifier to on the logger a
// context carries.
const LogField = "tenant_id"

type (
	idKey     struct{}
	crossKey  struct{}
	unbindKey struct{}
)

// WithID returns a context carrying this tenant identifier and no cross-tenant
// mark. The writers reach it through Bind; it leaves the logger alone. The empty
// identifier states that the context belongs to no tenant and does not cross.
func WithID(ctx context.Context, id string) context.Context {
	return context.WithValue(context.WithValue(ctx, crossKey{}, false), idKey{}, id)
}

// Logger is how this package reaches the logger a context carries: the logging module's own
// functions for binding a field and taking one off. The writers hand them in, because this package
// is imported by the root package, which imports no module of this repository.
type Logger struct {
	With    func(ctx context.Context, args ...any) context.Context
	Without func(ctx context.Context, keys ...string) context.Context
}

// Bind writes the identifier into the context, as WithID does, and brings the context's logger in
// line: the identifier bound under LogField, or, for the empty identifier, the field taken off. It
// keeps the way to take the field off, so a context marked to cross later gives it up too.
func Bind(ctx context.Context, id string, logger Logger) context.Context {
	ctx = WithID(ctx, id)
	if id == "" {
		ctx = logger.Without(ctx, LogField)
	} else {
		ctx = logger.With(ctx, slog.String(LogField, id))
	}
	return context.WithValue(ctx, unbindKey{}, logger.Without)
}

// ID returns the tenant identifier this context carries. An identifier is never
// empty, so a stored empty string reads as absent, which is also how a context
// marked to cross reads.
func ID(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(idKey{}).(string)
	if !ok || id == "" {
		return "", false
	}
	return id, true
}

// WithCrossTenant returns a context marked as deliberately unscoped, carrying
// no tenant identifier, with the identifier a writer bound taken off its
// logger.
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
