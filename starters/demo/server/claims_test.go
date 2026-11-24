package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/starters/demo/server/internal/demotest"
)

var testdataFiles = []string{
	"sqlite-memory.yaml",
	"sqlite.yaml",
	"postgres.yaml",
	"postgres-redis-nats.yaml",
	"postgres-nats-redis.yaml",
	"postgres-memcached-nats.yaml",
}

type endpointClaim struct {
	listener string
	prefixes []string
}

type claimsCarrier struct {
	Endpoints map[string]struct {
		Listener        string   `config:"listener"`
		Prefixes        []string `config:"prefixes"`
		MaxBodyBytes    *int64   `config:"max-body-bytes"`
		TrustedProxies  []string `config:"trusted-proxies"`
		RemoteIPHeaders []string `config:"remote-ip-headers"`
	} `config:"endpoints"`
}

func readEffective(t *testing.T, name, locator, path string, target any) {
	t.Helper()
	reader, err := config.Load(t.Context(), core.ProcessRegistry, []string{"--config", locator})
	if err != nil {
		t.Fatalf("loading the effective configuration of %s: %v", name, err)
	}
	if err := reader.Decode(path, target); err != nil {
		t.Fatalf("decoding %s from the effective configuration of %s: %v", path, name, err)
	}
}

func claimsFromLocator(t *testing.T, name, locator string) map[string]endpointClaim {
	t.Helper()
	var parsed claimsCarrier
	readEffective(t, name, locator, "http", &parsed)
	if len(parsed.Endpoints) == 0 {
		t.Fatalf("%s declares no http.endpoints, in the file or in the defaults document", name)
	}
	claims := make(map[string]endpointClaim, len(parsed.Endpoints))
	for endpoint, declared := range parsed.Endpoints {
		prefixes := declared.Prefixes
		if prefixes == nil {
			prefixes = []string{"/"}
		}
		claims[endpoint] = endpointClaim{listener: declared.Listener, prefixes: prefixes}
	}
	return claims
}

func claimsFromFile(t *testing.T, path string) map[string]endpointClaim {
	t.Helper()
	doc, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return claimsFromConfig(t, path, doc)
}

func claimsFromConfig(t *testing.T, name string, doc []byte) map[string]endpointClaim {
	t.Helper()
	return claimsFromLocator(t, name, writeConfigFile(t, string(doc)))
}

// selectClaimedEndpoint is the request-time selection rule: the longest non-root
// claim a path falls under wins, a path under none goes to the endpoint claiming
// "/", and comparison is at segment boundaries, case-sensitively.
func selectClaimedEndpoint(claims map[string]endpointClaim, listener, path string) (string, string) {
	selected, prefix, length := "", "", -1
	fallback := ""
	for endpoint, claim := range claims {
		if claim.listener != listener {
			continue
		}
		for _, claimed := range claim.prefixes {
			if claimed == "/" {
				fallback = endpoint
				continue
			}
			if fallsUnderClaim(path, claimed) && len(claimed) > length {
				selected, prefix, length = endpoint, claimed, len(claimed)
			}
		}
	}
	if selected != "" {
		return selected, prefix
	}
	return fallback, "/"
}

func fallsUnderClaim(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

func literalLeadingPath(path string) string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return ""
	}
	segments := strings.Split(trimmed, "/")
	cut := len(segments)
	for i, segment := range segments {
		if strings.HasPrefix(segment, ":") || strings.HasPrefix(segment, "*") {
			cut = i
			break
		}
	}
	if cut == 0 {
		return ""
	}
	return "/" + strings.Join(segments[:cut], "/")
}

type documentedOperation struct {
	method   string
	path     string
	endpoint string
}

var patternParameter = regexp.MustCompile(`\{[^}]*\}`)

func documentedOperations(t *testing.T, name string, doc []byte) []documentedOperation {
	t.Helper()
	var parsed struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("%s is not JSON: %v", name, err)
	}
	methods := map[string]bool{
		"get": true, "head": true, "post": true, "put": true,
		"patch": true, "delete": true, "options": true, "trace": true,
	}
	var operations []documentedOperation
	for path, item := range parsed.Paths {
		for method, raw := range item {
			if !methods[method] {
				continue
			}
			var operation struct {
				Endpoint string `json:"x-endpoint"`
			}
			if err := json.Unmarshal(raw, &operation); err != nil {
				t.Fatalf("%s: the %s %s operation is not JSON: %v", name, method, path, err)
			}
			if operation.Endpoint == "" {
				continue
			}
			operations = append(operations, documentedOperation{
				method:   strings.ToUpper(method),
				path:     patternParameter.ReplaceAllString(path, ":p"),
				endpoint: operation.Endpoint,
			})
		}
	}
	if len(operations) == 0 {
		t.Fatalf("%s declares no operation", name)
	}
	return operations
}

func fragmentEndpoints(t *testing.T, name string, doc []byte) []string {
	t.Helper()
	var parsed struct {
		Fragments []struct {
			Endpoint string `json:"endpoint"`
		} `json:"x-fragments"`
	}
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("%s is not JSON: %v", name, err)
	}
	seen := map[string]bool{}
	var endpoints []string
	for _, fragment := range parsed.Fragments {
		if fragment.Endpoint == "" || seen[fragment.Endpoint] {
			continue
		}
		seen[fragment.Endpoint] = true
		endpoints = append(endpoints, fragment.Endpoint)
	}
	if len(endpoints) == 0 {
		t.Fatalf("%s declares no x-fragments", name)
	}
	return endpoints
}

// checkClaimsCover holds every operation's endpoint to selecting itself from the
// operation's literal leading path on its own listener.
func checkClaimsCover(t *testing.T, configName, documentName string,
	claims map[string]endpointClaim, operations []documentedOperation, fragments []string,
) {
	t.Helper()
	for _, operation := range operations {
		claim, declared := claims[operation.endpoint]
		if !declared {
			t.Errorf("%s: %s declares an operation on endpoint %q, which the configuration does not declare",
				configName, documentName, operation.endpoint)
			continue
		}
		leading := literalLeadingPath(operation.path)
		selected, prefix := selectClaimedEndpoint(claims, claim.listener, leading)
		if selected == operation.endpoint {
			continue
		}
		answer := "no endpoint on the listener selects it"
		if selected != "" {
			answer = "endpoint " + quote(selected) + " claims " + quote(prefix)
		}
		t.Errorf("%s: %s: %s %s (literal leading path %q) is on endpoint %q, but the claims select %s. "+
			"The operation's namespace has to stay claimed even when the module does not run",
			configName, documentName, operation.method, operation.path, leading,
			operation.endpoint, answer)
	}
	for _, endpoint := range fragments {
		if _, declared := claims[endpoint]; !declared {
			t.Errorf("%s: %s carries an x-fragment for endpoint %q, which the configuration does not declare",
				configName, documentName, endpoint)
		}
	}
}

const platformListener = "backend"

func nonRootBackendClaims(claims map[string]endpointClaim) map[string][]string {
	claimed := map[string][]string{}
	for endpoint, claim := range claims {
		if claim.listener != platformListener {
			continue
		}
		for _, prefix := range claim.prefixes {
			if prefix == "/" {
				continue
			}
			claimed[prefix] = append(claimed[prefix], endpoint)
		}
	}
	for prefix := range claimed {
		sort.Strings(claimed[prefix])
	}
	return claimed
}

// checkClaimsExact holds each configuration's non-root backend claims to the
// ownership table, reporting a namespace claimed from outside the table.
func checkClaimsExact(t *testing.T, configName string, claims map[string]endpointClaim) {
	t.Helper()
	claimed := nonRootBackendClaims(claims)
	expectedOwners := demotest.ExpectedOwners()
	for prefix, owner := range expectedOwners {
		endpoints, ok := claimed[prefix]
		switch {
		case !ok:
			t.Errorf("%s: no endpoint on the %s listener claims %q, which the ownership table gives to %q",
				configName, platformListener, prefix, owner)
		case len(endpoints) > 1:
			t.Errorf("%s: the %s listener claims %q from more than one endpoint (%s), and the ownership table gives it to %q",
				configName, platformListener, prefix, strings.Join(endpoints, ", "), owner)
		case endpoints[0] != owner:
			t.Errorf("%s: the %s listener claims %q for %q, but the ownership table gives it to %q",
				configName, platformListener, prefix, endpoints[0], owner)
		}
	}
	for prefix, endpoints := range claimed {
		if _, owned := expectedOwners[prefix]; owned {
			continue
		}
		t.Errorf("%s: %s claims %q on the %s listener, which the ownership table gives to no endpoint: "+
			"paths under it would reach that endpoint instead of the bundle",
			configName, strings.Join(endpoints, ", "), prefix, platformListener)
	}
}

// TestTheConfigurationsClaimExactlyTheOwnershipTable checks that each configuration's non-root backend
// claims give every prefix the ownership table's endpoint.
func TestTheConfigurationsClaimExactlyTheOwnershipTable(t *testing.T) {
	t.Parallel()
	configurations := []namedClaims{{
		name:   "the rendered template",
		claims: claimsFromConfig(t, "the rendered template", []byte(configBody(t, []int{8080}, ":memory:"))),
	}}
	for _, sample := range testdataFiles {
		configurations = append(configurations, namedClaims{
			name:   "testdata/" + sample,
			claims: claimsFromFile(t, filepath.Join("testdata", sample)),
		})
	}
	for _, configuration := range configurations {
		checkClaimsExact(t, configuration.name, configuration.claims)
	}
}

func quote(value string) string { return `"` + value + `"` }

func configFilePath(locator string) string {
	return strings.TrimPrefix(locator, "file://")
}

func guardedEndpointSet(t *testing.T) map[string]bool {
	t.Helper()
	var parsed struct {
		Gate struct {
			Endpoints []string `config:"endpoints"`
		} `config:"gate"`
	}
	locator := writeConfig(t, []int{8080})
	readEffective(t, "the case's configuration", locator, "authn", &parsed)
	guarded := map[string]bool{}
	for _, endpoint := range parsed.Gate.Endpoints {
		guarded[endpoint] = true
	}
	if len(guarded) == 0 {
		t.Fatalf("the case's configuration names no endpoint the gateway guards")
	}
	return guarded
}

type namedClaims struct {
	name   string
	claims map[string]endpointClaim
}

var auditSampleRequests = []string{}

type auditCarrier struct {
	Journal struct {
		Requests []string `config:"requests"`
	} `config:"journal"`
}

type namedConfig struct {
	name string
	body string
}

// checkAuditSamples holds every configuration's effective request-level audit
// sample to the platform default.
func checkAuditSamples(t *testing.T) {
	t.Helper()
	configurations := []namedConfig{{
		name: "the rendered template",
		body: configBody(t, []int{8080}, ":memory:"),
	}}
	for _, sample := range testdataFiles {
		path := filepath.Join("testdata", sample)
		doc, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		configurations = append(configurations, namedConfig{name: sample, body: string(doc)})
	}
	for _, configuration := range configurations {
		var parsed auditCarrier
		readEffective(t, configuration.name, writeConfigFile(t, configuration.body), "audit", &parsed)
		requests := parsed.Journal.Requests
		if len(requests) != len(auditSampleRequests) {
			t.Fatalf("%s records %d audit requests, want %d: %v",
				configuration.name, len(requests), len(auditSampleRequests), requests)
		}
		for i, want := range auditSampleRequests {
			if requests[i] != want {
				t.Errorf("%s records audit request %q at position %d, want %q",
					configuration.name, requests[i], i, want)
			}
		}
	}
}

func checkPlatformClaims(t *testing.T, locator string, served []byte) {
	t.Helper()
	checkAuditSamples(t)
	contract := mergedDocument(t)
	configurations := []namedClaims{
		{"the case's configuration", claimsFromFile(t, configFilePath(locator))},
	}
	for _, sample := range testdataFiles {
		configurations = append(configurations,
			namedClaims{"testdata/" + sample, claimsFromFile(t, filepath.Join("testdata", sample))})
	}
	for _, configuration := range configurations {
		checkClaimsExact(t, configuration.name, configuration.claims)
	}
	documents := []struct {
		name string
		body []byte
	}{
		{"the served /openapi.json", served},
		{"the merged document gen writes", contract},
	}
	for _, document := range documents {
		operations := documentedOperations(t, document.name, document.body)
		fragments := fragmentEndpoints(t, document.name, document.body)
		for _, configuration := range configurations {
			checkClaimsCover(t, configuration.name, document.name,
				configuration.claims, operations, fragments)
		}
	}
}

func mergedDocument(t *testing.T) []byte {
	t.Helper()
	mergedDocumentOnce.Do(func() {
		dir := t.TempDir()
		out := filepath.Join(dir, "api.json")
		res := runCommandArgs(t, hostBinary, dir, []string{"sqlite-memory", genCommand, "-o", out}, nil)
		if res.err != nil {
			mergedDocumentErr = fmt.Errorf("gen failed: %w\n%s", res.err, res.combined())
			return
		}
		mergedDocumentBytes, mergedDocumentErr = os.ReadFile(out)
	})
	if mergedDocumentErr != nil {
		t.Fatalf("getting the merged document gen writes: %v", mergedDocumentErr)
	}
	return mergedDocumentBytes
}

var (
	mergedDocumentOnce  sync.Once
	mergedDocumentBytes []byte
	mergedDocumentErr   error
)

var tenantParam = regexp.MustCompile(`\{([^}]+)\}`)

// TestTheTenantManagementRoutesAreDeclaredOpenForTheAuthorizationGuard holds the host's declaration
// list to the merged document in both directions.
func TestTheTenantManagementRoutesAreDeclaredOpenForTheAuthorizationGuard(t *testing.T) {
	t.Parallel()
	contract := mergedDocument(t)
	var document struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(contract, &document); err != nil {
		t.Fatalf("the merged document gen writes is not readable: %v", err)
	}

	declared := make(map[string]bool, len(tenantManagementRoutes))
	for _, r := range tenantManagementRoutes {
		declared[r.method+" "+r.path] = true
	}
	served := make(map[string]bool)
	for path, item := range document.Paths {
		if !strings.HasPrefix(path, "/api/v1/tenants/") {
			continue
		}
		for method, raw := range item {
			var operation struct {
				Endpoint string   `json:"x-endpoint"`
				Tags     []string `json:"tags"`
			}
			if err := json.Unmarshal(raw, &operation); err != nil {
				t.Fatalf("reading %s %s: %v", method, path, err)
			}
			if operation.Endpoint != "api" ||
				(!slices.Contains(operation.Tags, "authn.admin") && !slices.Contains(operation.Tags, "authn.invite")) {
				continue
			}
			key := strings.ToUpper(method) + " " + tenantParam.ReplaceAllString(path, ":$1")
			served[key] = true
			if !declared[key] {
				t.Errorf("the contract serves %s on the api endpoint, and management.go does not declare it "+
					"open to the authorization guard", key)
			}
		}
	}
	if len(served) == 0 {
		t.Fatal("the contract lists no tenant-management operation on the api endpoint")
	}
	for key := range declared {
		if !served[key] {
			t.Errorf("management.go declares %s open, which the contract does not serve as a "+
				"tenant-management operation", key)
		}
	}
}
