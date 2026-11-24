package admin

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

func requireNullField(t *testing.T, object map[string]json.RawMessage, name string) {
	t.Helper()
	raw, present := object[name]
	if !present {
		t.Errorf("the view carries no %q field", name)
		return
	}
	if string(raw) != "null" {
		t.Errorf("%s = %s, want null for an absent instant", name, raw)
	}
}

func requireUTCInstant(t *testing.T, object map[string]json.RawMessage, name string) {
	t.Helper()
	raw, present := object[name]
	if !present {
		t.Errorf("the view carries no %q field", name)
		return
	}
	if !strings.HasSuffix(string(raw), `Z"`) {
		t.Errorf("%s = %s, want an RFC 3339 instant in UTC ending in Z", name, raw)
	}
}

// TestATimeViewWritesAnInstantInUTC holds the wire rule for a present instant: a value stored in a non-UTC zone leaves as UTC with a trailing Z.
func TestATimeViewWritesAnInstantInUTC(t *testing.T) {
	at := time.Date(2027, 3, 4, 5, 6, 7, 0, time.FixedZone("UTC+8", 8*60*60))

	t.Run("a secret with an expiry", func(t *testing.T) {
		r := newRig(t)
		r.accounts.secrets[userID] = []authn.ServiceSecretInfo{{ID: "secret-1", Label: "ci", ExpiresAt: at}}
		rec := r.do(http.MethodGet, "/api/v1/system/auth/accounts/"+string(userID)+"/secrets", nil, fresh()...)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
		}
		var list struct {
			Secrets []map[string]json.RawMessage `json:"secrets"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatalf("decoding the secrets: %v", err)
		}
		if len(list.Secrets) != 1 {
			t.Fatalf("secrets = %+v, want one", list.Secrets)
		}
		requireUTCInstant(t, list.Secrets[0], "expires_at")
	})

	t.Run("a created secret with an expiry", func(t *testing.T) {
		r := newRig(t)
		rec := r.do(http.MethodPost, "/api/v1/system/auth/accounts/"+string(userID)+"/secrets",
			map[string]any{"label": "ci", "expires_at": "2027-03-04T05:06:07+08:00"}, fresh()...)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
		}
		var created map[string]json.RawMessage
		if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
			t.Fatalf("decoding the created secret: %v", err)
		}
		requireUTCInstant(t, created, "expires_at")
	})
}

// TestATimeViewWritesNullWhenThereIsNoInstant checks that an absent instant is published as null rather than the zero time.
func TestATimeViewWritesNullWhenThereIsNoInstant(t *testing.T) {
	t.Run("a secret that never expires", func(t *testing.T) {
		r := newRig(t)
		r.accounts.secrets[userID] = []authn.ServiceSecretInfo{{ID: "secret-1", Label: "ci"}}
		rec := r.do(http.MethodGet, "/api/v1/system/auth/accounts/"+string(userID)+"/secrets", nil, fresh()...)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
		}
		var list struct {
			Secrets []map[string]json.RawMessage `json:"secrets"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatalf("decoding the secrets: %v", err)
		}
		if len(list.Secrets) != 1 {
			t.Fatalf("secrets = %+v, want one", list.Secrets)
		}
		requireNullField(t, list.Secrets[0], "expires_at")
	})

	t.Run("a secret created with no expiry", func(t *testing.T) {
		r := newRig(t)
		rec := r.do(http.MethodPost, "/api/v1/system/auth/accounts/"+string(userID)+"/secrets",
			map[string]any{"label": "ci"}, fresh()...)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
		}
		var created map[string]json.RawMessage
		if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
			t.Fatalf("decoding the created secret: %v", err)
		}
		requireNullField(t, created, "expires_at")
	})

	t.Run("an impersonation still in force", func(t *testing.T) {
		r := newRig(t)
		r.records.put(authn.ImpersonationGrant{ID: grantA, Actor: adminID, Target: userID, Tenant: tenantB})
		rec := r.do(http.MethodGet, "/api/v1/system/tenants/"+tenantB+"/impersonations", nil, fresh()...)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
		}
		var list struct {
			Impersonations []map[string]json.RawMessage `json:"impersonations"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatalf("decoding the impersonations: %v", err)
		}
		if len(list.Impersonations) != 1 {
			t.Fatalf("impersonations = %+v, want one", list.Impersonations)
		}
		requireNullField(t, list.Impersonations[0], "ended_at")
	})
}
