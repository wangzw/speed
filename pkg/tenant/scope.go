package tenant

import (
	"context"

	"github.com/vislake/speed/pkg/tenant/internal/tenantctx"
)

// FromContext returns the tenant identifier this context carries. The second
// result is false when it carries none.
//
// There is no injection point beside this one, and that is the shape rather
// than an omission: exporting one would let any layer on the chain rewrite the
// tenant, which is the single decision this capability directory exists to
// make once. The writing side lives in an internal package, so the compiler
// enforces what a comment otherwise only asks for.
//
// Nor does this fall back to anything. A default would read a request that
// carries no tenant as one that carries a particular one, and the two are the
// opposite of each other downstream.
//
// Downstream therefore has to handle the false case itself. It is true on a
// listening endpoint this module does not guard, in a process that did not take
// this capability in, and on statements a caller builds without a request
// behind them.
func FromContext(ctx context.Context) (ID, bool) {
	id, ok := tenantctx.ID(ctx)
	if !ok {
		return "", false
	}
	return ID(id), true
}

// CrossTenant returns a context in which scoped statements are neither filtered
// by a tenant nor made to fill the tenant column.
//
// This is the only way across tenants. It is a context fact rather than a
// statement fact so that it can be established once at the mouth of a function
// and hold for everything that function goes on to issue, which is also what
// lets a reviewer see the intent by reading one line. A per-statement switch
// would have to be written at every site, and a missed one falls back to a
// refusal with nothing to point at.
//
// It is named rather than spelled as a boolean argument or a string key,
// because a name is what a reviewer recognises: the other two read like
// something else at the call site.
//
// Nothing here judges whether a given call should cross. The mark is a fact the
// caller states; whether it was right to state it is visible only in the code
// that did, and audit capture is another module's work.
func CrossTenant(ctx context.Context) context.Context {
	return tenantctx.WithCrossTenant(ctx)
}
