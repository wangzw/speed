package login

import (
	"net/http"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// A first login that would open an account stops at the username step. These
// cases drive the engine's part of the pending signup: the callback lands the
// user agent on the return target with the flow left at signup, GET
// flows/current reads the username challenge, and the answer opens the account
// and carries on to the policy.

// socialProofMethod is a redirect method whose callback proves an external
// identity with no binding, so a first login of it resolves to a pending signup
// once signup is open.
func socialProofMethod() *redirectMethod {
	m := &redirectMethod{}
	m.answer = func(authn.FlowState, authn.StepInput) (authn.Step, error) {
		return authn.StepProven(authn.Proof{
			External: &authn.External{Provider: "google", Subject: "g1", DisplayName: "Ann"},
			Methods:  []authn.Method{authn.MethodOIDC},
		}), nil
	}
	return m
}

// checkSignupReply checks the flow reply of a flow parked at the username step,
// so that every case reads the same challenge shape.
func checkSignupReply(t *testing.T, body string) {
	t.Helper()
	reply := decode(t, body)
	if reply["state"] != "signup" {
		t.Errorf("state = %v, want signup", reply["state"])
	}
	next, ok := reply["next"].(map[string]any)
	if !ok || next["kind"] != "challenge" {
		t.Fatalf("next = %v, want a challenge", reply["next"])
	}
	fields, ok := next["fields"].([]any)
	if !ok || len(fields) != 1 {
		t.Fatalf("fields = %v, want the one username field", next["fields"])
	}
	field, ok := fields[0].(map[string]any)
	if !ok || field["name"] != "username" || field["kind"] != "text" || field["required"] != true {
		t.Errorf("field = %v, want a required text username", fields[0])
	}
}

// TestACallbackThatWouldOpenAnAccountStopsAtTheUsernameStep holds that a
// callback which resolves to a first-login account is landed on the return
// target with nothing written, and that the resumable flow reads back the
// username challenge.
func TestACallbackThatWouldOpenAnAccountStopsAtTheUsernameStep(t *testing.T) {
	r := newRigMethod(t, socialProofMethod())
	r.withOpenSignup()
	cookie := startRedirect(t, r, `,"return_to":"https://app.example.org/after"`)

	cb := r.send(http.MethodGet, "/api/v1/auth/callback/password", "", "Cookie", cookie)
	if cb.Code != http.StatusSeeOther {
		t.Fatalf("the callback answered %d, want 303: %s", cb.Code, cb.Body.String())
	}
	if loc := cb.Header().Get("Location"); loc != "https://app.example.org/after" {
		t.Errorf("the callback landed on %q, want the flow's return_to", loc)
	}
	if len(r.admin.created) != 0 || len(r.admin.links) != 0 {
		t.Fatalf("the callback wrote %d accounts and %d bindings before the username, want none",
			len(r.admin.created), len(r.admin.links))
	}

	current := r.send(http.MethodGet, "/api/v1/auth/flows/current", "", "Cookie", cookie)
	if current.Code != http.StatusOK {
		t.Fatalf("GET flows/current after the callback answered %d: %s", current.Code, current.Body.String())
	}
	checkSignupReply(t, current.Body.String())
}

// TestTheUsernameOpensTheAccountAndThePolicyRuns holds that a legal username
// opens the pending account with that name and the flow goes on to the
// multi-factor policy rather than straight to ready.
func TestTheUsernameOpensTheAccountAndThePolicyRuns(t *testing.T) {
	r := newRigMethod(t, socialProofMethod())
	r.withOpenSignup()
	r.mfa.outcome = authn.MFAFactor
	r.mfa.offered = []authn.FactorRecord{{ID: "f1", Kind: authn.FactorTOTP}}
	cookie := startRedirect(t, r, "")

	cb := r.send(http.MethodGet, "/api/v1/auth/callback/password", "", "Cookie", cookie)
	if cb.Code != http.StatusSeeOther {
		t.Fatalf("the callback answered %d, want 303: %s", cb.Code, cb.Body.String())
	}

	answer := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		`{"seq":1,"step":"answer","input":{"username":"newperson"}}`, "Cookie", cookie)
	if answer.Code != http.StatusOK {
		t.Fatalf("the username answer answered %d: %s", answer.Code, answer.Body.String())
	}
	if len(r.admin.created) != 1 {
		t.Fatalf("%d accounts created, want one", len(r.admin.created))
	}
	if got := r.admin.created[0].Username; got != "newperson" {
		t.Errorf("the account was opened with %q, want the collected username", got)
	}
	reply := decode(t, answer.Body.String())
	if reply["state"] != "second" {
		t.Errorf("state after the username = %v, want second: the policy runs after the account opens", reply["state"])
	}
	next, _ := reply["next"].(map[string]any)
	if next["kind"] != "factor" {
		t.Errorf("next = %v, want the second-factor step", reply["next"])
	}
}

// TestARejectedUsernameKeepsTheFlowAtTheUsernameStep holds that a username which
// does not normalize is 400 and one another account holds is 409, that neither
// opens an account, and that the flow stays at signup so a legal name can follow
// at the same step number: a refusal counts the answer without moving the
// position.
func TestARejectedUsernameKeepsTheFlowAtTheUsernameStep(t *testing.T) {
	r := newRigMethod(t, socialProofMethod())
	r.withOpenSignup()
	r.accounts.lookupErr = map[string]error{
		string(authn.IdentifierUsername) + "\x00bad!": authn.UsernameShape(authn.ErrInvalidIdentifier),
	}
	r.accounts.lookup[string(authn.IdentifierUsername)+"\x00taken"] = "someone"
	cookie := startRedirect(t, r, "")

	cb := r.send(http.MethodGet, "/api/v1/auth/callback/password", "", "Cookie", cookie)
	if cb.Code != http.StatusSeeOther {
		t.Fatalf("the callback answered %d, want 303: %s", cb.Code, cb.Body.String())
	}

	refused := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		`{"seq":1,"step":"answer","input":{"username":"bad!"}}`, "Cookie", cookie)
	if refused.Code != http.StatusBadRequest || decode(t, refused.Body.String())["type"] != "urn:speed:authn:username-invalid" {
		t.Fatalf("a username that does not normalize answered %d %s, want 400 username-invalid", refused.Code, refused.Body.String())
	}

	taken := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		`{"seq":1,"step":"answer","input":{"username":"taken"}}`, "Cookie", cookie)
	if taken.Code != http.StatusConflict || decode(t, taken.Body.String())["type"] != "urn:speed:authn:username-taken" {
		t.Fatalf("a taken username answered %d %s, want 409 username-taken", taken.Code, taken.Body.String())
	}
	if len(r.admin.created) != 0 {
		t.Fatalf("%d accounts created by rejected usernames, want none", len(r.admin.created))
	}

	current := r.send(http.MethodGet, "/api/v1/auth/flows/current", "", "Cookie", cookie)
	if current.Code != http.StatusOK {
		t.Fatalf("GET flows/current after the rejections answered %d: %s", current.Code, current.Body.String())
	}
	checkSignupReply(t, current.Body.String())
	if seq, _ := decode(t, current.Body.String())["seq"].(float64); seq != 1 {
		t.Errorf("seq after two rejections = %v, want the position held at 1", seq)
	}

	// The flow is still retryable: the legal name at the same step number opens
	// the account.
	ok := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		`{"seq":1,"step":"answer","input":{"username":"newperson"}}`, "Cookie", cookie)
	if ok.Code != http.StatusOK {
		t.Fatalf("the retried username answered %d: %s", ok.Code, ok.Body.String())
	}
	if len(r.admin.created) != 1 || r.admin.created[0].Username != "newperson" {
		t.Errorf("created = %+v, want the one account with the retried name", r.admin.created)
	}
}
