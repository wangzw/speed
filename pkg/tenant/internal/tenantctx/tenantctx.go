// Package tenantctx carries the tenant a context belongs to.
//
// It is internal so that the only writer of a tenant identifier is this
// capability directory. The resolving layer puts one in as it admits a request,
// and nothing else can: a chain layer able to write an identifier could rewrite
// the scope every statement below it is filtered by, and the point of resolving
// a tenant once per request is that it happens once.
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

// idKey and crossKey are distinct types so that the two facts cannot be read
// as one another even if a future value has the same shape.
type (
	idKey    struct{}
	crossKey struct{}
)

// WithID returns a context carrying this tenant identifier and no cross-tenant
// mark. It is called by the resolving layer, and by nothing else.
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
