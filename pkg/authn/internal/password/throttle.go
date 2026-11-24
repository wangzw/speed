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

// The domains of the back-off parameters, and what a parameter outside its
// domain is read as. A setting is held to its domain when it is written, and a
// value stored before the domain was declared, or written around the store, is
// still read here: the default is the strict side of every one of them, so a
// value that cannot be acted on falls back to the more cautious reading.
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

// delay is how long a key that has failed n times in a row is made to wait: no
// time at all for the free attempts, and after them two to the power of the
// failures beyond them, in seconds, up to the cap.
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

// MaxThrottleKeys is how many keys beyond the fixed ones the in-process back-off
// remembers at once. The table does not grow past it: a caller who can present
// any number of distinct names would otherwise grow it without bound. Within
// the bound a key is forgotten when it has gone a whole window without a
// failure, and when the table is full the key quiet longest goes first, so a
// caller who presents this many other names within a window can make the
// back-off forget a key that is not fixed, and no key that is.
const MaxThrottleKeys = 16384

// ThrottleKey is the key the in-process back-off is kept under for a subject
// name as it was presented: the SHA-256, in hex, of the name with its white
// space removed and its letters folded to lower case. Spellings that differ
// only in case or in white space share one key, and the key is the same for a
// name in a table and a name that is not.
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

// Throttle is the in-process failure back-off. It implements authn.Throttle for
// a backend that has no database to keep the state in, and for one that must
// not need it: the static table is the back door an operator uses when the
// database is down, and it does not go down with it.
//
// The state is per process. With several replicas each counts for itself, so the
// attempts one key may make before it is held back are as many as there are
// replicas times the free attempts.
//
// The keys of different scopes never meet, whatever the strings are.
//
// A backend that checks a credential on every request does not use Check,
// Fail and Succeed, which are three steps with the derivation between them and
// let however many attempts arrive together through before the first failure is
// written. It uses Reserve, which counts the attempt as a failure at the moment
// it is admitted, and settles that attempt afterwards: a pass gives back the
// one attempt it reserved and nothing else, and a failure keeps it. A pass does
// not clear the failures that came before it, which only age: a caller who
// could clear the count with one request of its own, between any two guesses,
// would never be held back. A check that cannot be a guess, because the
// password it carries is one that already held, reserves nothing and reads
// Standing instead.
//
// A fixed key is one the table holds from the start and never forgets: the keys
// of the names a backend is the authority for, which a caller spraying other
// names must not be able to push out of the table and so make free of its
// back-off. Every other key is held in a table bounded by MaxThrottleKeys and
// swept of the keys whose window has passed.
type Throttle struct {
	params func() ThrottleParams
	now    func() time.Time
	limit  int

	mu      sync.Mutex
	entries map[string]*throttleState
	// recency runs from the most recently failed at the front to the least at
	// the back, and holds the keys that are not fixed; the back is what is swept
	// or forgotten.
	recency *list.List
}

type throttleState struct {
	key string
	// failures is the count of failed attempts as of last, before any aging.
	failures int
	// last is when the latest failure was recorded.
	last time.Time
	// inflight is how many attempts hold a reservation that is not yet settled,
	// and reserved is when the latest of them was made. An attempt in flight
	// counts as a failure until it is settled as a pass.
	inflight int
	reserved time.Time
	// elem is the state's place in recency, nil for a fixed key.
	elem *list.Element
}

// NewThrottle makes the back-off. params is read on every call, so a setting
// that changes takes effect at once; a nil params reads the defaults. now reads
// the time, and nil reads the system clock. The fixed keys are held from the
// start and never forgotten.
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

// tableKey joins the scope and the key. The scope is written with its length
// first, so two pairs whose concatenations are equal are still two keys, and no
// key under one scope can be taken for another's whatever bytes either holds.
func tableKey(scope authn.ThrottleScope, key string) string {
	return strconv.Itoa(len(scope)) + ":" + string(scope) + key
}

// live is the failure count of a state at now: zero when the window has passed
// since its last failure and the wait that count earned is over. A wait longer
// than the window is served in full, so a max-backoff above the fail window is
// a longer wait and not one the window cuts short.
func (s *throttleState) live(now time.Time, p ThrottleParams) int {
	if now.Sub(s.last) >= p.Window && !now.Before(s.last.Add(p.delay(s.failures))) {
		return 0
	}
	return s.failures
}

func (s *throttleState) pending(now time.Time, p ThrottleParams) int {
	return s.live(now, p) + s.inflight
}

// wait is how long an attempt arriving at now has to wait. The wait runs from
// the latest failure, or from the latest attempt in flight when there is one,
// because an attempt in flight is a failure that has not been settled yet.
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

// sweep forgets the keys that are not fixed and whose count has gone. The list
// is in the order of the last failure or reservation, so the sweep stops at the first key whose
// count still stands; a key behind it that has gone is forgotten when that one
// does. The caller holds mu.
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

// Check reports how long the key still has to wait. Zero means it may try now.
// It reserves nothing, so it is for a caller that only asks; a caller that goes
// on to check a credential reserves with Reserve. It never returns an error:
// this state is memory.
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

// Standing is how long the failures that stand make the key wait: the same wait
// Check reports, with the attempts in flight left out. Zero means no failure
// that has been recorded holds the key back. It is for a check that is not a
// guess, a password that is remembered from a derivation that held, which
// reserves nothing and so is not held against the attempts of others, and is
// still refused while the name is being held back for what it has already done.
// It changes no state: it admits nothing and remembers no key it is asked about.
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

// Reserve is Check and the first half of recording a failure in one step. When
// the key has to wait it reports the wait and no attempt. Otherwise it counts
// the attempt as a failure from this moment, under the same lock that read the
// wait, and returns it: attempts that arrive together are held against one
// another, and the free attempts of a key are the free attempts and not the
// width of whatever runs at once. The caller settles the attempt when it knows
// the outcome.
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

// Refund gives back the attempt: the credential held, so the attempt was not a
// failure. It gives back this attempt's own reservation and nothing of the
// failures that stood before it, and the failures that stood are not touched.
// An attempt that is not a guess at all, because the call ended before it could
// check, is given back the same way.
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

// enter makes a state for a key that is not fixed and is not in the table, and
// makes room for it first: when the table is full the key quiet longest goes.
// The caller holds mu.
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
