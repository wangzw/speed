package login_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
)

// The pending signup: a first login of a social provider, an SMS code or an
// enterprise IdP parks the flow at a username step before any account row is
// written, and opens the account only when the username arrives.

// startSocialSignup begins a first social login over the stand-in provider and
// returns the start reply, whose state is the pending signup.
func (h *harness) startSocialSignup() reply {
	h.t.Helper()
	start := h.post("/api/v1/auth/flows", map[string]any{"method": "google"})
	if start.status != http.StatusOK {
		h.t.Fatalf("POST flows answered %d: %s", start.status, start.body)
	}
	return start
}

// signupStep reads the state, step number and next step of a flow reply.
func signupStep(t *testing.T, r reply) (string, int, map[string]any) {
	t.Helper()
	doc := r.json(t)
	state, _ := doc["state"].(string)
	seq, _ := doc["seq"].(float64)
	next, _ := doc["next"].(map[string]any)
	return state, int(seq), next
}

// answerUsername sends one username answer to the flow.
func (h *harness) answerUsername(seq int, username string) reply {
	h.t.Helper()
	return h.post("/api/v1/auth/flows/answer", map[string]any{
		"seq": seq, "step": "answer", "input": map[string]string{"username": username},
	})
}

// TestASocialFirstLoginStopsAtTheUsernameStepBeforeOpeningAnAccount holds the
// pending signup against the real account store: the login stops at the username
// step with nothing written, an unusable name is 400 and a name another account
// holds is 409 with the flow still retryable, and the legal name opens the
// account, binds the identity and finishes the login.
func TestASocialFirstLoginStopsAtTheUsernameStepBeforeOpeningAnAccount(t *testing.T) {
	assembleWith(t, []core.Module{socialModule()}, func(h *harness) {
		writeASetting(t, h.ctx, h.store, testSignupKey, "open")

		start := h.startSocialSignup()
		state, seq, next := signupStep(t, start)
		if state != "signup" || next["kind"] != "challenge" {
			t.Fatalf("the first social login = %s %v, want the signup challenge", state, next)
		}
		fields, _ := next["fields"].([]any)
		if len(fields) != 1 {
			t.Fatalf("the signup step asks for %v, want the username alone", fields)
		}
		field, _ := fields[0].(map[string]any)
		if field["name"] != "username" || field["kind"] != "text" || field["required"] != true {
			t.Errorf("the signup field = %v, want a required text username", field)
		}

		// Nothing of the account exists while the step stands.
		if _, err := h.accounts.ByExternal(h.ctx, "google", "g1"); err == nil {
			t.Error("the identity is bound before the username arrived")
		}
		if _, err := h.accounts.Lookup(h.ctx, authn.IdentifierUsername, "newbie"); err == nil {
			t.Error("a username is held before the account exists")
		}
		if accounts, _, err := h.accounts.List(h.ctx, authn.AccountQuery{}); err != nil || len(accounts) != 0 {
			t.Errorf("accounts = %v, %v before the username arrived, want none", accounts, err)
		}

		// A name that cannot be used is 400, and the flow is still at the step.
		bad := h.answerUsername(seq, "not a name!")
		if bad.status != http.StatusBadRequest || bad.json(t)["type"] != "urn:speed:authn:bad-request" {
			t.Fatalf("an unusable username answered %d %s, want 400 bad-request", bad.status, bad.body)
		}
		if accounts, _, err := h.accounts.List(h.ctx, authn.AccountQuery{}); err != nil || len(accounts) != 0 {
			t.Errorf("accounts = %v after an unusable name, want none", accounts)
		}

		state, seq, next = signupStep(t, h.get("/api/v1/auth/flows/current"))
		if state != "signup" || next["kind"] != "challenge" {
			t.Fatalf("GET flows/current after the refusal = %s %v, want the signup challenge", state, next)
		}

		// A reserved name is the same 400, and still nothing is written.
		reserved := h.answerUsername(seq, "admin")
		if reserved.status != http.StatusBadRequest || reserved.json(t)["type"] != "urn:speed:authn:bad-request" {
			t.Fatalf("a reserved username answered %d %s, want 400 bad-request", reserved.status, reserved.body)
		}

		state, seq, _ = signupStep(t, h.get("/api/v1/auth/flows/current"))
		if state != "signup" {
			t.Fatalf("the flow left the signup step after a reserved name: %s", state)
		}

		// A name another account holds is 409 username-taken, and no account of
		// this login exists.
		h.createAccount("taken-one", "taken-one-password")
		taken := h.answerUsername(seq, "taken-one")
		if taken.status != http.StatusConflict || taken.json(t)["type"] != "urn:speed:authn:username-taken" {
			t.Fatalf("a taken username answered %d %s, want 409 username-taken", taken.status, taken.body)
		}
		if _, err := h.accounts.ByExternal(h.ctx, "google", "g1"); err == nil {
			t.Error("the identity was bound by a refused username")
		}

		state, seq, _ = signupStep(t, h.get("/api/v1/auth/flows/current"))
		if state != "signup" {
			t.Fatalf("the flow left the signup step after a taken name: %s", state)
		}

		// The legal name opens the account and the flow carries on to ready.
		opened := h.answerUsername(seq, "newbie")
		if opened.status != http.StatusOK {
			t.Fatalf("the username answer answered %d: %s", opened.status, opened.body)
		}
		state, seq, next = signupStep(t, opened)
		if state != "ready" || next["kind"] != "complete" {
			t.Fatalf("after the username the flow = %s %v, want ready complete", state, next)
		}

		done := h.post("/api/v1/auth/flows/answer", map[string]any{"seq": seq, "step": "complete"})
		if done.status != http.StatusOK || !h.selfStatus() {
			t.Fatalf("the completed login answered %d %s, want a session", done.status, done.body)
		}

		bound, err := h.accounts.ByExternal(h.ctx, "google", "g1")
		if err != nil {
			t.Fatalf("the identity of the opened account: %v", err)
		}
		held := identifiersOf(t, h.accounts, bound)
		if !slices.ContainsFunc(held, func(i authn.Identifier) bool {
			return i.Kind == authn.IdentifierUsername && i.Value == "newbie" && i.VerifiedVia == authn.VerifiedViaSelf
		}) {
			t.Errorf("identifiers = %+v, want the collected username written through self", held)
		}
	})
}

// TestASMSFirstLoginStopsAtTheUsernameStepAndItsPhoneSignsIn holds the SMS path
// against the real account store: the flow stops at the username step with
// nothing written, and the account it opens carries the proved phone as a
// sign-in way.
func TestASMSFirstLoginStopsAtTheUsernameStepAndItsPhoneSignsIn(t *testing.T) {
	const number = "+8613800000002"
	assembleWith(t, []core.Module{smsSignupModule()}, func(h *harness) {
		writeASetting(t, h.ctx, h.store, testSignupKey, "open")

		start := h.post("/api/v1/auth/flows", map[string]any{"method": "sms-signup"})
		if start.status != http.StatusOK {
			t.Fatalf("POST flows answered %d: %s", start.status, start.body)
		}
		state, seq, next := signupStep(t, start)
		if state != "signup" || next["kind"] != "challenge" {
			t.Fatalf("the first SMS login = %s %v, want the signup challenge", state, next)
		}
		if accounts, _, err := h.accounts.List(h.ctx, authn.AccountQuery{}); err != nil || len(accounts) != 0 {
			t.Errorf("accounts = %v before the username arrived, want none", accounts)
		}
		if _, err := h.accounts.Lookup(h.ctx, authn.IdentifierPhone, number); err == nil {
			t.Error("the phone is held before the username arrived")
		}

		opened := h.answerUsername(seq, "phoneowner")
		if opened.status != http.StatusOK {
			t.Fatalf("the username answer answered %d: %s", opened.status, opened.body)
		}
		state, seq, _ = signupStep(t, opened)
		if state != "ready" {
			t.Fatalf("after the username the flow = %s, want ready", state)
		}
		if done := h.post("/api/v1/auth/flows/answer", map[string]any{"seq": seq, "step": "complete"}); done.status != http.StatusOK {
			t.Fatalf("the completed login answered %d: %s", done.status, done.body)
		}

		id, err := h.accounts.Lookup(h.ctx, authn.IdentifierPhone, number)
		if err != nil {
			t.Fatalf("the phone's account: %v", err)
		}
		held := identifiersOf(t, h.accounts, id)
		if !slices.ContainsFunc(held, func(i authn.Identifier) bool {
			return i.Kind == authn.IdentifierPhone && i.Value == number && i.SignIn && !i.Contact
		}) {
			t.Errorf("identifiers = %+v, want the proved phone as a sign-in way and no contact", held)
		}
		if !slices.ContainsFunc(held, func(i authn.Identifier) bool {
			return i.Kind == authn.IdentifierUsername && i.Value == "phoneowner" && i.VerifiedVia == authn.VerifiedViaSelf
		}) {
			t.Errorf("identifiers = %+v, want the collected username", held)
		}
	})
}

// TestAnEnterpriseFirstLoginStopsAtTheUsernameStepAndWritesItsMembership holds
// the enterprise just-in-time path: the flow stops at the username step with no
// account or membership, and the opened account joins the provider's tenant.
func TestAnEnterpriseFirstLoginStopsAtTheUsernameStepAndWritesItsMembership(t *testing.T) {
	assembleWith(t, []core.Module{jitMethodModule(), jitRegisterModule()}, func(h *harness) {
		writeASetting(t, h.ctx, h.store, testSignupKey, "open")

		start := h.post("/api/v1/auth/flows", map[string]any{
			"method": "oidc-signup", "input": map[string]string{"provider": "0b8f0d3e-6f3a-4b3e-9c1e-2a4c5d6e7f80"},
		})
		if start.status != http.StatusOK {
			t.Fatalf("POST flows answered %d: %s", start.status, start.body)
		}
		state, seq, next := signupStep(t, start)
		if state != "signup" || next["kind"] != "challenge" {
			t.Fatalf("the first enterprise login = %s %v, want the signup challenge", state, next)
		}
		if _, err := h.accounts.ByExternal(h.ctx, "oidc:0b8f0d3e-6f3a-4b3e-9c1e-2a4c5d6e7f80", "u1"); err == nil {
			t.Error("the identity is bound before the username arrived")
		}
		if accounts, _, err := h.accounts.List(h.ctx, authn.AccountQuery{}); err != nil || len(accounts) != 0 {
			t.Errorf("accounts = %v before the username arrived, want none", accounts)
		}

		opened := h.answerUsername(seq, "jitperson")
		if opened.status != http.StatusOK {
			t.Fatalf("the username answer answered %d: %s", opened.status, opened.body)
		}
		state, seq, _ = signupStep(t, opened)
		if state != "ready" {
			t.Fatalf("after the username the flow = %s, want ready", state)
		}
		if done := h.post("/api/v1/auth/flows/answer", map[string]any{"seq": seq, "step": "complete"}); done.status != http.StatusOK {
			t.Fatalf("the completed login answered %d: %s", done.status, done.body)
		}

		id, err := h.accounts.ByExternal(h.ctx, "oidc:0b8f0d3e-6f3a-4b3e-9c1e-2a4c5d6e7f80", "u1")
		if err != nil {
			t.Fatalf("the identity of the opened account: %v", err)
		}
		membership, ok, err := h.memberships.Membership(h.ctx, "acme", id)
		if err != nil || !ok || !membership.Active {
			t.Errorf("membership = %+v, %v, %v, want the provider's tenant and active", membership, ok, err)
		}
	})
}

// identifiersOf reads an account's identifiers.
func identifiersOf(t *testing.T, accounts authn.Accounts, id authn.AccountID) []authn.Identifier {
	t.Helper()
	held, err := accounts.Identifiers(context.Background(), id)
	if err != nil {
		t.Fatalf("reading the identifiers of %q: %v", id, err)
	}
	return held
}

// smsSignupMethod is a stand-in SMS method whose first step is already the proof
// of a phone, so a case reaches the first SMS login without a code round trip.
type smsSignupMethod struct{}

func (smsSignupMethod) Name() string { return "sms-signup" }

func (smsSignupMethod) Describe() authn.MethodDescriptor {
	return authn.MethodDescriptor{Name: "sms-signup", Kind: authn.MethodKindForm, OpensAccount: true}
}

func (smsSignupMethod) Begin(context.Context, authn.Start) (authn.Step, error) {
	return authn.StepProven(authn.Proof{
		Identifier: &authn.Identifier{Kind: authn.IdentifierPhone, Value: "+8613800000002", VerifiedVia: authn.VerifiedViaSMSCode},
		Methods:    []authn.Method{authn.MethodSMS},
	}), nil
}

func (smsSignupMethod) Answer(context.Context, authn.FlowState, authn.StepInput) (authn.Step, error) {
	return authn.Step{}, fmt.Errorf("the stand-in SMS method has no answer step")
}

func smsSignupModule() core.Module {
	return core.Module{
		Name:     "stand-in.sms-signup",
		Provides: []core.Provision{{Token: (*authn.LoginMethod)(nil)}},
		New:      func(context.Context, *core.Registry) (any, error) { return smsSignupMethod{}, nil },
	}
}

// jitMethod is a stand-in enterprise OIDC method whose first step is already the
// proof of an identity of the tenant's enabled provider.
type jitMethod struct{}

func (jitMethod) Name() string { return "oidc-signup" }

func (jitMethod) Describe() authn.MethodDescriptor {
	return authn.MethodDescriptor{
		Name: "oidc-signup", Kind: authn.MethodKindEnterprise, Protocol: authn.MethodProtocolOIDC, OpensAccount: true,
	}
}

func (jitMethod) Begin(context.Context, authn.Start) (authn.Step, error) {
	return authn.StepProven(authn.Proof{
		Provider: "0b8f0d3e-6f3a-4b3e-9c1e-2a4c5d6e7f80",
		External: &authn.External{
			Provider: "oidc:0b8f0d3e-6f3a-4b3e-9c1e-2a4c5d6e7f80", Subject: "u1", Email: "jit@acme.com",
			Tenant: "acme", DisplayName: "Jit Person",
		},
		Methods: []authn.Method{authn.MethodOIDC},
	}), nil
}

func (jitMethod) Answer(context.Context, authn.FlowState, authn.StepInput) (authn.Step, error) {
	return authn.Step{}, fmt.Errorf("the stand-in enterprise method has no answer step")
}

func jitMethodModule() core.Module {
	return core.Module{
		Name:     "stand-in.oidc-signup",
		Provides: []core.Provision{{Token: (*authn.LoginMethod)(nil)}},
		New:      func(context.Context, *core.Registry) (any, error) { return jitMethod{}, nil },
	}
}

// jitProviderID is the stand-in enterprise provider's identifier. It is UUID
// text, the shape a session records a provider in.
const jitProviderID = "0b8f0d3e-6f3a-4b3e-9c1e-2a4c5d6e7f80"

// jitProviders is the register of one enabled just-in-time provider of tenant
// acme, with acme.com among its verified domains.
type jitProviders struct{ authn.SSOProviders }

func jitProvider() authn.SSOProvider {
	return authn.SSOProvider{
		ID: jitProviderID, Tenant: "acme", Protocol: authn.SSOProtocolOIDC,
		Enabled: true, Provisioning: authn.ProvisioningJIT,
	}
}

func (jitProviders) Get(_ context.Context, id string) (authn.SSOProvider, error) {
	if id != jitProviderID {
		return authn.SSOProvider{}, fmt.Errorf("no provider %q", id)
	}
	return jitProvider(), nil
}

func (jitProviders) Discover(_ context.Context, domain string) ([]authn.SSOProvider, error) {
	if !strings.EqualFold(domain, "acme.com") {
		return nil, nil
	}
	return []authn.SSOProvider{jitProvider()}, nil
}

func jitRegisterModule() core.Module {
	return core.Module{
		Name:     "stand-in.sso-signup",
		Provides: []core.Provision{{Token: (*authn.SSOProviders)(nil)}},
		New:      func(context.Context, *core.Registry) (any, error) { return jitProviders{}, nil },
	}
}
