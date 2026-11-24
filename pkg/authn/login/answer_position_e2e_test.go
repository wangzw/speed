package login_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	loginsms "github.com/vislake/speed/pkg/authn/login/sms"
	"github.com/vislake/speed/pkg/core"
	smslog "github.com/vislake/speed/pkg/sms/log"
)

type standInCodes struct{ accepted string }

func (c standInCodes) Send(context.Context, authn.SendCode) (string, error) {
	return "challenge-1", nil
}

func (c standInCodes) Verify(_ context.Context, _, code string) error {
	if code == c.accepted {
		return nil
	}
	return authn.ErrInvalidCredentials
}

func standInCodesModule() core.Module {
	return core.Module{
		Name:     "stand-in.sms-codes",
		Provides: []core.Provision{{Token: (*authn.SMSCodes)(nil)}},
		New:      func(context.Context, *core.Registry) (any, error) { return standInCodes{accepted: "123456"}, nil },
	}
}

func (h *harness) createPhoneAccount(username, phone string) {
	h.t.Helper()
	if _, err := h.admin.Create(h.ctx, authn.CreateAccount{
		Kind:             authn.KindUser,
		Username:         username,
		Phone:            phone,
		PhoneVerifiedVia: authn.Verification{Via: authn.VerifiedViaSMSCode},
		Via:              "admin",
	}); err != nil {
		h.t.Fatalf("creating account %q with phone %q: %v", username, phone, err)
	}
}

func withFlowHeader(token string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Authn-Flow", token) }
}

func assertRefusedWithoutASequence(t *testing.T, r reply) {
	t.Helper()
	if r.status != http.StatusUnauthorized {
		t.Fatalf("a refused answer answered %d %s, want 401", r.status, r.body)
	}
	doc := r.json(t)
	if doc["type"] != "urn:speed:authn:login-failed" {
		t.Errorf("the refusal is %v, want the login-failed problem", doc["type"])
	}
	if _, carried := doc["seq"]; carried {
		t.Errorf("the refusal carries a sequence number: %s", r.body)
	}
}

// TestARefusalKeepsTheAnswerPositionAcrossPaths holds that a refusal never moves the sequence number, so the client retries with the value it holds.
func TestARefusalKeepsTheAnswerPositionAcrossPaths(t *testing.T) {
	const number = "+8613800000011"
	assembleWithSections(t,
		[]core.Module{loginsms.Module(), standInCodesModule(), smslog.Module()},
		map[string]any{"sms": map[string]any{"carrier": "log"}},
		func(h *harness) {
			h.createAccount("alice", "alice-password")
			h.createPhoneAccount("phoneowner", number)

			t.Run("password over the Authn-Flow header", func(t *testing.T) {
				start := h.post("/api/v1/auth/flows", map[string]any{"method": "password", "binding": "header"})
				if start.status != http.StatusOK {
					t.Fatalf("POST flows answered %d: %s", start.status, start.body)
				}
				doc := start.json(t)
				token, _ := doc["flow_token"].(string)
				if token == "" {
					t.Fatalf("a header-bound start returned no flow token: %s", start.body)
				}
				seq := int(doc["seq"].(float64))

				refused := h.post("/api/v1/auth/flows/answer", map[string]any{
					"seq": seq, "step": "answer",
					"input": map[string]string{"identifier": "alice", "password": "wrong-horse"},
				}, withFlowHeader(token))
				assertRefusedWithoutASequence(t, refused)

				accepted := h.post("/api/v1/auth/flows/answer", map[string]any{
					"seq": seq, "step": "answer",
					"input": map[string]string{"identifier": "alice", "password": "alice-password"},
				}, withFlowHeader(token))
				if accepted.status != http.StatusOK {
					t.Fatalf("the corrected password with the same seq answered %d: %s", accepted.status, accepted.body)
				}
				answered := accepted.json(t)
				if next, _ := answered["next"].(map[string]any); next["kind"] != "complete" {
					t.Errorf("the corrected password moved to next %v, want complete", answered["next"])
				}
			})

			t.Run("sms over the flow cookie", func(t *testing.T) {
				start := h.post("/api/v1/auth/flows", map[string]any{
					"method": "sms", "input": map[string]string{"phone": number},
				})
				if start.status != http.StatusOK {
					t.Fatalf("POST flows answered %d: %s", start.status, start.body)
				}
				seq := int(start.json(t)["seq"].(float64))

				refused := h.post("/api/v1/auth/flows/answer", map[string]any{
					"seq": seq, "step": "answer", "input": map[string]string{"code": "000000"},
				})
				assertRefusedWithoutASequence(t, refused)

				accepted := h.post("/api/v1/auth/flows/answer", map[string]any{
					"seq": seq, "step": "answer", "input": map[string]string{"code": "123456"},
				})
				if accepted.status != http.StatusOK {
					t.Fatalf("the corrected code with the same seq answered %d: %s", accepted.status, accepted.body)
				}
				if next, _ := accepted.json(t)["next"].(map[string]any); next["kind"] != "complete" {
					t.Errorf("the corrected code moved to next %v, want complete", accepted.json(t)["next"])
				}
			})
		})
}
