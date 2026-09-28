package tenant

import (
	"context"
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/tenant/internal/tenantctx"
)

// TestFromContextDoesNotFallBack records the half of the property a test can
// reach. The other half — that nothing outside this capability directory can
// write an identifier — is enforced by the compiler, because the writing
// in an internal package; a test cannot observe its absence.
func TestFromContextDoesNotFallBack(t *testing.T) {
	if id, ok := FromContext(context.Background()); ok {
		t.Fatalf("a context with no tenant reported %q", id)
	}
}

// TestCrossTenantIsNotATenant records that crossing tenants and belonging to
// one are different facts. A context that crosses is not a context that
// belongs to some tenant, and reading it as one would make every unscoped
// statement look scoped.
func TestCrossTenantIsNotATenant(t *testing.T) {
	if id, ok := FromContext(CrossTenant(context.Background())); ok {
		t.Fatalf("a cross-tenant context reported tenant %q", id)
	}
}

// TestCrossTenantHandsOverTheIdentifier is the same fact on a context that did
// belong to a tenant: marking it to cross gives that tenant up. A reader of the
// marked context — the audit journal filling its tenant column is one — would
// otherwise file an unscoped statement under the tenant of the request that
// happened to issue it.
func TestCrossTenantHandsOverTheIdentifier(t *testing.T) {
	scoped := tenantctx.WithID(context.Background(), "acme")
	if id, ok := FromContext(CrossTenant(scoped)); ok {
		t.Fatalf("a context marked to cross still reported tenant %q", id)
	}
	if id, ok := FromContext(scoped); !ok || id != "acme" {
		t.Fatalf("marking a derived context changed its parent: got %q, %v", id, ok)
	}
}

// TestAnIdentifierOutsideTheDomainIsNotBound records the guard the convention
// carries with it. The empty identifier is what an unfilled tenant column
// holds, and binding it would write a row that belongs to no tenant.
func TestAnIdentifierOutsideTheDomainIsNotBound(t *testing.T) {
	for _, tc := range []struct {
		id   ID
		want error
	}{
		{"", ErrNoTenantInContext},
		{"acme corp", ErrInvalidTenant},
		{"acme\ncorp", ErrInvalidTenant},
	} {
		v, err := tc.id.Value()
		if !errors.Is(err, tc.want) {
			t.Errorf("binding %q gave %v, want %v", tc.id, err, tc.want)
		}
		// The value still comes back beside the refusal, for the callers
		// that read it and drop the error.
		if v != string(tc.id) {
			t.Errorf("binding %q returned the value %#v beside its refusal, want the string", tc.id, v)
		}
	}
	if v, err := ID("acme").Value(); err != nil || v != "acme" {
		t.Fatalf("binding an identifier in the domain gave %#v, %v", v, err)
	}
}

// TestStatusZeroIsRefused records which direction the zero value falls. A row
// whose status was never written, and a row holding a value this build does not
// know, both have to read as not served.
func TestStatusZeroIsRefused(t *testing.T) {
	var unset Status
	if unset != StatusDisabled {
		t.Fatalf("the zero status is %v, want the disabled one", unset)
	}
	if unknown := Status(99); unknown.String() != "disabled" {
		t.Fatalf("an unknown status reads as %q, want it to read as disabled", unknown)
	}
}
