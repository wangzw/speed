package static

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/internal/password"
	"github.com/vislake/speed/pkg/authn/internal/verdict"
	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/settings"
)

func cheapEntry(t *testing.T, plaintext string) string {
	t.Helper()
	encoded, err := HashPBKDF2(plaintext, 2)
	if err != nil {
		t.Fatalf("writing an entry for this case: %v", err)
	}
	return encoded
}

func tableWith(t *testing.T, subjects map[string]string) *backend {
	t.Helper()
	cfg := moduleConfig{
		Subjects:    make(map[string]string, len(subjects)),
		CacheSize:   configDefaults.CacheSize,
		Concurrency: configDefaults.Concurrency,
	}
	for subject, plaintext := range subjects {
		cfg.Subjects[subject] = cheapEntry(t, plaintext)
	}
	return backendOf(t, cfg)
}

func backendOf(t *testing.T, cfg moduleConfig) *backend {
	t.Helper()
	r, err := cfg.resolve()
	if err != nil {
		t.Fatalf("resolving the configuration for this case: %v", err)
	}
	b := newBackend(r)
	b.logger = slog.New(slog.DiscardHandler)
	return b
}

func basic(user, plaintext string) authn.Credential {
	return authn.Credential{
		Scheme: "basic",
		Value:  base64.StdEncoding.EncodeToString([]byte(user + ":" + plaintext)),
	}
}

func outcome(v authn.Verdict) verdict.Outcome { return verdict.OutcomeOf(v) }
func cause(v authn.Verdict) error             { return verdict.CauseOf(v) }

type fakeSettings struct {
	mu     sync.Mutex
	values map[string]any
	reads  map[string]int
	err    error
}

func newFakeSettings() *fakeSettings {
	return &fakeSettings{values: map[string]any{}, reads: map[string]int{}}
}

func (f *fakeSettings) set(key string, v any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.values[key] = v
}

func (f *fakeSettings) Get(_ settings.Layer, key string, target any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads[key]++
	if f.err != nil {
		return f.err
	}
	v, ok := f.values[key]
	if !ok {
		return settings.ErrUnknownKey
	}
	reflect.ValueOf(target).Elem().Set(reflect.ValueOf(v))
	return nil
}

func (f *fakeSettings) Set(context.Context, settings.Write) error { return nil }
func (f *fakeSettings) Delete(context.Context, settings.Layer, string, string) error {
	return nil
}

func (f *fakeSettings) List(context.Context, settings.Layer) ([]settings.Entry, error) {
	return nil, nil
}
func (f *fakeSettings) Declared() []settings.DeclaredItem { return nil }

func (f *fakeSettings) WrittenAt(settings.Layer, string) (time.Time, bool, error) {
	return time.Time{}, false, nil
}

func (f *fakeSettings) Read(key string, reads ...settings.LayerRead) ([]settings.LayerReadResult, error) {
	results := make([]settings.LayerReadResult, len(reads))
	for i, read := range reads {
		if err := f.Get(read.Layer, key, read.Target); err != nil {
			return nil, err
		}
		results[i] = settings.LayerReadResult{}
	}
	return results, nil
}

func counting(b *backend) *atomic.Int64 {
	var n atomic.Int64
	inner := b.check
	b.check = func(ctx context.Context, e password.Entry, plaintext string, wait time.Duration) (bool, error) {
		n.Add(1)
		return inner(ctx, e, plaintext, wait)
	}
	return &n
}

type handClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *handClock { return &handClock{t: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)} }

func (c *handClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *handClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// TestTheRightPasswordAuthenticatesAndTheWrongOneDoesNot holds what this backend
// is for, and holds the refusal apart from the pass.
func TestTheRightPasswordAuthenticatesAndTheWrongOneDoesNot(t *testing.T) {
	b := tableWith(t, map[string]string{"alice": "correct horse"})

	t.Run("the password in the table", func(t *testing.T) {
		before := time.Now()
		got := b.Verify(context.Background(), basic("alice", "correct horse"))
		if outcome(got) != verdict.Authenticated {
			t.Fatalf("the verdict reads %v, want authenticated: %v", outcome(got), cause(got))
		}
		p := verdict.PrincipalOf(got)
		if p.Account != "static.alice" {
			t.Errorf("the verdict carries the account %q, want %q: the static prefix and the subject",
				p.Account, "static.alice")
		}
		if len(p.Methods) != 1 || p.Methods[0] != authn.MethodStatic {
			t.Errorf("the verdict carries the methods %v, want [%s]", p.Methods, authn.MethodStatic)
		}
		if p.Assurance != authn.AssuranceSingle {
			t.Errorf("the verdict carries the assurance %v, want %v: a password is one factor",
				p.Assurance, authn.AssuranceSingle)
		}
		if p.Session != "" || p.Actor != "" || p.Scope != "" || p.Provider != "" || p.IDPAssured {
			t.Errorf("the verdict carries %+v, want no session, actor, scope or provider: a table "+
				"subject has none of them", p)
		}
		if p.AuthTime.Before(before.Add(-time.Second)) || p.AuthTime.After(time.Now().Add(time.Second)) {
			t.Errorf("the verdict carries the authentication time %v, want about now: every request "+
				"is an authentication of its own", p.AuthTime)
		}
		if p.AuthTime.Location() != time.UTC {
			t.Errorf("the authentication time is in %v, want UTC", p.AuthTime.Location())
		}
	})

	t.Run("a password that is not", func(t *testing.T) {
		got := b.Verify(context.Background(), basic("alice", "correct hors"))
		if outcome(got) != verdict.Denied {
			t.Errorf("the verdict reads %v, want denied", outcome(got))
		}
		if p := verdict.PrincipalOf(got); p.Account != "" {
			t.Errorf("the refusal carries the account %q, want none", p.Account)
		}
	})
}

// TestTheBackendDeclaresWhatItTakesAndMayHandBack holds the reserved name, the Basic carrier and the static account namespace.
func TestTheBackendDeclaresWhatItTakesAndMayHandBack(t *testing.T) {
	b := tableWith(t, map[string]string{"alice": "a"})

	if got := b.Name(); got != "static" {
		t.Errorf("the backend is named %q, want %q: the gateway lets the static prefix be declared "+
			"by that name alone", got, "static")
	}
	want := authn.Accepts{Schemes: []string{"basic"}, Accounts: authn.StaticPrefix}
	if got := b.Accepts(); !reflect.DeepEqual(got, want) {
		t.Errorf("the backend declares %+v, want %+v", got, want)
	}
	if len(b.Accepts().Cookies) != 0 || b.Accepts().Impersonation || b.Accepts().SSO {
		t.Errorf("the backend declares a cookie, impersonation or SSO, which a password table "+
			"has none of: %+v", b.Accepts())
	}
}

// TestEveryNoThisBackendGivesIsAboutTheCredential holds that a refusal is never an outage.
func TestEveryNoThisBackendGivesIsAboutTheCredential(t *testing.T) {
	b := tableWith(t, map[string]string{"alice": "secret"})

	for _, c := range []struct {
		name       string
		credential authn.Credential
	}{
		{"a scheme this backend does not take", authn.Credential{Scheme: "bearer", Value: "a.token"}},
		{"a cookie credential", authn.Credential{Cookie: "sid", Value: "x"}},
		{"the scheme in capitals", authn.Credential{Scheme: "Basic", Value: basic("alice", "secret").Value}},
		{"a credential that is not base64", authn.Credential{Scheme: "basic", Value: "not base64!"}},
		{"a credential with no colon in it", authn.Credential{Scheme: "basic", Value: base64.StdEncoding.EncodeToString([]byte("alice"))}},
		{"a credential with no user", basic("", "secret")},
		{"a name that is not in the table", basic("mallory", "secret")},
		{"the name in another case", basic("ALICE", "secret")},
		{"the wrong password", basic("alice", "wrong")},
		{"the empty password", basic("alice", "")},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := b.Verify(context.Background(), c.credential); outcome(got) != verdict.Denied {
				t.Errorf("the verdict reads %v, want denied: %v", outcome(got), cause(got))
			}
		})
	}
}

// TestThePasswordMayContainColons holds the split at the first colon: the user
// name cannot carry one, the password can.
func TestThePasswordMayContainColons(t *testing.T) {
	b := tableWith(t, map[string]string{"alice": "a:b:c"})

	if got := b.Verify(context.Background(), basic("alice", "a:b:c")); outcome(got) != verdict.Authenticated {
		t.Errorf("a password with colons reads %v, want authenticated", outcome(got))
	}
	if got := b.Verify(context.Background(), basic("alice", "b:c")); outcome(got) != verdict.Denied {
		t.Errorf("a password cut at the second colon reads %v, want denied", outcome(got))
	}
}

// TestACredentialIsNotReadAgainstAnotherNamesEntry holds that the user name picks the one entry the password is derived against.
func TestACredentialIsNotReadAgainstAnotherNamesEntry(t *testing.T) {
	b := tableWith(t, map[string]string{"alice": "same", "bob": "same"})

	for _, user := range []string{"alice", "bob"} {
		got := b.Verify(context.Background(), basic(user, "same"))
		if outcome(got) != verdict.Authenticated {
			t.Fatalf("%s reads %v", user, outcome(got))
		}
		if want := authn.AccountID("static." + user); verdict.PrincipalOf(got).Account != want {
			t.Errorf("the credential of %s authenticated %q, want %q", user, verdict.PrincipalOf(got).Account, want)
		}
	}
}

// TestACancelledCallIsAnOutageAndDerivesNothing holds that an expired call spends no derivation and answers unavailable.
func TestACancelledCallIsAnOutageAndDerivesNothing(t *testing.T) {
	b := tableWith(t, map[string]string{"alice": "correct horse"})
	derived := counting(b)

	if got := b.Verify(context.Background(), basic("alice", "correct horse")); outcome(got) != verdict.Authenticated {
		t.Fatalf("the first check reads %v", outcome(got))
	}
	if derived.Load() != 1 {
		t.Fatalf("the first check derived %d times, want 1", derived.Load())
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, stop := context.WithTimeout(context.Background(), 0)
	defer stop()

	for name, ctx := range map[string]context.Context{"cancelled": cancelled, "expired": expired} {
		for _, user := range []string{"alice"} {
			t.Run(name, func(t *testing.T) {
				for _, plaintext := range []string{"correct horse", "wrong"} {
					got := b.Verify(ctx, basic(user, plaintext))
					if outcome(got) != verdict.Unavailable {
						t.Errorf("the verdict for %q reads %v, want unavailable: the call's time was gone "+
							"before the password was checked", plaintext, outcome(got))
					}
					if !errors.Is(cause(got), ctx.Err()) {
						t.Errorf("the verdict carries %v, want the context's own error", cause(got))
					}
				}
			})
		}
	}
	if derived.Load() != 1 {
		t.Errorf("a call that had already ended derived: %d derivations in all, want the 1 of the first check", derived.Load())
	}
}

// TestAnEndedContextIsNotAFailedAttempt holds that a cancelled check counts against no name.
func TestAnEndedContextIsNotAFailedAttempt(t *testing.T) {
	b := tableWith(t, map[string]string{"alice": "secret"})
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	for _, user := range []string{"alice", "mallory"} {
		for range 20 {
			if got := b.Verify(cancelled, basic(user, "wrong")); outcome(got) != verdict.Unavailable {
				t.Fatalf("a cancelled call for %q reads %v, want unavailable", user, outcome(got))
			}
		}
	}
	if got := b.Verify(context.Background(), basic("alice", "secret")); outcome(got) != verdict.Authenticated {
		t.Errorf("after twenty cancelled calls the right password reads %v, want authenticated: "+
			"cancelled calls were counted as failures", outcome(got))
	}
	for range 6 {
		b.Verify(context.Background(), basic("mallory", "wrong"))
	}
	if got := b.Verify(context.Background(), basic("mallory", "wrong")); !errors.Is(cause(got), authn.ErrThrottled) {
		t.Errorf("six real failures of an unknown name read %v (%v), want it held back: the cancelled calls "+
			"were not what held it", outcome(got), cause(got))
	}
}

// TestACapacityLimitIsAnOutageThatSaysSo holds unavailable with the Retry-After sentinel.
func TestACapacityLimitIsAnOutageThatSaysSo(t *testing.T) {
	b := tableWith(t, map[string]string{"alice": "secret"})
	real := b.check
	b.check = func(context.Context, password.Entry, string, time.Duration) (bool, error) {
		return false, authn.ErrBusy
	}

	for range 20 {
		got := b.Verify(context.Background(), basic("alice", "secret"))
		if outcome(got) != verdict.Unavailable {
			t.Fatalf("a derivation refused for want of capacity reads %v, want unavailable", outcome(got))
		}
		if !errors.Is(cause(got), authn.ErrBusy) {
			t.Fatalf("the verdict carries %v, want one wrapping %v", cause(got), authn.ErrBusy)
		}
	}
	b.check = real
	if got := b.Verify(context.Background(), basic("alice", "secret")); outcome(got) != verdict.Authenticated {
		t.Errorf("after twenty refusals for want of capacity the right password reads %v, want "+
			"authenticated: they were counted as failures", outcome(got))
	}
}

// TestAnAskForCapacityIsAskedForWithNoWait holds what the backend asks of
// the Governor: no wait.
func TestAnAskForCapacityIsAskedForWithNoWait(t *testing.T) {
	b := tableWith(t, map[string]string{"alice": "secret"})
	var asked []time.Duration
	real := b.check
	b.check = func(ctx context.Context, e password.Entry, plaintext string, wait time.Duration) (bool, error) {
		asked = append(asked, wait)
		return real(ctx, e, plaintext, wait)
	}

	b.Verify(context.Background(), basic("alice", "secret"))

	if len(asked) != 1 || asked[0] != 0 {
		t.Errorf("the backend asked for capacity with the waits %v, want one ask with no wait", asked)
	}
}

// TestAPasswordThatHeldIsNotDerivedAgainWhileItIsRemembered holds that a remembered pass derives nothing and a refusal is never remembered.
func TestAPasswordThatHeldIsNotDerivedAgainWhileItIsRemembered(t *testing.T) {
	b := tableWith(t, map[string]string{"alice": "secret"})
	derived := counting(b)

	for range 3 {
		if got := b.Verify(context.Background(), basic("alice", "secret")); outcome(got) != verdict.Authenticated {
			t.Fatalf("the right password reads %v", outcome(got))
		}
	}
	if derived.Load() != 1 {
		t.Errorf("three checks of one password derived %d times, want once", derived.Load())
	}

	for range 3 {
		if got := b.Verify(context.Background(), basic("alice", "wrong")); outcome(got) != verdict.Denied {
			t.Fatalf("the wrong password reads %v", outcome(got))
		}
	}
	if derived.Load() != 4 {
		t.Errorf("three checks of a wrong password brought the derivations to %d, want 4: a refusal "+
			"is derived every time", derived.Load())
	}
}

// TestTheCacheDoesNotLetOneSubjectsPasswordStandForAnothers holds that a password held for one subject does not serve another.
func TestTheCacheDoesNotLetOneSubjectsPasswordStandForAnothers(t *testing.T) {
	b := tableWith(t, map[string]string{"alice": "alices", "bob": "bobs"})

	if got := b.Verify(context.Background(), basic("alice", "alices")); outcome(got) != verdict.Authenticated {
		t.Fatalf("alice's password reads %v", outcome(got))
	}
	if got := b.Verify(context.Background(), basic("bob", "alices")); outcome(got) != verdict.Denied {
		t.Errorf("alice's password, which is remembered, reads %v for bob, want denied", outcome(got))
	}
}

// TestTheCacheLifetimeIsASettingAndZeroSwitchesItOff holds the sixty-second default, a followed value, zero off and an out-of-domain default.
func TestTheCacheLifetimeIsASettingAndZeroSwitchesItOff(t *testing.T) {
	store := newFakeSettings()
	b := tableWith(t, map[string]string{"alice": "secret"})
	b.store = store
	derived := counting(b)
	check := func() { b.Verify(context.Background(), basic("alice", "secret")) }

	check()
	check()
	if derived.Load() != 1 {
		t.Errorf("with no setting stored the cache derived %d times in two checks, want once", derived.Load())
	}

	store.set(cacheTTLPath, time.Duration(0))
	check()
	check()
	if derived.Load() != 3 {
		t.Errorf("with the lifetime at zero two checks brought the derivations to %d, want 3", derived.Load())
	}

	for _, bad := range []time.Duration{-time.Second, password.MaxCacheTTL + time.Second} {
		store.set(cacheTTLPath, bad)
		before := derived.Load()
		check()
		check()
		if got := derived.Load() - before; got > 1 {
			t.Errorf("a lifetime of %s (outside the domain) derived %d times in two checks, want the "+
				"default's behaviour of at most once", bad, got)
		}
	}

	// A store that cannot be read is the declared default too.
	store.set(cacheTTLPath, time.Duration(0))
	store.err = errors.New("the store is not loaded")
	before := derived.Load()
	check()
	check()
	if got := derived.Load() - before; got > 1 {
		t.Errorf("an unreadable store derived %d times in two checks, want the default's behaviour", got)
	}
}

// TestAFailedNameIsHeldBackAndTheCheckIsNotMade holds that five failures are free, the sixth is made, and its failure locks the name.
func TestAFailedNameIsHeldBackAndTheCheckIsNotMade(t *testing.T) {
	b := tableWith(t, map[string]string{"alice": "secret"})
	clk := newClock()
	b.throttle = password.NewThrottle(b.throttleParams, clk.now)
	derived := counting(b)

	for i := 1; i <= 5; i++ {
		if got := b.Verify(context.Background(), basic("alice", "wrong")); outcome(got) != verdict.Denied {
			t.Fatalf("attempt %d reads %v", i, outcome(got))
		}
	}
	if derived.Load() != 5 {
		t.Fatalf("five free attempts derived %d times, want 5", derived.Load())
	}
	if got := b.Verify(context.Background(), basic("alice", "wrong")); outcome(got) != verdict.Denied {
		t.Fatalf("the sixth attempt reads %v", outcome(got))
	}
	if derived.Load() != 6 {
		t.Fatalf("the sixth attempt derived %d times in all, want 6", derived.Load())
	}

	got := b.Verify(context.Background(), basic("alice", "secret"))
	if outcome(got) != verdict.Denied {
		t.Fatalf("the right password during the back-off reads %v, want denied", outcome(got))
	}
	var held *authn.ThrottledError
	if !errors.As(cause(got), &held) || !errors.Is(cause(got), authn.ErrThrottled) {
		t.Fatalf("the refusal carries %v, want a ThrottledError", cause(got))
	}
	if held.RetryAfter != 2*time.Second {
		t.Errorf("the refusal says to wait %s, want 2s", held.RetryAfter)
	}
	if derived.Load() != 6 {
		t.Errorf("a name being held back was checked: %d derivations, want 6", derived.Load())
	}

	clk.advance(2 * time.Second)
	if passed := b.Verify(context.Background(), basic("alice", "secret")); outcome(passed) != verdict.Authenticated {
		t.Errorf("the right password after the back-off reads %v, want authenticated: %v", outcome(passed), cause(passed))
	}
	if failed := b.Verify(context.Background(), basic("alice", "wrong")); outcome(failed) != verdict.Denied {
		t.Fatalf("a failure after the pass reads %v, want denied", outcome(failed))
	}
	got = b.Verify(context.Background(), basic("alice", "secret"))
	if !errors.As(cause(got), &held) {
		t.Fatalf("the right password after the seventh failure reads %v (%v), want it held back: the pass "+
			"cleared the count", outcome(got), cause(got))
	}
	if held.RetryAfter != 4*time.Second {
		t.Errorf("the refusal says to wait %s, want 4s", held.RetryAfter)
	}
}

// TestTheBackoffDoesNotDependOnTheNameBeingInTheTable holds that present and absent names lock after the same failures.
func TestTheBackoffDoesNotDependOnTheNameBeingInTheTable(t *testing.T) {
	b := tableWith(t, map[string]string{"alice": "secret"})
	b.throttle = password.NewThrottle(b.throttleParams, newClock().now)

	for _, user := range []string{"alice", "ghost"} {
		for i := 1; i <= 6; i++ {
			b.Verify(context.Background(), basic(user, "wrong"))
		}
		got := b.Verify(context.Background(), basic(user, "secret"))
		if !errors.Is(cause(got), authn.ErrThrottled) {
			t.Errorf("after six failures the name %q reads %v (%v), want it held back like any other",
				user, outcome(got), cause(got))
		}
	}
}

// TestOneNamesBackoffIsNotAnothers holds the key: the failures of one name do not
// hold back the next.
func TestOneNamesBackoffIsNotAnothers(t *testing.T) {
	b := tableWith(t, map[string]string{"alice": "a", "bob": "b"})
	b.throttle = password.NewThrottle(b.throttleParams, newClock().now)

	for range 10 {
		b.Verify(context.Background(), basic("alice", "wrong"))
	}
	if got := b.Verify(context.Background(), basic("bob", "b")); outcome(got) != verdict.Authenticated {
		t.Errorf("bob reads %v after alice's failures, want authenticated", outcome(got))
	}
}

// TestTheBackoffParametersAreSettings holds the free attempts, window and cap, with an out-of-domain or absent store reading the default.
func TestTheBackoffParametersAreSettings(t *testing.T) {
	t.Run("free attempts", func(t *testing.T) {
		store := newFakeSettings()
		store.set(freeAttemptsPath, 1)
		b := tableWith(t, map[string]string{"alice": "secret"})
		b.store = store
		b.throttle = password.NewThrottle(b.throttleParams, newClock().now)

		b.Verify(context.Background(), basic("alice", "wrong"))
		b.Verify(context.Background(), basic("alice", "wrong"))
		if got := b.Verify(context.Background(), basic("alice", "secret")); !errors.Is(cause(got), authn.ErrThrottled) {
			t.Errorf("with one free attempt, the third attempt reads %v (%v), want held back", outcome(got), cause(got))
		}
	})

	t.Run("the cap", func(t *testing.T) {
		store := newFakeSettings()
		store.set(maxBackoffPath, 10*time.Second)
		b := tableWith(t, map[string]string{"alice": "secret"})
		b.store = store
		clk := newClock()
		b.throttle = password.NewThrottle(b.throttleParams, clk.now)

		for range 40 {
			b.Verify(context.Background(), basic("alice", "wrong"))
			clk.advance(10 * time.Second)
		}
		b.Verify(context.Background(), basic("alice", "wrong"))
		got := b.Verify(context.Background(), basic("alice", "secret"))
		var held *authn.ThrottledError
		if !errors.As(cause(got), &held) {
			t.Fatalf("after forty failures the name reads %v (%v)", outcome(got), cause(got))
		}
		if held.RetryAfter != 10*time.Second {
			t.Errorf("the wait is %s, want the cap of 10s the setting states", held.RetryAfter)
		}
	})

	t.Run("the window", func(t *testing.T) {
		store := newFakeSettings()
		store.set(failWindowPath, 2*time.Minute)
		b := tableWith(t, map[string]string{"alice": "secret"})
		b.store = store
		clk := newClock()
		b.throttle = password.NewThrottle(b.throttleParams, clk.now)

		for range 12 {
			b.Verify(context.Background(), basic("alice", "wrong"))
		}
		clk.advance(2 * time.Minute)
		if got := b.Verify(context.Background(), basic("alice", "secret")); outcome(got) != verdict.Authenticated {
			t.Errorf("after the window of two minutes the name reads %v (%v), want it forgotten",
				outcome(got), cause(got))
		}
	})

	t.Run("a value outside its domain is the default", func(t *testing.T) {
		store := newFakeSettings()
		store.set(freeAttemptsPath, 1000)
		store.set(failWindowPath, time.Second)
		store.set(maxBackoffPath, time.Nanosecond)
		b := tableWith(t, map[string]string{"alice": "secret"})
		b.store = store

		want := password.DefaultThrottleParams()
		if got := b.throttleParams(); got != want {
			t.Errorf("parameters outside their domains read as %+v, want the defaults %+v", got, want)
		}
	})

	t.Run("no store is the default", func(t *testing.T) {
		b := tableWith(t, map[string]string{"alice": "secret"})
		if got := b.throttleParams(); got != password.DefaultThrottleParams() {
			t.Errorf("with no store the parameters read as %+v", got)
		}
		if got := b.cacheTTL(); got != 60*time.Second {
			t.Errorf("with no store the cache lifetime reads as %s, want 60s", got)
		}
	})
}

// TestTheDeclaredSettingsAreTheDesignsAndTheirDomainsHold holds the declared defaults and domains that readers and declarations lean on.
func TestTheDeclaredSettingsAreTheDesignsAndTheirDomainsHold(t *testing.T) {
	if defaults.CacheTTL != time.Minute || defaults.FreeAttempts != 5 ||
		defaults.FailWindow != 15*time.Minute || defaults.MaxBackoff != 15*time.Minute {
		t.Errorf("the declared defaults are %+v, want 60s, 5, 15m and 15m", defaults)
	}
	for _, c := range []struct {
		name string
		in   func() bool
		out  func() bool
	}{
		{
			"cache-ttl", func() bool { return cacheTTLInDomain(0) && cacheTTLInDomain(5*time.Minute) },
			func() bool { return cacheTTLInDomain(-1) || cacheTTLInDomain(5*time.Minute+1) },
		},
		{
			"free-attempts", func() bool { return freeAttemptsInDomain(1) && freeAttemptsInDomain(20) },
			func() bool { return freeAttemptsInDomain(0) || freeAttemptsInDomain(21) },
		},
		{
			"fail-window", func() bool { return failWindowInDomain(time.Minute) && failWindowInDomain(24*time.Hour) },
			func() bool { return failWindowInDomain(time.Minute-1) || failWindowInDomain(24*time.Hour+1) },
		},
		{
			"max-backoff", func() bool { return maxBackoffInDomain(10*time.Second) && maxBackoffInDomain(24*time.Hour) },
			func() bool { return maxBackoffInDomain(10*time.Second-1) || maxBackoffInDomain(24*time.Hour+1) },
		},
	} {
		if !c.in() {
			t.Errorf("%s: the edges of the domain are refused", c.name)
		}
		if c.out() {
			t.Errorf("%s: a value just outside the domain is admitted", c.name)
		}
	}
	if !strings.HasPrefix(cacheTTLPath, "authn.static.") {
		t.Errorf("the setting path is %q, want it under authn.static", cacheTTLPath)
	}
}

// TestALegacyEntryStillAuthenticates holds that the old entry form is read at startup and verifies its password.
func TestALegacyEntryStillAuthenticates(t *testing.T) {
	phc := cheapEntry(t, "secret")
	fields := strings.Split(phc, "$") // "", "pbkdf2-sha256", "i=2,l=32", salt, hash
	legacy := "pbkdf2-sha256$2$" + fields[3] + "$" + fields[4]

	b := backendOf(t, moduleConfig{
		Subjects: map[string]string{"alice": legacy}, CacheSize: 16, Concurrency: 2,
	})
	if got := b.Verify(context.Background(), basic("alice", "secret")); outcome(got) != verdict.Authenticated {
		t.Errorf("a legacy entry reads %v (%v), want authenticated", outcome(got), cause(got))
	}
	if got := b.Verify(context.Background(), basic("alice", "wrong")); outcome(got) != verdict.Denied {
		t.Errorf("a legacy entry with the wrong password reads %v", outcome(got))
	}
}

// TestEntriesOfAllThreeFormsShareOneTable holds that each entry form verifies its own password in one table.
func TestEntriesOfAllThreeFormsShareOneTable(t *testing.T) {
	argon, err := Hash("argon-secret")
	if err != nil {
		t.Fatal(err)
	}
	phc := cheapEntry(t, "pbkdf-secret")
	fields := strings.Split(cheapEntry(t, "legacy-secret"), "$")
	b := backendOf(t, moduleConfig{
		Subjects: map[string]string{
			"argon": argon, "pbkdf": phc,
			"legacy": "pbkdf2-sha256$2$" + fields[3] + "$" + fields[4],
		},
		CacheSize: 16, Concurrency: 2,
	})
	for user, plaintext := range map[string]string{
		"argon": "argon-secret", "pbkdf": "pbkdf-secret", "legacy": "legacy-secret",
	} {
		if got := b.Verify(context.Background(), basic(user, plaintext)); outcome(got) != verdict.Authenticated {
			t.Errorf("%s reads %v (%v), want authenticated", user, outcome(got), cause(got))
		}
		if got := b.Verify(context.Background(), basic(user, plaintext+"x")); outcome(got) != verdict.Denied {
			t.Errorf("%s with a wrong password reads %v", user, outcome(got))
		}
	}
}

type capture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *capture) logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(syncWriter{c}, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

type syncWriter struct{ c *capture }

func (w syncWriter) Write(p []byte) (int, error) {
	w.c.mu.Lock()
	defer w.c.mu.Unlock()
	return w.c.buf.Write(p)
}

func (c *capture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

// TestTheStartupNamesTheEntriesThatAreOldOrWeak holds that each old or weak entry is named once and a sound one is not.
func TestTheStartupNamesTheEntriesThatAreOldOrWeak(t *testing.T) {
	sound, err := Hash("sound")
	if err != nil {
		t.Fatal(err)
	}
	weak := cheapEntry(t, "weak")
	fields := strings.Split(cheapEntry(t, "old"), "$")
	old := "pbkdf2-sha256$600000$" + fields[3] + "$" + fields[4]

	var out capture
	b := backendOf(t, moduleConfig{
		Subjects:  map[string]string{"sound": sound, "weak": weak, "old": old},
		CacheSize: 16, Concurrency: 2,
	})
	b.logger = out.logger()
	if err := b.wire(core.New()); err != nil {
		t.Fatalf("wiring: %v", err)
	}

	text := out.String()
	for _, want := range []string{"entry=weak", "entry=old"} {
		if !strings.Contains(text, want) {
			t.Errorf("the startup does not name %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "entry=sound") {
		t.Errorf("the startup names a sound entry:\n%s", text)
	}
	if strings.Count(text, "old form") != 1 {
		t.Errorf("the old-form entry is named %d times, want once:\n%s", strings.Count(text, "old form"), text)
	}
	if strings.Count(text, "below the least") != 1 {
		t.Errorf("the weak entry is named %d times, want once:\n%s", strings.Count(text, "below the least"), text)
	}
	for _, secret := range []string{sound, weak, old, fields[3], fields[4]} {
		if strings.Contains(text, secret) {
			t.Errorf("the startup log carries an entry or part of one:\n%s", text)
		}
	}
}

// TestTheLinesAboutTheTableNameAnEntryAndNeverASubject holds the
// attribute the table's own log lines carry.
func TestTheLinesAboutTheTableNameAnEntryAndNeverASubject(t *testing.T) {
	var out capture
	b := tableWith(t, map[string]string{"alice": "secret", "Admin": "upper", "admin": "lower"})
	b.logger = out.logger()
	if err := b.wire(core.New()); err != nil {
		t.Fatalf("wiring: %v", err)
	}
	b.throttle = password.NewThrottle(b.throttleParams, newClock().now)
	for range 10 {
		b.Verify(context.Background(), basic("typed-by-a-caller", "wrong"))
	}
	typed := out.String()
	for range 10 {
		b.Verify(context.Background(), basic("alice", "wrong"))
	}
	text := out.String()

	for _, line := range strings.Split(typed, "\n") {
		if strings.Contains(line, "held back") && !strings.Contains(line, "level=INFO") {
			t.Errorf("a name held back is written as something other than Info, which is what a problem on the caller's side is:\n%s", line)
		}
	}
	if !strings.Contains(typed, "held back") || strings.Contains(typed, "typed-by-a-caller") {
		t.Errorf("the line for a name the table does not hold is missing or writes the name:\n%s", typed)
	}
	var heldBack []string
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "held back") {
			heldBack = append(heldBack, line)
		}
	}
	if !slices.ContainsFunc(heldBack, func(line string) bool { return strings.Contains(line, "entry=alice") }) {
		t.Errorf("no line about a held-back name carries entry=alice:\n%s", text)
	}
	if !strings.Contains(text, "entries=Admin,admin") {
		t.Errorf("the line about names that share a back-off does not carry entries=Admin,admin:\n%s", text)
	}
	if strings.Contains(text, "subject=") || strings.Contains(text, "subjects=") {
		t.Errorf("a line about the table carries a field called subject:\n%s", text)
	}
}

// TestNamesThatDifferOnlyInCaseAreNamedAtStartupAndShareTheBackoff holds the case-folded back-off key and the startup naming.
func TestNamesThatDifferOnlyInCaseAreNamedAtStartupAndShareTheBackoff(t *testing.T) {
	var out capture
	b := tableWith(t, map[string]string{"Admin": "upper", "admin": "lower", "bob": "bob-secret"})
	b.logger = out.logger()
	if err := b.wire(core.New()); err != nil {
		t.Fatalf("wiring: %v", err)
	}

	text := out.String()
	if strings.Count(text, "share one back-off") != 1 {
		t.Errorf("the startup names the shared back-off %d times, want once:\n%s",
			strings.Count(text, "share one back-off"), text)
	}
	if !strings.Contains(text, "entries=Admin,admin") {
		t.Errorf("the startup does not name the two entries that share a back-off:\n%s", text)
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "share one back-off") && strings.Contains(line, "bob") {
			t.Errorf("the line about the shared back-off names a subject that has no twin:\n%s", line)
		}
	}

	b.throttle = password.NewThrottle(b.throttleParams, newClock().now)
	for range 6 {
		b.Verify(context.Background(), basic("Admin", "wrong"))
	}
	if got := b.Verify(context.Background(), basic("admin", "lower")); !errors.Is(cause(got), authn.ErrThrottled) {
		t.Errorf("the right password of the subject that shares the back-off reads %v (%v), want it held back",
			outcome(got), cause(got))
	}
	if got := b.Verify(context.Background(), basic("bob", "bob-secret")); outcome(got) != verdict.Authenticated {
		t.Errorf("a subject with its own back-off reads %v, want it to pass", outcome(got))
	}
}

// TestModuleIsAnUnexclusiveVerifierThatAlwaysRuns holds the non-exclusive capability and that the module never stands down.
func TestModuleIsAnUnexclusiveVerifierThatAlwaysRuns(t *testing.T) {
	m := Module()
	if m.Name != ModuleName {
		t.Errorf("the module is named %q, want %q", m.Name, ModuleName)
	}
	if len(m.Provides) != 1 || m.Provides[0].Exclusive {
		t.Errorf("the module provides %+v, want the verification capability, not claimed exclusively", m.Provides)
	}
	if m.Provides[0].Token != core.Token((*authn.Verifier)(nil)) {
		t.Errorf("the module delivers %v, want authn.Verifier", m.Provides[0].Token)
	}

	body := `{"authn":{"static":{"subjects":{"alice":"` + cheapEntry(t, "x") + `"}}}}`
	state, err := prepare(authntest.ConfigReader(t, m, body))
	if err != nil {
		t.Fatalf("preparing a good table: %v", err)
	}
	if state.State != core.StateEnabled {
		t.Errorf("the module takes the stance %v, want enabled: whether it runs is the command tree's decision", state.State)
	}
}

func load(t *testing.T, body string) (config.Reader, error) {
	t.Helper()
	locator := t.TempDir() + "/config.json"
	if err := os.WriteFile(locator, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	reg := core.New()
	reg.Register(jsonformat.Module())
	reg.Register(filesource.Module())
	reg.Register(core.Module{
		Name:      "host",
		Resources: []any{config.HostIdentity{Prefix: "SPEEDSTATICTEST", DefaultLocator: "file://" + locator}},
	})
	reg.Register(Module())
	return config.Load(t.Context(), reg, nil)
}

// TestAValueOutsideItsDomainIsTheDefaultAndIsReportedAtError holds the default, the named setting and the once-only Error.
func TestAValueOutsideItsDomainIsTheDefaultAndIsReportedAtError(t *testing.T) {
	store := newFakeSettings()
	store.set(freeAttemptsPath, 1000)
	store.set(failWindowPath, 2*time.Minute)
	store.set(maxBackoffPath, 30*time.Second)
	store.set(cacheTTLPath, 10*time.Minute)
	b := tableWith(t, map[string]string{"alice": "secret"})
	b.store = store
	var out capture
	b.logger = out.logger()

	for range 5 {
		if got := b.throttleParams(); got.FreeAttempts != 5 || got.Window != 2*time.Minute ||
			got.MaxBackoff != 30*time.Second {
			t.Fatalf("the parameters read as %+v, want the default free attempts and the stored rest", got)
		}
		if got := b.cacheTTL(); got != 60*time.Second {
			t.Fatalf("a cache lifetime of ten minutes reads as %s, want the default", got)
		}
	}
	text := out.String()
	for _, path := range []string{freeAttemptsPath, cacheTTLPath} {
		if n := strings.Count(text, "setting="+path); n != 1 {
			t.Errorf("%s is reported %d times over five reads, want once:\n%s", path, n, text)
		}
	}
	if strings.Contains(text, "setting="+failWindowPath) || strings.Contains(text, "setting="+maxBackoffPath) {
		t.Errorf("a setting inside its domain is reported:\n%s", text)
	}
	if !strings.Contains(text, "level=ERROR") || strings.Contains(text, "level=WARN") {
		t.Errorf("the report is not at Error:\n%s", text)
	}

	store.set(freeAttemptsPath, 3)
	if got := b.throttleParams().FreeAttempts; got != 3 {
		t.Fatalf("a value inside the domain reads as %d, want 3", got)
	}
	store.set(freeAttemptsPath, 1000)
	b.throttleParams()
	if n := strings.Count(out.String(), "setting="+freeAttemptsPath); n != 2 {
		t.Errorf("a fault that came back is reported %d times in all, want 2", n)
	}
}

// TestALockedNameIsRefusedEvenWhenItsPasswordIsRemembered holds that the cache saves a derivation, never a back-off.
func TestALockedNameIsRefusedEvenWhenItsPasswordIsRemembered(t *testing.T) {
	b := tableWith(t, map[string]string{"alice": "secret"})
	b.throttle = password.NewThrottle(b.throttleParams, newClock().now)
	derived := counting(b)

	if got := b.Verify(context.Background(), basic("alice", "secret")); outcome(got) != verdict.Authenticated {
		t.Fatalf("the right password reads %v", outcome(got))
	}
	for range 6 {
		b.Verify(context.Background(), basic("alice", "wrong"))
	}
	before := derived.Load()
	got := b.Verify(context.Background(), basic("alice", "secret"))
	if outcome(got) != verdict.Denied || !errors.Is(cause(got), authn.ErrThrottled) {
		t.Errorf("a remembered password for a name held back reads %v (%v), want it refused as held back",
			outcome(got), cause(got))
	}
	if derived.Load() != before {
		t.Error("the refusal derived")
	}
}

// TestALockedNameIsRefusedBeforeTheContextIsLookedAt holds the lock before the call's time check.
func TestALockedNameIsRefusedBeforeTheContextIsLookedAt(t *testing.T) {
	b := tableWith(t, map[string]string{"alice": "secret"})
	b.throttle = password.NewThrottle(b.throttleParams, newClock().now)
	for range 6 {
		b.Verify(context.Background(), basic("alice", "wrong"))
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if got := b.Verify(cancelled, basic("alice", "secret")); !errors.Is(cause(got), authn.ErrThrottled) {
		t.Errorf("an ended call for a name held back reads %v (%v), want it held back", outcome(got), cause(got))
	}
}

type leaveKey struct{}

// TestAWrongPasswordWhoseCallerLeftDuringTheDerivationIsStillCounted holds that derivations take no context and their answers still count.
func TestAWrongPasswordWhoseCallerLeftDuringTheDerivationIsStillCounted(t *testing.T) {
	b := tableWith(t, map[string]string{"alice": "secret"})
	b.throttle = password.NewThrottle(b.throttleParams, newClock().now)
	inner := b.check
	b.check = func(ctx context.Context, e password.Entry, plaintext string, wait time.Duration) (bool, error) {
		ok, err := inner(ctx, e, plaintext, wait)
		leave, _ := ctx.Value(leaveKey{}).(context.CancelFunc)
		leave()
		return ok, err
	}

	for range 6 {
		ctx, leave := context.WithCancel(context.Background())
		ctx = context.WithValue(ctx, leaveKey{}, leave)
		got := b.Verify(ctx, basic("alice", "wrong"))
		leave()
		if outcome(got) != verdict.Denied {
			t.Fatalf("a wrong password whose caller left reads %v, want denied", outcome(got))
		}
	}
	b.check = inner
	if got := b.Verify(context.Background(), basic("alice", "secret")); !errors.Is(cause(got), authn.ErrThrottled) {
		t.Errorf("after six wrong passwords whose callers left the name reads %v (%v), want it held back",
			outcome(got), cause(got))
	}
}

// TestSpellingsOfAnUnknownNameShareTheBackoff holds the normalised key across case and whitespace.
func TestSpellingsOfAnUnknownNameShareTheBackoff(t *testing.T) {
	b := tableWith(t, map[string]string{"alice": "secret"})
	b.throttle = password.NewThrottle(b.throttleParams, newClock().now)
	for _, spelling := range []string{"Ghost", "GHOST", " ghost", "ghost ", "gHoSt", "ghost"} {
		b.Verify(context.Background(), basic(spelling, "wrong"))
	}
	if got := b.Verify(context.Background(), basic("ghost", "wrong")); !errors.Is(cause(got), authn.ErrThrottled) {
		t.Errorf("six failures in six spellings read %v (%v), want one count", outcome(got), cause(got))
	}
}
