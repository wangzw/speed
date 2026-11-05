package admin

import (
	"context"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/feature"
	featureboard "github.com/vislake/speed/pkg/feature/board"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/internal/routecontract"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
)

// The cases here drive the real feature board through a real registry, to Start,
// beside the real administration module. They assert the three facts the design
// ties together for every group: the capability is present exactly when the
// group's name is in the frozen set, exactly when the group's routes are
// registered. Swapping two groups' conditions makes the two sides disagree.

// featureCapture keeps what the board logs, so a case reads the frozen set back.
type featureCapture struct {
	mu      sync.Mutex
	records []slog.Record
}

func (*featureCapture) Enabled(context.Context, slog.Level) bool { return true }

func (c *featureCapture) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, r.Clone())
	return nil
}

func (c *featureCapture) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *featureCapture) WithGroup(string) slog.Handler      { return c }

func (c *featureCapture) names(t *testing.T) []string {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.records {
		if r.Message != "feature set frozen" {
			continue
		}
		var names []string
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == "names" {
				names, _ = a.Value.Any().([]string)
				return false
			}
			return true
		})
		return names
	}
	t.Fatal("the feature board logged no frozen set")
	return nil
}

// diagnostic is the administration module's startup line, which names the
// route groups it mounted.
func (c *featureCapture) diagnostic(t *testing.T) string {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.records {
		if strings.Contains(r.Message, "the management routes are mounted on the endpoint") {
			return r.Message
		}
	}
	t.Fatal("the administration module logged no startup diagnostic")
	return ""
}

type captureLogger struct{ capture *featureCapture }

func (l captureLogger) Named(string) *slog.Logger { return slog.New(l.capture) }
func (captureLogger) Redaction() log.Redaction    { return noRedaction{} }

type noRedaction struct{}

func (noRedaction) AddKeys(...string)              {}
func (noRedaction) AddPattern(string, log.Matcher) {}

// openGate stands in for the authentication gateway, which only has to accept
// the board's impersonable registration.
type openGate struct{}

func (openGate) Exempt(string, string, string)       {}
func (openGate) Optional(string, string, string)     {}
func (openGate) Impersonable(string, string, string) {}

// capabilities names the optional groups a case switches on.
type capabilities struct {
	sessions    bool
	factors     bool
	secrets     bool
	sso         bool
	refresher   bool
	impersonate bool
}

// The two configuration fragments the cases assemble: the feature board's
// endpoint, and the item that turns the SSO provider group on. An absent item
// reads as off, which is the module's declared default.
const (
	featureBoardConfig = `"feature": {"board": {"endpoint": "account"}}`
	ssoEnabledConfig   = `"authn": {"admin": {"sso": {"enabled": true}}}`
)

// driveWithBoard assembles the administration module, the real board and the
// stand-ins both need, and advances to Start. The SSO management group is
// enabled, because the cases here hold the groups against their capabilities,
// not the configuration gate. It returns the mounter, so a case reads the
// groups the module registered, and the frozen feature names.
func driveWithBoard(t *testing.T, on capabilities) (*mounttest.Mounter, []string) {
	t.Helper()
	mounter, names, _ := driveWithConfig(t, on, "{"+featureBoardConfig+", "+ssoEnabledConfig+"}")
	return mounter, names
}

// driveWithConfig is driveWithBoard over a configuration body the case writes.
// It also returns the module's startup diagnostic, which names the mounted
// groups.
func driveWithConfig(t *testing.T, on capabilities, body string) (*mounttest.Mounter, []string, string) {
	t.Helper()

	mounter := mounttest.New(t)
	router := routecontract.NewRouter("account")
	capture := &featureCapture{}
	reader := authntest.ConfigReaderAll(t, body, featureboard.Module(), Module())

	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	reg.Register(Module())
	reg.Register(featureboard.Module())
	participants := []string{ModuleName, featureboard.ModuleName}

	register := func(m core.Module) {
		reg.Register(m)
		participants = append(participants, m.Name)
	}
	register(standIn("stand-in.mounter", mounter, (*mount.Mounter)(nil)))
	register(standIn("stand-in.accounts", newAccounts(), (*authn.Accounts)(nil)))
	register(standIn("stand-in.account-admin", newAccountAdmin(), (*authn.AccountAdmin)(nil)))
	register(standIn("stand-in.memberships", newMemberships(), (*authn.Memberships)(nil)))
	register(standIn("stand-in.signup-state", &fakeSignupVerification{}, (*authn.SignupVerification)(nil)))
	register(standIn("stand-in.audit", &recordingAudit{}, (*audit.Audit)(nil)))
	register(standIn("stand-in.settings", newStore(), (*settings.Store)(nil)))
	register(standIn("stand-in.router", router, (*speedhttp.Router)(nil)))
	register(standIn("stand-in.authn-gate", openGate{}, (*authn.Gate)(nil)))
	register(standIn("stand-in.logging", captureLogger{capture: capture}, (*log.Logger)(nil)))

	if on.sessions {
		register(standIn("stand-in.sessions", newSessions(), (*authn.Sessions)(nil)))
	}
	if on.factors {
		register(standIn("stand-in.mfa", &fakeMFA{}, (*authn.MFA)(nil)))
	}
	if on.secrets {
		register(standIn("stand-in.tokens", &fakeTokens{}, (*authn.Tokens)(nil)))
	}
	if on.sso {
		register(standIn("stand-in.sso", newProviders(), (*authn.SSOProviders)(nil)))
	}
	if on.refresher {
		register(standIn("stand-in.refresher", &fakeRefresher{protocol: "oidc"}, (*authn.SSOMetadataRefresher)(nil)))
	}
	if on.impersonate {
		register(standIn("stand-in.impersonation", newRecords(), (*authn.Impersonation)(nil)))
	}

	if err := reg.Drive(t.Context(), core.Drive{Depth: core.StageStart, Participants: participants}); err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	return mounter, capture.names(t), capture.diagnostic(t)
}

// TestAGroupIsPresentExactlyWhenItIsNamedAndMounted holds the equivalence for
// every group: capability present, exactly when the name is frozen, exactly when
// the group's routes are registered. The static name is in every combination.
func TestAGroupIsPresentExactlyWhenItIsNamedAndMounted(t *testing.T) {
	for _, tc := range []struct {
		name string
		on   capabilities
	}{
		{name: "none"},
		{name: "all", on: capabilities{sessions: true, factors: true, secrets: true, sso: true, refresher: true, impersonate: true}},
		{name: "sessions", on: capabilities{sessions: true}},
		{name: "factors", on: capabilities{factors: true}},
		{name: "secrets", on: capabilities{secrets: true}},
		{name: "sso without a refresher", on: capabilities{sso: true}},
		{name: "sso with a refresher", on: capabilities{sso: true, refresher: true}},
		{name: "refresher without sso", on: capabilities{refresher: true}},
		{name: "impersonation records", on: capabilities{impersonate: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mounter, names := driveWithBoard(t, tc.on)

			if !slices.Contains(names, featureAdmin) {
				t.Errorf("%s is absent from %v, but the account and membership groups are always mounted", featureAdmin, names)
			}
			for _, g := range []struct {
				feature string
				on      bool
				method  string
				path    string
			}{
				{featureSessions, tc.on.sessions, http.MethodGet, "/api/v1/system/auth/accounts/:id/sessions"},
				{featureFactors, tc.on.factors, http.MethodGet, "/api/v1/system/auth/accounts/:id/factors"},
				{featureServiceKeys, tc.on.secrets, http.MethodGet, "/api/v1/system/auth/accounts/:id/secrets"},
				{featureSSO, tc.on.sso, http.MethodGet, "/api/v1/tenants/:tenant/sso/providers"},
				{featureSSOMetadata, tc.on.sso && tc.on.refresher, http.MethodPost, "/api/v1/tenants/:tenant/sso/providers/:id/metadata/refresh"},
				{featureImpersonations, tc.on.impersonate, http.MethodGet, "/api/v1/system/tenants/:tenant/impersonations"},
			} {
				_, mounted := mounter.Registered(g.method, g.path)
				if got := slices.Contains(names, g.feature); got != g.on {
					t.Errorf("%s present = %v, want %v; the set is %v", g.feature, got, g.on, names)
				}
				if mounted != g.on {
					t.Errorf("%s mounted = %v, want %v", g.method+" "+g.path, mounted, g.on)
				}
			}

			// The static name's groups are mounted in every combination.
			for _, r := range []struct{ method, path string }{
				{http.MethodGet, "/api/v1/system/auth/accounts"},
				{http.MethodGet, "/api/v1/tenants/:tenant/members"},
			} {
				if _, ok := mounter.Registered(r.method, r.path); !ok {
					t.Errorf("%s %s is not mounted, but the static group is always mounted", r.method, r.path)
				}
			}
		})
	}
}

// TestTheSSOGroupFollowsItsConfigurationItem holds the one configuration gate:
// the register capability being present is not enough, because it says the
// process could manage providers, not that this deployment offers the
// management surface. Off or absent, the group's routes are not registered,
// neither feature is frozen into the document, and the startup diagnostic names
// no SSO group.
func TestTheSSOGroupFollowsItsConfigurationItem(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want bool
	}{
		{name: "the item is absent", body: "{" + featureBoardConfig + "}", want: false},
		{
			name: "the item is false",
			body: "{" + featureBoardConfig + `, "authn": {"admin": {"sso": {"enabled": false}}}}`,
			want: false,
		},
		{
			name: "the item is true",
			body: "{" + featureBoardConfig + ", " + ssoEnabledConfig + "}",
			want: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The register and the refresher are both present, so only the item
			// can make the answer differ.
			mounter, names, diagnostic := driveWithConfig(t,
				capabilities{sso: true, refresher: true}, tc.body)

			if _, mounted := mounter.Registered(http.MethodGet, "/api/v1/tenants/:tenant/sso/providers"); mounted != tc.want {
				t.Errorf("the provider route is mounted = %v, want %v", mounted, tc.want)
			}
			if _, mounted := mounter.Registered(http.MethodPost, "/api/v1/tenants/:tenant/sso/providers/:id/metadata/refresh"); mounted != tc.want {
				t.Errorf("the metadata-refresh route is mounted = %v, want %v", mounted, tc.want)
			}
			for _, feature := range []string{featureSSO, featureSSOMetadata} {
				if got := slices.Contains(names, feature); got != tc.want {
					t.Errorf("%s present = %v, want %v; the set is %v", feature, got, tc.want, names)
				}
			}
			for _, group := range []string{"sso-providers", "sso-metadata-refresh"} {
				if got := strings.Contains(diagnostic, group); got != tc.want {
					t.Errorf("the diagnostic names the %s group = %v, want %v: %s", group, got, tc.want, diagnostic)
				}
			}
		})
	}
}

// TestEveryDeclaredNameIsSpecified keeps the declarations and the posts in one
// place: every name a case can see frozen above is declared with the right
// conditionality.
func TestEveryDeclaredNameIsSpecified(t *testing.T) {
	want := map[string]bool{
		featureAdmin:          false,
		featureSessions:       true,
		featureFactors:        true,
		featureServiceKeys:    true,
		featureSSO:            true,
		featureSSOMetadata:    true,
		featureImpersonations: true,
	}
	got := map[string]bool{}
	for _, r := range Module().Resources {
		if s, ok := r.(feature.Spec); ok {
			if _, twice := got[s.Name]; twice {
				t.Errorf("%s is declared twice", s.Name)
			}
			got[s.Name] = s.Conditional
		}
	}
	for name, conditional := range want {
		declared, ok := got[name]
		if !ok {
			t.Errorf("%s is not declared", name)
			continue
		}
		if declared != conditional {
			t.Errorf("%s Conditional = %v, want %v", name, declared, conditional)
		}
	}
	if len(got) != len(want) {
		t.Errorf("declared %d names, want %d: %v", len(got), len(want), got)
	}
}
