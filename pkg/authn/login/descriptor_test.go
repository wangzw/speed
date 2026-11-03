package login

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// scopedHuman answers a different gate on each surface, so a case can tell the
// two entries of the methods document apart.
type scopedHuman map[authn.HumanScope]authn.HumanVerifier

func (h scopedHuman) verifier(scope authn.HumanScope) authn.HumanVerifier {
	if v, ok := h[scope]; ok {
		return v
	}
	return &fakeHuman{provider: "off"}
}

func (h scopedHuman) Required(scope authn.HumanScope) bool { return h.verifier(scope).Required(scope) }
func (h scopedHuman) Provider(scope authn.HumanScope) string {
	return h.verifier(scope).Provider(scope)
}
func (h scopedHuman) SiteKey(scope authn.HumanScope) string { return h.verifier(scope).SiteKey(scope) }
func (h scopedHuman) Verify(ctx context.Context, p authn.HumanProof, scope authn.HumanScope) error {
	return h.verifier(scope).Verify(ctx, p, scope)
}

// signupStore is a settings store whose registration switch is open and whose
// other declared switch, the disabled methods, names none.
func signupStore() *fakeStore {
	return &fakeStore{values: map[string]string{
		signupPath:          signupOpen,
		disabledMethodsPath: "[]",
	}}
}

// TestTheMethodsListingPublishesMethodDescriptors holds GET B/methods: it
// answers a descriptor per method taking part, in name order, next to the
// registration switch and the kinds of second factor the deployment offers.
func TestTheMethodsListingPublishesMethodDescriptors(t *testing.T) {
	password := &fakeMethod{name: "password", desc: authn.MethodDescriptor{
		Name: "password", Kind: authn.MethodKindForm,
		Fields: []authn.Field{
			{Name: "identifier", Kind: authn.FieldText, Required: true},
			{Name: "password", Kind: authn.FieldPassword, Required: true, Secret: true},
		},
	}}
	google := &fakeMethod{name: "google", desc: authn.MethodDescriptor{
		Name: "google", Kind: authn.MethodKindRedirect, Protocol: authn.MethodProtocolOIDC,
		Label: "Google", OpensAccount: true, BindingProviders: []string{"google"},
	}}
	r := newRigMethods(t, password, google)
	r.p.settings = newReader(signupStore(), r.p.logger)
	r.mfa.kinds = []authn.FactorKind{authn.FactorSMS, authn.FactorTOTP, authn.FactorWebAuthn}

	rec := r.send(http.MethodGet, "/api/v1/auth/methods", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET methods answered %d: %s", rec.Code, rec.Body.String())
	}
	body := decode(t, rec.Body.String())

	methods, ok := body["methods"].([]any)
	if !ok || len(methods) != 2 {
		t.Fatalf("methods = %v, want two descriptors", body["methods"])
	}
	// Name order: google sorts before password.
	if got := methods[0].(map[string]any)["name"]; got != "google" {
		t.Errorf("methods[0].name = %v, want google", got)
	}
	if got := methods[1].(map[string]any)["name"]; got != "password" {
		t.Errorf("methods[1].name = %v, want password", got)
	}

	g := methods[0].(map[string]any)
	if g["kind"] != "redirect" || g["protocol"] != "oidc" || g["label"] != "Google" || g["opens_account"] != true {
		t.Errorf("the google descriptor = %v, want a redirect oidc Google that opens an account", g)
	}
	if got := g["binding_providers"]; !reflect.DeepEqual(got, []any{"google"}) {
		t.Errorf("the google descriptor binding_providers = %v, want [google]", got)
	}
	p := methods[1].(map[string]any)
	if p["kind"] != "form" {
		t.Errorf("the password descriptor kind = %v, want form", p["kind"])
	}
	if v, present := p["opens_account"]; present {
		t.Errorf("the password descriptor carries opens_account = %v, want it absent", v)
	}
	if v, present := p["binding_providers"]; present {
		t.Errorf("the password descriptor carries binding_providers = %v, want it absent", v)
	}
	fields, ok := p["fields"].([]any)
	if !ok || len(fields) != 2 ||
		fields[0].(map[string]any)["name"] != "identifier" ||
		fields[1].(map[string]any)["secret"] != true {
		t.Errorf("the password descriptor fields = %v, want identifier then a secret password", p["fields"])
	}

	if got := body["signup"].(map[string]any)["enabled"]; got != true {
		t.Errorf("signup.enabled = %v, want true", got)
	}
	if got := body["mfa"].(map[string]any)["factors"].([]any); !reflect.DeepEqual(got, []any{"sms", "totp", "webauthn"}) {
		t.Errorf("mfa.factors = %v, want [sms totp webauthn]", got)
	}
}

// TestTheMethodsListingReportsTheHumanGatePerScope holds that the discovery
// document reports the gate once per surface: the phone surface guards the SMS
// sends and the other surface guards every send that does not address a phone
// number, and a deployment may run a different mechanism on each.
func TestTheMethodsListingReportsTheHumanGatePerScope(t *testing.T) {
	r := newRigMethods(t, &fakeMethod{name: "password"})
	r.p.settings = newReader(signupStore(), r.p.logger)
	r.p.human = scopedHuman{
		authn.HumanScopePhone: &fakeHuman{required: true, provider: "captcha"},
		authn.HumanScopeOther: &fakeHuman{required: true, provider: "pow"},
	}

	body := decode(t, r.send(http.MethodGet, "/api/v1/auth/methods", "").Body.String())
	human, ok := body["human"].(map[string]any)
	if !ok {
		t.Fatalf("human = %v, want an object with a phone and an other surface", body["human"])
	}

	phone, ok := human["phone"].(map[string]any)
	if !ok {
		t.Fatalf("human.phone = %v, want the phone surface's gate", human["phone"])
	}
	if phone["required"] != true || phone["provider"] != "captcha" {
		t.Errorf("human.phone = %v, want a required captcha", phone)
	}
	if v, present := phone["site_key"]; present {
		t.Errorf("human.phone carries site_key = %v, want it absent: a captcha has no widget key", v)
	}

	other, ok := human["other"].(map[string]any)
	if !ok {
		t.Fatalf("human.other = %v, want the other surface's gate", human["other"])
	}
	if other["required"] != true || other["provider"] != "pow" {
		t.Errorf("human.other = %v, want a required proof of work", other)
	}
}

// TestTheMethodsListingReportsNoGateAsOffOnEverySurface holds that no seam and
// the off mechanism read the same: every surface names off and requires
// nothing, because in both cases a caller presents no proof.
func TestTheMethodsListingReportsNoGateAsOffOnEverySurface(t *testing.T) {
	r := newRigMethods(t, &fakeMethod{name: "password"})
	r.p.settings = newReader(signupStore(), r.p.logger)

	body := decode(t, r.send(http.MethodGet, "/api/v1/auth/methods", "").Body.String())
	human, ok := body["human"].(map[string]any)
	if !ok {
		t.Fatalf("human = %v, want an object with a phone and an other surface", body["human"])
	}
	for _, scope := range []string{"phone", "other"} {
		surface, ok := human[scope].(map[string]any)
		if !ok {
			t.Fatalf("human.%s = %v, want the surface's gate", scope, human[scope])
		}
		if surface["required"] != false || surface["provider"] != "off" {
			t.Errorf("human.%s = %v, want the off mechanism requiring nothing", scope, surface)
		}
	}
}

// TestTheMethodsListingHidesADisabledMethod holds that a method named by the
// disabled-methods switch has no descriptor at all.
func TestTheMethodsListingHidesADisabledMethod(t *testing.T) {
	r := newRigMethods(t, &fakeMethod{name: "password"}, &fakeMethod{name: "google"})
	r.p.settings = readerWithDisabled(t, "google")

	body := decode(t, r.send(http.MethodGet, "/api/v1/auth/methods", "").Body.String())
	methods := body["methods"].([]any)
	if len(methods) != 1 || methods[0].(map[string]any)["name"] != "password" {
		t.Errorf("methods with google disabled = %v, want only password", methods)
	}
}

// TestTheMethodsListingFollowsTheAssembly holds that the published registration
// switch and factor kinds are the deployment's, read from what the assembly took
// in.
func TestTheMethodsListingFollowsTheAssembly(t *testing.T) {
	r := newRig(t, &fakeMethod{name: "password"})

	closed := decode(t, r.send(http.MethodGet, "/api/v1/auth/methods", "").Body.String())
	if got := closed["signup"].(map[string]any)["enabled"]; got != false {
		t.Errorf("signup.enabled with registration closed = %v, want false", got)
	}
	if got := closed["mfa"].(map[string]any)["factors"].([]any); !reflect.DeepEqual(got, []any{"totp"}) {
		t.Errorf("mfa.factors with one kind = %v, want [totp]", got)
	}

	r.p.settings = newReader(signupStore(), r.p.logger)
	r.mfa.kinds = []authn.FactorKind{authn.FactorTOTP, authn.FactorWebAuthn}
	open := decode(t, r.send(http.MethodGet, "/api/v1/auth/methods", "").Body.String())
	if got := open["signup"].(map[string]any)["enabled"]; got != true {
		t.Errorf("signup.enabled with registration open = %v, want true", got)
	}
	if got := open["mfa"].(map[string]any)["factors"].([]any); !reflect.DeepEqual(got, []any{"totp", "webauthn"}) {
		t.Errorf("mfa.factors with two kinds = %v, want [totp webauthn]", got)
	}
}

// TestTheMethodsListingReportsBothRegistrationSwitches holds the two members of
// the discovery document that carry registration: signup.enabled is true when
// either the first-login switch or the register route's switch is open, and
// signup.password.enabled reports the register route alone. A method that could
// open an account is not shown as able to while the first-login switch is closed,
// and the password descriptor never carries opens_account at all.
func TestTheMethodsListingReportsBothRegistrationSwitches(t *testing.T) {
	cases := []struct {
		name            string
		signup          string
		password        string
		enabled         bool
		passwordEnabled bool
	}{
		{"both closed", signupClosed, signupClosed, false, false},
		{"a first login may open an account", signupOpen, signupClosed, true, false},
		{"the register route alone", signupClosed, signupOpen, true, true},
		{"both open", signupOpen, signupOpen, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			password := &fakeMethod{name: "password", desc: authn.MethodDescriptor{
				Name: "password", Kind: authn.MethodKindForm,
			}}
			google := &fakeMethod{name: "google", desc: authn.MethodDescriptor{
				Name: "google", Kind: authn.MethodKindRedirect, Protocol: authn.MethodProtocolOIDC,
				Label: "Google", OpensAccount: true,
			}}
			r := newRigMethods(t, password, google)
			r.p.settings = readerWith(map[string]string{
				signupPath:         c.signup,
				passwordSignupPath: c.password,
			})

			body := decode(t, r.send(http.MethodGet, "/api/v1/auth/methods", "").Body.String())
			signup, ok := body["signup"].(map[string]any)
			if !ok {
				t.Fatalf("the listing carries no signup member: %v", body)
			}
			if got := signup["enabled"]; got != c.enabled {
				t.Errorf("signup.enabled = %v, want %v", got, c.enabled)
			}
			published, ok := signup["password"].(map[string]any)
			if !ok {
				t.Fatalf("signup.password is %v, want an object", signup["password"])
			}
			if got := published["enabled"]; got != c.passwordEnabled {
				t.Errorf("signup.password.enabled = %v, want %v", got, c.passwordEnabled)
			}

			methods := body["methods"].([]any)
			// Name order: google sorts before password.
			googleView := methods[0].(map[string]any)
			opens, present := googleView["opens_account"]
			if want := c.signup == signupOpen; present != want || (present && opens != true) {
				t.Errorf("the google descriptor carries opens_account = %v, present %v, want present %v and true",
					opens, present, want)
			}
			passwordView := methods[1].(map[string]any)
			if v, present := passwordView["opens_account"]; present {
				t.Errorf("the password descriptor carries opens_account = %v, want it absent", v)
			}
		})
	}
}
