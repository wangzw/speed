package main

import (
	"bufio"
	"bytes"
	"context"
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
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/audit/journal"
	"github.com/vislake/speed/pkg/authn/gate"
	"github.com/vislake/speed/pkg/authn/static"
	"github.com/vislake/speed/pkg/ratelimit"
	"github.com/vislake/speed/pkg/tenant/resolve"

	// The SQLite driver, so that a case can read what the host's migrate
	// command left behind without the host having to say.
	_ "github.com/glebarez/go-sqlite"
)

// The cases run the host as a real child process and reach it over a socket.
// What this example has to prove is assembly-level: that a query written with
// no tenant condition comes back scoped, that a refusal happens before a
// handler runs, that a command reaches a module for its declarations without
// running it, that a setting read falls back through its layers, that a
// published fact reaches a subscriber that never named the publisher, and that
// the journal's capture paths write a row each. None of those is observable
// from inside a package, and a status code that never crossed a connection
// would not prove any of them.

// hostBinary is the compiled program every case runs, built once in TestMain
// because building it is the slowest thing here.
var hostBinary string

// waitLimit bounds how long a case waits for the host to start listening or to
// leave. Exceeding it means the host hung, and the case says so with the output
// attached rather than with a timeout of the test binary.
const waitLimit = 15 * time.Second

// streamLag is how long a case lets what the host wrote reach the buffer it
// reads before reading it for a record that must not be there. The host's
// output crosses a pipe and a copying goroutine, so a record written an instant
// earlier may not be readable at once.
const streamLag = 200 * time.Millisecond

// settleLimit bounds how long a case waits for a capture path that writes after
// the response. A request-level audit row is written once the handler is done,
// so a case that reads the table the instant its call returns is racing it.
const settleLimit = 5 * time.Second

// The account every case authenticates with, and what a handler behind the
// gateway sees. The qualification is asserted rather than elided: it is what a
// policy written today stays right against when a second backend arrives.
const (
	subject   = "alice"
	password  = "s3cret"
	qualified = "local/" + subject
)

// The tenants a case reaches for by alias. The two active ones differ in
// whether a page size was written for them; the third is disabled and has a row
// of its own, so that its refusal is observably not about the rows being
// absent. The tenant the limiting row names is named by its constant instead,
// because the case that drives a request past the limit is the only one that
// reaches it.
const (
	acme    = acmeTenant
	globex  = globexTenant
	initech = initechTenant
)

// The registry names of the modules whose place in the assembly a case asserts
// on. They are read from the modules rather than written out, so that a module
// renamed is a compile error here rather than a case that quietly stops
// checking anything.
const (
	gateModuleName      = gate.ModuleName
	resolveModuleName   = resolve.ModuleName
	journalModuleName   = journal.ModuleName
	ratelimitModuleName = ratelimit.ModuleName
)

// gateConfigNamespace is where the gateway mounts its input items. A case
// asserts on the item a failed startup names, and the namespace is part of that
// item's path.
const gateConfigNamespace = gate.ConfigNamespace

// problemMediaType is the media type of the error responses the entry point
// writes, for itself and on behalf of the layers on its chain: the refusals of
// authentication, tenant resolution and the limiter, and the answers to a request
// that matches no route and to one whose method the route does not allow.
const problemMediaType = "application/problem+json"

// maxBodyBytes is the request body limit an endpoint carries when its
// configuration names none, which is the one every fixture here runs under.
const maxBodyBytes = 1 << 20

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "orders-host")
	if err != nil {
		fmt.Fprintln(os.Stderr, "creating the build directory failed:", err)
		os.Exit(1)
	}
	hostBinary = filepath.Join(dir, "orders-host")
	if out, err := exec.Command("go", "build", "-o", hostBinary, ".").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building the host failed: %v\n%s", err, out)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// record is one log record taken apart: its message, its level and time, and
// its attributes, all as text. The host's narration is read as records rather
// than as lines because the rendering carries a timestamp and the encoding
// differs per destination, so a whole line is not something a case can state in
// advance.
type record map[string]string

// is reports whether this record carries a message and every one of the
// key-and-value attribute pairs given.
func (r record) is(msg string, attrs ...string) bool {
	if len(attrs)%2 != 0 {
		panic("an attribute filter is a sequence of key and value pairs")
	}
	if r["msg"] != msg {
		return false
	}
	for i := 0; i < len(attrs); i += 2 {
		if r[attrs[i]] != attrs[i+1] {
			return false
		}
	}
	return true
}

// parseTextRecord reads one line of the text format, reporting false for a line
// that is not a record at all: the startup diagnostics share the stream with
// the records.
func parseTextRecord(line string) (record, bool) {
	rec := record{}
	rest := strings.TrimRight(line, " ")
	for rest != "" {
		eq := strings.IndexByte(rest, '=')
		if eq <= 0 {
			return nil, false
		}
		key := rest[:eq]
		if strings.ContainsAny(key, " \"") {
			return nil, false
		}
		rest = rest[eq+1:]
		var value string
		switch {
		case strings.HasPrefix(rest, `"`):
			quoted, err := strconv.QuotedPrefix(rest)
			if err != nil {
				return nil, false
			}
			unquoted, err := strconv.Unquote(quoted)
			if err != nil {
				return nil, false
			}
			value, rest = unquoted, rest[len(quoted):]
		default:
			if space := strings.IndexByte(rest, ' '); space >= 0 {
				value, rest = rest[:space], rest[space:]
			} else {
				value, rest = rest, ""
			}
		}
		rec[key] = value
		rest = strings.TrimLeft(rest, " ")
	}
	for _, key := range [...]string{"time", "level", "msg"} {
		if _, ok := rec[key]; !ok {
			return nil, false
		}
	}
	return rec, true
}

// records reads every record out of a stream, skipping whatever is not one.
func records(text string) []record {
	var out []record
	for _, line := range strings.Split(text, "\n") {
		if line == "" {
			continue
		}
		if rec, ok := parseTextRecord(line); ok {
			out = append(out, rec)
		}
	}
	return out
}

// findRecord reports whether some record in the stream matches, and returns it.
func findRecord(text, msg string, attrs ...string) (record, bool) {
	for _, rec := range records(text) {
		if rec.is(msg, attrs...) {
			return rec, true
		}
	}
	return nil, false
}

// recordOrder is where in the stream a matching record sits, counting records
// rather than lines, or -1 when there is none. Two records' positions are what
// a case compares when the property is the order they were written in.
func recordOrder(text, msg string, attrs ...string) int {
	for i, rec := range records(text) {
		if rec.is(msg, attrs...) {
			return i
		}
	}
	return -1
}

// requireFailedRun asserts that the run failed and that the text it failed with
// appears exactly once. The count is half the assertion: a host that printed the
// error the library already reported would put the same line on the stream
// twice, and neither the exit status nor the presence of the text would show it.
func requireFailedRun(t *testing.T, combined, mustSay string) {
	t.Helper()
	if got := strings.Count(combined, mustSay); got != 1 {
		t.Errorf("the failure text %q appears %d time(s), want exactly 1; the run wrote:\n%s",
			mustSay, got, combined)
	}
}

// fixture is one run's configuration and the file it points the engine at.
type fixture struct {
	config string
	dbPath string
	public string
	ops    string
}

// writeFixture lays out a configuration naming both endpoints, the engine, both
// guards and the three capture paths of the journal.
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

	// The catalog's refresh period is zero so that a case sees the roster
	// exactly as it was written, with no window between the two, and the
	// settings refresh is zero for the same reason: every change is seen
	// through the event the module publishes for itself.
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
audit:
  journal:
    endpoints:
      - public
    requests:
      - "GET /orders"
      - "POST /orders"
    events:
      - %q
settings:
  refresh-interval: 0s
ratelimit:
  endpoints:
    - public
observability:
  otel:
    service-name: orders-host
    batch:
      batch-timeout: 200ms
    endpoints:
      - public
    events:
      - %q
`, public, ops, dbPath, subject, hashed, placedEventType, placedEventType)

	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the configuration: %v", err)
	}
	return &fixture{config: configPath, dbPath: dbPath, public: public, ops: ops}
}

// writeUnguardedFixture lays out the same configuration with no endpoint named
// under the gateway, which is the shape a host must refuse to serve rather than
// serving guarded routes unguarded.
//
// The rate limiting module's section is here for the same reason every other
// one is: the serving command names that module, so a run it cannot read a
// section for stops in its own Prepare. This fixture exists to stop in the
// gateway's.
func writeUnguardedFixture(t *testing.T) *fixture {
	t.Helper()

	hashed, err := static.Hash(password)
	if err != nil {
		t.Fatalf("deriving the password hash for this run: %v", err)
	}
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "host.db")
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
    endpoints: []
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
ratelimit:
  endpoints:
    - public
observability:
  otel:
    service-name: orders-host
`, freeAddress(t), freeAddress(t), dbPath, subject, hashed)

	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the configuration: %v", err)
	}
	return &fixture{config: configPath, dbPath: dbPath}
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

// open opens the database file the run was pointed at.
func (f *fixture) open(t *testing.T) *sql.DB {
	t.Helper()
	handle, err := sql.Open("sqlite", f.dbPath)
	if err != nil {
		t.Fatalf("opening the database file: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	return handle
}

// tables lists the tables in the database file.
func (f *fixture) tables(t *testing.T) []string {
	t.Helper()
	rows, err := f.open(t).Query(
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

// journalRow is one row of the journal's table.
type journalRow struct {
	source  string
	action  string
	object  string
	outcome string
	purpose string
	actor   string
	tenant  string
}

// journal reads every row of the journal's table, oldest first.
func (f *fixture) journal(t *testing.T) []journalRow {
	t.Helper()
	rows, err := f.open(t).Query(
		"SELECT source, action, object, outcome, purpose, actor, COALESCE(tenant_id, '') " +
			"FROM audit_entries ORDER BY id")
	if err != nil {
		t.Fatalf("reading the journal: %v", err)
	}
	defer rows.Close()
	var out []journalRow
	for rows.Next() {
		var r journalRow
		if err := rows.Scan(&r.source, &r.action, &r.object, &r.outcome,
			&r.purpose, &r.actor, &r.tenant); err != nil {
			t.Fatalf("scanning a journal row: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading the journal: %v", err)
	}
	return out
}

// sources counts the journal rows by capture path.
func (f *fixture) sources(t *testing.T) map[string]int {
	t.Helper()
	counts := map[string]int{}
	for _, row := range f.journal(t) {
		counts[row.source]++
	}
	return counts
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

	read := func() string {
		mu.Lock()
		defer mu.Unlock()
		return written.String()
	}

	// exited is what Wait returned, written before stopped is closed and read
	// only after it is.
	var exited error
	stopped := make(chan struct{})

	// interrupt asks the run to stop and returns at once. It signals once
	// only: the host stops listening for the signal as it leaves, so a second
	// one landing then would kill it and read as an unclean exit.
	var signalled sync.Once
	interrupt := func() {
		signalled.Do(func() { _ = cmd.Process.Signal(os.Interrupt) })
	}

	var once sync.Once
	// stop interrupts the run and asserts that it left the way a graceful stop
	// is supposed to: on its own, within the wait limit, with a zero exit
	// status. A run that hangs past the limit is killed and reported, and a run
	// whose release failed exits non-zero, which is the only place a Close that
	// returned an error is visible from outside the process.
	stop := func() {
		once.Do(func() {
			interrupt()
			select {
			case <-stopped:
			case <-time.After(waitLimit):
				_ = cmd.Process.Kill()
				<-stopped
				t.Errorf("the host did not leave within %v of an interrupt and was killed; it wrote:\n%s",
					waitLimit, read())
				return
			}
			if exited != nil {
				t.Errorf("the host did not stop cleanly after an interrupt: %v; it wrote:\n%s", exited, read())
			}
		})
	}
	go func() {
		exited = cmd.Wait()
		close(stopped)
	}()
	t.Cleanup(stop)

	waitUntilReady(t, read)
	return &running{
		public:     "http://" + f.public,
		publicAddr: f.public,
		ops:        "http://" + f.ops,
		output:     read,
		interrupt:  interrupt,
		stop:       stop,
	}
}

// running is a host that is listening, together with what it has written and
// the ways to ask it to stop: interrupt only sends the signal, stop sends it
// and waits for the host to leave.
type running struct {
	public     string
	publicAddr string
	ops        string
	output     func() string
	interrupt  func()
	stop       func()
}

// get builds one GET request, with the tenant header and the credential the
// arguments name. An empty tenant sends no tenant header at all; an empty
// credential sends no Authorization header.
func get(t *testing.T, base, path, tenantName, credential string) *nethttp.Request {
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
	return r
}

// call makes one request and returns the status and the body.
func call(t *testing.T, base, path, tenantName, credential string) (int, string) {
	t.Helper()
	status, _, body := send(t, get(t, base, path, tenantName, credential))
	return status, body
}

// place makes one placement and returns the status and the body.
func place(t *testing.T, base, tenantName, credential, id, item string) (int, string) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"id": id, "item": item})
	if err != nil {
		t.Fatalf("encoding the placement: %v", err)
	}
	r, err := nethttp.NewRequest(nethttp.MethodPost, base+"/orders", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	r.Header.Set("Content-Type", "application/json")
	if tenantName != "" {
		r.Header.Set("X-Tenant", tenantName)
	}
	if credential != "" {
		r.SetBasicAuth(credential, password)
	}
	status, _, text := send(t, r)
	return status, text
}

// send performs one request and returns the status, the response headers and
// the body. The headers are kept because a refusal's Retry-After travels on one
// and nowhere in the body.
func send(t *testing.T, r *nethttp.Request) (int, nethttp.Header, string) {
	t.Helper()
	resp, err := nethttp.DefaultClient.Do(r)
	if err != nil {
		t.Fatalf("calling %s: %v", r.URL.Path, err)
	}
	defer resp.Body.Close()
	var body strings.Builder
	_, _ = io.Copy(&body, resp.Body)
	return resp.StatusCode, resp.Header, body.String()
}

// requireProblem asserts that a response carries the entry point's error media
// type. The body is left alone: its members are the entry point's to define, and
// what a case asserts about a refusal is its status and that the entry point
// wrote it.
func requireProblem(t *testing.T, what string, header nethttp.Header) {
	t.Helper()
	if got := header.Get("Content-Type"); got != problemMediaType {
		t.Errorf("%s was answered with media type %q, want %q", what, got, problemMediaType)
	}
}

// decode unmarshals a body into v, failing the case when it cannot.
func decode(t *testing.T, body string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(body), v); err != nil {
		t.Fatalf("decoding %q: %v", body, err)
	}
}

// freeAddress asks the operating system for a port and gives it back. There is
// a window between the release and the child's bind, which is the ordinary way
// a test hands a port to a subprocess: losing that race fails the case on the
// bind rather than passing it wrongly.
func freeAddress(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("asking for a free port: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("releasing the port: %v", err)
	}
	return addr
}

// waitUntilReady polls until the host reports that it is serving and that its
// seed is in place.
//
// Waiting on a listening port instead would hand every case a run that may
// still be seeding: the sockets are open before the seed runs, so the window a
// case acted in would depend on how long the seed took, and an interrupt aimed
// at "a serving run" could land in the middle of it.
func waitUntilReady(t *testing.T, output func() string) {
	t.Helper()
	deadline := time.Now().Add(waitLimit)
	for time.Now().Before(deadline) {
		if _, ok := findRecord(output(), readyMsg); ok {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the host never reported itself ready; it wrote:\n%s", output())
}

// lockedWriter collects a child's two streams into one buffer under a lock,
// because a reader in another goroutine polls it while the child writes.
type lockedWriter struct {
	mu  *sync.Mutex
	buf *strings.Builder
}

func (w lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

// TestMigrateBuildsEveryDeclaredTable is the case for reaching a module for its
// declarations alone. Neither application module and neither the journal nor
// the settings module is constructed by that command, their input items are not
// on its line, and their tables are built nevertheless — which is exactly what
// a host cannot check by reading the command tree.
func TestMigrateBuildsEveryDeclaredTable(t *testing.T) {
	f := writeFixture(t)
	f.migrate(t)

	got := f.tables(t)
	for _, want := range []string{orderTable, "settings", "audit_entries", "tenants"} {
		if !contains(got, want) {
			t.Errorf("the migration command left no %q table; the file holds %v", want, got)
		}
	}
}

// TestGuardedRouteIsScopedToTheRequestTenant is the case for tenant scoping.
//
// The handler writes a Find with no condition in it, and what comes back is one
// tenant's rows. The page size it applies is larger than the rows any tenant
// owns, so the count it returns is the count that tenant has: a run without the
// filter returns every tenant's rows here and the case fails on the number, not
// merely on the contents.
func TestGuardedRouteIsScopedToTheRequestTenant(t *testing.T) {
	f := writeFixture(t)
	f.migrate(t)
	run := f.start(t)

	const acmeOrders = 3

	status, body := call(t, run.public, "/orders", acme, subject)
	if status != nethttp.StatusOK {
		t.Fatalf("a guarded route with a credential answered %d: %s", status, body)
	}
	var got listing
	decode(t, body, &got)
	if got.Caller != qualified {
		t.Errorf("the handler saw caller %q, want %q: the qualification is what a policy is written against",
			got.Caller, qualified)
	}
	if got.Tenant != acme {
		t.Errorf("the handler saw tenant %q, want %q", got.Tenant, acme)
	}
	if got.PageSize != acmePageSize {
		t.Errorf("the handler read page size %d, want the %d written for %q",
			got.PageSize, acmePageSize, acme)
	}
	if got.PageSize <= acmeOrders {
		t.Fatalf("this case cannot tell the filter from the limit while the page size (%d) is not larger than one tenant's rows (%d)",
			got.PageSize, acmeOrders)
	}
	if len(got.Items) != acmeOrders {
		t.Errorf("the listing returned %d item(s) %v, want the %d %q owns: the query names no tenant of its own",
			len(got.Items), got.Items, acmeOrders, acme)
	}
	for _, item := range got.Items {
		if !strings.Contains(item, acme) {
			t.Errorf("the listing returned %q, which belongs to another tenant", item)
		}
	}
}

// TestTheGlobalLayerAnswersWhereNoTenantRowWasWritten is the other half of the
// fallback: globex has no row of its own, so its listing reads the global one.
//
// The global page size is smaller than globex's rows, so the count it returns
// is the limit and the contents are what tell the filter from its absence: a
// run without the filter returns the rows that sort first, which belong to
// another tenant.
func TestTheGlobalLayerAnswersWhereNoTenantRowWasWritten(t *testing.T) {
	f := writeFixture(t)
	f.migrate(t)
	run := f.start(t)

	status, body := call(t, run.public, "/orders", globex, subject)
	if status != nethttp.StatusOK {
		t.Fatalf("a guarded route with a credential answered %d: %s", status, body)
	}
	var got listing
	decode(t, body, &got)
	if got.PageSize != globalPageSize {
		t.Errorf("the handler read page size %d, want the global %d: %q has no row of its own",
			got.PageSize, globalPageSize, globex)
	}
	if got.PageSize == defaultPageSize {
		t.Errorf("the handler read the declared default %d, so nothing written was consulted at all",
			defaultPageSize)
	}
	if len(got.Items) != globalPageSize {
		t.Errorf("the listing returned %v, want the %d the global page size allows",
			got.Items, globalPageSize)
	}
	for _, item := range got.Items {
		if !strings.Contains(item, globex) {
			t.Errorf("the listing returned %q, which belongs to another tenant", item)
		}
	}
}

// TestTheDeclaredDefaultAnswersWhereNothingWasWritten is the third layer. The
// retention setting is written at neither layer, so the only answer a read can
// give is the declaration — and the declaration is not the zero value, so a
// run that answered with an empty struct instead would fail here.
func TestTheDeclaredDefaultAnswersWhereNothingWasWritten(t *testing.T) {
	f := writeFixture(t)
	f.migrate(t)
	run := f.start(t)

	status, body := call(t, run.public, "/orders", acme, subject)
	if status != nethttp.StatusOK {
		t.Fatalf("a guarded route with a credential answered %d: %s", status, body)
	}
	var got listing
	decode(t, body, &got)
	if got.RetentionDays != defaultRetentionDays {
		t.Errorf("the handler read retention %d, want the declared default %d: nothing writes that key",
			got.RetentionDays, defaultRetentionDays)
	}
}

// TestAuthenticationRefusesBeforeResolution is the case that tells the two
// layers apart by the answer rather than by the wiring. A request with neither
// a credential nor a tenant gets the authentication refusal: resolution runs
// inside the gateway, so a caller who never authenticated never reaches it.
func TestAuthenticationRefusesBeforeResolution(t *testing.T) {
	f := writeFixture(t)
	f.migrate(t)
	run := f.start(t)

	// The same request with no credential at all, naming a tenant that is
	// not on the roster either.
	status, header, _ := send(t, get(t, run.public, "/orders", "nobody", ""))
	if status != nethttp.StatusUnauthorized {
		t.Errorf("a request with no credential answered %d, want %d: authentication runs outside resolution",
			status, nethttp.StatusUnauthorized)
	}
	requireProblem(t, "the refusal by authentication", header)

	status, header, _ = send(t, get(t, run.public, "/orders", "nobody", subject))
	if status != nethttp.StatusNotFound {
		t.Errorf("an authenticated request naming an unknown tenant answered %d, want %d",
			status, nethttp.StatusNotFound)
	}
	requireProblem(t, "the refusal by resolution", header)
}

// TestTheTwoTenantRefusalsAreOneAnswer is the case for the roster not being
// enumerable. A tenant that exists and is disabled, and one that never existed,
// are answered identically — and the disabled one's order is in the table, so
// the refusal is about the roster rather than about the rows being absent.
//
// The status is asserted exactly, not merely as "the same": two answers that
// agree because both fell through to a handler's own error page would satisfy
// equality without any roster judgement having happened.
func TestTheTwoTenantRefusalsAreOneAnswer(t *testing.T) {
	f := writeFixture(t)
	f.migrate(t)
	run := f.start(t)

	disabledStatus, disabledHeader, disabledBody := send(t, get(t, run.public, "/orders", initech, subject))
	unknownStatus, unknownHeader, unknownBody := send(t, get(t, run.public, "/orders", "nobody", subject))

	if disabledStatus != nethttp.StatusNotFound {
		t.Errorf("a request naming a disabled tenant answered %d, want %d",
			disabledStatus, nethttp.StatusNotFound)
	}
	if disabledStatus != unknownStatus {
		t.Errorf("a disabled tenant answered %d and an unknown one %d; telling them apart enumerates the roster",
			disabledStatus, unknownStatus)
	}
	requireProblem(t, "the refusal of a disabled tenant", disabledHeader)
	requireProblem(t, "the refusal of an unknown tenant", unknownHeader)
	// The two bodies are the same bytes, and that sameness is the property
	// rather than an accident of this implementation: a refusal that explained
	// itself — "that tenant exists but is disabled" — would differ between the
	// two and is exactly the enumeration the answers have to be
	// indistinguishable against.
	if disabledBody != unknownBody {
		t.Errorf("the refusals carry different bodies (%q and %q); a refusal that says why is how a roster becomes enumerable",
			disabledBody, unknownBody)
	}

	var rows int
	if err := f.open(t).QueryRow(
		"SELECT COUNT(*) FROM orders WHERE tenant_id = ?", initech).Scan(&rows); err != nil {
		t.Fatalf("counting the disabled tenant's rows: %v", err)
	}
	if rows == 0 {
		t.Errorf("the disabled tenant has no row of its own, so the refusal cannot be told apart from an empty listing")
	}
}

// TestARequestNamingNoTenantIsRefused is the case for resolution having no
// per-route exception: a guarded route reached without a tenant is refused by
// resolution, which is a 404 and not a handler's error page.
func TestARequestNamingNoTenantIsRefused(t *testing.T) {
	f := writeFixture(t)
	f.migrate(t)
	run := f.start(t)

	status, header, body := send(t, get(t, run.public, "/orders", "", subject))
	if status != nethttp.StatusNotFound {
		t.Errorf("a request naming no tenant answered %d, want %d: resolution refuses it before the handler runs; body %q",
			status, nethttp.StatusNotFound, body)
	}
	requireProblem(t, "the refusal of a request naming no tenant", header)
}

// TestAnUnregisteredRouteIsRefusedAsUnauthenticated is the case for where the
// gateway sits relative to the engine's answer for a request that matches no
// route. A caller who never authenticated learns nothing about which paths
// exist: the entry point runs every layer on such a request before it answers
// the 404, so a route nobody registered is refused as unauthenticated rather than
// as not found.
func TestAnUnregisteredRouteIsRefusedAsUnauthenticated(t *testing.T) {
	f := writeFixture(t)
	f.migrate(t)
	run := f.start(t)

	status, header, _ := send(t, get(t, run.public, "/nothing-here", acme, ""))
	if status != nethttp.StatusUnauthorized {
		t.Errorf("an unauthenticated request for an unregistered route answered %d, want %d: every layer runs on a request that matches no route, so a caller learns nothing about which paths exist",
			status, nethttp.StatusUnauthorized)
	}
	requireProblem(t, "the refusal by authentication", header)
}

// TestAnAuthenticatedRequestForAnUnregisteredRouteIsNotFound is the other half:
// a caller that gets past both guards is told the route is not there, by the
// entry point and in its error media type. It names a tenant on the roster so
// that resolution lets it through, which is what makes the 404 the engine's and
// not resolution's.
func TestAnAuthenticatedRequestForAnUnregisteredRouteIsNotFound(t *testing.T) {
	f := writeFixture(t)
	f.migrate(t)
	run := f.start(t)

	status, header, body := send(t, get(t, run.public, "/nothing-here", acme, subject))
	if status != nethttp.StatusNotFound {
		t.Errorf("an authenticated request for an unregistered route answered %d, want %d: %s",
			status, nethttp.StatusNotFound, body)
	}
	requireProblem(t, "the answer to a request that matches no route", header)
}

// TestAMethodTheRouteDoesNotAllowIsAnsweredAfterTheGuards is the case for the
// 405 sharing the 404's place on the chain. The orders path allows GET and POST,
// and a PUT to it is answered by the entry point with the methods the path allows
// — but only to a caller the guards let through: the allowed methods are a fact
// about which paths exist, so a caller without a credential gets the 401 and no
// Allow header.
func TestAMethodTheRouteDoesNotAllowIsAnsweredAfterTheGuards(t *testing.T) {
	f := writeFixture(t)
	f.migrate(t)
	run := f.start(t)

	put := func(tenantName, credential string) *nethttp.Request {
		r, err := nethttp.NewRequest(nethttp.MethodPut, run.public+"/orders", nil)
		if err != nil {
			t.Fatalf("building the request: %v", err)
		}
		if tenantName != "" {
			r.Header.Set("X-Tenant", tenantName)
		}
		if credential != "" {
			r.SetBasicAuth(credential, password)
		}
		return r
	}

	status, header, body := send(t, put(acme, ""))
	if status != nethttp.StatusUnauthorized {
		t.Errorf("an unauthenticated request with a method the path does not allow answered %d, want %d: %s",
			status, nethttp.StatusUnauthorized, body)
	}
	if allow := header.Get("Allow"); allow != "" {
		t.Errorf("the refusal by authentication carries Allow %q, which tells a caller with no credential which methods the path takes", allow)
	}

	status, header, body = send(t, put(acme, subject))
	if status != nethttp.StatusMethodNotAllowed {
		t.Fatalf("an authenticated request with a method the path does not allow answered %d, want %d: %s",
			status, nethttp.StatusMethodNotAllowed, body)
	}
	requireProblem(t, "the answer to a method the path does not allow", header)
	allow := header.Get("Allow")
	for _, method := range []string{nethttp.MethodGet, nethttp.MethodPost} {
		if !strings.Contains(allow, method) {
			t.Errorf("the 405 carries Allow %q, which does not list %s, a method the path takes", allow, method)
		}
	}
}

// TestTheExemptedRouteNeedsNoCredential is the case for the exemption, and for
// how far it reaches. The status route is on the guarded endpoint and is
// reached without a credential, because the module that owns the route
// registered it through the gateway's own surface. It is not exempt from
// resolution: a request naming no tenant is still refused, because resolution
// has no per-route exception at all.
func TestTheExemptedRouteNeedsNoCredential(t *testing.T) {
	f := writeFixture(t)
	f.migrate(t)
	run := f.start(t)

	status, body := call(t, run.public, "/status", acme, "")
	if status != nethttp.StatusOK {
		t.Fatalf("the exempted route answered %d without a credential: %s", status, body)
	}

	// The same endpoint, the same tenant, no credential — and the guarded
	// route refuses. Without this half, a gateway that guarded nothing at
	// all would pass the half above.
	status, _ = call(t, run.public, "/orders", acme, "")
	if status != nethttp.StatusUnauthorized {
		t.Errorf("a guarded route on the same endpoint answered %d without a credential, want %d",
			status, nethttp.StatusUnauthorized)
	}

	status, _ = call(t, run.public, "/status", "", "")
	if status != nethttp.StatusNotFound {
		t.Errorf("the exempted route answered %d with no tenant, want %d: the exemption covers the credential alone",
			status, nethttp.StatusNotFound)
	}
}

// TestAGuardedRouteWithNoGatewayStopsTheStartup is the case for the failure
// this design treats as the worst one: a run that comes up with routes whose
// guard is missing. Nothing about serving would report it — the routes answer,
// the process lives — so the shape that reports it is a startup that refuses.
//
// The refusal comes from the gateway's own Prepare, and that is where it has to
// come from now: a module taken in at run level must start, so the gateway
// cannot stand itself down when it is given no endpoint to guard. It fails the
// startup instead, naming itself and the input item that is missing, and the
// guarded routes never reach the wire.
func TestAGuardedRouteWithNoGatewayStopsTheStartup(t *testing.T) {
	f := writeUnguardedFixture(t)
	f.migrate(t)

	ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
	defer cancel()
	cmd := exec.CommandContext(ctx, hostBinary, "serve", "--config", "file://"+f.config)
	out, err := cmd.CombinedOutput()

	// The deadline is checked before the error: a process that served on
	// until the case killed it reports a non-nil error too, so "it failed
	// and said the right thing" is satisfied by a run that never refused
	// anything.
	if ctx.Err() != nil {
		t.Fatalf("the run was still serving when the case gave up; a configuration naming no endpoint to guard must refuse to start rather than serve:\n%s", out)
	}
	if err == nil {
		t.Fatalf("a configuration naming no endpoint to guard served anyway; it wrote:\n%s", out)
	}
	if !strings.Contains(string(out), `module "`+gateModuleName+`" Prepare:`) {
		t.Errorf("the failure does not name the gateway as the module whose Prepare failed:\n%s", out)
	}
	if !strings.Contains(string(out), gateConfigNamespace+".endpoints") {
		t.Errorf("the failure does not name the input item the gateway is missing:\n%s", out)
	}
}

// TestTheOpsEndpointNeedsNeitherGuard is the case for the second endpoint: the
// health check is reached through neither authentication nor resolution,
// because it sits where neither is configured rather than where it was
// exempted.
func TestTheOpsEndpointNeedsNeitherGuard(t *testing.T) {
	f := writeFixture(t)
	f.migrate(t)
	run := f.start(t)

	status, body := call(t, run.ops, "/health", "", "")
	if status != nethttp.StatusOK {
		t.Fatalf("the health check answered %d: %s", status, body)
	}
	var got health
	decode(t, body, &got)
	if got.Status != "ok" {
		t.Errorf("the health check answered %q", got.Status)
	}
}

// TestTheStartupDiagnosticsListWhatWasFrozen is the case for what the gateway
// reports about itself. The backends that turned up and the exemption table are
// both frozen at startup and both named in the diagnostics, which is the only
// place either is stated.
func TestTheStartupDiagnosticsListWhatWasFrozen(t *testing.T) {
	f := writeFixture(t)
	f.migrate(t)
	run := f.start(t)

	backends, ok := findRecord(run.output(), "authn: the credential-verifying backends taking part")
	if !ok {
		t.Fatalf("the startup diagnostics name no credential-verifying backend; the run wrote:\n%s", run.output())
	}
	if backends["backends"] != "local" {
		t.Errorf("the diagnostics list the backends %q, want the one configured", backends["backends"])
	}

	frozen, ok := findRecord(run.output(), "authn: the exemption table, frozen")
	if !ok {
		t.Fatalf("the startup diagnostics say nothing about the exemption table; the run wrote:\n%s", run.output())
	}
	// An empty table is reported under one key and a populated one under
	// the endpoint each entry belongs to, so the assertion is on the
	// endpoint's own attribute. Each entry is the method and the path.
	if got, want := frozen[publicEndpoint], nethttp.MethodGet+" "+statusPath; !strings.Contains(got, want) {
		t.Errorf("the frozen exemption table lists %q for %q, which does not name the route %q that was registered",
			got, publicEndpoint, want)
	}
}

// TestTheEnvironmentOverridesTheFile is the case for the layers of
// configuration. A value written in the file and one derived from the
// environment reach the same input item, and the environment wins — which is
// observable because the two name different database files and only one of them
// is created.
func TestTheEnvironmentOverridesTheFile(t *testing.T) {
	f := writeFixture(t)
	fromEnv := filepath.Join(t.TempDir(), "from-env.db")

	cmd := exec.Command(hostBinary, "migrate", "--config", "file://"+f.config)
	// The variable's name is spelled out rather than built from envPrefix: a
	// case that assembled it the way the host does would follow the host's
	// own mistake, and a prefix that no longer reaches any input item would
	// leave this case green while the documented name stopped working.
	cmd.Env = append(os.Environ(), "ORDERSHOST_DB__SQLITE__DSN="+fromEnv)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("migrate with an overriding environment failed: %v\n%s", err, out)
	}

	if _, err := os.Stat(fromEnv); err != nil {
		t.Errorf("the file the environment named was not created: %v", err)
	}
	if _, err := os.Stat(f.dbPath); err == nil {
		t.Errorf("the file the configuration named was created anyway: the environment did not win")
	}
}

// TestAPlacementIsPublishedAndCounted is the case for the bus. The write
// publishes a fact under a type name, the inventory module counts it without
// naming the publisher or reading its payload, and the counter is read back
// through the key-value capability.
func TestAPlacementIsPublishedAndCounted(t *testing.T) {
	f := writeFixture(t)
	f.migrate(t)
	run := f.start(t)

	before := counter(t, run)

	status, body := place(t, run.public, acme, subject, "a9", "a widget placed by a case")
	if status != nethttp.StatusCreated {
		t.Fatalf("a placement answered %d: %s", status, body)
	}

	// The subscriber runs wherever the bus calls it, so the count is read
	// until it moves rather than once.
	deadline := time.Now().Add(settleLimit)
	for {
		if after := counter(t, run); after > before {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the counter stayed at %v after a placement was published", before)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// placeBody makes one placement with the body exactly as given, which is how a
// case sends the bodies place's encoder would never produce.
func placeBody(t *testing.T, base, tenantName, credential string, body io.Reader) (int, string) {
	t.Helper()
	r, err := nethttp.NewRequest(nethttp.MethodPost, base+"/orders", body)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Tenant", tenantName)
	r.SetBasicAuth(credential, password)
	status, _, text := send(t, r)
	return status, text
}

// orderRows counts the rows of the orders table, every tenant's.
func (f *fixture) orderRows(t *testing.T) int {
	t.Helper()
	var rows int
	if err := f.open(t).QueryRow("SELECT COUNT(*) FROM orders").Scan(&rows); err != nil {
		t.Fatalf("counting the orders: %v", err)
	}
	return rows
}

// TestAPlacementBodyThatIsNotAnOrderIsABadRequest is the case for the answer to a
// body the handler cannot bind. An empty body, one that is not JSON and one whose
// member has the wrong type are each a bad request, and none of them reaches the
// table: the handler stops at the bind, before it writes anything.
func TestAPlacementBodyThatIsNotAnOrderIsABadRequest(t *testing.T) {
	f := writeFixture(t)
	f.migrate(t)
	run := f.start(t)

	before := f.orderRows(t)
	for _, c := range []struct{ what, body string }{
		{"an empty body", ""},
		{"a body that is not JSON", `{"id": "b1", "item": `},
		{"a body whose member has the wrong type", `{"id": 7, "item": "a widget"}`},
	} {
		status, body := placeBody(t, run.public, acme, subject, strings.NewReader(c.body))
		if status != nethttp.StatusBadRequest {
			t.Errorf("a placement with %s answered %d, want %d: %s",
				c.what, status, nethttp.StatusBadRequest, body)
		}
	}
	if after := f.orderRows(t); after != before {
		t.Errorf("the orders table went from %d to %d row(s) across placements that could not be bound", before, after)
	}
}

// TestAPlacementBodyPastTheEndpointLimitIsTooLarge is the case for the one bind
// failure that is not the client's malformed JSON: the endpoint's limit cuts the
// body off, the bind reports it as an oversized body, and the handler answers it
// as one rather than as a bad request. The body is a well-formed document, so
// size is the only thing wrong with it.
func TestAPlacementBodyPastTheEndpointLimitIsTooLarge(t *testing.T) {
	f := writeFixture(t)
	f.migrate(t)
	run := f.start(t)

	before := f.orderRows(t)
	oversized := `{"id": "b2", "item": "` + strings.Repeat("x", maxBodyBytes) + `"}`
	status, body := placeBody(t, run.public, acme, subject, strings.NewReader(oversized))
	if status != nethttp.StatusRequestEntityTooLarge {
		t.Errorf("a placement of %d bytes answered %d, want %d: the endpoint's limit is %d bytes; %s",
			len(oversized), status, nethttp.StatusRequestEntityTooLarge, maxBodyBytes, body)
	}
	if after := f.orderRows(t); after != before {
		t.Errorf("the orders table went from %d to %d row(s) across a placement the limit cut off", before, after)
	}
}

// counter reads the placement counter over the guarded route.
func counter(t *testing.T, run *running) float64 {
	t.Helper()
	status, body := call(t, run.public, "/inventory", acme, subject)
	if status != nethttp.StatusOK {
		t.Fatalf("the counter route answered %d: %s", status, body)
	}
	var got inventory
	decode(t, body, &got)
	return got.Placed
}

// TestRecordsCarryTheWritingModule is the case for the narration's attribution.
// A module writes its records without putting its own name into the call, and
// the record still says which module wrote it — the logger it took through
// Named carries that.
func TestRecordsCarryTheWritingModule(t *testing.T) {
	f := writeFixture(t)
	f.migrate(t)
	run := f.start(t)

	if _, body := place(t, run.public, acme, subject, "a9", "a widget placed by a case"); body == "" {
		t.Fatal("the placement answered with no body")
	}

	deadline := time.Now().Add(settleLimit)
	for {
		if _, ok := findRecord(run.output(), "counted", "module", inventoryModuleName); ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no record from %q; the run wrote:\n%s", inventoryModuleName, run.output())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestTheJournalCapturesEveryPath is the case for the audit module's coverage.
// One write is captured three ways — the statement, the request that carried
// it, and the placement event it published — and the seed's cross-tenant write
// is captured a fourth, through the seam, which is the only path whose caller
// knows why.
//
// The four are asserted together on purpose: each path is configured
// separately, and a run that captured three of them would otherwise pass every
// case that names one. The statement and seam paths already have rows from the
// seed before any request is made, so those two are counted before and after —
// a case that only asked whether they were non-empty would pass on the seed's
// rows alone.
func TestTheJournalCapturesEveryPath(t *testing.T) {
	f := writeFixture(t)
	f.migrate(t)
	run := f.start(t)

	before := f.sources(t)
	if before["seam"] == 0 {
		t.Fatalf("the seed wrote no seam row, so this case cannot tell the seam apart from a path that never ran")
	}

	if _, body := place(t, run.public, acme, subject, "a9", "a widget placed by a case"); body == "" {
		t.Fatal("the placement answered with no body")
	}
	if _, body := call(t, run.public, "/orders", acme, subject); body == "" {
		t.Fatal("the listing answered with no body")
	}

	deadline := time.Now().Add(settleLimit)
	for {
		after := f.sources(t)
		if after["statement"] > before["statement"] &&
			after["request"] > before["request"] &&
			after["event"] > before["event"] {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the journal went from %v to %v after a placement and a listing; want a new row from each of the statement, request and event paths",
				before, after)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// The request path is configured for the read and the write, and the
	// write is the one that carries the placement: asserting on the method
	// is what tells that path apart from a capture that only ever saw reads.
	// The row names the route by its method and the path it was registered
	// under, and the configuration entry that selected it is spelled the same
	// way, so a row for the placement is also what shows the entry matched.
	var carried bool
	for _, row := range f.journal(t) {
		if row.source == "request" && row.action == nethttp.MethodPost && row.object == ordersPath {
			carried = true
		}
	}
	if !carried {
		t.Errorf("no request row records the placement, so the request path captured reads alone")
	}

	// The seam's row is the seed's, and the purpose it states is the one
	// thing no capture path could have supplied: the statement path sees the
	// write but not why it crossed tenants.
	var purposes []string
	for _, row := range f.journal(t) {
		if row.source == "seam" {
			purposes = append(purposes, row.purpose)
		}
	}
	if len(purposes) != 1 {
		t.Fatalf("the journal holds %d seam row(s), want the seed's one", len(purposes))
	}
	if !strings.Contains(purposes[0], "outside any request") {
		t.Errorf("the seam row's purpose is %q, which does not state why the write crossed tenants", purposes[0])
	}
}

// TestTheStartupDiagnosticsNameTheChain is the case for what a host can be told
// about the assembly it built. The endpoint's chain is reported in order, and
// the order is the claim: authentication sits outside resolution, so a request
// without a credential never drives a roster lookup.
func TestTheStartupDiagnosticsNameTheChain(t *testing.T) {
	f := writeFixture(t)
	f.migrate(t)
	run := f.start(t)

	chain := chainFor(t, run, publicEndpoint)
	auth := strings.Index(chain, gateModuleName)
	resolution := strings.Index(chain, resolveModuleName)
	journal := strings.Index(chain, journalModuleName)
	switch {
	case auth < 0 || resolution < 0 || journal < 0:
		t.Errorf("the chain %q is missing one of the guards the configuration asked for", chain)
	case auth >= resolution || resolution >= journal:
		t.Errorf("the chain %q puts the layers in an order that lets a request reach resolution before authentication",
			chain)
	}
}

// chainFor reads an endpoint's assembled chain out of the run's startup
// diagnostics: the layers on it, in order, outermost first.
func chainFor(t *testing.T, run *running, endpoint string) string {
	t.Helper()
	rec, ok := findRecord(run.output(), "an HTTP endpoint's chain is assembled", "endpoint", endpoint)
	if !ok {
		t.Fatalf("the startup diagnostics name no chain for %q; the run wrote:\n%s", endpoint, run.output())
	}
	return rec["chain"]
}

// TestTheLimitingLayerSitsInsideTheGuards is the case for where the rate
// limiting layer lands on an assembled endpoint.
//
// The layer registers itself on the endpoints the configuration names, so its
// reach is configuration and not something every route gets. On the endpoint it
// guards it sits inside authentication, tenant resolution and the journal:
// those three are the anchors its own declaration carries, and an anchor whose
// capability no layer on the endpoint stands for is dropped rather than
// honoured. The chain listing is where the anchors are therefore readable as
// positions that landed — a layer outside authentication reads no identity
// string, and one outside resolution reads the limits of the shared layer
// instead of the caller's tenant.
func TestTheLimitingLayerSitsInsideTheGuards(t *testing.T) {
	f := writeFixture(t)
	f.migrate(t)
	run := f.start(t)

	chain := chainFor(t, run, publicEndpoint)
	limiter := strings.Index(chain, ratelimitModuleName)
	if limiter < 0 {
		t.Fatalf("the chain %q carries no %q layer, which the configuration names %q for",
			chain, ratelimitModuleName, publicEndpoint)
	}
	for _, inside := range []string{gateModuleName, resolveModuleName, journalModuleName} {
		at := strings.Index(chain, inside)
		if at < 0 {
			t.Fatalf("the chain %q is missing the %q layer the limiter has to sit inside", chain, inside)
		}
		if limiter < at {
			t.Errorf("the chain %q puts %q outside %q, which it has to sit inside",
				chain, ratelimitModuleName, inside)
		}
	}

	// The endpoint the configuration does not name is served without the layer,
	// and its own listing is the witness: a limiter that turned up there would
	// have judged requests carrying neither an identity nor a tenant.
	if ops := chainFor(t, run, opsEndpoint); strings.Contains(ops, ratelimitModuleName) {
		t.Errorf("the chain %q carries the limiting layer on an endpoint the configuration does not name", ops)
	}
}

// TestTheLimitingLayerRefusesPastTheLimit is the case for the layer's decision
// being reached at all. The limit is the value written for one tenant, and the
// request past it is refused through the assembled chain: the answer is 429
// with a Retry-After header, and the journal, which sits outside the layer,
// captures the refusal.
//
// The tenant this case drives past the limit belongs to this case and to no
// other case, and the seed is why. The seed writes this tenant's low row on
// every serve, and the layer reads its limit per tenant — it takes the settings
// layer from the request's tenant, so a second identity inside the tenant reads
// the same low row, and the row reaches every route on the endpoint. Any case
// sending more than the seeded number of requests to this endpoint as that
// tenant is therefore refused by the seeded row alone, whatever any other case
// did: no counter and no row is shared between cases, since each serves its own
// host against its own database. A case added to this endpoint takes a tenant
// of its own; the case below holds that, by spending more than the row's value
// on each of the endpoint's other tenants.
//
// The settings module registers an item under its full path — the namespace
// plus the item key — and looks a key up by that exact path. A read of the bare
// item key covers no declaration and fails open, which is a run that lets every
// request through and says so only on its own standard error. The count and the
// refusal below are what tell the declared path from the bare one; the chain
// listing cannot, because the layer's position is the same either way.
func TestTheLimitingLayerRefusesPastTheLimit(t *testing.T) {
	f := writeFixture(t)
	f.migrate(t)
	run := f.start(t)

	for i := 1; i <= limitedLimit; i++ {
		status, _, body := send(t, get(t, run.public, "/orders", limitedTenant, subject))
		if status != nethttp.StatusOK {
			t.Fatalf("request %d of the %d the limit allows answered %d: %s",
				i, limitedLimit, status, body)
		}
	}

	status, header, body := send(t, get(t, run.public, "/orders", limitedTenant, subject))
	if status != nethttp.StatusTooManyRequests {
		t.Fatalf("the request past the limit answered %d, want %d: %s",
			status, nethttp.StatusTooManyRequests, body)
	}
	if got := header.Get("Retry-After"); got == "" || got == "0" {
		t.Errorf("the refusal carries Retry-After %q, want a positive number of seconds", got)
	}
	requireProblem(t, "the refusal by the limiter", header)

	// The request row is written once the inner chain has returned, so the
	// table is read until the refusal turns up rather than once.
	deadline := time.Now().Add(settleLimit)
	for {
		var captured bool
		for _, row := range f.journal(t) {
			if row.source == "request" && row.action == nethttp.MethodGet && row.object == ordersPath &&
				row.outcome == strconv.Itoa(nethttp.StatusTooManyRequests) &&
				row.tenant == limitedTenant {
				captured = true
			}
		}
		if captured {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the journal captured no request row for the refusal of %s on %s %s",
				limitedTenant, nethttp.MethodGet, ordersPath)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestTheLimitingRowCoversOneTenantAlone is the case for how far the row the
// seed writes reaches.
//
// The layer registers on the endpoint and reads its limit for the request's
// tenant, so the row the seed writes for one tenant covers every route on that
// endpoint for that tenant. Every other tenant keeps the declared default,
// which is far above what a case spends: the seed writes the low row for one
// tenant alone, so a case reaching the endpoint as another tenant is never
// refused by it. The reads below spend more than the row's value on each of
// them, which is what tells the declared default from a second copy of the row.
//
// The disabled tenant is left out on purpose: resolution refuses it before the
// layer is reached, so its request count says nothing about the row.
func TestTheLimitingRowCoversOneTenantAlone(t *testing.T) {
	f := writeFixture(t)
	f.migrate(t)
	run := f.start(t)

	for _, other := range []string{acme, globex} {
		for i := 1; i <= limitedLimit+1; i++ {
			status, _, body := send(t, get(t, run.public, "/orders", other, subject))
			if status != nethttp.StatusOK {
				t.Fatalf("request %d of %d to %s answered %d: %s; the row the seed writes names %s, and every other tenant on the endpoint reads the declared default",
					i, limitedLimit+1, other, status, body, limitedTenant)
			}
		}
	}
}

// TestAnInterruptStopsTheRun is the case for the lifecycle's exit. The run is
// asked to stop rather than killed, with a placement in flight, and the order
// the modules are released in is read off what reaches the stream.
//
// The entry point is stopped before any other module: the placement it
// admitted is answered, and neither application module is told to stop until
// it has been. Both take the router up in Init rather than requiring it, and
// here both are constructed after the entry point, so reverse construction
// order alone would stop them while the placement is still inside. The stream
// is read once the entry point is seen to stop listening, with the placement
// still waiting for its body, and a module stopped ahead of the entry point has
// written its record by then.
//
// The other modules follow in reverse construction order, every stop ahead of
// every close, and the closes run in reverse construction order again:
// inventory is constructed before orders, so it is stopped and closed after
// it. A record written as a module closes only reaches the stream because the
// logging requirement puts the logging module behind it: without the
// declaration, the logger would already be closed and the record would go
// nowhere while the case still saw a clean exit.
func TestAnInterruptStopsTheRun(t *testing.T) {
	f := writeFixture(t)
	f.migrate(t)
	run := f.start(t)

	if _, already := findRecord(run.output(), stoppedMsg); already {
		t.Fatal("the host had already stopped before the case interrupted it")
	}
	held := holdPlacement(t, run, acme, subject, "held", "a widget placed across the interrupt")
	run.interrupt()
	waitUntilRefused(t, run)

	draining := run.output()
	for _, name := range []string{ordersModuleName, inventoryModuleName} {
		if recordOrder(draining, stopMsg, "module", name) >= 0 {
			t.Errorf("%s was stopped while the entry point was still draining a placement it admitted; the run wrote:\n%s",
				name, draining)
		}
	}
	if status, body := held.finish(t); status != nethttp.StatusCreated {
		t.Errorf("the placement admitted before the interrupt answered %d: %s", status, body)
	}
	run.stop()

	out := run.output()
	if _, ok := findRecord(out, stoppedMsg); !ok {
		t.Fatalf("after an interrupt the run wrote no %q record; it wrote:\n%s", stoppedMsg, out)
	}

	// The records the release writes, in the order it has to write them.
	release := []struct{ msg, module string }{
		{stopMsg, ordersModuleName},
		{stopMsg, inventoryModuleName},
		{closeMsg, ordersModuleName},
		{closeMsg, inventoryModuleName},
	}
	previous := -1
	for i, r := range release {
		at := recordOrder(out, r.msg, "module", r.module)
		if at < 0 {
			t.Fatalf("the release wrote no %q record from %s; the run wrote:\n%s", r.msg, r.module, out)
		}
		if at < previous {
			t.Fatalf("the %q record from %s came before the %q record from %s; want %v, the stops in "+
				"reverse construction order and the closes after every stop in the same order; the run wrote:\n%s",
				r.msg, r.module, release[i-1].msg, release[i-1].module, release, out)
		}
		previous = at
	}
}

// heldPlacement is a placement the host has admitted and whose handler is
// waiting for the body, which the case sends when it chooses.
type heldPlacement struct {
	conn   net.Conn
	reader *bufio.Reader
	req    *nethttp.Request
	body   []byte
}

// holdPlacement sends a placement's head and returns once its handler has asked
// for the body.
//
// The head carries Expect: 100-continue, and the server sends the interim
// answer only when the handler first reads the body. Receiving it is what says
// the request is inside the handler: a connection that is merely open may still
// be waiting in the listener's backlog, where closing the socket drops it
// rather than draining it.
func holdPlacement(t *testing.T, run *running, tenantName, credential, id, item string) *heldPlacement {
	t.Helper()
	body, err := json.Marshal(map[string]string{"id": id, "item": item})
	if err != nil {
		t.Fatalf("encoding the placement: %v", err)
	}
	r, err := nethttp.NewRequest(nethttp.MethodPost, run.public+"/orders", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Expect", "100-continue")
	r.Header.Set("X-Tenant", tenantName)
	r.SetBasicAuth(credential, password)
	r.Close = true

	var wire bytes.Buffer
	if err = r.Write(&wire); err != nil {
		t.Fatalf("serialising the placement: %v", err)
	}
	head, ok := bytes.CutSuffix(wire.Bytes(), body)
	if !ok {
		t.Fatalf("the serialised placement does not end in its body:\n%s", wire.Bytes())
	}

	conn, err := net.DialTimeout("tcp", run.publicAddr, waitLimit)
	if err != nil {
		t.Fatalf("connecting to the public endpoint: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err = conn.SetDeadline(time.Now().Add(waitLimit)); err != nil {
		t.Fatalf("bounding the placement's connection: %v", err)
	}
	if _, err = conn.Write(head); err != nil {
		t.Fatalf("sending the placement's head: %v", err)
	}
	reader := bufio.NewReader(conn)
	interim, err := nethttp.ReadResponse(reader, r)
	if err != nil {
		t.Fatalf("reading the answer to the placement's head: %v", err)
	}
	if interim.StatusCode != nethttp.StatusContinue {
		var text strings.Builder
		_, _ = io.Copy(&text, interim.Body)
		t.Fatalf("the placement's head was answered %d rather than asked for its body: %s",
			interim.StatusCode, text.String())
	}
	return &heldPlacement{conn: conn, reader: reader, req: r, body: body}
}

// finish sends the body and returns the placement's status and response body.
func (h *heldPlacement) finish(t *testing.T) (int, string) {
	t.Helper()
	if _, err := h.conn.Write(h.body); err != nil {
		t.Fatalf("sending the placement's body: %v", err)
	}
	resp, err := nethttp.ReadResponse(h.reader, h.req)
	if err != nil {
		t.Fatalf("reading the answer to the placement: %v", err)
	}
	defer resp.Body.Close()
	var text strings.Builder
	_, _ = io.Copy(&text, resp.Body)
	return resp.StatusCode, text.String()
}

// waitUntilRefused polls the public endpoint until it refuses a connection,
// which is the entry point's Stop closing its listening socket, and then lets
// the stream catch up.
//
// The pause after the refusal is for a module stopped ahead of the entry point:
// its record was written before the socket closed, and the pause is the time
// the record has to cross into what the case reads.
func waitUntilRefused(t *testing.T, run *running) {
	t.Helper()
	deadline := time.Now().Add(waitLimit)
	for {
		conn, err := net.DialTimeout("tcp", run.publicAddr, time.Second)
		if err != nil {
			break
		}
		_ = conn.Close()
		if time.Now().After(deadline) {
			t.Fatalf("the public endpoint still accepted connections %v after the interrupt; the run wrote:\n%s",
				waitLimit, run.output())
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(streamLag)
}

// TestVersionAssemblesNothing is the case for an empty participating set being
// empty rather than a request for everything.
func TestVersionAssemblesNothing(t *testing.T) {
	cmd := exec.Command(hostBinary, "version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("version failed: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != hostVersion {
		t.Errorf("version answered %q, want %q", got, hostVersion)
	}
}

// TestHelpIsAnsweredBeforeASourceIsRead is the case for help never reaching the
// configuration. The locator names a source that cannot be read, and the
// question is still answered: the command-line library replies before the
// assembly begins.
func TestHelpIsAnsweredBeforeASourceIsRead(t *testing.T) {
	cmd := exec.Command(hostBinary, "serve", "--help",
		"--config", "file:///nonexistent/orders-host/config.yaml")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("help failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "serve") {
		t.Errorf("help answered %q, which does not describe the command that was asked about", out)
	}
}

// TestTheLocatorBeforeTheCommandIsTheSameRun is the case for where the root
// argument sits. The argument naming the config source belongs to the root, so
// writing it before the command and after it are the same run — which a command
// given its own copy of the flag would not be.
func TestTheLocatorBeforeTheCommandIsTheSameRun(t *testing.T) {
	f := writeFixture(t)

	after := exec.Command(hostBinary, "migrate", "--config", "file://"+f.config)
	if out, err := after.CombinedOutput(); err != nil {
		t.Fatalf("the locator after the command failed: %v\n%s", err, out)
	}
	before := exec.Command(hostBinary, "--config", "file://"+f.config, "migrate")
	if out, err := before.CombinedOutput(); err != nil {
		t.Fatalf("the locator before the command failed: %v\n%s", err, out)
	}

	if got := f.tables(t); !contains(got, orderTable) {
		t.Errorf("the run that named the source first left no %q table; the file holds %v", orderTable, got)
	}
}

// TestHashAssemblesNothing is the case for the command an operator reaches for
// before anything is running: it reads its input from standard input, writes a
// hash the subject table accepts, and assembles no module at all.
//
// The round trip is what makes this an assertion rather than a spelling check:
// the hash is parsed back through the same reader the subject table uses, so a
// command that emitted something of the right shape and the wrong contents
// fails here.
func TestHashAssemblesNothing(t *testing.T) {
	cmd := exec.Command(hostBinary, "hash")
	cmd.Stdin = strings.NewReader(password + "\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("hash failed: %v\n%s", err, out)
	}
	encoded := strings.TrimSpace(string(out))
	if _, err := static.ParseHash(encoded); err != nil {
		t.Errorf("the subject table cannot read what the hash command wrote: %v (it wrote %q)", err, encoded)
	}
}

// TestAnUnknownCommandIsReportedByTheLibrary is the case for the rejection
// coming from the command-line library rather than from this program, and for
// it arriving once.
func TestAnUnknownCommandIsReportedByTheLibrary(t *testing.T) {
	cmd := exec.Command(hostBinary, "nosuchcommand")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("an unknown command succeeded; it wrote:\n%s", out)
	}
	requireFailedRun(t, string(out), "nosuchcommand")
}

// TestAServedRequestProducesASpan is the telemetry case.
//
// The layer is registered on the endpoint by configuration and by nothing a
// route says, so a handler written with no thought for telemetry produces a
// span all the same, and the run exports it through the backend the command
// tree named. Neither the route nor the handler asks for any of it.
func TestAServedRequestProducesASpan(t *testing.T) {
	f := writeFixture(t)
	f.migrate(t)
	run := f.start(t)

	status, body := call(t, run.public, "/orders", acme, subject)
	if status != nethttp.StatusOK {
		t.Fatalf("a guarded route with a credential answered %d: %s", status, body)
	}

	// The batch processor hands the span over on its own schedule, so the case
	// waits for the line rather than reading it straight away. The span is
	// named for the endpoint and the method the layer observed, which is a
	// string nothing else in this run writes.
	deadline := time.Now().Add(settleLimit)
	for time.Now().Before(deadline) {
		if strings.Contains(run.output(), publicEndpoint+" GET") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no span named for the endpoint and the method reached the export backend; the run wrote:\n%s",
		run.output())
}

// contains reports whether a string slice holds a value.
func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
