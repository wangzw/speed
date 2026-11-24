package token

import (
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/routefixture"
	"github.com/vislake/speed/pkg/internal/routecontract"
)

// TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters checks the OpenAPI
// fragment and the registrations name the same operations.
func TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters(t *testing.T) {
	stubs, config := routefixture.Assembly(t, Module())
	stubs = append(stubs, routefixture.Seams(
		(*authn.Accounts)(nil),
		(*authn.Sessions)(nil),
		(*authn.Passwords)(nil),
		(*authn.Throttle)(nil),
	)...)
	routecontract.Check(t, routecontract.Case{
		Module: Module(),
		Stubs:  stubs,
		Config: routecontract.Config(config, routefixture.SigningKey(t)),
	})
}
