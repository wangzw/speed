// Package routefixture is the assembly the authentication capability's route
// checks run on: the real route-mounting module and gateway, a backend that
// refuses every credential, and stand-ins embedding one interface each.
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
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/settings"
)

// Backend is a credential-verifying backend of a host's own: it names itself
// and declares the carrier it takes, and it is taken in only when the module
// under test is not itself a backend.
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

// The stand-in types, one per capability; each embeds its interface and
// implements no method, so resolving one and holding it never calls a method.
// A capability called during Init is supplied for real instead.
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
	tenantDescs      struct {
		authn.TenantDescriptionResolver
	}
	smsCodes        struct{ authn.SMSCodes }
	emailCodeSender struct{ authn.EmailCodeSender }
	humanVerifier   struct{ authn.HumanVerifier }
	auditCap        struct{ audit.Audit }
	settingsStore   struct{ settings.Store }
	keyValue        struct{ kv.KV }
)

// impersonation carries the register read the administration module asserts
// before it mounts the group of routes that show a grant; an assembly that
// wants those routes declared must satisfy it.
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
	(*authn.HumanVerifier)(nil): func() core.Module {
		return standIn("human-verifier", &humanVerifier{}, (*authn.HumanVerifier)(nil))
	},
	(*audit.Audit)(nil):    func() core.Module { return standIn("audit", &auditCap{}, (*audit.Audit)(nil)) },
	(*settings.Store)(nil): func() core.Module { return standIn("settings-store", &settingsStore{}, (*settings.Store)(nil)) },
	(*kv.KV)(nil):          func() core.Module { return standIn("key-value", &keyValue{}, (*kv.KV)(nil)) },
	(*authn.TenantDescriptionResolver)(nil): func() core.Module {
		return standIn("tenant-descriptions", &tenantDescs{}, (*authn.TenantDescriptionResolver)(nil))
	},
}

// Seams is the stand-in set for exactly the capabilities named. A capability
// the module under test delivers exclusively must not be named here, since two
// exclusive providers in one assembly is a startup failure.
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

// PublicURL is the address the assembly is reached at from outside; the mounter
// builds the cross-origin check from it, and the session module refuses to
// start without one.
const PublicURL = "https://app.example.com"

// Assembly is the part every authentication module's check shares: the real
// route-mounting module, gateway and a database, a verifying backend unless
// module is one itself, and the configuration they run on.
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
				"mount": map[string]any{
					"public-url": PublicURL,
					// The fixture drives to Init, where the tenant scope's
					// routes need an endpoint to mount on; the gateway's Start
					// would judge it against tenant layers this assembly lacks.
					"endpoints": map[string]any{"tenants": routecontract.DefaultEndpoint},
				},
			},
		},
	})
	return stubs, config
}

func standIn(name string, instance any, token core.Token) core.Module {
	return routecontract.StandIn("stand-in."+name, instance, token)
}

// SigningKey writes a generated P-256 private key, mode 0600 where the token
// module reads it, and returns the configuration subtree that points the module
// at it; the module refuses to construct without a key.
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

func provides(module core.Module, token core.Token) bool {
	for _, provision := range module.Provides {
		if provision.Token == token {
			return true
		}
	}
	return false
}
