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
// binary. They are generous on purpose and go through coretest.Scale, so a
// loaded machine or a run with several gates at once makes the program slower
// rather than failing the case.
var (
	startLimit   = coretest.Scale(60 * time.Second)
	stopLimit    = coretest.Scale(30 * time.Second)
	commandLimit = coretest.Scale(60 * time.Second)
)

// probeTimeout bounds one request of a running program. It is a wait for an
// answer, so it is generous: a passing case returns as soon as the answer does.
var probeTimeout = coretest.Scale(30 * time.Second)

// raceWarning starts every report the race detector writes. A child that writes
// one has a data race in the program or in a module it runs, and the case fails
// naming it, whatever its exit status says.
const raceWarning = "WARNING: DATA RACE"

func TestMain(m *testing.M) {
	// A differential case re-executes this binary as the program, naming the
	// modules its variant leaves out. That branch builds nothing and runs no
	// test: it drops the modules from baselineServe, says what the run then
	// includes, and becomes the program. The product code carries no switch for
	// it; the variable is read here, in the test binary.
	if list, ok := os.LookupEnv(serveWithoutEnv); ok {
		serveWithout(list)
		os.Exit(run())
	}

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

// demoSeedSection is the seed section of the configuration template, kept apart
// so that a case can take it out whole: a configuration whose seed section is
// missing is one the command has to refuse, and a case cannot say that by
// editing a line of it. Its second account carries a hash rather than a
// plaintext password, so one case covers both credential forms.
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

// demoConfig is the sqlite-memory configuration a case writes: the listener
// address, the token signing key, the object store's root and the database
// locator, with those filled in. It is an override document, the shape
// testdata/sqlite-memory.yaml holds — the public address among it, so the
// WebAuthn relying party the sample binds is the one a case runs on — and the
// platform topology it leaves out arrives from the embedded defaults document,
// exactly as it does for the binary a person runs. No mounting key is repeated:
// the defaults document satisfies a required item. The seed section is one
// account more than the sample's: the case that seeds signs in with two, one
// given a plaintext password and one given a hash, and the sample files carry
// only the first shape so that a copy starts with one operator to replace rather
// than two. A case of its own holds the template to the sample's WebAuthn block,
// so the two do not drift apart.
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

// The listener a case's configuration declares. One listener carries every
// endpoint, so a case that names it indexes the free ports by this; the
// differential case's split topology adds indices of its own.
const (
	backendListener = iota
	listenerCount
)

// The split topology's extra listeners, indexed into its own port slice.
const (
	splitOpsListener      = 1
	splitFrontendListener = 2
	splitListenerCount    = 3
)

// freePorts hands back n ports that are free at the moment they are asked for.
// The listeners are closed before the caller uses the ports, so a port could be
// taken between here and there, and a collision reads as a serve that never
// became ready. The ordinary serving cases go through startServing and the
// differential runs through startVariant; both recognise that exit and start
// the whole attempt again over fresh ports, and freePorts is the port source
// they retry from.
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
	// The database is the stack's own: it lives in the process, which is enough
	// for a case that runs one command.
	return writeConfigWith(t, ports, ":memory:")
}

// writeConfigWith is writeConfig with the database locator chosen by the caller.
//
// A database in a file is what two commands of this tree can share. The
// sqlite-memory stack's own locator names a database held inside the process
// that opened it, and a case that runs the command that seeds and then the one
// that serves is two processes: what the first wrote is gone before the second
// starts, and the second finds the tables and none of the rows.
func writeConfigWith(t *testing.T, ports []int, dsn string) string {
	t.Helper()
	return writeConfigFile(t, configBody(t, ports, dsn))
}

// writeConfigReplacing is writeConfigWith with one edit applied to the
// configuration it writes. A case that holds what a run does with a
// configuration a copy could get wrong needs one, and a needle that is not
// there is the case failing rather than a run over the wrong file.
func writeConfigReplacing(t *testing.T, ports []int, dsn, old, new string) string {
	t.Helper()
	body := configBody(t, ports, dsn)
	replaced := strings.Replace(body, old, new, 1)
	if replaced == body {
		t.Fatalf("the configuration holds no %q to replace", old)
	}
	return writeConfigFile(t, replaced)
}

// configBody is the configuration a case runs over: the template with the
// case's two listener ports, its token signing key, its object store root and
// its database locator filled in. The key is minted for the case in a directory
// of its own, named by an absolute path so the child resolves it whatever its
// working directory, and the object store's root is a directory of the case's
// own, so a run writes no bytes into the source tree.
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

// writeConfigFile writes a configuration body and answers with the locator that
// names it. The locator is a file URL, because the config locator is a URI and
// its scheme is what selects the transport.
func writeConfigFile(t *testing.T, body string) string {
	t.Helper()
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
	return runCommandOn(t, hostBinary, config, args...)
}

// seedHostBinary is the program the seed command runs as: the binary TestMain
// built.
func seedHostBinary() string {
	return hostBinary
}

// runCommandOn is runCommand over a named program.
func runCommandOn(t *testing.T, binary, config string, args ...string) commandResult {
	t.Helper()
	return runCommandArgs(t, binary, "", append([]string{"--config", config}, args...), nil)
}

// runCommandArgs is runCommandOn with the argument list and the extra
// environment entries the caller names, so a case can run a command with no
// configuration source at all. An empty dir leaves the child in the test's own
// working directory; a case that runs on the compiled-in defaults names one of
// its own, so the relative paths the document holds resolve outside the source
// tree.
func runCommandArgs(t *testing.T, binary, dir string, args, extraEnv []string) commandResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), commandLimit)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = dir
	cmd.Env = append(childEnv(), extraEnv...)
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

// start launches a long-running command without waiting for it: the caller
// waits for ready afterwards. Its stop is registered as a cleanup.
func start(t *testing.T, config string, args ...string) *process {
	t.Helper()
	return startArgs(t, "", append([]string{"--config", config}, args...), nil)
}

// startArgs is start with the argument list and the extra environment entries
// the caller names; a case that runs on the compiled-in defaults passes no
// --config at all. An empty dir leaves the child in the test's own working
// directory.
func startArgs(t *testing.T, dir string, args, extraEnv []string) *process {
	t.Helper()
	return startProcess(t, hostBinary, dir, args, extraEnv)
}

// startProcess starts a named program as a long-running child. The end-to-end
// cases' own binary comes through here too, so every serving child's standard
// error is captured the same way and a bind failure is recognisable on all of
// them.
func startProcess(t *testing.T, binary, dir string, args, extraEnv []string) *process {
	t.Helper()
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
		// Wait closes the pipes, so the readers have to reach EOF first: a
		// child that writes on its way out would otherwise lose whatever the
		// reader had not consumed when Wait returned. The readers reach EOF
		// only when every holder of the write end has exited, so a descendant
		// that inherited standard error and never exits would hold this wait
		// open; no child these cases start has such a descendant.
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

// consume reads one of the child's streams line by line and keeps what it read.
// On standard output it also closes the ready channel once the ready record is
// seen; standard error carries the startup diagnostics, which a case reads to
// learn which modules the run did not enable.
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

// output is what the child has written to standard output so far.
func (p *process) output() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return strings.Join(p.lines, "\n")
}

// stderr is what the child has written to standard error so far: the startup
// diagnostics, and any failure the process reported before leaving.
func (p *process) stderr() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return strings.Join(p.diagnostics, "\n")
}

// waitReadyErr waits for the ready record and answers why it never came. The
// output readers are drained before the child is reaped, so once done closes a
// caller that reads standard error afterwards sees the whole failure. The record
// is written once every module has started, so a wait that sees it is past the
// binding of every endpoint — a wait on a port alone would not be.
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
		// The readers drain before Wait runs, so done closes only once the
		// captured diagnostics are complete.
		p.readers.Wait()
	})
}

// TestProcessReadsTheLastLineBeforeExit holds the captured standard error to a
// line written on the child's last write. cmd.Wait closes the pipes as soon as
// the direct child exits, so a reader that has not drained them loses whatever
// follows; only waiting for the readers first keeps it. The writer here is a
// subshell the child backgrounds, so it still holds the write end after the
// child itself is gone: without the wait the parent closes the read end before
// the line is written, and it is lost every time.
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

// bindFailure reports whether this child exited because it could not bind one
// of its listeners, over the same predicate the differential child uses.
func (p *process) bindFailure() bool {
	return bindFailureText(p.stderr())
}

// launchRetries is how many times a launch is retried when a port freePorts
// handed out was taken before the child bound it.
const launchRetries = 3

// bindFailureText reports whether a child's standard error says it exited
// because it could not bind one of its listeners. freePorts closes the socket
// it probed before the child binds the address, so another listener can take
// the port in that gap; that is a property of the port rather than of the run,
// and it is the only non-ready exit a launch retries. The detection matches the
// listener's bind sentinel together with the net error for a taken address, so
// a certificate that cannot load, which shares the sentinel, is not retried.
func bindFailureText(stderr string) bool {
	return strings.Contains(stderr, speedhttp.ErrListen.Error()) &&
		strings.Contains(stderr, "address already in use")
}

// startWithRetry runs one launch attempt and waits for the child's ready
// record. When the child exited because a probed port was taken before it could
// bind, the attempt runs again from source over fresh ports, so a collision is
// never reported as the case's own failure. Any other failure, or an exhausted
// retry budget, comes back to the caller with the last child and its ports.
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

// startServing starts a serving child over a configuration the case renders for
// one attempt's ports, and waits for its ready record. It is the entry the
// ordinary serving cases go through; the differential case launches through
// startVariant, which holds the same retry. An attempt whose child exits on a
// taken port is rendered again over fresh ports, so a collision beside the
// suite's other children is not read as the case's failure. It answers the child
// and the ports it serves on; the caller builds its addresses from the returned
// ports, not from the ones it saw while rendering.
func startServing(t *testing.T, attempt func(ports []int) *process) (*process, []int) {
	t.Helper()
	child, ports, err := startServingFrom(t, freePorts, attempt)
	if err != nil {
		t.Fatalf("%v\n%s", err, child.output())
	}
	return child, ports
}

// startServingFrom is startServing over a chosen port source, so a case can
// hand the first attempt a port another listener holds and watch the retry.
func startServingFrom(t *testing.T, source func(*testing.T, int) []int,
	attempt func(ports []int) *process,
) (*process, []int, error) {
	t.Helper()
	return startWithRetry(t, source, listenerCount, attempt,
		func(p *process) error { return p.waitReadyErr(startLimit) },
		(*process).bindFailure,
		func(p *process) { p.stop(t) },
	)
}

// response is one HTTP answer, kept whole so a case can assert on it.
type response struct {
	status int
	body   []byte
}

// get makes one unauthenticated request of an address and reads the whole
// answer.
func get(t *testing.T, url string) response {
	t.Helper()
	return send(t, &http.Client{Timeout: probeTimeout}, http.MethodGet, url, nil, nil)
}

// rawResponse is one answer with its headers, for the cases that compare two
// anonymous answers byte for byte.
type rawResponse struct {
	status int
	header http.Header
	body   []byte
}

// getRaw makes one unauthenticated request and keeps the answer's headers.
func getRaw(t *testing.T, url string) rawResponse {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("building GET %s: %v", url, err)
	}
	res, err := (&http.Client{Timeout: probeTimeout}).Do(req)
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

// sameExceptDate holds two answers to be identical apart from Date: status,
// every header (names canonical, values in order) and the body. Nothing else is
// filtered: a header that changes from one request to the next is a difference
// the case reports rather than one it hides.
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

// send makes one request and reads the whole answer. A non-nil body is encoded
// as JSON and the content type is set for it; the headers are set as given.
// withTenant copies a request's headers and adds the tenant the request
// announces as X-Tenant. The api endpoint resolves a tenant from that header,
// so a tenant-addressed management route there is decided in that tenant's
// policy domain; the account and admin endpoints ignore it.
func withTenant(headers map[string]string, tenant string) map[string]string {
	out := make(map[string]string, len(headers)+1)
	for name, value := range headers {
		out[name] = value
	}
	out["X-Tenant"] = tenant
	return out
}

// tenantOfPath answers the tenant a path under /api/v1/tenants/ names, so a
// case that drives the console's pages announces the same one as the route.
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

// signIn runs a password login to the end and answers with the access token it
// hands out.
//
// The flow takes two calls: the first carries the credentials and leaves the
// flow ready, and the second takes the result with the complete step. Both need
// the flow cookie, which is why the client keeps a jar.
func signIn(t *testing.T, accountBase, identifier, password string) string {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("building a cookie jar: %v", err)
	}
	client := &http.Client{Timeout: probeTimeout, Jar: jar}
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

// TestServeAnswersTheProbes seeds and then serves on a file-backed sqlite
// database, and reads the operations surface: the liveness and readiness probes
// answer 200 and the merged document holds the operations the modules declared.
// The database is a file because the platform policy module refuses to start
// without the system tenant in the roster, and the seed and the serve are two
// processes: an in-memory database would lose the row between them.
func TestServeAnswersTheProbes(t *testing.T) {
	t.Parallel()
	// config is the configuration the successful attempt ran over, for the
	// platform-claims check below: a retry rewrites it with the fresh ports.
	var config string
	serve, ports := startServing(t, func(ports []int) *process {
		config = writeConfigWith(t, ports, filepath.Join(t.TempDir(), "probes.db"))
		if res := runCommand(t, config, "sqlite", seedCommand); res.err != nil {
			t.Fatalf("seed failed: %v\n%s", res.err, res.combined())
		}
		return start(t, config, "sqlite", serveCommand)
	})

	// The address shield is assembled on the account endpoint. The startup
	// line lists each endpoint's chain as written, so a run whose configuration
	// names the account endpoint reports the layer there. Its place before the
	// gateway is what makes it a shield: it counts a source that has not
	// identified itself yet.
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

	// The web endpoint answers for whatever bundle the binary embeds, and what
	// that is depends on the checkout rather than on the code: the built
	// frontend once the workspace has built it, and nothing servable before
	// that. Both answers are the host module's — its entry document when the
	// bundle carries one, the problem document for a bundle without an entry
	// document when it does not — so the case pins the shape of each rather
	// than a state the caller cannot control.
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

	// The platform baseline is assembled, and not merely the infrastructure the
	// skeleton began with. Two routes only the identity system mounts answer on
	// the account endpoint: the exempt list of login methods, and the token
	// module's published verification keys — the latter only because the signing
	// key the configuration names was read. A module missing from the assembly
	// leaves the path undeclared, which the gateway refuses with 401 rather than
	// 200, so each status distinguishes "assembled" from "not".
	accountBase := fmt.Sprintf("http://127.0.0.1:%d", ports[backendListener])
	for _, path := range []string{"/api/v1/auth/methods", "/api/v1/auth/jwks.json"} {
		res := get(t, accountBase+path)
		if res.status != http.StatusOK {
			t.Errorf("GET %s on the account endpoint answered %d, want 200:\n%s", path, res.status, res.body)
		}
	}

	// The assembly does not show through an anonymous answer on a guarded
	// endpoint. The two paths below are both under prefixes the admin endpoint
	// claims, and the two capabilities behind them are assembled differently:
	// sms.admin is imported for the contract but named by no baseline command,
	// so it registers no route here, while tenant.admin runs. The dispatch selects an
	// endpoint by its claims alone, so both requests reach the admin chain and
	// the gateway refuses each before any route matters — same status, same
	// headers but Date, same body, and a challenge for the admin realm. Before
	// the claims existed the selection was derived from the assembly, and
	// /api/v1/system/sms/templates answered with the frontend bundle while
	// /api/v1/system/tenants answered 401.
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

	// Every operation of the platform's API surface falls under a prefix its
	// own endpoint claims. The check reads the served document — the available
	// set of this binary — and the committed platform contract, and holds each
	// of them against this case's configuration and the six samples.
	checkPlatformClaims(t, config, document.body)

	// The deployment assembles two factor kinds, and the second one is what
	// makes a step-up completable -- which is why it is here rather than left
	// for a copy to add. A step-up asks for a factor kind the session has not
	// already proved, so a TOTP-only deployment leaves a session that signed in
	// with TOTP nothing to raise itself with and refuses the step-up start. The
	// listing the login engine mounts publishes the kinds the assembly takes
	// in, so it is where "a second kind is here" is read: the signing-in copy
	// carries TOTP and WebAuthn, and both are listed.
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

	// The preference matrix is assembled and not merely declared. It reports the
	// endpoints it mounted its routes on once it has taken up its required seams
	// — the database and the roster — so the line is written only by a run that
	// assembled it, and only after it mounted. The outbound delivery pipeline
	// requires the matrix, the queue and its registration face, and the same
	// run reaching ready is what says its requirements were all satisfied.
	if out := serve.output(); !strings.Contains(out, "notification.preferences: the preference routes are mounted") {
		t.Errorf("serve reported no preference mount, so the preference matrix was not assembled:\n%s", out)
	}
}

// TestTheGuardsOfferNoBasicChallenge holds that a 401 on a guarded endpoint
// carries no Basic challenge.
//
// The static password table is the only backend that takes the Basic scheme,
// and a browser opens its native credential prompt for a Basic challenge on any
// 401 — so a session that lapsed on a guarded endpoint would raise a native
// password box in front of a signed-in user instead of letting the frontend end
// the session and return to its own login page. The demo wires no Basic backend,
// so the challenge names Bearer alone and a browser has nothing to prompt from.
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
	client := &http.Client{Timeout: probeTimeout}
	// One guarded endpoint per gateway chain: the account endpoint, the admin
	// endpoint and the api endpoint, whose chain also carries tenant resolution
	// and the authorization guard.
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

// TestGenWritesTheMergedDocument runs gen and reads what it wrote: a document a
// JSON parser accepts, carrying the operations the modules declared and the
// fragments they came from. It is run twice, once writing a file and once
// writing standard output, because the two are the two ways the command
// delivers its result.
func TestGenWritesTheMergedDocument(t *testing.T) {
	t.Parallel()
	config := writeConfig(t, freePorts(t, listenerCount))

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
		// The operations of this starter's own modules carry the
		// application-owner tag orval's platform generation filters on; no
		// platform operation carries it. Which operations those are is read
		// from each fragment's declaring module, judged by gen's own criterion
		// — the platform module path, read from the core package rather than
		// written down — so the case and gen agree on the direction: code
		// under the platform module path is platform-owned, every other
		// module path is application-owned. A second business module is marked
		// by gen the same way the first one is, and the case has to agree with
		// it rather than call the correct mark an error.
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
			// The named anchor: self-service creation is a platform module's
			// route now, so it must be in the document and must not carry the
			// application-owned tag. The generic loop below holds the tag to
			// the declaring module's package; this names the route the move
			// touched, so a fragment dropped from the imports is reported here
			// rather than only as a smaller document.
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

		// Every module the binary's imports reach and that declares a fragment
		// is in the document, whether or not a command names it for a run. The
		// available set is what the imports reach, and gen merges the whole of
		// it; a platform module left unimported drops its fragment from the
		// contract silently, so the declaring set the process registry holds is
		// what the document is held against.
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

		// The platform modules whose fragment belongs to the contract whether or
		// not a run enables them: the SMS management surface is held out of the
		// baseline by the credential criterion, the SAML provider is named by
		// capabilityModules and stands down unless its switch is on, and the
		// preference matrix mounts on the endpoints its own configuration names.
		// All three still declare a fragment, and a case that reads only the
		// registry would miss it — a module left unimported shrinks both sides
		// together.
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

// committedMergedDocumentPath is the merged document artifact the repository
// commits, beside the feature vocabulary. It is relative to this module's
// directory, which is where `go test` runs the package.
const committedMergedDocumentPath = "../../../contracts/api.json"

// committedMergedDocumentTitle is the info.title the committed artifact is
// written with: the title in the regeneration command web/orval.config.ts
// documents. The version is the program's own, which gen uses when the command
// line names none.
const committedMergedDocumentTitle = "speed"

// TestCommittedMergedDocument holds the committed merged document to what this
// binary's gen command writes, byte for byte, the application-owner marking
// included. The frontend's SDK is generated from the committed artifact, and a
// Go route added or changed without regenerating alters what gen writes while
// leaving the artifact — and every case that reads only the artifact — green.
// Running the command rather than rebuilding its output in this process is
// what keeps the case honest: it compares the artifact with the command's
// current result, not with a second copy of the same code.
func TestCommittedMergedDocument(t *testing.T) {
	t.Parallel()
	config := writeConfig(t, freePorts(t, listenerCount))
	out := filepath.Join(t.TempDir(), "api.json")
	res := runCommand(t, config, "sqlite-memory", genCommand,
		"--title", committedMergedDocumentTitle, "-o", out)
	if res.err != nil {
		t.Fatalf("gen failed: %v\n%s", res.err, res.combined())
	}
	generated, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading what gen wrote: %v", err)
	}
	committed, err := os.ReadFile(committedMergedDocumentPath)
	if err != nil {
		t.Fatalf("reading %s: %v (regenerate it with the gen command web/orval.config.ts documents)",
			committedMergedDocumentPath, err)
	}
	if !bytes.Equal(generated, committed) {
		t.Errorf("the committed merged document is out of step with what gen writes: %s\n"+
			"regenerate %s with the gen command web/orval.config.ts documents",
			firstByteDifference(generated, committed), committedMergedDocumentPath)
	}
}

// firstByteDifference names the first byte two documents disagree on, with a
// short excerpt of each, so a stale artifact reports the operation that changed
// rather than dumping both documents into the failure.
func firstByteDifference(generated, committed []byte) string {
	common := min(len(generated), len(committed))
	for index := range common {
		if generated[index] != committed[index] {
			return fmt.Sprintf("byte %d differs: gen wrote %q, the committed document has %q",
				index, excerpt(generated, index), excerpt(committed, index))
		}
	}
	return fmt.Sprintf("gen wrote %d bytes, the committed document has %d; the first %d are equal",
		len(generated), len(committed), common)
}

// excerpt is the bytes around index, as a string, at most about eighty long.
func excerpt(document []byte, index int) string {
	const context = 40
	start := max(index-context, 0)
	end := min(index+context, len(document))
	return string(document[start:end])
}

// TestMarkOperationsWithNoOwnedOperationIsIdentity pins the byte-level
// property gen relies on: with no application-owned operation, the document
// markOperations writes is the document Merge wrote. markOperations mirrors
// pkg/ops/openapi's document shape by hand, so a top-level field Merge starts
// writing would otherwise be dropped from the contract silently.
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

// The credentials the seed writes and this case signs in with.
const (
	seedIdentifier = "operator"
	seedPassword   = "operator-dev-secret"
	// hashIdentifier is the account the configuration gives as a hash instead
	// of as a plaintext password. It signs in with the same password, because
	// the entry the configuration carries is that password's — the same entry
	// the static credential table holds.
	hashIdentifier = "ops"
	// demoTenant is the tenant the configuration under test names, the one the
	// seed registers and binds the administrator in.
	demoTenant = "demo"
)

// TestSeedBootstrapsTheConsole runs the seed command against a database that no
// other command has touched, runs it a second time, and then holds what it is
// for: an account it writes signs in, and one it marks as a platform
// administrator reaches the platform's management surface, which refuses a
// caller that holds no permission.
//
// It runs on the sqlite stack, whose database is a file, because the command
// that seeds and the command that serves are two processes and sqlite-memory's
// database belongs to one of them. The seed carries the migrations with it — it
// drives past them — so it is run on a database that was never migrated.
func TestSeedBootstrapsTheConsole(t *testing.T) {
	t.Parallel()
	// The database is a file the seed and every serve attempt share: the seed
	// leaves it unchanged on a second run, so it runs once over the same path.
	// Each serve attempt renders a fresh configuration, so its listeners, its
	// token signing key and its object-store directory change between attempts
	// while the database stays the one the seed wrote.
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
	// adminGet signs in with the seeded credential and reads one administration
	// route, which the gateway guards and the authorization layer decides on.
	adminGet := func(t *testing.T, identifier, path string) response {
		t.Helper()
		token := signIn(t, accountBase, identifier, seedPassword)
		headers := map[string]string{"Authorization": "Bearer " + token}
		if tenant := tenantOfPath(path); tenant != "" {
			headers = withTenant(headers, tenant)
		}
		return send(t, &http.Client{Timeout: probeTimeout}, http.MethodGet,
			adminBase+path, nil, headers)
	}

	// The account the seed marks is an administrator: the platform roster
	// answers, and it holds the tenant the seed registered.
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
	// The seed writes two accounts, and the default personal-tenant policy
	// builds one for each: the roster is the system tenant, the business tenant
	// the seed registered and the two personal tenants.
	if len(listed.Tenants) != 4 {
		t.Errorf("the roster holds %d tenant(s), want the system tenant, the one the seed registered "+
			"and a personal tenant for each of the two seeded accounts", len(listed.Tenants))
	}

	// Both administration roles are builtin in the domains the seed wrote them:
	// platform-admin only in the system domain, tenant-admin in the system and
	// business tenant domains. The console reads the flag and withholds the
	// role's write controls; the server refuses those writes whether or not a
	// client does. A role without the flag is one the console offers to delete,
	// and deleting the role that holds every declared permission locks the
	// operator out of the console.
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

	// The bindings say the same per account, which the roles alone do not: the
	// platform administrator holds platform-admin and tenant-admin in the system
	// tenant and only tenant-admin in the business tenant.
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

	// The platform binding rests on a system membership: the policy refuses the
	// binding without one, so finding the account among the system tenant's
	// members holds the seed's order — the membership is written first.
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

	// The administrator reaches the console's tenant-addressed pages for the
	// tenant the seed registered. A binding in the platform domain alone does
	// not answer for a tenant — the role is a per-domain row — so the seed
	// writes the role and its grants into the tenant's domain too. Each of
	// these reads answered 403 before that, which is what this case holds
	// against. The SSO provider group is not among them: this configuration
	// leaves authn.admin.sso.enabled off, so its route is not mounted.
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

	// The account the seed marks nothing for signs in — which is what an entry
	// given as a hash rather than as a plaintext has to produce — and reaches
	// nothing that asks for a permission.
	if refused := adminGet(t, hashIdentifier, "/api/v1/system/tenants"); refused.status != http.StatusForbidden {
		t.Errorf("the account holding no role reached the roster with %d, want %d: %s",
			refused.status, http.StatusForbidden, refused.body)
	}
}

// emailSeedSection is the seed section the adoption cases run: the operator is
// named by an email address and created with a username, the shape the
// deployment template renders. The address is the entry's configured login name
// and the username is the second one; the tenant administrator is named by the
// same address, so the configuration is valid.
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

// takenAddressSeedSection creates the account the adoption entry's address names
// under a username of its own, so that address is taken while the adoption
// entry's username is free. The tenant list is written empty because it is a
// container leaf: the compiled defaults name the walkthrough tenant, whose
// administrator this section's account list does not hold.
const takenAddressSeedSection = `seed:
  accounts:
    - identifier: "operator@demo.example.test"
      username: "someone-else"
      password: "someone-else-secret"
      display-name: "Someone Else"
      platform-admin: true
  tenants: []`

// The operator's two login names in emailSeedSection.
const (
	emailSeedIdentifier = "operator@demo.example.test"
	emailSeedUsername   = "operator"
)

// accountCount is how many live accounts the database holds.
func accountCount(t *testing.T, ctx context.Context, accounts authn.Accounts) int {
	t.Helper()
	listed, _, err := accounts.List(ctx, authn.AccountQuery{})
	if err != nil {
		t.Fatalf("listing the accounts: %v", err)
	}
	return len(listed)
}

// TestSeedAdoptsAnAccountWhoseEmailIdentifierWasRemoved holds the seed to an
// account entry that names both an email address and a username, after the
// holder removes the address from the account page. The address is released and
// the username remains, so the second run finds the account under its username:
// it adopts that account, writes the address back to nothing and starts no
// second account. A run that looked only the address up would try to create the
// account again, collide with the username's unique constraint and fail the
// deployment's seed step.
//
// The administrator's membership is removed before the second run too, because
// the line that reconciles it is not the one that adopts the account: an adopted
// account still gets a missing administrator membership written, which is what
// keeps the operator administering the deployment after the address is gone.
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

	// The holder removes the address from the account page: the account keeps its
	// username, and the address no longer names it. The administrator's tenant
	// membership goes with it, to be restored by the second run.
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

// TestSeedCreatesAnAccountWhenNeitherLoginNameExists is the regression beside
// the adoption: an entry whose address and username are both free is created as
// before, and both names resolve to the one account the run wrote.
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

// TestSeedSkipsAnEntryWhoseAddressAnotherAccountHolds holds the skip semantics
// the adoption path sits beside: the configured address already names an
// account, so the run leaves that account as it stands and does not create a
// second one under the free username.
func TestSeedSkipsAnEntryWhoseAddressAnotherAccountHolds(t *testing.T) {
	t.Parallel()
	ports := freePorts(t, listenerCount)
	dsn := filepath.Join(t.TempDir(), "address-taken.db")
	// The first run writes the account holding the operator's address under a
	// different username.
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

// TestSeedBindsTheAdministratorInEveryServedTenant holds the seed's bindings
// against a deployment that serves more than one tenant: the administrator
// must hold the platform-administrator role in each configured tenant's
// domain, not only in the platform domain and not only in the first tenant.
//
// A role and its binding are per-domain rows. A binding in the platform domain
// alone does not answer for a tenant, and the console's tenant-addressed pages
// decide in the tenant the path names, so an implementation that writes one
// binding for the whole run leaves a deployment with a configured tenant whose
// administration pages are refused with 403. The reads below are exactly those
// pages, one request per tenant the configuration names.
// TestAnUnprovedEmailIsGatedAndAnAccountWithoutOneIsNot holds the gate in the
// assembled program: the seeded operator has no email and writes freely, while
// an account created with an administrator-asserted address reads pending and
// is refused every management write with 403 contact-required until its holder
// proves the address.
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

	created := send(t, &http.Client{Timeout: probeTimeout}, http.MethodPost,
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
	refused := send(t, &http.Client{Timeout: probeTimeout}, http.MethodPost,
		base+"/api/v1/system/auth/accounts", map[string]any{
			"kind": "user", "username": "another",
		}, map[string]string{"Authorization": "Bearer " + gated})
	if refused.status != http.StatusForbidden || !strings.Contains(string(refused.body), "contact-required") {
		t.Fatalf("a management write from an unproved email answered %d, want 403 contact-required:\n%s",
			refused.status, refused.body)
	}
}

func TestSeedBindsTheAdministratorInEveryServedTenant(t *testing.T) {
	t.Parallel()
	// Two tenants, so a seed that binds the first one it reads is
	// distinguishable from one that binds them all.
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
	client := &http.Client{Timeout: probeTimeout}
	token := signIn(t, accountBase, seedIdentifier, seedPassword)
	for _, tenant := range []string{demoTenant, secondTenant} {
		// The SSO provider group is not here for the reason the tenant-admin
		// case gives: this configuration leaves its item off.
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

// TestCreatingATenantWritesTheFirstTenantAdminBinding holds the registration's
// whole path in the assembled program: the route goes through the host's
// tenant.Provisioner, the new tenant's domain gets its tenant-admin role, and
// the caller becomes the tenant's first administrator with a membership and the
// tenant-admin binding. A route that wrote only the roster row would leave a
// tenant nobody can enter, which the roster cannot detect on a later removal.
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
	client := &http.Client{Timeout: probeTimeout}
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

	// The binding names the creator the tenant's first administrator, and the
	// membership is what lets it enter the tenant: a binding alone names an
	// account the admission layer refuses.
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

	// The provisioned role carries the builtin mark, as the seeded
	// administrator roles do: the management surface may not delete it.
	deleted := send(t, client, http.MethodDelete,
		base+"/api/v1/system/tenants/"+created+"/roles/"+string(authz.TenantAdminRole), nil, auth)
	if deleted.status != http.StatusConflict {
		t.Errorf("deleting the provisioned tenant-admin role answered %d, want 409:\n%s",
			deleted.status, deleted.body)
	}
}

// TestRemovingASystemMembershipWithdrawsThePlatformBinding holds the cascade in
// the assembled program: a platform-administrator binding is written only for an
// active system member, and removing that membership withdraws the binding in
// the same request. The event bus this stack runs delivers in-process, so the
// read after the removal observes what the cascade left.
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
	client := &http.Client{Timeout: probeTimeout}
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

	// A platform-administrator binding is refused without the membership, so the
	// membership is written first — the order the seed uses as well.
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
	// The change reaches the policy module through the outbox relay, which the
	// baseline runs in the same process: the withdrawal is eventual, bounded by
	// the relay's poll interval, so the case waits for it rather than reading
	// once.
	deadline := time.Now().Add(coretest.Scale(30 * time.Second))
	for rolesOf()[string(authz.PlatformAdminRole)] && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if rolesOf()[string(authz.PlatformAdminRole)] {
		t.Error("the platform-admin binding survived the removal of the system membership")
	}
}

// TestTheNotificationSurfaceIsReachable holds that the notification routes the
// console's notification center calls answer a signed-in caller rather than the
// authorization guard's 403.
//
// The notification capability declares no permission point of its own, so its
// routes carry no gate and the guard, whose default is closed, refuses each one
// 403 — which is what every route here answered before the host registered them
// open. The case signs in as the administrator the seed writes and holds the
// four reads reachable, then opens the stream and reads the event-stream answer
// its handler writes before the first frame.
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

	// The api endpoint is tenant-scoped: the request announces the tenant the
	// seed registered and the administrator is bound in, and carries the access
	// token the login above handed out.
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
		res := send(t, &http.Client{Timeout: probeTimeout}, http.MethodGet, base+path, nil, headers)
		if res.status != http.StatusOK {
			t.Errorf("GET %s answered %d, want 200:\n%s", path, res.status, res.body)
		}
	}

	// The stream is a long-lived SSE connection. Its handler writes the status
	// and the event-stream content type before the first frame, so the client's
	// Do returns once the connection is admitted and the case reads both without
	// waiting for an event; cancelling the request's context ends the read.
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

// TestTheJournalRecordsTheSeamWritesAndNoRequestRow holds the journal's two
// capture paths to what this product carries: the seam a capability records its
// own write at, and the request-level sample, which the platform defaults leave
// empty. The object store's client-facing routes the old sample named are gone —
// the capability has no terminal-user API, and bytes are reached through a URL
// the business backend mints — so the case asks for them and finds no route and
// no row. The account creation is recorded by the authentication module at its
// seam: exactly one row, and no request row beside it. The profile read the
// frontend polls on its own is performed too and must leave no request row.
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
	client := &http.Client{Timeout: probeTimeout}
	headers := map[string]string{
		"Authorization": "Bearer " + token,
		"X-Tenant":      demoTenant,
	}

	// The frontend reads the caller's profile after every load. The sample
	// names no read, so this one leaves the journal untouched.
	if res := send(t, client, http.MethodGet, base+"/api/v1/auth/me", nil, headers); res.status != http.StatusOK {
		t.Fatalf("GET /api/v1/auth/me answered %d, want 200:\n%s", res.status, res.body)
	}

	// The object store answers no client-facing route at all: a declaration and
	// a delete, the two requests the old sample named, reach no endpoint
	// handler.
	for _, gone := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/objects"},
		{http.MethodDelete, "/api/v1/objects/absent"},
	} {
		if res := send(t, client, gone.method, base+gone.path, nil, headers); res.status != http.StatusNotFound {
			t.Fatalf("%s %s answered %d, want the absent route's 404:\n%s",
				gone.method, gone.path, res.status, res.body)
		}
	}

	// The account-management action is recorded at the seam. The empty sample
	// names no request, so one call leaves one seam row and no request row
	// beside it.
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

	// The journal's query surface reads the rows back. The read is repeated
	// until the seam row is there: a capture path writes after the handler
	// returns, so a read can beat the row.
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

// TestTheSeedRefusesAConfigurationItCannotWrite holds the ways a seed whose
// configuration is wrong used to pass for success: an account list that is
// empty, an account that names no credential, and an account whose credential
// key is misspelled. Each used to leave the operator with no way to sign in
// while the command reported seeded — and the second leaves the account in the
// database with no credential at all, which is the state a later run cannot
// repair and cannot report. The third is refused by the strict decode of the
// account entry before the command's own check sees it.
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

// TestTheConfigTemplateMirrorsTheSampleFile holds the configuration a case runs
// to the sample file it documents itself as mirroring. The template is a second
// copy of the platform sections, and a section written into the sample alone —
// the WebAuthn relying party is the one this guards — would be a deliverable a
// copy runs and no case does, so the second factor that turns a step-up into a
// completable flow would be exercised by nothing here. The comparison is against
// the sample file's own text rather than a constant, so a change to the sample
// obliges the template to follow it.
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

// flyProxyCarrier is the part of the deployment template's http section this
// case reads: the module-level proxy settings, which every endpoint inherits.
type flyProxyCarrier struct {
	TrustedProxies  []string `config:"trusted-proxies"`
	RemoteIPHeaders []string `config:"remote-ip-headers"`
}

// renderDeployTemplate renders deploy/fly.yaml.tmpl the way deploy/entrypoint.sh
// does: every placeholder in it is replaced with a value of the shape the
// entrypoint renders. It answers the template's text, so a case reads the
// sections the deployment build carries rather than a copy of them.
func renderDeployTemplate(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "deploy", "fly.yaml.tmpl"))
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

// TestTheFlyTemplateTrustsTheFlyProxy holds the deployment template, not a
// test-local copy of it, to the one proxy a Fly Machine is reached through.
// Fly's proxy is the only peer whose forwarded headers are believed, and the
// client address is read from Fly-Client-IP because Fly appends the app's own
// Anycast address as the rightmost X-Forwarded-For entry. The template is
// rendered the way deploy/entrypoint.sh renders it and loaded through the same
// pkg/config entry point the binary uses, so a deployment that runs the
// template carries the trust settings this case asserts.
func TestTheFlyTemplateTrustsTheFlyProxy(t *testing.T) {
	t.Parallel()
	rendered := renderDeployTemplate(t)
	// The default build carries the mail and social methods, so every section
	// the template names is a declared input item.

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

// deploySeedCarrier is the account entry the deployment template's seed item
// carries: the login name the operator signs in with, and the address written
// beside it.
//
// The account fields mirror the seed module's own entry type. The library
// decodes a list element whole and refuses a member no field answers for, so
// every member the template writes has to be named here even though this case
// reads only the identifier and the username.
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

// TestTheFlyTemplateSeedsTheOperatorUsername holds the template to the seed
// entry the entrypoint renders. The address is the identifier the account is
// looked up by, and the username is the login name it is created with: a
// placeholder left in place would seed a username the login surface cannot
// find. The case renders the template the way deploy/entrypoint.sh does and
// reads the item back, so the placeholder and its input cannot drift apart.
func TestTheFlyTemplateSeedsTheOperatorUsername(t *testing.T) {
	t.Parallel()
	rendered := renderDeployTemplate(t)
	// The default build carries the mail and social methods, so the template's
	// sections are all declared input items.
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

// yamlBlock answers the block a key opens: the key's own line and every
// following line indented more deeply than it, which is the mapping or sequence
// beneath that key. The key is matched on its trimmed text, so the caller passes
// the key with its trailing colon; the empty string means the document holds no
// such key.
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
