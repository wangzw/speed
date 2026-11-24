package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestThePersonalTenantSettingsWriteIsRefused holds that the authorization layer refuses a personal
// tenant's settings write 403 while the same write under an ordinary tenant answers 204.
func TestThePersonalTenantSettingsWriteIsRefused(t *testing.T) {
	t.Parallel()
	base, token := seededServe(t, "")
	client := &http.Client{Timeout: probeTimeout()}
	auth := map[string]string{"Authorization": "Bearer " + token}
	personal := personalTenantName(t, client, base, token)

	write := send(t, client, http.MethodPut,
		base+"/api/v1/tenants/"+personal+"/settings/tenant.admission/require-mfa",
		map[string]any{"value": true},
		withTenant(auth, personal))
	if write.status != http.StatusForbidden {
		t.Fatalf("the personal tenant's settings write answered %d, want 403:\n%s",
			write.status, write.body)
	}
	var problem struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(write.body, &problem); err != nil {
		t.Fatalf("the refusal is not JSON: %v\n%s", err, write.body)
	}
	if problem.Type != "urn:speed:authz:forbidden" {
		t.Errorf("the refusal type is %q, want urn:speed:authz:forbidden", problem.Type)
	}

	operator := signIn(t, base, seedIdentifier, seedPassword)
	allowed := send(t, client, http.MethodPut,
		base+"/api/v1/tenants/"+demoTenant+"/settings/tenant.admission/require-mfa",
		map[string]any{"value": true},
		withTenant(map[string]string{"Authorization": "Bearer " + operator}, demoTenant))
	if allowed.status != http.StatusNoContent {
		t.Fatalf("the business tenant's settings write answered %d, want 204:\n%s",
			allowed.status, allowed.body)
	}
}
