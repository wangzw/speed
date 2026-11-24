package admin

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
)

// TestAnAccountOwningAnOrganizationIsRefusedWithItsOwnCode checks that an owner-protected account is refused 409 account-organization-owner with the organization named in the detail.
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
