package password

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/vislake/speed/pkg/authn"
)

// ThrottleParams are the three numbers of the back-off, which are settings and
// can change while the process runs.
type ThrottleParams struct {
	// FreeAttempts is how many failures a key may have before any back-off.
	FreeAttempts int
	// Window is how long a key may go without a failure before its count
	// returns to zero, once any wait the count earned is over.
	Window time.Duration
	// MaxBackoff is the longest a key is made to wait.
	MaxBackoff time.Duration
}

// These are the domains of the back-off parameters; a value outside its domain,
// written through the store or around it, is read as its default.
const (
	// MinFreeAttempts and MaxFreeAttempts bound the free-attempt setting.
	MinFreeAttempts, MaxFreeAttempts = 1, 20
	// MinWindow and MaxWindow bound the failure-window setting.
	MinWindow, MaxWindow = time.Minute, 24 * time.Hour
	// MinBackoff and MaxBackoff bound the back-off cap setting.
	MinBackoff, MaxBackoff = 10 * time.Second, 24 * time.Hour
)

// DefaultThrottleParams are what the back-off runs on when nothing states other
// values: five free failures, a fifteen minute window and a fifteen minute cap.
func DefaultThrottleParams() ThrottleParams {
	return ThrottleParams{FreeAttempts: 5, Window: 15 * time.Minute, MaxBackoff: 15 * time.Minute}
}

// Normalized is the parameters with each one that is outside its domain replaced
// by the default.
func (p ThrottleParams) Normalized() ThrottleParams {
	d := DefaultThrottleParams()
	if p.FreeAttempts < MinFreeAttempts || p.FreeAttempts > MaxFreeAttempts {
		p.FreeAttempts = d.FreeAttempts
	}
	if p.Window < MinWindow || p.Window > MaxWindow {
		p.Window = d.Window
	}
	if p.MaxBackoff < MinBackoff || p.MaxBackoff > MaxBackoff {
		p.MaxBackoff = d.MaxBackoff
	}
	return p
}

func (p ThrottleParams) delay(n int) time.Duration {
	over := n - p.FreeAttempts
	if over <= 0 {
		return 0
	}
	// 2^over seconds overflows a Duration past 33 doublings, long after the cap
	// has been passed for any cap this type allows.
	if over > 30 {
		return p.MaxBackoff
	}
	return min(time.Duration(1<<over)*time.Second, p.MaxBackoff)
}

// MaxThrottleKeys bounds how many keys beyond the fixed ones the back-off
// remembers at once: a key is forgotten after a whole window without a failure,
// and a full table drops the key quiet longest, never a fixed one.
const MaxThrottleKeys = 16384

// ThrottleKey is the key a subject name is kept under: the SHA-256, in hex, of
// the name with its white space removed and its letters folded to lower case.
func ThrottleKey(subject string) string {
	folded := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, subject)
	sum := sha256.Sum256([]byte(folded))
	return hex.EncodeToString(sum[:])
}

// Key names one counter: the scope it is kept under and the key within it.
type Key struct {
	// Scope is the first half of the key.
	Scope authn.ThrottleScope
	// Key is the value the back-off counts under.
	Key string
}

// Throttle is an in-process authn.Throttle. Its state is per process, so with
// several replicas a key gets as many free attempts as there are replicas times
// FreeAttempts, and keys of different scopes never meet.
type Throttle struct {
	params func() ThrottleParams
	now    func() time.Time
	limit  int

	mu      sync.Mutex
	entries map[string]*throttleState
	// recency holds the non-fixed keys, most recently failed at the front.
	recency *list.List
}

type throttleState struct {
	key string
	// failures is the count of failed attempts as of last, before any aging.
	failures int
	// last is when the latest failure was recorded.
	last time.Time
	// inflight is how many reservations are unsettled, and reserved is when the
	// latest was made; an attempt in flight counts as a failure until settled.
	inflight int
	reserved time.Time
	// elem is the state's place in recency, nil for a fixed key.
	elem *list.Element
}

// NewThrottle makes the back-off. params is read on every call, so a changed
// setting takes effect at once, and nil params reads the defaults; a nil now
// reads the system clock. Fixed keys are never forgotten.
func NewThrottle(params func() ThrottleParams, now func() time.Time, fixed ...Key) *Throttle {
	if params == nil {
		params = DefaultThrottleParams
	}
	if now == nil {
		now = time.Now
	}
	t := &Throttle{
		params:  params,
		now:     now,
		limit:   MaxThrottleKeys,
		entries: make(map[string]*throttleState, len(fixed)),
		recency: list.New(),
	}
	for _, k := range fixed {
		tk := tableKey(k.Scope, k.Key)
		t.entries[tk] = &throttleState{key: tk}
	}
	return t
}

var _ authn.Throttle = (*Throttle)(nil)

// tableKey joins the scope and the key, the scope's length first, so no two
// pairs collide whatever bytes either holds.
func tableKey(scope authn.ThrottleScope, key string) string {
	return strconv.Itoa(len(scope)) + ":" + string(scope) + key
}

// live is the failure count at now, zero once the window has passed since the
// last failure and the wait that count earned is over; a wait longer than the
// window is served in full.
func (s *throttleState) live(now time.Time, p ThrottleParams) int {
	if now.Sub(s.last) >= p.Window && !now.Before(s.last.Add(p.delay(s.failures))) {
		return 0
	}
	return s.failures
}

func (s *throttleState) pending(now time.Time, p ThrottleParams) int {
	return s.live(now, p) + s.inflight
}

// wait is how long an attempt arriving at now has to wait, counted from the
// latest failure or the latest in-flight reservation.
func (s *throttleState) wait(now time.Time, p ThrottleParams) time.Duration {
	d := p.delay(s.pending(now, p))
	if d == 0 {
		return 0
	}
	from := s.last
	if s.inflight > 0 && s.reserved.After(from) {
		from = s.reserved
	}
	return max(from.Add(d).Sub(now), 0)
}

// sweep forgets the non-fixed keys whose count has gone, stopping at the first
// one whose count still stands. The caller holds mu.
func (t *Throttle) sweep(now time.Time, p ThrottleParams) {
	for el := t.recency.Back(); el != nil; el = t.recency.Back() {
		s, _ := el.Value.(*throttleState)
		if s.pending(now, p) > 0 {
			return
		}
		t.forget(s)
	}
}

// forget removes a key that is not fixed. The caller holds mu.
func (t *Throttle) forget(s *throttleState) {
	delete(t.entries, s.key)
	t.recency.Remove(s.elem)
}

// Check reports how long the key still has to wait, zero when it may try now.
// It reserves nothing, so a caller that goes on to check a credential reserves
// with Reserve, and it never returns an error.
func (t *Throttle) Check(_ context.Context, scope authn.ThrottleScope, key string) (time.Duration, error) {
	p := t.params().Normalized()
	now := t.now()

	t.mu.Lock()
	defer t.mu.Unlock()
	t.sweep(now, p)
	s, found := t.entries[tableKey(scope, key)]
	if !found {
		return 0, nil
	}
	return s.wait(now, p), nil
}

// Standing is how long the standing failures make the key wait, leaving out the
// attempts in flight; zero means none hold it back. It changes no state and
// admits nothing.
func (t *Throttle) Standing(scope authn.ThrottleScope, key string) time.Duration {
	p := t.params().Normalized()
	now := t.now()

	t.mu.Lock()
	defer t.mu.Unlock()
	s, found := t.entries[tableKey(scope, key)]
	if !found {
		return 0
	}
	d := p.delay(s.live(now, p))
	if d == 0 {
		return 0
	}
	return max(s.last.Add(d).Sub(now), 0)
}

// Attempt is one attempt that Reserve admitted. It is settled exactly once, with
// Refund or with Fail, and a second settlement does nothing. One goroutine owns
// it.
type Attempt struct {
	t     *Throttle
	key   string
	state *throttleState
	done  bool
}

// Reserve is Check and the first half of recording a failure: when the key must
// wait it returns the wait and no attempt, otherwise it counts the attempt as a
// failure now and returns it for the caller to settle.
func (t *Throttle) Reserve(scope authn.ThrottleScope, key string) (time.Duration, *Attempt) {
	p := t.params().Normalized()
	now := t.now()
	k := tableKey(scope, key)

	t.mu.Lock()
	defer t.mu.Unlock()
	t.sweep(now, p)
	s, found := t.entries[k]
	switch {
	case !found:
		s = t.enter(k)
	default:
		if wait := s.wait(now, p); wait > 0 {
			return wait, nil
		}
		if s.elem != nil {
			t.recency.MoveToFront(s.elem)
		}
	}
	s.inflight++
	s.reserved = now
	return 0, &Attempt{t: t, key: k, state: s}
}

// Refund gives back this attempt's own reservation, not the failures that stood
// before it; an attempt that never reached a check is given back the same way.
func (a *Attempt) Refund() {
	t := a.t
	p := t.params().Normalized()
	now := t.now()

	t.mu.Lock()
	defer t.mu.Unlock()
	if a.done {
		return
	}
	a.done = true
	// A state that was pushed out of a full table took its reservations with it.
	if t.entries[a.key] != a.state {
		return
	}
	s := a.state
	s.inflight--
	if s.elem != nil && s.pending(now, p) == 0 {
		t.forget(s)
	}
}

// Fail keeps the attempt as the failure it was reserved as, and engaged reports
// that this failure is the first beyond the free attempts, the one that started
// a back-off.
func (a *Attempt) Fail() (engaged bool) {
	t := a.t
	p := t.params().Normalized()
	now := t.now()

	t.mu.Lock()
	defer t.mu.Unlock()
	if a.done {
		return false
	}
	a.done = true
	own := a.state
	if t.entries[a.key] != own {
		own = nil
	}
	return t.record(a.key, now, p, own)
}

// Fail records one failed attempt, and engaged reports that this failure is the
// first beyond the free attempts, the one that started a back-off.
func (t *Throttle) Fail(_ context.Context, scope authn.ThrottleScope, key string) (bool, error) {
	p := t.params().Normalized()
	now := t.now()

	t.mu.Lock()
	defer t.mu.Unlock()
	t.sweep(now, p)
	return t.record(tableKey(scope, key), now, p, nil), nil
}

// enter makes a state for a new non-fixed key, dropping the quiet longest when
// the table is full. The caller holds mu.
func (t *Throttle) enter(k string) *throttleState {
	for t.recency.Len() >= t.limit {
		oldest, _ := t.recency.Back().Value.(*throttleState)
		t.forget(oldest)
	}
	s := &throttleState{key: k}
	s.elem = t.recency.PushFront(s)
	t.entries[k] = s
	return s
}

// record writes one failure under the key, and settles the reservation of own
// when it is not nil. engaged is whether the count has just passed the free
// attempts. The caller holds mu.
func (t *Throttle) record(k string, now time.Time, p ThrottleParams, own *throttleState) (engaged bool) {
	s, found := t.entries[k]
	switch {
	case !found:
		s = t.enter(k)
	case s.elem != nil:
		t.recency.MoveToFront(s.elem)
	}
	if own != nil {
		s.inflight--
	}
	s.failures = s.live(now, p) + 1
	s.last = now
	return s.failures == p.FreeAttempts+1
}

// Succeed clears the key's failures, the ones that stand and no reservation
// that is still in flight. It is for the callers that want a pass to clear the
// count; a backend that checks a credential on every request does not call it.
func (t *Throttle) Succeed(_ context.Context, scope authn.ThrottleScope, key string) error {
	k := tableKey(scope, key)

	t.mu.Lock()
	defer t.mu.Unlock()
	s, found := t.entries[k]
	if !found {
		return nil
	}
	s.failures = 0
	if s.elem != nil && s.inflight == 0 {
		t.forget(s)
	}
	return nil
}

// Len is how many keys are remembered, fixed ones included, for the tests.
func (t *Throttle) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.entries)
}
