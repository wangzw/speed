package humanverify

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"math/bits"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

const challengeFullPath = "/api/v1/auth/human/challenge"

// TestTheChallengeRouteExistsOnlyInTheProofOfWorkMode checks that the challenge route exists only in proof-of-work mode.
func TestTheChallengeRouteExistsOnlyInTheProofOfWorkMode(t *testing.T) {
	for _, mode := range []string{ModeOff, ModePoW, ModeCaptcha, ModeTurnstile} {
		t.Run(mode, func(t *testing.T) {
			rig := newRig(t, body(t, sectionFor(mode)))
			rig.drive(t)
			registration, present := rig.mounter.Registered(http.MethodPost, challengeFullPath)
			if want := mode == ModePoW; present != want {
				t.Fatalf("the challenge route is present = %v in mode %q, want %v", present, mode, want)
			}
			if present && registration.Kind != "public" {
				t.Errorf("the route is registered %q, want public", registration.Kind)
			}
		})
	}
}

type challengeAnswer struct {
	Provider   string `json:"provider"`
	Algorithm  string `json:"algorithm"`
	Challenge  string `json:"challenge"`
	Difficulty int    `json:"difficulty"`
	ExpiresIn  int    `json:"expires_in"`
}

// TestTheChallengeRouteAnswersTheDeclaredShape checks that the challenge route answers the declared shape.
func TestTheChallengeRouteAnswersTheDeclaredShape(t *testing.T) {
	rig := newRig(t, body(t, sectionFor(ModePoW)))
	verifier := rig.drive(t)

	recorder := rig.mounter.Do(httptest.NewRequest(http.MethodPost, challengeFullPath+"?scope=other", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("the challenge route answered %d, want 200:\n%s", recorder.Code, recorder.Body)
	}
	var answer challengeAnswer
	if err := json.Unmarshal(recorder.Body.Bytes(), &answer); err != nil {
		t.Fatalf("the answer is not JSON: %v\n%s", err, recorder.Body)
	}
	if answer.Provider != "pow" {
		t.Errorf("provider = %q, want pow", answer.Provider)
	}
	if answer.Algorithm != "sha256" {
		t.Errorf("algorithm = %q, want sha256", answer.Algorithm)
	}
	if answer.Challenge == "" {
		t.Error("the answer carries no challenge")
	}
	if answer.Difficulty < 18 || answer.Difficulty > 22 {
		t.Errorf("difficulty = %d, want a value in 18..22", answer.Difficulty)
	}
	if answer.ExpiresIn != 300 {
		t.Errorf("expires_in = %d, want the default 300 seconds", answer.ExpiresIn)
	}

	proof := authn.HumanProof{Token: answer.Challenge + "." + solveChallenge(t, answer.Challenge, answer.Difficulty)}
	if err := verifier.Verify(t.Context(), proof, authn.HumanScopeOther); err != nil {
		t.Fatalf("the issued challenge was refused: %v", err)
	}
	if err := verifier.Verify(t.Context(), proof, authn.HumanScopeOther); err == nil {
		t.Fatal("the issued challenge was accepted twice")
	}
}

// TestTheChallengeRouteRefusesAMissingOrUnknownScope checks that the challenge route refuses a missing or unknown scope.
func TestTheChallengeRouteRefusesAMissingOrUnknownScope(t *testing.T) {
	for _, target := range []string{challengeFullPath, challengeFullPath + "?scope=email"} {
		t.Run(target, func(t *testing.T) {
			rig := newRig(t, body(t, sectionFor(ModePoW)))
			rig.drive(t)
			recorder := rig.mounter.Do(httptest.NewRequest(http.MethodPost, target, nil))
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("the challenge route answered %d, want 400:\n%s", recorder.Code, recorder.Body)
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
		})
	}
}

// TestTheChallengeRouteAnswersUnavailableWhenTheStoreFails checks that the challenge route answers unavailable when the store fails.
func TestTheChallengeRouteAnswersUnavailableWhenTheStoreFails(t *testing.T) {
	rig := newRig(t, body(t, sectionFor(ModePoW)))
	rig.drive(t)
	rig.store.incErr = errStubKV

	recorder := rig.mounter.Do(httptest.NewRequest(http.MethodPost, challengeFullPath+"?scope=other", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("a store fault answered %d, want 503:\n%s", recorder.Code, recorder.Body)
	}
	var problem struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
		t.Fatalf("the answer is not JSON: %v\n%s", err, recorder.Body)
	}
	if problem.Type != "urn:speed:authn:unavailable" {
		t.Errorf("the problem type is %q, want urn:speed:authn:unavailable", problem.Type)
	}
}

// TestTheChallengeRouteAnswersThrottledAtTheIssuanceCap checks that the challenge route answers throttled at the issuance cap.
func TestTheChallengeRouteAnswersThrottledAtTheIssuanceCap(t *testing.T) {
	rig := newRig(t, body(t, sectionFor(ModePoW)))
	rig.drive(t)

	for i := 0; i < 10; i++ {
		recorder := rig.mounter.Do(httptest.NewRequest(http.MethodPost, challengeFullPath+"?scope=other", nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("request %d answered %d, want 200:\n%s", i+1, recorder.Code, recorder.Body)
		}
	}
	recorder := rig.mounter.Do(httptest.NewRequest(http.MethodPost, challengeFullPath+"?scope=other", nil))
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("the request past the cap answered %d, want 429:\n%s", recorder.Code, recorder.Body)
	}
	var problem struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
		t.Fatalf("the answer is not JSON: %v\n%s", err, recorder.Body)
	}
	if problem.Type != "urn:speed:authn:throttled" {
		t.Errorf("the problem type is %q, want urn:speed:authn:throttled", problem.Type)
	}
}

func solveChallenge(t *testing.T, challenge string, difficulty int) string {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(challenge)
	if err != nil {
		t.Fatalf("decoding the challenge %q: %v", challenge, err)
	}
	for nonce := uint64(0); nonce < 1<<24; nonce++ {
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
		if zeros >= difficulty {
			return strconv.FormatUint(nonce, 10)
		}
	}
	t.Fatalf("no nonce under 2^24 reaches difficulty %d for the challenge %q", difficulty, challenge)
	return ""
}
