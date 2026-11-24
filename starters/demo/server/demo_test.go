package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/core/coretest"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/ops/openapi"
	"github.com/vislake/speed/starters/demo/server/internal/demotest"
)

var hostBinary string

const deployDirEnv = "TREECHECK_DEPLOY_DIR"

func startLimit() time.Duration {
	return coretest.Scale(60 * time.Second)
}

func stopLimit() time.Duration {
	return coretest.Scale(30 * time.Second)
}

func commandLimit() time.Duration {
	return coretest.Scale(60 * time.Second)
}

func probeTimeout() time.Duration {
	return coretest.Scale(30 * time.Second)
}

const raceWarning = "WARNING: DATA RACE"

// TestMain builds the program once and runs the suite.
func TestMain(m *testing.M) {
	if list, ok := os.LookupEnv(serveWithoutEnv); ok {
		serveWithout(list)
		os.Exit(run())
	}

	// No case may see a host-prefix or OpenTelemetry variable: the cache key
	// cannot record what an in-process configuration load reads from them.
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if !strings.HasPrefix(name, envPrefix+"_") && !strings.HasPrefix(name, "OTEL_") {
			continue
		}
		if err := os.Unsetenv(name); err != nil {
			fmt.Fprintf(os.Stderr, "removing %s from the test process failed: %v\n", name, err)
			os.Exit(1)
		}
	}

	if _, ok := os.LookupEnv(testProcessProbeEnv); ok {
		os.Exit(m.Run())
	}

	if os.Getenv(deployDirEnv) != "" {
		os.Exit(m.Run())
	}

	dir, err := os.MkdirTemp("", "demo")
	if err != nil {
		fmt.Fprintln(os.Stderr, "creating the build directory failed:", err)
		os.Exit(1)
	}
	hostBinary = filepath.Join(dir, "demo")
	if err := demotest.BuildProgram(hostBinary, raceEnabled); err != nil {
		fmt.Fprintln(os.Stderr, err)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func childEnv() []string {
	return demotest.ChildEnv()
}

const testProcessProbeEnv = "TREECHECK_TEST_PROCESS_PROBE"

// TestChildEnvForwardsOnlyTheNamedVariables checks that a child receives a listed variable and not one
// the host exports outside the list.
func TestChildEnvForwardsOnlyTheNamedVariables(t *testing.T) {
	t.Setenv("TREECHECK_ENV_PROBE", "outside")
	t.Setenv("LANG", "C.UTF-8")
	env := childEnv()
	if slices.Contains(env, "TREECHECK_ENV_PROBE=outside") {
		t.Errorf("childEnv forwarded TREECHECK_ENV_PROBE, which its list does not name")
	}
	if !slices.Contains(env, "LANG=C.UTF-8") {
		t.Errorf("childEnv did not forward LANG=C.UTF-8, which its list names")
	}
}

// TestTheTestProcessDropsTheHostPrefix checks that a fresh run of this test binary does not inherit a
// DEMO_* variable from the host.
func TestTheTestProcessDropsTheHostPrefix(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), commandLimit())
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run", "^TestTheHostPrefixIsAbsent$")
	cmd.Env = append(os.Environ(), "DEMO_PROBE=1", testProcessProbeEnv+"=1")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("the re-entered test binary was still running after the limit and was killed: %v", ctx.Err())
	}
	if err != nil {
		t.Fatalf("%s: %v\n%s", reentryFailure(out), err, out)
	}
}

func reentryFailure(out []byte) string {
	if strings.Contains(string(out), "inherited DEMO_PROBE") {
		return "the re-entered test binary inherited DEMO_PROBE from the host"
	}
	return "the re-entered test binary failed for a reason other than the host prefix"
}

// TestReentryFailureNamesTheActualCause checks that a failure unrelated to the host prefix is not
// reported as the prefix surviving.
func TestReentryFailureNamesTheActualCause(t *testing.T) {
	probe := []byte("--- FAIL: TestTheHostPrefixIsAbsent\n" +
		"    demo_test.go:167: the test process inherited DEMO_PROBE from the host\n")
	if got := reentryFailure(probe); !strings.Contains(got, "inherited DEMO_PROBE") {
		t.Errorf("the host-prefix failure is not named: %s", got)
	}
	if got := reentryFailure([]byte("building the demo program failed: exit status 1\n")); strings.Contains(got, "DEMO_PROBE") {
		t.Errorf("a failure that is not the probe is reported as one: %s", got)
	}
}

// TestTheHostPrefixIsAbsent is the case a fresh process runs: it fails when DEMO_PROBE survived into
// it.
func TestTheHostPrefixIsAbsent(t *testing.T) {
	if _, ok := os.LookupEnv(testProcessProbeEnv); !ok {
		return
	}
	if _, ok := os.LookupEnv("DEMO_PROBE"); ok {
		t.Fatalf("the test process inherited DEMO_PROBE from the host")
	}
}

const demoSeedSection = `seed:
  accounts:
    - identifier: "operator"
      password: "operator-dev-secret"
      display-name: "Demo Operator"
      platform-admin: true
    - identifier: "ops"
      password-hash: "$argon2id$v=19$m=19456,t=2,p=1$xeAjr/xEt4mML0QaEhw8pw$Khjd7856RU6MatFxmOtE0YFZJLsC05yxX5HwJhpBXno"
      display-name: "Demo Ops"
  tenants:
    - name: demo
      admins:
        - operator
  # Lowered to the domain minimum for local demonstration: impersonation
  # requires a non-SMS factor that has been active for at least
  # authn.mfa.sensitive-min-age, and the declared default of 24h leaves a
  # freshly enrolled factor ineligible for a day. Production keeps the
  # declared default; seed writes a value only while the global layer holds
  # no explicit row for the key.
  settings:
    - key: "authn.mfa.sensitive-min-age"
      value: "1h"`

const demoConfig = `http:
  listeners:
    backend:
      address: "127.0.0.1:%d"
  cors:
    endpoints:
      api:
        allow-origins:
          - "https://app.demo.test"
ops:
  title: demo
  version: 0.1.0
  description: "The demo assembly's API: every operation the modules named in this stack declare, merged from their OpenAPI fragments."
  servers: ["http://localhost:8081"]
observability:
  otel:
    service-name: demo
authn:
  gate:
    mount:
      public-url: "http://localhost:8081"
  token:
    signing-key-file: "%s"
  mfa:
    totp:
      issuer: demo
    webauthn:
      rp-id: localhost
      rp-name: demo
      origins:
        - "http://localhost:8081"
objectstore:
  fs:
    root: "%s"
%s
db:
  sqlite:
    dsn: "%s"
    encryption-key: "dev-only-change-me-0123456789abcdef"
`

const (
	backendListener = iota
	listenerCount
)

const (
	splitOpsListener      = 1
	splitFrontendListener = 2
	splitListenerCount    = 3
)

func freePorts(t *testing.T, n int) []int {
	t.Helper()
	ports := make([]int, 0, n)
	listeners := make([]net.Listener, 0, n)
	for range n {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("finding a free port: %v", err)
		}
		listeners = append(listeners, l)
		ports = append(ports, l.Addr().(*net.TCPAddr).Port)
	}
	for _, l := range listeners {
		_ = l.Close()
	}
	return ports
}

func writeConfig(t *testing.T, ports []int) string {
	t.Helper()
	return writeConfigWith(t, ports, ":memory:")
}

// writeConfigWith is writeConfig with the database locator chosen by the
// caller, which is what lets two commands share a file.
func writeConfigWith(t *testing.T, ports []int, dsn string) string {
	t.Helper()
	return writeConfigFile(t, configBody(t, ports, dsn))
}

func writeConfigReplacing(t *testing.T, ports []int, dsn, old, new string) string {
	t.Helper()
	body := configBody(t, ports, dsn)
	replaced := strings.Replace(body, old, new, 1)
	if replaced == body {
		t.Fatalf("the configuration holds no %q to replace", old)
	}
	return writeConfigFile(t, replaced)
}

func configBody(t *testing.T, ports []int, dsn string) string {
	t.Helper()
	key := filepath.Join(t.TempDir(), "token-signing-key.pem")
	if err := ensureSigningKey(key); err != nil {
		t.Fatalf("minting the case's token signing key: %v", err)
	}
	objects := filepath.Join(t.TempDir(), "objects")
	return fmt.Sprintf(demoConfig, ports[0], key, objects,
		demoSeedSection, dsn)
}

func writeConfigFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "demo.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the configuration: %v", err)
	}
	return "file://" + path
}

type commandResult struct {
	stdout string
	stderr string
	err    error
}

func (r commandResult) combined() string {
	return "standard output:\n" + r.stdout + "\nstandard error:\n" + r.stderr
}

func runCommand(t *testing.T, config string, args ...string) commandResult {
	t.Helper()
	return runCommandOn(t, hostBinary, config, args...)
}

func seedHostBinary() string {
	return hostBinary
}

func runCommandOn(t *testing.T, binary, config string, args ...string) commandResult {
	t.Helper()
	return runCommandArgs(t, binary, "", append([]string{"--config", config}, args...), nil)
}

func runCommandArgs(t *testing.T, binary, dir string, args, extraEnv []string) commandResult {
	t.Helper()
	if binary == "" {
		t.Fatal("this test binary was started only for the deployment-template cases, so it built no program")
	}
	limit := commandLimit()
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = dir
	cmd.Env = append(childEnv(), extraEnv...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		err = fmt.Errorf("the command was still running after %s and was killed: %w", limit, ctx.Err())
	}
	res := commandResult{stdout: stdout.String(), stderr: stderr.String(), err: err}
	if strings.Contains(res.stderr, raceWarning) {
		t.Errorf("%s reported a data race:\n%s", strings.Join(args, " "), res.combined())
	}
	return res
}

type process struct {
	cmd *exec.Cmd

	mu          sync.Mutex
	lines       []string
	diagnostics []string

	ready    chan struct{}
	readyOne sync.Once
	done     chan struct{}
	readers  sync.WaitGroup

	waitErr  error
	stopOnce sync.Once
}

func start(t *testing.T, config string, args ...string) *process {
	t.Helper()
	return startArgs(t, "", append([]string{"--config", config}, args...), nil)
}

func startArgs(t *testing.T, dir string, args, extraEnv []string) *process {
	t.Helper()
	return startProcess(t, hostBinary, dir, args, extraEnv)
}

func startProcess(t *testing.T, binary, dir string, args, extraEnv []string) *process {
	t.Helper()
	if binary == "" {
		t.Fatal("this test binary was started only for the deployment-template cases, so it built no program")
	}
	cmd := exec.Command(binary, args...)
	cmd.Dir = dir
	cmd.Env = append(childEnv(), extraEnv...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("opening the child's standard output: %v", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("opening the child's standard error: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting %s: %v", strings.Join(args, " "), err)
	}
	p := &process{cmd: cmd, ready: make(chan struct{}), done: make(chan struct{})}
	p.readers.Add(2)
	go func() {
		defer p.readers.Done()
		p.consume(stdout, false)
	}()
	go func() {
		defer p.readers.Done()
		p.consume(stderr, true)
	}()
	go func() {
		// Wait closes the pipes, so the readers must reach EOF first or a child's last
		// write is lost.
		p.readers.Wait()
		p.waitErr = cmd.Wait()
		close(p.done)
	}()
	t.Cleanup(func() {
		p.stop(t)
		if diagnostics := p.stderr(); strings.Contains(diagnostics, raceWarning) {
			t.Errorf("%s reported a data race:\n%s", strings.Join(args, " "), diagnostics)
		}
	})
	return p
}

func (p *process) consume(r io.Reader, diagnostic bool) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		p.mu.Lock()
		if diagnostic {
			p.diagnostics = append(p.diagnostics, line)
		} else {
			p.lines = append(p.lines, line)
		}
		p.mu.Unlock()
		if !diagnostic && strings.Contains(line, "msg="+readyMsg) {
			p.readyOne.Do(func() { close(p.ready) })
		}
	}
}

func (p *process) output() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return strings.Join(p.lines, "\n")
}

func (p *process) stderr() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return strings.Join(p.diagnostics, "\n")
}

// waitReadyErr waits for the ready record and answers why it never came, after
// draining the readers.
func (p *process) waitReadyErr(limit time.Duration) error {
	select {
	case <-p.ready:
		return nil
	case <-p.done:
		return fmt.Errorf("serve exited before it was ready: %w", p.waitErr)
	case <-time.After(limit):
		return fmt.Errorf("serve did not write its ready record within %s", limit)
	}
}

func (p *process) stop(t *testing.T) {
	t.Helper()
	p.stopOnce.Do(func() {
		if p.cmd.Process == nil {
			return
		}
		_ = p.cmd.Process.Signal(syscall.SIGTERM)
		limit := stopLimit()
		select {
		case <-p.done:
		case <-time.After(limit):
			_ = p.cmd.Process.Kill()
			<-p.done
			t.Errorf("serve did not leave within %s of SIGTERM\n%s", limit, p.output())
		}
		p.readers.Wait()
	})
}

// TestProcessReadsTheLastLineBeforeExit holds the captured standard error to a line written on the
// child's last write.
func TestProcessReadsTheLastLineBeforeExit(t *testing.T) {
	t.Parallel()
	const marker = "the last line the child wrote"
	const script = `( sleep 1; printf '` + marker + `\n' >&2 ) & exit 1`
	child := startProcess(t, "/bin/sh", t.TempDir(), []string{"-c", script}, nil)
	<-child.done
	child.stop(t)
	if !strings.HasSuffix(child.stderr(), marker) {
		t.Fatalf("the child's last line was not read; standard error is %q", child.stderr())
	}
}

func (p *process) bindFailure() bool {
	return bindFailureText(p.stderr())
}

const launchRetries = 3

// bindFailureText recognises a taken port from the bind sentinel and the net
// error, so a certificate that cannot load is not retried.
func bindFailureText(stderr string) bool {
	return strings.Contains(stderr, speedhttp.ErrListen.Error()) &&
		strings.Contains(stderr, "address already in use")
}

func startWithRetry[T any](t *testing.T,
	source func(*testing.T, int) []int, portCount int,
	attempt func(ports []int) T,
	waitReady func(T) error,
	failedByBind func(T) bool,
	stop func(T),
) (T, []int, error) {
	t.Helper()
	for round := 0; ; round++ {
		ports := source(t, portCount)
		child := attempt(ports)
		err := waitReady(child)
		if err == nil || !failedByBind(child) || round >= launchRetries {
			return child, ports, err
		}
		stop(child)
		t.Logf("a port was taken before the child could bind it; retrying with fresh ports (%d/%d)",
			round+1, launchRetries)
	}
}

func startServing(t *testing.T, attempt func(ports []int) *process) (*process, []int) {
	t.Helper()
	child, ports, err := startServingFrom(t, freePorts, attempt)
	if err != nil {
		t.Fatalf("%v\n%s", err, child.output())
	}
	return child, ports
}

func startServingFrom(t *testing.T, source func(*testing.T, int) []int,
	attempt func(ports []int) *process,
) (*process, []int, error) {
	t.Helper()
	return startWithRetry(t, source, listenerCount, attempt,
		func(p *process) error { return p.waitReadyErr(startLimit()) },
		(*process).bindFailure,
		func(p *process) { p.stop(t) },
	)
}

type response struct {
	status int
	body   []byte
}

func get(t *testing.T, url string) response {
	t.Helper()
	return send(t, &http.Client{Timeout: probeTimeout()}, http.MethodGet, url, nil, nil)
}

type rawResponse struct {
	status int
	header http.Header
	body   []byte
}

func getRaw(t *testing.T, url string) rawResponse {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("building GET %s: %v", url, err)
	}
	res, err := (&http.Client{Timeout: probeTimeout()}).Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading the body of GET %s: %v", url, err)
	}
	return rawResponse{status: res.StatusCode, header: res.Header, body: body}
}

// sameExceptDate holds two answers identical apart from Date: status, every
// header and the body.
func sameExceptDate(t *testing.T, what string, want, got rawResponse) {
	t.Helper()
	if want.status != got.status {
		t.Errorf("%s: the statuses differ: %d and %d\nfirst body:\n%s\nsecond body:\n%s",
			what, want.status, got.status, want.body, got.body)
	}
	names := map[string]bool{}
	for name := range want.header {
		names[name] = true
	}
	for name := range got.header {
		names[name] = true
	}
	for name := range names {
		if http.CanonicalHeaderKey(name) == "Date" {
			continue
		}
		first, second := want.header.Values(name), got.header.Values(name)
		if !slices.Equal(first, second) {
			t.Errorf("%s: the %s headers differ: %q and %q", what, name, first, second)
		}
	}
	if !bytes.Equal(want.body, got.body) {
		t.Errorf("%s: the bodies differ:\nfirst:\n%s\nsecond:\n%s", what, want.body, got.body)
	}
}

// send makes one request and reads the whole answer; withTenant adds X-Tenant,
// and tenantOfPath answers the tenant a /api/v1/tenants/ path names.
func withTenant(headers map[string]string, tenant string) map[string]string {
	out := make(map[string]string, len(headers)+1)
	for name, value := range headers {
		out[name] = value
	}
	out["X-Tenant"] = tenant
	return out
}

func tenantOfPath(path string) string {
	rest, ok := strings.CutPrefix(path, "/api/v1/tenants/")
	if !ok {
		return ""
	}
	tenant, _, _ := strings.Cut(rest, "/")
	return tenant
}

func send(t *testing.T, client *http.Client, method, url string, body any,
	headers map[string]string,
) response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encoding the body of %s %s: %v", method, url, err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("building %s %s: %v", method, url, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer func() { _ = res.Body.Close() }()
	answer, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading the body of %s %s: %v", method, url, err)
	}
	return response{status: res.StatusCode, body: answer}
}

// signIn runs a password login to the end and answers with the access token.
func signIn(t *testing.T, accountBase, identifier, password string) string {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("building a cookie jar: %v", err)
	}
	client := &http.Client{Timeout: probeTimeout(), Jar: jar}
	credentials := map[string]any{
		"method":   "password",
		"delivery": "token",
		"binding":  "cookie",
		"input":    map[string]string{"identifier": identifier, "password": password},
	}
	if res := send(t, client, http.MethodPost, accountBase+"/api/v1/auth/flows", credentials, nil); res.status != http.StatusOK {
		t.Fatalf("starting the login flow answered %d: %s", res.status, res.body)
	}
	answer := send(t, client, http.MethodPost, accountBase+"/api/v1/auth/flows/answer",
		map[string]string{"step": "complete"}, nil)
	if answer.status != http.StatusOK {
		t.Fatalf("completing the login flow answered %d: %s", answer.status, answer.body)
	}
	var token struct {
		AccessToken string `json:"access_token"`
	}
	if err = json.Unmarshal(answer.body, &token); err != nil {
		t.Fatalf("reading the login's answer: %v\n%s", err, answer.body)
	}
	if token.AccessToken == "" {
		t.Fatalf("the login answered no access token: %s", answer.body)
	}
	return token.AccessToken
}

// TestServeAnswersTheProbes seeds and serves on a file-backed database and reads the operations probes
// and the merged document.
func TestServeAnswersTheProbes(t *testing.T) {
	t.Parallel()
	var config string
	serve, ports := startServing(t, func(ports []int) *process {
		config = writeConfigWith(t, ports, filepath.Join(t.TempDir(), "probes.db"))
		if res := runCommand(t, config, "sqlite", seedCommand); res.err != nil {
			t.Fatalf("seed failed: %v\n%s", res.err, res.combined())
		}
		return start(t, config, "sqlite", serveCommand)
	})

	chains := parseChains(strings.Split(serve.output(), "\n"))
	account, ok := chains["account"]
	if !ok {
		t.Fatalf("the run wrote no assembly line for the account endpoint:\n%s", serve.output())
	}
	shield, gate := slices.Index(account.layers, "ratelimit.address"), slices.Index(account.layers, "authn.gate")
	if shield < 0 {
		t.Fatalf("the account endpoint's chain %v carries no address shield: the configured "+
			"address-endpoints list did not reach the endpoint", account.layers)
	}
	if gate < 0 {
		t.Fatalf("the account endpoint's chain %v carries no authentication gateway", account.layers)
	}
	if shield > gate {
		t.Errorf("the account endpoint's chain %v runs the address shield inside the gateway, "+
			"so it would count requests after identity is settled", account.layers)
	}

	opsBase := fmt.Sprintf("http://127.0.0.1:%d", ports[backendListener])

	health := get(t, opsBase+"/health")
	if health.status != http.StatusOK {
		t.Fatalf("GET /health answered %d, want 200:\n%s", health.status, health.body)
	}
	if !strings.Contains(string(health.body), `"ok"`) {
		t.Errorf("GET /health answered %s, want a status of ok", health.body)
	}

	ready := get(t, opsBase+"/ready")
	if ready.status != http.StatusOK {
		t.Fatalf("GET /ready answered %d, want 200:\n%s", ready.status, ready.body)
	}

	document := get(t, opsBase+"/openapi.json")
	if document.status != http.StatusOK {
		t.Fatalf("GET /openapi.json answered %d, want 200:\n%s", document.status, document.body)
	}
	var merged struct {
		Paths map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(document.body, &merged); err != nil {
		t.Fatalf("the served document is not JSON: %v\n%s", err, document.body)
	}
	if len(merged.Paths) == 0 {
		t.Errorf("the served document holds no paths:\n%s", document.body)
	}

	web := get(t, fmt.Sprintf("http://127.0.0.1:%d/", ports[backendListener]))
	switch web.status {
	case http.StatusOK:
		if !strings.Contains(string(web.body), "<!doctype html>") {
			t.Errorf("GET / on the web endpoint answered 200 with a body that is not an HTML entry document:\n%s", web.body)
		}
	case http.StatusNotFound:
		if !strings.Contains(string(web.body), `"status":404`) {
			t.Errorf("GET / on the web endpoint answered 404 without the problem document:\n%s", web.body)
		}
	default:
		t.Errorf("GET / on the web endpoint answered %d, want 200 with the built frontend or 404 without it", web.status)
	}

	accountBase := fmt.Sprintf("http://127.0.0.1:%d", ports[backendListener])
	for _, path := range []string{"/api/v1/auth/methods", "/api/v1/auth/jwks.json"} {
		res := get(t, accountBase+path)
		if res.status != http.StatusOK {
			t.Errorf("GET %s on the account endpoint answered %d, want 200:\n%s", path, res.status, res.body)
		}
	}

	templates := getRaw(t, accountBase+"/api/v1/system/sms/templates")
	tenants := getRaw(t, accountBase+"/api/v1/system/tenants")
	for _, answer := range []struct {
		path string
		got  rawResponse
	}{
		{"/api/v1/system/sms/templates", templates},
		{"/api/v1/system/tenants", tenants},
	} {
		if answer.got.status != http.StatusUnauthorized {
			t.Errorf("GET %s on the admin endpoint answered %d, want 401:\n%s",
				answer.path, answer.got.status, answer.got.body)
		}
		if challenge := answer.got.header.Get("Www-Authenticate"); !strings.Contains(challenge, `realm="admin"`) {
			t.Errorf("GET %s answered a 401 with %q, want a challenge for the admin realm",
				answer.path, challenge)
		}
	}
	sameExceptDate(t, "GET /api/v1/system/sms/templates and GET /api/v1/system/tenants", templates, tenants)

	checkPlatformClaims(t, config, document.body)

	discovery := get(t, accountBase+"/api/v1/auth/methods")
	var listed struct {
		MFA struct {
			Factors []string `json:"factors"`
		} `json:"mfa"`
	}
	if err := json.Unmarshal(discovery.body, &listed); err != nil {
		t.Fatalf("GET /api/v1/auth/methods is not JSON: %v\n%s", err, discovery.body)
	}
	if !slices.Contains(listed.MFA.Factors, "totp") || !slices.Contains(listed.MFA.Factors, "webauthn") {
		t.Errorf("the deployment lists factor kinds %v, want both totp and webauthn: a step-up needs a "+
			"kind the sign-in kind is not, so a single-kind deployment cannot complete one",
			listed.MFA.Factors)
	}

	if out := serve.output(); !strings.Contains(out, "notification.preferences: the preference routes are mounted") {
		t.Errorf("serve reported no preference mount, so the preference matrix was not assembled:\n%s", out)
	}
}

// TestTheGuardsOfferNoBasicChallenge holds that a 401 on a guarded endpoint carries no Basic
// challenge.
func TestTheGuardsOfferNoBasicChallenge(t *testing.T) {
	t.Parallel()
	_, ports := startServing(t, func(ports []int) *process {
		config := writeConfigWith(t, ports, filepath.Join(t.TempDir(), "challenge.db"))
		if res := runCommand(t, config, "sqlite", seedCommand); res.err != nil {
			t.Fatalf("seed failed: %v\n%s", res.err, res.combined())
		}
		return start(t, config, "sqlite", serveCommand)
	})

	base := fmt.Sprintf("http://127.0.0.1:%d", ports[backendListener])
	client := &http.Client{Timeout: probeTimeout()}
	for _, path := range []string{
		"/api/v1/auth/me",
		"/api/v1/system/tenants",
		"/api/v1/notifications/messages",
	} {
		req, err := http.NewRequest(http.MethodGet, base+path, nil)
		if err != nil {
			t.Fatalf("building GET %s: %v", path, err)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Errorf("GET %s answered %d, want 401", path, res.StatusCode)
		}
		challenge := res.Header.Get("Www-Authenticate")
		if strings.Contains(challenge, "Basic") {
			t.Errorf("GET %s answered a 401 with %q: a browser answers a Basic challenge with its "+
				"native password prompt, so the guarded endpoints must carry none", path, challenge)
		}
		if !strings.Contains(challenge, "Bearer") {
			t.Errorf("GET %s answered a 401 with %q, want a Bearer challenge", path, challenge)
		}
	}
}

// TestGenWritesTheMergedDocument runs gen and reads the document it writes to a file and to standard
// output.
func TestGenWritesTheMergedDocument(t *testing.T) {
	t.Parallel()
	config := writeConfig(t, freePorts(t, listenerCount))

	type merged struct {
		OpenAPI string `json:"openapi"`
		Info    struct {
			Title   string `json:"title"`
			Version string `json:"version"`
		} `json:"info"`
		Paths     map[string]map[string]json.RawMessage `json:"paths"`
		Fragments []struct {
			Module   string `json:"module"`
			Endpoint string `json:"endpoint"`
		} `json:"x-fragments"`
	}

	check := func(t *testing.T, body []byte) {
		t.Helper()
		var doc merged
		if err := json.Unmarshal(body, &doc); err != nil {
			t.Fatalf("the document gen wrote is not JSON: %v\n%s", err, body)
		}
		if doc.OpenAPI != "3.1.0" {
			t.Errorf("the document declares openapi %q, want 3.1.0", doc.OpenAPI)
		}
		if doc.Info.Title != "demo-api" || doc.Info.Version != "9.9.9" {
			t.Errorf("the document's info is %q %q, want demo-api 9.9.9", doc.Info.Title, doc.Info.Version)
		}
		if len(doc.Paths) == 0 {
			t.Fatalf("the document holds no paths:\n%s", body)
		}
		for _, path := range []string{"/health", "/ready", "/openapi.json"} {
			if _, ok := doc.Paths[path]["get"]; !ok {
				t.Errorf("the document has no GET %s", path)
			}
		}
		platform := platformModulePath()
		expected := map[string]bool{}
		declaring := map[string]string{}
		for _, spec := range core.Resources[speedhttp.Spec](core.ProcessRegistry) {
			descriptor, found := core.ProcessRegistry.Lookup(spec.Module)
			if !found {
				t.Fatalf("module %q declares a fragment but is not registered", spec.Module)
			}
			owner, ok := modulePackage(descriptor)
			if !ok {
				t.Fatalf("module %q carries no lifecycle callback, so the package it is declared in cannot be read", spec.Module)
			}
			applicationOwned := owner != platform && !strings.HasPrefix(owner, platform+"/")
			var fragment struct {
				Paths map[string]map[string]json.RawMessage `json:"paths"`
			}
			if err := json.Unmarshal(spec.Value.Document, &fragment); err != nil {
				t.Fatalf("reading the fragment module %q declares: %v", spec.Module, err)
			}
			for path, methods := range fragment.Paths {
				absolute := spec.Value.ResolvePath(path)
				for method := range methods {
					key := strings.ToUpper(method) + " " + absolute
					if _, duplicate := expected[key]; duplicate {
						t.Fatalf("%s is declared by more than one fragment", key)
					}
					expected[key] = applicationOwned
					declaring[key] = spec.Module
				}
			}
		}
		if applicationOwned, declared := expected["POST /api/v1/orgs"]; !declared {
			t.Errorf("the document has no POST /api/v1/orgs, which the self-service creation module declares")
		} else if applicationOwned {
			t.Errorf("the self-service creation route is application-owned; it is a platform module's route")
		}
		present := map[string]bool{}
		for path, methods := range doc.Paths {
			for method, raw := range methods {
				key := strings.ToUpper(method) + " " + path
				present[key] = true
				var operation struct {
					Tags []string `json:"tags"`
				}
				if err := json.Unmarshal(raw, &operation); err != nil {
					t.Fatalf("reading %s %s: %v", strings.ToUpper(method), path, err)
				}
				carries := slices.Contains(operation.Tags, applicationOperationTag)
				want, declared := expected[key]
				switch {
				case !declared:
					t.Errorf("%s is in the document but no fragment declares it", key)
				case want && !carries:
					t.Errorf("%s is declared by %q, an application module outside the platform module %s, but carries tags %v, want %q among them",
						key, declaring[key], platform, operation.Tags, applicationOperationTag)
				case !want && carries:
					t.Errorf("%s is declared by %q, whose code lives under the platform module %s, yet carries %q",
						key, declaring[key], platform, applicationOperationTag)
				}
			}
		}
		for key := range expected {
			if !present[key] {
				t.Errorf("the document has no %s, which module %q declares", key, declaring[key])
			}
		}
		named := false
		for _, fragment := range doc.Fragments {
			if fragment.Module == "ops" {
				named = true
			}
		}
		if !named {
			t.Errorf("the document's x-fragments names no ops fragment: %+v", doc.Fragments)
		}

		declared := map[string]int{}
		for _, spec := range core.Resources[speedhttp.Spec](core.ProcessRegistry) {
			declared[spec.Module]++
		}
		merged := map[string]int{}
		for _, fragment := range doc.Fragments {
			merged[fragment.Module]++
		}
		if len(merged) != len(declared) {
			t.Errorf("the document names %d fragments, the binary declares %d:\ndocument: %v\ndeclared: %v",
				len(merged), len(declared), merged, declared)
		}
		for module := range declared {
			if merged[module] == 0 {
				t.Errorf("the document names no fragment for %s, which declares one", module)
			}
		}

		for _, module := range []string{"sms.admin", "authn.sso.saml", "notification.preferences"} {
			if merged[module] == 0 {
				t.Errorf("the document names no fragment for %s: it declares one, and no command has "+
					"to name it for the contract to carry it", module)
			}
		}
	}

	t.Run("to a file", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "openapi.json")
		res := runCommand(t, config, "sqlite-memory", genCommand,
			"--title", "demo-api", "--version", "9.9.9", "-o", out)
		if res.err != nil {
			t.Fatalf("gen failed: %v\n%s", res.err, res.combined())
		}
		body, err := os.ReadFile(out)
		if err != nil {
			t.Fatalf("reading what gen wrote: %v", err)
		}
		check(t, body)
	})

	t.Run("to standard output", func(t *testing.T) {
		res := runCommand(t, config, "sqlite-memory", genCommand,
			"--title", "demo-api", "--version", "9.9.9")
		if res.err != nil {
			t.Fatalf("gen failed: %v\n%s", res.err, res.combined())
		}
		check(t, []byte(res.stdout))
	})
}

// TestMarkOperationsWithNoOwnedOperationIsIdentity pins that with no application-owned operation the
// marked document equals Merge's.
func TestMarkOperationsWithNoOwnedOperationIsIdentity(t *testing.T) {
	t.Parallel()
	specs := core.Resources[speedhttp.Spec](core.ProcessRegistry)
	merged, err := openapi.Merge(specs, openapi.Options{Title: "demo-api", Version: "9.9.9"})
	if err != nil {
		t.Fatalf("merging the declared fragments: %v", err)
	}
	unmarked, err := markOperations(merged, map[string]bool{})
	if err != nil {
		t.Fatalf("marking no operation: %v", err)
	}
	if !bytes.Equal(unmarked, merged) {
		t.Errorf("marking no operation changed the document: %d bytes in, %d bytes out", len(merged), len(unmarked))
	}
}

const (
	seedIdentifier = "operator"
	seedPassword   = "operator-dev-secret"
	hashIdentifier = "ops"
	demoTenant     = "demo"
)

// TestSeedBootstrapsTheConsole runs seed twice on an untouched database and holds that a seeded
// account signs in and a platform administrator reaches the management surface.
func TestSeedBootstrapsTheConsole(t *testing.T) {
	t.Parallel()
	dsn := filepath.Join(t.TempDir(), "demo.db")
	config := writeConfigWith(t, freePorts(t, listenerCount), dsn)

	first := runCommand(t, config, "sqlite", seedCommand)
	if first.err != nil {
		t.Fatalf("seed failed: %v\n%s", first.err, first.combined())
	}
	for _, want := range []string{
		"seeded: account " + seedIdentifier,
		"seeded: account " + hashIdentifier,
		"seeded: tenant system",
		"seeded: tenant " + demoTenant,
		"seeded: setting authn.mfa.sensitive-min-age=1h",
		"seeded: role system/" + string(platformAdminRole),
		"seeded: role system/" + string(tenantAdminRole),
		"seeded: role " + demoTenant + "/" + string(tenantAdminRole),
		"seeded: binding ",
	} {
		if !strings.Contains(first.stdout, want) {
			t.Errorf("the seed wrote nothing holding %q:\n%s", want, first.combined())
		}
	}
	if strings.Contains(first.stdout, "seeded: role "+demoTenant+"/"+string(platformAdminRole)) {
		t.Errorf("the seed wrote a platform-admin role outside the platform domain:\n%s", first.combined())
	}

	second := runCommand(t, config, "sqlite", seedCommand)
	if second.err != nil {
		t.Fatalf("the second seed failed: %v\n%s", second.err, second.combined())
	}
	if strings.Contains(second.stdout, "seeded:") {
		t.Errorf("the second seed wrote something, so it is not idempotent:\n%s", second.combined())
	}
	for _, want := range []string{
		"skipped: account " + seedIdentifier,
		"skipped: tenant system",
		"skipped: tenant " + demoTenant,
		"skipped: setting authn.mfa.sensitive-min-age (exists)",
		"skipped: role system/" + string(platformAdminRole),
		"skipped: role system/" + string(tenantAdminRole),
		"skipped: role " + demoTenant + "/" + string(tenantAdminRole),
		"skipped: grants on system/" + string(platformAdminRole),
		"skipped: grants on system/" + string(tenantAdminRole),
		"skipped: grants on " + demoTenant + "/" + string(tenantAdminRole),
		"skipped: binding ",
	} {
		if !strings.Contains(second.stdout, want) {
			t.Errorf("the second seed reported nothing holding %q:\n%s", want, second.combined())
		}
	}

	_, ports := startServing(t, func(ports []int) *process {
		return start(t, writeConfigWith(t, ports, dsn), "sqlite", serveCommand)
	})

	accountBase := fmt.Sprintf("http://127.0.0.1:%d", ports[backendListener])
	adminBase := fmt.Sprintf("http://127.0.0.1:%d", ports[backendListener])
	adminGet := func(t *testing.T, identifier, path string) response {
		t.Helper()
		token := signIn(t, accountBase, identifier, seedPassword)
		headers := map[string]string{"Authorization": "Bearer " + token}
		if tenant := tenantOfPath(path); tenant != "" {
			headers = withTenant(headers, tenant)
		}
		return send(t, &http.Client{Timeout: probeTimeout()}, http.MethodGet,
			adminBase+path, nil, headers)
	}

	allowed := adminGet(t, seedIdentifier, "/api/v1/system/tenants")
	if allowed.status != http.StatusOK {
		t.Fatalf("the seeded administrator was refused the tenant roster with %d: %s",
			allowed.status, allowed.body)
	}
	var listed struct {
		Tenants []json.RawMessage `json:"tenants"`
	}
	if err := json.Unmarshal(allowed.body, &listed); err != nil {
		t.Fatalf("reading the roster: %v\n%s", err, allowed.body)
	}
	if len(listed.Tenants) != 4 {
		t.Errorf("the roster holds %d tenant(s), want the system tenant, the one the seed registered "+
			"and a personal tenant for each of the two seeded accounts", len(listed.Tenants))
	}

	rolesIn := func(domain string) map[string]bool {
		got := adminGet(t, seedIdentifier, "/api/v1/system/tenants/"+domain+"/roles")
		if got.status != http.StatusOK {
			t.Fatalf("reading the roles of %s answered %d: %s", domain, got.status, got.body)
		}
		var roles struct {
			Roles []struct {
				Role    string `json:"role"`
				Builtin bool   `json:"builtin"`
			} `json:"roles"`
		}
		if err := json.Unmarshal(got.body, &roles); err != nil {
			t.Fatalf("reading the roles of %s: %v\n%s", domain, err, got.body)
		}
		out := make(map[string]bool, len(roles.Roles))
		for _, r := range roles.Roles {
			out[r.Role] = r.Builtin
		}
		return out
	}
	systemRoles := rolesIn(string(authz.PlatformDomain))
	for _, role := range []authz.Role{platformAdminRole, tenantAdminRole} {
		builtin, found := systemRoles[string(role)]
		if !found {
			t.Errorf("the system domain does not hold the %s role", role)
			continue
		}
		if !builtin {
			t.Errorf("the %s role in the system domain is not builtin", role)
		}
	}
	if _, found := systemRoles[string(tenantAdminRole)]; !found {
		t.Errorf("the system tenant has no tenant-admin role: %v", systemRoles)
	}
	tenantRoles := rolesIn(demoTenant)
	if builtin, found := tenantRoles[string(tenantAdminRole)]; !found {
		t.Errorf("the business tenant does not hold the tenant-admin role: %v", tenantRoles)
	} else if !builtin {
		t.Errorf("the tenant-admin role in %s is not builtin", demoTenant)
	}
	if _, found := tenantRoles[string(platformAdminRole)]; found {
		t.Errorf("the business tenant holds the platform-admin role, which exists only in the platform domain: %v",
			tenantRoles)
	}

	profile := adminGet(t, seedIdentifier, "/api/v1/auth/me")
	if profile.status != http.StatusOK {
		t.Fatalf("reading the administrator's profile answered %d: %s", profile.status, profile.body)
	}
	var who struct {
		AccountID string `json:"account_id"`
	}
	if err := json.Unmarshal(profile.body, &who); err != nil || who.AccountID == "" {
		t.Fatalf("reading the administrator's account identifier: %v\n%s", err, profile.body)
	}
	boundRoles := func(domain string) map[string]bool {
		got := adminGet(t, seedIdentifier,
			"/api/v1/system/tenants/"+domain+"/bindings/"+who.AccountID)
		if got.status != http.StatusOK {
			t.Fatalf("reading the administrator's bindings in %s answered %d: %s",
				domain, got.status, got.body)
		}
		var listed struct {
			Bindings []struct {
				Role string `json:"role"`
			} `json:"bindings"`
		}
		if err := json.Unmarshal(got.body, &listed); err != nil {
			t.Fatalf("reading the administrator's bindings in %s: %v\n%s", domain, err, got.body)
		}
		out := make(map[string]bool, len(listed.Bindings))
		for _, b := range listed.Bindings {
			out[b.Role] = true
		}
		return out
	}
	systemBindings := boundRoles(string(authz.PlatformDomain))
	for _, role := range []authz.Role{platformAdminRole, tenantAdminRole} {
		if !systemBindings[string(role)] {
			t.Errorf("the platform administrator holds no %s binding in the system tenant: %v",
				role, systemBindings)
		}
	}
	tenantBindings := boundRoles(demoTenant)
	if !tenantBindings[string(tenantAdminRole)] {
		t.Errorf("the business tenant's administrator holds no tenant-admin binding: %v", tenantBindings)
	}
	if tenantBindings[string(platformAdminRole)] {
		t.Errorf("the business tenant's administrator holds the platform-admin binding: %v", tenantBindings)
	}

	members := adminGet(t, seedIdentifier,
		"/api/v1/tenants/"+string(authz.PlatformDomain)+"/members")
	if members.status != http.StatusOK {
		t.Fatalf("reading the system tenant's members answered %d: %s", members.status, members.body)
	}
	var systemMembers struct {
		Members []struct {
			Account string `json:"account"`
			Active  bool   `json:"active"`
		} `json:"members"`
	}
	if err := json.Unmarshal(members.body, &systemMembers); err != nil {
		t.Fatalf("reading the system tenant's members: %v\n%s", err, members.body)
	}
	foundMember := false
	for _, m := range systemMembers.Members {
		if m.Account == who.AccountID && m.Active {
			foundMember = true
		}
	}
	if !foundMember {
		t.Errorf("the platform administrator is not an active member of the system tenant: %+v",
			systemMembers.Members)
	}

	for _, path := range []string{
		"/api/v1/tenants/" + demoTenant + "/members",
		"/api/v1/tenants/" + demoTenant + "/invitations",
	} {
		got := adminGet(t, seedIdentifier, path)
		if got.status != http.StatusOK {
			t.Errorf("the seeded administrator was refused %s with %d: %s",
				path, got.status, got.body)
		}
	}

	if refused := adminGet(t, hashIdentifier, "/api/v1/system/tenants"); refused.status != http.StatusForbidden {
		t.Errorf("the account holding no role reached the roster with %d, want %d: %s",
			refused.status, http.StatusForbidden, refused.body)
	}
}

const emailSeedSection = `seed:
  accounts:
    - identifier: "operator@demo.example.test"
      username: "operator"
      password: "operator-dev-secret"
      display-name: "Demo Operator"
      platform-admin: true
  tenants:
    - name: demo
      admins:
        - operator@demo.example.test`

const takenAddressSeedSection = `seed:
  accounts:
    - identifier: "operator@demo.example.test"
      username: "someone-else"
      password: "someone-else-secret"
      display-name: "Someone Else"
      platform-admin: true
  tenants: []`

const (
	emailSeedIdentifier = "operator@demo.example.test"
	emailSeedUsername   = "operator"
)

func accountCount(t *testing.T, ctx context.Context, accounts authn.Accounts) int {
	t.Helper()
	listed, _, err := accounts.List(ctx, authn.AccountQuery{})
	if err != nil {
		t.Fatalf("listing the accounts: %v", err)
	}
	return len(listed)
}

// TestSeedAdoptsAnAccountWhoseEmailIdentifierWasRemoved holds that a run finds the account by its
// username after the holder removed the address, and restores its administrator membership.
func TestSeedAdoptsAnAccountWhoseEmailIdentifierWasRemoved(t *testing.T) {
	t.Parallel()
	ports := freePorts(t, listenerCount)
	dsn := filepath.Join(t.TempDir(), "adopt.db")
	config := writeConfigReplacing(t, ports, dsn, demoSeedSection, emailSeedSection)

	first := runCommand(t, config, "sqlite", seedCommand)
	if first.err != nil {
		t.Fatalf("seed failed: %v\n%s", first.err, first.combined())
	}
	if want := "seeded: account " + emailSeedIdentifier; !strings.Contains(first.stdout, want) {
		t.Fatalf("the seed did not create the operator (%q):\n%s", want, first.combined())
	}

	withSeededAccounts(t, dsn, func(ctx context.Context, accounts authn.Accounts,
		admin authn.AccountAdmin, memberships authn.Memberships,
	) {
		id, err := accounts.Lookup(ctx, authn.IdentifierUsername, emailSeedUsername)
		if err != nil {
			t.Fatalf("looking up the seeded username: %v", err)
		}
		identifiers, err := accounts.Identifiers(ctx, id)
		if err != nil {
			t.Fatalf("reading the seeded account's identifiers: %v", err)
		}
		removed := false
		for _, identifier := range identifiers {
			if identifier.Kind != authn.IdentifierEmail || identifier.Value != emailSeedIdentifier {
				continue
			}
			if removeErr := admin.RemoveIdentifier(ctx, id, identifier.ID); removeErr != nil {
				t.Fatalf("removing the email identifier: %v", removeErr)
			}
			removed = true
		}
		if !removed {
			t.Fatalf("the seeded account holds no %s identifier to remove: %+v", emailSeedIdentifier, identifiers)
		}
		if _, lookupErr := accounts.Lookup(ctx, authn.IdentifierEmail, emailSeedIdentifier); !errors.Is(lookupErr, authn.ErrAccountNotFound) {
			t.Fatalf("the removed address still names an account: %v", lookupErr)
		}
		if got := accountCount(t, ctx, accounts); got != 1 {
			t.Fatalf("the database holds %d account(s) before the second seed, want one", got)
		}
		if removeErr := memberships.Remove(ctx, id, demoTenant); removeErr != nil {
			t.Fatalf("removing the administrator's membership: %v", removeErr)
		}
	})

	second := runCommand(t, config, "sqlite", seedCommand)
	if second.err != nil {
		t.Fatalf("the seed over the account whose address the holder removed failed: %v\n%s",
			second.err, second.combined())
	}
	if want := "skipped: account " + emailSeedIdentifier + " (exists as " + emailSeedUsername + ")"; !strings.Contains(second.stdout, want) {
		t.Errorf("the second seed did not report the account it adopted (%q):\n%s", want, second.combined())
	}
	if strings.Contains(second.stdout, "seeded: account ") {
		t.Errorf("the second seed created an account instead of adopting the one under its username:\n%s",
			second.combined())
	}

	withSeededAccounts(t, dsn, func(ctx context.Context, accounts authn.Accounts,
		_ authn.AccountAdmin, memberships authn.Memberships,
	) {
		if got := accountCount(t, ctx, accounts); got != 1 {
			t.Errorf("the second seed left %d account(s), want the one it adopted", got)
		}
		id, err := accounts.Lookup(ctx, authn.IdentifierUsername, emailSeedUsername)
		if err != nil {
			t.Fatalf("looking up the adopted account: %v", err)
		}
		if found, lookupErr := accounts.Lookup(ctx, authn.IdentifierEmail, emailSeedIdentifier); !errors.Is(lookupErr, authn.ErrAccountNotFound) {
			t.Errorf("the second seed wrote the removed address back (lookup answered %q, %v)", found, lookupErr)
		}
		member, err := memberships.IsMember(ctx, id, demoTenant)
		if err != nil {
			t.Fatalf("reading the adopted administrator's membership: %v", err)
		}
		if !member {
			t.Errorf("the second seed did not restore the adopted administrator's membership")
		}
	})
}

// TestSeedCreatesAnAccountWhenNeitherLoginNameExists holds that an entry whose address and username
// are both free is created once and both names resolve.
func TestSeedCreatesAnAccountWhenNeitherLoginNameExists(t *testing.T) {
	t.Parallel()
	ports := freePorts(t, listenerCount)
	dsn := filepath.Join(t.TempDir(), "create.db")
	config := writeConfigReplacing(t, ports, dsn, demoSeedSection, emailSeedSection)

	res := runCommand(t, config, "sqlite", seedCommand)
	if res.err != nil {
		t.Fatalf("seed failed: %v\n%s", res.err, res.combined())
	}
	if want := "seeded: account " + emailSeedIdentifier; !strings.Contains(res.stdout, want) {
		t.Errorf("the seed did not create an account whose login names are both free (%q):\n%s",
			want, res.combined())
	}
	withSeededAccounts(t, dsn, func(ctx context.Context, accounts authn.Accounts,
		_ authn.AccountAdmin, _ authn.Memberships,
	) {
		byEmail, emailErr := accounts.Lookup(ctx, authn.IdentifierEmail, emailSeedIdentifier)
		byUsername, usernameErr := accounts.Lookup(ctx, authn.IdentifierUsername, emailSeedUsername)
		if emailErr != nil || usernameErr != nil {
			t.Fatalf("one of the created account's login names does not resolve: address %v, username %v",
				emailErr, usernameErr)
		}
		if byEmail != byUsername {
			t.Errorf("the address and the username resolve to different accounts: %q and %q", byEmail, byUsername)
		}
		if got := accountCount(t, ctx, accounts); got != 1 {
			t.Errorf("the seed wrote %d account(s), want one", got)
		}
	})
}

// TestSeedSkipsAnEntryWhoseAddressAnotherAccountHolds holds that a configured address that already
// names an account leaves it as it stands.
func TestSeedSkipsAnEntryWhoseAddressAnotherAccountHolds(t *testing.T) {
	t.Parallel()
	ports := freePorts(t, listenerCount)
	dsn := filepath.Join(t.TempDir(), "address-taken.db")
	takenConfig := writeConfigReplacing(t, ports, dsn, demoSeedSection, takenAddressSeedSection)
	if res := runCommand(t, takenConfig, "sqlite", seedCommand); res.err != nil {
		t.Fatalf("seeding the account that holds the address failed: %v\n%s", res.err, res.combined())
	}
	config := writeConfigReplacing(t, ports, dsn, demoSeedSection, emailSeedSection)

	res := runCommand(t, config, "sqlite", seedCommand)
	if res.err != nil {
		t.Fatalf("seed failed: %v\n%s", res.err, res.combined())
	}
	if want := "skipped: account " + emailSeedIdentifier + " (exists)"; !strings.Contains(res.stdout, want) {
		t.Errorf("the seed did not report the account the address names (%q):\n%s", want, res.combined())
	}
	if strings.Contains(res.stdout, "seeded: account ") {
		t.Errorf("the seed created an account for the entry whose address is taken:\n%s", res.combined())
	}
	withSeededAccounts(t, dsn, func(ctx context.Context, accounts authn.Accounts,
		_ authn.AccountAdmin, _ authn.Memberships,
	) {
		if got := accountCount(t, ctx, accounts); got != 1 {
			t.Errorf("the seed left %d accounts, want the one the address names", got)
		}
		if id, err := accounts.Lookup(ctx, authn.IdentifierUsername, emailSeedUsername); !errors.Is(err, authn.ErrAccountNotFound) {
			t.Errorf("the seed created an account under the free username (lookup answered %q, %v)", id, err)
		}
	})
}

// TestAnUnprovedEmailIsGatedAndAnAccountWithoutOneIsNot holds that a seeded
// operator with no email writes freely while an asserted address is refused.
func TestAnUnprovedEmailIsGatedAndAnAccountWithoutOneIsNot(t *testing.T) {
	t.Parallel()
	dsn := filepath.Join(t.TempDir(), "demo.db")
	config := writeConfigWith(t, freePorts(t, listenerCount), dsn)
	if res := runCommand(t, config, "sqlite", seedCommand); res.err != nil {
		t.Fatalf("seed failed: %v\n%s", res.err, res.combined())
	}
	_, ports := startServing(t, func(ports []int) *process {
		return start(t, writeConfigWith(t, ports, dsn), "sqlite", serveCommand)
	})

	base := fmt.Sprintf("http://127.0.0.1:%d", ports[backendListener])
	operator := signIn(t, base, seedIdentifier, seedPassword)
	headers := map[string]string{"Authorization": "Bearer " + operator}

	created := send(t, &http.Client{Timeout: probeTimeout()}, http.MethodPost,
		base+"/api/v1/system/auth/accounts", map[string]any{
			"kind":               "user",
			"username":           "unproved",
			"email":              "unproved@demo.test",
			"email_verified_via": "admin",
			"password":           "unproved-password",
		}, headers)
	if created.status != http.StatusCreated {
		t.Fatalf("the operator with no email was refused a management write: %d\n%s",
			created.status, created.body)
	}

	gated := signIn(t, base, "unproved", "unproved-password")
	refused := send(t, &http.Client{Timeout: probeTimeout()}, http.MethodPost,
		base+"/api/v1/system/auth/accounts", map[string]any{
			"kind": "user", "username": "another",
		}, map[string]string{"Authorization": "Bearer " + gated})
	if refused.status != http.StatusForbidden || !strings.Contains(string(refused.body), "contact-required") {
		t.Fatalf("a management write from an unproved email answered %d, want 403 contact-required:\n%s",
			refused.status, refused.body)
	}
}

// TestSeedBindsTheAdministratorInEveryServedTenant holds that the administrator holds the
// platform-administrator role in every configured tenant's domain.
func TestSeedBindsTheAdministratorInEveryServedTenant(t *testing.T) {
	t.Parallel()
	secondTenant := "acme"
	dsn := filepath.Join(t.TempDir(), "demo.db")
	write := func(ports []int) string {
		return writeConfigReplacing(t, ports, dsn,
			"  tenants:\n    - name: "+demoTenant+"\n      admins:\n        - "+seedIdentifier+"\n",
			"  tenants:\n    - name: "+demoTenant+"\n      admins:\n        - "+seedIdentifier+"\n    - name: "+secondTenant+"\n      admins:\n        - "+seedIdentifier+"\n")
	}
	config := write(freePorts(t, listenerCount))

	seeded := runCommand(t, config, "sqlite", seedCommand)
	if seeded.err != nil {
		t.Fatalf("seed failed: %v\n%s", seeded.err, seeded.combined())
	}

	_, ports := startServing(t, func(ports []int) *process {
		return start(t, write(ports), "sqlite", serveCommand)
	})

	accountBase := fmt.Sprintf("http://127.0.0.1:%d", ports[backendListener])
	adminBase := fmt.Sprintf("http://127.0.0.1:%d", ports[backendListener])
	client := &http.Client{Timeout: probeTimeout()}
	token := signIn(t, accountBase, seedIdentifier, seedPassword)
	for _, tenant := range []string{demoTenant, secondTenant} {
		for _, path := range []string{
			"/members",
			"/invitations",
		} {
			got := send(t, client, http.MethodGet,
				adminBase+"/api/v1/tenants/"+tenant+path, nil,
				withTenant(map[string]string{"Authorization": "Bearer " + token}, tenant))
			if got.status != http.StatusOK {
				t.Errorf("the seeded administrator was refused the %s page of tenant %q with %d: %s",
					path, tenant, got.status, got.body)
			}
		}
	}
}

// TestCreatingATenantWritesTheFirstTenantAdminBinding holds that registration writes the tenant-admin
// role and the creator's membership and binding.
func TestCreatingATenantWritesTheFirstTenantAdminBinding(t *testing.T) {
	t.Parallel()
	dsn := filepath.Join(t.TempDir(), "demo.db")
	config := writeConfigWith(t, freePorts(t, listenerCount), dsn)
	if res := runCommand(t, config, "sqlite", seedCommand); res.err != nil {
		t.Fatalf("seed failed: %v\n%s", res.err, res.combined())
	}
	_, ports := startServing(t, func(ports []int) *process {
		return start(t, writeConfigWith(t, ports, dsn), "sqlite", serveCommand)
	})

	base := fmt.Sprintf("http://127.0.0.1:%d", ports[backendListener])
	client := &http.Client{Timeout: probeTimeout()}
	token := signIn(t, base, seedIdentifier, seedPassword)
	auth := map[string]string{"Authorization": "Bearer " + token}

	profile := send(t, client, http.MethodGet, base+"/api/v1/auth/me", nil, auth)
	if profile.status != http.StatusOK {
		t.Fatalf("reading the administrator's profile answered %d: %s", profile.status, profile.body)
	}
	var who struct {
		AccountID string `json:"account_id"`
	}
	if err := json.Unmarshal(profile.body, &who); err != nil || who.AccountID == "" {
		t.Fatalf("reading the administrator's account identifier: %v\n%s", err, profile.body)
	}

	const created = "newco"
	registered := send(t, client, http.MethodPost, base+"/api/v1/system/tenants",
		map[string]any{"name": created}, auth)
	if registered.status != http.StatusCreated {
		t.Fatalf("registering a tenant answered %d, want 201:\n%s", registered.status, registered.body)
	}

	bound := send(t, client, http.MethodGet,
		base+"/api/v1/system/tenants/"+created+"/bindings/"+who.AccountID, nil, auth)
	if bound.status != http.StatusOK {
		t.Fatalf("reading the creator's bindings in the new tenant answered %d:\n%s",
			bound.status, bound.body)
	}
	var listed struct {
		Bindings []struct {
			Role string `json:"role"`
		} `json:"bindings"`
	}
	if err := json.Unmarshal(bound.body, &listed); err != nil {
		t.Fatalf("reading the creator's bindings: %v\n%s", err, bound.body)
	}
	if len(listed.Bindings) != 1 || listed.Bindings[0].Role != string(authz.TenantAdminRole) {
		t.Errorf("the creator's bindings = %+v, want one tenant-admin", listed.Bindings)
	}

	members := send(t, client, http.MethodGet,
		base+"/api/v1/tenants/"+created+"/members", nil, withTenant(auth, created))
	if members.status != http.StatusOK {
		t.Fatalf("reading the new tenant's members answered %d:\n%s", members.status, members.body)
	}
	var roster struct {
		Members []struct {
			Account string `json:"account"`
			Active  bool   `json:"active"`
		} `json:"members"`
	}
	if err := json.Unmarshal(members.body, &roster); err != nil {
		t.Fatalf("reading the new tenant's members: %v\n%s", err, members.body)
	}
	found := false
	for _, member := range roster.Members {
		if member.Account == who.AccountID && member.Active {
			found = true
		}
	}
	if !found {
		t.Errorf("the creator is not an active member of the tenant it created: %+v", roster.Members)
	}

	deleted := send(t, client, http.MethodDelete,
		base+"/api/v1/system/tenants/"+created+"/roles/"+string(authz.TenantAdminRole), nil, auth)
	if deleted.status != http.StatusConflict {
		t.Errorf("deleting the provisioned tenant-admin role answered %d, want 409:\n%s",
			deleted.status, deleted.body)
	}
}

// TestRemovingASystemMembershipWithdrawsThePlatformBinding holds that removing an active system
// membership withdraws the platform-administrator binding.
func TestRemovingASystemMembershipWithdrawsThePlatformBinding(t *testing.T) {
	t.Parallel()
	dsn := filepath.Join(t.TempDir(), "demo.db")
	config := writeConfigWith(t, freePorts(t, listenerCount), dsn)
	if res := runCommand(t, config, "sqlite", seedCommand); res.err != nil {
		t.Fatalf("seed failed: %v\n%s", res.err, res.combined())
	}
	_, ports := startServing(t, func(ports []int) *process {
		return start(t, writeConfigWith(t, ports, dsn), "sqlite", serveCommand)
	})

	base := fmt.Sprintf("http://127.0.0.1:%d", ports[backendListener])
	client := &http.Client{Timeout: probeTimeout()}
	operator := signIn(t, base, seedIdentifier, seedPassword)
	auth := map[string]string{"Authorization": "Bearer " + operator}

	const (
		targetName     = "cascade-target"
		targetPassword = "cascade-target-secret"
		system         = string(authz.PlatformDomain)
	)
	created := send(t, client, http.MethodPost, base+"/api/v1/system/auth/accounts",
		map[string]any{"kind": "user", "username": targetName, "password": targetPassword}, auth)
	if created.status != http.StatusCreated {
		t.Fatalf("creating the target account answered %d:\n%s", created.status, created.body)
	}
	target := signIn(t, base, targetName, targetPassword)
	profile := send(t, client, http.MethodGet, base+"/api/v1/auth/me", nil,
		map[string]string{"Authorization": "Bearer " + target})
	var who struct {
		AccountID string `json:"account_id"`
	}
	if err := json.Unmarshal(profile.body, &who); err != nil || who.AccountID == "" {
		t.Fatalf("reading the target's account identifier: %v\n%s", err, profile.body)
	}

	member := send(t, client, http.MethodPut,
		base+"/api/v1/tenants/"+system+"/members/"+who.AccountID,
		map[string]any{"active": true}, withTenant(auth, system))
	if member.status != http.StatusNoContent {
		t.Fatalf("adding the target to the system tenant answered %d:\n%s", member.status, member.body)
	}
	bound := send(t, client, http.MethodPut,
		base+"/api/v1/system/tenants/"+system+"/bindings/"+who.AccountID+"/"+string(authz.PlatformAdminRole),
		nil, auth)
	if bound.status != http.StatusNoContent {
		t.Fatalf("binding the target to platform-admin answered %d:\n%s", bound.status, bound.body)
	}

	rolesOf := func() map[string]bool {
		got := send(t, client, http.MethodGet,
			base+"/api/v1/system/tenants/"+system+"/bindings/"+who.AccountID, nil, auth)
		if got.status != http.StatusOK {
			t.Fatalf("reading the target's bindings answered %d:\n%s", got.status, got.body)
		}
		var listed struct {
			Bindings []struct {
				Role string `json:"role"`
			} `json:"bindings"`
		}
		if err := json.Unmarshal(got.body, &listed); err != nil {
			t.Fatalf("reading the target's bindings: %v\n%s", err, got.body)
		}
		out := make(map[string]bool, len(listed.Bindings))
		for _, b := range listed.Bindings {
			out[b.Role] = true
		}
		return out
	}
	if !rolesOf()[string(authz.PlatformAdminRole)] {
		t.Fatal("the platform-admin binding did not land")
	}

	removed := send(t, client, http.MethodDelete,
		base+"/api/v1/tenants/"+system+"/members/"+who.AccountID, nil, withTenant(auth, system))
	if removed.status != http.StatusNoContent {
		t.Fatalf("removing the target's system membership answered %d:\n%s", removed.status, removed.body)
	}
	deadline := time.Now().Add(coretest.Scale(30 * time.Second))
	for rolesOf()[string(authz.PlatformAdminRole)] && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if rolesOf()[string(authz.PlatformAdminRole)] {
		t.Error("the platform-admin binding survived the removal of the system membership")
	}
}

// TestTheNotificationSurfaceIsReachable holds that the notification routes answer a signed-in caller
// rather than the guard's 403.
func TestTheNotificationSurfaceIsReachable(t *testing.T) {
	t.Parallel()
	dsn := filepath.Join(t.TempDir(), "demo.db")
	config := writeConfigWith(t, freePorts(t, listenerCount), dsn)

	if res := runCommand(t, config, "sqlite", seedCommand); res.err != nil {
		t.Fatalf("seed failed: %v\n%s", res.err, res.combined())
	}

	_, ports := startServing(t, func(ports []int) *process {
		return start(t, writeConfigWith(t, ports, dsn), "sqlite", serveCommand)
	})

	base := fmt.Sprintf("http://127.0.0.1:%d", ports[backendListener])
	token := signIn(t, base, seedIdentifier, seedPassword)
	headers := map[string]string{
		"Authorization": "Bearer " + token,
		"X-Tenant":      demoTenant,
	}

	for _, path := range []string{
		"/api/v1/notifications/messages",
		"/api/v1/notifications/messages/unread-count",
		"/api/v1/notifications/preferences",
		"/api/v1/notifications/types",
	} {
		res := send(t, &http.Client{Timeout: probeTimeout()}, http.MethodGet, base+path, nil, headers)
		if res.status != http.StatusOK {
			t.Errorf("GET %s answered %d, want 200:\n%s", path, res.status, res.body)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), coretest.Scale(30*time.Second))
	defer cancel()
	streamReq, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v1/notifications/stream", nil)
	if err != nil {
		t.Fatalf("building the stream request: %v", err)
	}
	for name, value := range headers {
		streamReq.Header.Set(name, value)
	}
	stream, err := http.DefaultClient.Do(streamReq)
	if err != nil {
		t.Fatalf("opening the notification stream: %v", err)
	}
	defer func() { _ = stream.Body.Close() }()
	if stream.StatusCode != http.StatusOK {
		t.Errorf("GET /api/v1/notifications/stream answered %d, want 200", stream.StatusCode)
	}
	if ct := stream.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("the notification stream answered content type %q, want text/event-stream", ct)
	}
	cancel()
}

// TestTheJournalRecordsTheSeamWritesAndNoRequestRow holds that a seam write leaves one row, no request
// row sits beside it, and a polled read leaves none.
func TestTheJournalRecordsTheSeamWritesAndNoRequestRow(t *testing.T) {
	t.Parallel()
	dsn := filepath.Join(t.TempDir(), "demo.db")
	config := writeConfigWith(t, freePorts(t, listenerCount), dsn)

	if res := runCommand(t, config, "sqlite", seedCommand); res.err != nil {
		t.Fatalf("seed failed: %v\n%s", res.err, res.combined())
	}

	_, ports := startServing(t, func(ports []int) *process {
		return start(t, writeConfigWith(t, ports, dsn), "sqlite", serveCommand)
	})

	base := fmt.Sprintf("http://127.0.0.1:%d", ports[backendListener])
	token := signIn(t, base, seedIdentifier, seedPassword)
	client := &http.Client{Timeout: probeTimeout()}
	headers := map[string]string{
		"Authorization": "Bearer " + token,
		"X-Tenant":      demoTenant,
	}

	if res := send(t, client, http.MethodGet, base+"/api/v1/auth/me", nil, headers); res.status != http.StatusOK {
		t.Fatalf("GET /api/v1/auth/me answered %d, want 200:\n%s", res.status, res.body)
	}

	for _, gone := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/objects"},
		{http.MethodDelete, "/api/v1/objects/absent"},
	} {
		if res := send(t, client, gone.method, base+gone.path, nil, headers); res.status != http.StatusNotFound {
			t.Fatalf("%s %s answered %d, want the absent route's 404:\n%s",
				gone.method, gone.path, res.status, res.body)
		}
	}

	created := send(t, client, http.MethodPost, base+"/api/v1/system/auth/accounts", map[string]any{
		"kind":               "user",
		"username":           "sample-admin-write",
		"email":              "sample-admin-write@demo.test",
		"email_verified_via": "admin",
		"display_name":       "Sample Admin Write",
	}, headers)
	if created.status != http.StatusCreated {
		t.Fatalf("creating an account answered %d, want 201:\n%s", created.status, created.body)
	}

	type record struct {
		Source string `json:"source"`
		Action string `json:"action"`
		Object string `json:"object"`
	}
	var page struct {
		Entries []record `json:"entries"`
	}
	readJournal := func() []record {
		res := send(t, client, http.MethodGet, base+"/api/v1/system/audit/records?limit=200", nil, headers)
		if res.status != http.StatusOK {
			t.Fatalf("reading the journal answered %d, want 200:\n%s", res.status, res.body)
		}
		if err := json.Unmarshal(res.body, &page); err != nil {
			t.Fatalf("reading the journal's answer: %v\n%s", err, res.body)
		}
		return page.Entries
	}
	count := func(entries []record, source, action, object string) int {
		n := 0
		for _, r := range entries {
			if r.Source == source && r.Action == action && r.Object == object {
				n++
			}
		}
		return n
	}
	var entries []record
	for deadline := time.Now().Add(coretest.Scale(30 * time.Second)); ; {
		entries = readJournal()
		if count(entries, "seam", "authn.account.create", "") > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	readable, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("rendering the journal's rows: %v", err)
	}

	for _, gone := range [][2]string{
		{http.MethodPost, "/api/v1/objects"},
		{http.MethodDelete, "/api/v1/objects/:id"},
	} {
		if n := count(entries, "request", gone[0], gone[1]); n != 0 {
			t.Errorf("the journal holds %d request row(s) for the absent route %q %q, want none:\n%s",
				n, gone[0], gone[1], readable)
		}
	}
	if n := count(entries, "request", http.MethodGet, "/api/v1/auth/me"); n != 0 {
		t.Errorf("the journal holds %d request row(s) for GET /api/v1/auth/me, want none:\n%s", n, readable)
	}
	if n := count(entries, "request", http.MethodPost, "/api/v1/system/auth/accounts"); n != 0 {
		t.Errorf("the journal holds %d request row(s) for POST /api/v1/system/auth/accounts, "+
			"which the seam already records:\n%s", n, readable)
	}
	if n := count(entries, "seam", "authn.account.create", ""); n != 1 {
		t.Errorf("the journal holds %d seam row(s) for authn.account.create, want exactly 1:\n%s",
			n, readable)
	}
}

// TestTheSeedRefusesAConfigurationItCannotWrite holds that an empty account list, an account with no
// credential and a misspelled credential key all fail the command.
func TestTheSeedRefusesAConfigurationItCannotWrite(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		old  string
		new  string
		want string
	}{
		{
			"an empty account list",
			demoSeedSection, "seed:\n  accounts: []",
			"seed.accounts",
		},
		{
			"an account that names no credential",
			"      password: \"operator-dev-secret\"\n", "",
			"neither a password nor a password-hash",
		},
		{
			"a credential key that is misspelled",
			"      password: ", "      passwrd: ",
			"does not convert",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ports := freePorts(t, listenerCount)
			config := writeConfigReplacing(t, ports, filepath.Join(t.TempDir(), "demo.db"), tc.old, tc.new)

			res := runCommand(t, config, "sqlite", seedCommand)
			if res.err == nil {
				t.Fatalf("the seed accepted a configuration it cannot write:\n%s", res.combined())
			}
			if strings.Contains(res.stdout, "seeded:") {
				t.Errorf("the seed reported work it did not do:\n%s", res.combined())
			}
			if !strings.Contains(res.stderr, tc.want) {
				t.Errorf("the seed's failure does not say what is wrong with the configuration (%q):\n%s",
					tc.want, res.combined())
			}
		})
	}
}

// TestTheConfigTemplateMirrorsTheSampleFile holds the case configuration to the sample file it
// documents itself as mirroring.
func TestTheConfigTemplateMirrorsTheSampleFile(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("testdata", "sqlite-memory.yaml"))
	if err != nil {
		t.Fatalf("reading the sample configuration: %v", err)
	}
	block := yamlBlock(string(raw), "webauthn:")
	if block == "" {
		t.Fatalf("testdata/sqlite-memory.yaml carries no webauthn block to mirror")
	}
	rendered := configBody(t, []int{8080}, ":memory:")
	if !strings.Contains(rendered, block) {
		t.Errorf("the configuration template does not carry the sample file's webauthn block, so a copy's "+
			"relying party is not what a case exercises; want the template to hold:\n%s\nrendered:\n%s",
			block, rendered)
	}
}

type flyProxyCarrier struct {
	TrustedProxies  []string `config:"trusted-proxies"`
	RemoteIPHeaders []string `config:"remote-ip-headers"`
}

func renderDeployTemplate(t *testing.T) string {
	t.Helper()
	dir, ok := os.LookupEnv(deployDirEnv)
	if !ok || dir == "" {
		if strings.HasPrefix(t.Name(), "TestTheDeployTemplate") {
			t.Skip("the deployment template is read outside this module; ./treecheck runs this case with " + deployDirEnv)
		}
		t.Fatalf("a case that reads the deployment template has to be named TestTheDeployTemplate*, so "+
			"./treecheck runs it with %s", deployDirEnv)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "fly.yaml.tmpl"))
	if err != nil {
		t.Fatalf("reading the deployment template: %v", err)
	}
	rendered := string(raw)
	for placeholder, value := range map[string]string{
		"__PUBLIC_ORIGIN__":          "https://demo.example.test",
		"__PUBLIC_DOMAIN__":          "demo.example.test",
		"__OPERATOR_USERNAME__":      "deploy-operator",
		"__OPERATOR_EMAIL__":         "operator@demo.example.test",
		"__OPERATOR_PASSWORD__":      "operator-test-secret",
		"__ENCRYPTION_KEY__":         "test-only-encryption-key-0123456789",
		"__SMTP_HOST__":              "smtp.example.test",
		"__SMTP_PORT__":              "587",
		"__SMTP_TLS__":               "starttls",
		"__SMTP_USERNAME__":          "smtp-user",
		"__SMTP_PASSWORD__":          "smtp-user-secret",
		"__SMTP_FROM__":              "Demo <no-reply@demo.example.test>",
		"__GOOGLE_CLIENT_ID__":       "google-client.example.test",
		"__GOOGLE_CLIENT_SECRET__":   "google-client-secret",
		"__GITHUB_CLIENT_ID__":       "github-client",
		"__GITHUB_CLIENT_SECRET__":   "github-client-secret",
		"__HUMANVERIFY_MODE__":       "turnstile",
		"__HUMANVERIFY_PHONE_MODE__": "pow",
		"__TURNSTILE_SITE_KEY__":     "turnstile-site-key.example.test",
		"__TURNSTILE_SECRET_KEY__":   "turnstile-client-secret",
		"__TURNSTILE_ACTION__":       "login",
	} {
		if !strings.Contains(rendered, placeholder) {
			t.Fatalf("the deployment template carries no %s placeholder to render", placeholder)
		}
		rendered = strings.ReplaceAll(rendered, placeholder, value)
	}
	return rendered
}

// TestTheDeployTemplateTrustsTheFlyProxy holds the rendered deployment template to Fly's proxy as the
// only trusted peer.
func TestTheDeployTemplateTrustsTheFlyProxy(t *testing.T) {
	t.Parallel()
	rendered := renderDeployTemplate(t)

	var carrier flyProxyCarrier
	readEffective(t, "deploy/fly.yaml.tmpl", writeConfigFile(t, rendered), "http", &carrier)

	if want := []string{"172.16.0.0/16"}; !slices.Equal(carrier.TrustedProxies, want) {
		t.Errorf("the template's http.trusted-proxies resolved to %v, want the Fly proxy network %v",
			carrier.TrustedProxies, want)
	}
	if want := []string{"Fly-Client-IP"}; !slices.Equal(carrier.RemoteIPHeaders, want) {
		t.Errorf("the template's http.remote-ip-headers resolved to %v, want %v",
			carrier.RemoteIPHeaders, want)
	}
}

type deploySeedCarrier struct {
	Accounts []struct {
		Identifier    string `config:"identifier"`
		Username      string `config:"username"`
		Password      string `config:"password"`
		PasswordHash  string `config:"password-hash"`
		DisplayName   string `config:"display-name"`
		PlatformAdmin bool   `config:"platform-admin"`
	} `config:"accounts"`
}

// TestTheDeployTemplateSeedsTheOperatorUsername holds the rendered template to a seed entry whose
// username placeholder cannot survive.
func TestTheDeployTemplateSeedsTheOperatorUsername(t *testing.T) {
	t.Parallel()
	rendered := renderDeployTemplate(t)
	locator := writeConfigFile(t, rendered)
	var got deploySeedCarrier
	readEffective(t, "deploy/fly.yaml.tmpl", locator, seedNamespace, &got)
	if len(got.Accounts) != 1 {
		t.Fatalf("the template seeds %d accounts, want the operator alone", len(got.Accounts))
	}
	account := got.Accounts[0]
	if account.Identifier != "operator@demo.example.test" || account.Username != "deploy-operator" {
		t.Errorf("the seeded account reads identifier %q and username %q, want the operator's address "+
			"and the rendered username", account.Identifier, account.Username)
	}
}

func yamlBlock(doc, key string) string {
	lines := strings.Split(doc, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != key {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		block := []string{line}
		for _, next := range lines[i+1:] {
			if strings.TrimSpace(next) == "" {
				break
			}
			if len(next)-len(strings.TrimLeft(next, " ")) <= indent {
				break
			}
			block = append(block, next)
		}
		return strings.Join(block, "\n")
	}
	return ""
}
