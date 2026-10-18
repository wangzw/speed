package impersonation

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/routefixture"
	"github.com/vislake/speed/pkg/internal/routecontract"
)

// TestTheStartDeclaresTheUnseasonedRefusal holds the fragment's 403 of the start
// operation. The design gives an actor with no seasoned non-SMS factor its own
// 403 rather than the step-up 401, so the declared operation a generated client
// is built from carries it.
func TestTheStartDeclaresTheUnseasonedRefusal(t *testing.T) {
	var doc struct {
		Paths map[string]map[string]struct {
			Responses map[string]struct {
				Description string `json:"description"`
			} `json:"responses"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(impersonationFragment, &doc); err != nil {
		t.Fatalf("the fragment is not JSON: %v", err)
	}
	op, ok := doc.Paths["/api/v1/auth/impersonation"]["post"]
	if !ok {
		t.Fatal("the fragment declares no POST /api/v1/auth/impersonation")
	}
	refusal, ok := op.Responses["403"]
	if !ok {
		t.Fatal("the start operation declares no 403")
	}
	if !strings.Contains(refusal.Description, "factor-not-seasoned") {
		t.Errorf("the 403 reads %q, want it to name factor-not-seasoned", refusal.Description)
	}
}

// TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters holds the one thing
// the OpenAPI fragment and the registrations have to agree on: the fragment is
// the only source a generated client is built from, and the design leaves the
// two free to drift with no failure signal at run time. The check drives the
// module's own Init, through the real route-mounting module, and compares what
// it registered there with what it declared.
func TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters(t *testing.T) {
	stubs, config := routefixture.Assembly(t, Module())
	stubs = append(stubs, routefixture.Seams(
		(*authn.Sessions)(nil),
		(*authn.Accounts)(nil),
		(*authn.Memberships)(nil),
		(*authn.MFA)(nil),
		(*audit.Audit)(nil),
		(*authn.ScopeGuard)(nil),
	)...)
	routecontract.Check(t, routecontract.Case{
		Module: Module(),
		Stubs:  stubs,
		Config: config,
	})
}
