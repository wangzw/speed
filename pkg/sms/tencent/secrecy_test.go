package tencent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/sms"
)

// The credential the cases configure: the values testConfig installs, so a
// carrier that quotes a request value in its own answer is quoting one of these.
const (
	echoedSecretID  = "AKIDexample"
	echoedSecretKey = "secret-key-example"
	// echoedCredential is the phrase each seeded answer quotes — a carrier that
	// repeats a request value in its reply.
	echoedCredential = "the credential " + echoedSecretID + " / " + echoedSecretKey + " was rejected"
)

// topLevelError is an answer whose top-level Error quotes message: the shape a
// carrier uses when its structured reply repeats a value the request sent.
func topLevelError(message string) string {
	encoded, _ := json.Marshal(map[string]any{"Response": map[string]any{
		"Error":     map[string]any{"Code": "InternalError.UnknownError", "Message": message},
		"RequestId": "req-1",
	}})
	return string(encoded)
}

// perNumberStatus is an answer whose single per-number status quotes message.
func perNumberStatus(message string) string {
	encoded, _ := json.Marshal(map[string]any{"Response": map[string]any{
		"SendStatusSet": []map[string]any{{
			"Code": "FailedOperation.JsonParseFail", "Message": message, "PhoneNumber": "+8613800138000",
		}},
		"RequestId": "req-1",
	}})
	return string(encoded)
}

// secrecySubmission is one template submission the Offer case hands the sender.
func secrecySubmission() sms.Submission {
	return sms.Submission{
		Name: "authn.code.login", Content: "your code is ${code}", Kind: sms.KindVerification,
		Locale: "zh-CN", Params: []string{"code"},
	}
}

// TestNoSeamRepeatsTheCredentialInItsError holds that a module holding a secret
// configuration value keeps it out of the text of every failure its seams return,
// even when the carrier echoes the value back in its answer. That echo is the
// shape most easily missed: the module does not write the value into the error so
// much as fail to keep the carrier's own message out of it, and the SDK copies a
// non-200 body whole into the error it builds.
//
// Both seams are covered — the three management operations and the sending call —
// against each echo shape the carrier has: a structured top-level error, a
// per-number send status, and an HTTP failure whose body the SDK embeds.
func TestNoSeamRepeatsTheCredentialInItsError(t *testing.T) {
	leaked := []string{echoedSecretID, echoedSecretKey}

	send := func(s *sender) error {
		_, err := s.Send(context.Background(), resolvedMessage("chal-secrecy"))
		return err
	}
	calls := map[string]struct {
		call  func(*sender) error
		serve func(*testing.T) string
	}{
		"offer over a top-level error": {
			call:  func(s *sender) error { _, err := s.Offer(context.Background(), secrecySubmission()); return err },
			serve: func(t *testing.T) string { return answerServer(t, topLevelError(echoedCredential)) },
		},
		"approval over a top-level error": {
			call:  func(s *sender) error { _, err := s.Approval(context.Background(), "1001"); return err },
			serve: func(t *testing.T) string { return answerServer(t, topLevelError(echoedCredential)) },
		},
		"retire over a top-level error": {
			call:  func(s *sender) error { return s.Retire(context.Background(), "1001") },
			serve: func(t *testing.T) string { return answerServer(t, topLevelError(echoedCredential)) },
		},
		"send over a top-level error": {
			call:  send,
			serve: func(t *testing.T) string { return answerServer(t, topLevelError(echoedCredential)) },
		},
		"send over a per-number status": {
			call:  send,
			serve: func(t *testing.T) string { return answerServer(t, perNumberStatus(echoedCredential)) },
		},
		"send over an HTTP failure that quotes it in the body": {
			call: send,
			serve: func(t *testing.T) string {
				return fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
					body, _ := json.Marshal(map[string]any{"echoed": echoedCredential})
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write(body)
				})
			},
		},
	}
	for name, c := range calls {
		t.Run(name, func(t *testing.T) {
			s := newTestSender(t, c.serve(t))
			err := c.call(s)
			if err == nil {
				t.Fatal("the answer gave no error")
			}
			if !errors.Is(err, sms.ErrUncertain) {
				t.Fatalf("the answer gave %v, want the uncertain sentinel", err)
			}
			for _, value := range leaked {
				if strings.Contains(err.Error(), value) {
					t.Errorf("the failure %q repeats the credential value %q", err, value)
				}
			}
		})
	}
}
