package gate

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/observability"
)

func TestConfiguredEndpointsAreGuardedWhateverOrderTheyAreWritten(t *testing.T) {
	g, err := newGateway(settings{endpoints: []string{"web", "api", "ops"}, budget: time.Second, inFlight: 1})
	if err != nil {
		t.Fatal(err)
	}
	names, err := g.expandGuarded(routerWith("api", "ops", "web"))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"api", "ops", "web"}; !slices.Equal(names, want) {
		t.Fatalf("expanded to %v, want %v", names, want)
	}
	g.guarded = names
	for _, name := range names {
		if !g.guards(name) {
			t.Errorf("the endpoint %q is configured and is not guarded", name)
		}
	}
}

// TestGuardedListIsOrderIndependent holds that configured order of endpoints does not matter.
func TestGuardedListIsOrderIndependent(t *testing.T) {
	for _, body := range []string{
		`{"authn":{"gate":{"endpoints":["public","admin"]}}}`,
		`{"authn":{"gate":{"endpoints":["*"]}}}`,
	} {
		t.Run(body, func(t *testing.T) {
			gateRef := make(chan authn.Gate, 1)
			registrant := core.Module{
				Name:     "stand-in.registrant",
				Requires: []core.Requirement{{Token: (*authn.Gate)(nil)}},
				New:      func(context.Context, *core.Registry) (any, error) { return struct{}{}, nil },
				Init: func(_ context.Context, reg *core.Registry, _ any) error {
					gt, err := core.Resolve[authn.Gate](reg)
					if err != nil {
						return err
					}
					gt.Exempt("admin", "GET", "/health")
					gateRef <- gt
					return nil
				},
			}
			err := assembly{
				body:     body,
				backends: []authn.Verifier{basicBackend("static", "static.")},
				router:   descendingRouter{routerWith("admin", "public")},
				modules:  []core.Module{registrant},
			}.drive(t, nothing)
			if err != nil {
				t.Fatalf("the drive failed: %v", err)
			}
		})
	}
}

type descendingRouter struct{ *fakeRouter }

func (r descendingRouter) Endpoints() []string {
	out := r.fakeRouter.Endpoints()
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func TestAdmissionConflictDoesNotDependOnTheOrderBackendsWereListed(t *testing.T) {
	alpha, beta := schemeBackend("alpha", "x"), schemeBackend("beta", "x")
	gamma, delta := cookieBackend("gamma", "c"), cookieBackend("delta", "c")
	var first string
	for i, order := range [][]authn.Verifier{
		{alpha, beta, gamma, delta}, {gamma, delta, alpha, beta}, {delta, gamma, beta, alpha}, {beta, alpha, delta, gamma},
	} {
		_, err := buildDispatch(order)
		if err == nil {
			t.Fatal("a set with conflicts was admitted")
		}
		if i == 0 {
			first = err.Error()
		} else if err.Error() != first {
			t.Errorf("order %d fails with %q, order 0 with %q", i, err.Error(), first)
		}
	}
}

func TestConfigurationBoundaries(t *testing.T) {
	base := func() moduleConfig {
		c := configDefaults
		c.Endpoints = []string{"public"}
		return c
	}
	for name, edit := range map[string]func(*moduleConfig){
		"a call budget of one nanosecond": func(c *moduleConfig) { c.CallBudget = 1 },
		"an in-flight bound of one":       func(c *moduleConfig) { c.InFlight = 1 },
		"a mixed-case localhost":          func(c *moduleConfig) { c.Mount.PublicURL = "http://LocalHost:8080" },
	} {
		if _, err := func() (settings, error) { c := base(); edit(&c); return c.resolve() }(); err != nil {
			t.Errorf("%s is refused: %v", name, err)
		}
	}
	for name, edit := range map[string]func(*moduleConfig){
		"a base path with a fragment mark":    func(c *moduleConfig) { c.Mount.Bases.Default = "/auth#x" },
		"a public URL with a bare query mark": func(c *moduleConfig) { c.Mount.PublicURL = "https://a.example?" },
	} {
		if _, err := func() (settings, error) { c := base(); edit(&c); return c.resolve() }(); !errors.Is(err, authn.ErrInvalidConfig) {
			t.Errorf("%s is accepted (err %v)", name, err)
		}
	}
}

// TestConfigBoundaries holds the edges of the configuration values: a fragment mark in the base path, a bare question mark in the public URL, a loopback host in capitals, and the unspecified addresses, which are not loopback.
func TestConfigBoundaries(t *testing.T) {
	base := func(m mountConfig) moduleConfig {
		c := configDefaults
		c.Mount = m
		c.Mount.Bases = configDefaults.Mount.Bases
		c.Mount.Bases.Default = "/api/v1/auth"
		return c
	}
	withBase := func(p string) moduleConfig { c := configDefaults; c.Mount.Bases.Default = p; return c }
	for _, c := range []struct {
		name string
		cfg  moduleConfig
		err  error
	}{
		{"base path with a fragment mark", withBase("/auth#x"), authn.ErrInvalidConfig},
		{"public url with a bare question mark", base(mountConfig{PublicURL: "https://example.com?"}), authn.ErrInvalidConfig},
		{"loopback in capitals", base(mountConfig{PublicURL: "http://LocalHost:8080"}), nil},
		{"unspecified ipv4", base(mountConfig{PublicURL: "http://0.0.0.0:8080"}), authn.ErrInsecureConfig},
		{"unspecified ipv6", base(mountConfig{PublicURL: "http://[::]:8080"}), authn.ErrInsecureConfig},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := c.cfg.resolve()
			if c.err == nil && err != nil || c.err != nil && !errors.Is(err, c.err) {
				t.Errorf("resolve gave %v, want %v", err, c.err)
			}
		})
	}
}

func TestAmbiguousNeighboursStopTheStartup(t *testing.T) {
	providerA, providerB := sdkmetric.NewMeterProvider(), sdkmetric.NewMeterProvider()
	for name, pair := range map[string][2]supply{
		"scope guard": {
			{module: "stand-in.admission-a", token: (*authn.ScopeGuard)(nil), instance: fakeScope{}},
			{module: "stand-in.admission-b", token: (*authn.ScopeGuard)(nil), instance: fakeScope{}},
		},
		"route mounter": {
			{module: "stand-in.mount-a", token: (*mount.Mounter)(nil), instance: fakeMounter{}},
			{module: "stand-in.mount-b", token: (*mount.Mounter)(nil), instance: fakeMounter{}},
		},
		"meter provider": {
			{module: "stand-in.meter-a", token: (*observability.MeterProvider)(nil), instance: providerA},
			{module: "stand-in.meter-b", token: (*observability.MeterProvider)(nil), instance: providerB},
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := assembly{
				body:     guarding("public"),
				backends: []authn.Verifier{basicBackend("static", "static.")},
				router:   routerWith("public"),
				supplied: pair[:],
			}.drive(t, nothing)
			if !errors.Is(err, core.ErrAmbiguousProvider) {
				t.Errorf("the drive returned %v, want the ambiguity carried out", err)
			}
		})
	}
}

type failingLookupRouter struct{ err error }

func (r failingLookupRouter) Endpoint(string) (speedhttp.Endpoint, error) { return nil, r.err }
func (r failingLookupRouter) Endpoints() []string                         { return []string{"public"} }

func TestALookupFailureIsNotReadAsAnUnknownEndpoint(t *testing.T) {
	boom := errors.New("router exploded")

	g := testGateway(t, []string{"public"}, basicBackend("static", "static."))
	g.Exempt("public", "GET", "/health")
	err := g.seal(failingLookupRouter{err: boom})
	if !errors.Is(err, boom) || errors.Is(err, authn.ErrUnknownEndpoint) {
		t.Errorf("seal returned %v, want the router's own failure and not ErrUnknownEndpoint", err)
	}

	g = testGateway(t, []string{"public"}, basicBackend("static", "static."))
	g.router = failingLookupRouter{err: boom}
	g.settings.mount.Endpoint = "public"
	err = g.checkMount(nil)
	if !errors.Is(err, boom) || errors.Is(err, authn.ErrUnknownEndpoint) {
		t.Errorf("checkMount returned %v, want the router's own failure and not ErrUnknownEndpoint", err)
	}
}

func TestPrepareAndNewReportAMissingConfigurationReader(t *testing.T) {
	m := Module()
	_, err := m.Prepare(t.Context(), core.New())
	if !errors.Is(err, core.ErrMissingProvider) || !strings.Contains(err.Error(), "reader") {
		t.Errorf("Prepare returned %v, want the missing reader named", err)
	}
	_, err = m.New(t.Context(), core.New())
	if !errors.Is(err, core.ErrMissingProvider) || !strings.Contains(err.Error(), "reader") {
		t.Errorf("New returned %v, want the missing reader named", err)
	}
}

func TestAConfigurationThatDoesNotDecodeStopsTheStartup(t *testing.T) {
	for key, body := range map[string]string{
		"authn.gate.call-budget":          `{"authn":{"gate":{"call-budget":"soon"}}}`,
		"authn.gate.in-flight":            `{"authn":{"gate":{"in-flight":"many"}}}`,
		"authn.gate.endpoints":            `{"authn":{"gate":{"endpoints":5}}}`,
		"authn.gate.mount.allow-insecure": `{"authn":{"gate":{"mount":{"allow-insecure":"maybe"}}}}`,
	} {
		t.Run(key, func(t *testing.T) {
			err := assembly{
				body:     body,
				backends: []authn.Verifier{basicBackend("static", "static.")},
				router:   routerWith("public"),
			}.drive(t, nothing)
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Errorf("the drive returned %v, want a failure naming %s: a value that does not decode "+
					"must not leave the gateway running on zero settings, guarding no endpoint", err, key)
			}
		})
	}
}

func TestNewReportsAConfigurationItCannotRunOn(t *testing.T) {
	m := Module()
	reader := authntest.ConfigReaderAll(t, `{"authn":{"gate":{"call-budget":"0s"}}}`, m)
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	instance, err := m.New(t.Context(), reg)
	if !errors.Is(err, authn.ErrInvalidConfig) || instance != nil {
		t.Errorf("New returned (%v, %v), want no instance and ErrInvalidConfig", instance, err)
	}
}

// TestAnAmbiguousLoggingOrMetricOrRouterFailsTheStartup holds that more optional capabilities whose resolution can fail for a reason other than absence.
func TestAnAmbiguousLoggingOrMetricOrRouterFailsTheStartup(t *testing.T) {
	type pair = []supply
	loggerA := supply{module: "stand-in.logging-a", token: (*log.Logger)(nil), instance: recordingLogging{logger: quiet()}}
	loggerB := supply{module: "stand-in.logging-b", token: (*log.Logger)(nil), instance: recordingLogging{logger: quiet()}}
	meterA := supply{module: "stand-in.meter-a", token: (*observability.MeterProvider)(nil), instance: noop.NewMeterProvider()}
	meterB := supply{module: "stand-in.meter-b", token: (*observability.MeterProvider)(nil), instance: noop.NewMeterProvider()}
	routerB := supply{module: "stand-in.router-b", token: (*speedhttp.Router)(nil), instance: routerWith("public")}
	for _, c := range []struct {
		name     string
		supplied pair
		notErr   error
	}{
		{"two logging modules", pair{loggerA, loggerB}, nil},
		{"two metric providers", pair{meterA, meterB}, nil},
		{"two routers", pair{routerB}, authn.ErrNoRouter},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := assembly{
				body:     guarding("public"),
				backends: []authn.Verifier{basicBackend("static", "static.")},
				router:   routerWith("public"),
				supplied: c.supplied,
			}.drive(t, nothing)
			if !errors.Is(err, core.ErrAmbiguousProvider) {
				t.Fatalf("the drive returned %v, want one wrapping %v", err, core.ErrAmbiguousProvider)
			}
			if c.notErr != nil && errors.Is(err, c.notErr) {
				t.Errorf("the failure is reported as %v, which names a cause that is not the one", c.notErr)
			}
		})
	}
}

type refusingMeter struct {
	noop.Meter
	counter, callback bool
}

func (m refusingMeter) Int64ObservableCounter(name string, opts ...metric.Int64ObservableCounterOption) (metric.Int64ObservableCounter, error) {
	if m.counter {
		return nil, errors.New("counter refused")
	}
	return m.Meter.Int64ObservableCounter(name, opts...)
}

func (m refusingMeter) RegisterCallback(f metric.Callback, instruments ...metric.Observable) (metric.Registration, error) {
	if m.callback {
		return nil, errors.New("callback refused")
	}
	return m.Meter.RegisterCallback(f, instruments...)
}

type refusingProvider struct {
	noop.MeterProvider
	meter refusingMeter
}

func (p refusingProvider) Meter(string, ...metric.MeterOption) metric.Meter { return p.meter }

func TestAMeterThatRefusesTheCounterStopsTheStartup(t *testing.T) {
	for name, meter := range map[string]refusingMeter{
		"the counter":  {counter: true},
		"the callback": {callback: true},
	} {
		t.Run(name, func(t *testing.T) {
			err := assembly{
				body:     guarding("public"),
				backends: []authn.Verifier{basicBackend("static", "static.")},
				router:   routerWith("public"),
				supplied: []supply{{
					module: "stand-in.meter", token: (*observability.MeterProvider)(nil),
					instance: refusingProvider{meter: meter},
				}},
			}.drive(t, nothing)
			if err == nil || !strings.Contains(err.Error(), "refused") {
				t.Errorf("the drive returned %v, want the meter's refusal carried out", err)
			}
		})
	}
}

// TestNoConfigReaderFailsTheStartup holds that a command with no configuration reader fails the startup.
func TestNoConfigReaderFailsTheStartup(t *testing.T) {
	reg := core.New()
	reg.Register(Module())
	reg.Register(backendModule(basicBackend("static", "static.")))
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageStart,
		Participants: []string{ModuleName, "authn.static"},
		Action:       nothing,
	})
	if err == nil {
		t.Fatal("the drive succeeded with no configuration reader")
	}
}
