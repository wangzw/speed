package authntest_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
)

// recorder stands in for the *testing.T a test handed to Context; only the two
// methods Context is allowed to call are given, so any other call reaches the
// nil embedded interface and panics.
type recorder struct {
	testing.TB
	failed  bool
	message string
}

func (r *recorder) Helper() {}

func (r *recorder) Fatalf(format string, args ...any) {
	r.failed = true
	r.message = fmt.Sprintf(format, args...)
}

func build(opts ...authntest.Option) (context.Context, *recorder) {
	r := &recorder{}
	return authntest.Context(r, context.Background(), opts...), r
}

func mustBuild(t *testing.T, opts ...authntest.Option) (authn.Identity, authn.Access) {
	t.Helper()
	ctx := authntest.Context(t, context.Background(), opts...)
	id, ok := authn.FromContext(ctx)
	if !ok {
		t.Fatal("the context Context returned carries no identity")
	}
	return id, authn.AccessFromContext(ctx)
}

const (
	sessionID = authn.SessionID("0190a6f2-3b6c-7c2e-8a4d-5e6f7a8b9c0d")
	providerA = "0190a6f2-3b6c-7c2e-8a4d-5e6f7a8b9c0e"
)

// attributesOf returns sixteen attributes whose keys and values together are
// exactly size bytes, each key three bytes, so the total is the only bound a
// case near 4 KiB can cross.
func attributesOf(size int) map[string]string {
	const entries = 16
	m := make(map[string]string, entries)
	rest := size - entries*3
	for i := range entries {
		n := rest / (entries - i)
		m[fmt.Sprintf("k%02d", i)] = strings.Repeat("v", n)
		rest -= n
	}
	return m
}

// TestEachOptionIsReadBackThroughTheRootPackage holds every option to the
// reader the production code uses.
func TestEachOptionIsReadBackThroughTheRootPackage(t *testing.T) {
	authTime := time.Date(2026, time.March, 4, 5, 6, 7, 0, time.UTC)
	impersonation := []authntest.Option{
		authntest.WithActor("acct-admin"), authntest.WithSession(sessionID), authntest.WithScope("tenant-a"),
	}

	for _, c := range []struct {
		name string
		opts []authntest.Option
		read func(authn.Identity, authn.Access) any
		want any
	}{
		{
			"account",
			[]authntest.Option{authntest.WithAccount("acct-7")},
			func(i authn.Identity, _ authn.Access) any { return i.Account() }, authn.AccountID("acct-7"),
		},
		{
			"actor", impersonation,
			func(i authn.Identity, _ authn.Access) any { return i.Actor() }, authn.AccountID("acct-admin"),
		},
		{
			"session",
			[]authntest.Option{authntest.WithSession(sessionID)},
			func(i authn.Identity, _ authn.Access) any { return i.Session() }, sessionID,
		},
		{
			"methods",
			[]authntest.Option{authntest.WithMethods(authn.MethodPassword, authn.MethodTOTP)},
			func(i authn.Identity, _ authn.Access) any { return fmt.Sprint(i.Methods()) },
			fmt.Sprint([]authn.Method{authn.MethodPassword, authn.MethodTOTP}),
		},
		{
			"assurance",
			[]authntest.Option{authntest.WithAssurance(authn.AssuranceMulti)},
			func(i authn.Identity, _ authn.Access) any { return i.Assurance() }, authn.AssuranceMulti,
		},
		{
			"idp assured",
			[]authntest.Option{
				authntest.WithAssurance(authn.AssuranceMulti), authntest.WithProvider(providerA), authntest.WithIDPAssured(true),
			},
			func(i authn.Identity, _ authn.Access) any { return i.AssuranceFromIDP() }, true,
		},
		{
			"auth time",
			[]authntest.Option{authntest.WithAuthTime(authTime)},
			func(i authn.Identity, _ authn.Access) any { return i.AuthTime() }, authTime,
		},
		{
			"backend",
			[]authntest.Option{authntest.WithBackend("session")},
			func(i authn.Identity, _ authn.Access) any { return i.Backend() }, "session",
		},
		{
			"provider",
			[]authntest.Option{authntest.WithProvider(providerA)},
			func(i authn.Identity, _ authn.Access) any { return i.Provider() }, providerA,
		},
		{
			"scope",
			[]authntest.Option{
				authntest.WithActor("acct-admin"), authntest.WithSession(sessionID), authntest.WithScope("tenant-b"),
			},
			func(i authn.Identity, _ authn.Access) any { s, _ := i.Scope(); return s }, "tenant-b",
		},
		{
			"attribute",
			[]authntest.Option{authntest.WithAttributes(map[string]string{"region": "eu"})},
			func(i authn.Identity, _ authn.Access) any { v, _ := i.Attribute("region"); return v }, "eu",
		},
		{
			"access optional",
			[]authntest.Option{authntest.WithAccess(authn.AccessOptional)},
			func(_ authn.Identity, a authn.Access) any { return a }, authn.AccessOptional,
		},
		{
			"challenge cookie",
			[]authntest.Option{authntest.WithChallenge("Cookie")},
			func(i authn.Identity, _ authn.Access) any { return i.Challenge() }, "Cookie",
		},
		{
			"challenge basic",
			[]authntest.Option{authntest.WithChallenge("Basic")},
			func(i authn.Identity, _ authn.Access) any { return i.Challenge() }, "Basic",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			id, access := mustBuild(t, c.opts...)
			if got := c.read(id, access); got != c.want {
				t.Errorf("the option %q reads back as %v, want %v", c.name, got, c.want)
			}
		})
	}
}

// TestAnImpersonationIsKeyedByItsActor holds that the key is the actor, not the account.
func TestAnImpersonationIsKeyedByItsActor(t *testing.T) {
	id, _ := mustBuild(t,
		authntest.WithAccount("acct-user"),
		authntest.WithActor("acct-admin"),
		authntest.WithSession(sessionID),
		authntest.WithScope("tenant-a"),
	)
	if id.Key() != string(id.Actor()) {
		t.Errorf("Key() = %q, Actor() = %q: the accountability key is always the actor", id.Key(), id.Actor())
	}
	if id.Key() != "acct-admin" || id.Account() != "acct-user" {
		t.Errorf("Key() = %q and Account() = %q, want acct-admin and acct-user", id.Key(), id.Account())
	}
	if id.Key() == string(id.Account()) {
		t.Error("the key equals the impersonated account, so its allowance would be charged for the actor")
	}
	if !id.Impersonating() {
		t.Error("an identity with an actor different from the account does not read as impersonating")
	}
}

// TestUnsaidFieldsTakeTheDocumentedDefaults pins the defaults the godoc
// states.
func TestUnsaidFieldsTakeTheDocumentedDefaults(t *testing.T) {
	id, access := mustBuild(t)

	if id.Account() != "test-account" || id.Account() != authntest.DefaultAccount {
		t.Errorf("the default account is %q, want test-account", id.Account())
	}
	if id.Actor() != id.Account() || id.Impersonating() {
		t.Errorf("the default identity names the actor %q beside the account %q", id.Actor(), id.Account())
	}
	if id.Key() != "test-account" {
		t.Errorf("the default key is %q, want the account", id.Key())
	}
	if got := id.Methods(); !slices.Equal(got, []authn.Method{authn.MethodPassword}) {
		t.Errorf("the default methods are %v, want the password alone", got)
	}
	if id.Assurance() != authn.AssuranceSingle {
		t.Errorf("the default assurance is %v, want single", id.Assurance())
	}
	if id.AssuranceFromIDP() {
		t.Error("the default identity says its assurance is an IdP's declaration")
	}
	want := time.Date(2026, time.October, 1, 8, 0, 0, 0, time.UTC)
	if !id.AuthTime().Equal(want) || id.AuthTime().Location() != time.UTC || !authntest.DefaultAuthTime().Equal(want) {
		t.Errorf("the default authentication time is %v, want %v in UTC", id.AuthTime(), want)
	}
	if id.Backend() != "authntest" || id.Backend() != authntest.DefaultBackend {
		t.Errorf("the default backend is %q, want authntest", id.Backend())
	}
	if id.Challenge() != "Bearer" || authntest.DefaultChallenge != "Bearer" {
		t.Errorf("the default challenge is %q, want Bearer", id.Challenge())
	}
	if access != authn.AccessRequired {
		t.Errorf("the default access level is %v, want required", access)
	}
	if id.Session() != "" || id.Provider() != "" {
		t.Errorf("the default identity carries the session %q and the provider %q", id.Session(), id.Provider())
	}
	if scope, ok := id.Scope(); ok || scope != "" {
		t.Errorf("the default identity is confined to the scope %q", scope)
	}
	if got := id.Attributes(); got == nil || len(got) != 0 {
		t.Errorf("the default attributes are %v, want an empty set that can be written into", got)
	}
}

// TestAssuranceNoneReadsAsSingleAsItDoesAtTheGateway holds that an identity asked
// for no assurance reads as single.
func TestAssuranceNoneReadsAsSingleAsItDoesAtTheGateway(t *testing.T) {
	id, _ := mustBuild(t, authntest.WithAssurance(authn.AssuranceNone))
	if id.Assurance() != authn.AssuranceSingle {
		t.Errorf("an identity made with no assurance reads %v, want single", id.Assurance())
	}
}

// TestAContextOutsideAuthntestCarriesNobody holds that a bare context reads as no
// identity and an unknown access level.
func TestAContextOutsideAuthntestCarriesNobody(t *testing.T) {
	bare := context.Background()
	if _, ok := authn.FromContext(bare); ok {
		t.Error("a bare context carries an identity")
	}
	if got := authn.AccessFromContext(bare); got != authn.AccessUnknown {
		t.Errorf("a bare context reads the access level %v, want unknown", got)
	}
}

type ctxKey struct{}

// TestContextKeepsWhatWasAlreadyInTheContext holds that values of the given
// context survive.
func TestContextKeepsWhatWasAlreadyInTheContext(t *testing.T) {
	parent := context.WithValue(context.Background(), ctxKey{}, "kept")
	ctx := authntest.Context(t, parent)
	if ctx.Value(ctxKey{}) != "kept" {
		t.Error("the context Context returned lost a value of the context it was given")
	}
}

// TestWhatTheCallerHandedOverIsCopied holds that an option copies the map or
// slice it was given.
func TestWhatTheCallerHandedOverIsCopied(t *testing.T) {
	attrs := map[string]string{"region": "eu"}
	methods := []authn.Method{authn.MethodPassword, authn.MethodTOTP}
	withAttrs := authntest.WithAttributes(attrs)
	withMethods := authntest.WithMethods(methods...)

	attrs["region"] = "us"
	methods[0] = authn.MethodSAML

	id, _ := mustBuild(t, withAttrs, withMethods)
	if v, _ := id.Attribute("region"); v != "eu" {
		t.Errorf("the attribute reads %q after the caller changed its map, want eu", v)
	}
	if got := id.Methods(); got[0] != authn.MethodPassword {
		t.Errorf("the methods read %v after the caller changed its slice", got)
	}
}

// TestAnIdentityItMakesSatisfiesWhatItsFieldsSay holds that Satisfies answers
// from the fields the options set.
func TestAnIdentityItMakesSatisfiesWhatItsFieldsSay(t *testing.T) {
	now := authntest.DefaultAuthTime().Add(time.Minute)
	single, _ := mustBuild(t)
	multi, _ := mustBuild(t, authntest.WithAssurance(authn.AssuranceMulti))
	need := authn.Requirement{Assurance: authn.AssuranceMulti}

	if single.Satisfies(need, now) {
		t.Error("a single-factor identity satisfies a multi-factor requirement")
	}
	if !multi.Satisfies(need, now) {
		t.Error("a multi-factor identity does not satisfy a multi-factor requirement")
	}
	if multi.Satisfies(authn.Requirement{Assurance: authn.AssuranceMulti, MaxAge: time.Second}, now) {
		t.Error("an identity authenticated a minute ago satisfies a requirement of one second's freshness")
	}
}

// TestACombinationTheGatewayWouldRefuseFailsTheTest holds that a shape the
// gateway's exit check refuses fails the asking test and leaves no identity.
func TestACombinationTheGatewayWouldRefuseFailsTheTest(t *testing.T) {
	longID := authn.AccountID(strings.Repeat("a", 65))
	tooMany := map[string]string{}
	for i := range 33 {
		tooMany[fmt.Sprintf("k%d", i)] = "v"
	}
	bulky := map[string]string{}
	for i := range 20 { // 20 * (3 + 250) is above the 4 KiB bound, each entry within its own
		bulky[fmt.Sprintf("k%02d", i)] = strings.Repeat("v", 250)
	}
	nine := []authn.Method{
		authn.MethodPassword, authn.MethodSMS, authn.MethodTOTP, authn.MethodWebAuthn, authn.MethodOAuth,
		authn.MethodOIDC, authn.MethodSAML, authn.MethodStatic, authn.MethodClientSecret,
	}
	actorOK := []authntest.Option{authntest.WithSession(sessionID), authntest.WithScope("tenant-a")}

	for _, c := range []struct {
		name string
		opts []authntest.Option
		why  string // what the message has to name
	}{
		{"an empty account", []authntest.Option{authntest.WithAccount("")}, "account"},
		{"an account with a space", []authntest.Option{authntest.WithAccount("has space")}, "account"},
		{"an account with a slash", []authntest.Option{authntest.WithAccount("a/b")}, "account"},
		{"an account of 65 bytes", []authntest.Option{authntest.WithAccount(longID)}, "account"},
		{"an actor without a session", []authntest.Option{authntest.WithActor("acct-admin"), authntest.WithScope("tenant-a")}, "WithSession"},
		{"an actor without a scope", []authntest.Option{authntest.WithActor("acct-admin"), authntest.WithSession(sessionID)}, "WithScope"},
		{"an actor that is the account", append([]authntest.Option{authntest.WithActor(authntest.DefaultAccount)}, actorOK...), "actor"},
		{"an actor that is not an identifier", append([]authntest.Option{authntest.WithActor("bad actor")}, actorOK...), "actor"},
		{"an IdP declaration without multi-factor", []authntest.Option{authntest.WithIDPAssured(true), authntest.WithProvider(providerA)}, "multi-factor"},
		{"an IdP declaration without a provider", []authntest.Option{authntest.WithIDPAssured(true), authntest.WithAssurance(authn.AssuranceMulti)}, "WithProvider"},
		{"no methods", []authntest.Option{authntest.WithMethods()}, "method"},
		{"a method outside the set", []authntest.Option{authntest.WithMethods("telepathy")}, "closed set"},
		{"a repeated method", []authntest.Option{authntest.WithMethods(authn.MethodPassword, authn.MethodPassword)}, "repeated"},
		{"nine methods", []authntest.Option{authntest.WithMethods(nine...)}, "methods"},
		{"an assurance above multi", []authntest.Option{authntest.WithAssurance(authn.Assurance(9))}, "assurance"},
		{"the zero authentication time", []authntest.Option{authntest.WithAuthTime(time.Time{})}, "zero time"},
		{"a scope without an actor", []authntest.Option{authntest.WithScope("tenant-a")}, "WithActor"},
		{"a scope with a space", append([]authntest.Option{authntest.WithActor("acct-admin")}, authntest.WithSession(sessionID), authntest.WithScope("tenant a")), "scope"},
		{"a provider that is not a UUID", []authntest.Option{authntest.WithProvider("idp-1")}, "provider"},
		{"a backend with a capital", []authntest.Option{authntest.WithBackend("Session")}, "backend"},
		{"an empty backend", []authntest.Option{authntest.WithBackend("")}, "backend"},
		{"a static account from another backend", []authntest.Option{authntest.WithAccount("static.ops")}, "static"},
		{"a static backend with a library account", []authntest.Option{authntest.WithBackend("static")}, "static"},
		{"an attribute key with a capital", []authntest.Option{authntest.WithAttributes(map[string]string{"Region": "eu"})}, "key"},
		{"an attribute value with a control character", []authntest.Option{authntest.WithAttributes(map[string]string{"k": "a\x00b"})}, "value"},
		{"an attribute value over 256 bytes", []authntest.Option{authntest.WithAttributes(map[string]string{"k": strings.Repeat("v", 257)})}, "value"},
		{"an attribute value that is not UTF-8", []authntest.Option{authntest.WithAttributes(map[string]string{"k": "\xff"})}, "value"},
		{"33 attributes", []authntest.Option{authntest.WithAttributes(tooMany)}, "attributes"},
		{"attributes over 4 KiB together", []authntest.Option{authntest.WithAttributes(bulky)}, "bytes"},
		{"an access level that is not defined", []authntest.Option{authntest.WithAccess(authn.Access(9))}, "access"},
		{"an identity on an open route", []authntest.Option{authntest.WithAccess(authn.AccessOpen)}, "Anonymous"},
		{"an identity on a route the gateway decided nothing for", []authntest.Option{authntest.WithAccess(authn.AccessUnknown)}, "Anonymous"},
		{"a provider in upper case", []authntest.Option{authntest.WithProvider(strings.ToUpper(providerA))}, "provider"},
		{"a provider in upper case under an IdP declaration", []authntest.Option{
			authntest.WithAssurance(authn.AssuranceMulti), authntest.WithProvider(strings.ToUpper(providerA)), authntest.WithIDPAssured(true),
		}, "provider"},
		{"a scope with a tab", append([]authntest.Option{authntest.WithActor("acct-admin"), authntest.WithSession(sessionID)}, authntest.WithScope("tenant\ta")), "scope"},
		{"a scope with a line feed", append([]authntest.Option{authntest.WithActor("acct-admin"), authntest.WithSession(sessionID)}, authntest.WithScope("tenant\na")), "scope"},
		{"a scope with a NUL", append([]authntest.Option{authntest.WithActor("acct-admin"), authntest.WithSession(sessionID)}, authntest.WithScope("tenant\x00a")), "scope"},
		{"an attribute key of 65 bytes", []authntest.Option{authntest.WithAttributes(map[string]string{strings.Repeat("k", 65): "v"})}, "key"},
		{"an attribute key that starts with a dot", []authntest.Option{authntest.WithAttributes(map[string]string{".k": "v"})}, "key"},
		{"attributes of 4097 bytes together", []authntest.Option{authntest.WithAttributes(attributesOf(4097))}, "bytes"},
		{"an actor of the static namespace on another backend", append([]authntest.Option{authntest.WithActor("static.ops")}, actorOK...), "actor"},
		{"an actor outside the static namespace on the static backend", append([]authntest.Option{
			authntest.WithAccount("static.ops"), authntest.WithBackend("static"), authntest.WithActor("acct-admin"),
		}, actorOK...), "actor"},
		{"a challenge in lower case", []authntest.Option{authntest.WithChallenge("bearer")}, "challenge"},
		{"an empty challenge", []authntest.Option{authntest.WithChallenge("")}, "challenge"},
		{"a challenge with a space", []authntest.Option{authntest.WithChallenge("Bearer token")}, "challenge"},
		{"a challenge that is not a token", []authntest.Option{authntest.WithChallenge("Bearer,")}, "challenge"},
		{"a challenge in upper case", []authntest.Option{authntest.WithChallenge("BEARER")}, "challenge"},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx, r := build(c.opts...)
			if !r.failed {
				t.Fatalf("Context accepted %s, which the gateway would not stamp", c.name)
			}
			if !strings.Contains(r.message, c.why) {
				t.Errorf("the failure %q does not name %q", r.message, c.why)
			}
			if _, ok := authn.FromContext(ctx); ok {
				t.Error("the context returned beside a failure carries an identity")
			}
		})
	}
}

// TestTheNeighbouringAdmissibleCombinationsPass holds that each refusal above has
// a nearby shape that passes.
func TestTheNeighbouringAdmissibleCombinationsPass(t *testing.T) {
	boundary := map[string]string{}
	for i := range 32 {
		boundary[fmt.Sprintf("k%02d", i)] = strings.Repeat("v", 100)
	}
	for _, c := range []struct {
		name string
		opts []authntest.Option
	}{
		{"an account of 64 bytes", []authntest.Option{authntest.WithAccount(authn.AccountID(strings.Repeat("a", 64)))}},
		{"an impersonation with its session and scope", []authntest.Option{
			authntest.WithActor("acct-admin"), authntest.WithSession(sessionID), authntest.WithScope("tenant-a"),
		}},
		{"an IdP declaration with multi-factor and a provider", []authntest.Option{
			authntest.WithAssurance(authn.AssuranceMulti), authntest.WithProvider(providerA), authntest.WithIDPAssured(true),
		}},
		{"eight methods", []authntest.Option{authntest.WithMethods(
			authn.MethodPassword, authn.MethodSMS, authn.MethodTOTP, authn.MethodWebAuthn,
			authn.MethodOAuth, authn.MethodOIDC, authn.MethodSAML, authn.MethodClientSecret)}},
		{"a static account from the static backend", []authntest.Option{
			authntest.WithAccount("static.ops"), authntest.WithBackend("static"), authntest.WithMethods(authn.MethodStatic),
		}},
		{"a dotted backend name", []authntest.Option{authntest.WithBackend("corp.ldap")}},
		{"32 attributes within the byte bound", []authntest.Option{authntest.WithAttributes(boundary)}},
		{"an attribute under the idp prefix", []authntest.Option{authntest.WithAttributes(map[string]string{"idp.group": "eng"})}},
		{"attributes of 4096 bytes together", []authntest.Option{authntest.WithAttributes(attributesOf(4096))}},
		{"an attribute key of 64 bytes", []authntest.Option{authntest.WithAttributes(map[string]string{strings.Repeat("k", 64): "v"})}},
		{"an attribute value of 256 bytes", []authntest.Option{authntest.WithAttributes(map[string]string{"k": strings.Repeat("v", 256)})}},
		{"a scope with a hyphen and a dot", []authntest.Option{
			authntest.WithActor("acct-admin"), authntest.WithSession(sessionID), authntest.WithScope("tenant-a.eu"),
		}},
		{"an actor of the static namespace on the static backend", []authntest.Option{
			authntest.WithAccount("static.ops"), authntest.WithBackend("static"), authntest.WithActor("static.root"),
			authntest.WithSession(sessionID), authntest.WithScope("tenant-a"),
		}},
		{"a challenge the gateway names a cookie carrier with", []authntest.Option{authntest.WithChallenge("Cookie")}},
		{"a challenge of another scheme", []authntest.Option{authntest.WithChallenge("Negotiate")}},
		{"a challenge whose token carries a hyphen and a digit", []authntest.Option{authntest.WithChallenge("Aws4-hmac-sha256")}},
		{"a route that is optional", []authntest.Option{authntest.WithAccess(authn.AccessOptional)}},
		{"an attribute under the authn prefix", []authntest.Option{authntest.WithAttributes(map[string]string{"authn.internal": "x"})}},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx, r := build(c.opts...)
			if r.failed {
				t.Fatalf("Context refused %s: %s", c.name, r.message)
			}
			if _, ok := authn.FromContext(ctx); !ok {
				t.Error("the context carries no identity")
			}
		})
	}
}

// TestALaterOptionOverridesAnEarlierOne holds that the last option wins.
func TestALaterOptionOverridesAnEarlierOne(t *testing.T) {
	id, _ := mustBuild(t, authntest.WithAccount("first"), authntest.WithAccount("second"))
	if id.Account() != "second" {
		t.Errorf("the account is %q, want the later option's", id.Account())
	}
}

// TestAnAnonymousContextCarriesTheLevelAndNobody holds that an anonymous context
// carries the level and no identity.
func TestAnAnonymousContextCarriesTheLevelAndNobody(t *testing.T) {
	for name, level := range map[string]authn.Access{"open": authn.AccessOpen, "optional": authn.AccessOptional} {
		t.Run(name, func(t *testing.T) {
			ctx := authntest.Anonymous(t, context.Background(), level)
			if _, ok := authn.FromContext(ctx); ok {
				t.Error("an anonymous context carries an identity")
			}
			if got := authn.AccessFromContext(ctx); got != level {
				t.Errorf("the anonymous context reads the level %v, want %v", got, level)
			}
		})
	}
}

// TestAnAnonymousContextKeepsWhatWasAlreadyInTheContext is the parent-value
// control for Anonymous, as for Context.
func TestAnAnonymousContextKeepsWhatWasAlreadyInTheContext(t *testing.T) {
	parent := context.WithValue(context.Background(), ctxKey{}, "kept")
	ctx := authntest.Anonymous(t, parent, authn.AccessOptional)
	if ctx.Value(ctxKey{}) != "kept" {
		t.Error("the context Anonymous returned lost a value of the context it was given")
	}
}

// TestALevelNoAnonymousRequestHasFailsTheTest holds that Anonymous refuses a level
// no anonymous request can carry.
func TestALevelNoAnonymousRequestHasFailsTheTest(t *testing.T) {
	for name, level := range map[string]authn.Access{
		"required":  authn.AccessRequired,
		"unknown":   authn.AccessUnknown,
		"undefined": authn.Access(9),
	} {
		t.Run(name, func(t *testing.T) {
			r := &recorder{}
			ctx := authntest.Anonymous(r, context.Background(), level)
			if !r.failed {
				t.Fatalf("Anonymous accepted the level %v", level)
			}
			if !strings.Contains(r.message, "access") {
				t.Errorf("the failure %q does not name the access level", r.message)
			}
			if got := authn.AccessFromContext(ctx); got != authn.AccessUnknown {
				t.Errorf("the context returned beside a failure reads the level %v", got)
			}
		})
	}
}

// TestTheChallengeDefaultsToBearerAndTheLastOptionWins holds that the challenge
// stays Bearer unless an option replaces it, and the last option wins.
func TestTheChallengeDefaultsToBearerAndTheLastOptionWins(t *testing.T) {
	id, _ := mustBuild(t, authntest.WithChallenge("Basic"), authntest.WithChallenge("Cookie"))
	if id.Challenge() != "Cookie" {
		t.Errorf("the challenge reads %q after two options, want the later one", id.Challenge())
	}
	id, _ = mustBuild(t, authntest.WithBackend("static"), authntest.WithAccount("static.ops"))
	if id.Challenge() != authntest.DefaultChallenge {
		t.Errorf("the challenge reads %q with no option, want %q", id.Challenge(), authntest.DefaultChallenge)
	}
}
