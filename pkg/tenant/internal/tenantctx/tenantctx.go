// Package tenantctx carries the tenant a context belongs to.
//
// It is internal so that the only writers of a tenant identifier are in this
// capability directory, and both read the roster before they write: the
// resolving layer as it admits a request, and the roster's Enter as a path with
// no request behind it enters a tenant. Nothing else can. A chain layer able to
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
// carried, and writing a tenant into a context lifts the mark it carried.
package tenantctx

import "context"

// LogField is the name both writers bind the identifier to on the logger a
// context carries. It is a contract once a dashboard reads it, and the two
// writers have to agree on it, so it is fixed once, here.
const LogField = "tenant_id"

// idKey and crossKey are distinct types so that the two facts cannot be read
// as one another even if a future value has the same shape.
type (
	idKey    struct{}
	crossKey struct{}
)

// WithID returns a context carrying this tenant identifier and no cross-tenant
// mark. It is called by the resolving layer and by the roster's Enter, and by
// nothing else.
//
// The empty identifier is how a writer states that the context belongs to no
// tenant and does not cross either: ID reads it as absent, and the mark is
// lifted all the same.
func WithID(ctx context.Context, id string) context.Context {
	return context.WithValue(context.WithValue(ctx, crossKey{}, false), idKey{}, id)
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
// no tenant identifier.
func WithCrossTenant(ctx context.Context) context.Context {
	return context.WithValue(context.WithValue(ctx, idKey{}, ""), crossKey{}, true)
}

// CrossTenant reports whether this context is marked as deliberately unscoped.
func CrossTenant(ctx context.Context) bool {
	marked, _ := ctx.Value(crossKey{}).(bool)
	return marked
}
