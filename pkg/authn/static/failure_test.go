package static

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/password"
	"github.com/vislake/speed/pkg/authn/internal/verdict"
)

// errNoDerivation stands for a derivation the process cannot run.
var errNoDerivation = errors.New("this process cannot derive")

// a derivation the process cannot run is a failed attempt and a refusal.
func TestADerivationTheProcessCannotRunIsAFailedAttemptAndNotAnOutage(t *testing.T) {
	b := tableWith(t, map[string]string{"alice": "secret"})
	b.throttle = password.NewThrottle(b.throttleParams, newClock().now)
	real := b.check
	b.check = func(context.Context, password.Entry, string, time.Duration) (bool, error) {
		return false, errNoDerivation
	}
	for i := range 6 {
		got := b.Verify(context.Background(), basic("alice", "secret"))
		if outcome(got) != verdict.Denied {
			t.Fatalf("attempt %d: a derivation the process cannot run reads %v, want denied", i+1, outcome(got))
		}
		if !errors.Is(cause(got), errNoDerivation) {
			t.Fatalf("attempt %d: the refusal carries %v, want the derivation's own error kept in the chain", i+1, cause(got))
		}
	}
	b.check = real
	if got := b.Verify(context.Background(), basic("alice", "secret")); !errors.Is(cause(got), authn.ErrThrottled) {
		t.Errorf("after six derivations the process could not run the name reads %v (%v), want it held back: they were not counted",
			outcome(got), cause(got))
	}
}

// when the caller has gone, an error from the derivation is the caller's.
func TestADerivationErrorWhoseCallerLeftIsAnOutageAndNotCounted(t *testing.T) {
	b := tableWith(t, map[string]string{"alice": "secret"})
	b.throttle = password.NewThrottle(b.throttleParams, newClock().now)
	real := b.check
	for range 20 {
		ctx, leave := context.WithCancel(context.Background())
		b.check = func(context.Context, password.Entry, string, time.Duration) (bool, error) {
			leave()
			return false, errors.New("this process cannot derive")
		}
		got := b.Verify(ctx, basic("alice", "secret"))
		leave()
		if outcome(got) != verdict.Unavailable || !errors.Is(cause(got), context.Canceled) {
			t.Fatalf("a derivation that failed after its caller left reads %v (%v), want unavailable with the context's error",
				outcome(got), cause(got))
		}
		// The call is not described as ended during the derivation: that
		// derivation gave no answer, and a derivation that ran to its end is the
		// one that is counted.
		if text := cause(got).Error(); !strings.Contains(text, "the call ended before a derivation gave an answer") ||
			strings.Contains(text, "during") {
			t.Fatalf("the outage reads %q, want the text that says the call ended before a derivation gave an answer", text)
		}
	}
	b.check = real
	if got := b.Verify(context.Background(), basic("alice", "secret")); outcome(got) != verdict.Authenticated {
		t.Errorf("after twenty such calls the right password reads %v (%v), want authenticated: they were counted", outcome(got), cause(got))
	}
}

// a pass served from the cache gives back its own attempt and clears nothing: the
// failures before it stand and only age.
func TestAPassServedFromTheCacheClearsNothingOfTheFailuresBeforeIt(t *testing.T) {
	b := tableWith(t, map[string]string{"alice": "secret"})
	b.throttle = password.NewThrottle(b.throttleParams, newClock().now)
	derived := counting(b)

	if got := b.Verify(context.Background(), basic("alice", "secret")); outcome(got) != verdict.Authenticated {
		t.Fatalf("the first pass reads %v", outcome(got))
	}
	for range 4 {
		b.Verify(context.Background(), basic("alice", "wrong"))
	}
	before := derived.Load()
	if got := b.Verify(context.Background(), basic("alice", "secret")); outcome(got) != verdict.Authenticated {
		t.Fatalf("the remembered pass reads %v (%v)", outcome(got), cause(got))
	}
	if derived.Load() != before {
		t.Fatalf("the remembered pass derived, so it was not served from the cache")
	}
	// Four failures stand. The fifth is the last free one, the sixth is made and
	// holds the name back: the pass in between took neither more nor fewer.
	for i := range 2 {
		if got := b.Verify(context.Background(), basic("alice", "wrong")); outcome(got) != verdict.Denied || errors.Is(cause(got), authn.ErrThrottled) {
			t.Fatalf("failure %d after the pass reads %v (%v), want a refusal that was checked", i+5, outcome(got), cause(got))
		}
	}
	if got := b.Verify(context.Background(), basic("alice", "secret")); !errors.Is(cause(got), authn.ErrThrottled) {
		t.Errorf("six failures with a pass between them read %v (%v) for the right password, want it held back: the pass cleared the count",
			outcome(got), cause(got))
	}
}

// the names of the table keep their back-off however many other names are
// sprayed, in the back-off the backend builds for itself.
func TestTheSubjectsOfTheTableKeepTheirBackoffUnderASprayOfNames(t *testing.T) {
	b := tableWith(t, map[string]string{"alice": "secret", "Bob": "bobs"})
	for _, subject := range []string{"alice", "Bob"} {
		for range 12 {
			if _, err := b.throttle.Fail(context.Background(), authn.ScopeIdentifier, password.ThrottleKey(subject)); err != nil {
				t.Fatal(err)
			}
		}
	}
	for i := range 2 * password.MaxThrottleKeys {
		b.Verify(context.Background(), basic("sprayed-"+strconv.Itoa(i), "x"))
	}
	for _, c := range []authn.Credential{basic("alice", "secret"), basic("Bob", "bobs"), basic("bob", "bobs")} {
		if got := b.Verify(context.Background(), c); !errors.Is(cause(got), authn.ErrThrottled) {
			t.Errorf("after a spray of names a subject of the table reads %v (%v), want it still held back", outcome(got), cause(got))
		}
	}
}

// TestTheLineThatSaysANameIsHeldBackNamesItsReasonClass holds the attribute a
// security event of the back-off carries: the single line written when a name
// passes its free attempts says reason_class=throttled, whether the name is in
// the table or not, and the lines that say nothing about a back-off do not.
func TestTheLineThatSaysANameIsHeldBackNamesItsReasonClass(t *testing.T) {
	for _, name := range []string{"alice", "typed-by-a-caller"} {
		t.Run(name, func(t *testing.T) {
			var out capture
			b := tableWith(t, map[string]string{"alice": "secret"})
			b.logger = out.logger()
			b.throttle = password.NewThrottle(b.throttleParams, newClock().now)
			for range 5 {
				b.Verify(context.Background(), basic(name, "wrong"))
			}
			if got := out.String(); strings.Contains(got, "reason_class") {
				t.Fatalf("a line carries reason_class before any back-off began:\n%s", got)
			}
			b.Verify(context.Background(), basic(name, "wrong"))

			var lines []string
			for _, line := range strings.Split(out.String(), "\n") {
				if strings.Contains(line, "held back") {
					lines = append(lines, line)
				}
			}
			if len(lines) != 1 {
				t.Fatalf("the sixth failure of %q wrote %d lines about a back-off, want one:\n%s", name, len(lines), out.String())
			}
			if !strings.Contains(lines[0], "reason_class=throttled") {
				t.Errorf("the line about a back-off does not carry reason_class=throttled:\n%s", lines[0])
			}
		})
	}
}
