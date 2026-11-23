package main

// The SMS management page: sms.admin is named for the serve roster, and the
// running binary publishes its feature. The feature document is what the
// console's navigation reads before it offers the page, so publishing the name
// is the condition the walkthrough's browser needs. The baseline template names no
// admin endpoint for sms.admin, so this case also holds the boundary that keeps
// the page out of a normal deployment.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"

	smsadmin "github.com/vislake/speed/pkg/sms/admin"
)

// The e2e manifest's member account. The operator the manifest seeds carries
// the impersonation fixture's two second factors when a case writes them, so a
// password alone does not complete its sign-in; the member has none, and the
// feature document asks only for an authenticated caller.
const (
	e2eFeatureAccount  = "member"
	e2eFeaturePassword = "member-dev-secret"
)

// TestE2EServesTheSMSAdminPage holds the roster entry and the published
// feature: the module is in the capability ceiling every build carries, and a
// running binary answers GET /api/v1/features with sms.admin.
func TestE2EServesTheSMSAdminPage(t *testing.T) {
	t.Parallel()
	if !slices.Contains(capabilityModules, smsadmin.ModuleName) {
		t.Fatalf("the capability ceiling names no %q module, so the binary would publish no "+
			"SMS management feature and the console would offer no page", smsadmin.ModuleName)
	}

	binary := e2eBinary(t)
	_, ports := startServing(t, func(ports []int) *process {
		config := "file://" + e2eConfig(t,
			fmt.Sprintf("127.0.0.1:%d", ports[backendListener]))
		seedE2E(t, binary, config)
		return startE2E(t, binary, config)
	})

	base := fmt.Sprintf("http://127.0.0.1:%d", ports[backendListener])
	token := signIn(t, base, e2eFeatureAccount, e2eFeaturePassword)
	answer := authenticatedGet(t, base+"/api/v1/features", token)
	if answer.status != http.StatusOK {
		t.Fatalf("GET /api/v1/features answered %d, want 200:\n%s", answer.status, answer.body)
	}

	var document struct {
		Features []string `json:"features"`
	}
	if err := json.Unmarshal(answer.body, &document); err != nil {
		t.Fatalf("the feature document is not JSON: %v\n%s", err, answer.body)
	}
	if !slices.Contains(document.Features, smsadmin.ModuleName) {
		t.Errorf("the feature document omits %q, so the console's navigation would offer no "+
			"SMS page: the module is in the roster but its declaration did not reach the "+
			"feature board", smsadmin.ModuleName)
	}
}
