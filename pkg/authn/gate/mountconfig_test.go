package gate

import (
	"errors"
	"log/slog"
	nethttp "net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// envPrefix is the prefix the loader in these cases reads variables under. It is
// one no machine carries variables under, so only what a case sets reaches the
// values.
const envPrefix = "SPEEDGATETEST"

// loadSettings runs the real loader over a JSON primary source holding body, with
// args as the command line, and reads this module's settings from the result. The
// environment is the process's own, which a case steers with t.Setenv.
func loadSettings(t *testing.T, body string, args ...string) (settings, error) {
	t.Helper()
	locator := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(locator, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the primary config source: %v", err)
	}
	reg := core.New()
	reg.Register(jsonformat.Module())
	reg.Register(filesource.Module())
	reg.Register(core.Module{
		Name:      "host",
		Resources: []any{config.HostIdentity{Prefix: envPrefix, DefaultLocator: "file://" + locator}},
	})
	reg.Register(Module())
	reader, err := config.Load(t.Context(), reg, args)
	if err != nil {
		return settings{}, err
	}
	return readSettings(reader)
}

// TestAnEnvironmentVariableCannotRelaxWhatTheMainSourceStates holds the item
// that widens what the gateway accepts: the list of origins trusted beyond
// the public one.
func TestAnEnvironmentVariableCannotRelaxWhatTheMainSourceStates(t *testing.T) {
	const gate = "SPEEDGATETEST_AUTHN__GATE__"

	t.Run("the control: an item that takes the environment", func(t *testing.T) {
		t.Setenv(gate+"MOUNT__PUBLIC_URL", "https://env.example.com")
		set, err := loadSettings(t, `{}`)
		if err != nil {
			t.Fatalf("loading: %v", err)
		}
		if set.mount.PublicURL != "https://env.example.com" {
			t.Fatalf("the public URL reads %q, so the variable name used here is not one the loader reads "+
				"and the cases below prove nothing", set.mount.PublicURL)
		}
	})

	t.Run("the trusted origins", func(t *testing.T) {
		t.Setenv(gate+"TRUSTED_ORIGINS", "https://evil.example")
		set, err := loadSettings(t, `{}`)
		if err != nil {
			t.Fatalf("loading: %v", err)
		}
		if len(set.trustedOrigins) != 0 {
			t.Errorf("the gateway trusts %v, which a variable put there", set.trustedOrigins)
		}
	})
}

// TestACommandLineArgumentCannotRelaxWhatTheMainSourceStates holds the same
// item against the command line, which a process listing shows and a wrapper
// script can add to.
func TestACommandLineArgumentCannotRelaxWhatTheMainSourceStates(t *testing.T) {
	set, err := loadSettings(t, `{}`, "--authn.gate.trusted-origins=https://evil.example")
	if err == nil && len(set.trustedOrigins) != 0 {
		t.Errorf("the gateway trusts %v, which an argument put there", set.trustedOrigins)
	}
	if err != nil && !strings.Contains(err.Error(), "trusted-origins") {
		t.Errorf("loading failed with %q, which does not say the argument was the problem", err)
	}
}

// TestTheMainSourceCanRelaxWhatItAloneMay is the control for the cases above: the
// item does take a value from the main source, so a refusal there is a refusal of
// the other origins and not of the item.
func TestTheMainSourceCanRelaxWhatItAloneMay(t *testing.T) {
	set, err := loadSettings(t, `{"authn":{"gate":{"mount":{"public-url":"https://example.com"},`+
		`"trusted-origins":["https://app.example.com"]}}}`)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	if len(set.trustedOrigins) != 1 || set.trustedOrigins[0] != "https://app.example.com" {
		t.Errorf("the settings read %+v and %v", set.mount, set.trustedOrigins)
	}
}

// TestAnHTTPPublicURLStartsOnlyOnALoopbackHost holds the one form an http
// public URL may take: a loopback host, whose traffic never leaves the
// machine.
func TestAnHTTPPublicURLStartsOnlyOnALoopbackHost(t *testing.T) {
	for _, c := range []struct {
		name  string
		body  string
		want  error
		named string
	}{
		{
			"an http address", `{"authn":{"gate":{"mount":{"public-url":"http://example.com"}}}}`,
			authn.ErrInsecureConfig, mountPublicURLItem,
		},
		{
			"an http private address", `{"authn":{"gate":{"mount":{"public-url":"http://10.0.0.5:8080"}}}}`,
			authn.ErrInsecureConfig, mountPublicURLItem,
		},
		{
			"an http address with allow-insecure written beside it",
			`{"authn":{"gate":{"mount":{"public-url":"http://example.com","allow-insecure":true}}}}`,
			config.ErrUnknownKey, "authn.gate.mount.allow-insecure",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := loadSettings(t, c.body)
			if !errors.Is(err, c.want) {
				t.Fatalf("loading gave %v, want %v", err, c.want)
			}
			if !strings.Contains(err.Error(), c.named) {
				t.Errorf("the refusal %q does not name %s", err, c.named)
			}
		})
	}

	for _, address := range []string{"http://localhost:8080", "http://127.0.0.1", "http://[::1]:8080"} {
		t.Run(address, func(t *testing.T) {
			set, err := loadSettings(t, `{"authn":{"gate":{"mount":{"public-url":"`+address+`"}}}}`)
			if err != nil {
				t.Fatalf("loading a loopback http address: %v", err)
			}
			if set.mount.PublicURL != address {
				t.Errorf("the public URL reads %q, want %q", set.mount.PublicURL, address)
			}
		})
	}
}

func warnings(logs *recorder) []slog.Record {
	var out []slog.Record
	for _, r := range logs.recordsOf() {
		if r.Level >= slog.LevelWarn {
			out = append(out, r)
		}
	}
	return out
}

func messages(records []slog.Record) []string {
	out := make([]string, len(records))
	for i, r := range records {
		out[i] = r.Message
	}
	return out
}

// TestACookieWithoutTheHostPrefixIsWarnedAboutOnceEach holds the diagnostic for a
// cookie name a backend takes: the one that is host-only by its prefix needs
// none, and each that is not gets a line of its own naming the backend and the
// cookie, and never a value.
func TestACookieWithoutTheHostPrefixIsWarnedAboutOnceEach(t *testing.T) {
	logs := liveAssembly{
		backends: []authn.Verifier{
			cookieBackend("plain", "sid", "other"),
			cookieBackend("hardened", "__Host-sid"),
			basicBackend("static", "static."),
		},
	}.serve(t, func(*live) {})

	var named []string
	for _, r := range warnings(logs) {
		if strings.Contains(r.Message, hostCookiePrefix) {
			named = append(named, attrText(r, "backend")+"/"+attrText(r, "cookie_name"))
		}
	}
	if want := "plain/other plain/sid"; strings.Join(named, " ") != want {
		t.Errorf("the cookie warnings name %v, want one each for %s and none for the __Host- one", named, want)
	}
}

// TestTheCookiePrefixDiagnosticFollowsThePublicURL holds the diagnostic on
// each kind of public URL.
func TestTheCookiePrefixDiagnosticFollowsThePublicURL(t *testing.T) {
	for _, c := range []struct {
		publicURL string
		http      bool
	}{
		{"https://app.example.com", false},
		{"https://localhost:8443", false},
		{"http://localhost:8080", true},
		{"http://127.0.0.1:8080", true},
		{"http://[::1]:8080", true},
	} {
		t.Run(c.publicURL, func(t *testing.T) {
			logs := liveAssembly{
				gateItems: `"mount":{"public-url":"` + c.publicURL + `"}`,
				backends:  []authn.Verifier{cookieBackend("session", "authn-sid", "__Host-authn-sid")},
			}.serve(t, func(*live) {})

			var warned []string
			for _, r := range warnings(logs) {
				if strings.Contains(r.Message, hostCookiePrefix) {
					warned = append(warned, attrText(r, "cookie_name"))
				}
			}
			var told int
			for _, r := range logs.recordsOf() {
				if r.Level == slog.LevelInfo && strings.Contains(r.Message, "carry no __Host- or __Secure- prefix and are not Secure") {
					told++
					if attrText(r, "setting") != ConfigNamespace+"."+mountPublicURLItem {
						t.Errorf("the Info line names the setting %q, want %s.%s", attrText(r, "setting"),
							ConfigNamespace, mountPublicURLItem)
					}
				}
			}
			if c.http && (len(warned) != 0 || told != 1) {
				t.Errorf("on %s the diagnostics warned about %v and wrote %d Info line(s), want no Warn and one Info",
					c.publicURL, warned, told)
			}
			if !c.http && (strings.Join(warned, " ") != "authn-sid" || told != 0) {
				t.Errorf("on %s the diagnostics warned about %v and wrote %d Info line(s), want one Warn for "+
					"authn-sid and no Info", c.publicURL, warned, told)
			}
		})
	}
}

// TestAMountEndpointTheResolutionListCannotBeReadForIsOnlyAWarning holds the
// failure direction of the one check that reads another module's
// configuration: when the list has a shape this module cannot decode, the
// startup goes on and says on a Warn line that the endpoint was not checked
// against it.
func TestAMountEndpointTheResolutionListCannotBeReadForIsOnlyAWarning(t *testing.T) {
	type stand struct {
		name string
		top  string
		mod  func() core.Module
	}
	badShape := func() core.Module {
		type shape struct {
			Endpoints map[string]int `config:"endpoints"`
		}
		return core.Module{
			Name: "stand-in.resolution",
			Resources: []any{config.Schema{
				Namespace: "tenant.resolve",
				Mounts:    []config.Mount{{Value: &shape{}}},
				Items:     map[string]config.Item{"endpoints": {Origins: config.OriginPrimary}},
			}},
		}
	}
	for _, c := range []stand{
		{"the list has a shape this module cannot decode", `"tenant":{"resolve":{"endpoints":{"a":1}}}`, badShape},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := liveAssembly{
				gateItems: `"mount":{"endpoints":{"default":"public"}}`,
				topItems:  c.top,
				backends:  []authn.Verifier{basicBackend("static", "static.")},
			}
			if c.mod != nil {
				a.modules = []core.Module{c.mod()}
			}
			logs := a.serve(t, func(*live) {})

			var found bool
			for _, r := range warnings(logs) {
				if strings.Contains(r.Message, "tenant resolution layer's endpoint list") {
					found = true
					if attrText(r, "key") != "tenant.resolve.endpoints" {
						t.Errorf("the warning names the key %q, want tenant.resolve.endpoints", attrText(r, "key"))
					}
				}
			}
			if !found {
				t.Errorf("the startup went on without a Warn line saying the check was skipped: %v", messages(warnings(logs)))
			}
		})
	}
}

// fakeMounter is the module that mounts the capability's routes as the gateway
// sees it: a delivered Mounter and nothing else. The gateway reads whether one
// is there, never what it does.
type fakeMounter struct{}

func (fakeMounter) Public(string, string, gin.HandlerFunc, ...mount.Option)   {}
func (fakeMounter) Optional(string, string, gin.HandlerFunc, ...mount.Option) {}
func (fakeMounter) Guarded(string, string, gin.HandlerFunc, ...mount.Option)  {}
func (fakeMounter) Callback([]string, string, gin.HandlerFunc)                {}
func (fakeMounter) BaseIn(speedhttp.Scope) string                             { return "/api/v1/auth" }
func (fakeMounter) Endpoint() string                                          { return "public" }
func (fakeMounter) PublicURL() string                                         { return "" }
func (fakeMounter) CSRF() *nethttp.CrossOriginProtection                      { return nil }

func mounting() supply {
	return supply{module: "stand-in.mount", token: (*mount.Mounter)(nil), instance: fakeMounter{}}
}

// TestAMountEndpointLeftEmptyIsNotJudgedWhenNoModuleMountsRoutes holds that
// the gateway picks no endpoint of its own accord: with the key empty and no
// module that mounts routes in the assembly nothing is checked, whatever the
// entry point declares and whatever the admission layer or the resolution
// layer covers, and no sentinel about the routes is produced.
func TestAMountEndpointLeftEmptyIsNotJudgedWhenNoModuleMountsRoutes(t *testing.T) {
	err := assembly{
		body:     `{"authn":{"gate":{"endpoints":["public"]}},"tenant":{"resolve":{"endpoints":["public"]}}}`,
		backends: []authn.Verifier{basicBackend("static", "static.")},
		router:   routerWith("public"),
		modules:  []core.Module{resolutionLayer("")},
		supplied: []supply{{module: "stand-in.admission", token: (*authn.ScopeGuard)(nil), instance: fakeScope{"public": true}}},
	}.drive(t, nothing)
	if err != nil {
		t.Fatalf("the drive failed: %v: the only endpoint is covered by both layers, the key is empty and nothing mounts routes", err)
	}
}

// TestAMountEndpointLeftEmptyIsJudgedWhenAModuleMountsRoutes holds the same
// assembly with a module that mounts routes added: the empty key is then the
// only endpoint, and the gateway refuses it for what covers it, from the real
// descriptor and its stages.
func TestAMountEndpointLeftEmptyIsJudgedWhenAModuleMountsRoutes(t *testing.T) {
	admission := func(covered bool) supply {
		return supply{module: "stand-in.admission", token: (*authn.ScopeGuard)(nil), instance: fakeScope{"public": covered}}
	}
	for _, c := range []struct {
		name     string
		body     string
		modules  []core.Module
		supplied []supply
		want     string
	}{
		{
			"the admission layer guards the only endpoint",
			`{"authn":{"gate":{"endpoints":["public"]}}}`, nil,
			[]supply{mounting(), admission(true)},
			"tenant admission layer",
		},
		{
			"the admission layer guards another endpoint",
			`{"authn":{"gate":{"endpoints":["public"]}}}`, nil,
			[]supply{mounting(), admission(false)},
			"",
		},
		{
			"the resolution layer resolves a tenant on the only endpoint",
			`{"authn":{"gate":{"endpoints":["public"]}},"tenant":{"resolve":{"endpoints":["public"]}}}`,
			[]core.Module{resolutionLayer("")},
			[]supply{mounting()},
			"tenant.resolve.endpoints",
		},
		{
			"the resolution layer resolves a tenant on another endpoint",
			`{"authn":{"gate":{"endpoints":["public"]}},"tenant":{"resolve":{"endpoints":["admin"]}}}`,
			[]core.Module{resolutionLayer("")},
			[]supply{mounting()},
			"",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := assembly{
				body:     c.body,
				backends: []authn.Verifier{basicBackend("static", "static.")},
				router:   routerWith("public"),
				modules:  c.modules,
				supplied: c.supplied,
			}.drive(t, nothing)
			if c.want == "" {
				if err != nil {
					t.Fatalf("the drive failed: %v", err)
				}
				return
			}
			if !errors.Is(err, authn.ErrInvalidConfig) {
				t.Fatalf("the drive returned %v, want one wrapping %v", err, authn.ErrInvalidConfig)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("the failure reads %q and does not carry %q", err, c.want)
			}
			if !strings.Contains(err.Error(), defaultMountItem) || !strings.Contains(err.Error(), `"public"`) {
				t.Errorf("the failure reads %q and does not name the key and the endpoint it took", err)
			}
		})
	}
}

// TestAModuleThatMountsRoutesWithNoEndpointToPickIsRefusedAtStartToo holds the
// fallback for a mounting module that did not settle the endpoint itself: with
// the key empty and several endpoints declared the gateway reaches the same
// sentinel from the same rule, so the routes are never left on an endpoint nobody
// chose.
func TestAModuleThatMountsRoutesWithNoEndpointToPickIsRefusedAtStartToo(t *testing.T) {
	err := assembly{
		body:     `{}`,
		backends: []authn.Verifier{basicBackend("static", "static.")},
		router:   routerWith("admin", "public"),
		supplied: []supply{mounting()},
	}.drive(t, nothing)
	if !errors.Is(err, authn.ErrRoutesUnconfigured) {
		t.Fatalf("the drive returned %v, want one wrapping %v", err, authn.ErrRoutesUnconfigured)
	}
}

// TestTheTenantScopesMountEndpointMustBeCoveredByBothTenantLayers is the
// opposite rule for the tenant-administration scope: its routes decide in the
// tenant's own policy domain, so the endpoint they are mounted on has to be
// one both tenant layers guard.
func TestTheTenantScopesMountEndpointMustBeCoveredByBothTenantLayers(t *testing.T) {
	resolved := `"tenant":{"resolve":{"endpoints":["api"]}}`
	for _, c := range []struct {
		name    string
		body    string
		modules []core.Module
		scope   authn.ScopeGuard
		want    string // text the failure must carry; empty means the start succeeds
	}{
		{
			name:    "covered by both layers",
			body:    `{"authn":{"gate":{"endpoints":["account","api"],"mount":{"endpoints":{"default":"account","tenants":"api"}}}},` + resolved + `}`,
			modules: []core.Module{resolutionLayer("")},
			scope:   fakeScope{"api": true},
		},
		{
			name:    "the admission layer does not cover it",
			body:    `{"authn":{"gate":{"endpoints":["account","api"],"mount":{"endpoints":{"default":"account","tenants":"api"}}}},` + resolved + `}`,
			modules: []core.Module{resolutionLayer("")},
			scope:   fakeScope{"other": true},
			want:    "tenant admission layer",
		},
		{
			name:    "no admission layer is in the command",
			body:    `{"authn":{"gate":{"endpoints":["account","api"],"mount":{"endpoints":{"default":"account","tenants":"api"}}}},` + resolved + `}`,
			modules: []core.Module{resolutionLayer("")},
			want:    "tenant admission layer",
		},
		{
			name:    "the resolution layer does not resolve on it",
			body:    `{"authn":{"gate":{"endpoints":["account","api"],"mount":{"endpoints":{"default":"account","tenants":"api"}}}},"tenant":{"resolve":{"endpoints":["other"]}}}`,
			modules: []core.Module{resolutionLayer("")},
			scope:   fakeScope{"api": true},
			want:    "tenant.resolve.endpoints",
		},
		{
			name:  "no resolution list is configured",
			body:  `{"authn":{"gate":{"endpoints":["account","api"],"mount":{"endpoints":{"default":"account","tenants":"api"}}}}}`,
			scope: fakeScope{"api": true},
			want:  "tenant.resolve.endpoints",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := assembly{
				body:     c.body,
				backends: []authn.Verifier{basicBackend("static", "static.")},
				router:   routerWith("account", "api"),
				modules:  c.modules,
			}
			if c.scope != nil {
				a.supplied = []supply{{module: "stand-in.admission", token: (*authn.ScopeGuard)(nil), instance: c.scope}}
			}
			err := a.drive(t, nothing)
			if c.want == "" {
				if err != nil {
					t.Fatalf("the drive failed: %v", err)
				}
				return
			}
			if !errors.Is(err, authn.ErrInvalidConfig) {
				t.Fatalf("the drive returned %v, want one wrapping %v", err, authn.ErrInvalidConfig)
			}
			for _, text := range []string{c.want, "authn.gate.mount.endpoints.tenants", `"api"`} {
				if !strings.Contains(err.Error(), text) {
					t.Errorf("the failure reads %q and does not carry %q", err, text)
				}
			}
		})
	}
}
