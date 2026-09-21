package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	nethttp "net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn/static"

	// The SQLite driver, so that a case can read what the host's migrate
	// command left behind without the host having to say.
	_ "github.com/glebarez/go-sqlite"
)

// The cases run the host as a real child process and reach it over a socket.
// What this example has to prove is assembly-level: that a query written with
// no tenant condition comes back scoped, that a refusal happens before a
// handler runs, and that a command reaches a module for its declarations
// without running it. None of those is observable from inside a package.

// hostBinary is the compiled program every case runs, built once in TestMain
// because building it is the slowest thing here.
var hostBinary string

// waitLimit bounds how long a case waits for the host to start listening or to
// leave. Exceeding it means the host hung, and the case says so with the output
// attached rather than with a timeout of the test binary.
const waitLimit = 15 * time.Second

// The account every case authenticates with.
const (
	subject  = "alice"
	password = "s3cret"
	// qualified is what a handler behind the gateway sees.
	qualified = "local/" + subject
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "tenanted-host")
	if err != nil {
		fmt.Fprintln(os.Stderr, "creating the build directory failed:", err)
		os.Exit(1)
	}
	hostBinary = filepath.Join(dir, "tenanted-host")
	if out, err := exec.Command("go", "build", "-o", hostBinary, ".").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building the host failed: %v\n%s", err, out)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// fixture is one run's configuration and the file it points the engine at.
type fixture struct {
	config string
	dbPath string
	public string
	ops    string
}

// writeFixture lays out a configuration naming both endpoints, the engine and
// both guards.
func writeFixture(t *testing.T) *fixture {
	t.Helper()

	hashed, err := static.Hash(password)
	if err != nil {
		t.Fatalf("deriving the password hash for this run: %v", err)
	}
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "host.db")
	public := freeAddress(t)
	ops := freeAddress(t)

	// The refresh period is zero so that a case sees the roster exactly as
	// it was written, with no window between the two.
	body := fmt.Sprintf(`http:
  endpoints:
    public:
      address: %q
    ops:
      address: %q
db:
  sqlite:
    dsn: %q
authn:
  gate:
    endpoints:
      - public
  static:
    name: local
    subjects:
      %s: %q
tenant:
  resolve:
    endpoints:
      - public
    extractor: header
    header-name: X-Tenant
  catalog:
    refresh-period: 0s
`, public, ops, dbPath, subject, hashed)

	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the configuration: %v", err)
	}
	return &fixture{config: configPath, dbPath: dbPath, public: public, ops: ops}
}

// migrate runs the migration command and returns its output. A case asserts on
// what it left in the database rather than on what it printed.
func (f *fixture) migrate(t *testing.T) string {
	t.Helper()
	cmd := exec.Command(hostBinary, "migrate", "--config", "file://"+f.config)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("migrate failed: %v\n%s", err, out)
	}
	return string(out)
}

// tables lists the tables in the database file.
func (f *fixture) tables(t *testing.T) []string {
	t.Helper()
	handle, err := sql.Open("sqlite", f.dbPath)
	if err != nil {
		t.Fatalf("opening the database file: %v", err)
	}
	defer handle.Close()
	rows, err := handle.Query(
		"SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name")
	if err != nil {
		t.Fatalf("reading the schema: %v", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scanning a table name: %v", err)
		}
		names = append(names, name)
	}
	// Next stops on an error as well as on the end, and a query that failed
	// halfway would otherwise look like a database with fewer tables.
	if err := rows.Err(); err != nil {
		t.Fatalf("reading the schema: %v", err)
	}
	sort.Strings(names)
	return names
}

// start runs the serving command and waits until it answers.
func (f *fixture) start(t *testing.T) *running {
	t.Helper()
	cmd := exec.Command(hostBinary, "serve", "--config", "file://"+f.config)
	var mu sync.Mutex
	var written strings.Builder
	cmd.Stdout = lockedWriter{&mu, &written}
	cmd.Stderr = lockedWriter{&mu, &written}
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the host: %v", err)
	}

	stopped := make(chan struct{})
	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = cmd.Process.Signal(os.Interrupt)
			select {
			case <-stopped:
			case <-time.After(waitLimit):
				_ = cmd.Process.Kill()
				<-stopped
			}
		})
	}
	go func() {
		_ = cmd.Wait()
		close(stopped)
	}()
	t.Cleanup(stop)

	read := func() string {
		mu.Lock()
		defer mu.Unlock()
		return written.String()
	}
	waitUntilListening(t, f.public, read)
	return &running{public: "http://" + f.public, ops: "http://" + f.ops, output: read}
}

// running is a host that is listening, together with what it has written.
type running struct {
	public string
	ops    string
	output func() string
}

// call makes one request and returns the status and the body. A nil tenant
// sends no tenant header at all; a nil credential sends no Authorization
// header.
func call(t *testing.T, base, path, tenantName, credential string) (int, string) {
	t.Helper()
	r, err := nethttp.NewRequest(nethttp.MethodGet, base+path, nil)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	if tenantName != "" {
		r.Header.Set("X-Tenant", tenantName)
	}
	if credential != "" {
		r.SetBasicAuth(credential, password)
	}
	resp, err := nethttp.DefaultClient.Do(r)
	if err != nil {
		t.Fatalf("calling %s: %v", path, err)
	}
	defer resp.Body.Close()
	var body strings.Builder
	_, _ = io.Copy(&body, resp.Body)
	return resp.StatusCode, body.String()
}

// TestMigrateBuildsEveryDeclaredTable is the case for reaching a module for its
// declarations alone. The application module is not constructed by that
// command, its input items are not on its line, and its table is built
// nevertheless — which is exactly what a host cannot check by reading the
// command tree.
func TestMigrateBuildsEveryDeclaredTable(t *testing.T) {
	f := writeFixture(t)
	f.migrate(t)

	got := f.tables(t)
	want := []string{"orders", "tenants"}
	for _, name := range want {
		if !contains(got, name) {
			t.Fatalf("after migrate the database holds %v, and it is missing %q", got, name)
		}
	}
}

// TestGuardedRouteIsScopedToTheRequestTenant is the case for the whole
// release unit: the handler writes a query with no tenant condition, and gets
// back one tenant's rows.
func TestGuardedRouteIsScopedToTheRequestTenant(t *testing.T) {
	f := writeFixture(t)
	f.migrate(t)
	host := f.start(t)

	status, body := call(t, host.public, "/orders", acmeTenant, subject)
	if status != nethttp.StatusOK {
		t.Fatalf("the guarded route answered %d: %s\n%s", status, body, host.output())
	}
	var got orders
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decoding the answer %q: %v", body, err)
	}
	if got.Caller != qualified {
		t.Fatalf("the handler saw caller %q, want %q", got.Caller, qualified)
	}
	if got.Tenant != acmeTenant {
		t.Fatalf("the handler saw tenant %q, want %q", got.Tenant, acmeTenant)
	}
	if len(got.Items) != 1 || got.Items[0] != "a widget for acme" {
		t.Fatalf("the unscoped query returned %v, want just acme's row", got.Items)
	}
}

// TestAuthenticationRefusesBeforeResolution is the case for the layer's place
// on the chain.
//
// The request names a tenant that is not on the roster and carries no
// credential, and that pairing is what makes the case discriminating. The two
// layers answer differently — resolution with 404, authentication with 401 —
// so the status says which of them reached the request first. With the layer
// ordered the other way round, resolution would find no such tenant and the
// request would end as a 404 without authentication ever being consulted, and
// this case would fail.
func TestAuthenticationRefusesBeforeResolution(t *testing.T) {
	f := writeFixture(t)
	f.migrate(t)
	host := f.start(t)

	status, _ := call(t, host.public, "/orders", "no-such-tenant", "")
	if status != nethttp.StatusUnauthorized {
		t.Fatalf("a request naming an unknown tenant and carrying no credential "+
			"answered %d, want 401 from authentication\n%s", status, host.output())
	}
}

// TestTheTwoTenantRefusalsAreOneAnswer is the case for the roster not being
// enumerable: a tenant that was disabled and one that never existed have to
// look the same from outside.
func TestTheTwoTenantRefusalsAreOneAnswer(t *testing.T) {
	f := writeFixture(t)
	f.migrate(t)
	host := f.start(t)

	disabled, disabledBody := call(t, host.public, "/orders", globexTenant, subject)
	unknown, unknownBody := call(t, host.public, "/orders", "no-such-tenant", subject)
	if disabled != unknown {
		t.Fatalf("a disabled tenant answered %d and an unknown one %d", disabled, unknown)
	}
	if disabledBody != unknownBody {
		t.Fatalf("the two answers differ in their bodies: %q and %q",
			disabledBody, unknownBody)
	}
	if disabled != nethttp.StatusNotFound {
		t.Fatalf("the shared answer was %d, want 404", disabled)
	}
	// The refusing is about the roster and not about the rows: a row for
	// the disabled tenant is in the table, and it stayed out of reach.
	if !strings.Contains(host.output(), "not being served") {
		t.Fatalf("the log does not say why the disabled tenant was refused:\n%s",
			host.output())
	}
}

// TestARequestNamingNoTenantIsRefused is the case for the fail-closed
// direction: resolution has no per-route exception, so a request that names no
// tenant never reaches a handler.
func TestARequestNamingNoTenantIsRefused(t *testing.T) {
	f := writeFixture(t)
	f.migrate(t)
	host := f.start(t)

	status, _ := call(t, host.public, "/orders", "", subject)
	if status != nethttp.StatusNotFound {
		t.Fatalf("a request naming no tenant answered %d, want 404\n%s",
			status, host.output())
	}
}

// TestTheOpsEndpointNeedsNeitherGuard is the case for how a route that needs
// no tenant is reached. Resolution is configured per endpoint and has no
// per-route exception, so the health check sits on an endpoint that resolves
// none.
func TestTheOpsEndpointNeedsNeitherGuard(t *testing.T) {
	f := writeFixture(t)
	f.migrate(t)
	host := f.start(t)

	status, body := call(t, host.ops, "/health", "", "")
	if status != nethttp.StatusOK {
		t.Fatalf("the health check answered %d: %s", status, body)
	}
	if !strings.Contains(body, "ok") {
		t.Fatalf("the health check answered %q", body)
	}
}

// lockedWriter serialises the two streams into one buffer, since the host
// writes to both from goroutines of its own.
type lockedWriter struct {
	mu *sync.Mutex
	to *strings.Builder
}

func (w lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.to.Write(p)
}

// freeAddress picks an address nothing is listening on. There is a window
// between releasing it and the host binding it; it is the ordinary way to give
// a child process a port, and a case that loses the race fails on the bind
// rather than passing wrongly.
func freeAddress(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("looking for a free port: %v", err)
	}
	defer l.Close()
	return l.Addr().String()
}

// waitUntilListening blocks until the host accepts a connection.
func waitUntilListening(t *testing.T, address string, output func() string) {
	t.Helper()
	deadline := time.Now().Add(waitLimit)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", address, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the host did not start listening on %s within %s. It wrote:\n%s",
		address, waitLimit, output())
}

// contains reports whether a sorted list holds a name.
func contains(list []string, want string) bool {
	for _, name := range list {
		if name == want {
			return true
		}
	}
	return false
}
