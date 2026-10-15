package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// The cases run the program as a real child process and reach it over sockets:
// what this skeleton demonstrates — that serve binds every endpoint and the
// operations surface answers, that gen writes the merged document — is
// assembly-level and not observable from inside the package.
//
// The child is built once in TestMain because building it is the slowest thing
// here, and it is built for the race detector when this binary is, so the
// detector watches the program under test rather than only this harness.

// hostBinary is the compiled program every case runs.
var hostBinary string

// The bounds a case waits within. Exceeding one means the program hung, and the
// case says so with the output attached rather than with a timeout of the test
// binary.
var (
	startLimit   = scaledForRace(60 * time.Second)
	stopLimit    = scaledForRace(30 * time.Second)
	commandLimit = scaledForRace(60 * time.Second)
)

// raceWarning starts every report the race detector writes. A child that writes
// one has a data race in the program or in a module it runs, and the case fails
// naming it, whatever its exit status says.
const raceWarning = "WARNING: DATA RACE"

// scaledForRace lengthens a bound for a binary built with the race detector,
// which runs several times slower.
func scaledForRace(d time.Duration) time.Duration {
	if raceEnabled {
		return 3 * d
	}
	return d
}

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "demo")
	if err != nil {
		fmt.Fprintln(os.Stderr, "creating the build directory failed:", err)
		os.Exit(1)
	}
	hostBinary = filepath.Join(dir, "demo")
	args := []string{"build"}
	if raceEnabled {
		args = append(args, "-race")
	}
	args = append(args, "-o", hostBinary, ".")
	if out, buildErr := exec.Command("go", args...).CombinedOutput(); buildErr != nil {
		fmt.Fprintf(os.Stderr, "building the program failed: %v\n%s", buildErr, out)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// childEnv is the environment a child starts with: this process's own, less
// every variable under the host's prefix and every OpenTelemetry one, so that
// what a case configures is the whole of what the child reads.
func childEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, envPrefix+"_") || strings.HasPrefix(kv, "OTEL_") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

// demoConfig is the sqlite-memory configuration a case writes: the five
// endpoint addresses, the token signing key, and the object store's root, with
// the ports filled in. It is the file testdata/sqlite-memory.yaml holds with
// the ports and the two paths filled in, so a case exercises the same config a
// person runs by hand. The platform sections are the baseline every stack
// carries; the case that reads the platform's own routes holds that they are
// assembled, and the case that reads an undeclared route on a guarded endpoint
// holds that it is not.
const demoConfig = `http:
  endpoints:
    api:
      address: "127.0.0.1:%d"
    account:
      address: "127.0.0.1:%d"
    admin:
      address: "127.0.0.1:%d"
    ops:
      address: "127.0.0.1:%d"
    web:
      address: "127.0.0.1:%d"
  cors:
    endpoints:
      api:
        allow-origins:
          - "https://app.demo.test"
ops:
  endpoint: ops
  title: demo
  version: 0.1.0
webui:
  endpoint: web
log:
  level: info
observability:
  otel:
    service-name: demo
authn:
  gate:
    endpoints: [api, account, admin]
    mount:
      endpoint: account
      public-url: "https://demo.test"
  static:
    subjects:
      operator: "$argon2id$v=19$m=19456,t=2,p=1$xeAjr/xEt4mML0QaEhw8pw$Khjd7856RU6MatFxmOtE0YFZJLsC05yxX5HwJhpBXno"
  token:
    algorithm: ES256
    signing-key-file: "%s"
  mfa:
    totp:
      issuer: demo
tenant:
  resolve:
    endpoints: [api]
    extractor: header
    header-name: X-Tenant
  admission:
    endpoints: [api]
  admin:
    endpoints: [admin]
authz:
  guard:
    endpoints: [api]
    undeclared: closed
  admin:
    endpoints: [admin]
audit:
  journal:
    endpoints: [api, account, admin]
    requests:
      - "GET /auth/me"
      - "POST /auth/admin/accounts"
  admin:
    endpoints: [admin]
ratelimit:
  endpoints: [api]
settings:
  admin:
    endpoints: [admin]
notification:
  inbox:
    endpoints: [api]
  stream:
    endpoints: [api]
objectstore:
  fs:
    root: "%s"
  metadata:
    endpoints: [api]
db:
  sqlite:
    dsn: ":memory:"
    encryption-key: "dev-only-change-me-0123456789abcdef"
kv:
  memory:
    replicas: 1
event:
  memory:
    replicas: 1
`

// freePorts hands back n ports that are free at the moment they are asked for.
// The listeners are closed before the caller uses the ports, so a port could be
// taken between here and there; in practice nothing else binds a port this
// quickly, and a collision reads as a serve that never became ready.
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

// writeConfig writes the sqlite-memory configuration with the given endpoints
// and returns the locator that names it. The locator is a file URL, because the
// config locator is a URI and its scheme is what selects the transport. The
// token signing key is minted for the case in a directory of its own, named by
// an absolute path so the child resolves it whatever its working directory; the
// object store's root is a directory of the case's own too, so a run writes no
// bytes into the source tree.
func writeConfig(t *testing.T, ports []int) string {
	t.Helper()
	key := filepath.Join(t.TempDir(), "token-signing-key.pem")
	if err := ensureSigningKey(key); err != nil {
		t.Fatalf("minting the case's token signing key: %v", err)
	}
	objects := filepath.Join(t.TempDir(), "objects")
	body := fmt.Sprintf(demoConfig, ports[0], ports[1], ports[2], ports[3], ports[4], key, objects)
	path := filepath.Join(t.TempDir(), "demo.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the configuration: %v", err)
	}
	return "file://" + path
}

// commandResult is what a one-shot command left behind.
type commandResult struct {
	stdout string
	stderr string
	err    error
}

// combined is both streams, for a failure message.
func (r commandResult) combined() string {
	return "standard output:\n" + r.stdout + "\nstandard error:\n" + r.stderr
}

// runCommand runs one command of the binary to completion under a time bound.
func runCommand(t *testing.T, config string, args ...string) commandResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), commandLimit)
	defer cancel()
	full := append([]string{"--config", config}, args...)
	cmd := exec.CommandContext(ctx, hostBinary, full...)
	cmd.Env = childEnv()
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		err = fmt.Errorf("the command was still running after %s and was killed: %w", commandLimit, ctx.Err())
	}
	res := commandResult{stdout: stdout.String(), stderr: stderr.String(), err: err}
	if strings.Contains(res.stderr, raceWarning) {
		t.Errorf("%s reported a data race:\n%s", strings.Join(args, " "), res.combined())
	}
	return res
}

// process is a long-running command of the binary: serve.
type process struct {
	cmd *exec.Cmd

	mu    sync.Mutex
	lines []string

	ready    chan struct{}
	readyOne sync.Once
	done     chan struct{}

	waitErr  error
	stopOnce sync.Once
}

// start launches a long-running command without waiting for it: the caller
// waits for ready afterwards. Its stop is registered as a cleanup.
func start(t *testing.T, config string, args ...string) *process {
	t.Helper()
	full := append([]string{"--config", config}, args...)
	cmd := exec.Command(hostBinary, full...)
	cmd.Env = childEnv()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("opening the child's standard output: %v", err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting %s: %v", strings.Join(args, " "), err)
	}
	p := &process{cmd: cmd, ready: make(chan struct{}), done: make(chan struct{})}
	go p.consume(stdout)
	go func() {
		p.waitErr = cmd.Wait()
		close(p.done)
	}()
	t.Cleanup(func() {
		p.stop(t)
		if strings.Contains(stderr.String(), raceWarning) {
			t.Errorf("%s reported a data race:\n%s", strings.Join(args, " "), stderr.String())
		}
	})
	return p
}

// consume reads the child's standard output line by line, keeps what it read,
// and closes the ready channel once the ready record is seen.
func (p *process) consume(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		p.mu.Lock()
		p.lines = append(p.lines, line)
		p.mu.Unlock()
		if strings.Contains(line, "msg="+readyMsg) {
			p.readyOne.Do(func() { close(p.ready) })
		}
	}
}

// output is what the child has written so far.
func (p *process) output() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return strings.Join(p.lines, "\n")
}

// waitReady waits for the ready record and fails the case when it never comes.
// The record is written once every module has started, so a wait that sees it
// is past the binding of every endpoint — a wait on a port alone would not be.
func (p *process) waitReady(t *testing.T) {
	t.Helper()
	select {
	case <-p.ready:
	case <-p.done:
		t.Fatalf("serve exited before it was ready: %v\n%s", p.waitErr, p.output())
	case <-time.After(startLimit):
		t.Fatalf("serve did not write its ready record within %s\n%s", startLimit, p.output())
	}
}

// stop asks the child to stop and waits for it to leave.
func (p *process) stop(t *testing.T) {
	t.Helper()
	p.stopOnce.Do(func() {
		if p.cmd.Process == nil {
			return
		}
		_ = p.cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-p.done:
		case <-time.After(stopLimit):
			_ = p.cmd.Process.Kill()
			<-p.done
			t.Errorf("serve did not leave within %s of SIGTERM\n%s", stopLimit, p.output())
		}
	})
}

// response is one HTTP answer, kept whole so a case can assert on it.
type response struct {
	status int
	body   []byte
}

// get makes one request of an address and reads the whole answer.
func get(t *testing.T, url string) response {
	t.Helper()
	client := &http.Client{Timeout: 10 * time.Second}
	res, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading the body of GET %s: %v", url, err)
	}
	return response{status: res.StatusCode, body: body}
}

// TestServeAnswersTheProbes runs migrate and then serve on the sqlite-memory
// stack, which needs nothing installed, and reads the operations surface: the
// liveness and readiness probes answer 200 and the merged document holds the
// operations the modules declared.
func TestServeAnswersTheProbes(t *testing.T) {
	ports := freePorts(t, len(servedEndpoints))
	config := writeConfig(t, ports)

	if res := runCommand(t, config, "sqlite-memory", migrateCommand); res.err != nil {
		t.Fatalf("migrate failed: %v\n%s", res.err, res.combined())
	}

	serve := start(t, config, "sqlite-memory", serveCommand)
	serve.waitReady(t)

	opsBase := fmt.Sprintf("http://127.0.0.1:%d", ports[3])

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

	// The web endpoint serves the embedded frontend bundle.
	web := get(t, fmt.Sprintf("http://127.0.0.1:%d/", ports[4]))
	if web.status != http.StatusOK {
		t.Errorf("GET / on the web endpoint answered %d, want 200", web.status)
	}

	// The platform baseline is assembled, and not merely the infrastructure the
	// skeleton began with. Two routes only the identity system mounts answer on
	// the account endpoint: the exempt list of login methods, and the token
	// module's published verification keys — the latter only because the signing
	// key the configuration names was read. A module missing from the assembly
	// leaves the path undeclared, which the gateway refuses with 401 rather than
	// 200, so each status distinguishes "assembled" from "not".
	accountBase := fmt.Sprintf("http://127.0.0.1:%d", ports[1])
	for _, path := range []string{"/auth/methods", "/auth/jwks.json"} {
		res := get(t, accountBase+path)
		if res.status != http.StatusOK {
			t.Errorf("GET %s on the account endpoint answered %d, want 200:\n%s", path, res.status, res.body)
		}
	}
}

// TestGenWritesTheMergedDocument runs gen and reads what it wrote: a document a
// JSON parser accepts, carrying the operations the modules declared and the
// fragments they came from. It is run twice, once writing a file and once
// writing standard output, because the two are the two ways the command
// delivers its result.
func TestGenWritesTheMergedDocument(t *testing.T) {
	config := writeConfig(t, freePorts(t, len(servedEndpoints)))

	// merged is the part of the document a case asserts on.
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
		named := false
		for _, fragment := range doc.Fragments {
			if fragment.Module == "ops" {
				named = true
			}
		}
		if !named {
			t.Errorf("the document's x-fragments names no ops fragment: %+v", doc.Fragments)
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
