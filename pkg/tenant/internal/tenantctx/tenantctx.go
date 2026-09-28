// Package tenantctx carries the tenant a context belongs to.
//
// It is internal so that the only writers of a tenant identifier are in this
// capability directory, and both read the roster before they write: the
// resolving layer as it admits a request, and the roster's entries as a path
// with no request behind it enters a tenant. Nothing else can. A chain layer able to
// write an identifier could rewrite the scope every statement below it is
// filtered by, and a writer that did not read the roster could put a tenant
// into a context that the roster does not carry or does not serve.
//
// The cross-tenant mark is the exception, and it is here for the same reason
// rather than in spite of it. Crossing tenants is meant to be written at the
// call site — tenant.CrossTenant is the public way in — so the mark is a fact
// the caller states, not one this package decides about.
//
// The two facts exclude each other. A context carries a tenant or crosses
// tenants, never both: marking a context to cross gives up the tenant it
// carried, and writing a tenant into a context lifts the mark it carried. The
// logger a context carries follows the same fact: the identifier is bound on
// it while the context belongs to that tenant, and taken off when the context
// stops belonging to it.
package tenantctx

import (
	"context"
	"log/slog"
)

// LogField is the name both writers bind the identifier to on the logger a
// context carries. It is a contract once a dashboard reads it, and the two
// writers have to agree on it, so it is fixed once, here.
const LogField = "tenant_id"

// idKey, crossKey and unbindKey are distinct types so that the facts cannot be
// read as one another even if a future value has the same shape.
type (
	idKey     struct{}
	crossKey  struct{}
	unbindKey struct{}
)

// WithID returns a context carrying this tenant identifier and no cross-tenant
// mark. The writers reach it through Bind; it leaves the logger alone.
//
// The empty identifier is how a writer states that the context belongs to no
// tenant and does not cross either: ID reads it as absent, and the mark is
// lifted all the same.
func WithID(ctx context.Context, id string) context.Context {
	return context.WithValue(context.WithValue(ctx, crossKey{}, false), idKey{}, id)
}

// Logger is how this package reaches the logger a context carries: the logging
// module's own functions for binding a field onto it and for taking one off.
// They are handed in by the writers, which import the logging module, because
// this package is imported by the root package, which imports no module of
// this repository — the logging module arrives with the configuration's whole
// dependency set, and a package that defines a model would carry all of it.
type Logger struct {
	With    func(ctx context.Context, args ...any) context.Context
	Without func(ctx context.Context, keys ...string) context.Context
}

// Bind is what a writer does: it writes the identifier into the context, as
// WithID does, and brings the context's logger in line with it — the
// identifier bound under LogField, replacing one bound before, or, for the
// empty identifier, the field taken off. It keeps the way to take the field
// off again beside it, so that a context marked to cross later gives up the
// field along with the tenant.
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
// empty, so a stored empty string reads as absent — which is also how a context
// marked to cross reads.
func ID(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(idKey{}).(string)
	if !ok || id == "" {
		return "", false
	}
	return id, true
}

// WithCrossTenant returns a context marked as deliberately unscoped, carrying
// no tenant identifier, and a logger that no longer carries the identifier a
// writer bound on it.
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
