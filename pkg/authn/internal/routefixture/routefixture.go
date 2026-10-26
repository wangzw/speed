// Package routefixture is the assembly the authentication capability's route
// checks run on: the parts every module of the directory shares, and one
// stand-in for the capabilities each one holds.
//
// Two of the parts are the real modules, and both for one reason: a stand-in
// would encode this fixture's own reading of a fact the check exists to hold to
// production. The route-mounting capability's module owns the base path every
// registration is written under, and its interface belongs to
// pkg/authn/internal/mount, so no package outside the capability can implement
// it anyway. The gateway declares the configuration keys the deployment's own
// address is read from — they are the gateway's, not the mounter's — so a case
// that left it out would have nowhere to write an address, and the session
// module refuses to start with none.
//
// The backend that verifies a credential answers the two questions the gateway
// asks while it is assembling — its name and the carriers it takes — and
// refuses every credential, because no request is served. It is taken in only
// when the module under test is not itself a backend.
//
// The rest are stand-in capabilities with no body at all. A module resolves
// each in its Prepare, its New or its Init and holds it, and the check drives
// to Init and serves no request. The three a module does call while it is
// assembling — the scope guard the gateway asks which endpoints an impersonating
// identity may reach, the login method the engine names, and the register reads
// the administration and SAML modules assert — carry that one method each and
// no more.
//
// A stand-in embeds one interface and no more. Embedding several in one struct
// would leave every method they have in common ambiguous, and an ambiguous
// method is not promoted: the struct would satisfy none of the interfaces it
// was built for, which the registry reports as a module that did not deliver
// what it declared.
package routefixture

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/gate"
	"github.com/vislake/speed/pkg/authn/mount"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/routecontract"
	"github.com/vislake/speed/pkg/settings"
)

// Backend is a credential-verifying backend of a host's own, in the shape the
// gateway admits one: it names itself and declares the carrier it takes. It is
// the one stand-in here that answers more than "I am present", because the
// gateway reads both of those while it is assembling, and it is taken in only
// when the module under test is not itself a backend.
type Backend struct{}

var _ authn.Verifier = Backend{}

// Name is this backend's name, unique within the assembly.
func (Backend) Name() string { return "routefixture" }

// Accepts declares the one carrier this backend takes.
func (Backend) Accepts() authn.Accepts { return authn.Accepts{Schemes: []string{"Bearer"}} }

// Verify refuses every credential, which is all a check that serves no request
// asks of it.
func (Backend) Verify(context.Context, authn.Credential) authn.Verdict {
	return authn.Denied(errors.New("routefixture: no request is served"))
}

// The stand-in types, one per capability. Each embeds its interface and
// implements no method: resolving one and holding it never calls a method, and
// a capability that is called during Init — the entry point, the gateway, the
// database, the bus — is supplied for real instead.
type (
	accounts         struct{ authn.Accounts }
	accountAdmin     struct{ authn.AccountAdmin }
	signups          struct{ authn.Signups }
	signupState      struct{ authn.SignupVerification }
	passwords        struct{ authn.Passwords }
	memberships      struct{ authn.Memberships }
	sessions         struct{ authn.Sessions }
	mfa              struct{ authn.MFA }
	throttle         struct{ authn.Throttle }
	ssoMetadata      struct{ authn.SSOMetadataRefresher }
	accountLifecycle struct{ authn.AccountLifecycle }
	verifier         struct{ authn.Verifier }
	tokens           struct{ authn.Tokens }
	decider          struct{ authn.Decider }
	smsCodes         struct{ authn.SMSCodes }
	emailCodeSender  struct{ authn.EmailCodeSender }
	auditCap         struct{ audit.Audit }
	settingsStore    struct{ settings.Store }
)

// impersonation carries the read the administration module asserts an
// impersonation register to have before it mounts the group of routes that
// show a grant. The module judges the assertion itself and mounts nothing
// without it, so an assembly that wants those routes declared must satisfy it.
type impersonation struct {
	authn.Impersonation
}

// Get is the register read the administration module asserts, and it is never
// called: no request is served.
func (impersonation) Get(context.Context, string) (authn.ImpersonationGrant, error) {
	return authn.ImpersonationGrant{}, errors.New("routefixture: no request is served")
}

// scopeGuard is a tenant scope guard of a host's own. The gateway asks it which
// endpoints an impersonating identity may reach while it is writing its startup
// diagnostic, so it has to answer, and it answers none.
type scopeGuard struct {
	authn.ScopeGuard
}

// Covers reports that this guard admits an impersonating identity to no
// endpoint: no request is served, so where it is admitted does not matter.
func (scopeGuard) Covers(string) bool { return false }

// loginMethod is a login method of a host's own. The engine names every method
// it takes up while it is assembling, so it has to answer, and it is never
// begun or answered.
type loginMethod struct {
	authn.LoginMethod
}

// Name is this method's stable name, unique across the assembly.
func (loginMethod) Name() string { return "routefixture" }

// Begin starts no flow: no request is served.
func (loginMethod) Begin(context.Context, authn.Start) (authn.Step, error) {
	return authn.Step{}, errors.New("routefixture: no request is served")
}

// Answer answers no step: no request is served.
func (loginMethod) Answer(context.Context, authn.FlowState, authn.StepInput) (authn.Step, error) {
	return authn.Step{}, errors.New("routefixture: no request is served")
}

// ssoProviders carries the write the SAML module asserts an enterprise provider
// register to have before it builds at all: without it an assertion could be
// replayed, so the module refuses to construct.
type ssoProviders struct {
	authn.SSOProviders
}

// RememberAssertion is the replay-line write the SAML module asserts, and it is
// never called: no assertion is verified.
func (ssoProviders) RememberAssertion(context.Context, string, string, time.Time) error {
	return errors.New("routefixture: no request is served")
}

// Domains is the single-provider domain read the management surface asserts the
// register to have. It is never called.
func (ssoProviders) Domains(context.Context, string) ([]authn.SSOProviderDomain, error) {
	return nil, errors.New("routefixture: no request is served")
}

// DomainsMany is the batch domain read the management surface asserts the
// register to have. It is never called.
func (ssoProviders) DomainsMany(context.Context, []string) (map[string][]authn.SSOProviderDomain, error) {
	return nil, errors.New("routefixture: no request is served")
}

// standIns maps a capability token to the module that stands in for it. The
// keys are typed nil pointers, so two of them naming one capability compare
// equal.
var standIns = map[core.Token]func() core.Module{
	(*authn.Accounts)(nil):             func() core.Module { return standIn("accounts", &accounts{}, (*authn.Accounts)(nil)) },
	(*authn.AccountAdmin)(nil):         func() core.Module { return standIn("account-admin", &accountAdmin{}, (*authn.AccountAdmin)(nil)) },
	(*authn.Signups)(nil):              func() core.Module { return standIn("signups", &signups{}, (*authn.Signups)(nil)) },
	(*authn.SignupVerification)(nil):   func() core.Module { return standIn("signup-state", &signupState{}, (*authn.SignupVerification)(nil)) },
	(*authn.Passwords)(nil):            func() core.Module { return standIn("passwords", &passwords{}, (*authn.Passwords)(nil)) },
	(*authn.Memberships)(nil):          func() core.Module { return standIn("memberships", &memberships{}, (*authn.Memberships)(nil)) },
	(*authn.Sessions)(nil):             func() core.Module { return standIn("sessions", &sessions{}, (*authn.Sessions)(nil)) },
	(*authn.MFA)(nil):                  func() core.Module { return standIn("mfa", &mfa{}, (*authn.MFA)(nil)) },
	(*authn.Throttle)(nil):             func() core.Module { return standIn("throttle", &throttle{}, (*authn.Throttle)(nil)) },
	(*authn.SSOProviders)(nil):         func() core.Module { return standIn("sso-providers", &ssoProviders{}, (*authn.SSOProviders)(nil)) },
	(*authn.SSOMetadataRefresher)(nil): func() core.Module { return standIn("sso-metadata", &ssoMetadata{}, (*authn.SSOMetadataRefresher)(nil)) },
	(*authn.ScopeGuard)(nil):           func() core.Module { return standIn("scope-guard", &scopeGuard{}, (*authn.ScopeGuard)(nil)) },
	(*authn.LoginMethod)(nil):          func() core.Module { return standIn("login-method", &loginMethod{}, (*authn.LoginMethod)(nil)) },
	(*authn.AccountLifecycle)(nil): func() core.Module {
		return standIn("account-lifecycle", &accountLifecycle{}, (*authn.AccountLifecycle)(nil))
	},
	(*authn.Verifier)(nil):      func() core.Module { return standIn("verifier", &verifier{}, (*authn.Verifier)(nil)) },
	(*authn.Tokens)(nil):        func() core.Module { return standIn("tokens", &tokens{}, (*authn.Tokens)(nil)) },
	(*authn.Decider)(nil):       func() core.Module { return standIn("decider", &decider{}, (*authn.Decider)(nil)) },
	(*authn.Impersonation)(nil): func() core.Module { return standIn("impersonation", &impersonation{}, (*authn.Impersonation)(nil)) },
	(*authn.SMSCodes)(nil):      func() core.Module { return standIn("sms-codes", &smsCodes{}, (*authn.SMSCodes)(nil)) },
	(*authn.EmailCodeSender)(nil): func() core.Module {
		return standIn("email-code-sender", &emailCodeSender{}, (*authn.EmailCodeSender)(nil))
	},
	(*audit.Audit)(nil):    func() core.Module { return standIn("audit", &auditCap{}, (*audit.Audit)(nil)) },
	(*settings.Store)(nil): func() core.Module { return standIn("settings-store", &settingsStore{}, (*settings.Store)(nil)) },
}

// Seams is the stand-in set for exactly the capabilities named, which are the
// ones a module under test requires and nothing supplies otherwise.
//
// A capability the module under test delivers exclusively must not be named
// here: two exclusive providers in one assembly is a startup failure, and the
// module's own instance is the one that belongs there.
func Seams(tokens ...core.Token) []core.Module {
	out := make([]core.Module, 0, len(tokens))
	for _, token := range tokens {
		build, known := standIns[token]
		if !known {
			panic("routefixture: no stand-in is declared for the capability handed in")
		}
		out = append(out, build())
	}
	return out
}

// PublicURL is the address the assembly is reached at from outside. The mounter
// builds the cross-origin check from it, and the modules that name a cookie or
// build a redirect read it as well; the session module refuses to start without
// one, because the cookie it writes is named and scoped by that address.
const PublicURL = "https://app.example.com"

// Assembly is the part of the assembly every authentication module shares: the
// route-mounting capability's own module, the gateway, a credential-verifying
// backend, and a database, together with the configuration they run on. That
// configuration holds the deployment's own address and the database's file; a
// case merges its own module's section into it.
//
// The gateway is the real one and is taken in for the configuration as much as
// for the capability: the keys the deployment's own address is read from are
// the gateway's, so an assembly that left it out would have nowhere to write
// them, and the session module refuses to start with no address at all. The
// gateway in turn refuses to start without a backend that verifies a
// credential, so one is taken in beside it — unless module is itself a backend,
// which the session and token modules both are, and which two of them would
// make a claim conflict neither has.
func Assembly(t *testing.T, module core.Module) ([]core.Module, map[string]any) {
	t.Helper()
	database, databaseConfig := routecontract.SQLite(t)
	stubs := []core.Module{
		mount.Module(),
		gate.Module(),
		database,
	}
	if !provides(module, (*authn.Verifier)(nil)) {
		stubs = append(stubs, standIn("backend", Backend{}, (*authn.Verifier)(nil)))
	}
	config := routecontract.Config(databaseConfig, map[string]any{
		"authn": map[string]any{
			"gate": map[string]any{
				"mount": map[string]any{"public-url": PublicURL},
			},
		},
	})
	return stubs, config
}

// standIn is a module delivering one capability over the instance handed in.
func standIn(name string, instance any, token core.Token) core.Module {
	return routecontract.StandIn("stand-in."+name, instance, token)
}

// SigningKey writes a P-256 private key and returns the configuration subtree
// that points the token module at it, for a case to merge into its own.
//
// The token module refuses to construct without a key: there would be nothing
// to verify a token with. The key is generated rather than fixed, so that no
// private key is committed, and it is written where the module's own check
// reads it — mode 0600, or the module reports the file as readable by other
// users.
func SigningKey(t *testing.T) map[string]any {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("routefixture: generating a signing key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("routefixture: encoding the signing key: %v", err)
	}
	encoded := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	path := filepath.Join(t.TempDir(), "signing-key.pem")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatalf("routefixture: writing the signing key: %v", err)
	}
	return map[string]any{
		"authn": map[string]any{
			"token": map[string]any{"signing-key-file": path},
		},
	}
}

// provides reports whether a module delivers the capability a token designates.
func provides(module core.Module, token core.Token) bool {
	for _, provision := range module.Provides {
		if provision.Token == token {
			return true
		}
	}
	return false
}
