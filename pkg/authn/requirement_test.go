package authn

import (
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn/internal/identity"
	"github.com/vislake/speed/pkg/authn/internal/model"
)

var requirementNow = time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)

func identityAt(assurance Assurance, age time.Duration, idpAssured bool) Identity {
	p := Principal{
		Account:    "acct",
		Methods:    []Method{MethodPassword},
		Assurance:  assurance,
		IDPAssured: idpAssured,
		AuthTime:   requirementNow.Add(-age),
	}
	if idpAssured {
		p.Methods = []Method{MethodOIDC}
		p.Provider = "0198f0aa-0000-7000-8000-000000000001"
	}
	return identity.New(p, "b", "Bearer")
}

// TestFreshFirstFactorIsASingleFactorWithinFiveMinutes holds the one place
// the five minutes and the absence of the local flag are written.
func TestFreshFirstFactorIsASingleFactorWithinFiveMinutes(t *testing.T) {
	r := FreshFirstFactor()
	if r.Assurance != AssuranceSingle || r.MaxAge != 5*time.Minute || r.Local {
		t.Fatalf("FreshFirstFactor() = %+v, want a single factor, five minutes, not local", r)
	}
	if again := FreshFirstFactor(); again != r {
		t.Errorf("FreshFirstFactor() is %+v once and %+v the next time", r, again)
	}

	for _, c := range []struct {
		name string
		id   Identity
		want bool
	}{
		{"a single factor proven now", identityAt(AssuranceSingle, 0, false), true},
		{"a single factor four minutes ago", identityAt(AssuranceSingle, 4*time.Minute, false), true},
		{"a single factor exactly five minutes ago", identityAt(AssuranceSingle, 5*time.Minute, false), true},
		{"a single factor five minutes and a second ago", identityAt(AssuranceSingle, 5*time.Minute+time.Second, false), false},
		{"a single factor an hour ago", identityAt(AssuranceSingle, time.Hour, false), false},
		{"multi-factor proven now", identityAt(AssuranceMulti, 0, false), true},
		{"an IdP's multi-factor is accepted, the requirement is not local", identityAt(AssuranceMulti, time.Minute, true), true},
		{"no identity at all", Identity{}, false},
	} {
		if got := c.id.Satisfies(r, requirementNow); got != c.want {
			t.Errorf("%s: Satisfies(FreshFirstFactor) = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestFreshLocalMultiIsMultiFactorWithinTheWindowAndLocal holds the requirement
// of an account-security action on an account that has factors.
func TestFreshLocalMultiIsMultiFactorWithinTheWindowAndLocal(t *testing.T) {
	window := 10 * time.Minute
	r := FreshLocalMulti(window)
	if r.Assurance != AssuranceMulti || r.MaxAge != window || !r.Local {
		t.Fatalf("FreshLocalMulti(%v) = %+v, want multi-factor, that window, local", window, r)
	}
	if other := FreshLocalMulti(time.Hour); other.MaxAge != time.Hour {
		t.Errorf("FreshLocalMulti(1h).MaxAge = %v: the window is not read", other.MaxAge)
	}

	for _, c := range []struct {
		name string
		id   Identity
		want bool
	}{
		{"local multi-factor proven now", identityAt(AssuranceMulti, 0, false), true},
		{"local multi-factor exactly at the window", identityAt(AssuranceMulti, window, false), true},
		{"local multi-factor past the window", identityAt(AssuranceMulti, window+time.Second, false), false},
		{"a single factor proven now", identityAt(AssuranceSingle, 0, false), false},
		{"an IdP's multi-factor proven now does not count as local", identityAt(AssuranceMulti, 0, true), false},
		{"no identity at all", Identity{}, false},
	} {
		if got := c.id.Satisfies(r, requirementNow); got != c.want {
			t.Errorf("%s: Satisfies(FreshLocalMulti(%v)) = %v, want %v", c.name, window, got, c.want)
		}
	}
}

// TestAWindowThatIsNotPositiveIsARequirementNobodyMeets checks that a non-positive
// window is met by nobody, rather than a requirement with no limit.
func TestAWindowThatIsNotPositiveIsARequirementNobodyMeets(t *testing.T) {
	best := identityAt(AssuranceMulti, 0, false)
	for _, w := range []time.Duration{0, -time.Second, -time.Hour} {
		r := FreshLocalMulti(w)
		if best.Satisfies(r, requirementNow) {
			t.Errorf("a local multi-factor authentication of this instant satisfies FreshLocalMulti(%v) = %+v", w, r)
		}
		if r.Assurance != AssuranceMulti || !r.Local {
			t.Errorf("FreshLocalMulti(%v) = %+v, which also dropped the level or the local flag", w, r)
		}
	}
	if !best.Satisfies(FreshLocalMulti(time.Nanosecond), requirementNow) {
		t.Error("a local multi-factor authentication of this instant does not satisfy a window of a nanosecond")
	}
}

// TestTheRootConstructorsAreTheModelsAndNothingElse keeps the root's requirements
// identical to the model's, so a second constant cannot drift.
func TestTheRootConstructorsAreTheModelsAndNothingElse(t *testing.T) {
	if FreshFirstFactor() != model.FreshFirstFactor() {
		t.Error("the root FreshFirstFactor differs from the model's")
	}
	if FreshLocalMulti(7*time.Minute) != model.FreshLocalMulti(7*time.Minute) {
		t.Error("the root FreshLocalMulti differs from the model's")
	}
}
