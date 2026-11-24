package admin

import (
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// TestTheStepUpChallengeCarriesTheSchemeTheLevelAndTheFreshness checks the challenge's scheme, acr_values and max_age, and the Bearer fallback.
func TestTheStepUpChallengeCarriesTheSchemeTheLevelAndTheFreshness(t *testing.T) {
	got := stepUpChallenge("Cookie", authn.Requirement{Assurance: authn.AssuranceMulti, MaxAge: 10 * time.Minute})
	want := `Cookie error="insufficient_user_authentication", ` +
		`error_description="A stronger or more recent authentication is required", ` +
		`acr_values="2", max_age="600"`
	if got != want {
		t.Errorf("the step-up challenge is %q, want %q", got, want)
	}

	got = stepUpChallenge("Bearer", authn.Requirement{Assurance: authn.AssuranceSingle})
	want = `Bearer error="insufficient_user_authentication", ` +
		`error_description="A stronger or more recent authentication is required", acr_values="1"`
	if got != want {
		t.Errorf("a challenge with no freshness is %q, want %q", got, want)
	}

	got = stepUpChallenge("Bearer;evil", authn.Requirement{Assurance: authn.AssuranceSingle})
	if want := "Bearer error="; len(got) < len(want) || got[:len(want)] != want {
		t.Errorf("a challenge for an unusable scheme reads %q, want it to fall back to Bearer", got)
	}
}
