// Package settings_test holds the checks that need a second module's exported
// surface. It is the external test package rather than the internal one because
// the identifier's domain belongs to the tenant module: a consumer's test
// package may point at a provider's interface package, while this package's own
// code may not, and the compilation boundary is what keeps the two apart.
package settings_test

import (
	"strconv"
	"testing"

	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/tenant"
)

// identifierShapes is the one table the tenant identifier's value domain is
// driven through: the tenant module's exported predicate, and this module's
// layer constructor, which restates the domain and panics rather than returning
// an error.
//
// The domain is non-empty, and free of blanks and control characters anywhere
// in it. The accepted column is that rule, spelled out per shape; the colon is
// in the domain on purpose, because a consumer that builds a key out of an
// identifier escapes it rather than refusing it.
//
// The two statements are held in step here rather than by one implementation:
// settings declares no dependency on the tenant module, so outside a test
// package it cannot reach the predicate. A divergence is not harmless — the
// rate-limit layer hands the identifier it read out of the request context to
// settings.ForTenant, so a settings-side tightening would panic on every
// request of a tenant the roster accepts.
var identifierShapes = []struct {
	value    string
	accepted bool
}{
	{value: "acme", accepted: true},
	{value: "a", accepted: true},
	{value: "tenant-1", accepted: true},
	{value: "0f9a2b7c", accepted: true},
	{value: "a:b", accepted: true},
	{value: "", accepted: false},
	{value: " ", accepted: false},
	{value: "ac me", accepted: false},
	{value: "ac\tme", accepted: false},
	{value: "ac\nme", accepted: false},
	{value: "ac\rme", accepted: false},
	{value: "\u00a0", accepted: false},
	{value: "ac\u0085me", accepted: false},
	{value: "ac\x00me", accepted: false},
}

// TestForTenantAgreesWithTheTenantIdentifierDomain drives the shared table
// through both statements of the domain and asserts each one lands where the
// rule says. The expected column is what keeps a table of agreeing wrong
// answers from passing, and every shape is driven rather than sampled, so a
// divergence in either statement fails on the shape that carries it.
func TestForTenantAgreesWithTheTenantIdentifierDomain(t *testing.T) {
	for _, shape := range identifierShapes {
		t.Run(strconv.Quote(shape.value), func(t *testing.T) {
			if got := tenant.IsIdentifier(shape.value); got != shape.accepted {
				t.Errorf("tenant.IsIdentifier(%q) = %v, want %v",
					shape.value, got, shape.accepted)
			}
			if got := acceptsInSettings(shape.value); got != shape.accepted {
				t.Errorf("settings.ForTenant accepted %q = %v, want %v",
					shape.value, got, shape.accepted)
			}
		})
	}
}

// acceptsInSettings reports whether the settings module's tenant-layer
// constructor takes the identifier. That constructor panics on one outside the
// domain, and the panic is its refusal.
func acceptsInSettings(id string) (accepted bool) {
	defer func() {
		if recover() != nil {
			accepted = false
		}
	}()
	settings.ForTenant(id)
	return true
}
