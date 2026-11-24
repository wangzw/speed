package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"testing"

	"github.com/vislake/speed/pkg/tenant"
	tenantself "github.com/vislake/speed/pkg/tenant/self"
)

// TestTheCallerTenantReadIsOnTheAccountEndpoint holds the route and the served contract together.
func TestTheCallerTenantReadIsOnTheAccountEndpoint(t *testing.T) {
	t.Parallel()
	dsn := filepath.Join(t.TempDir(), "demo.db")
	config := writeConfigFile(t, configBody(t, freePorts(t, listenerCount), dsn))
	if res := runCommand(t, config, "sqlite-memory", seedCommand); res.err != nil {
		t.Fatalf("seed failed: %v\n%s", res.err, res.combined())
	}
	_, ports := startServing(t, func(ports []int) *process {
		return start(t, writeConfigFile(t, configBody(t, ports, dsn)), "sqlite-memory", serveCommand)
	})

	base := fmt.Sprintf("http://127.0.0.1:%d", ports[backendListener])
	token := signIn(t, base, hashIdentifier, seedPassword)

	read := send(t, &http.Client{Timeout: probeTimeout()}, http.MethodGet,
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
	if len(answer.Tenants) == 0 {
		t.Errorf("the caller answered no tenant(s), want its personal tenant:\n%s", read.body)
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

// TestThePersonalAssemblyCarriesTheCallerTenantRead holds that an assembly with personal tenants also
// names tenant.self.
func TestThePersonalAssemblyCarriesTheCallerTenantRead(t *testing.T) {
	t.Parallel()
	if !slices.Contains(baselineServe, tenantself.ModuleName) {
		t.Fatalf("baselineServe does not name %q, so an assembly that carries personal tenants "+
			"cannot list the caller's own membership", tenantself.ModuleName)
	}

	base, token := seededServe(t, "")
	read := send(t, &http.Client{Timeout: probeTimeout()}, http.MethodGet,
		base+"/api/v1/auth/me/tenants", nil,
		map[string]string{"Authorization": "Bearer " + token})
	if read.status != http.StatusOK {
		t.Fatalf("the personal assembly answered GET /api/v1/auth/me/tenants with %d:\n%s",
			read.status, read.body)
	}
	var answer struct {
		Tenants []struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
		} `json:"tenants"`
	}
	if err := json.Unmarshal(read.body, &answer); err != nil {
		t.Fatalf("the answer is not JSON: %v\n%s", err, read.body)
	}
	if len(answer.Tenants) != 1 || answer.Tenants[0].Name == "" {
		t.Errorf("the seeded account answered %d tenant(s), want its one personal tenant:\n%s",
			len(answer.Tenants), read.body)
	}
	if len(answer.Tenants) == 1 && answer.Tenants[0].Kind != string(tenant.KindPersonal) {
		t.Errorf("the personal tenant's kind is %q, want personal:\n%s",
			answer.Tenants[0].Kind, read.body)
	}
}
