package login

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/routefixture"
	"github.com/vislake/speed/pkg/internal/routecontract"
	kvmemory "github.com/vislake/speed/pkg/kv/memory"
)

// TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters holds the one thing
// the OpenAPI fragment and the registrations have to agree on: the fragment is
// the only source a generated client is built from, and the design leaves the
// two free to drift with no failure signal at run time. The check drives the
// module's own Init, through the real route-mounting module, and compares what
// it registered there with what it declared.
//
// The key-value store is the real in-process one, because Init builds the flow
// store over it.
func TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters(t *testing.T) {
	stubs, config := routefixture.Assembly(t, Module())
	stubs = append(stubs, kvmemory.Module())
	stubs = append(stubs, routefixture.Seams(
		(*authn.Accounts)(nil),
		(*authn.AccountAdmin)(nil),
		(*authn.Signups)(nil),
		(*authn.SignupVerification)(nil),
		(*authn.Passwords)(nil),
		(*authn.Memberships)(nil),
		(*authn.Throttle)(nil),
		(*authn.Sessions)(nil),
		(*authn.MFA)(nil),
		(*authn.LoginMethod)(nil),
		(*authn.SMSCodes)(nil),
		(*authn.EmailCodeSender)(nil),
	)...)
	routecontract.Check(t, routecontract.Case{
		Module: Module(),
		Stubs:  stubs,
		Config: config,
	})
}

// TestTheSignedInStartDeclaresTheContactRequiredRefusal holds the 403 of the
// route that starts a flow on an identity already signed in: a `purpose=link`
// flow is refused when the caller's self-registration has not verified an email
// address, so the declared operation a generated client is built from names that
// cause beside the refusal it shares with the other flow routes.
func TestTheSignedInStartDeclaresTheContactRequiredRefusal(t *testing.T) {
	var doc struct {
		Paths map[string]map[string]struct {
			Responses map[string]struct {
				Description string `json:"description"`
			} `json:"responses"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(loginFragment, &doc); err != nil {
		t.Fatalf("the fragment is not JSON: %v", err)
	}
	op, ok := doc.Paths[pathFlowsAuth]["post"]
	if !ok {
		t.Fatalf("the fragment declares no POST %s", pathFlowsAuth)
	}
	refusal, ok := op.Responses["403"]
	if !ok {
		t.Fatalf("POST %s declares no 403", pathFlowsAuth)
	}
	if !strings.Contains(refusal.Description, "contact-required") {
		t.Errorf("the 403 reads %q, want it to name contact-required", refusal.Description)
	}
}
