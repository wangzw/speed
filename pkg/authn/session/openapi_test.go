package session

import (
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/routefixture"
	"github.com/vislake/speed/pkg/internal/routecontract"
)

// TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters holds the one thing
// the OpenAPI fragment and the registrations have to agree on: the fragment is
// the only source a generated client is built from, and the design leaves the
// two free to drift with no failure signal at run time. The check drives the
// module's own Init, through the real route-mounting module, and compares what
// it registered there with what it declared.
//
// The database is real, because Init takes its handle up, and the account read
// is a stand-in, because Init resolves and stores it and no request is served.
func TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters(t *testing.T) {
	stubs, config := routefixture.Assembly(t, Module())
	stubs = append(stubs, routefixture.Seams((*authn.Accounts)(nil))...)
	routecontract.Check(t, routecontract.Case{
		Module: Module(),
		Stubs:  stubs,
		Config: config,
	})
}
