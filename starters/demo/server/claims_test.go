package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

// The dispatch selects an endpoint by the path prefixes the configuration
// gives its endpoints, and the claims are the product's fixed namespace map: a
// capability dropped from a run, or from the imports, keeps its namespace
// claimed. These cases hold the map to the platform's API surface in both
// directions: a namespace that lost its claim is reported here rather than
// showing through an anonymous answer, and a namespace claimed from outside
// the map is reported too — the frontend's page paths depend on no non-root
// claim covering them.
//
// Two documents are read. The served /openapi.json is the available set of
// this binary — the fragments of every module the imports reach, whether or
// not a command names it. contracts/api.json is the committed merged document
// @speed/api-sdk is generated from. A claim table has to cover both: the first
// is what this binary can answer, the second is what the platform ships, and a
// platform module the host stops importing drops out of the first without
// leaving the second.

// platformContractPath is the committed platform document, relative to this
// module's directory (starters/demo/server).
const platformContractPath = "../../../contracts/api.json"

// testdataFiles are the six sample configurations. They are override documents
// over the platform defaults, so each one's effective claim table — the
// compiled-in document plus whatever the file states — is held to the ownership
// table rather than to a value written in this file.
var testdataFiles = []string{
	"sqlite-memory.yaml",
	"sqlite.yaml",
	"postgres.yaml",
	"postgres-redis-nats.yaml",
	"postgres-nats-redis.yaml",
	"postgres-memcached-nats.yaml",
}

// endpointClaim is one endpoint's validated claim: the listener it sits on and
// the prefixes it answers there.
type endpointClaim struct {
	listener string
	prefixes []string
}

// claimsCarrier is the part of the http section the claim cases read, decoded
// through pkg/config so that the answer is the effective one: the host defaults
// document folded under the configuration file the run is pointed at, the same
// merge the binary performs.
type claimsCarrier struct {
	Endpoints map[string]struct {
		Listener string   `config:"listener"`
		Prefixes []string `config:"prefixes"`
	} `config:"endpoints"`
}

// readEffective decodes one module's section as a run of this binary would see
// it: the compiled-in defaults document below the file, the file above it, and
// the module's own prototype last. It loads through the same pkg/config entry
// point the binary uses, over the process registry, so a key that moved into
// defaults.go and a key the file still states take one path.
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

// claimsFromLocator answers the endpoints the run at locator would actually
// serve, defaults document included.
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

// claimsFromFile reads one configuration file and answers the claims of the run
// it configures.
func claimsFromFile(t *testing.T, path string) map[string]endpointClaim {
	t.Helper()
	doc, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return claimsFromConfig(t, path, doc)
}

// claimsFromConfig writes one configuration body and answers the claims of the
// run it configures.
func claimsFromConfig(t *testing.T, name string, doc []byte) map[string]endpointClaim {
	t.Helper()
	return claimsFromLocator(t, name, writeConfigFile(t, string(doc)))
}

// selectClaimedEndpoint is the request-time selection rule, written here so the
// cases read the configuration the way the dispatch does: the longest non-root
// claim the path falls under wins, a path under none goes to the endpoint that
// claimed "/", and a listener with no such endpoint selects nothing. The path
// is compared the way the dispatch compares it — at segment boundaries,
// case-sensitively — because a bare string-prefix match would let "/apifoo"
// fall under "/api".
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

// fallsUnderClaim reports whether a path lies at or below a claimed prefix at a
// segment boundary.
func fallsUnderClaim(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

// literalLeadingPath returns the literal part of a route path: everything
// before its first segment that begins with ":" or "*". A fully literal path
// contributes its whole path; the root and a path whose first segment is
// dynamic contribute "".
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

// documentedOperation is one operation of a merged document, with the endpoint
// its fragment names.
type documentedOperation struct {
	method   string
	path     string
	endpoint string
}

// patternParameter matches an OpenAPI path parameter, which is written {name}.
var patternParameter = regexp.MustCompile(`\{[^}]*\}`)

// documentedOperations answers every operation a merged document declares, with
// the path parameters rewritten the way a router writes them ({p} to :p), so
// the literal leading path below is the one a registered route would have.
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

// fragmentEndpoints answers the endpoint names the document's x-fragments
// carry.
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

// checkClaimsCover holds one claim table to one document: every operation's
// endpoint has to select itself from the operation's literal leading path on
// its own listener, and every endpoint a fragment names has to be declared.
//
// The check is static and reads only the document and the claims. An operation
// running under a claim another endpoint owns would fail at startup with
// ErrUnclaimedRoute; this case exists for the other direction, a namespace no
// endpoint claims at all. That one is silent while the claiming module does not
// run — the baseline imports sms.admin but names it on no command — and an
// anonymous caller reads the difference as "401 or the fallback answer".
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

// platformListener is the listener expectedOwners describes: the one carrying
// the api, account, admin and web endpoints.
const platformListener = "backend"

// nonRootBackendClaims collects one configuration's non-root claims on the
// backend listener, grouped by the prefix they claim.
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

// checkClaimsExact holds one configuration's claim table to the platform's
// ownership table in the direction checkClaimsCover does not read: on the
// backend listener, the non-root prefixes and the endpoint each belongs to
// have to equal expectedOwners. checkClaimsCover reports a namespace that lost
// its claim; this reports a namespace claimed from outside the table. Such a
// claim is invisible to every other case — the frontend matches its routes
// against the manifest, not against the configuration — and at run time the
// claiming endpoint answers paths the platform leaves to the bundle.
func checkClaimsExact(t *testing.T, configName string, claims map[string]endpointClaim) {
	t.Helper()
	claimed := nonRootBackendClaims(claims)
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

// backendPrefixManifestPath is the dev-proxy manifest the frontend package
// carries, relative to this module's directory (starters/demo/server).
const backendPrefixManifestPath = "../web/backend-prefixes.json"

// readPrefixManifest decodes that manifest into one prefix-to-endpoint table.
// The file groups prefixes by the endpoint that claims them, which is the shape
// the dev proxy and the frontend's route guard read; the comparisons below work
// on the flat table, because that is the claim the configuration makes.
func readPrefixManifest(t *testing.T) map[string]string {
	t.Helper()
	doc, err := os.ReadFile(filepath.Clean(backendPrefixManifestPath))
	if err != nil {
		t.Fatalf("reading %s: %v", backendPrefixManifestPath, err)
	}
	var parsed map[string][]string
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("%s is not a JSON object of endpoint to prefixes: %v", backendPrefixManifestPath, err)
	}
	if len(parsed) == 0 {
		t.Fatalf("%s lists no endpoint", backendPrefixManifestPath)
	}
	owners := make(map[string]string)
	for endpoint, prefixes := range parsed {
		for _, prefix := range prefixes {
			if prefix == "/" {
				t.Errorf("%s: %q lists the root, which the web fallback endpoint claims and no API namespace owns",
					backendPrefixManifestPath, endpoint)
				continue
			}
			if other, duplicate := owners[prefix]; duplicate {
				t.Errorf("%s: both %q and %q list %q", backendPrefixManifestPath, other, endpoint, prefix)
				continue
			}
			owners[prefix] = endpoint
		}
	}
	return owners
}

// checkManifestAgainstOwnership holds the manifest to the differential case's
// ownership table in both directions: the table is the case's written claim
// about which endpoint answers each namespace, and the manifest is the
// frontend's copy of the same fact.
func checkManifestAgainstOwnership(t *testing.T, owners map[string]string) {
	t.Helper()
	for prefix, endpoint := range owners {
		expected, listed := expectedOwners[prefix]
		switch {
		case !listed:
			t.Errorf("%s: %q lists %q, which the ownership table gives to no endpoint",
				backendPrefixManifestPath, endpoint, prefix)
		case expected != endpoint:
			t.Errorf("%s: %q lists %q, but the ownership table gives it to %q",
				backendPrefixManifestPath, endpoint, prefix, expected)
		}
	}
	for prefix, endpoint := range expectedOwners {
		if _, listed := owners[prefix]; !listed {
			t.Errorf("%s: no endpoint lists %q, which the ownership table gives to %q",
				backendPrefixManifestPath, prefix, endpoint)
		}
	}
}

// checkManifestAgainstClaims holds the manifest to one configuration's claims on
// the backend listener, prefix for prefix and endpoint for endpoint.
func checkManifestAgainstClaims(t *testing.T, configName string, owners map[string]string, claims map[string]endpointClaim) {
	t.Helper()
	claimed := nonRootBackendClaims(claims)
	for prefix, endpoint := range owners {
		endpoints, ok := claimed[prefix]
		switch {
		case !ok:
			t.Errorf("%s: %s lists %q for %q, but the %s listener claims no such prefix",
				configName, backendPrefixManifestPath, prefix, endpoint, platformListener)
		case len(endpoints) > 1:
			t.Errorf("%s: %s lists %q for %q, but the %s listener claims it from more than one endpoint (%s)",
				configName, backendPrefixManifestPath, prefix, endpoint, platformListener, strings.Join(endpoints, ", "))
		case endpoints[0] != endpoint:
			t.Errorf("%s: %s lists %q for %q, but the %s listener claims it for %q",
				configName, backendPrefixManifestPath, prefix, endpoint, platformListener, endpoints[0])
		}
	}
	for prefix, endpoints := range claimed {
		if _, listed := owners[prefix]; listed {
			continue
		}
		t.Errorf("%s: the %s listener claims %q for %s, but %s lists no such prefix",
			configName, platformListener, prefix, strings.Join(endpoints, ", "), backendPrefixManifestPath)
	}
}

// TestThePrefixManifestAgreesWithTheDifferentialTableAndTheClaims holds the
// three statements of the backend claim table together: the frontend's
// dev-proxy manifest, the differential case's written ownership table, and each
// configuration's non-root claims on the backend listener. The design requires
// them equal; a manifest entry the configuration does not claim makes the dev
// proxy forward a namespace the run never answers, and a claim the manifest
// omits leaves the page paths unchecked against it.
//
// The case is static — it reads the manifest, the table and the configurations
// without starting a process — so the three are held together even when the
// cases that run the program are not.
func TestThePrefixManifestAgreesWithTheDifferentialTableAndTheClaims(t *testing.T) {
	owners := readPrefixManifest(t)
	checkManifestAgainstOwnership(t, owners)
	configurations := []namedClaims{{
		name:   "the rendered template",
		claims: claimsFromConfig(t, "the rendered template", []byte(configBody(t, []int{8080, 8083}, ":memory:"))),
	}}
	for _, sample := range testdataFiles {
		configurations = append(configurations, namedClaims{
			name:   "testdata/" + sample,
			claims: claimsFromFile(t, filepath.Join("testdata", sample)),
		})
	}
	for _, configuration := range configurations {
		checkClaimsExact(t, configuration.name, configuration.claims)
		checkManifestAgainstClaims(t, configuration.name, owners, configuration.claims)
	}
}

// quote wraps a value in double quotes for a failure message.
func quote(value string) string { return `"` + value + `"` }

// configFilePath turns the file locator a case hands the program back into the
// path of the file it wrote.
func configFilePath(locator string) string {
	return strings.TrimPrefix(locator, "file://")
}

// guardedEndpointSet answers the endpoints the gateway guards in the case's
// configuration, read the way the run reads it: the defaults document supplies
// the endpoint list and the file need not repeat it.
func guardedEndpointSet(t *testing.T) map[string]bool {
	t.Helper()
	var parsed struct {
		Gate struct {
			Endpoints []string `config:"endpoints"`
		} `config:"gate"`
	}
	locator := writeConfig(t, []int{8080, 8083})
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

// namedClaims is one configuration's claim table under the name the failure
// messages use.
type namedClaims struct {
	name   string
	claims map[string]endpointClaim
}

// auditSampleRequests is the request-level audit sample every configuration
// ships: none. Every write this product carries records at the seam its own
// capability writes, so the default document names no request, and a frontend
// poll named here would land a row per poll. The empty list is still held, not
// skipped: a copy that adds an entry, or a default that grows one back, shows
// here rather than only in the rows a run writes.
var auditSampleRequests = []string{}

// auditCarrier is the part of the audit section the sample case reads.
type auditCarrier struct {
	Journal struct {
		Requests []string `config:"requests"`
	} `config:"journal"`
}

// namedConfig is one configuration body under the name a failure names it by.
type namedConfig struct {
	name string
	body string
}

// checkAuditSamples holds the effective request-level audit sample of every
// sample configuration, and of the rendered template -- the configuration the
// HTTP cases run -- to the platform default. The list is read through
// pkg/config, so it is the list the run records: it may come from the host
// defaults document, and a sample that changed it in the file would show here.
// The hit case alone cannot do this: it runs the rendered template, so a
// sample whose entries all name the wrong route stays green there.
func checkAuditSamples(t *testing.T) {
	t.Helper()
	configurations := []namedConfig{{
		name: "the rendered template",
		body: configBody(t, []int{8080, 8083}, ":memory:"),
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

// checkPlatformClaims reads the two documents and holds every configuration to
// them: the one the case is running, and the six samples a copy starts from.
// Each configuration is also held to the ownership table itself, in both
// directions, and its audit sample to the platform default.
func checkPlatformClaims(t *testing.T, locator string, served []byte) {
	t.Helper()
	checkAuditSamples(t)
	contract, err := os.ReadFile(filepath.Clean(platformContractPath))
	if err != nil {
		t.Fatalf("reading the committed platform contract: %v", err)
	}
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
		{"contracts/api.json", contract},
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

// tenantParam is the path form the authorization guard's declaration table
// matches: gin keeps a parameter as :name, while the contract writes {name}.
var tenantParam = regexp.MustCompile(`\{([^}]+)\}`)

// TestTheTenantManagementRoutesAreDeclaredOpenForTheAuthorizationGuard holds the
// host's declaration list to the committed contract. The tenant-management
// operations are served on the api endpoint, whose chain carries the
// authorization guard; their handlers decide each action through the Decider,
// so an operation the host does not declare open is refused 403 before its
// handler runs. The check runs both ways: a contract operation missing from the
// list, and a list entry the contract does not serve.
func TestTheTenantManagementRoutesAreDeclaredOpenForTheAuthorizationGuard(t *testing.T) {
	contract, err := os.ReadFile(filepath.Clean(platformContractPath))
	if err != nil {
		t.Fatalf("reading the committed platform contract: %v", err)
	}
	var document struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(contract, &document); err != nil {
		t.Fatalf("the committed contract is not readable: %v", err)
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
