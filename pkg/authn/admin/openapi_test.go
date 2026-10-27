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

// TestTheResetsDeclareTheUnseasonedRefusal holds the fragment's 403 of the two
// reset operations. The design gives an administrator with no seasoned non-SMS
// factor its own 403 rather than the step-up 401, so the declared operations a
// generated client is built from carry it.
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

// TestTheAccountCreateDeclaresBothConflictCauses holds the fragment's 409 of
// POST /auth/accounts. The operation conflicts for two reasons — the identifier
// is already held, or a password account has no username or email address to
// log in with — and a document carries one response per status, so the single
// declaration names both. The second cause is the login-identifier-required
// refusal a generated client would otherwise never learn from the contract.
func TestTheAccountCreateDeclaresBothConflictCauses(t *testing.T) {
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
	for _, cause := range []struct {
		name string
		text string
	}{
		{"the identifier is already held", "already exists"},
		{"the password account has no login name", "no username or email address"},
		{"the login-identifier-required code", "login-identifier-required"},
	} {
		if !strings.Contains(conflict.Description, cause.text) {
			t.Errorf("the 409 reads %q, want it to name %s (%q)", conflict.Description, cause.name, cause.text)
		}
	}
}

// TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters holds the one thing
// the OpenAPI fragment and the registrations have to agree on: the fragment is
// the only source a generated client is built from, and the design leaves the
// two free to drift with no failure signal at run time. The check drives the
// module's own Init, through the real route-mounting module, and compares what
// it registered there with what it declared. Given the accepted sets of the
// three paged routes, it also holds each one's declared query parameters to what
// its handler reads.
//
// The fragment declares the whole management surface, and Init mounts one group
// of it per capability it takes up there, so the assembly carries every group's
// capability: with one absent the module mounts less than it declares, and that
// is a legal assembly rather than a disagreement.
func TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters(t *testing.T) {
	stubs, config := routefixture.Assembly(t, Module())
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
			{Method: "GET", Path: "/api/v1/tenants/:tenant/members"}:               {"limit": true, "after": true},
			{Method: "GET", Path: "/api/v1/system/tenants/:tenant/impersonations"}: {"limit": true, "after": true},
		},
	})
}
