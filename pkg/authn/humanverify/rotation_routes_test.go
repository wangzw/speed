package humanverify

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// routeRotationRounds is how many requests a route rotation case makes from
// distinct sources. With a two-form rotation the chance that one form never
// appears is 2^-(rounds-1).
const routeRotationRounds = 48

// TestTheCaptchaChallengeRouteRotatesInsteadOfHonouringAForm holds the policy at
// the route: image, slider and an absent form are not a client's choice, so
// repeating any of those requests covers both rotating forms, and audio is never
// drawn unless it was asked for.
func TestTheCaptchaChallengeRouteRotatesInsteadOfHonouringAForm(t *testing.T) {
	for _, requested := range []string{"", "image", "slider"} {
		name := requested
		if name == "" {
			name = "absent"
		}
		t.Run(name, func(t *testing.T) {
			rig := newRig(t, body(t, sectionFor(ModeCaptcha)))
			rig.drive(t)

			seen := map[string]int{}
			for i := 0; i < routeRotationRounds; i++ {
				target := captchaChallengeFullPath + "?scope=phone"
				if requested != "" {
					target += "&form=" + requested
				}
				request := httptest.NewRequest(http.MethodPost, target, nil)
				// A distinct source per request keeps the per-source issuance
				// bound out of the sample; the form value is what the case
				// varies across subtests.
				request.RemoteAddr = fmt.Sprintf("198.51.100.%d:1234", i+1)
				recorder := rig.mounter.Do(request)
				if recorder.Code != http.StatusOK {
					t.Fatalf("request %d answered %d, want 200:\n%s", i+1, recorder.Code, recorder.Body)
				}
				var answer captchaAnswer
				if err := json.Unmarshal(recorder.Body.Bytes(), &answer); err != nil {
					t.Fatalf("the answer is not JSON: %v\n%s", err, recorder.Body)
				}
				seen[answer.Form]++
			}
			if seen["image"] == 0 || seen["slider"] == 0 {
				t.Fatalf("form=%q answered image %d times and slider %d times in %d requests, want both",
					requested, seen["image"], seen["slider"], routeRotationRounds)
			}
			if seen["audio"] != 0 {
				t.Errorf("form=%q answered the audio form %d times, want neither rotating form", requested, seen["audio"])
			}
		})
	}
}
