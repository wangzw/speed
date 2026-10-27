package model

import (
	"net/url"
	"reflect"
	"testing"
	"time"
)

// TestTheZeroValuesRefuse holds the reading of each new type's zero value, which
// the design fixes in the direction of refusing: an account nobody enabled is
// disabled, a membership nobody activated is suspended and reads as not a member,
// a session status nobody filled in is not active, no multi-factor outcome is
// asked for, and an identifier verification names no way.
func TestTheZeroValuesRefuse(t *testing.T) {
	var s AccountStatus
	if s != StatusDisabled || s == StatusActive || s == StatusDeleted {
		t.Errorf("the zero AccountStatus is %d, want StatusDisabled", s)
	}
	if got := (Account{}).Status; got != StatusDisabled {
		t.Errorf("the zero Account has status %d, want StatusDisabled", got)
	}
	if (Membership{}).Active {
		t.Error("the zero Membership is active, want suspended, which reads as not a member")
	}
	if (SessionStatus{}).Active {
		t.Error("the zero SessionStatus is active")
	}
	if MFANone != 0 || (MFAOutcome(0)) != MFANone {
		t.Errorf("MFANone = %d, want the zero value", MFANone)
	}
	if (SSOProvider{}).Enabled {
		t.Error("the zero SSOProvider is enabled, want disabled")
	}
	if (Verification{}).Valid() || (Verification{}).Proves() {
		t.Error("the zero Verification is valid or proves ownership")
	}
	if (AccountKind("")).Valid() || (SessionKind("")).Valid() {
		t.Error("the empty kind is in a closed set")
	}
	if (MFAResult{}).Assurance != AssuranceNone || (MFAResult{}).IDPAssured {
		t.Error("the zero MFAResult states a level")
	}
}

// TestAccountStatusReadsOneWayEveryWay pins the numbers of the status column and
// the names an event payload writes, and that a value outside the set is neither
// valid nor one of the three names.
func TestAccountStatusReadsOneWayEveryWay(t *testing.T) {
	for _, c := range []struct {
		s    AccountStatus
		n    uint8
		name string
	}{
		{StatusDisabled, 0, "disabled"},
		{StatusActive, 1, "active"},
		{StatusDeleted, 2, "deleted"},
	} {
		if uint8(c.s) != c.n || c.s.String() != c.name || !c.s.Valid() {
			t.Errorf("status %d reads %q valid=%v, want %d %q valid", c.s, c.s.String(), c.s.Valid(), c.n, c.name)
		}
	}
	for _, s := range []AccountStatus{3, 7, 255} {
		if s.Valid() || s.String() != "unknown" {
			t.Errorf("status %d is valid=%v and reads %q, want invalid and unknown", s, s.Valid(), s.String())
		}
	}
}

// TestSignupStateValidIsTheClosedSet holds the three values the signup_state
// column admits: none, pending and verified, and nothing above them.
func TestSignupStateValidIsTheClosedSet(t *testing.T) {
	for _, c := range []struct {
		s     SignupState
		valid bool
	}{
		{SignupNone, true},
		{SignupPending, true},
		{SignupVerified, true},
		{SignupState(3), false},
		{SignupState(255), false},
	} {
		if c.s.Valid() != c.valid {
			t.Errorf("SignupState(%d).Valid() = %v, want %v", c.s, c.s.Valid(), c.valid)
		}
	}
}

// TestTheKindsAreClosedSets holds the texts of the kinds, which the tables'
// CHECK constraints carry, and that Valid accepts them and nothing near them.
func TestTheKindsAreClosedSets(t *testing.T) {
	for _, c := range []struct {
		got   string
		want  string
		valid bool
	}{
		{string(KindUser), "user", AccountKind("user").Valid()},
		{string(KindService), "service", AccountKind("service").Valid()},
		{string(SessionBrowser), "browser", SessionKind("browser").Valid()},
		{string(SessionToken), "token", SessionKind("token").Valid()},
		{string(SessionService), "service", SessionKind("service").Valid()},
	} {
		if c.got != c.want || !c.valid {
			t.Errorf("a kind reads %q valid=%v, want %q valid", c.got, c.valid, c.want)
		}
	}
	for _, k := range []string{"", "User", "USER", "admin", "user ", "browse", "Browser", "refresh"} {
		if AccountKind(k).Valid() {
			t.Errorf("AccountKind(%q) is valid", k)
		}
		if SessionKind(k).Valid() {
			t.Errorf("SessionKind(%q) is valid", k)
		}
	}
}

// TestVerificationAcceptsTheClosedSetOfWays holds the forms the design lists for
// verified_via and which of them prove ownership: a code sent to the phone, a
// delivered invitation and an IdP's declaration do, an administrator's word and
// an unverified import do not, and a value outside the set is neither.
func TestVerificationAcceptsTheClosedSetOfWays(t *testing.T) {
	for _, c := range []struct {
		via    string
		valid  bool
		proves bool
	}{
		{"sms-code", true, true},
		{"email-code", true, true},
		{"invite", true, true},
		{"idp:0198f0aa-0000-7000-8000-000000000001", true, true},
		{"idp:google", true, true},
		{"admin", true, false},
		{"import-unverified", true, false},
		{"self", true, false},
		{"", false, false},
		{"idp:", false, false},
		{"idp", false, false},
		{"idp: x", false, false},
		{"idp:a/b", false, false},
		{"idp:a@b", false, false},
		{"idp:a:b", false, false},
		{"IDP:google", false, false},
		{"SMS-code", false, false},
		{"sms-code ", false, false},
		{"email-code ", false, false},
		{"E-mail-code", false, false},
		{"Self", false, false},
		{"import", false, false},
		{"unverified", false, false},
		{"jit", false, false},
	} {
		v := Verification{Via: c.via}
		if v.Valid() != c.valid {
			t.Errorf("Verification{%q}.Valid() = %v, want %v", c.via, v.Valid(), c.valid)
		}
		if v.Proves() != c.proves {
			t.Errorf("Verification{%q}.Proves() = %v, want %v", c.via, v.Proves(), c.proves)
		}
	}
	if long := "idp:" + string(repeatByte('a', 64)); !(Verification{Via: long}).Valid() {
		t.Error("an idp: provider of 64 bytes is refused")
	}
	if long := "idp:" + string(repeatByte('a', 65)); (Verification{Via: long}).Valid() {
		t.Error("an idp: provider of 65 bytes is accepted")
	}
}

func repeatByte(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

// TestThePageSizeIsTheDefaultOrAtMostTheCeiling holds how a listing reads its
// Limit: not positive reads as the default, above the ceiling reads as the
// ceiling, and anything between is taken as it is.
func TestThePageSizeIsTheDefaultOrAtMostTheCeiling(t *testing.T) {
	for _, c := range []struct{ in, want int }{
		{0, 100},
		{-1, 100},
		{-1 << 62, 100},
		{1, 1},
		{99, 99},
		{100, 100},
		{101, 101},
		{999, 999},
		{1000, 1000},
		{1001, 1000},
		{1 << 40, 1000},
	} {
		if got := (Page{Limit: c.in}).EffectiveLimit(); got != c.want {
			t.Errorf("Page{Limit: %d}.EffectiveLimit() = %d, want %d", c.in, got, c.want)
		}
		if got := (AccountQuery{Limit: c.in}).EffectiveLimit(); got != c.want {
			t.Errorf("AccountQuery{Limit: %d}.EffectiveLimit() = %d, want %d", c.in, got, c.want)
		}
		if got := (GrantQuery{Limit: c.in}).EffectiveLimit(); got != c.want {
			t.Errorf("GrantQuery{Limit: %d}.EffectiveLimit() = %d, want %d", c.in, got, c.want)
		}
	}
	if DefaultPageLimit != 100 || MaxPageLimit != 1000 {
		t.Errorf("the page bounds read %d and %d, want 100 and 1000", DefaultPageLimit, MaxPageLimit)
	}
}

// TestTheEndReasonsAreTheDesignsEight holds the closed set of why an
// impersonation record ends, by text.
func TestTheEndReasonsAreTheDesignsEight(t *testing.T) {
	want := []string{
		"explicit", "revoked", "expired", "session-ended", "actor-disabled",
		"target-disabled", "membership-removed", "start-failed",
	}
	got := []string{
		EndExplicit, EndRevoked, EndExpired, EndSessionEnded, EndActorDisabled,
		EndTargetDisabled, EndMembershipRemoved, EndStartFailed,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the End constants read %q, want %q", got, want)
	}
	for _, r := range want {
		if !ValidEndReason(r) {
			t.Errorf("ValidEndReason(%q) = false", r)
		}
	}
	for _, r := range []string{"", "Explicit", "explicit ", "actor", "disabled", "ended", "timeout", "start_failed"} {
		if ValidEndReason(r) {
			t.Errorf("ValidEndReason(%q) = true", r)
		}
	}
}

// TestTheClosedStringSetsAreFrozen pins the texts of the purposes, the input
// actions, the protocols and the ways of provisioning.
func TestTheClosedStringSetsAreFrozen(t *testing.T) {
	for _, c := range [][2]string{
		{PurposeLogin, "login"},
		{PurposeLink, "link"},
		{PurposeStepUp, "step-up"},
		{PurposeEnroll, "enroll"},
		{InputSubmit, "submit"},
		{InputCallback, "callback"},
		{SMSPurposeLogin, "login"},
		{SMSPurposeMFA, "mfa"},
		{SMSPurposeEnroll, "enroll"},
		{SMSPurposeReset, "reset"},
		{SSOProtocolOIDC, "oidc"},
		{SSOProtocolSAML, "saml"},
		{ProvisioningJIT, "jit"},
		{ProvisioningPreprovisioned, "preprovisioned"},
		{VerifiedViaSMSCode, "sms-code"},
		{VerifiedViaEmailCode, "email-code"},
		{VerifiedViaIDPPrefix, "idp:"},
		{VerifiedViaAdmin, "admin"},
		{VerifiedViaImportUnverified, "import-unverified"},
		{VerifiedViaSelf, "self"},
	} {
		if c[0] != c[1] {
			t.Errorf("constant reads %q, want %q", c[0], c[1])
		}
	}
}

// fullAccount and the fixtures after it have every field set, so a Clone that
// forgets one fails DeepEqual, and every map, slice and pointer holds something,
// so a Clone that shares one is caught by the walk below.
func fullAccount() Account {
	return Account{
		ID: "a", Kind: KindService, Status: StatusActive, DisplayName: "n", MFARequired: true, Protected: true,
		PasswordSet: true, Attributes: map[string]string{"k": "v"}, ValidAfter: at, CreatedAt: at,
	}
}

func fullCreateAccount() CreateAccount {
	return CreateAccount{
		ID: "a", Kind: KindUser, DisplayName: "n", Username: "u", UsernameVerifiedVia: Verification{Via: "admin"},
		Email: "e@x", EmailVerifiedVia: Verification{Via: "admin"},
		Phone: "+8613800000000", PhoneVerifiedVia: Verification{Via: "sms-code"}, Password: "p", PasswordHash: "h",
		MustChangePassword: true, MFARequired: true, Attributes: map[string]string{"k": "v"}, Via: "admin",
	}
}

func fullQuery() AccountQuery {
	s := StatusActive
	return AccountQuery{Status: &s, Kind: KindUser, IdentifierKind: IdentifierEmail, IdentifierPrefix: "abc", DisplayNamePrefix: "Al", After: "x", Limit: 5}
}

func fullNewSession() NewSession {
	return NewSession{
		Account: "a", Kind: SessionBrowser, Methods: []Method{MethodPassword, MethodTOTP}, Assurance: AssuranceMulti,
		IDPAssured: true, AuthTime: at, Provider: "p", Attributes: map[string]string{"idp.k": "v"},
		Client: ClientInfo{IP: "192.0.2.1", UserAgent: "ua"}, AbsoluteTTL: time.Hour,
	}
}

func fullSessionStatus() SessionStatus {
	return SessionStatus{
		Active: true, Account: "a", Kind: SessionToken, Methods: []Method{MethodPassword}, Assurance: AssuranceSingle,
		IDPAssured: true, AuthTime: at, Provider: "p", Attributes: map[string]string{"idp.k": "v"},
		ImpersonationID: "i", CredentialID: "c", CreatedAt: at, ExpiresAt: at,
	}
}

func fullElevation() Elevation {
	return Elevation{Methods: []Method{MethodPassword, MethodWebAuthn}, Assurance: AssuranceMulti, IDPAssured: true, AuthTime: at}
}

func fullIssueToken() IssueToken {
	return IssueToken{
		Account: "a", Actor: "b", Session: "s", Methods: []Method{MethodPassword}, Assurance: AssuranceSingle,
		AuthTime: at, ImpersonationID: "i", Scope: "t", ClientID: "c", TTL: time.Minute,
	}
}

func fullStart() Start {
	return Start{
		Purpose: PurposeStepUp, Account: "a", Fields: map[string]string{"k": "v"},
		Client: ClientInfo{IP: "192.0.2.1", UserAgent: "ua"}, SendsLeft: 3, Locale: "en-US",
	}
}

func fullFlowState() FlowState {
	return FlowState{ID: "f", Purpose: PurposeLogin, Step: "s", Provider: "p", Account: "a", Session: "s", Private: []byte{1, 2}, SendsLeft: 2, Locale: "zh-CN", Source: "192.0.2.1"}
}

func fullStepInput() StepInput {
	return StepInput{
		Action: InputCallback, Fields: map[string]string{"k": "v"},
		Query: url.Values{"code": {"c1", "c2"}}, Form: url.Values{"SAMLResponse": {"r"}},
		Client: ClientInfo{IP: "192.0.2.1", UserAgent: "ua"},
	}
}

func fullSSOProvider() SSOProvider {
	return SSOProvider{
		ID: "i", Tenant: "t", Protocol: SSOProtocolOIDC, Name: "n", Enabled: true, Provisioning: ProvisioningJIT,
		TrustIDPMFA: true, AllowIDPInitiated: true, AllowSHA1: true, MergeExisting: true,
		Config: []byte(`{"a":1}`), Version: 3,
	}
}

// shares reports the path of the first map, slice or pointer that a and b hold
// in common, which is where a copy still reaches the original. It walks both
// values in step.
func shares(path string, a, b reflect.Value) (string, bool) {
	switch a.Kind() {
	case reflect.Map:
		if a.IsNil() {
			return "", false
		}
		if a.Pointer() == b.Pointer() {
			return path, true
		}
		for _, k := range a.MapKeys() {
			if p, ok := shares(path+"["+k.String()+"]", a.MapIndex(k), b.MapIndex(k)); ok {
				return p, true
			}
		}
	case reflect.Slice:
		if a.Len() == 0 {
			return "", false
		}
		if a.Pointer() == b.Pointer() {
			return path, true
		}
		for i := range a.Len() {
			if p, ok := shares(path+"[]", a.Index(i), b.Index(i)); ok {
				return p, true
			}
		}
	case reflect.Pointer:
		if a.IsNil() {
			return "", false
		}
		if a.Pointer() == b.Pointer() {
			return path, true
		}
		return shares(path+"*", a.Elem(), b.Elem())
	case reflect.Struct:
		for i := range a.NumField() {
			if !a.Type().Field(i).IsExported() {
				continue
			}
			if p, ok := shares(path+"."+a.Type().Field(i).Name, a.Field(i), b.Field(i)); ok {
				return p, true
			}
		}
	}
	return "", false
}

// TestCloneSharesNothingWithTheOriginal is the property of every Clone of this
// package: the copy is equal to the original, no map, slice or pointer is the
// same one, and writing into the original's after the copy was taken does not
// change the copy. The walk is over the fields as reflection finds them, so a
// field added to a type later is covered without touching this test.
func TestCloneSharesNothingWithTheOriginal(t *testing.T) {
	type cloner struct {
		name  string
		full  any
		clone func(any) any
	}
	for _, c := range []cloner{
		{"Account", fullAccount(), func(v any) any { return v.(Account).Clone() }},
		{"CreateAccount", fullCreateAccount(), func(v any) any { return v.(CreateAccount).Clone() }},
		{"AccountQuery", fullQuery(), func(v any) any { return v.(AccountQuery).Clone() }},
		{"NewSession", fullNewSession(), func(v any) any { return v.(NewSession).Clone() }},
		{"SessionStatus", fullSessionStatus(), func(v any) any { return v.(SessionStatus).Clone() }},
		{"Elevation", fullElevation(), func(v any) any { return v.(Elevation).Clone() }},
		{"IssueToken", fullIssueToken(), func(v any) any { return v.(IssueToken).Clone() }},
		{"Start", fullStart(), func(v any) any { return v.(Start).Clone() }},
		{"FlowState", fullFlowState(), func(v any) any { return v.(FlowState).Clone() }},
		{"StepInput", fullStepInput(), func(v any) any { return v.(StepInput).Clone() }},
		{"SSOProvider", fullSSOProvider(), func(v any) any { return v.(SSOProvider).Clone() }},
	} {
		t.Run(c.name, func(t *testing.T) {
			requireAllSet(t, c.name, reflect.ValueOf(c.full))
			got := c.clone(c.full)
			if !reflect.DeepEqual(got, c.full) {
				t.Fatalf("Clone = %+v, want %+v", got, c.full)
			}
			if p, ok := shares(c.name, reflect.ValueOf(c.full), reflect.ValueOf(got)); ok {
				t.Errorf("the copy shares %s with the original", p)
			}
		})
	}

	// Writing through the original after the copy leaves the copy alone, for
	// each shape of field once: a map, a slice of values, a slice of bytes, a
	// pointer, and url.Values, a map of slices.
	a := fullAccount()
	ac := a.Clone()
	a.Attributes["k"] = "changed"
	if ac.Attributes["k"] != "v" {
		t.Error("a write into the original's map reached the copy of an Account")
	}
	ns := fullNewSession()
	nc := ns.Clone()
	ns.Methods[0] = MethodSMS
	if nc.Methods[0] != MethodPassword {
		t.Error("a write into the original's slice reached the copy of a NewSession")
	}
	fs := fullFlowState()
	fc := fs.Clone()
	fs.Private[0] = 99
	if fc.Private[0] != 1 {
		t.Error("a write into the original's bytes reached the copy of a FlowState")
	}
	q := fullQuery()
	qc := q.Clone()
	*q.Status = StatusDeleted
	if *qc.Status != StatusActive {
		t.Error("a write through the original's pointer reached the copy of an AccountQuery")
	}
	in := fullStepInput()
	ic := in.Clone()
	in.Query["code"][0] = "changed"
	in.Form["SAMLResponse"][0] = "changed"
	if ic.Query["code"][0] != "c1" || ic.Form["SAMLResponse"][0] != "r" {
		t.Error("a write into the original's url.Values reached the copy of a StepInput")
	}
	sp := fullSSOProvider()
	sc := sp.Clone()
	sp.Config[0] = 'X'
	if sc.Config[0] != '{' {
		t.Error("a write into the original's raw JSON reached the copy of an SSOProvider")
	}
}

// TestCloneKeepsWhatIsNilNil holds that a Clone of the zero value is the zero
// value: a nil map that became an empty one would read differently where the
// difference matters, such as an attribute set that was never given.
func TestCloneKeepsWhatIsNilNil(t *testing.T) {
	for name, equal := range map[string]bool{
		"Account":       reflect.DeepEqual(Account{}.Clone(), Account{}),
		"CreateAccount": reflect.DeepEqual(CreateAccount{}.Clone(), CreateAccount{}),
		"AccountQuery":  reflect.DeepEqual(AccountQuery{}.Clone(), AccountQuery{}),
		"NewSession":    reflect.DeepEqual(NewSession{}.Clone(), NewSession{}),
		"SessionStatus": reflect.DeepEqual(SessionStatus{}.Clone(), SessionStatus{}),
		"Elevation":     reflect.DeepEqual(Elevation{}.Clone(), Elevation{}),
		"IssueToken":    reflect.DeepEqual(IssueToken{}.Clone(), IssueToken{}),
		"Start":         reflect.DeepEqual(Start{}.Clone(), Start{}),
		"FlowState":     reflect.DeepEqual(FlowState{}.Clone(), FlowState{}),
		"StepInput":     reflect.DeepEqual(StepInput{}.Clone(), StepInput{}),
		"SSOProvider":   reflect.DeepEqual(SSOProvider{}.Clone(), SSOProvider{}),
	} {
		if !equal {
			t.Errorf("the Clone of a zero %s is not the zero value", name)
		}
	}
}

// hasReference reports whether a type holds a map, a slice or a pointer at any
// depth through structs, which is what makes a plain copy of a value share.
func hasReference(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Map, reflect.Slice, reflect.Pointer:
		return true
	case reflect.Struct:
		if t == reflect.TypeFor[time.Time]() {
			return false
		}
		for f := range t.Fields() {
			if hasReference(f.Type) {
				return true
			}
		}
	}
	return false
}

// TestEveryTypeWithAReferenceHasAClone holds the rule that a record holding a
// map, a slice or a pointer carries the Clone that copies it, and that a record
// without any has none to forget. A type added with a map and no Clone fails here.
func TestEveryTypeWithAReferenceHasAClone(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeFor[Account](), reflect.TypeFor[CreateAccount](), reflect.TypeFor[Verification](),
		reflect.TypeFor[SignupStatus](), reflect.TypeFor[RegisterAccount](),
		reflect.TypeFor[AccountQuery](), reflect.TypeFor[ExternalBinding](), reflect.TypeFor[ServiceSecretInfo](),
		reflect.TypeFor[Membership](), reflect.TypeFor[Page](), reflect.TypeFor[VerifyResult](),
		reflect.TypeFor[SetPasswordOptions](), reflect.TypeFor[ClientInfo](), reflect.TypeFor[NewSession](),
		reflect.TypeFor[Issued](), reflect.TypeFor[SessionInfo](), reflect.TypeFor[SessionStatus](),
		reflect.TypeFor[Elevation](), reflect.TypeFor[Start](), reflect.TypeFor[FlowState](),
		reflect.TypeFor[StepInput](), reflect.TypeFor[MFAResult](), reflect.TypeFor[ImpersonationRequest](),
		reflect.TypeFor[ImpersonationGrant](), reflect.TypeFor[GrantQuery](), reflect.TypeFor[IssueToken](),
		reflect.TypeFor[AccessToken](), reflect.TypeFor[RefreshToken](), reflect.TypeFor[SendCode](),
		reflect.TypeFor[SSOProvider](),
		// the types that were already here
		reflect.TypeFor[Principal](), reflect.TypeFor[Proof](), reflect.TypeFor[Pass](), reflect.TypeFor[External](),
	} {
		_, has := typ.MethodByName("Clone")
		if hasReference(typ) && !has {
			t.Errorf("%s holds a map, slice or pointer and has no Clone", typ.Name())
		}
		if !hasReference(typ) && has {
			t.Errorf("%s holds no reference and has a Clone, which would copy nothing", typ.Name())
		}
	}
}
