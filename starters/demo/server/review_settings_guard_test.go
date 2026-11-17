//go:build review

package main

// The review build's read-only business tenant, held through the program's own
// seams.
//
// The settings write guard refuses the protected- prefix while every other
// management seam admits the tenant, so organization administration opens and
// the settings surface is read-only. The tenant and its administrator are a
// fixture this case assembles on top of the review configuration; the shared
// review/sqlite.yaml stays as it is because the review journeys are built on its
// exact seed.
//
// The case drives the assembled program, not the guard alone: the tenant's
// administrator has to reach the member list, the settings read has to report
// the surface read-only, and a write with permission has to be refused by the
// host seam. The ordinary tenant is driven alongside it, and the personal
// tenant's authorization refusal is checked, so the fixture cannot pass by
// making every tenant read-only.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/tenant"
	tenantpersonal "github.com/vislake/speed/pkg/tenant/personal"
)

// protectedReviewTenant is the protected- prefixed business tenant this case
// assembles. Its identifier is also the name it is addressed by, so the tenant
// in the path and the X-Tenant header name the same domain.
const protectedReviewTenant = "protected-acme"

const (
	protectedReviewAdminIdentifier = "protected-admin@demo.test"
	protectedReviewAdminLogin      = "protected-admin"
	protectedReviewAdminPassword   = "protected-admin-secret"
)

// TestReviewSettingsWriteGuardRefusesTheProtectedFixture holds the guard split
// without a server: the review build refuses the protected- prefix, the ordinary
// business tenant stays writable, and the personal domain keeps its own refusal.
// The management guards admit the protected tenant, which is what lets it enter
// organization administration; only the settings write is refused.
func TestReviewSettingsWriteGuardRefusesTheProtectedFixture(t *testing.T) {
	t.Parallel()
	guard := settingsWriteGuard{}
	ctx := context.Background()
	for _, domain := range []string{protectedTestTenantPrefix + "acme", protectedTestTenantPrefix} {
		denied, err := guard.Denied(ctx, domain)
		if err != nil {
			t.Fatalf("Denied(%q): %v", domain, err)
		}
		if !denied {
			t.Errorf("Denied(%q) = false, want the protected test tenant refused", domain)
		}
	}
	for _, domain := range []string{"acme", string(authz.PlatformDomain), "protected", "acme-protected"} {
		denied, err := guard.Denied(ctx, domain)
		if err != nil {
			t.Fatalf("Denied(%q): %v", domain, err)
		}
		if denied {
			t.Errorf("Denied(%q) = true, want an ordinary business tenant allowed", domain)
		}
	}
	for _, domain := range []string{tenantpersonal.IDPrefix + "acct-1", tenantpersonal.IDPrefix} {
		denied, err := guard.Denied(ctx, domain)
		if err != nil {
			t.Fatalf("Denied(%q): %v", domain, err)
		}
		if !denied {
			t.Errorf("Denied(%q) = false, want the personal domain still refused", domain)
		}
	}

	// The prefix is read-only on the settings surface alone: the management
	// surface's write guard and the tenant write guard both admit the tenant,
	// which is what lets it enter organization administration at all.
	tenantID := protectedTestTenantPrefix + "acme"
	policy := policyWriteGuard{}
	for _, op := range []authz.WriteOp{authz.WriteRole, authz.WriteGrant, authz.WriteBinding} {
		denied, err := policy.Denied(ctx, tenantID, op)
		if err != nil {
			t.Fatalf("the management write guard refused %q with an error: %v", tenantID, err)
		}
		if denied {
			t.Errorf("the management write guard denies %q op %d, want the tenant admitted", tenantID, op)
		}
	}
	product := tenancyProductOf(newFakeRoster(), newFakeMemberships(), nil, nil, nil)
	for _, op := range []tenant.WriteOp{tenant.WriteDisable, tenant.WriteDelete} {
		denied, err := product.Denied(ctx, tenant.ID(tenantID), op)
		if err != nil {
			t.Fatalf("the tenant write guard refused %q with an error: %v", tenantID, err)
		}
		if denied {
			t.Errorf("the tenant write guard denies %q op %d, want the tenant admitted", tenantID, op)
		}
	}
}

// TestReviewProtectedTenantIsReadOnlyOnTheSettingsSurface builds the review
// program over a configuration that adds protected-acme and its administrator.
// The administrator enters the tenant's member list and reads the two admission
// policies, but the surface reports itself read-only and its write is refused
// with 403 protected. The ordinary tenant answers writable and accepts the same
// write, and the account's personal tenant keeps its authorization refusal.
func TestReviewProtectedTenantIsReadOnlyOnTheSettingsSurface(t *testing.T) {
	t.Parallel()
	_, ports := startServing(t, func(ports []int) *process {
		root := t.TempDir()
		dsn := filepath.Join(root, "review-protected.db")
		body := reviewConfigBody(t, root,
			fmt.Sprintf("127.0.0.1:%d", ports[backendListener]), dsn)
		body = addProtectedReviewFixture(t, body)
		path := filepath.Join(root, "review.yaml")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("writing the review configuration: %v", err)
		}
		config := "file://" + path
		seedReview(t, reviewHostBinary, config)
		return startReview(t, reviewHostBinary, config)
	})

	base := fmt.Sprintf("http://127.0.0.1:%d", ports[backendListener])
	token := signIn(t, base, protectedReviewAdminLogin, protectedReviewAdminPassword)
	client := &http.Client{Timeout: probeTimeout}
	auth := map[string]string{"Authorization": "Bearer " + token}

	// The tenant's administrator reaches the organization management surface:
	// the protected prefix is a settings write policy, not an admission rule.
	members := send(t, client, http.MethodGet,
		base+"/api/v1/tenants/"+protectedReviewTenant+"/members", nil,
		withTenant(auth, protectedReviewTenant))
	if members.status != http.StatusOK {
		t.Fatalf("GET the protected tenant's members answered %d, want 200:\n%s",
			members.status, members.body)
	}

	// The settings read lists the two admission policies and reports the whole
	// surface read-only.
	answer := readTenantSettings(t, client, base,
		withTenant(auth, protectedReviewTenant), protectedReviewTenant)
	if answer.Writable {
		t.Error("the protected tenant's settings surface reports writable=true")
	}
	if answer.ReadOnlyReason != "protected" {
		t.Errorf("read_only_reason = %q, want protected", answer.ReadOnlyReason)
	}
	if len(answer.Settings) != 2 {
		t.Errorf("the protected tenant's settings list holds %d item(s), want the two admission policies",
			len(answer.Settings))
	}

	// A write the administrator is permitted to make is refused by the host
	// seam, before the audit and the store.
	write := send(t, client, http.MethodPut,
		base+"/api/v1/tenants/"+protectedReviewTenant+"/settings/tenant.admission/require-mfa",
		map[string]any{"value": true},
		withTenant(auth, protectedReviewTenant))
	if write.status != http.StatusForbidden {
		t.Fatalf("the protected tenant's settings write answered %d, want 403:\n%s",
			write.status, write.body)
	}
	var problem struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(write.body, &problem); err != nil {
		t.Fatalf("the refusal is not JSON: %v\n%s", err, write.body)
	}
	if problem.Type != "urn:speed:settings:protected" {
		t.Errorf("the refusal type is %q, want urn:speed:settings:protected", problem.Type)
	}

	// The ordinary business tenant is unchanged: the same write is accepted.
	ordinary := readTenantSettings(t, client, base,
		withTenant(auth, "acme"), "acme")
	if !ordinary.Writable {
		t.Errorf("the ordinary tenant's settings surface reports writable=false, reason %q",
			ordinary.ReadOnlyReason)
	}
	ordinaryWrite := send(t, client, http.MethodPut,
		base+"/api/v1/tenants/acme/settings/tenant.admission/require-mfa",
		map[string]any{"value": true},
		withTenant(auth, "acme"))
	if ordinaryWrite.status != http.StatusNoContent {
		t.Fatalf("the ordinary tenant's settings write answered %d, want 204:\n%s",
			ordinaryWrite.status, ordinaryWrite.body)
	}

	// The personal tenant keeps its own semantics. Its tenant administrator
	// holds no settings permission, so the route's authorization refuses the
	// read before the host seam is asked; the seam's personal refusal itself is
	// pinned by TestReviewSettingsWriteGuardRefusesTheProtectedFixture.
	personalName := personalTenantNameOf(t, client, base, auth)
	personalRead := send(t, client, http.MethodGet,
		base+"/api/v1/tenants/"+personalName+"/settings", nil,
		withTenant(auth, personalName))
	if personalRead.status != http.StatusForbidden {
		t.Fatalf("the personal tenant's settings read answered %d, want the authorization refusal:\n%s",
			personalRead.status, personalRead.body)
	}
	var personalProblem struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(personalRead.body, &personalProblem); err != nil {
		t.Fatalf("the personal refusal is not JSON: %v\n%s", err, personalRead.body)
	}
	if personalProblem.Type != "urn:speed:authz:forbidden" {
		t.Errorf("the personal refusal type is %q, want urn:speed:authz:forbidden", personalProblem.Type)
	}
}

// addProtectedReviewFixture adds the protected tenant and its administrator to
// the review configuration body. The administrator is a proved-email account,
// which is what makes its settings write reach the host seam instead of being
// stopped as an unverified self-registration's. The tenant is added beside
// acme, and the administrator is a second administrator of acme so the case can
// drive the ordinary tenant with the same session.
func addProtectedReviewFixture(t *testing.T, body string) string {
	t.Helper()
	accountNeedle := "      display-name: Demo Member\n  tenants:"
	accountReplacement := "      display-name: Demo Member\n" +
		"    - identifier: \"" + protectedReviewAdminIdentifier + "\"\n" +
		"      username: \"" + protectedReviewAdminLogin + "\"\n" +
		"      password: \"" + protectedReviewAdminPassword + "\"\n" +
		"      display-name: \"Protected Tenant Administrator\"\n" +
		"      proved-email: true\n" +
		"  tenants:"
	if !strings.Contains(body, accountNeedle) {
		t.Fatalf("the review configuration holds no account block to extend")
	}
	body = strings.Replace(body, accountNeedle, accountReplacement, 1)

	tenantNeedle := "    - name: acme\n      admins:\n        - member\n"
	tenantReplacement := "    - name: acme\n      admins:\n        - member\n" +
		"        - " + protectedReviewAdminIdentifier + "\n" +
		"    - name: " + protectedReviewTenant + "\n" +
		"      admins:\n        - " + protectedReviewAdminIdentifier + "\n"
	if !strings.Contains(body, tenantNeedle) {
		t.Fatalf("the review configuration holds no acme tenant block to extend")
	}
	return strings.Replace(body, tenantNeedle, tenantReplacement, 1)
}

// tenantSettingsAnswer is the tenant settings read's envelope.
type tenantSettingsAnswer struct {
	Settings []struct {
		Key string `json:"key"`
	} `json:"settings"`
	Writable       bool   `json:"writable"`
	ReadOnlyReason string `json:"read_only_reason"`
}

// readTenantSettings reads one tenant's settings surface and decodes it.
func readTenantSettings(t *testing.T, client *http.Client, base string,
	headers map[string]string, tenantID string,
) tenantSettingsAnswer {
	t.Helper()
	read := send(t, client, http.MethodGet,
		base+"/api/v1/tenants/"+tenantID+"/settings", nil, headers)
	if read.status != http.StatusOK {
		t.Fatalf("GET %s's settings answered %d, want 200:\n%s", tenantID, read.status, read.body)
	}
	var answer tenantSettingsAnswer
	if err := json.Unmarshal(read.body, &answer); err != nil {
		t.Fatalf("the settings answer is not JSON: %v\n%s", err, read.body)
	}
	return answer
}

// personalTenantNameOf reads the caller's personal tenant name from the account's own
// tenant list.
func personalTenantNameOf(t *testing.T, client *http.Client, base string,
	headers map[string]string,
) string {
	t.Helper()
	read := send(t, client, http.MethodGet, base+"/api/v1/auth/me/tenants", nil, headers)
	if read.status != http.StatusOK {
		t.Fatalf("GET the caller's tenants answered %d, want 200:\n%s", read.status, read.body)
	}
	var answer struct {
		Tenants []struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
		} `json:"tenants"`
	}
	if err := json.Unmarshal(read.body, &answer); err != nil {
		t.Fatalf("the tenant list is not JSON: %v\n%s", err, read.body)
	}
	for _, entry := range answer.Tenants {
		if entry.Kind == string(tenant.KindPersonal) {
			return entry.Name
		}
	}
	t.Fatalf("the caller answered no personal tenant:\n%s", read.body)
	return ""
}
