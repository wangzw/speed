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
// tenant, and put into a context a tenant the roster does not carry or does not
// serve. The writing side lives in an internal package, so the compiler
// enforces what a comment otherwise only asks for, and its two writers both
// read the roster first: the resolving layer as it admits a request, and
// Catalog.Enter for a path with no request behind it.
//
// Nor does this fall back to anything. A default would read a request that
// carries no tenant as one that carries a particular one, and the two are the
// opposite of each other downstream.
//
// Downstream therefore has to handle the false case itself. It holds on a
// listening endpoint this module does not guard, in a process that did not take
// this capability in, on a path with no request behind it that has not entered
// a tenant, and in a context marked to cross tenants.
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
// The context it returns belongs to no tenant. Marking a context that carried
// one gives that tenant up: FromContext reports none in the result, and the
// tenant field the resolving layer or Catalog.Enter bound on the context's
// logger is taken off, so a record written across tenants names none. A
// crossing statement belongs to no tenant, and a reader that took the marked
// context for the one it was derived from — the audit journal filling its
// tenant column is one — would file an unscoped statement under a scope it
// does not have.
//
// What crossing lifts is the condition and the fill, not the column. A row
// written across tenants still has to carry a tenant, so an insert under this
// mark whose row names none, and a write under it that puts an empty value, a
// null or a value outside the identifier's domain into the tenant column, is
// refused: such a row belongs to no tenant, and no scoped statement could reach
// it again.
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
