// Package settings_test holds the checks that need a second module's exported
// surface, which this package's own code may not import.
package settings_test

import (
	"strconv"
	"testing"

	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/tenant"
)

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
// through both statements and asserts each lands where the rule says.
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

func acceptsInSettings(id string) (accepted bool) {
	defer func() {
		if recover() != nil {
			accepted = false
		}
	}()
	settings.ForTenant(id)
	return true
}
