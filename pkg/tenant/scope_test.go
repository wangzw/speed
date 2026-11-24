package tenant

import (
	"context"
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/tenant/internal/tenantctx"
)

// TestFromContextDoesNotFallBack checks that a context with no tenant reports
// none rather than a default.
func TestFromContextDoesNotFallBack(t *testing.T) {
	if id, ok := FromContext(context.Background()); ok {
		t.Fatalf("a context with no tenant reported %q", id)
	}
}

// TestCrossTenantIsNotATenant checks that a context marked to cross reports no
// tenant.
func TestCrossTenantIsNotATenant(t *testing.T) {
	if id, ok := FromContext(CrossTenant(context.Background())); ok {
		t.Fatalf("a cross-tenant context reported tenant %q", id)
	}
}

// TestCrossTenantHandsOverTheIdentifier checks that marking a scoped context to
// cross gives up its tenant without changing the parent.
func TestCrossTenantHandsOverTheIdentifier(t *testing.T) {
	scoped := tenantctx.WithID(context.Background(), "acme")
	if id, ok := FromContext(CrossTenant(scoped)); ok {
		t.Fatalf("a context marked to cross still reported tenant %q", id)
	}
	if id, ok := FromContext(scoped); !ok || id != "acme" {
		t.Fatalf("marking a derived context changed its parent: got %q, %v", id, ok)
	}
}

// TestAnIdentifierOutsideTheDomainIsNotBound checks that Value refuses an
// identifier outside the domain and still returns the string.
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
		if v != string(tc.id) {
			t.Errorf("binding %q returned the value %#v beside its refusal, want the string", tc.id, v)
		}
	}
	if v, err := ID("acme").Value(); err != nil || v != "acme" {
		t.Fatalf("binding an identifier in the domain gave %#v, %v", v, err)
	}
}

// TestStatusZeroIsRefused checks that the zero status and an unknown status both
// read as disabled and not served.
func TestStatusZeroIsRefused(t *testing.T) {
	var unset Status
	if unset != StatusDisabled {
		t.Fatalf("the zero status is %v, want the disabled one", unset)
	}
	if unknown := Status(99); unknown.String() != "disabled" {
		t.Fatalf("an unknown status reads as %q, want it to read as disabled", unknown)
	}
}
