//go:build review

package main

// The review build's SMS management page: sms.admin is named for the serve
// roster, and the running binary publishes its feature. The feature document is
// what the console's navigation reads before it offers the page, so publishing
// the name is the condition a reviewer's browser needs. The baseline imports the
// module for its contract and names it on no command, so this case also holds
// the boundary that keeps the page out of a normal deployment.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"

	smsadmin "github.com/vislake/speed/pkg/sms/admin"
)

// The review manifest's member account. The operator the review manifest seeds
// carries the impersonation fixture's two second factors, so a password alone
// does not complete its sign-in; the member has none, and the feature document
// asks only for an authenticated caller.
const (
	reviewFeatureAccount  = "member"
	reviewFeaturePassword = "member-dev-secret"
)

// TestReviewServesTheSMSAdminPage holds the roster entry and the published
// feature: the module is in the capability ceiling every build carries, and a
// running review binary answers GET /api/v1/features with sms.admin.
func TestReviewServesTheSMSAdminPage(t *testing.T) {
	if !slices.Contains(capabilityModules, smsadmin.ModuleName) {
		t.Fatalf("the capability ceiling names no %q module, so the review binary would publish no "+
			"SMS management feature and the console would offer no page", smsadmin.ModuleName)
	}

	binary := reviewBinary(t)
	ports := freePorts(t, listenerCount)
	path := reviewConfig(t,
		fmt.Sprintf("127.0.0.1:%d", ports[backendListener]),
		fmt.Sprintf("127.0.0.1:%d", ports[opsListener]))
	config := "file://" + path
	seedReview(t, binary, config)

	serve := startReview(t, binary, config)
	serve.waitReady(t)

	base := fmt.Sprintf("http://127.0.0.1:%d", ports[backendListener])
	token := signIn(t, base, reviewFeatureAccount, reviewFeaturePassword)
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
