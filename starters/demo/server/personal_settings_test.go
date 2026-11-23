package main

// A personal tenant's settings surface refuses a write from the account's own
// tenant administrator. The personal domain belongs to the host that built it,
// and its administrator holds no settings permission, so the authorization
// layer refuses the write before the store is asked.
//
// This case covers that authorization-layer refusal: the request never reaches
// the host's settingsWriteGuard, which would answer with its own protected-write
// problem. The guard itself is covered by
// TestTheSettingsWriteGuardRefusesAPersonalDomainWrite in tenancy_test.go and
// TestAWriteSettingsRefusalIsProtected in pkg/settings/tenantadmin.

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestThePersonalTenantSettingsWriteIsRefused drives the assembled program: the
// seeded account signs in, finds its personal tenant, and writes one admission
// setting under it. The authorization layer refuses the write with 403 before
// the host's settings write guard is reached. The same write under an ordinary
// business tenant the operator administers answers 204, so the refusal belongs
// to the personal domain rather than to the route.
func TestThePersonalTenantSettingsWriteIsRefused(t *testing.T) {
	t.Parallel()
	base, token := seededServe(t, "")
	client := &http.Client{Timeout: probeTimeout}
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

	// The same write is allowed under an ordinary business tenant the operator
	// administers, so the refusal above is the personal domain's alone.
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
