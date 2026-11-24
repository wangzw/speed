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

func (h *harness) startSocialSignup() reply {
	h.t.Helper()
	start := h.post("/api/v1/auth/flows", map[string]any{"method": "google"})
	if start.status != http.StatusOK {
		h.t.Fatalf("POST flows answered %d: %s", start.status, start.body)
	}
	return start
}

func signupStep(t *testing.T, r reply) (string, int, map[string]any) {
	t.Helper()
	doc := r.json(t)
	state, _ := doc["state"].(string)
	seq, _ := doc["seq"].(float64)
	next, _ := doc["next"].(map[string]any)
	return state, int(seq), next
}

func (h *harness) answerUsername(seq int, username string) reply {
	h.t.Helper()
	return h.post("/api/v1/auth/flows/answer", map[string]any{
		"seq": seq, "step": "answer", "input": map[string]string{"username": username},
	})
}

func (h *harness) answerPhoneSignup(seq int, username, displayName, password string) reply {
	h.t.Helper()
	return h.post("/api/v1/auth/flows/answer", map[string]any{
		"seq": seq, "step": "answer", "input": map[string]string{
			"username": username, "display_name": displayName, "password": password,
		},
	})
}

// TestARefusedUsernameKeepsTheAnswerPosition holds that a refusal leaves the position, so the same step number answers again.
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

// TestARefusedUsernameCountsAgainstTheAnswerLimit holds that each refusal is an answer, so keeping the position does not make the step free.
func TestARefusedUsernameCountsAgainstTheAnswerLimit(t *testing.T) {
	assembleWith(t, []core.Module{socialModule()}, func(h *harness) {
		writeASetting(t, h.ctx, h.store, testSignupKey, "open")
		_, seq, _ := signupStep(t, h.startSocialSignup())

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

// TestASocialFirstLoginStopsAtTheUsernameStepBeforeOpeningAnAccount holds that nothing is written there, invalid and taken names refuse, and a legal name opens and finishes the login.
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

		reserved := h.answerSignup(seq, "admin", socialPassword)
		if reserved.status != http.StatusBadRequest || reserved.json(t)["type"] != "urn:speed:authn:username-reserved" {
			t.Fatalf("a reserved username answered %d %s, want 400 username-reserved", reserved.status, reserved.body)
		}

		state, seq, _ = signupStep(t, h.get("/api/v1/auth/flows/current"))
		if state != "signup" {
			t.Fatalf("the flow left the signup step after a reserved name: %s", state)
		}

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

// TestASMSFirstLoginStopsAtTheUsernameStepAndItsPhoneSignsIn holds that the flow stops there and the opened account carries the proved phone as a sign-in way.
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

		opened := h.answerPhoneSignup(seq, "phoneowner", "", phonePassword)
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
			return i.Kind == authn.IdentifierPhone && i.Value == number && i.SignIn && i.Contact
		}) {
			t.Errorf("identifiers = %+v, want the proved phone as a sign-in way and a contact", held)
		}
		if !slices.ContainsFunc(held, func(i authn.Identifier) bool {
			return i.Kind == authn.IdentifierUsername && i.Value == "phoneowner" && i.VerifiedVia == authn.VerifiedViaSelf
		}) {
			t.Errorf("identifiers = %+v, want the collected username", held)
		}
	})
}

// TestAnEnterpriseFirstLoginStopsAtTheUsernameStepAndWritesItsMembership holds that no account or membership exists until the username, which then joins the provider's tenant.
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

func identifiersOf(t *testing.T, accounts authn.Accounts, id authn.AccountID) []authn.Identifier {
	t.Helper()
	held, err := accounts.Identifiers(context.Background(), id)
	if err != nil {
		t.Fatalf("reading the identifiers of %q: %v", id, err)
	}
	return held
}

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

type noNameJitMethod struct{}

func (noNameJitMethod) Name() string { return "oidc-signup-noname" }

func (noNameJitMethod) Describe() authn.MethodDescriptor {
	return authn.MethodDescriptor{
		Name: "oidc-signup-noname", Kind: authn.MethodKindEnterprise, Protocol: authn.MethodProtocolOIDC, OpensAccount: true,
	}
}

func (noNameJitMethod) Begin(context.Context, authn.Start) (authn.Step, error) {
	return authn.StepProven(authn.Proof{
		Provider: "0b8f0d3e-6f3a-4b3e-9c1e-2a4c5d6e7f80",
		External: &authn.External{
			Provider: "oidc:0b8f0d3e-6f3a-4b3e-9c1e-2a4c5d6e7f80", Subject: "u2", Email: "jit2@acme.com", Tenant: "acme",
		},
		Methods: []authn.Method{authn.MethodOIDC},
	}), nil
}

func (noNameJitMethod) Answer(context.Context, authn.FlowState, authn.StepInput) (authn.Step, error) {
	return authn.Step{}, fmt.Errorf("the stand-in enterprise method has no answer step")
}

func noNameJitMethodModule() core.Module {
	return core.Module{
		Name:     "stand-in.oidc-signup-noname",
		Provides: []core.Provision{{Token: (*authn.LoginMethod)(nil)}},
		New:      func(context.Context, *core.Registry) (any, error) { return noNameJitMethod{}, nil },
	}
}

const jitProviderID = "0b8f0d3e-6f3a-4b3e-9c1e-2a4c5d6e7f80"

type jitProviders struct{ authn.SSOProviders }

func jitProvider() authn.SSOProvider {
	return authn.SSOProvider{
		ID: jitProviderID, Tenant: "acme", Protocol: authn.SSOProtocolOIDC,
		Enabled: true, Provisioning: authn.ProvisioningJIT,
		UsernameStrategy: authn.UsernameStrategyUserConfirm,
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

const socialPassword = "ann-password-1"

const phonePassword = "phone-password-1"

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

type noNameSocialMethod struct{ socialMethod }

func (noNameSocialMethod) Begin(context.Context, authn.Start) (authn.Step, error) {
	return authn.StepProven(authn.Proof{
		External: &authn.External{Provider: "google", Subject: "g2"},
		Methods:  []authn.Method{authn.MethodOIDC},
	}), nil
}

func noNameSocialModule() core.Module {
	return core.Module{
		Name:     "stand-in.google-noname",
		Provides: []core.Provision{{Token: (*authn.LoginMethod)(nil)}},
		New: func(context.Context, *core.Registry) (any, error) {
			return noNameSocialMethod{}, nil
		},
	}
}

// TestAnEmptyUsernameOnTheSignupStepIsItsOwnRefusal holds that a blank name answers 400 username-invalid and writes nothing.
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

func (h *harness) answerSignup(seq int, username, password string) reply {
	h.t.Helper()
	return h.post("/api/v1/auth/flows/answer", map[string]any{
		"seq": seq, "step": "answer", "input": map[string]string{"username": username, "password": password},
	})
}

// checkSignupFields checks the fields of a pending-signup challenge in order:
// the username is a required text input, the display name is an optional text
// one, and the password, when asked for, is a required secret one.
func checkSignupFields(t *testing.T, next map[string]any, names ...string) {
	t.Helper()
	raw, _ := next["fields"].([]any)
	if len(raw) != len(names) {
		t.Fatalf("the signup step asks for %v, want %v", next["fields"], names)
	}
	for i, name := range names {
		field, _ := raw[i].(map[string]any)
		required, _ := field["required"].(bool)
		wantRequired := name != "display_name"
		if field["name"] != name || required != wantRequired {
			t.Errorf("field %d = %v, want %q with required=%v", i, raw[i], name, wantRequired)
		}
		switch name {
		case "username", "display_name":
			if field["kind"] != "text" {
				t.Errorf("the %s field = %v, want text", name, field)
			}
		case "password":
			if field["kind"] != "password" || field["secret"] != true {
				t.Errorf("the password field = %v, want a secret password", field)
			}
		}
	}
}

func checkNoProvidedIdentity(t *testing.T, next map[string]any) {
	t.Helper()
	for _, key := range []string{"provided_method", "provided_display_name", "provided_email"} {
		if got, present := next[key]; present {
			t.Errorf("next carries %s = %v, want it absent", key, got)
		}
	}
}

// TestASocialFirstLoginWritesTheDeclaredAddressAsAContactAndTakesAPassword holds that the step shows the verified address read-only and the account keeps it as default contact and sign-in way.
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

// TestAnAddressTakenBeforeTheAccountWriteIsLinkRequiredAndLeavesNoAccount holds that an address taken between the read and the write answers link-required and leaves no account.
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

// TestASocialFirstLoginWritesNoAddressTheProviderDidNotVerify holds that an unverified claim is neither written nor shown.
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

// TestASocialSignupWithoutAPasswordOpensNothing holds that a username-only submission answers 400 and the same step number answers again with a password.
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

// TestASocialSignupWithAPasswordAgainstThePolicyOpensNothing holds that a short password answers 400 and the flow stays retryable.
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

// TestAPhoneSignupWithoutAPasswordOpensNothing holds that a username and display name without a password answer 400 and the same step number retries.
func TestAPhoneSignupWithoutAPasswordOpensNothing(t *testing.T) {
	const number = "+8613800000002"
	assembleWith(t, []core.Module{smsSignupModule()}, func(h *harness) {
		writeASetting(t, h.ctx, h.store, testSignupKey, "open")

		start := h.post("/api/v1/auth/flows", map[string]any{"method": "sms-signup"})
		if start.status != http.StatusOK {
			t.Fatalf("POST flows answered %d: %s", start.status, start.body)
		}
		_, seq, _ := signupStep(t, start)
		refused := h.post("/api/v1/auth/flows/answer", map[string]any{
			"seq": seq, "step": "answer", "input": map[string]string{
				"username": "phoneowner", "display_name": "Phone Person",
			},
		})
		if refused.status != http.StatusBadRequest || refused.json(t)["type"] != "urn:speed:authn:bad-request" {
			t.Fatalf("a username without a password answered %d %s, want 400 bad-request", refused.status, refused.body)
		}
		if _, err := h.accounts.Lookup(h.ctx, authn.IdentifierUsername, "phoneowner"); err == nil {
			t.Error("a username without a password opened an account")
		}
		if _, err := h.accounts.Lookup(h.ctx, authn.IdentifierPhone, number); err == nil {
			t.Error("a username without a password wrote the proved phone")
		}

		state, seq2, _ := signupStep(t, h.get("/api/v1/auth/flows/current"))
		if state != "signup" || seq2 != seq {
			t.Fatalf("the flow after the refusal = %s at %d, want signup at %d", state, seq2, seq)
		}

		if opened := h.answerPhoneSignup(seq, "phoneowner", "Phone Person", phonePassword); opened.status != http.StatusOK {
			t.Fatalf("the retry with a password answered %d: %s", opened.status, opened.body)
		}
	})
}

// TestAPhoneSignupWithAPasswordAgainstThePolicyOpensNothing holds that a short password answers 400 and the flow stays retryable.
func TestAPhoneSignupWithAPasswordAgainstThePolicyOpensNothing(t *testing.T) {
	const number = "+8613800000002"
	assembleWith(t, []core.Module{smsSignupModule()}, func(h *harness) {
		writeASetting(t, h.ctx, h.store, testSignupKey, "open")

		start := h.post("/api/v1/auth/flows", map[string]any{"method": "sms-signup"})
		if start.status != http.StatusOK {
			t.Fatalf("POST flows answered %d: %s", start.status, start.body)
		}
		_, seq, _ := signupStep(t, start)
		refused := h.answerPhoneSignup(seq, "newbie", "", "short")
		if refused.status != http.StatusBadRequest || refused.json(t)["type"] != "urn:speed:authn:bad-request" {
			t.Fatalf("a password against the policy answered %d %s, want 400 bad-request", refused.status, refused.body)
		}
		if _, err := h.accounts.Lookup(h.ctx, authn.IdentifierUsername, "newbie"); err == nil {
			t.Error("a password against the policy opened an account")
		}
		if _, err := h.accounts.Lookup(h.ctx, authn.IdentifierPhone, number); err == nil {
			t.Error("a password against the policy wrote the proved phone")
		}

		state, seq2, _ := signupStep(t, h.get("/api/v1/auth/flows/current"))
		if state != "signup" || seq2 != seq {
			t.Fatalf("the flow after the refusal = %s at %d, want signup at %d", state, seq2, seq)
		}

		if opened := h.answerPhoneSignup(seq, "newbie", "", phonePassword); opened.status != http.StatusOK {
			t.Fatalf("the retry with a legal password answered %d: %s", opened.status, opened.body)
		}
		if done := h.loginFlow("newbie", phonePassword); done.status != http.StatusOK {
			t.Fatalf("a password login with the collected password answered %d: %s", done.status, done.body)
		}
	})
}

// TestTheSMSAndEnterpriseSignupStepsAskForTheirOwnFields holds that the SMS step asks username, optional display name and password, and the enterprise step only username.
func TestTheSMSAndEnterpriseSignupStepsAskForTheirOwnFields(t *testing.T) {
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
			checkSignupFields(t, next, "username", "display_name", "password")
			checkNoProvidedIdentity(t, next)

			if opened := h.answerPhoneSignup(seq, "phoneowner", "", phonePassword); opened.status != http.StatusOK {
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

// TestAPhoneSignupWithoutADisplayNameTakesTheUsername holds that an account without a display name is named after its username.
func TestAPhoneSignupWithoutADisplayNameTakesTheUsername(t *testing.T) {
	const number = "+8613800000002"
	assembleWith(t, []core.Module{smsSignupModule()}, func(h *harness) {
		writeASetting(t, h.ctx, h.store, testSignupKey, "open")

		start := h.post("/api/v1/auth/flows", map[string]any{"method": "sms-signup"})
		if start.status != http.StatusOK {
			t.Fatalf("POST flows answered %d: %s", start.status, start.body)
		}
		_, seq, next := signupStep(t, start)
		checkSignupFields(t, next, "username", "display_name", "password")

		if opened := h.answerPhoneSignup(seq, "phoneowner", "", phonePassword); opened.status != http.StatusOK {
			t.Fatalf("the username answer answered %d: %s", opened.status, opened.body)
		}
		account := h.checkPhoneSignupAccount(t, number)
		if account.DisplayName != "phoneowner" {
			t.Errorf("display name = %q, want the username %q", account.DisplayName, "phoneowner")
		}
	})
}

// TestAPhoneSignupTakesTheDisplayNameItIsGiven holds that the optional field a
// phone signup asks for is what names the account when it is filled in.
func TestAPhoneSignupTakesTheDisplayNameItIsGiven(t *testing.T) {
	const number = "+8613800000002"
	assembleWith(t, []core.Module{smsSignupModule()}, func(h *harness) {
		writeASetting(t, h.ctx, h.store, testSignupKey, "open")

		start := h.post("/api/v1/auth/flows", map[string]any{"method": "sms-signup"})
		if start.status != http.StatusOK {
			t.Fatalf("POST flows answered %d: %s", start.status, start.body)
		}
		_, seq, next := signupStep(t, start)
		checkSignupFields(t, next, "username", "display_name", "password")

		if opened := h.answerPhoneSignup(seq, "phonenamed", "Phone Person", phonePassword); opened.status != http.StatusOK {
			t.Fatalf("the username answer answered %d: %s", opened.status, opened.body)
		}
		account := h.checkPhoneSignupAccount(t, number)
		if account.DisplayName != "Phone Person" {
			t.Errorf("display name = %q, want the submitted %q", account.DisplayName, "Phone Person")
		}
	})
}

func (h *harness) checkPhoneSignupAccount(t *testing.T, number string) authn.Account {
	t.Helper()
	id, err := h.accounts.Lookup(h.ctx, authn.IdentifierPhone, number)
	if err != nil {
		t.Fatalf("the phone's account: %v", err)
	}
	account, err := h.accounts.Get(h.ctx, id)
	if err != nil {
		t.Fatalf("reading the phone's account: %v", err)
	}
	return account
}

// TestEnterpriseJITSignupNamesTheAccountFromTheProviderOrTheUsername holds that the account takes the provider's name when given and the username otherwise.
func TestEnterpriseJITSignupNamesTheAccountFromTheProviderOrTheUsername(t *testing.T) {
	t.Run("provider name", func(t *testing.T) {
		assembleWith(t, []core.Module{jitMethodModule(), jitRegisterModule()}, func(h *harness) {
			writeASetting(t, h.ctx, h.store, testSignupKey, "open")

			start := h.post("/api/v1/auth/flows", map[string]any{
				"method": "oidc-signup", "input": map[string]string{"provider": jitProviderID},
			})
			if start.status != http.StatusOK {
				t.Fatalf("POST flows answered %d: %s", start.status, start.body)
			}
			_, seq, _ := signupStep(t, start)
			if opened := h.answerUsername(seq, "jitperson"); opened.status != http.StatusOK {
				t.Fatalf("the username answer answered %d: %s", opened.status, opened.body)
			}
			id, err := h.accounts.ByExternal(h.ctx, "oidc:"+jitProviderID, "u1")
			if err != nil {
				t.Fatalf("the identity of the opened account: %v", err)
			}
			account, err := h.accounts.Get(h.ctx, id)
			if err != nil {
				t.Fatalf("reading the opened account: %v", err)
			}
			if account.DisplayName != "Jit Person" {
				t.Errorf("display name = %q, want the provider's %q", account.DisplayName, "Jit Person")
			}
		})
	})

	t.Run("no provider name", func(t *testing.T) {
		assembleWith(t, []core.Module{noNameJitMethodModule(), jitRegisterModule()}, func(h *harness) {
			writeASetting(t, h.ctx, h.store, testSignupKey, "open")

			start := h.post("/api/v1/auth/flows", map[string]any{
				"method": "oidc-signup-noname", "input": map[string]string{"provider": jitProviderID},
			})
			if start.status != http.StatusOK {
				t.Fatalf("POST flows answered %d: %s", start.status, start.body)
			}
			_, seq, _ := signupStep(t, start)
			if opened := h.answerUsername(seq, "jitperson"); opened.status != http.StatusOK {
				t.Fatalf("the username answer answered %d: %s", opened.status, opened.body)
			}
			id, err := h.accounts.ByExternal(h.ctx, "oidc:"+jitProviderID, "u2")
			if err != nil {
				t.Fatalf("the identity of the opened account: %v", err)
			}
			account, err := h.accounts.Get(h.ctx, id)
			if err != nil {
				t.Fatalf("reading the opened account: %v", err)
			}
			if account.DisplayName != "jitperson" {
				t.Errorf("display name = %q, want the username %q", account.DisplayName, "jitperson")
			}
		})
	})
}

// TestSocialJITSignupNamesTheAccountFromTheProviderOrTheUsername holds that the account takes the provider's name when given and the username otherwise.
func TestSocialJITSignupNamesTheAccountFromTheProviderOrTheUsername(t *testing.T) {
	t.Run("provider name", func(t *testing.T) {
		assembleWith(t, []core.Module{socialModule()}, func(h *harness) {
			writeASetting(t, h.ctx, h.store, testSignupKey, "open")

			_, seq, _ := signupStep(t, h.startSocialSignup())
			if opened := h.answerSignup(seq, "socialperson", socialPassword); opened.status != http.StatusOK {
				t.Fatalf("the username answer answered %d: %s", opened.status, opened.body)
			}
			id, err := h.accounts.ByExternal(h.ctx, "google", "g1")
			if err != nil {
				t.Fatalf("the identity of the opened account: %v", err)
			}
			account, err := h.accounts.Get(h.ctx, id)
			if err != nil {
				t.Fatalf("reading the opened account: %v", err)
			}
			if account.DisplayName != "Ann" {
				t.Errorf("display name = %q, want the provider's %q", account.DisplayName, "Ann")
			}
		})
	})

	t.Run("no provider name", func(t *testing.T) {
		assembleWith(t, []core.Module{noNameSocialModule()}, func(h *harness) {
			writeASetting(t, h.ctx, h.store, testSignupKey, "open")

			_, seq, _ := signupStep(t, h.startSocialSignup())
			if opened := h.answerSignup(seq, "socialperson", socialPassword); opened.status != http.StatusOK {
				t.Fatalf("the username answer answered %d: %s", opened.status, opened.body)
			}
			id, err := h.accounts.ByExternal(h.ctx, "google", "g2")
			if err != nil {
				t.Fatalf("the identity of the opened account: %v", err)
			}
			account, err := h.accounts.Get(h.ctx, id)
			if err != nil {
				t.Fatalf("reading the opened account: %v", err)
			}
			if account.DisplayName != "socialperson" {
				t.Errorf("display name = %q, want the username %q", account.DisplayName, "socialperson")
			}
		})
	})
}
