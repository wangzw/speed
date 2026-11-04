package pow

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math/bits"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/settings"
)

// fakeClock is the clock a case drives, so an expiry is reached by advancing the
// time rather than by sleeping.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock() *fakeClock { return &fakeClock{now: time.Unix(1_700_000_000, 0).UTC()} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// errKV is the fault a case arms the store with.
var errKV = errors.New("the key-value store is unavailable")

// fakeKV is an in-memory kv.KV over a case's clock, with faults it can arm one
// operation at a time.
type fakeKV struct {
	mu     sync.Mutex
	clock  *fakeClock
	m      map[string][]byte
	exp    map[string]time.Time
	getErr error
	setErr error
	casErr error
	incErr error
}

func newFakeKV(clock *fakeClock) *fakeKV {
	return &fakeKV{clock: clock, m: map[string][]byte{}, exp: map[string]time.Time{}}
}

var _ interface {
	Get(context.Context, string) ([]byte, bool, error)
	Set(context.Context, string, []byte, time.Duration) error
	Delete(context.Context, string) error
	IncrByFloat(context.Context, string, float64) (float64, error)
	IncrByFloatWithTTL(context.Context, string, float64, time.Duration) (float64, error)
	CompareAndSwap(context.Context, string, []byte, []byte) (bool, error)
	SetIfAbsent(context.Context, string, []byte, time.Duration) (bool, error)
} = (*fakeKV)(nil)

func (k *fakeKV) liveLocked(key string) bool {
	e, ok := k.exp[key]
	if !ok {
		return true
	}
	if !k.clock.Now().Before(e) {
		delete(k.m, key)
		delete(k.exp, key)
		return false
	}
	return true
}

func (k *fakeKV) Get(_ context.Context, key string) ([]byte, bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.getErr != nil {
		return nil, false, k.getErr
	}
	if !k.liveLocked(key) {
		return nil, false, nil
	}
	v, ok := k.m[key]
	return append([]byte(nil), v...), ok, nil
}

func (k *fakeKV) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.setErr != nil {
		return k.setErr
	}
	k.m[key] = append([]byte(nil), value...)
	if ttl > 0 {
		k.exp[key] = k.clock.Now().Add(ttl)
	} else {
		delete(k.exp, key)
	}
	return nil
}

func (k *fakeKV) Delete(_ context.Context, key string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.m, key)
	delete(k.exp, key)
	return nil
}

func (k *fakeKV) CompareAndSwap(_ context.Context, key string, old, newVal []byte) (bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.casErr != nil {
		return false, k.casErr
	}
	live := k.liveLocked(key)
	var current []byte
	if live {
		current = k.m[key]
	}
	if string(current) != string(old) {
		return false, nil
	}
	k.m[key] = append([]byte(nil), newVal...)
	return true, nil
}

func (k *fakeKV) SetIfAbsent(_ context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.liveLocked(key) {
		return false, nil
	}
	k.m[key] = append([]byte(nil), value...)
	if ttl > 0 {
		k.exp[key] = k.clock.Now().Add(ttl)
	}
	return true, nil
}

func (k *fakeKV) IncrByFloat(ctx context.Context, key string, delta float64) (float64, error) {
	return k.IncrByFloatWithTTL(ctx, key, delta, 0)
}

func (k *fakeKV) IncrByFloatWithTTL(_ context.Context, key string, delta float64, ttl time.Duration) (float64, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.incErr != nil {
		return 0, k.incErr
	}
	var current float64
	if k.liveLocked(key) {
		if v, ok := k.m[key]; ok {
			current, _ = strconv.ParseFloat(string(v), 64)
		}
	} else {
		delete(k.exp, key)
	}
	current += delta
	k.m[key] = []byte(strconv.FormatFloat(current, 'g', -1, 64))
	if _, ok := k.exp[key]; !ok && ttl > 0 {
		k.exp[key] = k.clock.Now().Add(ttl)
	}
	return current, nil
}

// keys lists the live keys the store holds, sorted.
func (k *fakeKV) keys() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	out := make([]string, 0, len(k.m))
	for key := range k.m {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// settingsStub is the settings reader a case scripts. A zero global limit is
// outside the domain and therefore reads as the declared default.
type settingsStub struct {
	difficulty int
	ttl        time.Duration
	global     int
	err        error
}

func (s settingsStub) Get(_ settings.Layer, key string, target any) error {
	if s.err != nil {
		return s.err
	}
	switch key {
	case difficultyPath:
		p, ok := target.(*int)
		if !ok {
			return settings.ErrTypeMismatch
		}
		*p = s.difficulty
	case challengeTTLPath:
		p, ok := target.(*time.Duration)
		if !ok {
			return settings.ErrTypeMismatch
		}
		*p = s.ttl
	case globalIssueLimitPath:
		p, ok := target.(*int)
		if !ok {
			return settings.ErrTypeMismatch
		}
		*p = s.global
	default:
		return settings.ErrUnknownKey
	}
	return nil
}

// newTestProvider builds the provider over a fake store and a clock.
func newTestProvider(t *testing.T, cfg settingsStub) (*Provider, *fakeKV, *fakeClock) {
	t.Helper()
	clock := newClock()
	store := newFakeKV(clock)
	p, err := New(store, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p, store, clock
}

// hashZeros is the work a proof has to show: the hash of the decoded challenge
// and the nonce in big-endian, read as a count of leading zero bits.
func hashZeros(t *testing.T, challenge string, nonce uint64) int {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(challenge)
	if err != nil {
		t.Fatalf("decoding the challenge %q: %v", challenge, err)
	}
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], nonce)
	h := sha256.New()
	h.Write(raw)
	h.Write(encoded[:])
	sum := h.Sum(nil)
	zeros := 0
	for _, b := range sum {
		if b != 0 {
			zeros += bits.LeadingZeros8(b)
			break
		}
		zeros += 8
	}
	return zeros
}

// solve finds a nonce that reaches difficulty.
func solve(t *testing.T, challenge string, difficulty int) string {
	t.Helper()
	for nonce := uint64(0); nonce < 1<<24; nonce++ {
		if hashZeros(t, challenge, nonce) >= difficulty {
			return strconv.FormatUint(nonce, 10)
		}
	}
	t.Fatalf("no nonce under 2^24 reaches difficulty %d for the challenge %q", difficulty, challenge)
	return ""
}

// insufficient finds a nonce that falls short of difficulty.
func insufficient(t *testing.T, challenge string, difficulty int) string {
	t.Helper()
	for nonce := uint64(0); nonce < 1<<20; nonce++ {
		if hashZeros(t, challenge, nonce) < difficulty {
			return strconv.FormatUint(nonce, 10)
		}
	}
	t.Fatalf("every nonce under 2^20 reaches difficulty %d for the challenge %q", difficulty, challenge)
	return ""
}

// solveBetween finds a nonce whose work reaches atLeast but not below, so a
// case can prove that a threshold was raised.
func solveBetween(t *testing.T, challenge string, atLeast, below int) string {
	t.Helper()
	for nonce := uint64(0); nonce < 1<<24; nonce++ {
		zeros := hashZeros(t, challenge, nonce)
		if zeros >= atLeast && zeros < below {
			return strconv.FormatUint(nonce, 10)
		}
	}
	t.Fatalf("no nonce under 2^24 has %d..%d leading zero bits for the challenge %q",
		atLeast, below-1, challenge)
	return ""
}

func mustIssue(t *testing.T, p *Provider, address string) Challenge {
	t.Helper()
	c, err := p.Issue(t.Context(), IssueOptions{Scope: authn.HumanScopeOther, RemoteIP: address})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	return c
}

// storedRecord is the value a challenge key holds, encoded as Issue writes it.
func storedRecord(t *testing.T, rec record) []byte {
	t.Helper()
	encoded, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("encoding the record: %v", err)
	}
	return encoded
}

// TestACorrectNonceIsAcceptedOnce is the single-use contract: the proof that a
// challenge and its solved nonce make is accepted once, and the same proof
// presented again is refused rather than accepted twice.
func TestACorrectNonceIsAcceptedOnce(t *testing.T) {
	p, _, _ := newTestProvider(t, settingsStub{difficulty: 18, ttl: time.Minute})
	c := mustIssue(t, p, "203.0.113.7")
	proof := authn.HumanProof{Token: c.Challenge + "." + solve(t, c.Challenge, c.Difficulty), RemoteIP: "203.0.113.7"}

	if err := p.Verify(t.Context(), proof, authn.HumanScopeOther); err != nil {
		t.Fatalf("the first presentation resolved with %v, want acceptance", err)
	}
	err := p.Verify(t.Context(), proof, authn.HumanScopeOther)
	if !errors.Is(err, authn.ErrHumanVerificationRequired) {
		t.Fatalf("the second presentation resolved with %v, want ErrHumanVerificationRequired", err)
	}
}

// TestOneProofCannotPassTwiceConcurrently holds the seam's concurrency promise:
// two requests presenting one proof at the same time cannot both pass, which is
// what the compare-and-swap that spends the challenge buys.
func TestOneProofCannotPassTwiceConcurrently(t *testing.T) {
	p, _, _ := newTestProvider(t, settingsStub{difficulty: 18, ttl: time.Minute})
	c := mustIssue(t, p, "203.0.113.7")
	proof := authn.HumanProof{Token: c.Challenge + "." + solve(t, c.Challenge, c.Difficulty)}

	const racers = 8
	errs := make([]error, racers)
	var wait sync.WaitGroup
	ctx := t.Context()
	for i := range errs {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			errs[i] = p.Verify(ctx, proof, authn.HumanScopeOther)
		}(i)
	}
	wait.Wait()

	accepted := 0
	for i, err := range errs {
		switch {
		case err == nil:
			accepted++
		case errors.Is(err, authn.ErrHumanVerificationRequired):
		default:
			t.Errorf("racer %d resolved with %v, want acceptance or ErrHumanVerificationRequired", i, err)
		}
	}
	if accepted != 1 {
		t.Errorf("%d racers accepted the same proof, want exactly 1", accepted)
	}
}

// TestAShortProofIsRefused holds the difficulty: a nonce whose hash carries
// fewer leading zero bits than the challenge was issued with is refused even
// though it names a live challenge.
func TestAShortProofIsRefused(t *testing.T) {
	p, _, _ := newTestProvider(t, settingsStub{difficulty: 18, ttl: time.Minute})
	c := mustIssue(t, p, "203.0.113.7")
	proof := authn.HumanProof{Token: c.Challenge + "." + insufficient(t, c.Challenge, c.Difficulty)}

	err := p.Verify(t.Context(), proof, authn.HumanScopeOther)
	if !errors.Is(err, authn.ErrHumanVerificationRequired) {
		t.Fatalf("a short proof resolved with %v, want ErrHumanVerificationRequired", err)
	}
	if errors.Is(err, authn.ErrUnavailable) {
		t.Errorf("a short proof reads as an outage: %v", err)
	}
}

// TestAnExpiredChallengeIsRefused holds the TTL: once the store stops serving
// the challenge, a proof that names it is refused as an invalid one.
func TestAnExpiredChallengeIsRefused(t *testing.T) {
	p, _, clock := newTestProvider(t, settingsStub{difficulty: 18, ttl: time.Minute})
	c := mustIssue(t, p, "203.0.113.7")
	proof := authn.HumanProof{Token: c.Challenge + "." + solve(t, c.Challenge, c.Difficulty)}

	clock.advance(time.Minute + time.Second)
	err := p.Verify(t.Context(), proof, authn.HumanScopeOther)
	if !errors.Is(err, authn.ErrHumanVerificationRequired) {
		t.Fatalf("an expired challenge resolved with %v, want ErrHumanVerificationRequired", err)
	}
}

// TestAKVFaultAnswersUnavailable holds the outage half of the contract on every
// read and write the check performs.
func TestAKVFaultAnswersUnavailable(t *testing.T) {
	t.Run("reading the challenge", func(t *testing.T) {
		p, store, _ := newTestProvider(t, settingsStub{difficulty: 18, ttl: time.Minute})
		c := mustIssue(t, p, "203.0.113.7")
		store.getErr = errKV
		err := p.Verify(t.Context(), authn.HumanProof{Token: c.Challenge + "." + solve(t, c.Challenge, c.Difficulty)}, authn.HumanScopeOther)
		if !errors.Is(err, authn.ErrUnavailable) {
			t.Fatalf("a store fault resolved with %v, want ErrUnavailable", err)
		}
		if errors.Is(err, authn.ErrHumanVerificationRequired) {
			t.Errorf("a store fault reads as an invalid proof: %v", err)
		}
	})

	t.Run("spending the challenge", func(t *testing.T) {
		p, store, _ := newTestProvider(t, settingsStub{difficulty: 18, ttl: time.Minute})
		c := mustIssue(t, p, "203.0.113.7")
		store.casErr = errKV
		err := p.Verify(t.Context(), authn.HumanProof{Token: c.Challenge + "." + solve(t, c.Challenge, c.Difficulty)}, authn.HumanScopeOther)
		if !errors.Is(err, authn.ErrUnavailable) {
			t.Fatalf("a spend fault resolved with %v, want ErrUnavailable", err)
		}
	})

	t.Run("issuing a challenge", func(t *testing.T) {
		p, store, _ := newTestProvider(t, settingsStub{difficulty: 18, ttl: time.Minute})
		store.incErr = errKV
		if _, err := p.Issue(t.Context(), IssueOptions{Scope: authn.HumanScopeOther, RemoteIP: "203.0.113.7"}); !errors.Is(err, authn.ErrUnavailable) {
			t.Fatalf("a counter fault resolved with %v, want ErrUnavailable", err)
		}
	})

	t.Run("storing a challenge", func(t *testing.T) {
		p, store, _ := newTestProvider(t, settingsStub{difficulty: 18, ttl: time.Minute})
		store.setErr = errKV
		if _, err := p.Issue(t.Context(), IssueOptions{Scope: authn.HumanScopeOther, RemoteIP: "203.0.113.7"}); !errors.Is(err, authn.ErrUnavailable) {
			t.Fatalf("a store fault resolved with %v, want ErrUnavailable", err)
		}
	})
}

// TestAnOverLongProofIsRefusedAsHumanProofTooLong holds the 400 of the seam's
// three failures: a proof longer than the provider admits is a bad request, not
// an invalid proof and not an outage.
func TestAnOverLongProofIsRefusedAsHumanProofTooLong(t *testing.T) {
	p, _, _ := newTestProvider(t, settingsStub{difficulty: 18, ttl: time.Minute})
	err := p.Verify(t.Context(), authn.HumanProof{Token: strings.Repeat("a", maxProofLen+1)}, authn.HumanScopeOther)
	if !errors.Is(err, authn.ErrHumanProofTooLong) {
		t.Fatalf("an over-long proof resolved with %v, want ErrHumanProofTooLong", err)
	}
	if errors.Is(err, authn.ErrHumanVerificationRequired) || errors.Is(err, authn.ErrUnavailable) {
		t.Errorf("an over-long proof reads as another failure: %v", err)
	}
}

// TestAMalformedProofIsRefused holds the proof's grammar: anything that is not
// a base64url challenge, a dot and a decimal nonce is an invalid proof.
func TestAMalformedProofIsRefused(t *testing.T) {
	p, _, _ := newTestProvider(t, settingsStub{difficulty: 18, ttl: time.Minute})
	for _, token := range []string{
		"no dot at all",
		".123",
		"challenge.",
		"!!!.123",
		"abc.def",
		"abc.-1",
	} {
		t.Run(token, func(t *testing.T) {
			if err := p.Verify(t.Context(), authn.HumanProof{Token: token}, authn.HumanScopeOther); !errors.Is(err, authn.ErrHumanVerificationRequired) {
				t.Fatalf("%q resolved with %v, want ErrHumanVerificationRequired", token, err)
			}
		})
	}
}

// TestTheStoredDifficultyDecidesTheThreshold holds that the difficulty a
// challenge was issued with is the one its proof is checked against, and that a
// stored value outside the domain is refused rather than trusted.
func TestTheStoredDifficultyDecidesTheThreshold(t *testing.T) {
	t.Run("a stored value outside the domain", func(t *testing.T) {
		p, store, _ := newTestProvider(t, settingsStub{difficulty: 18, ttl: time.Minute})
		c := mustIssue(t, p, "203.0.113.7")
		key := challengeKeyFor(c.Challenge)
		if err := store.Set(t.Context(), key, storedRecord(t, record{Scope: authn.HumanScopeOther, Difficulty: 23}), time.Minute); err != nil {
			t.Fatalf("overwriting the stored difficulty: %v", err)
		}
		err := p.Verify(t.Context(), authn.HumanProof{Token: c.Challenge + "." + insufficient(t, c.Challenge, c.Difficulty)}, authn.HumanScopeOther)
		if !errors.Is(err, authn.ErrHumanVerificationRequired) {
			t.Fatalf("a stored difficulty outside the domain resolved with %v, want ErrHumanVerificationRequired", err)
		}
	})

	t.Run("a raised threshold", func(t *testing.T) {
		p, store, _ := newTestProvider(t, settingsStub{difficulty: 18, ttl: time.Minute})
		c := mustIssue(t, p, "203.0.113.7")
		key := challengeKeyFor(c.Challenge)
		if err := store.Set(t.Context(), key, storedRecord(t, record{Scope: authn.HumanScopeOther, Difficulty: 20}), time.Minute); err != nil {
			t.Fatalf("overwriting the stored difficulty: %v", err)
		}
		proof := c.Challenge + "." + solveBetween(t, c.Challenge, 18, 20)
		err := p.Verify(t.Context(), authn.HumanProof{Token: proof}, authn.HumanScopeOther)
		if !errors.Is(err, authn.ErrHumanVerificationRequired) {
			t.Fatalf("a proof below the stored difficulty resolved with %v, want ErrHumanVerificationRequired", err)
		}
	})
}

// TestIssueStoresTheChallengeUnderItsDigest holds the storage shape: the key is
// the digest of the challenge, the value is the record of the surface and the
// difficulty, and the entry carries the configured lifetime.
func TestIssueStoresTheChallengeUnderItsDigest(t *testing.T) {
	p, store, _ := newTestProvider(t, settingsStub{difficulty: 18, ttl: 2 * time.Minute})
	c := mustIssue(t, p, "203.0.113.7")

	sum := sha256.Sum256([]byte(c.Challenge))
	key := challengeKeyPrefix + hexString(sum[:])
	value, present, err := store.Get(t.Context(), key)
	if err != nil {
		t.Fatalf("reading the stored challenge: %v", err)
	}
	if !present {
		t.Fatalf("the challenge is not stored under %q; keys are %v", key, store.keys())
	}
	var rec record
	if err := json.Unmarshal(value, &rec); err != nil {
		t.Fatalf("the stored value is not a record: %v (%q)", err, value)
	}
	if rec.Scope != authn.HumanScopeOther {
		t.Errorf("the stored scope is %q, want %q", rec.Scope, authn.HumanScopeOther)
	}
	if rec.Difficulty != c.Difficulty {
		t.Errorf("the stored difficulty is %d, want the difficulty %d", rec.Difficulty, c.Difficulty)
	}
	store.mu.Lock()
	expires := store.exp[key]
	store.mu.Unlock()
	if got := expires.Sub(store.clock.Now()); got != 2*time.Minute {
		t.Errorf("the stored lifetime is %v, want 2m", got)
	}
}

// TestAProofIsBoundToItsScope holds the scope binding: a proof issued for one
// surface is refused on the other, and the refusal consumes nothing, so the
// proof is still accepted on the surface it was issued for.
func TestAProofIsBoundToItsScope(t *testing.T) {
	p, _, _ := newTestProvider(t, settingsStub{difficulty: 18, ttl: time.Minute})
	c, err := p.Issue(t.Context(), IssueOptions{Scope: authn.HumanScopePhone, RemoteIP: "203.0.113.7"})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	proof := authn.HumanProof{Token: c.Challenge + "." + solve(t, c.Challenge, c.Difficulty)}

	err = p.Verify(t.Context(), proof, authn.HumanScopeOther)
	if !errors.Is(err, authn.ErrHumanVerificationRequired) {
		t.Fatalf("a proof issued for the phone surface resolved with %v on the other surface, want ErrHumanVerificationRequired", err)
	}
	if err := p.Verify(t.Context(), proof, authn.HumanScopePhone); err != nil {
		t.Fatalf("the refused proof was no longer accepted on its own surface: %v", err)
	}
}

// TestAnUnknownScopeIsRefused holds that a challenge can only be issued for one
// of the two surfaces the gate serves.
func TestAnUnknownScopeIsRefused(t *testing.T) {
	p, _, _ := newTestProvider(t, settingsStub{difficulty: 18, ttl: time.Minute})
	if _, err := p.Issue(t.Context(), IssueOptions{Scope: authn.HumanScope("email"), RemoteIP: "203.0.113.7"}); !errors.Is(err, ErrUnknownScope) {
		t.Fatalf("an unknown scope resolved with %v, want ErrUnknownScope", err)
	}
}

// TestTheIssuanceCapBoundsOneSource holds the anti-fill bound: one address may
// hold a limited number of challenges at a time, and another address is not
// affected by it.
func TestTheIssuanceCapBoundsOneSource(t *testing.T) {
	p, _, _ := newTestProvider(t, settingsStub{difficulty: 18, ttl: time.Minute})
	for i := 0; i < issueLimit; i++ {
		if _, err := p.Issue(t.Context(), IssueOptions{Scope: authn.HumanScopeOther, RemoteIP: "203.0.113.7"}); err != nil {
			t.Fatalf("Issue %d: %v", i+1, err)
		}
	}
	if _, err := p.Issue(t.Context(), IssueOptions{Scope: authn.HumanScopeOther, RemoteIP: "203.0.113.7"}); !errors.Is(err, ErrIssuanceLimited) {
		t.Fatalf("the cap resolved with %v, want ErrIssuanceLimited", err)
	}
	if _, err := p.Issue(t.Context(), IssueOptions{Scope: authn.HumanScopeOther, RemoteIP: "203.0.113.8"}); err != nil {
		t.Fatalf("another source was refused with the first source's cap: %v", err)
	}
}

// TestTheGlobalIssuanceCapBoundsAllSources holds the whole-deployment bound: a
// set of source addresses cannot issue more challenges together than the
// configured limit, however far below their per-source allowances each stays,
// and the bound lifts when its window expires.
func TestTheGlobalIssuanceCapBoundsAllSources(t *testing.T) {
	const limit = 3
	p, _, clock := newTestProvider(t, settingsStub{difficulty: 18, ttl: time.Minute, global: limit})

	sources := []string{"203.0.113.1", "203.0.113.2", "203.0.113.3", "203.0.113.4"}
	for i, address := range sources[:limit] {
		if _, err := p.Issue(t.Context(), IssueOptions{Scope: authn.HumanScopeOther, RemoteIP: address}); err != nil {
			t.Fatalf("Issue %d from %s: %v", i+1, address, err)
		}
	}
	if _, err := p.Issue(t.Context(), IssueOptions{Scope: authn.HumanScopeOther, RemoteIP: sources[limit]}); !errors.Is(err, ErrIssuanceLimited) {
		t.Fatalf("the global cap resolved with %v, want ErrIssuanceLimited", err)
	}

	clock.advance(issueWindow + time.Second)
	if _, err := p.Issue(t.Context(), IssueOptions{Scope: authn.HumanScopeOther, RemoteIP: sources[limit]}); err != nil {
		t.Fatalf("the global counter did not expire with its window: %v", err)
	}
}

// TestTheGlobalCounterCoversTheSameWindowAsTheSourceOne holds the counter's
// expiry: after the window passes, an exhausted global counter admits issuance
// again without the process restarting.
func TestTheGlobalCounterCoversTheSameWindowAsTheSourceOne(t *testing.T) {
	const limit = 2
	p, store, clock := newTestProvider(t, settingsStub{difficulty: 18, ttl: time.Minute, global: limit})

	for i := 0; i < limit; i++ {
		address := []string{"203.0.113.1", "203.0.113.2"}[i]
		if _, err := p.Issue(t.Context(), IssueOptions{Scope: authn.HumanScopeOther, RemoteIP: address}); err != nil {
			t.Fatalf("Issue %d: %v", i+1, err)
		}
	}
	if _, present, _ := store.Get(t.Context(), globalIssueKey()); !present {
		t.Fatal("the global counter is not stored")
	}
	clock.advance(issueWindow + time.Second)
	if _, present, _ := store.Get(t.Context(), globalIssueKey()); present {
		t.Error("the global counter survived its window")
	}
}

// TestTheProviderAnswersTheSeamContract pins the three constant answers.
func TestTheProviderAnswersTheSeamContract(t *testing.T) {
	p, _, _ := newTestProvider(t, settingsStub{difficulty: 18, ttl: time.Minute})
	if !p.Required(authn.HumanScopeOther) {
		t.Error("Required is false, want true")
	}
	if got := p.Provider(authn.HumanScopeOther); got != ProviderName {
		t.Errorf("Provider = %q, want %q", got, ProviderName)
	}
	if got := p.SiteKey(authn.HumanScopeOther); got != "" {
		t.Errorf("SiteKey = %q, want none", got)
	}
}

// hexString is the lowercase hexadecimal form of raw.
func hexString(raw []byte) string {
	const digits = "0123456789abcdef"
	var b strings.Builder
	b.Grow(len(raw) * 2)
	for _, c := range raw {
		b.WriteByte(digits[c>>4])
		b.WriteByte(digits[c&0x0f])
	}
	return b.String()
}

// challengeKeyFor is the storage key a challenge is kept under.
func challengeKeyFor(challenge string) string {
	sum := sha256.Sum256([]byte(challenge))
	return challengeKeyPrefix + hexString(sum[:])
}
