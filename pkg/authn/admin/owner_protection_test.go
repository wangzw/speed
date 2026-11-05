package admin

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
)

// TestAnAccountOwningAnOrganizationIsRefusedWithItsOwnCode holds the HTTP face
// of the owner protection: a dedicated 409 problem type, not the generic
// conflict, with the organization's identifier in the detail so the console can
// name the organization the operator has to hand on or delete.
func TestAnAccountOwningAnOrganizationIsRefusedWithItsOwnCode(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"delete", http.MethodDelete, "/api/v1/system/auth/accounts/" + string(userID), nil},
		{
			"disable", http.MethodPost, "/api/v1/system/auth/accounts/" + string(userID) + "/status",
			map[string]any{"status": "disabled"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			r.accountAdm.err = &authn.AccountOwnsOrganizationError{Tenant: "acme"}

			rec := r.do(tc.method, tc.path, tc.body, fresh()...)
			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d, want 409; body %s", rec.Code, rec.Body.String())
			}
			if got := problemCode(t, rec.Body.String()); got != mount.CodeAccountOrganizationOwner {
				t.Errorf("problem code = %q, want %q", got, mount.CodeAccountOrganizationOwner)
			}
			var p struct {
				Detail string `json:"detail"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
				t.Fatalf("decoding the problem body: %v", err)
			}
			if p.Detail != "acme" {
				t.Errorf("problem detail = %q, want the tenant acme", p.Detail)
			}
		})
	}
}
