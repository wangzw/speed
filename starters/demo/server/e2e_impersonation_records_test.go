package main

// The impersonation record routes are platform administration: the tenant in
// the path is the data the records are confined to, not the policy domain the
// decision is made in. These cases drive the assembly, whose seed is exactly the
// shape the rule is about: a platform administrator who is not a member of the
// target tenant, and a tenant administrator of that tenant holding no platform
// permission.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// The e2e fixture's target tenant and its tenant administrator. The operator
// is deliberately not a member of acme; the member administers it and holds no
// platform-domain permission.
const (
	e2eTargetTenant     = "acme"
	e2eTargetIdentifier = "member"
	e2eTargetPassword   = "member-dev-secret"
)

// TestE2EImpersonationRecordsStayInThePlatformDomain holds the record routes
// to the platform domain after they moved under the system scope. A platform
// administrator who is not a member of the tenant lists and revokes that
// tenant's records; a tenant administrator of the same tenant, holding no
// platform permission, is refused. The structural condition that a target must
// be a member of the tenant the impersonation is confined to is held beside
// them: an account outside the tenant cannot be impersonated.
func TestE2EImpersonationRecordsStayInThePlatformDomain(t *testing.T) {
	t.Parallel()
	base, operator := e2eRecordsServer(t)
	operatorAuth := map[string]string{"Authorization": "Bearer " + operator}

	memberID := e2eAccountID(t, base, operatorAuth, "username", e2eTargetIdentifier)
	grantID := e2eStartImpersonation(t, base, operatorAuth, memberID)

	// The platform administrator is not a member of acme and still reads the
	// tenant's records, because the decision is made in the platform domain and
	// the tenant is the data the records are confined to.
	listed := send(t, &http.Client{Timeout: probeTimeout}, http.MethodGet,
		base+"/api/v1/system/tenants/"+e2eTargetTenant+"/impersonations", nil, operatorAuth)
	if listed.status != http.StatusOK {
		t.Fatalf("the platform administrator was refused the tenant's records with %d:\n%s",
			listed.status, listed.body)
	}
	if !e2ePageHoldsGrant(t, listed.body, grantID) {
		t.Errorf("the page does not hold the record %s just started:\n%s", grantID, listed.body)
	}

	revoked := send(t, &http.Client{Timeout: probeTimeout}, http.MethodDelete,
		base+"/api/v1/system/tenants/"+e2eTargetTenant+"/impersonations/"+grantID, nil, operatorAuth)
	if revoked.status != http.StatusNoContent {
		t.Fatalf("the platform administrator could not revoke the record: %d\n%s",
			revoked.status, revoked.body)
	}

	// The tenant's own administrator is a member and holds the tenant's
	// management permission, and still cannot read or revoke the records: the
	// action is a platform permission, decided in the platform domain.
	tenantAuth := map[string]string{"Authorization": "Bearer " +
		signIn(t, base, e2eTargetIdentifier, e2eTargetPassword)}
	refusedList := send(t, &http.Client{Timeout: probeTimeout}, http.MethodGet,
		base+"/api/v1/system/tenants/"+e2eTargetTenant+"/impersonations", nil, tenantAuth)
	if refusedList.status != http.StatusForbidden {
		t.Errorf("the tenant administrator reached the tenant's records with %d, want 403:\n%s",
			refusedList.status, refusedList.body)
	}
	refusedRevoke := send(t, &http.Client{Timeout: probeTimeout}, http.MethodDelete,
		base+"/api/v1/system/tenants/"+e2eTargetTenant+"/impersonations/"+grantID, nil, tenantAuth)
	if refusedRevoke.status != http.StatusForbidden {
		t.Errorf("the tenant administrator revoked a record with %d, want 403:\n%s",
			refusedRevoke.status, refusedRevoke.body)
	}

	// The target-membership condition is structural: an enabled account outside
	// the tenant is refused even for the platform administrator.
	outsider := e2eCreateAccount(t, base, operatorAuth, "outsider")
	refusedStart := send(t, &http.Client{Timeout: probeTimeout}, http.MethodPost,
		base+"/api/v1/auth/impersonation", map[string]any{
			"account": outsider, "tenant": e2eTargetTenant, "reason": "not a member",
		}, operatorAuth)
	if refusedStart.status != http.StatusForbidden ||
		!containsJSON(refusedStart.body, "urn:speed:authn:impersonation-restricted") {
		t.Errorf("impersonating an account outside the tenant answered %d, want 403 "+
			"impersonation-restricted:\n%s", refusedStart.status, refusedStart.body)
	}
}

// e2eRecordsServer builds, seeds and serves the assembly and answers its
// address and a signed-in platform administrator's access token.
func e2eRecordsServer(t *testing.T) (base, token string) {
	t.Helper()
	binary := e2eBinary(t)
	var secret string
	_, ports := startServing(t, func(ports []int) *process {
		configPath := e2eConfig(t,
			fmt.Sprintf("127.0.0.1:%d", ports[backendListener]))
		config := "file://" + configPath
		seedE2E(t, binary, config)
		secret = seedE2EOperatorFactors(t, configPath)
		return startE2E(t, binary, config)
	})

	base = fmt.Sprintf("http://127.0.0.1:%d", ports[backendListener])
	return base, signInWithTOTP(t, base, e2eOperatorUsername, e2eOperatorPassword, secret)
}

// e2eAccountID finds one account by its exact login identifier through the
// platform account list, the read the console uses to resolve an account.
func e2eAccountID(t *testing.T, base string, headers map[string]string, kind, value string) string {
	t.Helper()
	listed := send(t, &http.Client{Timeout: probeTimeout}, http.MethodGet,
		base+"/api/v1/system/auth/accounts?identifier_kind="+kind+"&identifier_prefix="+value,
		nil, headers)
	if listed.status != http.StatusOK {
		t.Fatalf("listing accounts by %s %q answered %d:\n%s", kind, value, listed.status, listed.body)
	}
	var page struct {
		Accounts []struct {
			ID          string `json:"id"`
			Identifiers []struct {
				Kind  string `json:"kind"`
				Value string `json:"value"`
			} `json:"identifiers"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(listed.body, &page); err != nil {
		t.Fatalf("the account page is not JSON: %v\n%s", err, listed.body)
	}
	for _, account := range page.Accounts {
		for _, identifier := range account.Identifiers {
			if identifier.Kind == kind && identifier.Value == value {
				return account.ID
			}
		}
	}
	t.Fatalf("no account holds %s %q:\n%s", kind, value, listed.body)
	return ""
}

// e2eStartImpersonation starts an impersonation of account in the target
// tenant and answers the record's identifier.
func e2eStartImpersonation(t *testing.T, base string, headers map[string]string, account string) string {
	t.Helper()
	started := send(t, &http.Client{Timeout: probeTimeout}, http.MethodPost,
		base+"/api/v1/auth/impersonation", map[string]any{
			"account": account, "tenant": e2eTargetTenant, "reason": "a support case",
		}, headers)
	if started.status != http.StatusOK {
		t.Fatalf("starting the impersonation answered %d:\n%s", started.status, started.body)
	}
	var answer struct {
		Grant struct {
			ID string `json:"id"`
		} `json:"grant"`
	}
	if err := json.Unmarshal(started.body, &answer); err != nil {
		t.Fatalf("the start answer is not JSON: %v\n%s", err, started.body)
	}
	if answer.Grant.ID == "" {
		t.Fatalf("the start answer carries no record identifier:\n%s", started.body)
	}
	return answer.Grant.ID
}

// e2eCreateAccount creates a user account with no membership anywhere and
// answers its identifier.
func e2eCreateAccount(t *testing.T, base string, headers map[string]string, username string) string {
	t.Helper()
	created := send(t, &http.Client{Timeout: probeTimeout}, http.MethodPost,
		base+"/api/v1/system/auth/accounts", map[string]any{
			"kind": "user", "username": username, "password": "outsider-dev-secret",
		}, headers)
	if created.status != http.StatusCreated {
		t.Fatalf("creating the account %q answered %d:\n%s", username, created.status, created.body)
	}
	var answer struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
	}
	if err := json.Unmarshal(created.body, &answer); err != nil {
		t.Fatalf("the create answer is not JSON: %v\n%s", err, created.body)
	}
	if answer.Account.ID == "" {
		t.Fatalf("the create answer carries no account identifier:\n%s", created.body)
	}
	return answer.Account.ID
}

// e2ePageHoldsGrant reports whether one page of impersonation records holds
// grant.
func e2ePageHoldsGrant(t *testing.T, body []byte, grant string) bool {
	t.Helper()
	var page struct {
		Impersonations []struct {
			ID string `json:"id"`
		} `json:"impersonations"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("the impersonation page is not JSON: %v\n%s", err, body)
	}
	for _, record := range page.Impersonations {
		if record.ID == grant {
			return true
		}
	}
	return false
}

// containsJSON reports whether body carries want.
func containsJSON(body []byte, want string) bool {
	return strings.Contains(string(body), want)
}
