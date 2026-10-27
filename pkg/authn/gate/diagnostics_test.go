package gate

import (
	"context"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/observability"
)

func TestDescribeNamesWhatABackendTakes(t *testing.T) {
	full, err := admitBackend(&fakeVerifier{name: "hybrid", accepts: authn.Accepts{
		Schemes: []string{"bearer"}, Cookies: []string{"sid"}, Accounts: "acme.", Impersonation: true, SSO: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := describe(full), `hybrid (schemes bearer; cookies sid; accounts "acme."; impersonation; SSO)`; got != want {
		t.Errorf("describe reads %q, want %q", got, want)
	}
	plain, err := admitBackend(schemeBackend("token", "bearer"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := describe(plain), `token (schemes bearer; the library account namespace)`; got != want {
		t.Errorf("describe reads %q, want %q", got, want)
	}
	cookie, err := admitBackend(cookieBackend("session", "sid"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := describe(cookie), `session (cookies sid; the library account namespace)`; got != want {
		t.Errorf("describe reads %q, want %q", got, want)
	}
}

func TestEveryUnprefixedCookieNameIsWarnedAbout(t *testing.T) {
	h := &recorder{}
	g := testGateway(t, []string{"public"}, cookieBackend("session", "__Host-a", "plain"))
	g.logger = slog.New(h)
	g.warnUnprefixedCookies()
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.records) != 1 || attrText(h.records[0], "cookie_name") != "plain" {
		t.Errorf("the warnings are %d record(s), want one, about the cookie plain, though a prefixed name sorts before it", len(h.records))
	}
}

func TestAnAccessConflictNamesTheKindItIsWith(t *testing.T) {
	for other, register := range map[string]func(*gateway){
		"Optional":     func(g *gateway) { g.Optional("public", "GET", "/x") },
		"Impersonable": func(g *gateway) { g.Impersonable("public", "GET", "/x") },
	} {
		t.Run(other, func(t *testing.T) {
			g := testGateway(t, []string{"public"}, basicBackend("static", "static."))
			g.Exempt("public", "GET", "/x")
			register(g)
			err := g.seal(routerWith("public"))
			if err == nil || !strings.Contains(err.Error(), "both Exempt and "+other) {
				t.Errorf("seal returned %v, want a conflict between Exempt and %s", err, other)
			}
		})
	}
}

func TestGuardedAndReachableAreDescribedWhetherOrNotThereIsAny(t *testing.T) {
	if got := describeGuarded(nil); !strings.Contains(got, "no endpoint") {
		t.Errorf("an empty guarded set reads %q", got)
	}
	if got := describeGuarded([]string{"a", "b"}); got != "a, b" {
		t.Errorf("two endpoints read %q", got)
	}
	if got := describeReach(nil); !strings.HasPrefix(got, "none") {
		t.Errorf("no reachable endpoint reads %q", got)
	}
	if got := describeReach([]string{"a", "b"}); got != "a, b" {
		t.Errorf("two reachable endpoints read %q", got)
	}
}

func TestRegistrationAttrsListOnlyEndpointsThatCarryTheKind(t *testing.T) {
	routes := map[string]map[route]access{
		"a": {{method: "GET", path: "/x"}: {exempt: true}},
		"b": {{method: "GET", path: "/y"}: {optional: true}},
	}
	attrs := registrationAttrs(routes, func(a access) bool { return a.exempt }, "exemptions", "none")
	if len(attrs) != 1 {
		t.Fatalf("the exempt listing carries %d attribute(s), want one: %v", len(attrs), attrs)
	}
	if got := attrs[0].(slog.Attr); got.Key != "a" || got.Value.String() != "GET /x" {
		t.Errorf("the exempt listing reads %v, want a = GET /x", got)
	}
	none := registrationAttrs(routes, func(a access) bool { return a.impersonable }, "impersonable_routes", "nothing")
	if len(none) != 1 || none[0].(slog.Attr).Key != "impersonable_routes" || none[0].(slog.Attr).Value.String() != "nothing" {
		t.Errorf("an empty listing reads %v, want the none text under the heading", none)
	}
}

func TestMountEndpointJudgementSaysWhichSettingItJudged(t *testing.T) {
	for name, c := range map[string]struct {
		configured string
		want       string
	}{
		"empty": {"", "is empty, which takes the entry point's only endpoint"},
		"named": {"public", `is "public"`},
	} {
		t.Run(name, func(t *testing.T) {
			g := testGateway(t, []string{"public"}, basicBackend("static", "static."))
			g.router = routerWith("public")
			g.mounted = true
			g.scope = fakeScope{"public": true}
			g.settings.mount.Endpoints.Default = c.configured
			err := g.checkMount(nil)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("checkMount returned %v, want it to say %q", err, c.want)
			}
		})
	}
}

func TestReachableListsTheEndpointsTheScopeGuardCovers(t *testing.T) {
	g := testGateway(t, []string{"admin", "public"}, basicBackend("static", "static."))
	if got := g.reachable(); len(got) != 0 {
		t.Errorf("with no scope guard %v is reachable, want none", got)
	}
	g.scope = fakeScope{"public": true}
	if got := g.reachable(); !slices.Equal(got, []string{"public"}) {
		t.Errorf("reachable is %v, want [public]", got)
	}
}

func TestTheConclusionCounterIsDescribedAndUnitised(t *testing.T) {
	collected := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(collected))
	t.Cleanup(func() { _ = provider.Shutdown(t.Context()) })
	var rm metricdata.ResourceMetrics
	err := assembly{
		body:     guarding("public"),
		backends: []authn.Verifier{basicBackend("static", "static.")},
		router:   routerWith("public"),
		supplied: []supply{{module: "stand-in.meter", token: (*observability.MeterProvider)(nil), instance: provider}},
	}.drive(t, func(ctx context.Context, _ *core.Registry) error { return collected.Collect(ctx, &rm) })
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name == "authn.gate.conclusions" {
				if m.Unit != "{conclusion}" || !strings.Contains(m.Description, "authentication conclusions") {
					t.Errorf("the counter is unit %q, described %q", m.Unit, m.Description)
				}
				return
			}
		}
	}
	t.Fatal("the counter was not collected")
}

// TestTheLayerStageIsTheValueTheDesignPromises holds the stage the design promises as the anchor other layers place themselves by.
func TestTheLayerStageIsTheValueTheDesignPromises(t *testing.T) {
	g := testGateway(t, []string{"public"}, basicBackend("static", "static."))
	if got := g.middlewareFor("public").Stage; got != speedhttp.StageIdentify {
		t.Errorf("Stage is %v, want the identifying stage", got)
	}
}

// TestStartupDiagnosticsNameWhatIsOpenedAndWhatImpersonationReaches holds the startup diagnostics: the endpoints an impersonating identity can reach are named, and the frozen tables are listed per kind.
func TestStartupDiagnosticsNameWhatIsOpenedAndWhatImpersonationReaches(t *testing.T) {
	const reachMsg = "authn: the endpoints an impersonating identity can reach without a route registered for it"
	const exemptMsg = "authn: the exempt routes, frozen"
	for _, c := range []struct {
		name  string
		scope authn.ScopeGuard
		want  string
	}{
		{"with an admission layer on admin", fakeScope{"admin": true}, "admin"},
		{"with no admission layer", nil, "none"},
	} {
		t.Run(c.name, func(t *testing.T) {
			logs := liveAssembly{
				endpoints: []string{"admin", "public"},
				scope:     c.scope,
				gateItems: `"mount":{"endpoints":{"default":"public"}}`,
				backends:  []authn.Verifier{basicBackend("static", "static.")},
				optional:  []liveRoute{{endpoint: "public", method: "GET", path: "/catalog"}},
			}.serve(t, func(*live) {})
			rec, found := logs.recordNamed(reachMsg)
			if !found {
				t.Fatalf("no startup line says which endpoints an impersonating identity can reach")
			}
			if got := attrText(rec, "endpoints"); !strings.HasPrefix(got, c.want) || (c.want == "admin" && got != "admin") {
				t.Errorf("the line reads %q, want it to start with %q", got, c.want)
			}
			ex, found := logs.recordNamed(exemptMsg)
			if !found {
				t.Fatalf("no startup line lists the exemptions")
			}
			if got := attrText(ex, "exemptions"); !strings.HasPrefix(got, "none") {
				t.Errorf("with only an optional route registered the exemption line reads %q, want none", got)
			}
		})
	}
}

// fieldName is how the design writes a log field: lower case, words joined by
// underscores, no hyphen and no space.
var fieldName = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)

// TestEveryFieldTheStartupDiagnosticsWriteIsNamedInTheDesignsStyle holds the
// field names of the lines the gateway writes at startup to the one style the
// design fixes for log fields. The cases that read a refusal's fields hold
// those names; this one reads every attribute of every startup line, with
// nothing registered, which is the state in which the frozen tables are written
// under their headings and not under the names of endpoints.
func TestEveryFieldTheStartupDiagnosticsWriteIsNamedInTheDesignsStyle(t *testing.T) {
	logs := liveAssembly{
		backends: []authn.Verifier{basicBackend("static", "static.")},
	}.serve(t, func(*live) {})

	var seen int
	for _, rec := range logs.recordsOf() {
		if !strings.HasPrefix(rec.Message, diagnosticPrefix) {
			continue
		}
		rec.Attrs(func(a slog.Attr) bool {
			seen++
			if !fieldName.MatchString(a.Key) {
				t.Errorf("the startup line %q writes the field %q, want lower case words joined by underscores",
					rec.Message, a.Key)
			}
			return true
		})
	}
	if seen == 0 {
		t.Fatal("no startup line carried an attribute, so no field name was held to the style")
	}
}
