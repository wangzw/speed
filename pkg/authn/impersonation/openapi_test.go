package impersonation

import (
	"encoding/json"
	"reflect"
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
	op, ok := doc.Paths["/auth/impersonation"]["post"]
	if !ok {
		t.Fatal("the fragment declares no POST /auth/impersonation")
	}
	refusal, ok := op.Responses["403"]
	if !ok {
		t.Fatal("the start operation declares no 403")
	}
	if !strings.Contains(refusal.Description, "factor-not-seasoned") {
		t.Errorf("the 403 reads %q, want it to name factor-not-seasoned", refusal.Description)
	}
}

// TestTheStartRequestBodyCarriesTheDeclaredDescription holds the schema
// description of the start body. It lives on the input's blank field, which
// the entry point does not bind and the generator copies onto the inline body
// schema. Losing it would drop the only place a generated client learns that
// tenant and reason are refused rather than merely optional.
func TestTheStartRequestBodyCarriesTheDeclaredDescription(t *testing.T) {
	var doc struct {
		Paths map[string]map[string]struct {
			RequestBody struct {
				Content map[string]struct {
					Schema struct {
						Description string `json:"description"`
					} `json:"schema"`
				} `json:"content"`
			} `json:"requestBody"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(impersonationFragment, &doc); err != nil {
		t.Fatalf("the fragment is not JSON: %v", err)
	}
	op, ok := doc.Paths["/auth/impersonation"]["post"]
	if !ok {
		t.Fatal("the fragment declares no POST /auth/impersonation")
	}
	body, ok := op.RequestBody.Content["application/json"]
	if !ok {
		t.Fatal("the start operation declares no application/json request body")
	}
	field, ok := reflect.TypeOf(startInput{}).FieldByName("_")
	if !ok {
		t.Fatal("startInput declares no schema description on its blank field")
	}
	want := field.Tag.Get("description")
	if want == "" {
		t.Fatal("startInput's schema description is empty")
	}
	if body.Schema.Description != want {
		t.Errorf("the start body schema description reads %q, want %q", body.Schema.Description, want)
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
