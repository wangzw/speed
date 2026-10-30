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

// TestARefusedUsernameKeepsTheAnswerPosition holds the retry the step invites:
// a refusal leaves the answer position where it was, so the same step number
// answers again and the legal name opens the account. A refusal that moved the
// number would strand the retry, because the failure reply carries no new one.
func TestARefusedUsernameKeepsTheAnswerPosition(t *testing.T) {
	assembleWith(t, []core.Module{socialModule()}, func(h *harness) {
		writeASetting(t, h.ctx, h.store, testSignupKey, "open")
		_, seq, _ := signupStep(t, h.startSocialSignup())

		refused := h.answerSignup(seq, "admin", socialPassword)
		if refused.status != http.StatusBadRequest || refused.json(t)["type"] != "urn:speed:authn:username-reserved" {
			t.Fatalf("a reserved username answered %d %s, want 400 username-reserved", refused.status, refused.body)
		}

		accepted := h.answerSignup(seq, "newbie", socialPassword)
		if accepted.status != http.StatusOK {
			t.Fatalf("the retry with the same seq answered %d %s, want 200", accepted.status, accepted.body)
		}
		if _, err := h.accounts.Lookup(h.ctx, authn.IdentifierUsername, "newbie"); err != nil {
			t.Errorf("the accepted retry left no account: %v", err)
		}
	})
}

// TestARefusedUsernameCountsAgainstTheAnswerLimit holds that keeping the
// position unchanged does not make the step free: every refusal is an answer,
// and the flow fails once the limit is spent.
func TestARefusedUsernameCountsAgainstTheAnswerLimit(t *testing.T) {
	assembleWith(t, []core.Module{socialModule()}, func(h *harness) {
		writeASetting(t, h.ctx, h.store, testSignupKey, "open")
		_, seq, _ := signupStep(t, h.startSocialSignup())

		// The flow accepts eight answers; see design-authn-login's answer table
		// and flow.go's maxAnswers. The case is external to the package, so the
		// number is restated here as the contract's.
		const answerLimit = 8
		for i := 1; i <= answerLimit; i++ {
			refused := h.answerSignup(seq, "admin", socialPassword)
			if refused.status != http.StatusBadRequest {
				t.Fatalf("refusal %d answered %d %s, want 400", i, refused.status, refused.body)
			}
		}
		last := h.answerSignup(seq, "newbie", socialPassword)
		if last.status != http.StatusUnauthorized || last.json(t)["type"] != "urn:speed:authn:flow-invalid" {
			t.Fatalf("the answer past the limit answered %d %s, want 401 flow-invalid", last.status, last.body)
		}
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
		checkSignupFields(t, next, "username", "password")
		if got := next["provided_display_name"]; got != "Ann" {
			t.Errorf("provided_display_name = %v, want the provider's name for the person", got)
		}
		if got := next["provided_method"]; got != socialMethodName {
			t.Errorf("provided_method = %v, want the method that started the flow %q", got, socialMethodName)
		}
		if got := next["provided_email"]; got != "ann@x.org" {
			t.Errorf("provided_email = %v, want the provider's verified address", got)
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
		bad := h.answerSignup(seq, "not a name!", socialPassword)
		if bad.status != http.StatusBadRequest || bad.json(t)["type"] != "urn:speed:authn:username-invalid" {
			t.Fatalf("an unusable username answered %d %s, want 400 username-invalid", bad.status, bad.body)
		}
		if accounts, _, err := h.accounts.List(h.ctx, authn.AccountQuery{}); err != nil || len(accounts) != 0 {
			t.Errorf("accounts = %v after an unusable name, want none", accounts)
		}

		state, seq, next = signupStep(t, h.get("/api/v1/auth/flows/current"))
		if state != "signup" || next["kind"] != "challenge" {
			t.Fatalf("GET flows/current after the refusal = %s %v, want the signup challenge", state, next)
		}

		// A reserved name is its own 400, and still nothing is written.
		reserved := h.answerSignup(seq, "admin", socialPassword)
		if reserved.status != http.StatusBadRequest || reserved.json(t)["type"] != "urn:speed:authn:username-reserved" {
			t.Fatalf("a reserved username answered %d %s, want 400 username-reserved", reserved.status, reserved.body)
		}

		state, seq, _ = signupStep(t, h.get("/api/v1/auth/flows/current"))
		if state != "signup" {
			t.Fatalf("the flow left the signup step after a reserved name: %s", state)
		}

		// A name another account holds is 409 username-taken, and no account of
		// this login exists.
		h.createAccount("taken-one", "taken-one-password")
		taken := h.answerSignup(seq, "taken-one", socialPassword)
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
		opened := h.answerSignup(seq, "newbie", socialPassword)
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
		if !slices.ContainsFunc(held, func(i authn.Identifier) bool {
			return i.Kind == authn.IdentifierEmail && i.Value == "ann@x.org" &&
				i.VerifiedVia == "idp:google" && i.Contact && i.DefaultContact && i.SignIn
		}) {
			t.Errorf("identifiers = %+v, want the declared address as a proved default contact and sign-in way", held)
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

// socialPassword is the password the social first-login cases open an account
// with: long enough for the policy's default length and unlike their usernames.
const socialPassword = "ann-password-1"

// unverifiedSocialMethod is a stand-in social provider whose declared address
// the provider says it did not verify.
type unverifiedSocialMethod struct{ socialMethod }

func (unverifiedSocialMethod) Begin(context.Context, authn.Start) (authn.Step, error) {
	return authn.StepProven(authn.Proof{
		External: &authn.External{
			Provider:      "google",
			Subject:       "g2",
			Email:         "ann@x.org",
			EmailVerified: false,
		},
		Methods: []authn.Method{authn.MethodOIDC},
	}), nil
}

func unverifiedSocialModule() core.Module {
	return core.Module{
		Name:     "stand-in.google-unverified",
		Provides: []core.Provision{{Token: (*authn.LoginMethod)(nil)}},
		New: func(context.Context, *core.Registry) (any, error) {
			return unverifiedSocialMethod{}, nil
		},
	}
}

// TestAnEmptyUsernameOnTheSignupStepIsItsOwnRefusal holds that a blank name is
// the username field's failure rather than the body's: the client that posts an
// empty name gets 400 username-invalid, and nothing is written.
func TestAnEmptyUsernameOnTheSignupStepIsItsOwnRefusal(t *testing.T) {
	assembleWith(t, []core.Module{socialModule()}, func(h *harness) {
		writeASetting(t, h.ctx, h.store, testSignupKey, "open")
		_, seq, _ := signupStep(t, h.startSocialSignup())

		refused := h.answerSignup(seq, "", "correct horse battery")
		if refused.status != http.StatusBadRequest || refused.json(t)["type"] != "urn:speed:authn:username-invalid" {
			t.Fatalf("an empty username answered %d %s, want 400 username-invalid", refused.status, refused.body)
		}
		if accounts, _, err := h.accounts.List(h.ctx, authn.AccountQuery{}); err != nil || len(accounts) != 0 {
			t.Errorf("accounts = %v after an empty name, want none", accounts)
		}
	})
}

// answerSignup sends a pending-signup answer carrying the username and the
// password a social first login asks for beside it.
func (h *harness) answerSignup(seq int, username, password string) reply {
	h.t.Helper()
	return h.post("/api/v1/auth/flows/answer", map[string]any{
		"seq": seq, "step": "answer", "input": map[string]string{"username": username, "password": password},
	})
}

// checkSignupFields checks the fields of a pending-signup challenge in order:
// the username is a required text input, and the password, when asked for, is a
// required secret one.
func checkSignupFields(t *testing.T, next map[string]any, names ...string) {
	t.Helper()
	raw, _ := next["fields"].([]any)
	if len(raw) != len(names) {
		t.Fatalf("the signup step asks for %v, want %v", next["fields"], names)
	}
	for i, name := range names {
		field, _ := raw[i].(map[string]any)
		if field["name"] != name || field["required"] != true {
			t.Errorf("field %d = %v, want a required %q", i, raw[i], name)
		}
		switch name {
		case "username":
			if field["kind"] != "text" {
				t.Errorf("the username field = %v, want text", field)
			}
		case "password":
			if field["kind"] != "password" || field["secret"] != true {
				t.Errorf("the password field = %v, want a secret password", field)
			}
		}
	}
}

// checkNoProvidedIdentity checks that a pending-signup challenge carries none of
// the read-only identity members, which only a social first login adds.
func checkNoProvidedIdentity(t *testing.T, next map[string]any) {
	t.Helper()
	for _, key := range []string{"provided_method", "provided_display_name", "provided_email"} {
		if got, present := next[key]; present {
			t.Errorf("next carries %s = %v, want it absent", key, got)
		}
	}
}

// TestASocialFirstLoginWritesTheDeclaredAddressAsAContactAndTakesAPassword holds
// the social first login against the real account store: the step asks for a
// password beside the username and shows the provider's verified address
// read-only, and the account it opens holds that address as its default contact
// and, being its only address, as a sign-in way. The password the step took opens
// a login under the username and under that address afterwards.
func TestASocialFirstLoginWritesTheDeclaredAddressAsAContactAndTakesAPassword(t *testing.T) {
	assembleWith(t, []core.Module{socialModule()}, func(h *harness) {
		writeASetting(t, h.ctx, h.store, testSignupKey, "open")

		state, seq, next := signupStep(t, h.startSocialSignup())
		if state != "signup" || next["kind"] != "challenge" {
			t.Fatalf("the first social login = %s %v, want the signup challenge", state, next)
		}
		checkSignupFields(t, next, "username", "password")
		if got := next["provided_display_name"]; got != "Ann" {
			t.Errorf("provided_display_name = %v, want the provider's name for the person", got)
		}
		if got := next["provided_method"]; got != socialMethodName {
			t.Errorf("provided_method = %v, want the method that started the flow %q", got, socialMethodName)
		}
		if got := next["provided_email"]; got != "ann@x.org" {
			t.Errorf("provided_email = %v, want the provider's verified address", got)
		}

		opened := h.answerSignup(seq, "newbie", socialPassword)
		if opened.status != http.StatusOK {
			t.Fatalf("the signup answer answered %d: %s", opened.status, opened.body)
		}
		state, seq, next = signupStep(t, opened)
		if state != "ready" || next["kind"] != "complete" {
			t.Fatalf("after the answer the flow = %s %v, want ready complete", state, next)
		}
		if done := h.post("/api/v1/auth/flows/answer", map[string]any{"seq": seq, "step": "complete"}); done.status != http.StatusOK {
			t.Fatalf("the completed login answered %d: %s", done.status, done.body)
		}

		id, err := h.accounts.ByExternal(h.ctx, "google", "g1")
		if err != nil {
			t.Fatalf("the identity of the opened account: %v", err)
		}
		held := identifiersOf(t, h.accounts, id)
		if !slices.ContainsFunc(held, func(i authn.Identifier) bool {
			return i.Kind == authn.IdentifierEmail && i.Value == "ann@x.org" &&
				i.VerifiedVia == "idp:google" && i.Contact && i.DefaultContact && i.SignIn
		}) {
			t.Errorf("identifiers = %+v, want the declared address as a proved default contact and sign-in way", held)
		}

		if done := h.loginFlow("newbie", socialPassword); done.status != http.StatusOK {
			t.Fatalf("a password login with the collected password answered %d: %s", done.status, done.body)
		}
		if done := h.loginFlow("ann@x.org", socialPassword); done.status != http.StatusOK {
			t.Fatalf("a password login under the declared address answered %d: %s", done.status, done.body)
		}
	})
}

// TestAnAddressTakenBeforeTheAccountWriteIsLinkRequiredAndLeavesNoAccount holds
// the race the design settles: another account takes the address the provider
// declared verified between the read that found it free and this login's
// account write. The answer is the link-required refusal an address registered
// all along gives, the flow is spent, and the failed write leaves no half-built
// account or binding behind.
func TestAnAddressTakenBeforeTheAccountWriteIsLinkRequiredAndLeavesNoAccount(t *testing.T) {
	assembleWith(t, []core.Module{socialModule()}, func(h *harness) {
		writeASetting(t, h.ctx, h.store, testSignupKey, "open")

		state, seq, next := signupStep(t, h.startSocialSignup())
		if state != "signup" || next["kind"] != "challenge" {
			t.Fatalf("the first social login = %s %v, want the signup challenge", state, next)
		}
		if got := next["provided_email"]; got != "ann@x.org" {
			t.Fatalf("provided_email = %v, want the provider's verified address", got)
		}

		// Another account takes the address after the step decided it was free
		// and before this login writes its account.
		if _, err := h.admin.Create(h.ctx, authn.CreateAccount{
			Kind:             authn.KindUser,
			Username:         "holder",
			Email:            "ann@x.org",
			EmailVerifiedVia: authn.Verification{Via: authn.VerifiedViaIDPPrefix + "google"},
			Via:              "admin",
		}); err != nil {
			t.Fatalf("creating the account that takes the address: %v", err)
		}

		refused := h.answerSignup(seq, "newbie", socialPassword)
		if refused.status != http.StatusUnauthorized || refused.json(t)["type"] != "urn:speed:authn:link-required" {
			t.Fatalf("the answer after the address was taken answered %d %s, want 401 link-required", refused.status, refused.body)
		}

		// The failed write left no account of this login: no username, no
		// binding, and the fixture's one account is the only one there is.
		if _, err := h.accounts.Lookup(h.ctx, authn.IdentifierUsername, "newbie"); err == nil {
			t.Error("the refused signup opened the account it could not finish")
		}
		if _, err := h.accounts.ByExternal(h.ctx, "google", "g1"); err == nil {
			t.Error("the refused signup bound the identity")
		}
		if accounts, _, err := h.accounts.List(h.ctx, authn.AccountQuery{}); err != nil || len(accounts) != 1 {
			t.Errorf("accounts = %v, %v after the refused signup, want only the holder", accounts, err)
		}

		// The flow is spent: the username step cannot be retried once the
		// address is another account's.
		if current := h.get("/api/v1/auth/flows/current"); current.status != http.StatusUnauthorized {
			t.Errorf("GET flows/current after the refusal answered %d %s, want the failed tombstone", current.status, current.body)
		}
	})
}

// TestASocialFirstLoginWritesNoAddressTheProviderDidNotVerify holds that a
// provider's unverified claim is no more written into the account than it is a
// clue, and that the step carries no address for the client to show.
func TestASocialFirstLoginWritesNoAddressTheProviderDidNotVerify(t *testing.T) {
	assembleWith(t, []core.Module{unverifiedSocialModule()}, func(h *harness) {
		writeASetting(t, h.ctx, h.store, testSignupKey, "open")

		state, seq, next := signupStep(t, h.startSocialSignup())
		if state != "signup" || next["kind"] != "challenge" {
			t.Fatalf("the first social login = %s %v, want the signup challenge", state, next)
		}
		checkSignupFields(t, next, "username", "password")
		if got, present := next["provided_email"]; present && got != "" {
			t.Errorf("provided_email = %v, want it absent for an address the provider did not verify", got)
		}

		opened := h.answerSignup(seq, "newbie", socialPassword)
		if opened.status != http.StatusOK {
			t.Fatalf("the signup answer answered %d: %s", opened.status, opened.body)
		}

		id, err := h.accounts.ByExternal(h.ctx, "google", "g2")
		if err != nil {
			t.Fatalf("the identity of the opened account: %v", err)
		}
		if held := identifiersOf(t, h.accounts, id); slices.ContainsFunc(held, func(i authn.Identifier) bool {
			return i.Kind == authn.IdentifierEmail
		}) {
			t.Errorf("identifiers = %+v, want no email address", held)
		}
	})
}

// TestASocialSignupWithoutAPasswordOpensNothing holds that the password is part
// of the social step: a submission that carries only the username is 400
// bad-request, opens no account and leaves the flow at the step, and the same
// step number answers again with a password.
func TestASocialSignupWithoutAPasswordOpensNothing(t *testing.T) {
	assembleWith(t, []core.Module{socialModule()}, func(h *harness) {
		writeASetting(t, h.ctx, h.store, testSignupKey, "open")

		_, seq, _ := signupStep(t, h.startSocialSignup())
		refused := h.answerUsername(seq, "newbie")
		if refused.status != http.StatusBadRequest || refused.json(t)["type"] != "urn:speed:authn:bad-request" {
			t.Fatalf("a username without a password answered %d %s, want 400 bad-request", refused.status, refused.body)
		}
		if _, err := h.accounts.Lookup(h.ctx, authn.IdentifierUsername, "newbie"); err == nil {
			t.Error("a username without a password opened an account")
		}

		state, seq2, _ := signupStep(t, h.get("/api/v1/auth/flows/current"))
		if state != "signup" || seq2 != seq {
			t.Fatalf("the flow after the refusal = %s at %d, want signup at %d", state, seq2, seq)
		}

		if opened := h.answerSignup(seq, "newbie", socialPassword); opened.status != http.StatusOK {
			t.Fatalf("the retry with a password answered %d: %s", opened.status, opened.body)
		}
	})
}

// TestASocialSignupWithAPasswordAgainstThePolicyOpensNothing holds that the
// account store's policy governs the password the step takes: a short one is
// 400 bad-request, opens no account and leaves the flow retryable.
func TestASocialSignupWithAPasswordAgainstThePolicyOpensNothing(t *testing.T) {
	assembleWith(t, []core.Module{socialModule()}, func(h *harness) {
		writeASetting(t, h.ctx, h.store, testSignupKey, "open")

		_, seq, _ := signupStep(t, h.startSocialSignup())
		refused := h.answerSignup(seq, "newbie", "short")
		if refused.status != http.StatusBadRequest || refused.json(t)["type"] != "urn:speed:authn:bad-request" {
			t.Fatalf("a password against the policy answered %d %s, want 400 bad-request", refused.status, refused.body)
		}
		if _, err := h.accounts.Lookup(h.ctx, authn.IdentifierUsername, "newbie"); err == nil {
			t.Error("a password against the policy opened an account")
		}

		state, seq2, _ := signupStep(t, h.get("/api/v1/auth/flows/current"))
		if state != "signup" || seq2 != seq {
			t.Fatalf("the flow after the refusal = %s at %d, want signup at %d", state, seq2, seq)
		}

		if opened := h.answerSignup(seq, "newbie", socialPassword); opened.status != http.StatusOK {
			t.Fatalf("the retry with a legal password answered %d: %s", opened.status, opened.body)
		}
	})
}

// TestTheSMSAndEnterpriseSignupStepsAskForTheUsernameAlone holds that the
// password belongs to the social step only: the SMS and enterprise steps ask
// for the username by itself, and neither account takes the provider's declared
// address.
func TestTheSMSAndEnterpriseSignupStepsAskForTheUsernameAlone(t *testing.T) {
	t.Run("sms", func(t *testing.T) {
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
			checkSignupFields(t, next, "username")
			checkNoProvidedIdentity(t, next)

			if opened := h.answerUsername(seq, "phoneowner"); opened.status != http.StatusOK {
				t.Fatalf("the username answer answered %d: %s", opened.status, opened.body)
			}
			id, err := h.accounts.Lookup(h.ctx, authn.IdentifierPhone, number)
			if err != nil {
				t.Fatalf("the phone's account: %v", err)
			}
			if held := identifiersOf(t, h.accounts, id); slices.ContainsFunc(held, func(i authn.Identifier) bool {
				return i.Kind == authn.IdentifierEmail
			}) {
				t.Errorf("identifiers = %+v, want no email address", held)
			}
		})
	})

	t.Run("enterprise", func(t *testing.T) {
		assembleWith(t, []core.Module{jitMethodModule(), jitRegisterModule()}, func(h *harness) {
			writeASetting(t, h.ctx, h.store, testSignupKey, "open")

			start := h.post("/api/v1/auth/flows", map[string]any{
				"method": "oidc-signup", "input": map[string]string{"provider": jitProviderID},
			})
			if start.status != http.StatusOK {
				t.Fatalf("POST flows answered %d: %s", start.status, start.body)
			}
			state, seq, next := signupStep(t, start)
			if state != "signup" || next["kind"] != "challenge" {
				t.Fatalf("the first enterprise login = %s %v, want the signup challenge", state, next)
			}
			checkSignupFields(t, next, "username")
			checkNoProvidedIdentity(t, next)

			if opened := h.answerUsername(seq, "jitperson"); opened.status != http.StatusOK {
				t.Fatalf("the username answer answered %d: %s", opened.status, opened.body)
			}
			id, err := h.accounts.ByExternal(h.ctx, "oidc:"+jitProviderID, "u1")
			if err != nil {
				t.Fatalf("the identity of the opened account: %v", err)
			}
			if held := identifiersOf(t, h.accounts, id); slices.ContainsFunc(held, func(i authn.Identifier) bool {
				return i.Kind == authn.IdentifierEmail
			}) {
				t.Errorf("identifiers = %+v, want no email address: an enterprise just-in-time account does not write one", held)
			}
		})
	})
}
