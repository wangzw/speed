package saml

import (
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/routefixture"
	"github.com/vislake/speed/pkg/internal/routecontract"
)

// TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters checks that the declared operations match the routes the module registers.
func TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters(t *testing.T) {
	stubs, config := routefixture.Assembly(t, Module())
	config = routecontract.Config(config, map[string]any{
		"authn": map[string]any{"sso": map[string]any{"saml": map[string]any{"enabled": true}}},
	})
	stubs = append(stubs, routefixture.Seams(
		(*authn.SSOProviders)(nil),
		(*authn.Accounts)(nil),
	)...)
	routecontract.Check(t, routecontract.Case{
		Module: Module(),
		Stubs:  stubs,
		Config: config,
	})
}
