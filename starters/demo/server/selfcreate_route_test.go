package main

// The program-level cases for the self-service creation module: the host
// assembles it rather than implementing it, the route's hourly bound answers,
// the switch withdraws both the route and the feature, and the real account,
// roster, policy, settings and key-value modules answer behind it.

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"

	_ "github.com/vislake/speed/pkg/db/sqlite" // registers the sqlite driver these cases read the assembly's rows through
	tenantselfcreate "github.com/vislake/speed/pkg/tenant/selfcreate"
)

// TestTheSelfServiceCreationRouteRateLimits runs the default assembly: the
// seeded tenant's name is a 409, three creations are admitted, the fourth is
// the hourly refusal, and the feature document carries org.create.
func TestTheSelfServiceCreationRouteRateLimits(t *testing.T) {
	t.Parallel()
	base, token := seededServe(t, "")
	client := &http.Client{Timeout: probeTimeout()}
	headers := map[string]string{"Authorization": "Bearer " + token}

	// The seeded business tenant already carries the name, and the roster's one
	// namespace answers identifier-unavailable.
	held := send(t, client, http.MethodPost, base+"/api/v1/orgs",
		map[string]string{"name": "demo", "display_name": "Demo"}, headers)
	if held.status != http.StatusConflict {
		t.Fatalf("the held name answered %d, want 409:\n%s", held.status, held.body)
	}
	if !jsonContains(held.body, "identifier-unavailable") {
		t.Errorf("the held name does not carry identifier-unavailable:\n%s", held.body)
	}

	for i := range 3 {
		res := send(t, client, http.MethodPost, base+"/api/v1/orgs",
			map[string]string{
				"name":         fmt.Sprintf("team-%d", i),
				"display_name": fmt.Sprintf("Team %d", i),
			}, headers)
		if res.status != http.StatusCreated {
			t.Fatalf("creation %d answered %d, want 201:\n%s", i, res.status, res.body)
		}
	}
	limited := send(t, client, http.MethodPost, base+"/api/v1/orgs",
		map[string]string{"name": "team-four", "display_name": "Team Four"}, headers)
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
	t.Parallel()
	base, token := seededServe(t, "org:\n  create:\n    enabled: false\n")
	client := &http.Client{Timeout: probeTimeout()}
	headers := map[string]string{"Authorization": "Bearer " + token}

	absent := send(t, client, http.MethodPost, base+"/api/v1/orgs",
		map[string]string{"name": "team-one", "display_name": "Team One"}, headers)
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

// creationTables are the tables the creation path can write. A refusal that
// comes before the attempt is counted leaves every one of them as it was.
var creationTables = []string{
	"tenants",
	"authn_memberships",
	"authz_roles",
	"authz_grants",
	"authz_bindings",
	"audit_entries",
}

// TestBothCreationRefusalsWriteNoRows runs the default assembly and reaches
// both of its bounds. The seeded hourly bound answers first: three creations are
// admitted and the fourth is the hourly refusal. The bound is then raised to ten
// through the platform settings surface, two more creations fill the
// five-organization cap, and the sixth is the organization refusal. Each refusal
// is measured over the six tables the creation path writes, before and after:
// neither adds a row.
func TestBothCreationRefusalsWriteNoRows(t *testing.T) {
	t.Parallel()
	base, token, dsn := seededServeWithDatabase(t, "")
	client := &http.Client{Timeout: probeTimeout()}
	headers := map[string]string{"Authorization": "Bearer " + token}
	create := func(name string) response {
		t.Helper()
		return send(t, client, http.MethodPost, base+"/api/v1/orgs",
			map[string]string{"name": name, "display_name": name}, headers)
	}

	// The seeded hourly bound of three admits three creations.
	for i := range 3 {
		if res := create(fmt.Sprintf("team-%d", i)); res.status != http.StatusCreated {
			t.Fatalf("creation %d answered %d, want 201:\n%s", i, res.status, res.body)
		}
	}
	beforeHourly := tableRowCounts(t, dsn)
	hourly := create("team-three")
	if hourly.status != http.StatusTooManyRequests {
		t.Fatalf("the fourth creation answered %d, want 429:\n%s", hourly.status, hourly.body)
	}
	if !jsonContains(hourly.body, "rate-limited") {
		t.Errorf("the hourly refusal does not carry rate-limited:\n%s", hourly.body)
	}
	assertRowCounts(t, dsn, beforeHourly, "the hourly refusal")

	// Raise the hourly bound so the organization cap is the next refusal. The
	// write is the platform surface an operator uses, not a test hook.
	operator := signIn(t, base, seedIdentifier, seedPassword)
	raised := send(t, client, http.MethodPut,
		base+"/api/v1/system/settings/"+tenantselfcreate.SettingsNamespace+"/max-per-hour?layer=global",
		map[string]any{"value": 10},
		map[string]string{"Authorization": "Bearer " + operator})
	if raised.status != http.StatusNoContent {
		t.Fatalf("raising the hourly bound answered %d, want 204:\n%s", raised.status, raised.body)
	}

	// Two more creations bring the account to five active organizations.
	for i := 3; i < 5; i++ {
		if res := create(fmt.Sprintf("team-%d", i)); res.status != http.StatusCreated {
			t.Fatalf("creation %d answered %d, want 201:\n%s", i, res.status, res.body)
		}
	}
	beforeOrganization := tableRowCounts(t, dsn)
	over := create("team-five")
	if over.status != http.StatusConflict {
		t.Fatalf("the sixth creation answered %d, want 409:\n%s", over.status, over.body)
	}
	if !jsonContains(over.body, "organization-limit") {
		t.Errorf("the organization refusal does not carry organization-limit:\n%s", over.body)
	}
	assertRowCounts(t, dsn, beforeOrganization, "the organization-limit refusal")
}

// tableRowCounts reads the row count of every table the creation path writes.
// The connection is read-only and the cases read it between requests, so it
// observes the rows the last answered request left.
func tableRowCounts(t *testing.T, dsn string) map[string]int64 {
	t.Helper()
	database, err := sql.Open("sqlite", "file:"+dsn+"?mode=ro")
	if err != nil {
		t.Fatalf("opening the assembly's database: %v", err)
	}
	defer func() { _ = database.Close() }()
	database.SetMaxOpenConns(1)
	counts := make(map[string]int64, len(creationTables))
	for _, table := range creationTables {
		var n int64
		if err := database.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
			t.Fatalf("counting %s: %v", table, err)
		}
		counts[table] = n
	}
	return counts
}

// assertRowCounts holds every table the creation path writes to the counts taken
// before the refusal, naming the refusal in every failure.
func assertRowCounts(t *testing.T, dsn string, before map[string]int64, what string) {
	t.Helper()
	after := tableRowCounts(t, dsn)
	for _, table := range creationTables {
		if after[table] != before[table] {
			t.Errorf("%s added a row to %s: %d before, %d after", what, table, before[table], after[table])
		}
	}
}

// TestThePersonalRouteIsNotMounted runs the default assembly: the personal
// tenant is written inside the account's transaction, so the account endpoint
// mounts no personal-tenant route and a POST there is the endpoint's own 404.
func TestThePersonalRouteIsNotMounted(t *testing.T) {
	t.Parallel()
	base, token := seededServe(t, "")
	client := &http.Client{Timeout: probeTimeout()}
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
