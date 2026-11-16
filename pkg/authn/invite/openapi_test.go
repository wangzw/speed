package invite

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/routefixture"
	"github.com/vislake/speed/pkg/internal/openapigen"
	"github.com/vislake/speed/pkg/internal/routecontract"
	"github.com/vislake/speed/pkg/kv"
)

// TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters holds the one thing
// the OpenAPI fragment and the registrations have to agree on: the fragment is
// the only source a generated client is built from, and the design leaves the
// two free to drift with no failure signal at run time. The check drives the
// module's own Init, through the real route-mounting module, and compares what
// it registered there with what it declared.
func TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters(t *testing.T) {
	stubs, config := routefixture.Assembly(t, Module())
	stubs = append(stubs, routefixture.Seams(
		(*authn.Accounts)(nil),
		(*authn.SignupVerification)(nil),
		(*authn.Memberships)(nil),
		(*authn.TenantDescriptionResolver)(nil),
		(*audit.Audit)(nil),
		(*kv.KV)(nil),
	)...)
	routecontract.Check(t, routecontract.Case{
		Module: Module(),
		Stubs:  stubs,
		Config: config,
	})
}

// TestTheAcceptOperationPublishesEveryConflictProblemCode holds the public
// fragment's 409 answer to every problem code the acceptance can answer. The
// fragment carries one response per status, so a second 409 declaration
// replaces the first and the codes only the first names never reach a client.
func TestTheAcceptOperationPublishesEveryConflictProblemCode(t *testing.T) {
	generated, err := openapigen.Generate(fragmentTitle, fragmentVersion, publicOperations)
	if err != nil {
		t.Fatalf("generating %s: %v", publicFragmentPath, err)
	}
	var document struct {
		Paths map[string]map[string]struct {
			Responses map[string]struct {
				Description string `json:"description"`
			} `json:"responses"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(generated, &document); err != nil {
		t.Fatalf("parsing %s: %v", publicFragmentPath, err)
	}
	operation, ok := document.Paths["/auth/invitations/{token}"]["post"]
	if !ok {
		t.Fatalf("%s does not declare POST /auth/invitations/{token}", publicFragmentPath)
	}
	conflict, ok := operation.Responses["409"]
	if !ok {
		t.Fatalf("POST /auth/invitations/{token} declares no 409 answer")
	}
	for _, code := range []string{"invitation-terminal", "invitation-tenant-inactive", "membership-inactive"} {
		if !strings.Contains(conflict.Description, code) {
			t.Errorf("the 409 answer does not name %s: %s", code, conflict.Description)
		}
	}
}
