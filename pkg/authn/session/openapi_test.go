package session

import (
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/routefixture"
	"github.com/vislake/speed/pkg/internal/routecontract"
)

// TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters holds that the OpenAPI
// fragment and the registered routes agree.
func TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters(t *testing.T) {
	stubs, config := routefixture.Assembly(t, Module())
	stubs = append(stubs, routefixture.Seams((*authn.Accounts)(nil))...)
	routecontract.Check(t, routecontract.Case{
		Module: Module(),
		Stubs:  stubs,
		Config: config,
	})
}
