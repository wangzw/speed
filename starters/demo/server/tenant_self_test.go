package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tenantself "github.com/vislake/speed/pkg/tenant/self"
)

// TestTheCallerTenantReadIsOnTheAccountEndpoint holds the route and the
// contract together: the account endpoint answers the read for an authenticated
// caller with no membership, and the served document declares the operation
// against that endpoint. Either one alone would pass over a module that mounted
// the route somewhere the fragment does not describe, or declared it without
// mounting it.
func TestTheCallerTenantReadIsOnTheAccountEndpoint(t *testing.T) {
	ports := freePorts(t, listenerCount)
	dsn := filepath.Join(t.TempDir(), "demo.db")
	config := writeConfigWith(t, ports, dsn)
	if res := runCommand(t, config, "sqlite-memory", seedCommand); res.err != nil {
		t.Fatalf("seed failed: %v\n%s", res.err, res.combined())
	}
	serve := start(t, config, "sqlite-memory", serveCommand)
	serve.waitReady(t)

	base := fmt.Sprintf("http://127.0.0.1:%d", ports[backendListener])
	token := signIn(t, base, hashIdentifier, seedPassword)

	read := send(t, &http.Client{Timeout: 10 * time.Second}, http.MethodGet,
		base+"/api/v1/auth/me/tenants", nil,
		map[string]string{"Authorization": "Bearer " + token})
	if read.status != http.StatusOK {
		t.Fatalf("GET /api/v1/auth/me/tenants on the account endpoint answered %d, want 200:\n%s",
			read.status, read.body)
	}
	var answer struct {
		Tenants []json.RawMessage `json:"tenants"`
	}
	if err := json.Unmarshal(read.body, &answer); err != nil {
		t.Fatalf("the answer is not JSON: %v\n%s", err, read.body)
	}
	if answer.Tenants == nil {
		t.Errorf("the answer carries a null tenant list, want []:\n%s", read.body)
	}
	if len(answer.Tenants) != 0 {
		t.Errorf("the account with no membership answered %d tenant(s), want none:\n%s",
			len(answer.Tenants), read.body)
	}

	document := get(t, fmt.Sprintf("http://127.0.0.1:%d/openapi.json", ports[backendListener]))
	if document.status != http.StatusOK {
		t.Fatalf("GET /openapi.json answered %d, want 200:\n%s", document.status, document.body)
	}
	var parsed struct {
		Paths map[string]map[string]struct {
			OperationID string `json:"operationId"`
			Endpoint    string `json:"x-endpoint"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(document.body, &parsed); err != nil {
		t.Fatalf("the served document is not JSON: %v\n%s", err, document.body)
	}
	operation, ok := parsed.Paths["/api/v1/auth/me/tenants"]["get"]
	if !ok {
		t.Fatalf("the served document declares no GET /api/v1/auth/me/tenants:\n%s", document.body)
	}
	if operation.OperationID != "tenant.self.tenants.list" {
		t.Errorf("the operation is %q, want tenant.self.tenants.list", operation.OperationID)
	}
	if operation.Endpoint != "account" {
		t.Errorf("the operation is declared on the %q endpoint, want account", operation.Endpoint)
	}
}

// TestThePersonalAssemblyCarriesTheCallerTenantRead holds the ruling that a
// deployment enabling the personal tenant must also name tenant.self: the
// ensure is triggered by the caller's tenant list having settled and being
// empty, which no assembly can tell without this read.
func TestThePersonalAssemblyCarriesTheCallerTenantRead(t *testing.T) {
	if !slices.Contains(baselineServe, tenantself.ModuleName) {
		t.Fatalf("baselineServe does not name %q, so an assembly that enables the personal tenant "+
			"cannot tell that the caller holds no membership", tenantself.ModuleName)
	}

	// The personal assembly answers the read through the program: the seeded
	// account reads its own personal tenant, which the seed's account-creation
	// hook built before any browser called the backfill.
	base, token := personalServe(t, true)
	read := send(t, &http.Client{Timeout: 10 * time.Second}, http.MethodGet,
		base+"/api/v1/auth/me/tenants", nil,
		map[string]string{"Authorization": "Bearer " + token})
	if read.status != http.StatusOK {
		t.Fatalf("the personal assembly answered GET /api/v1/auth/me/tenants with %d:\n%s",
			read.status, read.body)
	}
	var answer struct {
		Tenants []struct {
			ID string `json:"id"`
		} `json:"tenants"`
	}
	if err := json.Unmarshal(read.body, &answer); err != nil {
		t.Fatalf("the answer is not JSON: %v\n%s", err, read.body)
	}
	if len(answer.Tenants) != 1 || !strings.HasPrefix(answer.Tenants[0].ID, personalIDPrefix) {
		t.Errorf("the seeded account answered %d tenant(s), want its one personal tenant:\n%s",
			len(answer.Tenants), read.body)
	}
}
