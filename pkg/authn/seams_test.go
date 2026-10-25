package authn

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/printer"
	"go/token"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// seamSignatures is the method set of every capability interface the design
// gives a signature for, written as the design writes it: the name, then the
// parameters and results with their names and groupings. TestTheSeamsMatchTheDesign
// reads the interfaces from the source of this package and compares them with
// these, so a method added, dropped, renamed or given another signature is
// found, and so is a parameter that lost its name.
//
// Every interface here is implemented outside this package, so a changed
// signature breaks each implementer: a module delivers the account, session,
// token, factor, login, SMS, SSO and impersonation contracts and the gate, a
// host delivers Decider, Provisioner and its own credential backends, and the
// tenant admission layer delivers ScopeGuard. Every entry is the design's own
// signature, Passwords.Clear among them.
var seamSignatures = map[string][]string{
	"Accounts": {
		"Get(ctx context.Context, id AccountID) (Account, error)",
		"Lookup(ctx context.Context, kind IdentifierKind, value string) (AccountID, error)",
		"ByExternal(ctx context.Context, provider, subject string) (AccountID, error)",
		"Identifiers(ctx context.Context, id AccountID) ([]Identifier, error)",
		"IdentifiersMany(ctx context.Context, ids []AccountID) (map[AccountID][]Identifier, error)",
		"Externals(ctx context.Context, id AccountID) ([]ExternalBinding, error)",
		"List(ctx context.Context, q AccountQuery) ([]Account, string, error)",
		"ListServiceSecrets(ctx context.Context, id AccountID) ([]ServiceSecretInfo, error)",
	},
	"AccountAdmin": {
		"Create(ctx context.Context, in CreateAccount) (Account, error)",
		"SetStatus(ctx context.Context, id AccountID, s AccountStatus, reason string) error",
		"Delete(ctx context.Context, id AccountID, reason string) error",
		"SetDisplayName(ctx context.Context, id AccountID, name string) error",
		"SetAttributes(ctx context.Context, id AccountID, attrs map[string]string) error",
		"SetProtected(ctx context.Context, id AccountID, protected bool) error",
		"SetMFARequired(ctx context.Context, id AccountID, required bool) error",
		"AddIdentifier(ctx context.Context, id AccountID, kind IdentifierKind, value string, via Verification) error",
		"RemoveIdentifier(ctx context.Context, id AccountID, identifierID string) error",
		"LinkExternal(ctx context.Context, id AccountID, l ExternalLink) error",
		"UnlinkExternal(ctx context.Context, id AccountID, externalID string) error",
		"CreateServiceSecret(ctx context.Context, id AccountID, label string, expires *time.Time) (secretID, secret string, err error)",
		"RevokeServiceSecret(ctx context.Context, id AccountID, secretID string) error",
		"Touch(ctx context.Context, id AccountID, at time.Time) error",
	},
	"Passwords": {
		"Verify(ctx context.Context, identifier, password string) (AccountID, VerifyResult, error)",
		"Set(ctx context.Context, id AccountID, password string, opt SetPasswordOptions) error",
		"ImportHash(ctx context.Context, id AccountID, encoded string, mustChange bool) error",
		"VerifyServiceSecret(ctx context.Context, clientID, secret string) (account AccountID, secretID string, expires time.Time, err error)",
		"Clear(ctx context.Context, id AccountID) error",
	},
	"Signups": {
		"Register(ctx context.Context, in RegisterAccount) (Account, error)",
	},
	"SignupVerification": {
		"SignupStateOf(ctx context.Context, id AccountID) (SignupStatus, error)",
		"PendingAccountByContact(ctx context.Context, kind string, value string) (accountID string, ok bool, err error)",
	},
	"Memberships": {
		"IsMember(ctx context.Context, id AccountID, tenant string) (bool, error)",
		"Membership(ctx context.Context, tenant string, id AccountID) (Membership, bool, error)",
		"Tenants(ctx context.Context, id AccountID) ([]Membership, error)",
		"Members(ctx context.Context, tenant string, page Page) ([]Membership, error)",
		"Put(ctx context.Context, m Membership) error",
		"Remove(ctx context.Context, id AccountID, tenant string) error",
	},
	"Throttle": {
		"Check(ctx context.Context, scope ThrottleScope, key string) (retryAfter time.Duration, err error)",
		"Fail(ctx context.Context, scope ThrottleScope, key string) (engaged bool, err error)",
		"Succeed(ctx context.Context, scope ThrottleScope, key string) error",
	},
	"AccountLifecycle": {
		"AccountDisabled(ctx context.Context, id AccountID) error",
		"AccountEnabled(ctx context.Context, id AccountID) error",
		"AccountDeleted(ctx context.Context, id AccountID) error",
		"ServiceSecretRevoked(ctx context.Context, id AccountID, credentialID string) error",
	},
	"Provisioner": {
		"Provision(ctx context.Context, p CreateAccount, via string) (CreateAccount, error)",
	},
	"EmailCodeSender": {
		"SendCode(ctx context.Context, to, code string, ttl time.Duration, locale string) error",
	},
	"HumanVerifier": {
		"Required() bool",
		"Provider() string",
		"SiteKey() string",
		"Verify(ctx context.Context, p HumanProof) error",
	},
	"Sessions": {
		"Create(ctx context.Context, in NewSession) (Issued, error)",
		"Get(ctx context.Context, id SessionID) (SessionInfo, error)",
		"List(ctx context.Context, account AccountID) ([]SessionInfo, error)",
		"RevocableSessions(ctx context.Context, account AccountID) ([]SessionID, error)",
		"Revoke(ctx context.Context, id SessionID, reason string) error",
		"RevokeAccount(ctx context.Context, account AccountID, except SessionID, reason string) ([]SessionID, error)",
		"Rotate(ctx context.Context, id SessionID) (Issued, error)",
		"Elevate(ctx context.Context, id SessionID, e Elevation) (Issued, error)",
		"SetImpersonation(ctx context.Context, id SessionID, grantID string) (Issued, error)",
		"Status(ctx context.Context, id SessionID) (SessionStatus, error)",
		"StatusFresh(ctx context.Context, id SessionID) (SessionStatus, error)",
		"ForCredential(ctx context.Context, account AccountID, credentialID string, expires *time.Time) (SessionID, error)",
		"RevokeCredential(ctx context.Context, credentialID, reason string) error",
		"SetCookie(w http.ResponseWriter, in Issued)",
		"ClearCookie(w http.ResponseWriter)",
		"Current(r *http.Request) (id SessionID, ok bool, err error)",
	},
	"Factor": {
		"Kind() FactorKind",
		"Enroll(ctx context.Context, f FlowState, in StepInput) (Step, error)",
		"Challenge(ctx context.Context, f FlowState, rec FactorRecord) (Step, error)",
		"Verify(ctx context.Context, f FlowState, rec FactorRecord, in StepInput) (Step, error)",
		"Remove(ctx context.Context, account AccountID, rec FactorRecord) error",
	},
	"MFA": {
		"Factors(ctx context.Context, id AccountID) ([]FactorRecord, error)",
		"Require(ctx context.Context, id AccountID, first Proof) (MFAOutcome, []FactorRecord, error)",
		"Challenge(ctx context.Context, f FlowState, factorID string) (Step, error)",
		"Verify(ctx context.Context, f FlowState, factorID string, in StepInput) (Step, error)",
		"Enroll(ctx context.Context, f FlowState, kind FactorKind, in StepInput) (Step, error)",
		"Assurance(first Proof, second *Pass) MFAResult",
		"Remove(ctx context.Context, id AccountID, factorID string) error",
		"Reset(ctx context.Context, id AccountID, reason string) (removed int, err error)",
		"Seasoned(ctx context.Context, id AccountID) (bool, error)",
		"Kinds() []FactorKind",
	},
	"LoginMethod": {
		"Name() string",
		"Describe() MethodDescriptor",
		"Begin(ctx context.Context, s Start) (Step, error)",
		"Answer(ctx context.Context, f FlowState, in StepInput) (Step, error)",
	},
	"Decider": {
		"Decide(ctx context.Context, d Decision) (allow bool, err error)",
	},
	"Impersonation": {
		"Start(ctx context.Context, actor Identity, r ImpersonationRequest) (ImpersonationGrant, error)",
		"End(ctx context.Context, actor Identity, reason string) error",
		"Revoke(ctx context.Context, id string, by Identity, reason string) error",
		"Active(ctx context.Context, id string) (ImpersonationGrant, bool, error)",
		"List(ctx context.Context, q GrantQuery) ([]ImpersonationGrant, string, error)",
	},
	"Tokens": {
		"Issue(ctx context.Context, in IssueToken) (AccessToken, error)",
		"NewRefresh(ctx context.Context, session SessionID) (RefreshToken, error)",
		"Refresh(ctx context.Context, presented string) (AccessToken, RefreshToken, error)",
		"Revoke(ctx context.Context, presented string) error",
		"JWKS(ctx context.Context) ([]byte, error)",
	},
	"ClaimsContributor": {
		"Claims(ctx context.Context, p Principal) (map[string]any, error)",
	},
	"SMSCodes": {
		"Send(ctx context.Context, in SendCode) (challengeID string, err error)",
		"Verify(ctx context.Context, challengeID, code string) error",
	},
	"SSOProviders": {
		"Get(ctx context.Context, id string) (SSOProvider, error)",
		"Secret(ctx context.Context, id string) (string, error)",
		"List(ctx context.Context, tenant string) ([]SSOProvider, error)",
		"Tenant(ctx context.Context, id string) (string, error)",
		"Discover(ctx context.Context, emailDomain string) ([]SSOProvider, error)",
		"Put(ctx context.Context, p SSOProvider, secret *string) (SSOProvider, error)",
		"Delete(ctx context.Context, id string) error",
		"ClaimDomain(ctx context.Context, idpID, domain string) (verifyToken string, err error)",
		"VerifyDomain(ctx context.Context, idpID, domain string) error",
		"RemoveDomain(ctx context.Context, idpID, domain string) error",
		"ReverifyDomains(ctx context.Context, idpID string) error",
	},
	"SSOMetadataRefresher": {
		"Protocol() string",
		"Refresh(ctx context.Context, idpID string) (SSOProvider, error)",
	},
	"Gate": {
		"Exempt(endpoint, method, path string)",
		"Optional(endpoint, method, path string)",
		"Impersonable(endpoint, method, path string)",
	},
	"Verifier": {
		"Name() string",
		"Accepts() Accepts",
		"Verify(ctx context.Context, c Credential) Verdict",
	},
	"Challenger": {
		"Challenge(realm string, rejected bool) []string",
	},
	"CookieClearer": {
		"ClearCookie(w http.ResponseWriter)",
	},
	"ScopeGuard": {
		"Covers(endpoint string) bool",
	},
}

// interfaceMethods reads the interfaces declared in the non-test files of the
// package in dir and returns, for each, its method set as "Name(params) results"
// in the printer's rendering of the declaration.
func interfaceMethods(t *testing.T, dir string) map[string][]string {
	t.Helper()
	bp, err := build.Default.ImportDir(dir, 0)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	fset := token.NewFileSet()
	out := make(map[string][]string)
	for _, name := range bp.GoFiles {
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts := spec.(*ast.TypeSpec)
				it, ok := ts.Type.(*ast.InterfaceType)
				if !ok {
					continue
				}
				var methods []string
				for _, m := range it.Methods.List {
					ft, ok := m.Type.(*ast.FuncType)
					if !ok || len(m.Names) != 1 {
						t.Fatalf("%s embeds or groups methods: this test reads one named method at a time", ts.Name.Name)
					}
					var buf bytes.Buffer
					if err := printer.Fprint(&buf, fset, ft); err != nil {
						t.Fatal(err)
					}
					methods = append(methods, m.Names[0].Name+strings.TrimPrefix(buf.String(), "func"))
				}
				out[ts.Name.Name] = methods
			}
		}
	}
	return out
}

// TestTheSeamsMatchTheDesign holds the method set of each capability interface
// to the signatures the design writes, in both directions: an interface that has
// a method the design does not list fails as surely as one that lacks one. The
// set of this package's interfaces is held to seamSignatures in both directions
// too, so an interface declared here without an entry there fails rather than
// going unchecked.
func TestTheSeamsMatchTheDesign(t *testing.T) {
	got := interfaceMethods(t, ".")
	for name, want := range seamSignatures {
		have, ok := got[name]
		if !ok {
			t.Errorf("the package declares no interface %s", name)
			continue
		}
		for _, w := range want {
			if !slices.Contains(have, w) {
				t.Errorf("%s has no method %q; it has %q", name, w, have)
			}
		}
		for _, h := range have {
			if !slices.Contains(want, h) {
				t.Errorf("%s has the method %q, which the design does not list", name, h)
			}
		}
	}
	for name := range got {
		if _, ok := seamSignatures[name]; !ok {
			t.Errorf("the package declares the interface %s, which seamSignatures does not hold, so its signature goes unchecked", name)
		}
	}
}

// TestTheSeamCheckSeesADriftedSignature runs the comparison over a package whose
// interface drifts in each way the check is for, so it cannot pass by reading
// nothing.
func TestTheSeamCheckSeesADriftedSignature(t *testing.T) {
	dir := writeSurfacePackage(t, map[string]string{
		"p.go": `package p

import "context"

type Seam interface {
	Do(ctx context.Context, id string) error
	Gone(ctx context.Context) error
	Renamed(ctx context.Context, other string) error
}
`,
	})
	got := interfaceMethods(t, dir)["Seam"]
	for _, want := range []string{
		"Do(ctx context.Context, id string) error",
		"Gone(ctx context.Context) error",
		"Renamed(ctx context.Context, other string) error",
	} {
		if !slices.Contains(got, want) {
			t.Fatalf("the reader did not render %q; it has %q", want, got)
		}
	}
	for _, drifted := range []string{
		"Do(ctx context.Context, id int) error",                   // another parameter type
		"Do(ctx context.Context, identifier string) error",        // a parameter renamed
		"Do(ctx context.Context, id string) (string, error)",      // another result
		"Renamed(ctx context.Context, other, extra string) error", // a parameter added
		"Done(ctx context.Context, id string) error",              // a method renamed
	} {
		if slices.Contains(got, drifted) {
			t.Errorf("the reader matched the drifted signature %q", drifted)
		}
	}
}

// moduleDeliveries lists, per module that delivers more than one interface, the
// interfaces its one instance has to implement together: core.Resolve asserts
// the instance to each of them, so a method name the interfaces share with two
// signatures leaves no type that is all of them.
var moduleDeliveries = map[string][]reflect.Type{
	"account": {
		reflect.TypeFor[Accounts](), reflect.TypeFor[AccountAdmin](),
		reflect.TypeFor[Signups](), reflect.TypeFor[SignupVerification](),
		reflect.TypeFor[Passwords](), reflect.TypeFor[Memberships](), reflect.TypeFor[Throttle](),
	},
	"session": {
		reflect.TypeFor[Sessions](), reflect.TypeFor[Verifier](), reflect.TypeFor[CookieClearer](),
		reflect.TypeFor[AccountLifecycle](),
	},
	"mfa":     {reflect.TypeFor[MFA](), reflect.TypeFor[AccountLifecycle]()},
	"sms":     {reflect.TypeFor[SMSCodes](), reflect.TypeFor[AccountLifecycle]()},
	"token":   {reflect.TypeFor[Tokens](), reflect.TypeFor[Verifier](), reflect.TypeFor[Challenger]()},
	"passkey": {reflect.TypeFor[Factor](), reflect.TypeFor[LoginMethod]()},
}

// nameClashes reports the method names that two of the interfaces share with
// different signatures. The same name with the same signature is not a clash:
// Sessions.ClearCookie and CookieClearer.ClearCookie are one method to the type
// that implements both.
func nameClashes(ifaces []reflect.Type) []string {
	seen := make(map[string]reflect.Type)
	owner := make(map[string]string)
	var out []string
	for _, it := range ifaces {
		for m := range it.Methods() {
			if prev, ok := seen[m.Name]; ok {
				if prev != m.Type {
					out = append(out, fmt.Sprintf("%s: %s of %s and %s of %s", m.Name, m.Type, it.Name(), prev, owner[m.Name]))
				}
				continue
			}
			seen[m.Name] = m.Type
			owner[m.Name] = it.Name()
		}
	}
	slices.Sort(out)
	return out
}

// TestAModulesInterfacesShareNoMethodNameWithAnotherSignature holds the rule that
// the interfaces one module delivers have disjoint method names, or equal
// signatures where they share one: a module is one instance, and an instance
// cannot implement two methods of one name.
func TestAModulesInterfacesShareNoMethodNameWithAnotherSignature(t *testing.T) {
	for module, ifaces := range moduleDeliveries {
		for _, clash := range nameClashes(ifaces) {
			t.Errorf("the %s module's interfaces clash: %s", module, clash)
		}
	}
}

// TestTheNameClashCheckSeesAClash gives the check interfaces that clash, that
// share a method with one signature, and that share nothing, so a reflection that
// compared nothing would be found.
func TestTheNameClashCheckSeesAClash(t *testing.T) {
	type (
		a      interface{ Do(int) error }
		same   interface{ Do(int) error }
		other  interface{ Do(string) error }
		apart  interface{ Else(int) error }
		named  interface{ Do(n int) error }
		result interface{ Do(int) int }
	)
	for _, c := range []struct {
		name   string
		ifaces []reflect.Type
		clash  bool
	}{
		{"the same name and signature", []reflect.Type{reflect.TypeFor[a](), reflect.TypeFor[same]()}, false},
		{"parameter names are not the signature", []reflect.Type{reflect.TypeFor[a](), reflect.TypeFor[named]()}, false},
		{"disjoint names", []reflect.Type{reflect.TypeFor[a](), reflect.TypeFor[apart]()}, false},
		{"another parameter type", []reflect.Type{reflect.TypeFor[a](), reflect.TypeFor[other]()}, true},
		{"another result", []reflect.Type{reflect.TypeFor[a](), reflect.TypeFor[result]()}, true},
		{"the clash is in the third of three", []reflect.Type{reflect.TypeFor[a](), reflect.TypeFor[apart](), reflect.TypeFor[other]()}, true},
	} {
		if got := len(nameClashes(c.ifaces)) > 0; got != c.clash {
			t.Errorf("%s: clash = %v, want %v", c.name, got, c.clash)
		}
	}
}

// TestSessionsAndCookieClearerShareOneClearCookie is the one name the session
// module's interfaces have in common, which the rule allows because the two
// signatures are equal; if either changes, one method no longer satisfies both
// and the module could not be built.
func TestSessionsAndCookieClearerShareOneClearCookie(t *testing.T) {
	s, _ := reflect.TypeFor[Sessions]().MethodByName("ClearCookie")
	c, _ := reflect.TypeFor[CookieClearer]().MethodByName("ClearCookie")
	if s.Type == nil || c.Type == nil || s.Type != c.Type {
		t.Errorf("Sessions.ClearCookie is %v and CookieClearer.ClearCookie is %v; they have to be one signature", s.Type, c.Type)
	}
}
