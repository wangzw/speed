package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"

	smsadmin "github.com/vislake/speed/pkg/sms/admin"
)

const (
	e2eFeatureAccount  = "member"
	e2eFeaturePassword = "member-dev-secret"
)

// TestE2EServesTheSMSAdminPage holds that sms.admin is in the ceiling and a running binary publishes
// its feature.
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
