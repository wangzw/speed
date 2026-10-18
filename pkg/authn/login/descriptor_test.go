package login

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

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
		Label: "Google", OpensAccount: true,
	}}
	r := newRigMethods(t, password, google)
	r.p.signup = true
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
	p := methods[1].(map[string]any)
	if p["kind"] != "form" {
		t.Errorf("the password descriptor kind = %v, want form", p["kind"])
	}
	if v, present := p["opens_account"]; present {
		t.Errorf("the password descriptor carries opens_account = %v, want it absent", v)
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

// TestTheMethodsListingHidesADisabledMethod holds that a method named by the
// disabled-methods switch has no descriptor at all.
func TestTheMethodsListingHidesADisabledMethod(t *testing.T) {
	r := newRigMethods(t, &fakeMethod{name: "password"}, &fakeMethod{name: "google"})
	r.p.settings = readerWithDisabled(t, "password")

	body := decode(t, r.send(http.MethodGet, "/api/v1/auth/methods", "").Body.String())
	methods := body["methods"].([]any)
	if len(methods) != 1 || methods[0].(map[string]any)["name"] != "google" {
		t.Errorf("methods with password disabled = %v, want only google", methods)
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

	r.p.signup = true
	r.mfa.kinds = []authn.FactorKind{authn.FactorTOTP, authn.FactorWebAuthn}
	open := decode(t, r.send(http.MethodGet, "/api/v1/auth/methods", "").Body.String())
	if got := open["signup"].(map[string]any)["enabled"]; got != true {
		t.Errorf("signup.enabled with registration open = %v, want true", got)
	}
	if got := open["mfa"].(map[string]any)["factors"].([]any); !reflect.DeepEqual(got, []any{"totp", "webauthn"}) {
		t.Errorf("mfa.factors with two kinds = %v, want [totp webauthn]", got)
	}
}
