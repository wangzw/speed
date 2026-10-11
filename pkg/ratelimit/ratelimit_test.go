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

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/tenant"
)

func init() {
	// gin prints a warning to standard output when an engine is made in its debug
	// mode, and the cases that build a context make one each.
	gin.SetMode(gin.TestMode)
}

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

// A tenant identifier may hold a colon, so a key built from a tenant segment
// and an identity key has to keep the two apart. Without escaping, these two
// would build one key and share one count across two tenants.
//
// The segments are the escape's inputs, not identity keys: an account
// identifier cannot hold a colon, so no identity makes the second one, and the
// test is about what the key builder does with a segment that does.
func TestEscapeKeepsTwoDistinctPairsApart(t *testing.T) {
	first := countKey("a:b", "c", 7)
	second := countKey("a", "b:c", 7)
	if first == second {
		t.Fatalf("two distinct pairs built the same key %q", first)
	}
}

func TestEscapeLeavesOrdinarySegmentsAlone(t *testing.T) {
	key := accountKey(t)
	if got := countKey("acme", key, 7); got != "ratelimit:acme:"+key+":7" {
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
		allowed, _ := l.count(context.Background(), accountKey(t), "acme", time.Minute, 5, &d)
		if d.tripped != "" {
			t.Fatalf("request %d tripped %q", i, d.tripped)
		}
		if !allowed {
			t.Fatalf("request %d was refused, want allowed", i)
		}
	}

	var d decision
	allowed, retry := l.count(context.Background(), accountKey(t), "acme", time.Minute, 5, &d)
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
		l.count(context.Background(), accountKey(t), "acme", time.Minute, 2, &decision{})
	}
	key := countKey("acme", accountKey(t), pinnedAt.UnixNano()/int64(time.Minute))
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

	l.count(context.Background(), accountKey(t), "acme", time.Minute, 1, &decision{})
	for i := 0; i < 4; i++ {
		allowed, _ := l.count(context.Background(), accountKey(t), "acme", time.Minute, 1, &decision{})
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
	l.count(context.Background(), accountKey(t), "acme", window, 10, &decision{})

	index := pinnedAt.UnixNano() / int64(window)
	ttl := kv.ttlOf(countKey("acme", accountKey(t), index))
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
	previous := countKey("acme", accountKey(t), pinnedAt.UnixNano()/width-1)
	kv.seed(previous, 10)

	// At a window's start the previous window weighs one, so a limit of five
	// is already spent.
	var d decision
	allowed, _ := l.count(context.Background(), accountKey(t), "acme", window, 5, &d)
	if d.tripped != "" {
		t.Fatalf("tripped %q", d.tripped)
	}
	if allowed {
		t.Fatal("the previous window's spending was not carried over")
	}
}

// The previous window weighs by the part of this window that has not elapsed,
// not by one and not by nothing. The clock stands three quarters into the
// window, so the previous window's ten requests weigh 2.5 and, with this
// request, the estimate is 3.5: a limit of four lets it through, and a limit of
// three refuses it. A previous window weighed as one, or by the part that has
// elapsed, refuses the first; one weighed as nothing lets the second through.
// The seconds left in the window are read at the same instant.
func TestCountWeighsThePreviousWindowByWhatHasNotElapsed(t *testing.T) {
	const window = time.Minute
	width := int64(window)
	start := pinnedAt.UnixNano() / width * width

	cases := []struct {
		limit int
		want  bool
	}{
		{4, true},
		{3, false},
	}
	for _, c := range cases {
		kv := newFakeKV()
		l := pinned()
		l.counters = kv
		l.now = func() time.Time { return time.Unix(0, start+3*width/4) }
		kv.seed(countKey("acme", accountKey(t), start/width-1), 10)

		var d decision
		allowed, retry := l.count(context.Background(), accountKey(t), "acme", window, c.limit, &d)
		if d.tripped != "" {
			t.Fatalf("limit %d: tripped %q", c.limit, d.tripped)
		}
		if allowed != c.want {
			t.Errorf("limit %d: allowed = %v, want %v: the estimate is 3.5 (ten requests weighing a quarter, plus this one)",
				c.limit, allowed, c.want)
		}
		if retry != 15 {
			t.Errorf("limit %d: retryAfter = %d, want the 15 seconds left of the window", c.limit, retry)
		}
	}
}

func TestCountStandsAsideWhenTheReadFails(t *testing.T) {
	l := newLimiter(nil)
	l.counters = &fakeKV{readErr: errors.New("backend down")}

	var d decision
	allowed, _ := l.count(context.Background(), accountKey(t), "acme", time.Minute, 5, &d)
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
	allowed, _ := l.count(context.Background(), accountKey(t), "acme", time.Minute, 5, &d)
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
	kv.putRaw(countKey("acme", accountKey(t), pinnedAt.UnixNano()/width-1), "not a number")

	var d decision
	allowed, _ := l.count(context.Background(), accountKey(t), "acme", time.Minute, 5, &d)
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
// path. This holds the paths the layers read against the declaration itself, so
// a read that named the bare item key cannot pass: it covers no declaration,
// and every decision would therefore fail open on it with nothing in the
// assembly to show for it.
//
// Both layers' readers are driven. The quota reads its two items and the shield
// its three, and between them they have to cover every declared item: a declared
// item no reader asks for is either dead weight in the declaration or a read
// that went missing, and this case fails either way.
func TestThresholdsReadsTheDeclaredPathOfEachItem(t *testing.T) {
	declared, ok := declaredSettingsSchema()
	if !ok {
		t.Fatal("the module declares no settings.Schema, so the read paths are held to nothing")
	}
	if len(declared.Items) == 0 {
		t.Fatal("the declared schema has no items, so this case would check nothing")
	}

	// The store serves every key and records the ones it was asked for, so a
	// read reaches the next item only if the one before it did not trip.
	store := &recordingStore{}
	l := newLimiter(nil)
	l.store = store

	var quota decision
	l.thresholds(settings.Global(), &quota)
	if quota.tripped != "" {
		t.Fatalf("the quota tripped %q; the store serves every key, so no read should fail", quota.tripped)
	}
	var shield decision
	l.shieldThresholds(&shield)
	if shield.tripped != "" {
		t.Fatalf("the shield tripped %q; the store serves every key, so no read should fail", shield.tripped)
	}

	want := make([]string, 0, len(declared.Items))
	for key := range declared.Items {
		want = append(want, declared.Namespace+"."+key)
	}
	got := slices.Clone(store.keys)
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("the layers read %v, want the declared paths %v", store.keys, want)
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
// carries an identity, since the increment happens before the decision;
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

// An exempt route, a command with no authentication module and a layer whose
// After edge did not land all arrive with no identity. The layer the module's
// own descriptor registers, which reads the identity off the request's context,
// lets such a request through without counting it. The limit here is zero, so a
// layer that counted the request would trip the domain check and write a line.
func TestARequestWithoutAnIdentityIsLetThroughAndNotCounted(t *testing.T) {
	counters := newFakeKV()
	var out lockedBuffer
	l := pinned("public")
	l.counters = counters
	l.store = &fakeStore{values: map[string]any{windowPath: time.Minute, limitPath: 0}}
	l.signal = newSignalTo(&out)
	h := startHost(t, hostOptions{limiter: l, viaModule: true})
	anonymous := map[string]string{anonymousHeader: "1"}

	if got := h.send(t, nethttp.MethodGet, "/things", anonymous); got.status != nethttp.StatusNoContent {
		t.Fatalf("a request with no identity was answered %d, want the route's 204", got.status)
	}
	if got := h.reached.count(); got != 1 {
		t.Fatalf("the route served %d requests, want 1", got)
	}
	// A request that matches no route has no identity either, and the layer is
	// as indifferent to it.
	requireProblem(t, h.send(t, nethttp.MethodGet, "/nowhere", anonymous), nethttp.StatusNotFound)
	if got := counters.size(); got != 0 {
		t.Fatalf("the layer counted a request with no identity: %d keys written", got)
	}
	if out.String() != "" {
		t.Fatalf("the layer reported on a request it had nothing to decide on: %q", out.String())
	}
}

// A request past the limit is refused in the entry point's error shape, with
// Retry-After on the response: the header is set before the response is written
// and survives it. The clock stands at the start of a window, so the whole
// window is what is left.
func TestARequestPastTheLimitIsAnsweredTooManyRequestsWithRetryAfter(t *testing.T) {
	l := pinned("public")
	l.counters = newFakeKV()
	l.store = &fakeStore{values: map[string]any{windowPath: time.Minute, limitPath: 1}}
	h := startHost(t, hostOptions{limiter: l})

	// The first request spends the window.
	if got := h.get(t, "/things"); got.status != nethttp.StatusNoContent {
		t.Fatalf("the first request was answered %d, want the route's 204", got.status)
	}

	refused := h.get(t, "/things")
	requireProblem(t, refused, nethttp.StatusTooManyRequests)
	if got := requireRetryAfter(t, refused); got != 60 {
		t.Fatalf("Retry-After is %d, want the 60 seconds left of the window", got)
	}
	if got := h.reached.count(); got != 1 {
		t.Fatalf("the route served %d requests, want only the first", got)
	}
	for i, d := range h.decisions.all() {
		if d.tripped != "" {
			t.Fatalf("decision %d tripped %q", i, d.tripped)
		}
	}
}

// When a module delivers the ErrorResponder capability the shape of the refusal
// is its own, and what the layer hands it is the status and nothing else. The
// Retry-After the layer set before asking for the response is still on it.
func TestADeliveredErrorResponderDecidesTheShapeOfTheRefusal(t *testing.T) {
	responder := &capturingResponder{}
	l := pinned("public")
	l.counters = newFakeKV()
	l.store = &fakeStore{values: map[string]any{windowPath: time.Minute, limitPath: 1}}
	h := startHost(t, hostOptions{limiter: l, responder: responder})

	h.get(t, "/things")
	refused := h.get(t, "/things")

	if refused.status != nethttp.StatusTooManyRequests {
		t.Fatalf("the refusal is %d, want 429", refused.status)
	}
	if ct := refused.header.Get("Content-Type"); ct != customMediaType {
		t.Fatalf("the refusal's media type is %q, want the responder's %q", ct, customMediaType)
	}
	if refused.body != `{"code":429}` {
		t.Fatalf("the refusal's body is %q, want the responder's", refused.body)
	}
	if got := requireRetryAfter(t, refused); got != 60 {
		t.Fatalf("Retry-After is %d, want the 60 seconds left of the window", got)
	}
	handed := responder.handed()
	if len(handed) != 1 || handed[0] != (speedhttp.Problem{Status: nethttp.StatusTooManyRequests}) {
		t.Fatalf("the responder was handed %+v, want one Problem carrying the status 429 alone", handed)
	}
}

// The layer does not read the route, so a request that matches none is counted
// like any other, and the one past the limit is refused by the layer where the
// entry point would have answered 404. The decisions come to the same as for a
// request with a route: reading no route is not a failure of any class.
func TestARequestMatchingNoRouteIsCountedAndRefusedLikeAnyOther(t *testing.T) {
	counters := newFakeKV()
	var out lockedBuffer
	l := pinned("public")
	l.counters = counters
	l.store = &fakeStore{values: map[string]any{windowPath: time.Minute, limitPath: 1}}
	l.signal = newSignalTo(&out)
	h := startHost(t, hostOptions{limiter: l})

	requireProblem(t, h.get(t, "/nowhere"), nethttp.StatusNotFound)
	refused := h.get(t, "/nowhere")

	requireProblem(t, refused, nethttp.StatusTooManyRequests)
	requireRetryAfter(t, refused)
	index := pinnedAt.UnixNano() / int64(time.Minute)
	if got := counters.value(countKey("", accountKey(t), index)); got != 2 {
		t.Fatalf("the window holds a count of %v, want both requests counted", got)
	}
	if got := h.reached.count(); got != 0 {
		t.Fatalf("a request that matches no route reached the route's handler %d times", got)
	}
	for i, d := range h.decisions.all() {
		if d.tripped != "" {
			t.Fatalf("decision %d tripped %q", i, d.tripped)
		}
	}
	if out.String() != "" {
		t.Fatalf("the layer reported a failure while counting requests that match no route: %q", out.String())
	}
}

// A path that exists with another method is counted like any other request, and
// the one past the limit is refused by the layer in place of the entry point's
// 405. The request let through gets the entry point's 405 with its Allow header;
// the refusal carries none, because a refusal that listed the methods of the
// path would tell a caller who is over its limit what the path accepts.
func TestARequestWhoseMethodIsNotAllowedIsCountedAndRefusedLikeAnyOther(t *testing.T) {
	l := pinned("public")
	l.counters = newFakeKV()
	l.store = &fakeStore{values: map[string]any{windowPath: time.Minute, limitPath: 1}}
	h := startHost(t, hostOptions{limiter: l})

	allowed := h.send(t, nethttp.MethodPost, "/things", nil)
	requireProblem(t, allowed, nethttp.StatusMethodNotAllowed)
	if allow := allowed.header.Get("Allow"); !strings.Contains(allow, nethttp.MethodGet) {
		t.Fatalf("the 405 carries Allow %q, want it to list GET", allow)
	}
	refused := h.send(t, nethttp.MethodPost, "/things", nil)

	requireProblem(t, refused, nethttp.StatusTooManyRequests)
	requireRetryAfter(t, refused)
	if allow := refused.header.Get("Allow"); allow != "" {
		t.Fatalf("the refusal carries Allow %q, which lists what the path accepts", allow)
	}
}

// An unreadable threshold lets the request through: the route serves it, and
// the class is reported.
func TestARequestIsLetThroughWhenTheThresholdsAreUnreadable(t *testing.T) {
	var out lockedBuffer
	l := pinned("public")
	l.counters = newFakeKV()
	l.store = &fakeStore{err: settings.ErrTypeMismatch}
	l.signal = newSignalTo(&out)
	h := startHost(t, hostOptions{limiter: l})

	if got := h.get(t, "/things"); got.status != nethttp.StatusNoContent {
		t.Fatalf("an unreadable threshold answered the request %d, want the route's 204", got.status)
	}
	if got := h.reached.count(); got != 1 {
		t.Fatalf("the route served %d requests, want 1", got)
	}
	decided := h.decisions.all()
	if len(decided) != 1 || decided[0].tripped != reasonThresholdRead {
		t.Fatalf("the decisions are %+v, want one that tripped %q", decided, reasonThresholdRead)
	}
	if !containsLine(out.String(), "letting requests through", string(reasonThresholdRead)) {
		t.Fatalf("the class was not reported: %q", out.String())
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
	s, clock := signalOnAPinnedClock(&out)

	s.settle(stoppedAt(reasonCounterRead))
	s.settle(stoppedAt(reasonCounterRead))
	// The counter passes in a decision that reached it once it has been quiet
	// long enough, so that class clears; the thresholds then fail and report
	// themselves.
	clock.advance(recoveryQuiet)
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
	s, clock := signalOnAPinnedClock(&out)

	for i := 0; i < 5; i++ {
		s.settle(stoppedAt(reasonCounterRead))
	}
	clock.advance(recoveryQuiet)
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
	s, clock := signalOnAPinnedClock(&out)

	// The thresholds fail, then the counter fails behind them.
	s.settle(stoppedAt(reasonThresholdRead))
	s.settle(stoppedAt(reasonCounterRead))
	s.settle(stoppedAt(reasonCounterRead))
	// The thresholds recover in a decision that still trips the counter,
	// once they have been quiet long enough.
	clock.advance(recoveryQuiet)
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

// hostLimiter is a type the host writes for itself. It embeds Limiter, so the
// interface's unexported method reaches it through the embedding and it
// delivers the capability without being this module's product.
type hostLimiter struct{ Limiter }

// Init wires this module's own product and leaves any other instance alone. An
// instance the host supplied has only to deliver Limiter; when it is not this
// module's product nothing is wired, so no layer is registered and the drive
// asks for no entry point, settings store or key-value primitives. The registry
// here holds none of them, so the second case, this module's own product
// supplied the same way, ends the drive on the entry point. That is what shows
// the first case passes because nothing was wired, not because wiring could not
// fail.
func TestInitWiresOnlyThisModulesOwnProduct(t *testing.T) {
	cases := []struct {
		name     string
		instance any
		wired    bool
	}{
		{"an instance of the host's own shape", hostLimiter{}, false},
		{"this module's own product", newLimiter([]string{"public"}), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reg := core.New()
			reg.Register(Module())
			reg.Supply(c.instance, ModuleName)

			err := reg.Drive(context.Background(), core.Drive{Depth: core.StageInit})

			if !c.wired {
				if err != nil {
					t.Fatalf("driving to Init returned %v, want an instance of another shape left alone", err)
				}
				return
			}
			if !errors.Is(err, core.ErrMissingProvider) || !strings.Contains(err.Error(), "the entry point") {
				t.Fatalf("driving to Init returned %v, want the wiring to fail taking up the entry point", err)
			}
		})
	}
}

// --- the layer -------------------------------------------------------------

func TestMiddlewareCarriesItsTokenAndItsStage(t *testing.T) {
	mw := newLimiter(nil).middleware()

	if mw.Handler == nil {
		t.Fatal("the layer has no handler")
	}
	if len(mw.Provides) != 1 || mw.Provides[0] != core.Token((*Limiter)(nil)) {
		t.Fatalf("Provides = %v, want the Limiter token", mw.Provides)
	}
	if mw.Stage != speedhttp.StageThrottle {
		t.Fatalf("Stage = %v, want the throttling stage", mw.Stage)
	}
	if len(mw.After) != 0 {
		t.Fatalf("After = %v, want none: the layer's place comes from its stage, not an anchor", mw.After)
	}
	if mw.Name != ModuleName {
		t.Errorf("Name = %q, want %q", mw.Name, ModuleName)
	}
}

// The layer's place on the chain comes from its stage: the throttling stage runs
// after the identifying stage, which settles the identity and the tenant, and
// before the recording stage, which is what keeps a refused request out of the
// journal. The stand-in layers are placed by their own stages, so only the stage
// order produces the order the chain listing shows.
func TestTheLayerSitsInsideIdentityAndOutsideTheJournal(t *testing.T) {
	l := pinned("public")
	l.counters = newFakeKV()
	l.store = &fakeStore{values: map[string]any{windowPath: time.Minute, limitPath: 10}}
	standingFor := func(name string, stage speedhttp.Stage, token core.Token) speedhttp.Middleware {
		return speedhttp.Middleware{
			Name:     name,
			Provides: []core.Token{token},
			Stage:    stage,
			Handler:  func(c *gin.Context) { c.Next() },
		}
	}
	h := startHost(t, hostOptions{limiter: l, register: func(ep speedhttp.Endpoint) {
		ep.Use(standingFor("gate", speedhttp.StageIdentify, (*authn.Gate)(nil)))
		ep.Use(standingFor("resolution", speedhttp.StageIdentify, (*tenant.Resolution)(nil)))
		ep.Use(standingFor("audit", speedhttp.StageRecord, (*audit.Audit)(nil)))
	}})

	limiting := strings.Index(h.chain, ModuleName+"(")
	if limiting < 0 {
		t.Fatalf("the chain %q lists no limiting layer", h.chain)
	}
	for _, inside := range []string{"gate(", "resolution("} {
		at := strings.Index(h.chain, inside)
		if at < 0 || at >= limiting {
			t.Errorf("the chain is %q, want %s ahead of the limiting layer", h.chain, inside)
		}
	}
	if at := strings.Index(h.chain, "audit("); at < 0 || at <= limiting {
		t.Errorf("the chain is %q, want the recording layer inside the limiting layer", h.chain)
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
		d := decideOnce(t, l, ctx)

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
	var clock *pinnedClock
	l.signal, clock = signalOnAPinnedClock(&out)

	// One request through the layer, its class accounted for as handle does.
	step := func() reason {
		return decideOnce(t, l, context.Background()).tripped
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
	// The counter class has been quiet long enough by now, so it is only the
	// decision not having asked about it that keeps it from clearing.
	clock.advance(recoveryQuiet)
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

// decideOnce runs one decision with an identity authntest builds, read back off
// the request's context as the layer's handler reads it, on a context gin makes
// for a test, carrying a request on ctx, and accounts for it as the handler does.
//
// The context has no chain behind it and no route, so what a case using it reads
// is the decision and the signal. Whether a request went on, and what it was
// answered with, is read off a real assembly by the cases that serve one.
func decideOnce(t testing.TB, l *limiter, ctx context.Context) decision {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(nethttp.MethodGet, "/things", nil).
		WithContext(authntest.Context(t, ctx))
	id, identified := authn.FromContext(c.Request.Context())
	if !identified {
		t.Fatal("authntest.Context left no identity on the request")
	}
	d := l.decide(c, id, identified)
	l.signal.settle(d)
	return d
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

func (s *recordingStore) Declared() []settings.DeclaredItem { return nil }

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

func (s *fakeStore) Declared() []settings.DeclaredItem { return nil }

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

// size is the number of keys the store holds.
func (k *fakeKV) size() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.values) + len(k.raw)
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
type fakeReader struct {
	endpoints        []string
	addressEndpoints []string
}

func (r fakeReader) Decode(_ string, target any) error {
	cfg, ok := target.(*Config)
	if !ok {
		return errors.New("unsupported target")
	}
	cfg.Endpoints = r.endpoints
	cfg.AddressEndpoints = r.addressEndpoints
	return nil
}

// registryWith hands back a registry holding a configuration reader, so that
// Prepare reaches the endpoint checks.
//
// The reader is the supplied instance of the config module, the way a host's
// run hands in the reader it loaded. A supplied instance exists from the Supply
// call on, so Prepare finds it although nothing has been constructed.
func registryWith(t *testing.T, endpoints []string) *core.Registry {
	t.Helper()
	return registryReading(t, fakeReader{endpoints: endpoints})
}

// registryReading is registryWith for a reader that serves both endpoint lists.
func registryReading(t *testing.T, reader fakeReader) *core.Registry {
	t.Helper()
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	return reg
}
