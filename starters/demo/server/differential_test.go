package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/http/cors"
	"github.com/vislake/speed/pkg/i18n"
	i18nhttp "github.com/vislake/speed/pkg/i18n/http"
	"github.com/vislake/speed/pkg/observability/otel"
	"github.com/vislake/speed/pkg/ratelimit"
)

// This file holds the differential case: the anonymous answers a run gives
// must not depend on which modules the run assembled. The property is the one
// the claim-based dispatch exists for — endpoint selection reads the
// configuration's prefixes and nothing about the registered routes — and the
// case is written so it can fail before that change: the endpoint a probe is
// expected to reach is read from the table below, not from the configuration
// under test.
//
// A variant process is this test binary re-executed with DEMO_SERVE_WITHOUT
// naming modules to drop from baselineServe. TestMain sees the variable, skips
// the build and the test run, prints the final inclusion list to standard
// error, and runs the program. Product code carries no switch.

const serveWithoutEnv = "DEMO_SERVE_WITHOUT"

// serveWithout drops the named modules from baselineServe and reports the final
// list. It runs before the command tree is built, so the serve command assembles
// what the list names and nothing else.
func serveWithout(list string) {
	drop := map[string]bool{}
	for _, name := range strings.Split(list, ",") {
		if name = strings.TrimSpace(name); name != "" {
			drop[name] = true
		}
	}
	kept := make([]string, 0, len(baselineServe))
	for _, name := range baselineServe {
		if !drop[name] {
			kept = append(kept, name)
		}
	}
	baselineServe = kept
	fmt.Fprintln(os.Stderr, "serve-without: "+strings.Join(kept, ","))
}

// expectedOwners is the prefix-to-endpoint table the case judges against. It is
// written here rather than read from the configuration under test: reading the
// configuration would make the case agree with whatever the run does, including
// a run whose selection is derived from the assembly. After the change the same
// table is what testdata/ and the case's configuration claim, and the D2 case
// holds the two together.
var expectedOwners = map[string]string{
	"/api":                       "api",
	"/api/v1/auth":               "account",
	"/api/v1/features":           "account",
	"/api/v1/workspace":          "account",
	"/api/v1/tenants":            "account",
	"/api/v1/system/auth":        "account",
	"/api/v1/system/audit":       "admin",
	"/api/v1/system/permissions": "admin",
	"/api/v1/system/settings":    "admin",
	"/api/v1/system/sms":         "admin",
	"/api/v1/system/tenants":     "admin",
	"/content":                   "content",
}

// expectedEndpoint answers which endpoint the table says answers a path on the
// backend listener. A path under no non-root prefix goes to web in the merged
// topology; on the split one the backend listener has no fallback and the
// dispatch answers, which is no endpoint at all.
func expectedEndpoint(path string, split bool) string {
	owner, prefix := "", ""
	for claimed, endpoint := range expectedOwners {
		if fallsUnderClaim(path, claimed) && len(claimed) > len(prefix) {
			owner, prefix = endpoint, claimed
		}
	}
	if owner != "" {
		return owner
	}
	if split {
		return ""
	}
	return "web"
}

// probe is one anonymous request the case sends to both runs.
type probe struct {
	listener string
	method   string
	path     string
	endpoint string
}

// probeMethods are the methods every documented path is asked with. An
// operation documented for one method is asked with all of them: the gateway's
// promise is that an unauthenticated refusal does not depend on whether the
// method is the registered one.
var probeMethods = []string{
	http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
	http.MethodPatch, http.MethodDelete, http.MethodOptions,
}

// outsideClaims are paths no claim of the table covers, besides the root.
var outsideClaims = []string{
	"/", "/console/tenants", "/zz.json", "/.well-known/zz",
	"/assets/zz.js", "//api/zz", "/API/zz", "/apix",
}

// buildProbes answers the request matrix: every documented path with every
// method, the table's own prefixes at their boundaries, and paths outside every
// claim. A documented path goes to the listener its fragment's endpoint sits
// on; the table probes go to the backend listener.
func buildProbes(t *testing.T, document []byte, claims map[string]endpointClaim, split bool) []probe {
	t.Helper()
	seen := map[probe]bool{}
	var probes []probe
	add := func(p probe) {
		if !seen[p] {
			seen[p] = true
			probes = append(probes, p)
		}
	}

	var parsed struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(document, &parsed); err != nil {
		t.Fatalf("the served document is not JSON: %v", err)
	}
	for path, item := range parsed.Paths {
		endpoint := ""
		for method, raw := range item {
			if !slices.Contains(probeMethods, strings.ToUpper(method)) {
				continue
			}
			var operation struct {
				Endpoint string `json:"x-endpoint"`
			}
			if err := json.Unmarshal(raw, &operation); err != nil {
				t.Fatalf("the %s %s operation is not JSON: %v", method, path, err)
			}
			if operation.Endpoint != "" {
				endpoint = operation.Endpoint
			}
		}
		if endpoint == "" {
			continue
		}
		claim, declared := claims[endpoint]
		if !declared {
			t.Errorf("the document has an operation on endpoint %q, which the configuration does not declare", endpoint)
			continue
		}
		requestPath := patternParameter.ReplaceAllString(path, "zz")
		for _, method := range probeMethods {
			add(probe{listener: claim.listener, method: method, path: requestPath, endpoint: endpoint})
		}
	}

	for prefix := range expectedOwners {
		for _, path := range []string{prefix, prefix + "/", prefix + "/zz-absent", prefix + "/zz/zz"} {
			add(probe{
				listener: "backend", method: http.MethodGet, path: path,
				endpoint: expectedEndpoint(path, split),
			})
		}
	}
	for _, path := range outsideClaims {
		add(probe{
			listener: "backend", method: http.MethodGet, path: path,
			endpoint: expectedEndpoint(path, split),
		})
	}
	return probes
}

// anonRoute is one route registered as exempt, or as accepting a request with
// no credential.
type anonRoute struct {
	method string
	path   string
}

// frozenTable is the baseline's anonymous surface, by endpoint, read from the
// gateway's startup diagnostics.
type frozenTable map[string][]anonRoute

// diagnosticPair matches one key="value" attribute of a diagnostic line. The
// endpoint keys are the endpoint names; the message and module keys are
// skipped by the value shape below.
var diagnosticPair = regexp.MustCompile(`([A-Za-z0-9_.-]+)="([^"]*)"`)

// routeToken matches one rendered route, "METHOD /path".
var routeToken = regexp.MustCompile(`^([A-Z]+) (/.*)$`)

// parseFrozenTable reads the two frozen tables the gateway writes at startup:
// the exempt routes and the routes that accept a request with no credential.
// For an anonymous request the two are the same surface, so both are collected.
func parseFrozenTable(lines []string) frozenTable {
	table := frozenTable{}
	for _, line := range lines {
		if !strings.Contains(line, "msg=\"authn: the exempt routes, frozen\"") &&
			!strings.Contains(line, "msg=\"authn: the routes that accept a request with no credential, frozen\"") {
			continue
		}
		for _, pair := range diagnosticPair.FindAllStringSubmatch(line, -1) {
			key, value := pair[1], pair[2]
			if key == "msg" || key == "module" {
				continue
			}
			for _, token := range strings.Split(value, ", ") {
				match := routeToken.FindStringSubmatch(token)
				if match == nil {
					continue
				}
				table[key] = append(table[key], anonRoute{method: match[1], path: match[2]})
			}
		}
	}
	return table
}

// hitsAnonymousSurface reports whether a probe is one the guarantee exempts: it
// hits the baseline's anonymous surface on the endpoint the expectation table
// names. Such a request is not sent — sending it would change the rate limiter
// and the journal — because its answer is allowed to differ.
func hitsAnonymousSurface(table frozenTable, ops []anonRoute, p probe) bool {
	switch p.endpoint {
	case "web":
		// The frontend host's catch-all route covers the endpoint's whole
		// path space, so every request there hits it.
		return true
	case "ops":
		for _, route := range ops {
			if route.method == p.method && routeMatches(route.path, p.path) {
				return true
			}
		}
		return false
	case "":
		// The dispatch answers; it registers no route and no module answers.
		return false
	}
	for _, route := range table[p.endpoint] {
		if route.method == p.method && routeMatches(route.path, p.path) {
			return true
		}
	}
	return false
}

// routeMatches reports whether a request path matches a registered route
// pattern: literal segments compare exactly, ":p" matches one segment, and
// "*p" matches the rest. Trailing slashes are significant, as they are to the
// engine.
func routeMatches(pattern, path string) bool {
	if strings.HasSuffix(pattern, "/") != strings.HasSuffix(path, "/") {
		return false
	}
	patternSegments := querylessSegments(pattern)
	pathSegments := querylessSegments(path)
	for i, segment := range patternSegments {
		if strings.HasPrefix(segment, "*") {
			return true
		}
		if i >= len(pathSegments) {
			return false
		}
		if strings.HasPrefix(segment, ":") {
			continue
		}
		if segment != pathSegments[i] {
			return false
		}
	}
	return len(pathSegments) == len(patternSegments)
}

// querylessSegments splits a path into non-empty segments.
func querylessSegments(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

// chainLine is one endpoint's assembly diagnostic.
type chainLine struct {
	routes int
	layers []string
}

// parseChains reads the assembly lines: the endpoint, its route count and the
// chain as written.
var (
	chainEndpoint = regexp.MustCompile(`endpoint=([A-Za-z0-9_.-]+)`)
	chainRoutes   = regexp.MustCompile(`routes=([0-9]+)`)
	chainList     = regexp.MustCompile(`chain="([^"]*)"`)
)

func parseChains(lines []string) map[string]chainLine {
	chains := map[string]chainLine{}
	for _, line := range lines {
		if !strings.Contains(line, "msg=\"an HTTP endpoint's chain is assembled\"") {
			continue
		}
		endpoint := chainEndpoint.FindStringSubmatch(line)
		routes := chainRoutes.FindStringSubmatch(line)
		chain := chainList.FindStringSubmatch(line)
		if endpoint == nil || routes == nil || chain == nil {
			continue
		}
		var parsed chainLine
		if _, err := fmt.Sscanf(routes[1], "%d", &parsed.routes); err != nil {
			continue
		}
		for _, layer := range strings.Split(chain[1], " -> ") {
			layer = strings.TrimSpace(layer)
			// A layer carries the stage it stands in as a leading marker,
			// "[observe] observability.otel(…)", or as a token of its own,
			// "[observe]". Strip the marker; skip a token that is only one.
			if strings.HasPrefix(layer, "[") {
				end := strings.Index(layer, "]")
				if end < 0 {
					continue
				}
				layer = strings.TrimSpace(layer[end+1:])
			}
			if layer == "" {
				continue
			}
			if index := strings.Index(layer, "("); index >= 0 {
				layer = layer[:index]
			}
			parsed.layers = append(parsed.layers, layer)
		}
		chains[endpoint[1]] = parsed
	}
	return chains
}

// shapingTokens are the capabilities a module that shapes every answer
// provides. Removing such a module changes the answers for reasons that have
// nothing to do with endpoint selection, so the case leaves it in place.
var shapingTokens = []core.Token{
	(*authn.Verifier)(nil),
	(*authn.Gate)(nil),
	(*speedhttp.ErrorResponder)(nil),
	(*i18n.Localizer)(nil),
	(*i18n.Resolver)(nil),
	(*i18n.Negotiator)(nil),
}

// preGateLayers maps the layer names a chain can carry before the gateway to
// the module that delivers them. A layer this case does not know is a failure:
// the answer-shaping set has to be decided for it before it can be left in
// place.
var preGateLayers = map[string]string{
	"observability.otel":       otel.ModuleName,
	"observability.otel:guard": otel.ModuleName,
	"i18n:accept-language":     i18nhttp.ModuleName,
	"http.cors":                cors.ModuleName,
	"ratelimit":                ratelimit.ModuleName,
}

// moduleClass is what the case does with a baseline module.
type moduleClass int

const (
	classShaping moduleClass = iota // not removed: it shapes every answer
	classRouting                    // removed in a variant of its own
	classInfra                      // not removed: no routes on a guarded endpoint
)

// baselineModule is the descriptor facts the classification needs.
type baselineModule struct {
	name          string
	requires      []core.Requirement
	provides      []core.Token
	specEndpoints []string
}

// baselineDescriptors reads the modules baselineServe names, with their
// dependency declarations and the endpoints their fragments describe. A name in
// baselineServe that no module registers is a failure: the list and the
// registry would otherwise drift apart silently.
func baselineDescriptors(t *testing.T) map[string]*baselineModule {
	t.Helper()
	named := map[string]bool{}
	for _, name := range baselineServe {
		named[name] = true
	}
	modules := map[string]*baselineModule{}
	for _, module := range core.ProcessRegistry.Modules() {
		if !named[module.Name] {
			continue
		}
		described := &baselineModule{name: module.Name, requires: module.Requires}
		for _, provision := range module.Provides {
			described.provides = append(described.provides, provision.Token)
		}
		modules[module.Name] = described
	}
	for _, resource := range core.Resources[speedhttp.Spec](core.ProcessRegistry) {
		if module, ok := modules[resource.Module]; ok {
			module.specEndpoints = append(module.specEndpoints, resource.Value.Endpoint)
		}
	}
	for _, name := range baselineServe {
		if modules[name] == nil {
			t.Errorf("baselineServe names %q, which no module registers", name)
		}
	}
	for layer, module := range preGateLayers {
		registered := false
		for _, candidate := range core.ProcessRegistry.Modules() {
			if candidate.Name == module {
				registered = true
				break
			}
		}
		if !registered {
			t.Errorf("the pre-gate layer mapping names the module %q for layer %q, which no module registers: "+
				"the mapping outlived a rename", module, layer)
		}
	}
	return modules
}

// classifyBaseline gives every baseline module its class. A module is shaping
// when it provides a capability every answer depends on, or when it is one of
// the layers before the gateway; routing when it declares a fragment on a
// guarded endpoint; and infrastructure otherwise.
func classifyBaseline(t *testing.T, modules map[string]*baselineModule, guarded map[string]bool) map[string]moduleClass {
	t.Helper()
	mapped := map[string]bool{}
	for _, module := range preGateLayers {
		mapped[module] = true
	}
	classes := map[string]moduleClass{}
	for name, module := range modules {
		switch {
		case mapped[name] || providesShapingToken(module.provides):
			classes[name] = classShaping
		case guardedEndpointOf(module, guarded):
			classes[name] = classRouting
		default:
			classes[name] = classInfra
		}
	}
	return classes
}

// guardedEndpointOf reports whether the module declares a fragment on a guarded
// endpoint.
func guardedEndpointOf(module *baselineModule, guarded map[string]bool) bool {
	for _, endpoint := range module.specEndpoints {
		if guarded[endpoint] {
			return true
		}
	}
	return false
}

// providesShapingToken reports whether one of the module's provisions is a
// shaping capability.
func providesShapingToken(provides []core.Token) bool {
	for _, provision := range provides {
		for _, wanted := range shapingTokens {
			if reflect.DeepEqual(provision, wanted) {
				return true
			}
		}
	}
	return false
}

// withDependents adds every module that non-optionally requires a capability
// the set provides, transitively. Removing a module without its dependents
// would leave a run that cannot start for a reason unrelated to the claim, so
// the variant takes them together.
func withDependents(seed map[string]bool, modules map[string]*baselineModule) map[string]bool {
	out := map[string]bool{}
	for name := range seed {
		out[name] = true
	}
	for {
		var provided []core.Token
		for name := range out {
			provided = append(provided, modules[name].provides...)
		}
		added := false
		for name, module := range modules {
			if out[name] {
				continue
			}
			for _, requirement := range module.requires {
				if requirement.Optional {
					continue
				}
				for _, token := range provided {
					if reflect.DeepEqual(requirement.Token, token) {
						out[name] = true
						added = true
						break
					}
				}
				if out[name] {
					break
				}
			}
		}
		if !added {
			return out
		}
	}
}

// variantGroup is one run with a set of modules left out.
type variantGroup struct {
	id      string
	modules []string
}

// mayNotStartGroups names the variants the case allows to leave not starting,
// each with the reason. The guarantee is fail-closed: every variant that
// buildGroups produces has to start, because leaving one out and still
// standing up is what makes the differential comparison meaningful. A variant
// that stops starting fails the case; only a debt written down here, with its
// reason, may be left out of the guarantee.
var mayNotStartGroups = map[string]string{}

// mayLeaveRouteCountUnchanged names the routing modules whose removal cannot
// lower an endpoint's route count in this case's configuration, each with the
// reason. A module that mounts its routes only when a switch is on mounts none
// here, so dropping it changes nothing and the removal check cannot tell a
// real module from a name that registered nothing. The check stays
// fail-closed otherwise: every other vacuous removal fails.
var mayLeaveRouteCountUnchanged = map[string]string{
	"workspace.personal": "its route is mounted only when workspace.personal.enabled is on, " +
		"and the case's configuration leaves the host switch off (the baseline default)",
}

// variantStartOutcome decides what a variant's readiness result means. A run
// that became ready passes. One that did not fails unless mayNotStartGroups
// exempts the group, in which case the exemption's reason is returned for the
// log. The decision is a function so a case can hold an unstarted variant to
// it without launching one.
func variantStartOutcome(group variantGroup, err error, output string) (exemption string, failure error) {
	if err == nil {
		return "", nil
	}
	if reason, exempt := mayNotStartGroups[group.id]; exempt {
		return reason, nil
	}
	return "", fmt.Errorf("dropping %v left a run that does not start: %w\n%s", group.modules, err, output)
}

// TestEveryRoutingVariantMustStart holds the start guarantee to the variants
// the case builds: an exemption has to be written down with its reason, and
// every other variant fails when it does not start rather than leaving the
// matrix silently. baselineDescriptors and classifyBaseline are the functions
// the differential case itself uses, so the groups held here are the ones it
// runs.
func TestEveryRoutingVariantMustStart(t *testing.T) {
	modules := baselineDescriptors(t)
	classes := classifyBaseline(t, modules, guardedEndpointSet(t))
	groups := buildGroups(modules, classes)
	if len(groups) == 0 {
		t.Fatalf("baselineServe holds no routing module, so there is nothing to remove")
	}
	built := map[string]bool{}
	for _, group := range groups {
		built[group.id] = true
		if reason, exempt := mayNotStartGroups[group.id]; exempt {
			if strings.TrimSpace(reason) == "" {
				t.Errorf("mayNotStartGroups exempts %q without a reason", group.id)
			}
			continue
		}
		if _, failure := variantStartOutcome(group, errors.New("the variant never became ready"), "output"); failure == nil {
			t.Errorf("variant %q can leave the matrix without starting", group.id)
		}
	}
	for id := range mayNotStartGroups {
		if !built[id] {
			t.Errorf("mayNotStartGroups names %q, which buildGroups does not produce: the exemption outlived its variant", id)
		}
	}
	// The route-count exemptions are held the same way: an entry has to name a
	// module this case removes and classifies as routing, and it has to carry
	// its reason, so an exemption cannot silently outlive the module or the
	// switch it was written for.
	for name, reason := range mayLeaveRouteCountUnchanged {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("mayLeaveRouteCountUnchanged exempts %q without a reason", name)
		}
		if _, known := modules[name]; !known {
			t.Errorf("mayLeaveRouteCountUnchanged names %q, which baselineServe does not include", name)
			continue
		}
		if classes[name] != classRouting {
			t.Errorf("mayLeaveRouteCountUnchanged names %q, which this case does not classify as routing", name)
		}
	}
}

// buildGroups writes the variants: one per routing module with its dependents,
// plus the group that leaves one endpoint with no routes at all.
func buildGroups(modules map[string]*baselineModule, classes map[string]moduleClass) []variantGroup {
	names := make([]string, 0, len(modules))
	for name := range modules {
		names = append(names, name)
	}
	sort.Strings(names)

	var groups []variantGroup
	group := func(id string, seed map[string]bool) {
		members := withDependents(seed, modules)
		var removal []string
		for name := range members {
			if classes[name] != classShaping {
				removal = append(removal, name)
			}
		}
		if len(removal) == 0 {
			return
		}
		sort.Strings(removal)
		groups = append(groups, variantGroup{id: id, modules: removal})
	}

	for _, name := range names {
		if classes[name] == classRouting {
			group(name, map[string]bool{name: true})
		}
	}
	for _, endpoint := range []string{"admin", "api"} {
		seed := map[string]bool{}
		for _, name := range names {
			if classes[name] == classRouting && slices.Contains(modules[name].specEndpoints, endpoint) {
				seed[name] = true
			}
		}
		group(endpoint+"-all", seed)
	}
	return groups
}

// lockedBuffer is a writer safe to read while the child still writes to it.
type lockedBuffer struct {
	mu     sync.Mutex
	buffer strings.Builder
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

// variantProcess is one child: the baseline serve, or a variant of it.
type variantProcess struct {
	cmd *exec.Cmd

	mu     sync.Mutex
	lines  []string
	stderr *lockedBuffer

	ready    chan struct{}
	readyOne sync.Once
	done     chan struct{}
	waitErr  error
	stopOnce sync.Once
}

// startChild launches the test binary as the program, with the given working
// directory and extra environment. It is the test binary rather than a built
// program because the variant changes baselineServe before run() builds the
// command tree; TestMain sees serveWithoutEnv and takes that branch.
func startChild(t *testing.T, binary, dir string, env []string, args ...string) *variantProcess {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Dir = dir
	cmd.Env = append(childEnv(), env...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("opening the child's standard output: %v", err)
	}
	p := &variantProcess{cmd: cmd, stderr: &lockedBuffer{}, ready: make(chan struct{}), done: make(chan struct{})}
	cmd.Stderr = p.stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the program: %v", err)
	}
	go p.consume(stdout)
	go func() {
		p.waitErr = cmd.Wait()
		close(p.done)
	}()
	t.Cleanup(func() { p.stop(stopLimit) })
	return p
}

// consume keeps the child's standard output and closes ready when the ready
// record arrives.
func (p *variantProcess) consume(reader io.Reader) {
	var pending []byte
	chunk := make([]byte, 32*1024)
	for {
		n, err := reader.Read(chunk)
		if n > 0 {
			pending = append(pending, chunk[:n]...)
			for {
				index := bytes.IndexByte(pending, '\n')
				if index < 0 {
					break
				}
				line := string(pending[:index])
				pending = pending[index+1:]
				p.mu.Lock()
				p.lines = append(p.lines, line)
				p.mu.Unlock()
				if strings.Contains(line, "msg=ready") {
					p.readyOne.Do(func() { close(p.ready) })
				}
			}
		}
		if err != nil {
			return
		}
	}
}

// waitReady waits for the ready record and answers why it never came.
func (p *variantProcess) waitReady(limit time.Duration) error {
	select {
	case <-p.ready:
		return nil
	case <-p.done:
		return fmt.Errorf("the run exited before it was ready: %w\nstandard output:\n%s\nstandard error:\n%s",
			p.waitErr, p.output(), p.stderrOutput())
	case <-time.After(limit):
		return fmt.Errorf("the run wrote no ready record within %s\nstandard output:\n%s\nstandard error:\n%s",
			limit, p.output(), p.stderrOutput())
	}
}

// output is what the child has written to standard output so far.
func (p *variantProcess) output() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return strings.Join(p.lines, "\n")
}

// linesCopy answers a copy of the child's standard output lines.
func (p *variantProcess) linesCopy() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.lines)
}

// stderrOutput is what the child has written to standard error so far.
func (p *variantProcess) stderrOutput() string {
	return p.stderr.String()
}

// stop asks the child to leave and waits for it.
func (p *variantProcess) stop(limit time.Duration) {
	p.stopOnce.Do(func() {
		if p.cmd.Process == nil {
			return
		}
		_ = p.cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-p.done:
		case <-time.After(limit):
			_ = p.cmd.Process.Kill()
			<-p.done
		}
	})
}

// topology is one deployment shape: the merged one keeps web on the backend
// listener beside the API, and the split one gives it a listener of its own.
type topology struct {
	name  string
	ports int
	split bool
}

// splitEndpoints is the split topology's endpoint table. It is written whole
// because http.endpoints is a container leaf: a file that gives the key
// replaces the platform defaults document's whole table rather than merging
// into it, so a partial table would drop the endpoints it left out.
const splitEndpoints = `  endpoints:
    api:
      listener: backend
      prefixes: ["/api"]
    account:
      listener: backend
      prefixes: ["/api/v1/auth", "/api/v1/features", "/api/v1/workspace", "/api/v1/tenants", "/api/v1/system/auth"]
    admin:
      listener: backend
      prefixes: ["/api/v1/system/audit", "/api/v1/system/permissions", "/api/v1/system/settings", "/api/v1/system/sms", "/api/v1/system/tenants"]
    content:
      listener: backend
      prefixes: ["/content"]
      max-body-bytes: 0
    ops:
      listener: ops
    web:
      listener: frontend
      prefixes: ["/"]
`

// render writes the topology's configuration with the given ports. The merged
// topology takes the endpoint table from the platform defaults document; the
// split one overrides the whole table to move the web endpoint onto a listener
// of its own.
func (topo topology) render(t *testing.T, ports []int) string {
	t.Helper()
	body := configBody(t, ports[:listenerCount], ":memory:")
	if !topo.split {
		return body
	}
	listener := fmt.Sprintf("    frontend:\n      address: \"127.0.0.1:%d\"\n", ports[2])
	opsListener := fmt.Sprintf("    ops:\n      address: \"127.0.0.1:%d\"\n", ports[opsListener])
	body = strings.Replace(body, opsListener, opsListener+listener, 1)
	body = strings.Replace(body, "  cors:\n", splitEndpoints+"  cors:\n", 1)
	if !strings.Contains(body, "    frontend:\n") ||
		!strings.Contains(body, "    web:\n      listener: frontend") {
		t.Fatalf("the split topology did not take:\n%s", body)
	}
	return body
}

// launch writes a configuration for the topology and starts the program over
// it. The working directory is a directory of the case's own, so the signing
// key the program generates on its first run (var/, relative to the working
// directory) never lands in the source tree.
func launch(t *testing.T, topo topology, without []string) (*variantProcess, map[string]int) {
	t.Helper()
	ports := freePorts(t, topo.ports)
	dir := t.TempDir()
	body := topo.render(t, ports)
	configPath := filepath.Join(dir, "demo.yaml")
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the configuration: %v", err)
	}
	binary, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatalf("resolving the test binary's path: %v", err)
	}
	env := []string{serveWithoutEnv + "=" + strings.Join(without, ",")}
	child := startChild(t, binary, dir, env,
		"--config", "file://"+configPath, "sqlite-memory", serveCommand)
	byName := map[string]int{"backend": ports[backendListener], "ops": ports[opsListener]}
	if topo.split {
		byName["frontend"] = ports[2]
	}
	return child, byName
}

// sendProbe makes one probe's request and reads the whole answer.
func sendProbe(t *testing.T, client *http.Client, ports map[string]int, p probe) rawResponse {
	t.Helper()
	url := fmt.Sprintf("http://127.0.0.1:%d%s", ports[p.listener], p.path)
	req, err := http.NewRequest(p.method, url, nil)
	if err != nil {
		t.Fatalf("building %s %s: %v", p.method, url, err)
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", p.method, url, err)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading the body of %s %s: %v", p.method, url, err)
	}
	return rawResponse{status: res.StatusCode, header: res.Header, body: body}
}

// firstDifference answers a description of the first difference between two
// answers, or "" when they are identical apart from Date: status, every header
// (names canonical, values in order) and the body. Nothing else is filtered; a
// header that changes between requests is reported for a person to judge.
func firstDifference(want, got rawResponse) string {
	if want.status != got.status {
		return fmt.Sprintf("status %d, want %d", got.status, want.status)
	}
	names := map[string]bool{}
	for name := range want.header {
		names[name] = true
	}
	for name := range got.header {
		names[name] = true
	}
	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)
	for _, name := range sorted {
		if http.CanonicalHeaderKey(name) == "Date" {
			continue
		}
		first, second := want.header.Values(name), got.header.Values(name)
		if !slices.Equal(first, second) {
			return fmt.Sprintf("header %s is %q, want %q", name, second, first)
		}
	}
	if !bytes.Equal(want.body, got.body) {
		return fmt.Sprintf("body differs: %q, want %q", got.body, want.body)
	}
	return ""
}

// runDifferential runs one topology's differential matrix: a baseline run, then
// one variant per group, with every non-exempt anonymous probe compared byte for
// byte apart from Date.
func runDifferential(t *testing.T, topo topology) {
	t.Helper()
	modules := baselineDescriptors(t)
	guarded := guardedEndpointSet(t)
	classes := classifyBaseline(t, modules, guarded)

	// Completeness: every module the baseline names has a class.
	for _, name := range baselineServe {
		if _, classified := classes[name]; !classified {
			t.Errorf("the module %q was not classified", name)
		}
	}

	baseline, ports := launch(t, topo, nil)
	if err := baseline.waitReady(startLimit); err != nil {
		t.Fatalf("the baseline %s run did not become ready: %v", topo.name, err)
	}

	document := getRaw(t, fmt.Sprintf("http://127.0.0.1:%d/openapi.json", ports["ops"]))
	if document.status != http.StatusOK {
		t.Fatalf("the baseline's /openapi.json answered %d: %s", document.status, document.body)
	}
	claims := claimsFromConfig(t, "the case's configuration", []byte(topo.render(t, []int{8080, 8083, 8084})))
	probes := buildProbes(t, document.body, claims, topo.split)
	table := parseFrozenTable(baseline.linesCopy())
	chains := parseChains(baseline.linesCopy())
	if len(chains) == 0 {
		t.Fatalf("the baseline wrote no assembly diagnostics:\n%s", baseline.output())
	}
	if len(table) == 0 {
		t.Fatalf("the baseline wrote no frozen anonymous surface:\n%s", baseline.output())
	}

	// The completeness guard: every layer before the gateway is a layer of the
	// entry point itself or one the mapping names, so a new shaping layer
	// cannot slip into the comparison unclassified.
	checkPreGateLayers(t, chains)

	// Non-vacuity, first half: the expectation table is the one the baseline's
	// own answers follow. A prefix the table says belongs to a guarded endpoint
	// answers 401 in that endpoint's realm, so the probes below really are
	// judged against the endpoint they are expected to reach.
	client := &http.Client{Timeout: 10 * time.Second}
	for prefix, endpoint := range expectedOwners {
		if !guarded[endpoint] {
			continue
		}
		answer := sendProbe(t, client, ports, probe{
			listener: "backend", method: http.MethodGet,
			path: prefix + "/zz-absent", endpoint: endpoint,
		})
		if answer.status != http.StatusUnauthorized {
			t.Errorf("GET %s answered %d, want 401 from the %s endpoint:\n%s",
				prefix+"/zz-absent", answer.status, endpoint, answer.body)
			continue
		}
		if challenge := answer.header.Get("Www-Authenticate"); !strings.Contains(challenge, `realm="`+endpoint+`"`) {
			t.Errorf("GET %s answered a 401 with %q, want a challenge for the %s realm: "+
				"the expectation table and the run disagree about who answers it",
				prefix+"/zz-absent", challenge, endpoint)
		}
	}

	// The comparator's own check: the same request twice is the same answer,
	// and two answers that differ are reported as different.
	first := getRaw(t, fmt.Sprintf("http://127.0.0.1:%d/api/v1/system/tenants", ports["backend"]))
	second := getRaw(t, fmt.Sprintf("http://127.0.0.1:%d/api/v1/system/tenants", ports["backend"]))
	if difference := firstDifference(first, second); difference != "" {
		t.Errorf("the comparator reports two identical requests as different: %s", difference)
	}
	methods := getRaw(t, fmt.Sprintf("http://127.0.0.1:%d/api/v1/auth/methods", ports["backend"]))
	absent := getRaw(t, fmt.Sprintf("http://127.0.0.1:%d/api/v1/auth/zz", ports["backend"]))
	if firstDifference(methods, absent) == "" {
		t.Errorf("the comparator reports GET /api/v1/auth/methods and GET /api/v1/auth/zz as identical")
	}

	opsRoutes := opsSurface(t, document.body)

	// The baseline's answers are collected once and reused by every variant.
	baselineAnswers := map[probe]rawResponse{}
	compared := 0
	for _, p := range probes {
		if hitsAnonymousSurface(table, opsRoutes, p) {
			continue
		}
		baselineAnswers[p] = sendProbe(t, client, ports, p)
		compared++
	}
	if compared == 0 {
		t.Fatalf("the matrix has no compared probe, so it would test nothing")
	}
	t.Logf("%s: %d probes, %d compared", topo.name, len(probes), compared)

	groups := buildGroups(modules, classes)
	if len(groups) == 0 {
		t.Fatalf("baselineServe holds no routing module, so there is nothing to remove")
	}
	for _, group := range groups {
		group := group
		t.Run(group.id, func(t *testing.T) {
			variant, variantPorts := launch(t, topo, group.modules)
			if err := variant.waitReady(startLimit); err != nil {
				exemption, failure := variantStartOutcome(group, err, variant.stderrOutput())
				if exemption != "" {
					t.Logf("dropping %v does not start; exempt: %s", group.modules, exemption)
				} else {
					t.Error(failure)
				}
				return
			}
			checkVariantRemoval(t, baseline, variant, group, chains)
			mismatches := 0
			for _, p := range probes {
				if hitsAnonymousSurface(table, opsRoutes, p) {
					continue
				}
				want, ok := baselineAnswers[p]
				if !ok {
					continue
				}
				got := sendProbe(t, client, variantPorts, p)
				if difference := firstDifference(want, got); difference != "" {
					mismatches++
					if mismatches <= 10 {
						t.Errorf("%s %s (expected %s): %s\nbaseline body:\n%s\nvariant body:\n%s",
							p.method, p.path, p.endpoint, difference, want.body, got.body)
					}
				}
			}
			if mismatches > 10 {
				t.Errorf("... and %d more differing answers", mismatches-10)
			}
		})
	}
}

// checkPreGateLayers holds every layer before the gateway to the entry point's
// own layers or the mapping. An unknown layer is a failure: the case has to be
// told what it shapes before it can leave the module in place.
func checkPreGateLayers(t *testing.T, chains map[string]chainLine) {
	t.Helper()
	for endpoint, chain := range chains {
		if !slices.Contains(chain.layers, "authn.gate") {
			// An endpoint the gateway does not guard has no pre-gate stretch;
			// every layer on it answers, and the classification covers it as
			// the endpoint's own chain.
			continue
		}
		for _, layer := range chain.layers {
			if layer == "authn.gate" {
				break
			}
			if strings.HasPrefix(layer, "http:") || layer == "holdAllow" {
				continue
			}
			if _, known := preGateLayers[layer]; known {
				continue
			}
			t.Errorf("endpoint %q carries the layer %q before the gateway, which the answer-shaping "+
				"mapping does not name: add it to preGateLayers and to the shaping class",
				endpoint, layer)
		}
	}
}

// checkVariantRemoval holds that the variant really left the modules out: the
// reported inclusion list does not name them, and the endpoints they mounted
// routes on lost routes.
func checkVariantRemoval(t *testing.T, baseline, variant *variantProcess, group variantGroup,
	baselineChains map[string]chainLine,
) {
	t.Helper()
	reported := variant.stderrOutput()
	line := ""
	for _, candidate := range strings.Split(reported, "\n") {
		if strings.HasPrefix(candidate, "serve-without: ") {
			line = candidate
			break
		}
	}
	if line == "" {
		t.Errorf("the variant wrote no serve-without line on standard error:\n%s", reported)
		return
	}
	included := strings.Split(strings.TrimPrefix(line, "serve-without: "), ",")
	for _, removed := range group.modules {
		if slices.Contains(included, removed) {
			t.Errorf("the run still includes %q, which the variant was to drop: %s", removed, line)
		}
	}

	variantChains := parseChains(variant.linesCopy())
	for _, removed := range group.modules {
		for _, endpoint := range endpointOfModule(t, removed) {
			before, known := baselineChains[endpoint]
			if !known {
				t.Errorf("the baseline wrote no chain for endpoint %q", endpoint)
				continue
			}
			after, seen := variantChains[endpoint]
			if !seen {
				t.Errorf("the variant wrote no chain for endpoint %q", endpoint)
				continue
			}
			if after.routes >= before.routes {
				if reason, exempt := mayLeaveRouteCountUnchanged[removed]; exempt {
					t.Logf("dropping %q leaves endpoint %q at %d route(s); exempt: %s",
						removed, endpoint, after.routes, reason)
					continue
				}
				t.Errorf("dropping %q left endpoint %q with %d route(s), not fewer than the baseline's %d: "+
					"the removal is vacuous", removed, endpoint, after.routes, before.routes)
			}
		}
	}
}

// endpointOfModule answers the guarded endpoints a baseline module's fragments
// describe; it is the endpoints whose route count a removal has to lower.
func endpointOfModule(t *testing.T, name string) []string {
	t.Helper()
	var endpoints []string
	for _, resource := range core.Resources[speedhttp.Spec](core.ProcessRegistry) {
		if resource.Module == name {
			endpoints = append(endpoints, resource.Value.Endpoint)
		}
	}
	return endpoints
}

// opsSurface answers the routes the operations fragment declares, as patterns.
func opsSurface(t *testing.T, document []byte) []anonRoute {
	t.Helper()
	var parsed struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(document, &parsed); err != nil {
		t.Fatalf("the served document is not JSON: %v", err)
	}
	var routes []anonRoute
	for path, item := range parsed.Paths {
		for method, raw := range item {
			if !slices.Contains(probeMethods, strings.ToUpper(method)) {
				continue
			}
			var operation struct {
				Endpoint string `json:"x-endpoint"`
			}
			if err := json.Unmarshal(raw, &operation); err != nil {
				t.Fatalf("the %s %s operation is not JSON: %v", method, path, err)
			}
			if operation.Endpoint == "ops" {
				routes = append(routes, anonRoute{
					method: strings.ToUpper(method),
					path:   patternParameter.ReplaceAllString(path, ":p"),
				})
			}
		}
	}
	return routes
}

// TestAnonymousAnswersDoNotDependOnTheAssembly runs the differential matrix on
// both deployment topologies.
func TestAnonymousAnswersDoNotDependOnTheAssembly(t *testing.T) {
	for _, topo := range []topology{
		{name: "merged", ports: listenerCount},
		{name: "split", ports: listenerCount + 1, split: true},
	} {
		topo := topo
		t.Run(topo.name, func(t *testing.T) { runDifferential(t, topo) })
	}
}
