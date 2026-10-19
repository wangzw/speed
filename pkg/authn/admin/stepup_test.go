package admin

import (
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// The step-up challenge a typed handler answers 401 with is built in this
// module, because the capability's writer takes a *gin.Context the handler no
// longer has. It has to stay the same shape that writer produces: the scheme
// the identity authenticated with, the problem code's description, the level
// and, when the requirement names one, the freshness.
func TestTheStepUpChallengeCarriesTheSchemeTheLevelAndTheFreshness(t *testing.T) {
	got := stepUpChallenge("Cookie", authn.Requirement{Assurance: authn.AssuranceMulti, MaxAge: 10 * time.Minute})
	want := `Cookie error="insufficient_user_authentication", ` +
		`error_description="A stronger or more recent authentication is required", ` +
		`acr_values="2", max_age="600"`
	if got != want {
		t.Errorf("the step-up challenge is %q, want %q", got, want)
	}

	// A requirement with no freshness writes no max_age.
	got = stepUpChallenge("Bearer", authn.Requirement{Assurance: authn.AssuranceSingle})
	want = `Bearer error="insufficient_user_authentication", ` +
		`error_description="A stronger or more recent authentication is required", acr_values="1"`
	if got != want {
		t.Errorf("a challenge with no freshness is %q, want %q", got, want)
	}

	// A scheme that is not an RFC 9110 token cannot go into the header: the
	// challenge falls back to Bearer.
	got = stepUpChallenge("Bearer;evil", authn.Requirement{Assurance: authn.AssuranceSingle})
	if want := "Bearer error="; len(got) < len(want) || got[:len(want)] != want {
		t.Errorf("a challenge for an unusable scheme reads %q, want it to fall back to Bearer", got)
	}
}
