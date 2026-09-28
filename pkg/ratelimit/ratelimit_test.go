package ratelimit

import (
	"bytes"
	"context"
	"errors"
	nethttp "net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/tenant"
)

// pinnedAt is the instant the pinned clock stands at: the start of a window,
// where the previous window weighs one. The weight a decision applies depends
// on where in the window the request lands, so a test that lets the clock run
// would pass or fail with the time of day.
var pinnedAt = time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)

func pinned(endpoints ...string) *limiter {
	l := newLimiter(endpoints)
	l.now = func() time.Time { return pinnedAt }
	return l
}

// --- the key ---------------------------------------------------------------

// A tenant identifier may hold a colon, and an identity string is two halves
// joined by a separator with no rule keeping a colon out of either. Without
// escaping, these two would build one key and share one count across two
// tenants.
func TestEscapeKeepsTwoDistinctPairsApart(t *testing.T) {
	first := countKey("a:b", "c", 7)
	second := countKey("a", "b:c", 7)
	if first == second {
		t.Fatalf("two distinct pairs built the same key %q", first)
	}
}

func TestEscapeLeavesOrdinarySegmentsAlone(t *testing.T) {
	if got := countKey("acme", "local/bob", 7); got != "ratelimit:acme:local/bob:7" {
		t.Fatalf("key = %q", got)
	}
}

func TestEscapeEscapesTheEscapeItself(t *testing.T) {
	// A segment holding a literal "%3A" must not read back as a colon.
	if got := escape("%3A"); got == escape(":") {
		t.Fatalf("a literal escape and a colon both became %q", got)
	}
}

// --- the retry hint --------------------------------------------------------

func TestRetryAfterRoundsUpAndNeverReachesZero(t *testing.T) {
	cases := []struct {
		remaining time.Duration
		want      int
	}{
		{time.Second, 1},
		{time.Second + time.Nanosecond, 2},
		{1500 * time.Millisecond, 2},
		{time.Nanosecond, 1},
	}
	for _, c := range cases {
		if got := retryAfterSeconds(int64(c.remaining)); got != c.want {
			t.Errorf("retryAfterSeconds(%v) = %d, want %d", c.remaining, got, c.want)
		}
	}
}

// --- counting --------------------------------------------------------------

func TestCountLetsThroughUpToTheLimit(t *testing.T) {
	kv := newFakeKV()
	l := newLimiter(nil)
	l.counters = kv

	for i := 1; i <= 5; i++ {
		var d decision
		allowed, _ := l.count(context.Background(), "local/bob", "acme", time.Minute, 5, &d)
		if d.tripped != "" {
			t.Fatalf("request %d tripped %q", i, d.tripped)
		}
		if !allowed {
			t.Fatalf("request %d was refused, want allowed", i)
		}
	}

	var d decision
	allowed, retry := l.count(context.Background(), "local/bob", "acme", time.Minute, 5, &d)
	if d.tripped != "" {
		t.Fatalf("tripped %q", d.tripped)
	}
	if allowed {
		t.Fatal("the request past the limit was allowed")
	}
	if retry < 1 {
		t.Fatalf("retryAfter = %d, want at least one", retry)
	}
}

// The increment happens before the decision, so a refused request still
// consumes. Counting only what was let through would let a caller that is
// already over its limit retry without spending anything.
func TestCountSpendsOnRefusedRequestsToo(t *testing.T) {
	kv := newFakeKV()
	l := pinned()
	l.counters = kv

	for i := 0; i < 3; i++ {
		l.count(context.Background(), "local/bob", "acme", time.Minute, 2, &decision{})
	}
	key := countKey("acme", "local/bob", pinnedAt.UnixNano()/int64(time.Minute))
	if got := kv.value(key); got != 3 {
		t.Fatalf("count = %v, want 3: refused requests did not consume", got)
	}
}

// A caller that keeps sending past its limit must not open the window up
// again: the third request over a limit of one is still refused.
func TestCountStaysRefusedAfterTheFirstRefusal(t *testing.T) {
	kv := newFakeKV()
	l := pinned()
	l.counters = kv

	l.count(context.Background(), "local/bob", "acme", time.Minute, 1, &decision{})
	for i := 0; i < 4; i++ {
		allowed, _ := l.count(context.Background(), "local/bob", "acme", time.Minute, 1, &decision{})
		if allowed {
			t.Fatalf("request %d past the limit was allowed", i+2)
		}
	}
}

// The expiry must cover two windows. With one window's expiry the key written
// in the previous window lapses the moment this one opens, for any identity
// string that made its first request at the start of a window, and the
// weighted term collapses to nothing — the layer then decides on the current
// window alone, which is the single fixed window the design rejected.
func TestCountAsksForTwoWindowsOfExpiry(t *testing.T) {
	kv := newFakeKV()
	l := pinned()
	l.counters = kv

	const window = time.Minute
	l.count(context.Background(), "local/bob", "acme", window, 10, &decision{})

	index := pinnedAt.UnixNano() / int64(window)
	ttl := kv.ttlOf(countKey("acme", "local/bob", index))
	if ttl < 2*window {
		t.Fatalf("ttl = %v, want at least %v: the previous window's key would lapse", ttl, 2*window)
	}
}

// The previous window weighs into the estimate, so a caller that spent the
// whole of it is refused early in the next one.
func TestCountCarriesThePreviousWindowOver(t *testing.T) {
	kv := newFakeKV()
	l := pinned()
	l.counters = kv

	const window = time.Minute
	width := int64(window)
	previous := countKey("acme", "local/bob", pinnedAt.UnixNano()/width-1)
	kv.seed(previous, 10)

	// At a window's start the previous window weighs one, so a limit of five
	// is already spent.
	var d decision
	allowed, _ := l.count(context.Background(), "local/bob", "acme", window, 5, &d)
	if d.tripped != "" {
		t.Fatalf("tripped %q", d.tripped)
	}
	if allowed {
		t.Fatal("the previous window's spending was not carried over")
	}
}

func TestCountStandsAsideWhenTheReadFails(t *testing.T) {
	l := newLimiter(nil)
	l.counters = &fakeKV{readErr: errors.New("backend down")}

	var d decision
	allowed, _ := l.count(context.Background(), "local/bob", "acme", time.Minute, 5, &d)
	if d.tripped != reasonCounterRead {
		t.Fatalf("tripped %q, want %q", d.tripped, reasonCounterRead)
	}
	if !allowed {
		t.Fatal("a failed read refused the request")
	}
}

func TestCountStandsAsideWhenTheIncrementFails(t *testing.T) {
	kv := newFakeKV()
	kv.incrementErr = errors.New("backend down")
	l := newLimiter(nil)
	l.counters = kv

	var d decision
	allowed, _ := l.count(context.Background(), "local/bob", "acme", time.Minute, 5, &d)
	if d.tripped != reasonCounterIncrement {
		t.Fatalf("tripped %q, want %q", d.tripped, reasonCounterIncrement)
	}
	if !allowed {
		t.Fatal("a failed increment refused the request")
	}
}

func TestCountStandsAsideOnAKeyHoldingNoNumber(t *testing.T) {
	kv := newFakeKV()
	l := pinned()
	l.counters = kv

	width := int64(time.Minute)
	kv.putRaw(countKey("acme", "local/bob", pinnedAt.UnixNano()/width-1), "not a number")

	var d decision
	allowed, _ := l.count(context.Background(), "local/bob", "acme", time.Minute, 5, &d)
	if d.tripped != reasonCounterNotNumeric {
		t.Fatalf("tripped %q, want %q", d.tripped, reasonCounterNotNumeric)
	}
	if !allowed {
		t.Fatal("a foreign value refused the request")
	}
}

// --- the thresholds --------------------------------------------------------

func TestThresholdsReadsTheLayerItIsGiven(t *testing.T) {
	store := &fakeStore{values: map[string]any{
		windowPath: 2 * time.Minute,
		limitPath:  42,
	}}
	l := newLimiter(nil)
	l.store = store

	var d decision
	window, limit := l.thresholds(settings.Global(), &d)
	if d.tripped != "" {
		t.Fatalf("tripped %q", d.tripped)
	}
	if window != 2*time.Minute || limit != 42 {
		t.Fatalf("window = %v, limit = %d", window, limit)
	}
}

// The settings module registers every declared item under its full path — the
// namespace, a dot, then the item key — and a read is looked up by that exact
// path. This holds the two paths the layer reads against the declaration
// itself, so a read that named the bare item key cannot pass: it covers no
// declaration, and every decision would therefore fail open on it with nothing
// in the assembly to show for it.
func TestThresholdsReadsTheDeclaredPathOfEachItem(t *testing.T) {
	declared, ok := declaredSettingsSchema()
	if !ok {
		t.Fatal("the module declares no settings.Schema, so the read paths are held to nothing")
	}
	if len(declared.Items) == 0 {
		t.Fatal("the declared schema has no items, so this case would check nothing")
	}

	// The store serves every key and records the ones it was asked for, so a
	// read reaches the second item only if the first one did not trip.
	store := &recordingStore{}
	l := newLimiter(nil)
	l.store = store

	var d decision
	l.thresholds(settings.Global(), &d)
	if d.tripped != "" {
		t.Fatalf("tripped %q; the store serves every key, so no read should fail", d.tripped)
	}

	want := make([]string, 0, len(declared.Items))
	for key := range declared.Items {
		want = append(want, declared.Namespace+"."+key)
	}
	got := slices.Clone(store.keys)
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("the layer read %v, want the declared paths %v", store.keys, want)
	}
}

func TestThresholdsStandsAsideWhenTheReadFails(t *testing.T) {
	l := newLimiter(nil)
	l.store = &fakeStore{err: settings.ErrUnknownKey}

	var d decision
	l.thresholds(settings.Global(), &d)
	if d.tripped != reasonThresholdRead {
		t.Fatalf("tripped %q, want %q", d.tripped, reasonThresholdRead)
	}
}

// Both domains exclude zero. A limit of zero would refuse every request that
// carries an identity string, since the increment happens before the decision;
// a window of zero leaves the window index undefined.
func TestThresholdsStandsAsideOutsideTheDomain(t *testing.T) {
	cases := map[string]map[string]any{
		"zero limit":      {windowPath: time.Minute, limitPath: 0},
		"zero window":     {windowPath: time.Duration(0), limitPath: 10},
		"negative limit":  {windowPath: time.Minute, limitPath: -1},
		"negative window": {windowPath: -time.Minute, limitPath: 10},
	}
	for name, values := range cases {
		l := newLimiter(nil)
		l.store = &fakeStore{values: values}
		var d decision
		l.thresholds(settings.Global(), &d)
		if d.tripped != reasonThresholdDomain {
			t.Errorf("%s: tripped %q, want %q", name, d.tripped, reasonThresholdDomain)
		}
	}
}

// --- the decision ----------------------------------------------------------

func TestDecideLetsARequestWithoutAnIdentityThrough(t *testing.T) {
	l := newLimiter(nil)
	l.counters = newFakeKV()
	l.store = &fakeStore{values: map[string]any{windowPath: time.Minute, limitPath: 0}}

	reached := false
	next := nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) { reached = true })
	rec := newRecorder()

	if d := l.decide(rec, newRequest(), next, authn.Identity{}, false); d.tripped != "" {
		t.Fatalf("tripped %q", d.tripped)
	}
	if !reached {
		t.Fatal("the request was not passed on")
	}
	if rec.status != nethttp.StatusOK {
		t.Fatalf("status = %d", rec.status)
	}
}

func TestDecideAnswersTooManyRequestsWithRetryAfter(t *testing.T) {
	l := pinned()
	l.counters = newFakeKV()
	l.store = &fakeStore{values: map[string]any{windowPath: time.Minute, limitPath: 1}}

	reached := false
	next := nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) { reached = true })

	// The first request spends the window.
	rec := newRecorder()
	l.decide(rec, newRequest(), next, authn.Identity{}, true)
	if !reached {
		t.Fatal("the first request was not passed on")
	}

	reached = false
	rec = newRecorder()
	if d := l.decide(rec, newRequest(), next, authn.Identity{}, true); d.tripped != "" {
		t.Fatalf("tripped %q", d.tripped)
	}
	if reached {
		t.Fatal("the request past the limit reached the inner chain")
	}
	if rec.status != nethttp.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.status)
	}
	if got := rec.header.Get("Retry-After"); got == "" || got == "0" {
		t.Fatalf("Retry-After = %q", got)
	}
}

func TestDecideLetsThroughWhenTheThresholdsAreUnreadable(t *testing.T) {
	l := pinned()
	l.counters = newFakeKV()
	l.store = &fakeStore{err: settings.ErrTypeMismatch}
	l.signal = newSignalTo(&bytes.Buffer{})

	reached := false
	next := nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) { reached = true })
	rec := newRecorder()

	if d := l.decide(rec, newRequest(), next, authn.Identity{}, true); d.tripped != reasonThresholdRead {
		t.Fatalf("tripped %q, want %q", d.tripped, reasonThresholdRead)
	}
	if !reached {
		t.Fatal("an unreadable threshold refused the request")
	}
}

// --- the signal ------------------------------------------------------------

// everyClass is the decision a request that ran the whole counting path
// produced: it evaluated every class and tripped none.
var everyClass = decision{evaluated: []reason{
	reasonThresholdRead,
	reasonThresholdDomain,
	reasonCounterRead,
	reasonCounterNotNumeric,
	reasonCounterIncrement,
}}

// stoppedAt is the decision a request the layer stopped at r produced: the
// classes evaluated ahead of r in the layer's own order, then r itself.
func stoppedAt(r reason) decision {
	d := decision{}
	for _, c := range everyClass.evaluated {
		d.evaluated = append(d.evaluated, c)
		if c == r {
			d.tripped = r
			return d
		}
	}
	return d
}

// The classes do not imply one another: while one has been failing, another
// has to be able to report itself. A single shared flag would keep the second
// one from ever writing a line.
func TestSignalReportsEachReasonOnItsOwn(t *testing.T) {
	var out bytes.Buffer
	s := newSignalTo(&out)

	s.settle(stoppedAt(reasonCounterRead))
	s.settle(stoppedAt(reasonCounterRead))
	// The counter passes in a decision that reached it, so that class clears;
	// the thresholds then fail and report themselves.
	s.settle(everyClass)
	s.settle(stoppedAt(reasonThresholdRead))

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("wrote %d lines, want 3:\n%s", len(lines), out.String())
	}
	if !strings.Contains(lines[0], "letting requests through") ||
		!strings.Contains(lines[0], string(reasonCounterRead)) {
		t.Errorf("first line = %q, want the counter reported", lines[0])
	}
	if !strings.Contains(lines[1], "back to limiting") ||
		!strings.Contains(lines[1], string(reasonCounterRead)) {
		t.Errorf("second line = %q, want the counter's recovery", lines[1])
	}
	if !strings.Contains(lines[2], "letting requests through") ||
		!strings.Contains(lines[2], string(reasonThresholdRead)) {
		t.Errorf("third line = %q, want the thresholds reported", lines[2])
	}
}

func TestSignalWritesOneLinePerFailureAndOnePerRecovery(t *testing.T) {
	var out bytes.Buffer
	s := newSignalTo(&out)

	for i := 0; i < 5; i++ {
		s.settle(stoppedAt(reasonCounterRead))
	}
	s.settle(everyClass) // a decision that evaluated this class and passed it
	s.settle(stoppedAt(reasonCounterRead))

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("wrote %d lines, want 3 (failure, recovery, failure):\n%s", len(lines), out.String())
	}
}

// A class whose condition was fixed clears its flag even while another class
// keeps failing, otherwise it can never report again. The thresholds are
// evaluated ahead of the counter, so they clear in a decision that then trips
// the counter.
func TestSignalRecoversAClassWhileAnotherKeepsFailing(t *testing.T) {
	var out bytes.Buffer
	s := newSignalTo(&out)

	// The thresholds fail, then the counter fails behind them.
	s.settle(stoppedAt(reasonThresholdRead))
	s.settle(stoppedAt(reasonCounterRead))
	s.settle(stoppedAt(reasonCounterRead))
	// The thresholds recover in a decision that still trips the counter.
	s.settle(stoppedAt(reasonCounterRead))

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	thresholds := 0
	for _, line := range lines {
		if strings.Contains(line, string(reasonThresholdRead)) {
			thresholds++
		}
	}
	if thresholds != 2 {
		t.Fatalf("the thresholds class wrote %d lines, want 2 (failure, recovery):\n%s",
			thresholds, out.String())
	}
}

// --- the descriptor --------------------------------------------------------

func TestDuplicateEndpointFindsTheSecondOne(t *testing.T) {
	if _, twice := duplicateEndpoint([]string{"public", "admin"}); twice {
		t.Fatal("distinct names were read as a repeat")
	}
	name, twice := duplicateEndpoint([]string{"public", "admin", "public"})
	if !twice || name != "public" {
		t.Fatalf("duplicateEndpoint = %q, %v", name, twice)
	}
}

func TestPrepareRefusesAnEmptyEndpointList(t *testing.T) {
	_, err := Module().Prepare(context.Background(), registryWith(t, nil))
	if !errors.Is(err, config.ErrMissingRequired) {
		t.Fatalf("err = %v, want ErrMissingRequired", err)
	}
}

func TestPrepareRefusesARepeatedEndpoint(t *testing.T) {
	_, err := Module().Prepare(context.Background(), registryWith(t, []string{"public", "public"}))
	if !errors.Is(err, ErrDuplicateEndpoint) {
		t.Fatalf("err = %v, want ErrDuplicateEndpoint", err)
	}
}

func TestPrepareAcceptsAConfiguredEndpoint(t *testing.T) {
	stance, err := Module().Prepare(context.Background(), registryWith(t, []string{"public"}))
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if stance.State != core.StateEnabled {
		t.Fatalf("state = %v, want enabled", stance.State)
	}
}

// --- the layer -------------------------------------------------------------

func TestMiddlewareCarriesTheAnchorsItNeeds(t *testing.T) {
	mw := newLimiter(nil).middleware()

	if len(mw.Provides) != 1 || mw.Provides[0] != core.Token((*Limiter)(nil)) {
		t.Fatalf("Provides = %v, want the Limiter token", mw.Provides)
	}
	want := []core.Token{(*authn.Gate)(nil), (*tenant.Resolution)(nil), (*audit.Audit)(nil)}
	if len(mw.After) != len(want) {
		t.Fatalf("After = %v, want %v", mw.After, want)
	}
	for i := range want {
		if mw.After[i] != want[i] {
			t.Errorf("After[%d] = %v, want %v", i, mw.After[i], want[i])
		}
	}
	if mw.Name != ModuleName {
		t.Errorf("Name = %q, want %q", mw.Name, ModuleName)
	}
}

// The product must implement what the descriptor delivers, or the registry
// ends the startup with ErrUndeliveredCapability.
func TestProductDeliversTheLimiter(t *testing.T) {
	var delivered any = newLimiter(nil)
	if _, ok := delivered.(Limiter); !ok {
		t.Fatal("the product does not implement Limiter")
	}
}

// --- the failure classes ---------------------------------------------------

// A context the caller cancelled belongs to the caller, not to the backend: the
// decision reports nothing and lets the request through. Folding it into the
// counter class would write a failure line for one client disconnect and raise
// that class's shared flag, so the next real backend failure would write
// nothing.
func TestDecideReportsNothingForACancelledContext(t *testing.T) {
	next := nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) {})
	cases := []struct {
		name     string
		counters *fakeKV
	}{
		{"the read", &fakeKV{readErr: context.Canceled}},
		{"the increment", &fakeKV{incrementErr: context.Canceled}},
	}
	for _, c := range cases {
		var out bytes.Buffer
		l := pinned()
		l.counters = c.counters
		l.store = &fakeStore{values: map[string]any{windowPath: time.Minute, limitPath: 10}}
		l.signal = newSignalTo(&out)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		d := l.decide(newRecorder(), newRequest().WithContext(ctx), next, authn.Identity{}, true)
		l.signal.settle(d)

		if d.tripped != "" {
			t.Errorf("%s: tripped %q, want the empty reason", c.name, d.tripped)
		}
		if out.Len() != 0 {
			t.Errorf("%s: wrote %q, want nothing", c.name, out.String())
		}
	}
}

// Every diagnostic line carries the module's name once. The capability
// directory and the module name are the same token here, so a format that
// writes both repeats it.
func TestDiagnosticsNameTheModuleOnce(t *testing.T) {
	var out bytes.Buffer
	newSignalTo(&out).settle(stoppedAt(reasonCounterRead))
	if got := strings.Count(strings.TrimSpace(out.String()), ModuleName+":"); got != 1 {
		t.Errorf("the signal line names the module %d times, want 1: %q", got, out.String())
	}

	_, err := Module().Prepare(context.Background(), registryWith(t, nil))
	if err == nil {
		t.Fatal("Prepare accepted an empty endpoint list")
	}
	if got := strings.Count(err.Error(), ModuleName+":"); got != 1 {
		t.Errorf("the startup error names the module %d times, want 1: %q", got, err)
	}
}

// A decision returns at the first class that trips, so a class behind it was
// never asked about. Clearing it there would print "back to limiting" for a
// condition nothing checked.
func TestDecideClearsOnlyTheClassesItEvaluated(t *testing.T) {
	var out bytes.Buffer
	l := pinned()
	next := nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) {})
	l.signal = newSignalTo(&out)

	// One request through the layer, its class accounted for as wrap does.
	step := func() reason {
		d := l.decide(newRecorder(), newRequest(), next, authn.Identity{}, true)
		l.signal.settle(d)
		return d.tripped
	}

	// The counter read fails, so its class is reported.
	l.counters = &fakeKV{readErr: errors.New("backend down")}
	l.store = &fakeStore{values: map[string]any{windowPath: time.Minute, limitPath: 10}}
	if tripped := step(); tripped != reasonCounterRead {
		t.Fatalf("tripped %q, want %q", tripped, reasonCounterRead)
	}
	if !containsLine(out.String(), "letting requests through", string(reasonCounterRead)) {
		t.Fatalf("the counter class was not reported:\n%s", out.String())
	}

	// Only the thresholds are broken now, and the decision stops there: it
	// never reached the counter class, so that class must not be cleared.
	out.Reset()
	l.counters = newFakeKV()
	l.store = &fakeStore{values: map[string]any{windowPath: time.Duration(0), limitPath: 10}}
	if tripped := step(); tripped != reasonThresholdDomain {
		t.Fatalf("tripped %q, want %q", tripped, reasonThresholdDomain)
	}
	if containsLine(out.String(), "back to limiting", string(reasonCounterRead)) {
		t.Fatalf("a class the decision never evaluated was reported recovered:\n%s", out.String())
	}

	// A decision that reaches the counter and passes clears it.
	out.Reset()
	l.store = &fakeStore{values: map[string]any{windowPath: time.Minute, limitPath: 10}}
	if tripped := step(); tripped != "" {
		t.Fatalf("tripped %q", tripped)
	}
	if !containsLine(out.String(), "back to limiting", string(reasonCounterRead)) {
		t.Fatalf("the counter class was not reported recovered:\n%s", out.String())
	}
}

// A window whose double overflows the duration type has its counting key
// written with a non-positive expiry, which the key-value contract reads as
// "no expiry": the key is then left in the store forever. It is out of domain
// like any other window the module does not act on.
func TestThresholdsStandsAsideOnAWindowTooLongToDouble(t *testing.T) {
	l := newLimiter(nil)
	l.store = &fakeStore{values: map[string]any{
		windowPath: 150 * 365 * 24 * time.Hour,
		limitPath:  10,
	}}
	var d decision
	l.thresholds(settings.Global(), &d)
	if d.tripped != reasonThresholdDomain {
		t.Fatalf("tripped %q, want %q", d.tripped, reasonThresholdDomain)
	}
}

// containsLine reports whether any line of out holds both parts.
func containsLine(out, first, second string) bool {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, first) && strings.Contains(line, second) {
			return true
		}
	}
	return false
}

// --- helpers ---------------------------------------------------------------

type recorder struct {
	header nethttp.Header
	status int
}

func newRecorder() *recorder {
	return &recorder{header: make(nethttp.Header), status: nethttp.StatusOK}
}

func (r *recorder) Header() nethttp.Header      { return r.header }
func (r *recorder) WriteHeader(code int)        { r.status = code }
func (r *recorder) Write(b []byte) (int, error) { return len(b), nil }

func newRequest() *nethttp.Request {
	return httptest.NewRequest(nethttp.MethodGet, "/things", nil)
}

// declaredSettingsSchema picks this module's settings declaration out of its
// own descriptor, so a case can hold the code to what the module declares
// rather than to a copy of the declaration.
func declaredSettingsSchema() (settings.Schema, bool) {
	for _, resource := range Module().Resources {
		if declared, ok := resource.(settings.Schema); ok {
			return declared, true
		}
	}
	return settings.Schema{}, false
}

// recordingStore records the key of every read it serves.
//
// It answers under a value inside both domains, so that a decision reaches the
// counter and a read's own failure cannot stand in for a wrong path.
type recordingStore struct {
	keys []string
}

func (s *recordingStore) Get(_ settings.Layer, key string, target any) error {
	s.keys = append(s.keys, key)
	switch out := target.(type) {
	case *time.Duration:
		*out = time.Minute
	case *int:
		*out = 10
	default:
		return errors.New("unsupported target")
	}
	return nil
}

func (s *recordingStore) Set(context.Context, settings.Write) error { return nil }
func (s *recordingStore) Delete(context.Context, settings.Layer, string, string) error {
	return nil
}

func (s *recordingStore) List(context.Context, settings.Layer) ([]settings.Entry, error) {
	return nil, nil
}

// fakeStore serves the two items this module reads.
type fakeStore struct {
	values map[string]any
	err    error
}

func (s *fakeStore) Get(_ settings.Layer, key string, target any) error {
	if s.err != nil {
		return s.err
	}
	value, ok := s.values[key]
	if !ok {
		return settings.ErrUnknownKey
	}
	switch out := target.(type) {
	case *time.Duration:
		*out = value.(time.Duration)
	case *int:
		*out = value.(int)
	default:
		return errors.New("unsupported target")
	}
	return nil
}

func (s *fakeStore) Set(context.Context, settings.Write) error { return nil }
func (s *fakeStore) Delete(context.Context, settings.Layer, string, string) error {
	return nil
}

func (s *fakeStore) List(context.Context, settings.Layer) ([]settings.Entry, error) {
	return nil, nil
}

// fakeKV is an in-process counter store that remembers what expiry each key
// was created with.
type fakeKV struct {
	mu           sync.Mutex
	values       map[string]float64
	raw          map[string]string
	ttls         map[string]time.Duration
	readErr      error
	incrementErr error
}

func newFakeKV() *fakeKV {
	return &fakeKV{
		values: make(map[string]float64),
		raw:    make(map[string]string),
		ttls:   make(map[string]time.Duration),
	}
}

func (k *fakeKV) Get(_ context.Context, key string) ([]byte, bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.readErr != nil {
		return nil, false, k.readErr
	}
	if raw, ok := k.raw[key]; ok {
		return []byte(raw), true, nil
	}
	value, ok := k.values[key]
	if !ok {
		return nil, false, nil
	}
	return []byte(strconv.FormatFloat(value, 'g', -1, 64)), true, nil
}

func (k *fakeKV) Set(context.Context, string, []byte, time.Duration) error { return nil }
func (k *fakeKV) Delete(context.Context, string) error                     { return nil }

func (k *fakeKV) IncrByFloat(_ context.Context, key string, delta float64) (float64, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.incrementErr != nil {
		return 0, k.incrementErr
	}
	k.values[key] += delta
	return k.values[key], nil
}

func (k *fakeKV) IncrByFloatWithTTL(_ context.Context, key string, delta float64, ttl time.Duration) (float64, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.incrementErr != nil {
		return 0, k.incrementErr
	}
	// The expiry is attached when the call creates the key, exactly as the
	// primitives specify: a live key keeps the expiry it already has.
	if _, live := k.values[key]; !live {
		k.ttls[key] = ttl
	}
	k.values[key] += delta
	return k.values[key], nil
}

func (k *fakeKV) CompareAndSwap(context.Context, string, []byte, []byte) (bool, error) {
	return false, nil
}

func (k *fakeKV) SetIfAbsent(context.Context, string, []byte, time.Duration) (bool, error) {
	return false, nil
}

func (k *fakeKV) value(key string) float64 {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.values[key]
}

func (k *fakeKV) ttlOf(key string) time.Duration {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.ttls[key]
}

func (k *fakeKV) seed(key string, value float64) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.values[key] = value
}

func (k *fakeKV) putRaw(key, raw string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.raw[key] = raw
}

// fakeReader serves this module's section.
type fakeReader struct{ endpoints []string }

func (r fakeReader) Decode(_ string, target any) error {
	cfg, ok := target.(*Config)
	if !ok {
		return errors.New("unsupported target")
	}
	cfg.Endpoints = r.endpoints
	return nil
}

// registryWith hands back a registry holding a configuration reader, so that
// Prepare reaches the endpoint checks.
//
// The reader is supplied rather than registered as a module: nothing is
// constructed at Prepare, and the configuration reader is a bootstrap product
// the host puts in before the drive begins.
func registryWith(t *testing.T, endpoints []string) *core.Registry {
	t.Helper()
	reg := core.New()
	reg.Supply((*config.Reader)(nil), fakeReader{endpoints})
	return reg
}
