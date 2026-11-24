package captcha

import (
	"errors"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

const rotationRounds = 64

func clearIssuance(t *testing.T, s *store) {
	t.Helper()
	if err := s.Delete(t.Context(), issueKey(source)); err != nil {
		t.Fatalf("clearing the issuance counter: %v", err)
	}
}

// TestIssueRotatesBetweenTheImageAndSliderForms checks that issuance rotates between the image and slider forms.
func TestIssueRotatesBetweenTheImageAndSliderForms(t *testing.T) {
	p, s := newProvider(t)
	seen := map[Form]int{}
	for i := 0; i < rotationRounds; i++ {
		clearIssuance(t, s)
		issued, err := p.Issue(t.Context(), IssueOptions{Scope: authn.HumanScopePhone, RemoteIP: source})
		if err != nil {
			t.Fatalf("Issue %d: %v", i+1, err)
		}
		seen[issued.Form]++
	}
	if seen[FormImage] == 0 || seen[FormSlider] == 0 {
		t.Fatalf("the rotation drew image %d times and slider %d times in %d identical requests, want both",
			seen[FormImage], seen[FormSlider], rotationRounds)
	}
	if seen[FormAudio] != 0 {
		t.Errorf("the rotation drew the audio form %d times, want neither of the non-accessibility forms", seen[FormAudio])
	}
}

// TestARequestedImageOrSliderFormDoesNotPinTheChallenge checks that a requested image or slider form does not pin the challenge.
func TestARequestedImageOrSliderFormDoesNotPinTheChallenge(t *testing.T) {
	for _, requested := range []Form{FormImage, FormSlider} {
		t.Run(string(requested), func(t *testing.T) {
			p, s := newProvider(t)
			seen := map[Form]int{}
			for i := 0; i < rotationRounds; i++ {
				clearIssuance(t, s)
				issued, err := p.Issue(t.Context(), IssueOptions{Scope: authn.HumanScopePhone, Form: requested, RemoteIP: source})
				if err != nil {
					t.Fatalf("Issue %d: %v", i+1, err)
				}
				seen[issued.Form]++
			}
			if seen[FormImage] == 0 || seen[FormSlider] == 0 {
				t.Fatalf("form=%s drew image %d times and slider %d times in %d requests, want both",
					requested, seen[FormImage], seen[FormSlider], rotationRounds)
			}
			if seen[FormAudio] != 0 {
				t.Errorf("form=%s drew the audio form %d times, want neither of the rotating forms", requested, seen[FormAudio])
			}
		})
	}
}

// TestAnAudioRequestReturnsTheAudioForm checks that an audio request returns the audio form.
func TestAnAudioRequestReturnsTheAudioForm(t *testing.T) {
	p, _ := newProvider(t)
	issued := issue(t, p, FormAudio)
	if issued.Form != FormAudio {
		t.Fatalf("form=audio answered the %q form, want %q", issued.Form, FormAudio)
	}
	if _, err := p.Media(t.Context(), issued.ID, MediaAudio); err != nil {
		t.Fatalf("the audio media: %v", err)
	}
	for _, kind := range []MediaKind{MediaImage, MediaBackground, MediaPiece} {
		if _, err := p.Media(t.Context(), issued.ID, kind); !errors.Is(err, ErrChallengeNotFound) {
			t.Errorf("the audio challenge carries the %s media, resolved with %v", kind, err)
		}
	}
	if _, err := p.Issue(t.Context(), IssueOptions{Scope: authn.HumanScopePhone, Form: FormAudio, Lang: "klingon", RemoteIP: "203.0.113.8"}); !errors.Is(err, ErrUnknownAudioLanguage) {
		t.Errorf("an unsupported audio language resolved with %v, want ErrUnknownAudioLanguage", err)
	}
}

// TestARotationFailureIssuesNothing checks that a rotation failure issues nothing.
func TestARotationFailureIssuesNothing(t *testing.T) {
	s := newStore()
	p, err := newProviderWithRotation(s, nil, func() (Form, error) {
		return "", errors.New("the random source is unavailable")
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for i := 0; i < issueLimit; i++ {
		if _, issueErr := p.Issue(t.Context(), IssueOptions{Scope: authn.HumanScopePhone, RemoteIP: source}); !errors.Is(issueErr, authn.ErrUnavailable) {
			t.Fatalf("Issue %d resolved with %v, want ErrUnavailable", i+1, issueErr)
		}
	}
	working, err := newProviderWithRotation(s, nil, func() (Form, error) { return FormImage, nil })
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := working.Issue(t.Context(), IssueOptions{Scope: authn.HumanScopePhone, RemoteIP: source}); err != nil {
		t.Fatalf("a failed rotation spent the source's issuance allowance: %v", err)
	}
}

// TestTheRotationCannotWidenTheDrawnForms checks that rotation cannot widen the drawn forms.
func TestTheRotationCannotWidenTheDrawnForms(t *testing.T) {
	for _, form := range []Form{FormAudio, Form("puzzle"), ""} {
		t.Run(string(form), func(t *testing.T) {
			p, err := newProviderWithRotation(newStore(), nil, func() (Form, error) { return form, nil })
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if _, err := p.Issue(t.Context(), IssueOptions{Scope: authn.HumanScopePhone, RemoteIP: source}); !errors.Is(err, authn.ErrUnavailable) {
				t.Fatalf("a rotation returning %q resolved with %v, want ErrUnavailable", form, err)
			}
		})
	}
}

// TestNewRequiresARotation holds the constructor's other precondition.
func TestNewRequiresARotation(t *testing.T) {
	if _, err := newProviderWithRotation(newStore(), nil, nil); err == nil {
		t.Fatal("newProviderWithRotation accepted no rotation")
	}
}

func drawRotated(t *testing.T, p *Provider, s *store, form Form) Challenge {
	t.Helper()
	const attempts = 256
	for i := 0; i < attempts; i++ {
		clearIssuance(t, s)
		issued, err := p.Issue(t.Context(), IssueOptions{Scope: authn.HumanScopePhone, RemoteIP: source})
		if err != nil {
			t.Fatalf("Issue %d: %v", i+1, err)
		}
		if issued.Form == form {
			return issued
		}
	}
	t.Fatalf("the rotation never drew the %s form in %d attempts", form, attempts)
	return Challenge{}
}

// TestRotatedFormsKeepTheOneTimeAndLifetimeSemantics checks that rotated forms keep the one-time and lifetime semantics.
func TestRotatedFormsKeepTheOneTimeAndLifetimeSemantics(t *testing.T) {
	for _, form := range []Form{FormImage, FormSlider} {
		t.Run(string(form), func(t *testing.T) {
			p, s := newProvider(t)

			issued := drawRotated(t, p, s, form)
			answer := answerOf(t, s, issued)
			if err := p.Verify(t.Context(), proof(issued.ID, answer), authn.HumanScopePhone); err != nil {
				t.Fatalf("the drawn answer was refused: %v", err)
			}
			if err := p.Verify(t.Context(), proof(issued.ID, answer), authn.HumanScopePhone); !errors.Is(err, authn.ErrHumanVerificationRequired) {
				t.Fatalf("the second presentation resolved with %v, want ErrHumanVerificationRequired", err)
			}

			fresh := drawRotated(t, p, s, form)
			freshAnswer := answerOf(t, s, fresh)
			s.advance(challengeTTL + time.Second)
			if err := p.Verify(t.Context(), proof(fresh.ID, freshAnswer), authn.HumanScopePhone); !errors.Is(err, authn.ErrHumanVerificationRequired) {
				t.Fatalf("an expired %s challenge resolved with %v, want ErrHumanVerificationRequired", form, err)
			}
		})
	}
}
