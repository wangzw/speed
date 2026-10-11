package gate

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

// sound is a configuration that resolves, which each case then spoils in one
// way. A case that spoils one field has to find that field's check refusing it,
// not another's.
func sound() moduleConfig {
	return moduleConfig{
		Endpoints:  []string{"public"},
		CallBudget: time.Second,
		InFlight:   8,
		Mount:      mountConfig{BasePath: "/auth"},
	}
}

// TestAConfigurationThisGatewayCannotRunOnIsRefused holds the inputs that are
// refused rather than run on.
//
// The repeated endpoint is the one worth naming: it would register the
// authentication layer twice on that endpoint, so every request would be
// authenticated twice and every backend asked twice — doubling the load on a
// credential store and every counter read off it, for no gain at all. The empty
// list and the wildcard written beside names are the two this module's
// default-to-everything makes mistakes worth refusing: neither can be read as
// what the writer meant.
func TestAConfigurationThisGatewayCannotRunOnIsRefused(t *testing.T) {
	for _, c := range []struct {
		name     string
		mutate   func(*moduleConfig)
		want     error
		mentions string
	}{
		{"an endpoint named twice", func(m *moduleConfig) { m.Endpoints = []string{"public", "admin", "public"} }, authn.ErrInvalidConfig, "twice"},
		{"the wildcard named twice", func(m *moduleConfig) { m.Endpoints = []string{"*", "*"} }, authn.ErrInvalidConfig, "twice"},
		{"an endpoint name that is empty", func(m *moduleConfig) { m.Endpoints = []string{""} }, authn.ErrInvalidConfig, "empty endpoint name"},
		{"an empty list", func(m *moduleConfig) { m.Endpoints = []string{} }, authn.ErrInvalidConfig, "empty list"},
		{"no list at all", func(m *moduleConfig) { m.Endpoints = nil }, authn.ErrInvalidConfig, "empty list"},
		{"the wildcard beside a name", func(m *moduleConfig) { m.Endpoints = []string{"*", "admin"} }, authn.ErrInvalidConfig, "beside named endpoints"},
		{"a name beside the wildcard", func(m *moduleConfig) { m.Endpoints = []string{"admin", "*"} }, authn.ErrInvalidConfig, "beside named endpoints"},
		{"a call budget of zero", func(m *moduleConfig) { m.CallBudget = 0 }, authn.ErrInvalidConfig, "never gives back"},
		{"a negative call budget", func(m *moduleConfig) { m.CallBudget = -time.Second }, authn.ErrInvalidConfig, "never gives back"},
		{"an in-flight gate that admits nothing", func(m *moduleConfig) { m.InFlight = 0 }, authn.ErrInvalidConfig, "refuses every request"},
		{"a negative in-flight gate", func(m *moduleConfig) { m.InFlight = -3 }, authn.ErrInvalidConfig, "refuses every request"},

		{"a base path with no leading slash", func(m *moduleConfig) { m.Mount.BasePath = "auth" }, authn.ErrInvalidConfig, mountBasePathItem},
		{"an empty base path", func(m *moduleConfig) { m.Mount.BasePath = "" }, authn.ErrInvalidConfig, mountBasePathItem},
		{"a base path that is the root", func(m *moduleConfig) { m.Mount.BasePath = "/" }, authn.ErrInvalidConfig, mountBasePathItem},
		{"a base path with a trailing slash", func(m *moduleConfig) { m.Mount.BasePath = "/auth/" }, authn.ErrInvalidConfig, mountBasePathItem},
		{"a base path with a wildcard", func(m *moduleConfig) { m.Mount.BasePath = "/auth/*all" }, authn.ErrInvalidConfig, mountBasePathItem},
		{"a base path with a parameter", func(m *moduleConfig) { m.Mount.BasePath = "/:tenant/auth" }, authn.ErrInvalidConfig, mountBasePathItem},
		{"a base path with a brace pattern", func(m *moduleConfig) { m.Mount.BasePath = "/auth/{tenant}" }, authn.ErrInvalidConfig, mountBasePathItem},
		{"a base path with a query", func(m *moduleConfig) { m.Mount.BasePath = "/auth?x=1" }, authn.ErrInvalidConfig, mountBasePathItem},
		{"a base path with a repeated slash", func(m *moduleConfig) { m.Mount.BasePath = "/a//b" }, authn.ErrInvalidConfig, mountBasePathItem},
		{"a base path with a dot segment", func(m *moduleConfig) { m.Mount.BasePath = "/a/../b" }, authn.ErrInvalidConfig, mountBasePathItem},

		{"a public URL that is not a URL", func(m *moduleConfig) { m.Mount.PublicURL = "not a url" }, authn.ErrInvalidConfig, mountPublicURLItem},
		{"a public URL with no host", func(m *moduleConfig) { m.Mount.PublicURL = "https://" }, authn.ErrInvalidConfig, mountPublicURLItem},
		{"a public URL of another scheme", func(m *moduleConfig) { m.Mount.PublicURL = "ftp://example.com" }, authn.ErrInvalidConfig, mountPublicURLItem},
		{"a public URL with a path", func(m *moduleConfig) { m.Mount.PublicURL = "https://example.com/app" }, authn.ErrInvalidConfig, "more than an origin"},
		{"a public URL with a trailing slash", func(m *moduleConfig) { m.Mount.PublicURL = "https://example.com/" }, authn.ErrInvalidConfig, "more than an origin"},
		{"a public URL with a query", func(m *moduleConfig) { m.Mount.PublicURL = "https://example.com?x=1" }, authn.ErrInvalidConfig, "more than an origin"},
		{"a public URL with a fragment", func(m *moduleConfig) { m.Mount.PublicURL = "https://example.com#x" }, authn.ErrInvalidConfig, "more than an origin"},
		{"a public URL with credentials", func(m *moduleConfig) { m.Mount.PublicURL = "https://u:p@example.com" }, authn.ErrInvalidConfig, "more than an origin"},
		{"a public URL that is http", func(m *moduleConfig) { m.Mount.PublicURL = "http://example.com" }, authn.ErrInsecureConfig, mountInsecureItem},
		{"a public URL that is http on a name that only looks like loopback", func(m *moduleConfig) { m.Mount.PublicURL = "http://localhost.example.com" }, authn.ErrInsecureConfig, mountInsecureItem},
		{"a public URL that is http on an address that is not loopback", func(m *moduleConfig) { m.Mount.PublicURL = "http://10.0.0.1:8080" }, authn.ErrInsecureConfig, mountInsecureItem},

		{"a trusted origin with a path", func(m *moduleConfig) { m.TrustedOrigins = []string{"https://a.example/x"} }, authn.ErrInvalidConfig, trustedOriginsItem},
		{"a trusted origin with no scheme", func(m *moduleConfig) { m.TrustedOrigins = []string{"a.example"} }, authn.ErrInvalidConfig, trustedOriginsItem},
		{"a trusted origin that is empty", func(m *moduleConfig) { m.TrustedOrigins = []string{""} }, authn.ErrInvalidConfig, trustedOriginsItem},
		{"a trusted origin that is a wildcard", func(m *moduleConfig) { m.TrustedOrigins = []string{"https://*.example.com"} }, authn.ErrInvalidConfig, trustedOriginsItem},
		{"a trusted origin with user information", func(m *moduleConfig) { m.TrustedOrigins = []string{"https://user@a.example"} }, authn.ErrInvalidConfig, trustedOriginsItem},
		{"a trusted origin with an empty port", func(m *moduleConfig) { m.TrustedOrigins = []string{"https://a.example:"} }, authn.ErrInvalidConfig, trustedOriginsItem},
		{"a trusted origin with a port out of range", func(m *moduleConfig) { m.TrustedOrigins = []string{"https://a.example:70000"} }, authn.ErrInvalidConfig, trustedOriginsItem},
		{"a trusted origin with a host that is not ASCII", func(m *moduleConfig) { m.TrustedOrigins = []string{"https://b\u00fccher.example"} }, authn.ErrInvalidConfig, trustedOriginsItem},
		{"a trusted origin after one that is sound", func(m *moduleConfig) { m.TrustedOrigins = []string{"https://a.example", "https://*.b.example"} }, authn.ErrInvalidConfig, "entry 2"},
		{"a public URL with an empty port", func(m *moduleConfig) { m.Mount.PublicURL = "https://example.com:" }, authn.ErrInvalidConfig, mountPublicURLItem},
		{"a public URL that is a wildcard", func(m *moduleConfig) { m.Mount.PublicURL = "https://*.example.com" }, authn.ErrInvalidConfig, mountPublicURLItem},
		{"a public URL with a host that is not ASCII", func(m *moduleConfig) { m.Mount.PublicURL = "https://b\u00fccher.example" }, authn.ErrInvalidConfig, mountPublicURLItem},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := sound()
			c.mutate(&m)
			_, err := m.resolve()
			if err == nil {
				t.Fatal("the configuration resolved without complaint")
			}
			if !strings.Contains(err.Error(), c.mentions) {
				t.Errorf("the failure reads %q and does not say what is wrong (looked for %q)", err, c.mentions)
			}
			// The sentinel table is closed over what this module judges itself,
			// and a caller tells a configuration it has to correct from every
			// other startup failure by it.
			if !errors.Is(err, c.want) {
				t.Errorf("the failure reads %q and does not wrap %v", err, c.want)
			}
		})
	}
}

// TestASoundConfigurationResolves keeps the cases above honest: each of them has
// to fail for its own mutation, not for the shape they all start from, and the
// edges of what is admitted are admitted.
func TestASoundConfigurationResolves(t *testing.T) {
	for _, c := range []struct {
		name   string
		mutate func(*moduleConfig)
		check  func(*testing.T, settings)
	}{
		{"as it stands", func(*moduleConfig) {}, func(t *testing.T, s settings) {
			if len(s.endpoints) != 1 || s.allGuarded() {
				t.Errorf("the settings guard %v, want public alone", s.endpoints)
			}
		}},
		{"two endpoints", func(m *moduleConfig) { m.Endpoints = []string{"public", "admin"} }, func(t *testing.T, s settings) {
			if len(s.endpoints) != 2 {
				t.Errorf("the settings guard %v, want both endpoints", s.endpoints)
			}
		}},
		{"the wildcard alone", func(m *moduleConfig) { m.Endpoints = []string{"*"} }, func(t *testing.T, s settings) {
			if !s.allGuarded() {
				t.Errorf("the wildcard is not read as every endpoint: %v", s.endpoints)
			}
		}},
		{"a base path of several segments", func(m *moduleConfig) { m.Mount.BasePath = "/a/b/c" }, nil},
		{"an https public URL", func(m *moduleConfig) { m.Mount.PublicURL = "https://example.com" }, func(t *testing.T, s settings) {
			if got, _ := mount.Origin(s.mount.PublicURL); got != "https://example.com" {
				t.Errorf("the origin reads %q", got)
			}
		}},
		{"an https public URL with a port", func(m *moduleConfig) { m.Mount.PublicURL = "https://example.com:8443" }, func(t *testing.T, s settings) {
			if got, _ := mount.Origin(s.mount.PublicURL); got != "https://example.com:8443" {
				t.Errorf("the origin reads %q", got)
			}
		}},
		{"an http public URL on localhost", func(m *moduleConfig) { m.Mount.PublicURL = "http://localhost:8080" }, nil},
		{"an http public URL on 127.0.0.1", func(m *moduleConfig) { m.Mount.PublicURL = "http://127.0.0.1:8080" }, nil},
		{"an http public URL on ::1", func(m *moduleConfig) { m.Mount.PublicURL = "http://[::1]:8080" }, nil},
		{"an http public URL with insecure use allowed", func(m *moduleConfig) {
			m.Mount.PublicURL = "http://example.com"
			m.Mount.AllowInsecure = true
		}, nil},
		{"no public URL", func(m *moduleConfig) { m.Mount.PublicURL = "" }, func(t *testing.T, s settings) {
			if got, _ := mount.Origin(s.mount.PublicURL); got != "" {
				t.Errorf("the origin reads %q with no public URL", got)
			}
		}},
		{"trusted origins", func(m *moduleConfig) {
			m.TrustedOrigins = []string{"https://a.example", "http://b.example:8080"}
		}, nil},
		{"a trusted origin written in a form a browser never sends", func(m *moduleConfig) {
			m.TrustedOrigins = []string{"HTTPS://A.Example:443"}
		}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := sound()
			c.mutate(&m)
			set, err := m.resolve()
			if err != nil {
				t.Fatalf("a sound configuration gave %v", err)
			}
			if c.check != nil {
				c.check(t, set)
			}
		})
	}
}

// TestTheDefaultsAreTheDesignsAndGuardEverything holds what a gateway is worth
// with no configuration file at all: it guards every endpoint, with a five
// second budget, a width of 64 and the base path /auth. The default is the side a
// mistake falls on refusal.
func TestTheDefaultsAreTheDesignsAndGuardEverything(t *testing.T) {
	reader := authntest.ConfigReader(t, Module(), `{}`)
	set, err := readSettings(reader)
	if err != nil {
		t.Fatalf("reading no configuration at all: %v", err)
	}
	if !set.allGuarded() {
		t.Errorf("the gateway guards %v by default, want every endpoint", set.endpoints)
	}
	if set.budget != 5*time.Second || set.inFlight != 64 {
		t.Errorf("the budget is %s and the width %d, want 5s and 64", set.budget, set.inFlight)
	}
	if set.mount.BasePath != "/auth" || set.mount.Endpoint != "" || set.mount.PublicURL != "" || set.mount.AllowInsecure {
		t.Errorf("the mount section reads %+v by default, want /auth and nothing else", set.mount)
	}
	if len(set.trustedOrigins) != 0 {
		t.Errorf("the gateway trusts %v by default, want no origin beyond the public URL's", set.trustedOrigins)
	}
}

// TestAGatewayToldToGuardNothingRefusesToStart holds the only answer this module
// has when the configuration writes an empty list: a startup failure naming the
// key. A gateway that guards no endpoint is a configuration mistake and not a way
// to switch authentication off, since every route on every endpoint would be
// served without a credential; the way to have no gateway is to leave the module
// out of the command. The absent key is not this case: it is the default, and the
// default guards everything.
func TestAGatewayToldToGuardNothingRefusesToStart(t *testing.T) {
	_, err := prepare(authntest.ConfigReader(t, Module(), `{"authn":{"gate":{"endpoints":[]}}}`))
	if err == nil {
		t.Fatal("a gateway with an empty endpoint list was carried into the run, so a command would " +
			"come up serving every route on every endpoint without a credential")
	}
	if !strings.Contains(err.Error(), ConfigNamespace+"."+endpointsItem) {
		t.Errorf("the failure reads %q and does not name the key to write", err)
	}
	if !errors.Is(err, authn.ErrInvalidConfig) {
		t.Errorf("the failure reads %q and does not wrap %v", err, authn.ErrInvalidConfig)
	}

	state, err := prepare(authntest.ConfigReader(t, Module(), `{}`))
	if err != nil {
		t.Fatalf("a gateway with no configuration at all was refused: %v", err)
	}
	if state.State != core.StateEnabled {
		t.Errorf("the gateway takes the stance %v, want enabled", state.State)
	}
}

// TestTheEndpointListAndTheMountKeysAreReadFromConfiguration holds the new keys
// through the real loader: the list in its three forms, and the mount section
// with each key.
func TestTheEndpointListAndTheMountKeysAreReadFromConfiguration(t *testing.T) {
	body := `{"authn":{"gate":{
		"endpoints":["public","admin"],
		"call-budget":"2s",
		"in-flight":4,
		"mount":{"endpoint":"admin","base-path":"/identity","public-url":"https://id.example.com","allow-insecure":false},
		"trusted-origins":["https://app.example.com"]
	}}}`
	set, err := readSettings(authntest.ConfigReader(t, Module(), body))
	if err != nil {
		t.Fatalf("reading the configuration: %v", err)
	}
	if len(set.endpoints) != 2 || set.endpoints[0] != "public" || set.endpoints[1] != "admin" {
		t.Errorf("the endpoints read %v", set.endpoints)
	}
	if set.budget != 2*time.Second || set.inFlight != 4 {
		t.Errorf("the budget reads %s and the width %d", set.budget, set.inFlight)
	}
	if set.mount != (mountConfig{Endpoint: "admin", BasePath: "/identity", PublicURL: "https://id.example.com"}) {
		t.Errorf("the mount section reads %+v", set.mount)
	}
	if len(set.trustedOrigins) != 1 || set.trustedOrigins[0] != "https://app.example.com" {
		t.Errorf("the trusted origins read %v", set.trustedOrigins)
	}

	set, err = readSettings(authntest.ConfigReader(t, Module(), `{"authn":{"gate":{"endpoints":["*"]}}}`))
	if err != nil || !set.allGuarded() {
		t.Errorf("the wildcard in a file reads %v (%v), want every endpoint", set.endpoints, err)
	}
}

// TestTheKeysThatDecideWhatIsGuardedTakeTheMainSourceAlone holds the origins of
// the three items that decide the guard: the endpoint list, the trusted origins
// and the switch that relaxes the checks. A command line argument must not be
// able to change which endpoints are guarded or which origins are trusted, and
// the insecure switch is for the file a deployment is reviewed by.
func TestTheKeysThatDecideWhatIsGuardedTakeTheMainSourceAlone(t *testing.T) {
	items := schema().Items
	for _, key := range []string{endpointsItem, trustedOriginsItem, mountInsecureItem} {
		item, found := items[key]
		if !found {
			t.Errorf("the schema does not declare %q", key)
			continue
		}
		if item.Origins != config.OriginPrimary {
			t.Errorf("%s takes the origins %v, want the primary source alone", key, item.Origins)
		}
		if item.Required {
			t.Errorf("%s is required, but every key has a default", key)
		}
	}
	for _, key := range []string{budgetItem, inFlightItem, mountEndpointItem, mountBasePathItem, mountPublicURLItem} {
		if _, found := items[key]; !found {
			t.Errorf("the schema does not declare %q", key)
		}
	}
	for key, item := range items {
		if item.Required {
			t.Errorf("%s is declared required, and the gateway declares no required key", key)
		}
		if item.Description == "" {
			t.Errorf("%s has no description", key)
		}
	}
}

// TestWhereAHostWritesThisModulesInputs pins the two names that are easy to swap
// for one another, because both are spelled out of the same two words.
//
// The configuration namespace is this module's own name: each backend hangs its
// section under the capability directory too, and a gateway that took the bare
// heading would sit on the path they extend. The startup diagnostics go the other
// way — they are prefixed with the capability directory, because a reader
// scanning them is looking for what authentication did, not for which of its
// modules said so.
func TestWhereAHostWritesThisModulesInputs(t *testing.T) {
	if ConfigNamespace != ModuleName {
		t.Errorf("the configuration namespace is %q while the module is %q, so a host writing "+
			"one would be read by neither", ConfigNamespace, ModuleName)
	}
	if diagnosticPrefix != "authn: " {
		t.Errorf("the startup diagnostics are prefixed %q, want %q", diagnosticPrefix, "authn: ")
	}
}

// TestARefusedOriginIsNotCopiedIntoTheFailure holds the entries a refusal must
// not quote: a trusted origin or a public URL written with a password where none
// belongs is named by its position, and the refusal's text carries nothing of it.
func TestARefusedOriginIsNotCopiedIntoTheFailure(t *testing.T) {
	const secret = "hunter2-do-not-print"
	for _, c := range []struct {
		name   string
		mutate func(*moduleConfig)
	}{
		{"a trusted origin with user information", func(m *moduleConfig) {
			m.TrustedOrigins = []string{"https://a.example", "https://ops:" + secret + "@b.example"}
		}},
		{"a trusted origin with an empty port and user information", func(m *moduleConfig) {
			m.TrustedOrigins = []string{"https://ops:" + secret + "@b.example:"}
		}},
		{"a public URL with user information", func(m *moduleConfig) {
			m.Mount.PublicURL = "https://ops:" + secret + "@b.example"
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := sound()
			c.mutate(&m)
			_, err := m.resolve()
			if err == nil {
				t.Fatal("the configuration resolved without complaint")
			}
			if strings.Contains(err.Error(), secret) {
				t.Errorf("the failure copies the secret: %q", err)
			}
		})
	}
}
