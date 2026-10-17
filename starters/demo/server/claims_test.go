package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
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

// testdataFiles are the six sample configurations. Every one of them carries
// the same claim table; the cases below hold each of them to it rather than to
// a value written in this file.
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

// claimsDocument is the part of a configuration the claim cases read.
type claimsDocument struct {
	HTTP struct {
		Endpoints map[string]struct {
			Listener string   `yaml:"listener"`
			Prefixes []string `yaml:"prefixes"`
		} `yaml:"endpoints"`
	} `yaml:"http"`
}

// claimsFromConfig parses a configuration and answers its endpoints' claims. An
// endpoint that omits prefixes claims "/", which is the same default pkg/http
// applies.
func claimsFromConfig(t *testing.T, name string, doc []byte) map[string]endpointClaim {
	t.Helper()
	var parsed claimsDocument
	if err := yaml.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("%s is not YAML: %v", name, err)
	}
	if len(parsed.HTTP.Endpoints) == 0 {
		t.Fatalf("%s declares no http.endpoints", name)
	}
	claims := make(map[string]endpointClaim, len(parsed.HTTP.Endpoints))
	for endpoint, declared := range parsed.HTTP.Endpoints {
		prefixes := declared.Prefixes
		if prefixes == nil {
			prefixes = []string{"/"}
		}
		claims[endpoint] = endpointClaim{listener: declared.Listener, prefixes: prefixes}
	}
	return claims
}

// claimsFromFile reads one configuration file and answers its claims.
func claimsFromFile(t *testing.T, path string) map[string]endpointClaim {
	t.Helper()
	doc, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return claimsFromConfig(t, path, doc)
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

// quote wraps a value in double quotes for a failure message.
func quote(value string) string { return `"` + value + `"` }

// configFilePath turns the file locator a case hands the program back into the
// path of the file it wrote.
func configFilePath(locator string) string {
	return strings.TrimPrefix(locator, "file://")
}

// guardedEndpointSet answers the endpoints the gateway guards, as the case's
// configuration declares them.
func guardedEndpointSet(t *testing.T) map[string]bool {
	t.Helper()
	var parsed struct {
		Authn struct {
			Gate struct {
				Endpoints []string `yaml:"endpoints"`
			} `yaml:"gate"`
		} `yaml:"authn"`
	}
	if err := yaml.Unmarshal([]byte(configBody(t, []int{8080, 8083}, ":memory:")), &parsed); err != nil {
		t.Fatalf("reading the gateway's endpoint list from the configuration: %v", err)
	}
	guarded := map[string]bool{}
	for _, endpoint := range parsed.Authn.Gate.Endpoints {
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

// checkPlatformClaims reads the two documents and holds every configuration to
// them: the one the case is running, and the six samples a copy starts from.
// Each configuration is also held to the ownership table itself, in both
// directions.
func checkPlatformClaims(t *testing.T, locator string, served []byte) {
	t.Helper()
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
