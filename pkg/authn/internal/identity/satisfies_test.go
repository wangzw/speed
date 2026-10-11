package identity_test

import (
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn/internal/identity"
	"github.com/vislake/speed/pkg/authn/internal/model"
)

// A freshness requirement is measured against an instant. A zero instant is a
// clock that was never set, and it reads as no answer, not as a pass.
func TestAFreshnessRequirementIsNotMetAtTheZeroInstant(t *testing.T) {
	fresh := func(authTime time.Time) identity.Identity {
		p := principal()
		p.AuthTime = authTime
		return identity.New(p, "b", "c")
	}
	r := model.Requirement{Assurance: model.AssuranceSingle, MaxAge: time.Minute}
	for name, id := range map[string]identity.Identity{
		"a day-old authentication":    fresh(t0.Add(-24 * time.Hour)),
		"a current authentication":    fresh(t0),
		"an authentication never set": fresh(time.Time{}),
	} {
		if id.Satisfies(r, time.Time{}) {
			t.Errorf("%s satisfies a MaxAge requirement at the zero instant", name)
		}
	}
	// The same requirement with a real instant still tells fresh from stale.
	if !fresh(t0).Satisfies(r, t0) {
		t.Error("a current authentication does not satisfy a MaxAge requirement")
	}
	if fresh(t0.Add(-24*time.Hour)).Satisfies(r, t0) {
		t.Error("a day-old authentication satisfies a one-minute MaxAge")
	}
	if fresh(time.Time{}).Satisfies(r, t0) {
		t.Error("an authentication that was never set satisfies a MaxAge requirement")
	}
	// A requirement that asks for no freshness never reads the instant.
	if !fresh(t0).Satisfies(model.Requirement{Assurance: model.AssuranceSingle}, time.Time{}) {
		t.Error("a requirement without MaxAge is refused at the zero instant")
	}
}
