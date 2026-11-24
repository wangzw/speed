package main

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

const requiredSSOTenant = `    - name: demo
      admins:
        - operator
      settings:
        - key: "tenant.admission.require-sso"
          value: "true"
`

// TestAnInvitationWriteUnderRequiredSSOIsRefusedForALocalPasswordSession holds that a tenant requiring
// SSO refuses a local-password session's invitation write 401 sso_required before the handler decides.
func TestAnInvitationWriteUnderRequiredSSOIsRefusedForALocalPasswordSession(t *testing.T) {
	t.Parallel()
	dsn := filepath.Join(t.TempDir(), "demo.db")
	config := writeConfigReplacing(t, freePorts(t, listenerCount), dsn,
		"    - name: demo\n      admins:\n        - operator\n", requiredSSOTenant)
	if res := runCommand(t, config, "sqlite", seedCommand); res.err != nil {
		t.Fatalf("seed failed: %v\n%s", res.err, res.combined())
	}
	_, ports := startServing(t, func(ports []int) *process {
		return start(t, writeConfigReplacing(t, ports, dsn,
			"    - name: demo\n      admins:\n        - operator\n", requiredSSOTenant),
			"sqlite", serveCommand)
	})

	base := fmt.Sprintf("http://127.0.0.1:%d", ports[backendListener])
	token := signIn(t, base, seedIdentifier, seedPassword)
	auth := map[string]string{
		"Authorization": "Bearer " + token,
		"X-Tenant":      demoTenant,
	}

	issued := send(t, &http.Client{Timeout: probeTimeout()}, http.MethodPost,
		base+"/api/v1/tenants/"+demoTenant+"/invitations",
		map[string]any{"email": "invitee@demo.test"}, auth)
	if issued.status != http.StatusUnauthorized {
		t.Fatalf("the invitation write answered %d, want 401 sso_required:\n%s",
			issued.status, issued.body)
	}
	if !strings.Contains(string(issued.body), "urn:speed:authn:sso_required") {
		t.Errorf("the refusal is %q, want the sso_required problem type", issued.body)
	}
}
