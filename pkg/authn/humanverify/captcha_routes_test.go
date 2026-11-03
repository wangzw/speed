package humanverify

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vislake/speed/pkg/authn/humanverify/captcha"
)

// The captcha routes as the mounter mounts them on its default base.
const (
	captchaChallengeFullPath  = "/api/v1/auth/human/captcha/challenge"
	captchaImageFullPath      = "/api/v1/auth/human/captcha/image/:id"
	captchaBackgroundFullPath = "/api/v1/auth/human/captcha/background/:id"
	captchaPieceFullPath      = "/api/v1/auth/human/captcha/piece/:id"
	captchaAudioFullPath      = "/api/v1/auth/human/captcha/audio/:id"
)

// captchaAnswer is the wire shape the challenge route answers with.
type captchaAnswer struct {
	Provider      string `json:"provider"`
	Form          string `json:"form"`
	Instruction   string `json:"instruction"`
	ID            string `json:"id"`
	ImageURL      string `json:"image_url"`
	BackgroundURL string `json:"background_url"`
	PieceURL      string `json:"piece_url"`
	AudioURL      string `json:"audio_url"`
	ExpiresIn     int    `json:"expires_in"`
}

// TestTheCaptchaRoutesExistOnlyInTheCaptchaMode holds the mode condition: the
// challenge and media routes are present when the deployment runs the captcha
// on a surface, and absent in the other modes.
func TestTheCaptchaRoutesExistOnlyInTheCaptchaMode(t *testing.T) {
	routes := []struct{ method, path string }{
		{http.MethodPost, captchaChallengeFullPath},
		{http.MethodGet, captchaImageFullPath},
		{http.MethodGet, captchaBackgroundFullPath},
		{http.MethodGet, captchaPieceFullPath},
		{http.MethodGet, captchaAudioFullPath},
	}
	for _, mode := range []string{ModeOff, ModePoW, ModeCaptcha, ModeTurnstile} {
		t.Run(mode, func(t *testing.T) {
			rig := newRig(t, body(t, sectionFor(mode)))
			rig.drive(t)
			for _, route := range routes {
				registration, present := rig.mounter.Registered(route.method, route.path)
				if want := mode == ModeCaptcha; present != want {
					t.Errorf("%s %s is present = %v in mode %q, want %v",
						route.method, route.path, present, mode, want)
					continue
				}
				if present && registration.Kind != "public" {
					t.Errorf("%s %s is registered %q, want public", route.method, route.path, registration.Kind)
				}
			}
		})
	}
}

// TestTheCaptchaChallengeRouteAnswersEachForm holds the challenge route for
// every form: the id, the form, the lifetime and the media paths the form
// carries. Each named path serves bytes with the format's signature.
func TestTheCaptchaChallengeRouteAnswersEachForm(t *testing.T) {
	cases := []struct {
		form    captcha.Form
		named   []string
		unnamed []string
	}{
		{
			form:    captcha.FormImage,
			named:   []string{"image"},
			unnamed: []string{"background", "piece", "audio"},
		},
		{
			form:    captcha.FormSlider,
			named:   []string{"background", "piece"},
			unnamed: []string{"image", "audio"},
		},
		{
			form:    captcha.FormAudio,
			named:   []string{"audio"},
			unnamed: []string{"image", "background", "piece"},
		},
	}
	for _, tc := range cases {
		t.Run(string(tc.form), func(t *testing.T) {
			rig := newRig(t, body(t, sectionFor(ModeCaptcha)))
			rig.drive(t)

			recorder := rig.mounter.Do(httptest.NewRequest(http.MethodPost,
				captchaChallengeFullPath+"?form="+string(tc.form), nil))
			if recorder.Code != http.StatusOK {
				t.Fatalf("the challenge route answered %d, want 200:\n%s", recorder.Code, recorder.Body)
			}
			var answer captchaAnswer
			if err := json.Unmarshal(recorder.Body.Bytes(), &answer); err != nil {
				t.Fatalf("the answer is not JSON: %v\n%s", err, recorder.Body)
			}
			if answer.Provider != captcha.ProviderName {
				t.Errorf("provider = %q, want %q", answer.Provider, captcha.ProviderName)
			}
			if answer.Form != string(tc.form) {
				t.Errorf("form = %q, want %q", answer.Form, tc.form)
			}
			if answer.Instruction != captchaInstruction(tc.form) {
				t.Errorf("instruction = %q, want %q", answer.Instruction, captchaInstruction(tc.form))
			}
			if answer.ID == "" {
				t.Error("the answer carries no id")
			}
			if answer.ExpiresIn != 300 {
				t.Errorf("expires_in = %d, want the five-minute 300", answer.ExpiresIn)
			}

			urls := map[string]string{
				"image":      answer.ImageURL,
				"background": answer.BackgroundURL,
				"piece":      answer.PieceURL,
				"audio":      answer.AudioURL,
			}
			for _, name := range tc.unnamed {
				if urls[name] != "" {
					t.Errorf("the %s form names the %s media", tc.form, name)
				}
			}
			for _, name := range tc.named {
				if urls[name] == "" {
					t.Fatalf("the %s form names no %s media", tc.form, name)
				}
				served := rig.mounter.Do(httptest.NewRequest(http.MethodGet, urls[name], nil))
				if served.Code != http.StatusOK {
					t.Fatalf("the %s media answered %d:\n%s", name, served.Code, served.Body)
				}
				body := served.Body.Bytes()
				if name == "audio" {
					if served.Header().Get("Content-Type") != "audio/wav" || !bytes.HasPrefix(body, []byte("RIFF")) {
						t.Errorf("the audio media is %q without a RIFF header", served.Header().Get("Content-Type"))
					}
					continue
				}
				if served.Header().Get("Content-Type") != "image/png" ||
					!bytes.HasPrefix(body, []byte("\x89PNG\r\n\x1a\n")) {
					t.Errorf("the %s media is %q without a PNG header", name, served.Header().Get("Content-Type"))
				}
			}
		})
	}
}

// TestTheCaptchaMediaRoutesRefuseUnknownIDs holds the 404 of an id this provider
// did not draw, whether it is the wrong shape or simply unknown.
func TestTheCaptchaMediaRoutesRefuseUnknownIDs(t *testing.T) {
	rig := newRig(t, body(t, sectionFor(ModeCaptcha)))
	rig.drive(t)
	for _, path := range []string{
		"/api/v1/auth/human/captcha/image/not-an-id",
		"/api/v1/auth/human/captcha/background/aaaaaaaaaaaaaaaaaaaaaa",
		"/api/v1/auth/human/captcha/piece/",
		"/api/v1/auth/human/captcha/audio/zzzzzzzzzzzzzzzzzzzzzz",
	} {
		recorder := rig.mounter.Do(httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusNotFound {
			t.Errorf("%s answered %d, want 404:\n%s", path, recorder.Code, recorder.Body)
		}
	}
}

// TestTheCaptchaChallengeRouteRefusesAnUnknownForm holds the closed set of
// forms at the route: an unknown one is the 400 of a bad request.
func TestTheCaptchaChallengeRouteRefusesAnUnknownForm(t *testing.T) {
	rig := newRig(t, body(t, sectionFor(ModeCaptcha)))
	rig.drive(t)

	recorder := rig.mounter.Do(httptest.NewRequest(http.MethodPost,
		captchaChallengeFullPath+"?form=puzzle", nil))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("an unknown form answered %d, want 400:\n%s", recorder.Code, recorder.Body)
	}
	var problem struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
		t.Fatalf("the answer is not JSON: %v\n%s", err, recorder.Body)
	}
	if problem.Type != "urn:speed:authn:bad-request" {
		t.Errorf("the problem type is %q, want urn:speed:authn:bad-request", problem.Type)
	}
}

// TestTheCaptchaRoutesAnswerUnavailableWhenTheStoreFails holds the outage
// answer on both the challenge and the media route.
func TestTheCaptchaRoutesAnswerUnavailableWhenTheStoreFails(t *testing.T) {
	t.Run("challenge", func(t *testing.T) {
		rig := newRig(t, body(t, sectionFor(ModeCaptcha)))
		rig.drive(t)
		rig.store.setErr = errStubKV

		recorder := rig.mounter.Do(httptest.NewRequest(http.MethodPost, captchaChallengeFullPath, nil))
		if recorder.Code != http.StatusServiceUnavailable {
			t.Fatalf("a store fault answered %d, want 503:\n%s", recorder.Code, recorder.Body)
		}
	})
	t.Run("media", func(t *testing.T) {
		rig := newRig(t, body(t, sectionFor(ModeCaptcha)))
		rig.drive(t)

		issued := rig.mounter.Do(httptest.NewRequest(http.MethodPost,
			captchaChallengeFullPath+"?form=image", nil))
		if issued.Code != http.StatusOK {
			t.Fatalf("the challenge route answered %d:\n%s", issued.Code, issued.Body)
		}
		var answer captchaAnswer
		if err := json.Unmarshal(issued.Body.Bytes(), &answer); err != nil {
			t.Fatalf("the answer is not JSON: %v", err)
		}
		rig.store.getErr = errStubKV

		recorder := rig.mounter.Do(httptest.NewRequest(http.MethodGet, answer.ImageURL, nil))
		if recorder.Code != http.StatusServiceUnavailable {
			t.Fatalf("a store fault answered %d, want 503:\n%s", recorder.Code, recorder.Body)
		}
	})
}
