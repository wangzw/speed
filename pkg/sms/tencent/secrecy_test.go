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

const (
	echoedSecretID   = "AKIDexample"
	echoedSecretKey  = "secret-key-example"
	echoedCredential = "the credential " + echoedSecretID + " / " + echoedSecretKey + " was rejected"
)

func topLevelError(message string) string {
	encoded, _ := json.Marshal(map[string]any{"Response": map[string]any{
		"Error":     map[string]any{"Code": "InternalError.UnknownError", "Message": message},
		"RequestId": "req-1",
	}})
	return string(encoded)
}

func perNumberStatus(message string) string {
	encoded, _ := json.Marshal(map[string]any{"Response": map[string]any{
		"SendStatusSet": []map[string]any{{
			"Code": "FailedOperation.JsonParseFail", "Message": message, "PhoneNumber": "+8613800138000",
		}},
		"RequestId": "req-1",
	}})
	return string(encoded)
}

func secrecySubmission() sms.Submission {
	return sms.Submission{
		Name: "authn.code.login", Content: "your code is ${code}", Kind: sms.KindVerification,
		Locale: "zh-CN", Params: []string{"code"},
	}
}

// TestNoSeamRepeatsTheCredentialInItsError checks that neither seam returns the
// credential in a failure's text, even when the carrier echoes it back.
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
