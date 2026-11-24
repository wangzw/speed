package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
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
	"github.com/vislake/speed/starters/demo/server/internal/demotest"
)

const serveWithoutEnv = "DEMO_SERVE_WITHOUT"

func serveWithout(list string) {
	drop := map[string]bool{}
	for _, name := range strings.Split(list, ",") {
		if name = strings.TrimSpace(name); name != "" {
			drop[name] = true
		}
	}
	without := func(roster []string) []string {
		kept := make([]string, 0, len(roster))
		for _, name := range roster {
			if !drop[name] {
				kept = append(kept, name)
			}
		}
		return kept
	}
	baselineServe = without(baselineServe)
	capabilityModules = without(capabilityModules)
	kept := slices.Concat(baselineServe, capabilityModules)
	fmt.Fprintln(os.Stderr, "serve-without: "+strings.Join(kept, ","))
}

func serveRoster() []string {
	return slices.Concat(baselineServe, capabilityModules)
}

func expectedEndpoint(path string, split bool) string {
	owner, prefix := "", ""
	for claimed, endpoint := range demotest.ExpectedOwners() {
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

type probe struct {
	listener string
	method   string
	path     string
	endpoint string
}

var probeMethods = []string{
	http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
	http.MethodPatch, http.MethodDelete, http.MethodOptions,
}

var outsideClaims = []string{
	"/", "/admin/tenants", "/zz.json", "/.well-known/zz",
	"/assets/zz.js", "//api/zz", "/API/zz", "/apix",
}

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

	for prefix := range demotest.ExpectedOwners() {
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

type anonRoute struct {
	method string
	path   string
}

type frozenTable map[string][]anonRoute

var diagnosticPair = regexp.MustCompile(`([A-Za-z0-9_.-]+)="([^"]*)"`)

var routeToken = regexp.MustCompile(`^([A-Z]+) (/.*)$`)

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

// Such a request is not sent — sending it would change the rate limiter and the
// journal — because its answer is allowed to differ.
func hitsAnonymousSurface(table frozenTable, ops []anonRoute, p probe) bool {
	switch p.endpoint {
	case "web":
		return true
	case "ops":
		for _, route := range ops {
			if route.method == p.method && routeMatches(route.path, p.path) {
				return true
			}
		}
		return false
	case "":
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
// pattern: ":p" matches one segment, "*p" the rest, and trailing slashes are
// significant.
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

func querylessSegments(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

type chainLine struct {
	routes int
	layers []string
}

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

var shapingTokens = []core.Token{
	(*authn.Verifier)(nil),
	(*authn.Gate)(nil),
	(*speedhttp.ErrorResponder)(nil),
	(*i18n.Localizer)(nil),
	(*i18n.Resolver)(nil),
	(*i18n.Negotiator)(nil),
}

// A layer this case does not know is a failure: the answer-shaping set has to be
// decided for it before it can be left in place.
var preGateLayers = map[string]string{
	"observability.otel":       otel.ModuleName,
	"observability.otel:guard": otel.ModuleName,
	"i18n:accept-language":     i18nhttp.ModuleName,
	"http.cors":                cors.ModuleName,
	"ratelimit":                ratelimit.ModuleName,
	"ratelimit.address":        ratelimit.ModuleName,
}

type moduleClass int

const (
	classShaping moduleClass = iota
	classRouting
	classInfra
)

type assembledModule struct {
	name          string
	requires      []core.Requirement
	provides      []core.Token
	specEndpoints []string
}

func assembledDescriptors(t *testing.T) map[string]*assembledModule {
	t.Helper()
	named := map[string]bool{}
	for _, name := range serveRoster() {
		named[name] = true
	}
	modules := map[string]*assembledModule{}
	for _, module := range core.ProcessRegistry.Modules() {
		if !named[module.Name] {
			continue
		}
		described := &assembledModule{name: module.Name, requires: module.Requires}
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
	for _, name := range serveRoster() {
		if modules[name] == nil {
			t.Errorf("the serve roster names %q, which no module registers", name)
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

func classifyModules(t *testing.T, modules map[string]*assembledModule, guarded map[string]bool) map[string]moduleClass {
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

func guardedEndpointOf(module *assembledModule, guarded map[string]bool) bool {
	for _, endpoint := range module.specEndpoints {
		if guarded[endpoint] {
			return true
		}
	}
	return false
}

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

func withDependents(seed map[string]bool, modules map[string]*assembledModule) map[string]bool {
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

type variantGroup struct {
	id      string
	modules []string
}

var mayNotStartGroups = map[string]string{
	"org.personal": "authn.account requires the account provisioning seam this module provides, " +
		"so an assembly without it does not start: a user account's personal tenant is written " +
		"in the account's own transaction",
}

func variantStartOutcome(group variantGroup, err error, output string) (exemption string, failure error) {
	if err == nil {
		return "", nil
	}
	if reason, exempt := mayNotStartGroups[group.id]; exempt {
		return reason, nil
	}
	return "", fmt.Errorf("dropping %v left a run that does not start: %w\n%s", group.modules, err, output)
}

func routeCountOutcome(removed, endpoint string, before, after int) (exemption string, failure error) {
	if after < before {
		return "", nil
	}
	if reason, exempt := mayLeaveRouteCountUnchanged[removed]; exempt {
		return reason, nil
	}
	return "", fmt.Errorf("dropping %q left endpoint %q with %d route(s), not fewer than the baseline's %d: "+
		"the removal is vacuous", removed, endpoint, after, before)
}

// TestEveryRoutingVariantMustStart holds that every variant the case builds starts unless an exemption
// names it with its reason.
func TestEveryRoutingVariantMustStart(t *testing.T) {
	t.Parallel()
	modules := assembledDescriptors(t)
	classes := classifyModules(t, modules, guardedEndpointSet(t))
	groups := buildGroups(modules, classes)
	if len(groups) == 0 {
		t.Fatalf("the assembled serve roster holds no variant seed, so there is nothing to remove")
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
	for name, reason := range mayLeaveRouteCountUnchanged {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("mayLeaveRouteCountUnchanged exempts %q without a reason", name)
		}
		if _, known := modules[name]; !known {
			t.Errorf("mayLeaveRouteCountUnchanged names %q, which the assembled roster does not include", name)
			continue
		}
		if classes[name] != classRouting {
			t.Errorf("mayLeaveRouteCountUnchanged names %q, which this case does not classify as routing", name)
		}
	}
	for _, name := range capabilityModules {
		if classes[name] == classShaping {
			continue
		}
		if !built[name] {
			t.Errorf("the capability module %q is assembled but is not a variant seed: "+
				"the start property is not held for it", name)
		}
	}
}

func buildGroups(modules map[string]*assembledModule, classes map[string]moduleClass) []variantGroup {
	names := make([]string, 0, len(baselineServe))
	for _, name := range baselineServe {
		if modules[name] != nil {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	var groups []variantGroup
	built := map[string]bool{}
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
		built[id] = true
	}

	for _, name := range names {
		if classes[name] == classRouting {
			group(name, map[string]bool{name: true})
		}
	}

	var extras []string
	for _, name := range capabilityModules {
		if modules[name] != nil && !slices.Contains(names, name) && classes[name] != classShaping {
			extras = append(extras, name)
		}
	}
	sort.Strings(extras)
	for _, name := range extras {
		group(name, map[string]bool{name: true})
	}

	var mustNotStart []string
	for name := range mayNotStartGroups {
		mustNotStart = append(mustNotStart, name)
	}
	sort.Strings(mustNotStart)
	for _, name := range mustNotStart {
		if modules[name] != nil && !built[name] {
			group(name, map[string]bool{name: true})
		}
	}

	endpointSeeds := slices.Concat(names, extras)
	for _, endpoint := range []string{"admin", "api"} {
		seed := map[string]bool{}
		for _, name := range endpointSeeds {
			if classes[name] == classRouting && slices.Contains(modules[name].specEndpoints, endpoint) {
				seed[name] = true
			}
		}
		group(endpoint+"-all", seed)
	}
	return groups
}

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
	t.Cleanup(func() { p.stop(stopLimit()) })
	return p
}

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

func (p *variantProcess) output() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return strings.Join(p.lines, "\n")
}

func (p *variantProcess) linesCopy() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.lines)
}

func (p *variantProcess) stderrOutput() string {
	return p.stderr.String()
}

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

type topology struct {
	name  string
	ports int
	split bool
	body  func(*testing.T, []int, string) string
}

// http.endpoints is a container leaf, so a partial table would drop the
// endpoints it left out.
const splitEndpoints = `  endpoints:
    api:
      listener: backend
      prefixes: ["/api"]
    account:
      listener: backend
      prefixes: ["/api/v1/auth", "/api/v1/features", "/api/v1/orgs"]
    admin:
      listener: backend
      prefixes: ["/api/v1/system/audit", "/api/v1/system/auth", "/api/v1/system/permissions", "/api/v1/system/settings", "/api/v1/system/sms", "/api/v1/system/tenants"]
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

func (topo topology) render(t *testing.T, ports []int, dsn string) string {
	t.Helper()
	var body string
	if topo.body != nil {
		body = topo.body(t, ports[:listenerCount], dsn)
	} else {
		body = differentialConfigBody(t, ports[:listenerCount], dsn)
		body += "ratelimit:\n  address-endpoints: []\n"
	}
	if !topo.split {
		return body
	}
	backendListenerLine := fmt.Sprintf(
		"    backend:\n      address: \"127.0.0.1:%d\"\n", ports[backendListener])
	additions := fmt.Sprintf(
		"    ops:\n      address: \"127.0.0.1:%d\"\n"+
			"    frontend:\n      address: \"127.0.0.1:%d\"\n",
		ports[splitOpsListener], ports[splitFrontendListener])
	body = strings.Replace(body, backendListenerLine, backendListenerLine+additions, 1)
	body = strings.Replace(body, "  cors:\n", splitEndpoints+"  cors:\n", 1)
	if !strings.Contains(body, "    frontend:\n") ||
		!strings.Contains(body, "    web:\n      listener: frontend") {
		t.Fatalf("the split topology did not take:\n%s", body)
	}
	return body
}

func bindFailure(child *variantProcess) bool {
	return bindFailureText(child.stderrOutput())
}

const missingDependencyText = "core: capability has no available provider"

func startupFailureMiss(child *variantProcess, err error, want string) string {
	if err == nil {
		return fmt.Sprintf("the run started, want %s to stop it", want)
	}
	if bindFailure(child) {
		return fmt.Sprintf("the run exited because it could not bind a probed port, which is not %s: %v",
			want, err)
	}
	if strings.Contains(child.stderrOutput(), missingDependencyText) {
		return ""
	}
	return fmt.Sprintf("the run did not fail for %s: %v; its standard error says:\n%s",
		want, err, child.stderrOutput())
}

func startVariant(t *testing.T, topo topology, without []string) (*variantProcess, map[string]int, error) {
	t.Helper()
	return startVariantWith(t, topo, without, freePorts)
}

func startVariantWith(t *testing.T, topo topology, without []string,
	source func(*testing.T, int) []int,
) (*variantProcess, map[string]int, error) {
	t.Helper()
	run, _, err := startWithRetry(t, source, topo.ports,
		func(ports []int) variantRun {
			child, byName := launchOn(t, topo, without, ports)
			return variantRun{child: child, byName: byName}
		},
		func(run variantRun) error { return run.child.waitReady(startLimit()) },
		func(run variantRun) bool { return bindFailure(run.child) },
		func(run variantRun) { run.child.stop(stopLimit()) },
	)
	return run.child, run.byName, err
}

type variantRun struct {
	child  *variantProcess
	byName map[string]int
}

func launchOn(t *testing.T, topo topology, without []string, ports []int) (*variantProcess, map[string]int) {
	t.Helper()
	dir := t.TempDir()
	dsn := filepath.Join(dir, "demo.db")
	body := topo.render(t, ports, dsn)
	configPath := filepath.Join(dir, "demo.yaml")
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the configuration: %v", err)
	}
	seeded := runCommandOn(t, seedHostBinary(), "file://"+configPath, "sqlite", seedCommand)
	if seeded.err != nil {
		t.Fatalf("seeding the variant's database failed: %v\n%s", seeded.err, seeded.combined())
	}
	binary, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatalf("resolving the test binary's path: %v", err)
	}
	env := []string{serveWithoutEnv + "=" + strings.Join(without, ",")}
	child := startChild(t, binary, dir, env,
		"--config", "file://"+configPath, "sqlite", serveCommand)
	byName := map[string]int{
		"backend": ports[backendListener],
		"ops":     ports[backendListener],
	}
	if topo.split {
		byName["ops"] = ports[splitOpsListener]
		byName["frontend"] = ports[splitFrontendListener]
	}
	return child, byName
}

// TestATakenPortLeavesTheChildUnableToBind reproduces the gap freePorts leaves: a port taken between
// the probe and the bind fails the child.
func TestATakenPortLeavesTheChildUnableToBind(t *testing.T) {
	t.Parallel()
	topo := topology{name: "merged", ports: listenerCount}
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("binding a port to hold: %v", err)
	}
	defer func() { _ = blocker.Close() }()
	ports := []int{blocker.Addr().(*net.TCPAddr).Port}

	child, _ := launchOn(t, topo, nil, ports)
	if err := child.waitReady(startLimit()); err == nil {
		t.Fatalf("the child became ready on a port another listener holds")
	}
	if !bindFailure(child) {
		t.Fatalf("a taken port did not read as a bind failure:\n%s", child.stderrOutput())
	}
}

// TestBindFailureIsOnlyTheTakenAddress holds that a certificate that cannot load is not mistaken for a
// taken port.
func TestBindFailureIsOnlyTheTakenAddress(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		stderr string
		want   bool
	}{
		{
			name: "a certificate that cannot load shares the sentinel",
			stderr: `time=2026-11-17T00:00:00Z level=ERROR msg="http: listener could not bind ` +
				`its address: listener \"backend\" could not load the certificate \"/tmp/tls.crt\" ` +
				`with the key \"/tmp/tls.key\": tls: failed to find any PEM data in certificate input"`,
			want: false,
		},
		{
			name:   "the sentinel and an exit status without a taken address",
			stderr: "http: listener could not bind its address: listener \"backend\": exit status 1",
			want:   false,
		},
		{
			name:   "the net error without the listener sentinel",
			stderr: "listen tcp 127.0.0.1:8081: bind: address already in use",
			want:   false,
		},
		{
			name: "the sentinel with the taken address",
			stderr: `http: listener could not bind its address: listener "backend" on ` +
				`"127.0.0.1:8081": listen tcp 127.0.0.1:8081: bind: address already in use`,
			want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := bindFailureText(tc.stderr); got != tc.want {
				t.Errorf("bindFailureText(%q) = %v, want %v", tc.stderr, got, tc.want)
			}
		})
	}
}

// TestStartVariantRetriesAPortTakenInTheGap holds that a variant launch recovers over fresh ports
// instead of reporting the collision.
func TestStartVariantRetriesAPortTakenInTheGap(t *testing.T) {
	t.Parallel()
	topo := topology{name: "merged", ports: listenerCount}
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("binding a port to hold: %v", err)
	}
	defer func() { _ = blocker.Close() }()
	stolen := []int{blocker.Addr().(*net.TCPAddr).Port}

	attempts := 0
	source := func(t *testing.T, n int) []int {
		attempts++
		if attempts == 1 {
			return stolen
		}
		return freePorts(t, n)
	}
	child, ports, err := startVariantWith(t, topo, nil, source)
	if err != nil {
		t.Fatalf("the launch did not recover from a taken port: %v", err)
	}
	defer child.stop(stopLimit())
	if attempts < 2 {
		t.Fatalf("the launch became ready on its first attempt, so the retry was not exercised")
	}
	if ports["backend"] == stolen[backendListener] {
		t.Fatalf("the retry reused the port another listener holds")
	}
}

// TestStartServingRetriesAPortTakenInTheGap holds that the ordinary serving entry recovers over fresh
// ports instead of failing the case.
func TestStartServingRetriesAPortTakenInTheGap(t *testing.T) {
	t.Parallel()
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("binding a port to hold: %v", err)
	}
	defer func() { _ = blocker.Close() }()
	stolen := blocker.Addr().(*net.TCPAddr).Port

	attempts := 0
	source := func(t *testing.T, n int) []int {
		attempts++
		if attempts == 1 {
			return []int{stolen}
		}
		return freePorts(t, n)
	}
	child, ports, err := startServingFrom(t, source, func(ports []int) *process {
		dsn := filepath.Join(t.TempDir(), "retry.db")
		config := writeConfigWith(t, ports, dsn)
		if res := runCommand(t, config, "sqlite", seedCommand); res.err != nil {
			t.Fatalf("seed failed: %v\n%s", res.err, res.combined())
		}
		return start(t, config, "sqlite", serveCommand)
	})
	if err != nil {
		t.Fatalf("the launch did not recover from a taken port: %v\nstandard error:\n%s",
			err, child.stderr())
	}
	if attempts < 2 {
		t.Fatalf("the launch became ready on its first attempt, so the retry was not exercised")
	}
	if ports[backendListener] == stolen {
		t.Fatalf("the retry reused the port another listener holds")
	}
}

// TestTheStartupFailureCriterionRejectsABindFailure holds that a run which started or exited on a
// taken port is not the missing-dependency failure.
func TestTheStartupFailureCriterionRejectsABindFailure(t *testing.T) {
	t.Parallel()
	const want = "the missing provisioning seam"
	topo := topology{name: "criterion", ports: listenerCount}

	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("binding a port to hold: %v", err)
	}
	defer func() { _ = blocker.Close() }()
	taken := []int{blocker.Addr().(*net.TCPAddr).Port}
	collided, _ := launchOn(t, topo, nil, taken)
	waitErr := collided.waitReady(startLimit())
	if waitErr == nil {
		t.Fatalf("the child became ready on a port another listener holds")
	}
	if miss := startupFailureMiss(collided, waitErr, want); !strings.Contains(miss, "could not bind") {
		t.Fatalf("a run that could not bind a probed port read as %q, want it rejected as a bind failure", miss)
	}

	started, _, err := startVariant(t, topo, nil)
	if err != nil {
		t.Fatalf("the run did not become ready: %v", err)
	}
	defer started.stop(stopLimit())
	if miss := startupFailureMiss(started, nil, want); miss == "" {
		t.Fatalf("a run that started read as the held startup failure")
	}

	running := &variantProcess{stderr: &lockedBuffer{}}
	if miss := startupFailureMiss(running, errors.New("the run wrote no ready record within 1s"), want); miss == "" {
		t.Errorf("a run that timed out while still running read as %s", want)
	}
	unrelated := &variantProcess{stderr: &lockedBuffer{}}
	_, _ = unrelated.stderr.Write([]byte("serve: the certificate could not load"))
	if miss := startupFailureMiss(unrelated, errors.New("the run exited: exit status 1"), want); miss == "" {
		t.Errorf("a run that exited for an unrelated reason read as %s", want)
	}
}

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

// firstDifference answers the first difference apart from Date: status, every
// header (names canonical, values in order) and the body.
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

func runDifferential(t *testing.T, topo topology) {
	t.Helper()
	modules := assembledDescriptors(t)
	guarded := guardedEndpointSet(t)
	classes := classifyModules(t, modules, guarded)

	for _, name := range serveRoster() {
		if _, classified := classes[name]; !classified {
			t.Errorf("the module %q was not classified", name)
		}
	}

	baseline, ports, err := startVariant(t, topo, nil)
	if err != nil {
		t.Fatalf("the baseline %s run did not become ready: %v", topo.name, err)
	}

	claims := claimsFromConfig(t, "the case's configuration", []byte(topo.render(t, []int{8080, 8081, 8082}, ":memory:")))
	document := getRaw(t, fmt.Sprintf("http://127.0.0.1:%d/openapi.json", ports[claims["ops"].listener]))
	if document.status != http.StatusOK {
		t.Fatalf("the baseline's /openapi.json answered %d: %s", document.status, document.body)
	}
	probes := buildProbes(t, document.body, claims, topo.split)
	table := parseFrozenTable(baseline.linesCopy())
	chains := parseChains(baseline.linesCopy())
	if len(chains) == 0 {
		t.Fatalf("the baseline wrote no assembly diagnostics:\n%s", baseline.output())
	}
	if len(table) == 0 {
		t.Fatalf("the baseline wrote no frozen anonymous surface:\n%s", baseline.output())
	}

	checkPreGateLayers(t, chains)

	client := &http.Client{Timeout: probeTimeout()}
	for prefix, endpoint := range demotest.ExpectedOwners() {
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
			t.Parallel()
			variant, variantPorts, err := startVariant(t, topo, group.modules)
			defer variant.stop(stopLimit())
			if err != nil {
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

func checkPreGateLayers(t *testing.T, chains map[string]chainLine) {
	t.Helper()
	for endpoint, chain := range chains {
		if !slices.Contains(chain.layers, "authn.gate") {
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
			reason, failure := routeCountOutcome(removed, endpoint, before.routes, after.routes)
			if failure != nil {
				t.Error(failure)
			} else if reason != "" {
				t.Logf("dropping %q leaves endpoint %q at %d route(s); exempt: %s",
					removed, endpoint, after.routes, reason)
			}
		}
	}
}

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

func variantGroupNamed(t *testing.T, name string) variantGroup {
	t.Helper()
	modules := assembledDescriptors(t)
	classes := classifyModules(t, modules, guardedEndpointSet(t))
	for _, group := range buildGroups(modules, classes) {
		if group.id == name {
			return group
		}
	}
	t.Fatalf("the case builds no variant for %q", name)
	return variantGroup{}
}

func removalRouteCounts(t *testing.T, topo topology, groups []variantGroup) (map[string]chainLine, map[string]map[string]chainLine) {
	t.Helper()
	baseline, _, err := startVariant(t, topo, nil)
	if err != nil {
		t.Fatalf("the baseline %s run did not become ready: %v", topo.name, err)
	}
	before := parseChains(baseline.linesCopy())
	if len(before) == 0 {
		t.Fatalf("the baseline wrote no assembly diagnostics:\n%s", baseline.output())
	}
	after := map[string]map[string]chainLine{}
	for _, group := range groups {
		variant, _, err := startVariant(t, topo, group.modules)
		if err != nil {
			t.Fatalf("the variant %q did not become ready: %v", group.id, err)
		}
		chains := parseChains(variant.linesCopy())
		if len(chains) == 0 {
			t.Fatalf("the variant %q wrote no assembly diagnostics:\n%s", group.id, variant.output())
		}
		after[group.id] = chains
	}
	return before, after
}

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

// TestAnonymousAnswersDoNotDependOnTheAssembly runs the differential matrix on both deployment
// topologies, each variant in parallel with the others.
func TestAnonymousAnswersDoNotDependOnTheAssembly(t *testing.T) {
	t.Parallel()
	for _, topo := range []topology{
		{name: "merged", ports: listenerCount},
		{name: "split", ports: splitListenerCount, split: true},
	} {
		topo := topo
		t.Run(topo.name, func(t *testing.T) {
			t.Parallel()
			runDifferential(t, topo)
		})
	}
}
