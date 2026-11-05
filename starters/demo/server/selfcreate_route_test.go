package main

// The program-level cases for the self-service creation module: the host
// assembles it rather than implementing it, the route's hourly bound answers,
// the switch withdraws both the route and the feature, and the real account,
// roster, policy, settings and key-value modules answer behind it.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	tenantselfcreate "github.com/vislake/speed/pkg/tenant/selfcreate"
)

// TestTheSelfServiceCreationRouteRateLimits runs the default assembly: the
// seeded tenant's alias is a 409, three creations are admitted, the fourth is
// the hourly refusal, and the feature document carries org.create.
func TestTheSelfServiceCreationRouteRateLimits(t *testing.T) {
	base, token := seededServe(t, "")
	client := &http.Client{Timeout: 10 * time.Second}
	headers := map[string]string{"Authorization": "Bearer " + token}

	// The seeded business tenant already carries the alias, and the roster's one
	// namespace answers identifier-unavailable.
	held := send(t, client, http.MethodPost, base+"/api/v1/orgs",
		map[string]string{"alias": "demo", "display_name": "Demo"}, headers)
	if held.status != http.StatusConflict {
		t.Fatalf("the held alias answered %d, want 409:\n%s", held.status, held.body)
	}
	if !jsonContains(held.body, "identifier-unavailable") {
		t.Errorf("the held alias does not carry identifier-unavailable:\n%s", held.body)
	}

	for i := range 3 {
		res := send(t, client, http.MethodPost, base+"/api/v1/orgs",
			map[string]string{
				"alias":        fmt.Sprintf("team-%d", i),
				"display_name": fmt.Sprintf("Team %d", i),
			}, headers)
		if res.status != http.StatusCreated {
			t.Fatalf("creation %d answered %d, want 201:\n%s", i, res.status, res.body)
		}
	}
	limited := send(t, client, http.MethodPost, base+"/api/v1/orgs",
		map[string]string{"alias": "team-four", "display_name": "Team Four"}, headers)
	if limited.status != http.StatusTooManyRequests {
		t.Fatalf("the fourth creation answered %d, want 429:\n%s", limited.status, limited.body)
	}
	if !jsonContains(limited.body, "rate-limited") {
		t.Errorf("the rate refusal does not carry rate-limited:\n%s", limited.body)
	}

	features := authenticatedGet(t, base+"/api/v1/features", token)
	var document struct {
		Features []string `json:"features"`
	}
	if err := json.Unmarshal(features.body, &document); err != nil {
		t.Fatalf("the feature document is not JSON: %v\n%s", err, features.body)
	}
	if !slices.Contains(document.Features, tenantselfcreate.FeatureName) {
		t.Errorf("the feature document does not carry %q: %v", tenantselfcreate.FeatureName, document.Features)
	}
}

// TestTheSelfServiceCreationRouteIsAbsentWhenTheSwitchIsOff runs the program
// with org.create.enabled false: the route is a 404, and the feature is
// absent.
func TestTheSelfServiceCreationRouteIsAbsentWhenTheSwitchIsOff(t *testing.T) {
	base, token := seededServe(t, "org:\n  create:\n    enabled: false\n")
	client := &http.Client{Timeout: 10 * time.Second}
	headers := map[string]string{"Authorization": "Bearer " + token}

	absent := send(t, client, http.MethodPost, base+"/api/v1/orgs",
		map[string]string{"alias": "team-one", "display_name": "Team One"}, headers)
	if absent.status != http.StatusNotFound {
		t.Fatalf("POST /api/v1/orgs with the switch off answered %d, want 404:\n%s", absent.status, absent.body)
	}

	features := authenticatedGet(t, base+"/api/v1/features", token)
	var document struct {
		Features []string `json:"features"`
	}
	if err := json.Unmarshal(features.body, &document); err != nil {
		t.Fatalf("the feature document is not JSON: %v\n%s", err, features.body)
	}
	for _, name := range document.Features {
		if name == tenantselfcreate.FeatureName {
			t.Errorf("the feature document carries %q while the switch is off", name)
		}
	}
}

// TestThePersonalRouteIsNotMounted runs the default assembly: the personal
// tenant is written inside the account's transaction, so the account endpoint
// mounts no personal-tenant route and a POST there is the endpoint's own 404.
func TestThePersonalRouteIsNotMounted(t *testing.T) {
	base, token := seededServe(t, "")
	client := &http.Client{Timeout: 10 * time.Second}
	res := send(t, client, http.MethodPost, base+"/api/v1/orgs/personal", nil,
		map[string]string{"Authorization": "Bearer " + token})
	if res.status != http.StatusNotFound {
		t.Errorf("POST /api/v1/orgs/personal answered %d, want 404:\n%s", res.status, res.body)
	}
}

// jsonContains reports whether a response body carries the text, so a case can
// name a problem code without decoding the whole problem document.
func jsonContains(body []byte, text string) bool {
	return bytes.Contains(body, []byte(text))
}
