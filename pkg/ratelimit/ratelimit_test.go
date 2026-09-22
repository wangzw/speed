package ratelimit

import (
	"bytes"
	"context"
	"errors"
	nethttp "net/http"
	"net/http/httptest"
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
		allowed, _, tripped := l.count(context.Background(), "local/bob", "acme", time.Minute, 5)
		if tripped != "" {
			t.Fatalf("request %d tripped %q", i, tripped)
		}
		if !allowed {
			t.Fatalf("request %d was refused, want allowed", i)
		}
	}

	allowed, retry, tripped := l.count(context.Background(), "local/bob", "acme", time.Minute, 5)
	if tripped != "" {
		t.Fatalf("tripped %q", tripped)
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
		l.count(context.Background(), "local/bob", "acme", time.Minute, 2)
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

	l.count(context.Background(), "local/bob", "acme", time.Minute, 1)
	for i := 0; i < 4; i++ {
		allowed, _, _ := l.count(context.Background(), "local/bob", "acme", time.Minute, 1)
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
	l.count(context.Background(), "local/bob", "acme", window, 10)

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
	allowed, _, tripped := l.count(context.Background(), "local/bob", "acme", window, 5)
	if tripped != "" {
		t.Fatalf("tripped %q", tripped)
	}
	if allowed {
		t.Fatal("the previous window's spending was not carried over")
	}
}

func TestCountStandsAsideWhenTheReadFails(t *testing.T) {
	l := newLimiter(nil)
	l.counters = &fakeKV{readErr: errors.New("backend down")}

	allowed, _, tripped := l.count(context.Background(), "local/bob", "acme", time.Minute, 5)
	if tripped != reasonCounterRead {
		t.Fatalf("tripped %q, want %q", tripped, reasonCounterRead)
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

	allowed, _, tripped := l.count(context.Background(), "local/bob", "acme", time.Minute, 5)
	if tripped != reasonCounterIncrement {
		t.Fatalf("tripped %q, want %q", tripped, reasonCounterIncrement)
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

	allowed, _, tripped := l.count(context.Background(), "local/bob", "acme", time.Minute, 5)
	if tripped != reasonCounterNotNumeric {
		t.Fatalf("tripped %q, want %q", tripped, reasonCounterNotNumeric)
	}
	if !allowed {
		t.Fatal("a foreign value refused the request")
	}
}

// --- the thresholds --------------------------------------------------------

func TestThresholdsReadsTheLayerItIsGiven(t *testing.T) {
	store := &fakeStore{values: map[string]any{
		windowKey: 2 * time.Minute,
		limitKey:  42,
	}}
	l := newLimiter(nil)
	l.store = store

	window, limit, tripped := l.thresholds(settings.Global())
	if tripped != "" {
		t.Fatalf("tripped %q", tripped)
	}
	if window != 2*time.Minute || limit != 42 {
		t.Fatalf("window = %v, limit = %d", window, limit)
	}
}

func TestThresholdsStandsAsideWhenTheReadFails(t *testing.T) {
	l := newLimiter(nil)
	l.store = &fakeStore{err: settings.ErrUnknownKey}

	_, _, tripped := l.thresholds(settings.Global())
	if tripped != reasonThresholdRead {
		t.Fatalf("tripped %q, want %q", tripped, reasonThresholdRead)
	}
}

// Both domains exclude zero. A limit of zero would refuse every request that
// carries an identity string, since the increment happens before the decision;
// a window of zero leaves the window index undefined.
func TestThresholdsStandsAsideOutsideTheDomain(t *testing.T) {
	cases := map[string]map[string]any{
		"zero limit":      {windowKey: time.Minute, limitKey: 0},
		"zero window":     {windowKey: time.Duration(0), limitKey: 10},
		"negative limit":  {windowKey: time.Minute, limitKey: -1},
		"negative window": {windowKey: -time.Minute, limitKey: 10},
	}
	for name, values := range cases {
		l := newLimiter(nil)
		l.store = &fakeStore{values: values}
		if _, _, tripped := l.thresholds(settings.Global()); tripped != reasonThresholdDomain {
			t.Errorf("%s: tripped %q, want %q", name, tripped, reasonThresholdDomain)
		}
	}
}

// --- the decision ----------------------------------------------------------

func TestDecideLetsARequestWithoutAnIdentityThrough(t *testing.T) {
	l := newLimiter(nil)
	l.counters = newFakeKV()
	l.store = &fakeStore{values: map[string]any{windowKey: time.Minute, limitKey: 0}}

	reached := false
	next := nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) { reached = true })
	rec := newRecorder()

	if tripped := l.decide(rec, newRequest(), next, authn.Identity{}, false); tripped != "" {
		t.Fatalf("tripped %q", tripped)
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
	l.store = &fakeStore{values: map[string]any{windowKey: time.Minute, limitKey: 1}}

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
	if tripped := l.decide(rec, newRequest(), next, authn.Identity{}, true); tripped != "" {
		t.Fatalf("tripped %q", tripped)
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

	if tripped := l.decide(rec, newRequest(), next, authn.Identity{}, true); tripped != reasonThresholdRead {
		t.Fatalf("tripped %q, want %q", tripped, reasonThresholdRead)
	}
	if !reached {
		t.Fatal("an unreadable threshold refused the request")
	}
}

// --- the signal ------------------------------------------------------------

// The classes do not imply one another: while one has been failing, another
// has to be able to report itself. A single shared flag would keep the second
// one from ever writing a line.
func TestSignalReportsEachReasonOnItsOwn(t *testing.T) {
	var out bytes.Buffer
	s := newSignalTo(&out)

	s.settle(reasonCounterRead)
	s.settle(reasonCounterRead)
	s.settle(reasonThresholdRead)

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	// The counter fails, then recovers as the thresholds fail.
	if len(lines) != 3 {
		t.Fatalf("wrote %d lines, want 3:\n%s", len(lines), out.String())
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
		s.settle(reasonCounterRead)
	}
	s.settle("") // a decision in which this class did not appear
	s.settle(reasonCounterRead)

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("wrote %d lines, want 3 (failure, recovery, failure):\n%s", len(lines), out.String())
	}
}

// A class whose condition was fixed must clear its flag even while another
// class keeps failing, otherwise it can never report again.
func TestSignalRecoversAClassWhileAnotherKeepsFailing(t *testing.T) {
	var out bytes.Buffer
	s := newSignalTo(&out)

	s.settle(reasonCounterRead)
	s.settle(reasonThresholdRead)
	// The counter is back, the thresholds are still broken.
	s.settle(reasonThresholdRead)
	s.settle(reasonThresholdRead)
	// The counter breaks again: this must be reported as a fresh failure.
	s.settle(reasonCounterRead)
	s.settle(reasonCounterRead)

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	counter := 0
	for _, line := range lines {
		if strings.Contains(line, string(reasonCounterRead)) {
			counter++
		}
	}
	if counter != 3 {
		t.Fatalf("the counter class wrote %d lines, want 3 (failure, recovery, failure):\n%s",
			counter, out.String())
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
