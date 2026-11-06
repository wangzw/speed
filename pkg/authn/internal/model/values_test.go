package model

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// requireAllSet fails when any field of v, a struct, is its zero value, at any
// depth through pointers, slices and maps. A fixture that passes it cannot
// hide a field a Clone forgot.
func requireAllSet(t *testing.T, name string, v reflect.Value) {
	t.Helper()
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			t.Fatalf("%s is nil", name)
		}
		requireAllSet(t, name, v.Elem())
	case reflect.Slice:
		if v.Len() == 0 {
			t.Fatalf("%s is empty", name)
		}
		for i := range v.Len() {
			requireAllSet(t, name+"[]", v.Index(i))
		}
	case reflect.Map:
		if v.Len() == 0 {
			t.Fatalf("%s is empty", name)
		}
	case reflect.Struct:
		if v.Type() == reflect.TypeOf(time.Time{}) {
			if v.IsZero() {
				t.Fatalf("%s is the zero time", name)
			}
			return
		}
		for i := range v.NumField() {
			requireAllSet(t, name+"."+v.Type().Field(i).Name, v.Field(i))
		}
	default:
		if v.IsZero() {
			t.Fatalf("%s is zero", name)
		}
	}
}

var at = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

func fullPrincipal() Principal {
	return Principal{
		Account: "acct", Actor: "admin", Session: "sess",
		Methods: []Method{MethodPassword, MethodTOTP}, Assurance: AssuranceMulti, IDPAssured: true,
		AuthTime: at, Scope: "acme", Provider: "prov", Attributes: map[string]string{"k": "v"},
	}
}

func fullExternal() External {
	return External{
		Provider: "google", Subject: "s", Email: "e@x", EmailVerified: true, DisplayName: "d",
		PreferredUsername: "pu", Tenant: "t", Attributes: map[string]string{"idp.k": "v"},
	}
}

func fullProof() Proof {
	ext := fullExternal()
	return Proof{
		Account: "acct",
		Identifier: &Identifier{
			ID: "i", Kind: IdentifierPhone, Value: "+86", VerifiedAt: at, VerifiedVia: "sms-code",
			SignIn: true, Contact: true, DefaultContact: true,
		},
		External: &ext, Methods: []Method{MethodSMS}, UserVerified: true, IDPAsserted: true, Provider: "prov",
		Aliases: []ExternalLink{{Provider: "p", Subject: "s", Email: "e", EmailVerified: true}}, PasswordChanged: true,
	}
}

// Clone must carry every field. The fixtures have every field set (checked), so
// a Clone that drops or rewrites one fails DeepEqual.
func TestCloneCarriesEveryField(t *testing.T) {
	p, pr, ps, ex := fullPrincipal(), fullProof(), Pass{Methods: []Method{MethodWebAuthn}, UserVerified: true}, fullExternal()
	requireAllSet(t, "Principal", reflect.ValueOf(p))
	requireAllSet(t, "Proof", reflect.ValueOf(pr))
	requireAllSet(t, "Pass", reflect.ValueOf(ps))
	requireAllSet(t, "External", reflect.ValueOf(ex))
	if got := p.Clone(); !reflect.DeepEqual(got, p) {
		t.Errorf("Principal.Clone = %+v, want %+v", got, p)
	}
	if got := pr.Clone(); !reflect.DeepEqual(got, pr) {
		t.Errorf("Proof.Clone = %+v, want %+v", got, pr)
	}
	if got := ps.Clone(); !reflect.DeepEqual(got, ps) {
		t.Errorf("Pass.Clone = %+v, want %+v", got, ps)
	}
	if got := ex.Clone(); !reflect.DeepEqual(got, ex) {
		t.Errorf("External.Clone = %+v, want %+v", got, ex)
	}

	// The other polarity of every flag: a Clone that forces one true fails here.
	pr = fullProof()
	pr.UserVerified, pr.IDPAsserted, pr.PasswordChanged = false, false, false
	pr.External.EmailVerified, pr.Aliases[0].EmailVerified = false, false
	ex.EmailVerified = false
	p.IDPAssured = false
	if got := p.Clone(); !reflect.DeepEqual(got, p) {
		t.Errorf("Principal.Clone = %+v, want %+v", got, p)
	}
	if got := pr.Clone(); !reflect.DeepEqual(got, pr) {
		t.Errorf("Proof.Clone = %+v, want %+v", got, pr)
	}
	if got := ex.Clone(); !reflect.DeepEqual(got, ex) {
		t.Errorf("External.Clone = %+v, want %+v", got, ex)
	}
}

// Every byte value, alone and in each position of an otherwise valid
// identifier, against the alphabet written out as a literal.
func TestParseAccountIDEveryByte(t *testing.T) {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-"
	for b := range 256 {
		c := string([]byte{byte(b)})
		want := strings.Contains(alphabet, c)
		for _, s := range []string{c, c + "ab", "a" + c + "b", "ab" + c} {
			_, err := ParseAccountID(s)
			if (err == nil) != want {
				t.Errorf("ParseAccountID(%q): accepted = %v, want %v", s, err == nil, want)
			}
		}
	}
}

// The numeric values are what the token's acr claim and the factor table's
// status column carry, and the texts are what the tables' CHECK constraints and
// the client see.
func TestWireValuesAreFrozen(t *testing.T) {
	if strconv.Itoa(int(AssuranceSingle)) != "1" || strconv.Itoa(int(AssuranceMulti)) != "2" {
		t.Errorf("assurance levels read %d and %d, want 1 and 2 (the acr claim's text)", AssuranceSingle, AssuranceMulti)
	}
	if FactorPending != 0 || FactorActive != 1 || FactorDisabled != 2 {
		t.Errorf("factor statuses read %d, %d, %d, want 0, 1, 2 (the status column)", FactorPending, FactorActive, FactorDisabled)
	}
	for _, c := range [][2]string{
		{string(FactorTOTP), "totp"},
		{string(FactorSMS), "sms"},
		{string(FactorWebAuthn), "webauthn"},
		{string(IdentifierUsername), "username"},
		{string(IdentifierEmail), "email"},
		{string(IdentifierPhone), "phone"},
		{FieldText, "text"},
		{FieldPassword, "password"},
		{FieldCode, "code"},
		{FieldPhone, "phone"},
	} {
		got, want := c[0], c[1]
		if got != want {
			t.Errorf("constant reads %q, want %q", got, want)
		}
	}
}
