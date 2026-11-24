package admin

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/routefixture"
	"github.com/vislake/speed/pkg/internal/routecontract"
)

// TestTheResetsDeclareTheUnseasonedRefusal holds the fragment's 403 of the two reset operations.
func TestTheResetsDeclareTheUnseasonedRefusal(t *testing.T) {
	var doc struct {
		Paths map[string]map[string]struct {
			Responses map[string]struct {
				Description string `json:"description"`
			} `json:"responses"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(adminFragment, &doc); err != nil {
		t.Fatalf("the fragment is not JSON: %v", err)
	}
	for _, path := range []string{
		"/auth/accounts/{id}/password",
		"/auth/accounts/{id}/mfa-reset",
	} {
		op, ok := doc.Paths[path]["post"]
		if !ok {
			t.Fatalf("the fragment declares no POST %s", path)
		}
		refusal, ok := op.Responses["403"]
		if !ok {
			t.Fatalf("POST %s declares no 403", path)
		}
		if !strings.Contains(refusal.Description, "factor-not-seasoned") {
			t.Errorf("the 403 of %s reads %q, want it to name factor-not-seasoned", path, refusal.Description)
		}
	}
}

// TestTheAccountCreateDeclaresItsIdentifierConflict holds the fragment's 409 of POST /auth/accounts: the identifier is already held.
func TestTheAccountCreateDeclaresItsIdentifierConflict(t *testing.T) {
	var doc struct {
		Paths map[string]map[string]struct {
			Responses map[string]struct {
				Description string `json:"description"`
			} `json:"responses"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(adminFragment, &doc); err != nil {
		t.Fatalf("the fragment is not JSON: %v", err)
	}
	op, ok := doc.Paths["/auth/accounts"]["post"]
	if !ok {
		t.Fatal("the fragment declares no POST /auth/accounts")
	}
	conflict, ok := op.Responses["409"]
	if !ok {
		t.Fatal("POST /auth/accounts declares no 409")
	}
	if !strings.Contains(conflict.Description, "already exists") {
		t.Errorf("the 409 reads %q, want it to name the identifier that is already held", conflict.Description)
	}
}

// TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters checks that the OpenAPI fragment and the registered routes agree.
func TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters(t *testing.T) {
	stubs, config := routefixture.Assembly(t, Module())
	config = routecontract.Config(config, map[string]any{
		"authn": map[string]any{
			"admin": map[string]any{"sso": map[string]any{"enabled": true}},
		},
	})
	stubs = append(stubs, routefixture.Seams(
		(*authn.Accounts)(nil),
		(*authn.AccountAdmin)(nil),
		(*authn.Memberships)(nil),
		(*authn.SignupVerification)(nil),
		(*audit.Audit)(nil),
		(*authn.Decider)(nil),
		(*authn.Passwords)(nil),
		(*authn.Sessions)(nil),
		(*authn.MFA)(nil),
		(*authn.Tokens)(nil),
		(*authn.SSOProviders)(nil),
		(*authn.Impersonation)(nil),
		(*authn.SSOMetadataRefresher)(nil),
	)...)
	routecontract.Check(t, routecontract.Case{
		Module: Module(),
		Stubs:  stubs,
		Config: config,
		QueryParams: map[routecontract.Route]map[string]bool{
			{Method: "GET", Path: "/api/v1/system/auth/accounts"}:                  accountListParams,
			{Method: "GET", Path: "/api/v1/tenants/:tenant/members"}:               {"query": true, "role": true, "limit": true, "after": true},
			{Method: "GET", Path: "/api/v1/system/tenants/:tenant/impersonations"}: {"limit": true, "after": true},
		},
	})
}
