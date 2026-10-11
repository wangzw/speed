package admission

import (
	"context"
	"errors"
	"maps"
	nethttp "net/http"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/observability"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/tenant"
)

// TestEveryNotFoundIsTheSameBytes holds the property that makes the layer no
// oracle for tenants: the five ways to be turned away with a 404 are told apart
// in the log and the metric and nowhere a caller can read. The statuses, the
// headers and the bodies are equal byte for byte.
func TestEveryNotFoundIsTheSameBytes(t *testing.T) {
	impersonating := func(r *rig, scope string) context.Context {
		return r.as(acme, authntest.WithAccount(alice), authntest.WithActor(bob),
			authntest.WithSession("s1"), authntest.WithScope(scope))
	}
	type path struct {
		name  string
		setup func(r *rig)
		ctx   func(r *rig) context.Context
		want  outcome
	}
	paths := []path{
		{
			"an impersonation reaching a route with no tenant", func(*rig) {},
			func(r *rig) context.Context {
				return r.as("", authntest.WithAccount(alice), authntest.WithActor(bob),
					authntest.WithSession("s1"), authntest.WithScope(acme))
			}, scopeMismatch,
		},
		{
			"an impersonation confined to another tenant", func(*rig) {},
			func(r *rig) context.Context { return impersonating(r, globex) }, scopeMismatch,
		},
		{
			"a non-member, no decider", func(*rig) {},
			func(r *rig) context.Context { return r.as(acme, authntest.WithAccount(bob)) }, notMember,
		},
		{
			"a non-member the decider refused", func(r *rig) { r.withDecider(false) },
			func(r *rig) context.Context { return r.as(acme, authntest.WithAccount(bob)) }, notMember,
		},
		{
			"an impersonation of a non-member", func(r *rig) { r.withDecider(true) },
			func(r *rig) context.Context {
				return r.as(acme, authntest.WithAccount(bob), authntest.WithActor(alice),
					authntest.WithSession("s1"), authntest.WithScope(acme))
			}, notMember,
		},
		{"a non-member when the tenant requires both", func(r *rig) {
			r.withProviders()
			store := r.withSettings()
			store.set(settings.ForTenant(acme), requireMFAKey, true)
			store.set(settings.ForTenant(acme), requireSSOKey, true)
		}, func(r *rig) context.Context { return r.as(acme, authntest.WithAccount(bob)) }, notMember},
	}

	var first reply
	for i, p := range paths {
		r := newRig(t)
		p.setup(r)
		got := r.serve(p.ctx(r))
		if got.status != nethttp.StatusNotFound {
			t.Fatalf("%s answered %d, want 404", p.name, got.status)
		}
		r.onlyCounted(p.want)
		if i == 0 {
			first = got
			continue
		}
		if got.status != first.status || got.body != first.body || !maps.EqualFunc(got.header, first.header, equalValues) {
			t.Fatalf("%s answered %d %v %q, and %s answered %d %v %q: a 404 that differs tells a caller which one it got",
				p.name, got.status, got.header, got.body, paths[0].name, first.status, first.header, first.body)
		}
	}
	if len(first.header.Values("WWW-Authenticate")) != 0 {
		t.Fatalf("the 404 carries a challenge: %v", first.header)
	}
	if strings.Contains(first.body, "member") || strings.Contains(first.body, "tenant") || strings.Contains(first.body, "scope") {
		t.Fatalf("the 404 body says why: %q", first.body)
	}
}

func equalValues(a, b []string) bool { return strings.Join(a, "\x00") == strings.Join(b, "\x00") }

// TestEveryOutcomeIsCountedOnceAndOnlyOnce holds the metric's closed set: each
// of the eight values is reached by a request of its own, and every request is
// counted under exactly one.
func TestEveryOutcomeIsCountedOnceAndOnlyOnce(t *testing.T) {
	r := newRig(t)
	r.withProviders()
	decider := r.withDecider(false)
	store := r.withSettings()
	store.set(settings.ForTenant(globex), requireMFAKey, true)
	r.members.add(alice, globex)

	requests := []struct {
		want outcome
		ctx  context.Context
		pre  func()
	}{
		{admitted, r.as(acme), nil},
		{admittedByPolicy, r.as(acme, authntest.WithAccount(bob)), func() { decider.allow = true }},
		{notMember, r.as(acme, authntest.WithAccount("carol")), func() { decider.allow = false }},
		{scopeMismatch, r.as("", authntest.WithActor(bob), authntest.WithSession("s"), authntest.WithScope(acme)), nil},
		{stepUpMFA, r.as(globex), nil},
		{stepUpSSO, r.as(acme), func() { store.set(settings.ForTenant(acme), requireSSOKey, true) }},
		{noIdentity, r.bare(acme), nil},
		{unavailable, r.as(acme, authntest.WithAccount("dave")), func() { r.members.fail(errBoom) }},
	}
	seen := map[outcome]bool{}
	var want int64
	for _, q := range requests {
		if q.pre != nil {
			q.pre()
		}
		r.serve(q.ctx)
		want++
		seen[q.want] = true
		got := r.l.countsByName()
		if got[q.want.String()] != 1 {
			t.Fatalf("after the request for %s the counts are %v, want it at 1", q.want, got)
		}
	}
	for o := outcome(0); o < outcomeCount; o++ {
		if !seen[o] {
			t.Fatalf("no request in the table reaches %s", o)
		}
	}
	var total int64
	for _, n := range r.l.countsByName() {
		total += n
	}
	if total != want {
		t.Fatalf("%d requests were counted %d times in all: %v", want, total, r.l.countsByName())
	}
}

// TestTheOutcomeNamesAreTheDocumentedClosedSet holds the values themselves,
// which a dashboard is written against.
func TestTheOutcomeNamesAreTheDocumentedClosedSet(t *testing.T) {
	want := []string{
		"admitted", "admitted-by-policy", "not-member", "scope-mismatch",
		"step-up-mfa", "step-up-sso", "no-identity", "unavailable",
	}
	if int(outcomeCount) != len(want) {
		t.Fatalf("there are %d outcomes, want %d", outcomeCount, len(want))
	}
	for i, name := range want {
		if outcome(i).String() != name {
			t.Errorf("outcome %d is named %q, want %q", i, outcome(i), name)
		}
	}
}

// TestTheStatusSetIsClosed holds that 403 is not an answer of this layer and the
// statuses are the three the design lists.
func TestTheStatusSetIsClosed(t *testing.T) {
	allowed := map[int]bool{
		nethttp.StatusOK: true, nethttp.StatusNotFound: true,
		nethttp.StatusUnauthorized: true, nethttp.StatusServiceUnavailable: true,
	}
	for o := outcome(0); o < outcomeCount; o++ {
		got := o.status()
		if !allowed[got] || got == nethttp.StatusForbidden {
			t.Errorf("%s is answered %d, outside the closed set", o, got)
		}
		if o.passes() != (got == nethttp.StatusOK) {
			t.Errorf("%s passes=%v but is answered %d", o, o.passes(), got)
		}
	}
}

// TestTheDecisionCountReachesTheMetricCapability reads the counter through a real
// meter provider, where the assembly carries one: every outcome has a point from
// the first collection, the empty ones included, and each count is the number of
// requests that got it.
func TestTheDecisionCountReachesTheMetricCapability(t *testing.T) {
	collected := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(collected))
	t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("shutting the meter provider down: %v", err)
		}
	})

	var counts map[string]int64
	var meterName, unit string
	err := fixture{supplied: []supply{{"stand-in.meter", (*observability.MeterProvider)(nil), provider}}}.
		drive(t, core.StageInit, func(ctx context.Context, reg *core.Registry) error {
			anchor, err := core.Resolve[tenant.Admission](reg)
			if err != nil {
				return err
			}
			l := anchor.(*layer)
			l.cache = newDecisionCache(newRig(t).clock.read)
			l.members().add(alice, acme)
			serve := func(ctx context.Context) {
				serveThrough(t, l.middlewareFor("public").Handler, ctx)
			}
			base := log.WithLogger(context.Background(), newDiscardLogger())
			member := inTenant(authntest.Context(t, base, authntest.WithAccount(alice)), acme)
			stranger := inTenant(authntest.Context(t, base, authntest.WithAccount(bob)), acme)
			serve(member)
			serve(member)
			serve(stranger)

			var rm metricdata.ResourceMetrics
			if err := collected.Collect(ctx, &rm); err != nil {
				return err
			}
			counts, meterName, unit = decisionCounts(rm)
			return nil
		})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	if counts == nil {
		t.Fatalf("no %q instrument was collected: the counts are held in the process and reach no dashboard", decisionsInstrument)
	}
	if meterName != meterNameForTest || unit != "{decision}" {
		t.Errorf("the counter is on meter %q with unit %q, want %q and {decision}", meterName, unit, meterNameForTest)
	}
	want := map[string]int64{"admitted": 2, "not-member": 1}
	for o := outcome(0); o < outcomeCount; o++ {
		got, present := counts[o.String()]
		if !present {
			t.Errorf("no point for %s: a series that does not exist before its first request cannot be alerted on", o)
		}
		if got != want[o.String()] {
			t.Errorf("%s counts %d, want %d", o, got, want[o.String()])
		}
	}
}

const meterNameForTest = "tenant"

// TestTheStepUpChallengeCarriesTheDocumentedValues pins the two strings a client
// acts on by their literals: the acr_values that names what the request must
// satisfy and the error_description. A wrong acr tells the client to satisfy the
// wrong requirement, and a test that read either off the constant under test
// would let it change with no failure.
func TestTheStepUpChallengeCarriesTheDocumentedValues(t *testing.T) {
	r := newRig(t)
	r.withProviders()
	store := r.withSettings()
	store.set(settings.ForTenant(acme), requireMFAKey, true)
	store.set(settings.ForTenant(acme), requireSSOKey, true)

	mfa := r.serve(r.as(acme))
	if got := mfa.header.Get("WWW-Authenticate"); !strings.HasSuffix(got, `acr_values="2"`) {
		t.Errorf("the multi-factor challenge is %q, want it to end with acr_values=\"2\"", got)
	}
	if got := mfa.header.Get("WWW-Authenticate"); !strings.Contains(got, `error_description="A stronger or more recent authentication is required"`) {
		t.Errorf("the challenge is %q, want the documented error_description", got)
	}

	sso := r.serve(r.as(acme, authntest.WithAssurance(authn.AssuranceMulti)))
	if got := sso.header.Get("WWW-Authenticate"); !strings.HasSuffix(got, `acr_values="sso"`) {
		t.Errorf("the SSO challenge is %q, want it to end with acr_values=\"sso\"", got)
	}
}

// TestCloseWithdrawsTheCounterCallback holds what the Close callback does: a
// meter provider outliving the layer must stop reading a closed product, so the
// counter's callback is withdrawn and the series stops. A case that only
// recollected the series while the layer ran would let a Close that never
// unregisters through.
func TestCloseWithdrawsTheCounterCallback(t *testing.T) {
	collected := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(collected))
	t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("shutting the meter provider down: %v", err)
		}
	})

	var live bool
	err := fixture{supplied: []supply{{"stand-in.meter", (*observability.MeterProvider)(nil), provider}}}.
		drive(t, core.StageInit, func(ctx context.Context, _ *core.Registry) error {
			var rm metricdata.ResourceMetrics
			if err := collected.Collect(ctx, &rm); err != nil {
				return err
			}
			counts, _, _ := decisionCounts(rm)
			live = counts != nil
			return nil
		})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	if !live {
		t.Fatalf("no %q instrument was collected while the layer ran: the callback was never registered", decisionsInstrument)
	}
	var rm metricdata.ResourceMetrics
	if err := collected.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collecting after the close: %v", err)
	}
	if counts, _, _ := decisionCounts(rm); counts != nil {
		t.Fatalf("the counter is still collected after Close: %v: the callback was not withdrawn", counts)
	}
}

// TestCloseSurfacesAFailedWithdrawal holds the direction of a withdrawal that
// fails: Close reports it, so a host that closes the layer is not told the
// callback is gone when it is not.
func TestCloseSurfacesAFailedWithdrawal(t *testing.T) {
	base := sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewManualReader()))
	t.Cleanup(func() {
		if err := base.Shutdown(context.Background()); err != nil {
			t.Errorf("shutting the meter provider down: %v", err)
		}
	})
	provider := faultyMeterProvider{MeterProvider: base, meter: faultyMeter{Meter: base.Meter(meterName)}}
	err := fixture{supplied: []supply{{"stand-in.meter", (*observability.MeterProvider)(nil), provider}}}.
		drive(t, core.StageInit, nil)
	if !errors.Is(err, errBoom) {
		t.Fatalf("the drive ended with %v, want the failed withdrawal to surface", err)
	}
}

// faultyMeterProvider hands out a meter whose callback registration cannot be
// withdrawn.
type faultyMeterProvider struct {
	metric.MeterProvider
	meter metric.Meter
}

func (p faultyMeterProvider) Meter(string, ...metric.MeterOption) metric.Meter { return p.meter }

// faultyMeter registers a callback and hands back a registration whose
// Unregister always fails.
type faultyMeter struct{ metric.Meter }

func (m faultyMeter) RegisterCallback(f metric.Callback, instruments ...metric.Observable) (metric.Registration, error) {
	real, err := m.Meter.RegisterCallback(f, instruments...)
	if err != nil {
		return nil, err
	}
	return faultyRegistration{Registration: real}, nil
}

// faultyRegistration forwards the sealed interface to a real registration and
// fails only the withdrawal.
type faultyRegistration struct{ metric.Registration }

func (faultyRegistration) Unregister() error { return errBoom }

func decisionCounts(rm metricdata.ResourceMetrics) (map[string]int64, string, string) {
	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != decisionsInstrument {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				return nil, "", ""
			}
			out := make(map[string]int64, len(sum.DataPoints))
			for _, point := range sum.DataPoints {
				v, _ := point.Attributes.Value(attribute.Key(outcomeAttribute))
				out[v.AsString()] = point.Value
			}
			return out, scope.Scope.Name, m.Unit
		}
	}
	return nil, "", ""
}

// TestARefusalLeavesOneLineThroughTheRequestsLogger holds what the log carries:
// one Info record for each refusal, through the logger the request's context
// carries, so it has the fields the layers outside bound; the layer adds the
// class of cause and nothing that identifies a credential.
func TestARefusalLeavesOneLineThroughTheRequestsLogger(t *testing.T) {
	for _, c := range []struct {
		name string
		want outcome
		ctx  func(r *rig) context.Context
	}{
		{"not a member", notMember, func(r *rig) context.Context { return r.as(acme, authntest.WithAccount(bob)) }},
		{"another tenant's scope", scopeMismatch, func(r *rig) context.Context {
			return r.as(acme, authntest.WithActor(bob), authntest.WithSession("s"), authntest.WithScope(globex))
		}},
		{"multi-factor", stepUpMFA, func(r *rig) context.Context {
			r.withSettings().set(settings.ForTenant(acme), requireMFAKey, true)
			return r.as(acme)
		}},
		{"SSO", stepUpSSO, func(r *rig) context.Context {
			r.withSettings().set(settings.ForTenant(acme), requireSSOKey, true)
			return r.as(acme)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			ctx := c.ctx(r)
			// The gateway binds the account, and the resolution layer the
			// tenant; the record is read with both.
			ctx = log.WithAttrs(ctx, "account", "bound-by-the-gateway")
			r.serve(ctx)
			infos := r.logs.atLevel(levelInfo)
			if len(infos) != 1 {
				t.Fatalf("the refusal wrote %+v, want one Info line", r.logs.all())
			}
			rec := infos[0]
			if rec.attrs["outcome"] != c.want.String() {
				t.Errorf("the line carries outcome %q, want %q", rec.attrs["outcome"], c.want)
			}
			if rec.attrs["tenant_id"] != acme || rec.attrs["account"] != "bound-by-the-gateway" {
				t.Errorf("the line was not written through the request's logger: %v", rec.attrs)
			}
			if !strings.HasPrefix(rec.msg, diagnosticPrefix) {
				t.Errorf("the line %q does not start with %q", rec.msg, diagnosticPrefix)
			}
			if len(r.logs.atLevel(levelError)) != 0 {
				t.Errorf("a refusal was written at Error: %+v", r.logs.atLevel(levelError))
			}
		})
	}
}

// TestNoSecretReachesAnswerOrLog holds that nothing of the credential, the
// settings or the cause is in what a caller reads: the cause of a 503 goes to
// the log, and the Authorization header is not read at all.
func TestNoSecretReachesAnswerOrLog(t *testing.T) {
	r := newRig(t)
	r.members.fail(errBoom)
	ctx := r.as(acme)
	req := serveThrough(t, r.l.middlewareFor("public").Handler, ctx)
	if strings.Contains(req.body, errBoom.Error()) {
		t.Fatalf("the cause reached the caller: %q", req.body)
	}
	for k, v := range req.header {
		if strings.Contains(strings.Join(v, ""), errBoom.Error()) {
			t.Fatalf("the cause reached the header %s", k)
		}
	}
}

// compile-time check that the interfaces the rig stubs are the ones the layer
// takes.
var (
	_ authn.Memberships  = (*memberStub)(nil)
	_ authn.Decider      = (*deciderStub)(nil)
	_ authn.SSOProviders = (*providersStub)(nil)
	_ settings.Store     = (*settingsStub)(nil)
)

// TestCoversIsAnExactMembershipTest holds the guard: the configured names and
// nothing else, matched whole. The star is a name no list may hold, and it is
// not a pattern either.
func TestCoversIsAnExactMembershipTest(t *testing.T) {
	l := newLayer(resolved{endpoints: []string{"public", "admin-2"}}, newMemberStub())
	for name, want := range map[string]bool{
		"public": true, "admin-2": true,
		"admin": false, "Public": false, "public ": false, "": false, "*": false, "pub*": false, "admin-": false,
	} {
		if got := l.Covers(name); got != want {
			t.Errorf("Covers(%q) = %v, want %v", name, got, want)
		}
	}
}

// TestTheChallengeNeverCarriesWhatEndsAHeaderField holds the two strings put in
// the challenge from outside: the scheme the gateway stamped, and the endpoint
// name used as the realm.
func TestTheChallengeNeverCarriesWhatEndsAHeaderField(t *testing.T) {
	for scheme, want := range map[string]string{
		"":              "Bearer",
		"Cookie":        "Cookie",
		"Basic":         "Basic",
		"Bear\r\ner":    "Bearer",
		"Bearer realm=": "Bearer",
		"Béarer":        "Bearer",
		`Bea"rer`:       "Bearer",
		// The interior of the token set: a digit the range may cut, the
		// special characters beside the letters, and a tag ending in the
		// last letter of either alphabet.
		"v0":      "v0",
		"Token-z": "Token-z",
		"ABZ":     "ABZ",
		"buzz":    "buzz",
	} {
		if got := challengeScheme(scheme); got != want {
			t.Errorf("challengeScheme(%q) = %q, want %q", scheme, got, want)
		}
	}
	if got := quoteRealm("pub\"lic\\\r\nx"); got != `pub\"lic\\x` {
		t.Errorf("quoteRealm escaped to %q", got)
	}
	l := newLayer(resolved{endpoints: []string{`pub"lic`}}, newMemberStub())
	rp := serveThrough(t, l.middlewareFor(`pub"lic`).Handler, inTenant(context.Background(), acme))
	if want := `Bearer realm="pub\"lic", Cookie realm="pub\"lic"`; rp.header.Get("WWW-Authenticate") != want {
		t.Errorf("the no-identity challenge is %q, want %q", rp.header.Get("WWW-Authenticate"), want)
	}
}
