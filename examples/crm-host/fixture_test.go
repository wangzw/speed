package main

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/glebarez/go-sqlite" // the database/sql driver the harness reads SQLite through
	"go.yaml.in/yaml/v3"
)

// A case is one stack's worth of backends, a configuration, and the host
// processes run against them. The API below is what a feature slice extends:
// a slice adds its own configuration through config.set before starting a
// run, creates what it needs through the command line or the admin endpoint,
// and reads what it asserts on through sql.

// The operator every case configures in the static backend's table. The owner
// half of the operator's identity is the host's operatorOwner.
const (
	operatorSubject  = "root"
	operatorPassword = "operator-secret"
)

// tenantHeader is the request header the configuration names for resolution.
const tenantHeader = "X-Tenant"

// journalledRoutes are the routes whose requests the journal captures: every
// route an application module registers as worth a journal row, so a module
// added to the host has its requests captured without an edit here.
func journalledRoutes() []string {
	var routes []string
	for _, a := range applications {
		for _, route := range a.journalled {
			if !slices.Contains(routes, route) {
				routes = append(routes, route)
			}
		}
	}
	return routes
}

// workerEnv empties the two endpoint lists a process without the HTTP entry
// point cannot honour. The journal and the telemetry assembler take the entry
// point up in Init whenever their list is not empty, and the lists are the
// serving configuration's; an empty variable is a given value, and an empty
// list.
//
// The variable names are spelled out rather than built from envPrefix: a case
// that assembled them the way the host does would follow the host's mistake.
var workerEnv = []string{
	"CRM_AUDIT__JOURNAL__ENDPOINTS=",
	"CRM_OBSERVABILITY__OTEL__ENDPOINTS=",
}

// The log level every run is held to. Readiness, the stop and the release
// records are all read from Info records, so a run at a quieter level would
// read as one that never came up.
const (
	logLevelPath = "log.level"
	logLevelEnv  = "CRM_LOG__LEVEL"
	runLogLevel  = "info"
)

// oneShotAddress is where every endpoint of a one-shot command's configuration
// points. A one-shot command binds nothing — the ones that run the HTTP entry
// module stop before Serve — so its addresses only have to be well formed, and
// port zero never collides with a port reserved for a replica.
const oneShotAddress = "127.0.0.1:0"

// configTree is a configuration being written, as nested maps. Paths are
// dotted; the file nests them.
type configTree map[string]any

// set writes a value under a dotted path, creating the sections on the way.
func (c configTree) set(path string, value any) {
	parts := strings.Split(path, ".")
	node := c
	for _, part := range parts[:len(parts)-1] {
		next, ok := node[part].(configTree)
		if !ok {
			next = configTree{}
			node[part] = next
		}
		node = next
	}
	node[parts[len(parts)-1]] = value
}

// get reads the value under a dotted path, if there is one.
func (c configTree) get(path string) (any, bool) {
	parts := strings.Split(path, ".")
	node := c
	for _, part := range parts[:len(parts)-1] {
		next, ok := node[part].(configTree)
		if !ok {
			return nil, false
		}
		node = next
	}
	v, ok := node[parts[len(parts)-1]]
	return v, ok
}

// crmCase is one case: a stack, the backends it runs on, and its configuration,
// seen from one test. The helpers fail the test the view is bound to, so a
// subtest takes a view of its own with in(t) and a failure there ends the
// subtest rather than its parent.
type crmCase struct {
	t *testing.T
	*caseState
}

// caseState is what every view of a case shares.
type caseState struct {
	// root is the test the case was laid out in. What the case holds for its
	// whole length — its database, its claims on the shared servers, the
	// harness's own connection — is released when root ends, whichever view
	// took it up.
	root  *testing.T
	stack stack
	dir   string
	// suffix is unique to the case. Tenants and users are named with it, so
	// cases sharing a server that has nothing to isolate by — Memcached —
	// never share a key.
	suffix string
	config configTree
	// onEndpoint are the items a case sets on one endpoint beside its
	// address, by endpoint. The endpoint table is a map, which only the
	// primary source gives, and every launch writes it whole.
	onEndpoint map[string]configTree
	// processLocal says some backend of the stack lives in one process, so
	// the case runs a single replica.
	processLocal bool

	dialect    string // "sqlite" or "postgres": how the harness reads the database
	dsn        string // where the harness reads it
	sqlitePath string // the database file, on the SQLite stack
	receiver   *otlpReceiver

	// linked says the host processes reach each backend over a link the
	// case can cut, and links holds the proxy of each link in use.
	linked bool
	links  map[link]*faultProxy

	postgres  string
	redisAddr string
	redisDB   int
	redisUp   bool
	natsURL   string
	natsName  string

	mu       sync.Mutex
	replicas int // how many serving replicas have been numbered
	db       *sql.DB
}

// in is a view of the case whose failures end t: a subtest of the case's test.
func (c *crmCase) in(t *testing.T) *crmCase { return &crmCase{t: t, caseState: c.caseState} }

// newCase lays out a case on one stack: the stack-independent configuration,
// then each of the stack's four modules configured by its backend.
func newCase(t *testing.T, s stack) *crmCase {
	t.Helper()
	return layOutCase(t, s, false)
}

// layOutCase is newCase, with the host processes reaching each backend over a
// link the case can cut when linked says so.
func layOutCase(t *testing.T, s stack, linked bool) *crmCase {
	t.Helper()
	var raw [4]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatalf("drawing the case suffix: %v", err)
	}
	c := &crmCase{t: t, caseState: &caseState{
		root:   t,
		stack:  s,
		dir:    t.TempDir(),
		suffix: hex.EncodeToString(raw[:]),
		config: configTree{},
		linked: linked,
	}}
	c.writeBaseConfig()
	for _, module := range []string{s.engine, s.kv, s.event, s.exporter} {
		b, ok := backends[module]
		if !ok {
			t.Fatalf("stack %s names %q, which the harness has no backend for; add it to backends", s.name, module)
		}
		if b.processLocal {
			c.processLocal = true
		}
		b.configure(c)
	}
	return c
}

// caseHashIterations is the PBKDF2 cost of every password the cases derive:
// the CRM users', through users.password-iterations, and the operator's, which
// the harness writes into the static backend's subject table itself.
//
// Each Basic request, each user created and each sign-in derives a password
// once, and at the production cost a derivation takes over a second in a
// binary built with the race detector, which put a -race run of this package
// far past go test's default timeout. Both backends verify a hash at the count
// the hash carries, so what a case shows does not rest on the count; a case
// whose property rests on how long a derivation takes sets the production cost
// itself.
const caseHashIterations = 1_000

// operatorHash is the operator's entry of the static backend's subject table,
// derived at caseHashIterations in the format that backend documents: scheme,
// iteration count, salt and key, separated by '$', the last two in unpadded
// standard base64. It is derived here because static.Hash takes no count: it
// derives at the backend's fixed production cost, and the derivation at a
// given cost is unexported. The entry `hash` writes is held to that backend's
// own derivation by TestAnOperatorSignsInWithWhatHashWrote, and every
// operator request of every case to this one.
func operatorHash() (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, operatorPassword, salt, caseHashIterations, 32)
	if err != nil {
		return "", err
	}
	return strings.Join([]string{
		"pbkdf2-sha256", strconv.Itoa(caseHashIterations),
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key),
	}, "$"), nil
}

// writeBaseConfig writes everything that does not depend on the stack.
//
// The roster's refresh period is zero so that a case sees the roster exactly as
// it was written: a replica rereads it after its own writes, and a case that
// writes through one replica and reads through another sets a period of its
// own.
func (c *crmCase) writeBaseConfig() {
	hashed, err := operatorHash()
	if err != nil {
		c.t.Fatalf("deriving the operator's hash: %v", err)
	}
	guarded := []string{apiEndpoint, accountEndpoint, adminEndpoint}
	c.config.set(logLevelPath, runLogLevel)
	c.config.set("authn.gate.endpoints", guarded)
	c.config.set("authn.static.name", operatorOwner)
	c.config.set("authn.static.subjects", map[string]string{operatorSubject: hashed})
	c.config.set("users.password-iterations", caseHashIterations)
	c.config.set("tenant.resolve.endpoints", []string{apiEndpoint})
	c.config.set("tenant.resolve.extractor", "header")
	c.config.set("tenant.resolve.header-name", tenantHeader)
	c.config.set("tenant.catalog.refresh-period", "0s")
	c.config.set("audit.journal.endpoints", guarded)
	c.config.set("audit.journal.requests", journalledRoutes())
	c.config.set("ratelimit.endpoints", []string{apiEndpoint})
	c.config.set("observability.otel.service-name", hostName)
	c.config.set("observability.otel.endpoints", guarded)
	c.config.set("observability.otel.batch.batch-timeout", "200ms")
}

// postgresDatabase is the case's PostgreSQL database, created on first use. The
// engine and any PostgreSQL backend of the stack share it, as one deployment's
// modules would.
func (c *crmCase) postgresDatabase() string {
	if c.postgres == "" {
		c.postgres = createPostgresDatabase(c.root)
	}
	return c.postgres
}

// redisDatabase is the case's Redis database, claimed on first use and shared by
// the stack's Redis backends.
func (c *crmCase) redisDatabase() (string, int) {
	if !c.redisUp {
		c.redisAddr, c.redisDB = claimRedisDatabase(c.root)
		c.redisUp = true
	}
	return c.redisAddr, c.redisDB
}

// natsSpace is the case's NATS server locator, and the one name its event
// namespace and its key-value bucket share, claimed on first use and shared by
// the stack's NATS backends.
func (c *crmCase) natsSpace() (url, name string) {
	if c.natsURL == "" {
		c.natsName = natsName(c.suffix)
		c.natsURL = natsNamespace(c.root, c.natsName)
	}
	return c.natsURL, c.natsName
}

// newAddresses reserves the listening addresses of one serving replica. They
// are reserved right before the replica is launched, so the window between a
// reservation and the bind is the launch alone.
func (c *crmCase) newAddresses() map[string]string {
	c.t.Helper()
	addrs := map[string]string{}
	for _, name := range servedEndpoints {
		addrs[name] = freeAddress(c.t)
	}
	return addrs
}

// writeConfig writes the configuration under a file name of the case's
// directory, with every endpoint at the address given, and returns its locator.
//
// The file is replaced whole, by a rename, so a child starting while it is
// rewritten reads the old configuration or the new one and never half of one;
// and it is left alone when its content is unchanged.
func (c *crmCase) writeConfig(file string, addrs map[string]string) string {
	c.t.Helper()
	c.mu.Lock()
	if level, _ := c.config.get(logLevelPath); level != runLogLevel {
		c.mu.Unlock()
		c.t.Fatalf("the configuration sets %s to %v: readiness, the stop and the release records are read "+
			"from Info records, so it stays %s; a case wanting fewer records filters the ones it reads",
			logLevelPath, level, runLogLevel)
	}
	endpoints := configTree{}
	for name, addr := range addrs {
		entry := configTree{}
		for key, value := range c.onEndpoint[name] {
			entry[key] = value
		}
		entry["address"] = addr
		endpoints[name] = entry
	}
	c.config.set("http.endpoints", endpoints)
	body, err := yaml.Marshal(c.config)
	c.mu.Unlock()
	if err != nil {
		c.t.Fatalf("encoding the configuration: %v", err)
	}
	path := filepath.Join(c.dir, file)
	if err := writeFileAtomic(path, body); err != nil {
		c.t.Fatalf("writing the configuration: %v", err)
	}
	return "file://" + path
}

// setOnEndpoint sets an item of one endpoint beside the address each launch
// gives it, for every launch after the call.
func (c *crmCase) setOnEndpoint(endpoint, key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.onEndpoint == nil {
		c.onEndpoint = map[string]configTree{}
	}
	if c.onEndpoint[endpoint] == nil {
		c.onEndpoint[endpoint] = configTree{}
	}
	c.onEndpoint[endpoint][key] = value
}

// oneShotConfig is the configuration every command other than serve runs with:
// the serving configuration with every endpoint at port zero.
func (c *crmCase) oneShotConfig() string {
	c.t.Helper()
	addrs := map[string]string{}
	for _, name := range servedEndpoints {
		addrs[name] = oneShotAddress
	}
	return c.writeConfig("config-one-shot.yaml", addrs)
}

// requireRunLevel fails the case when an environment given to a run would move
// the log level off the one the harness reads.
func (c *crmCase) requireRunLevel(env []string) {
	c.t.Helper()
	for _, kv := range env {
		if value, ok := strings.CutPrefix(kv, logLevelEnv+"="); ok && !strings.EqualFold(strings.TrimSpace(value), runLogLevel) {
			c.t.Fatalf("the run is given %s: readiness, the stop and the release records are read from Info records, "+
				"so the level stays %s", kv, runLogLevel)
		}
	}
}

// args builds the argument list of one one-shot command of the case's stack.
func (c *crmCase) args(words ...string) []string {
	return append(append([]string{c.stack.name}, words...), "--config", c.oneShotConfig())
}

// run runs a one-shot command of the stack and returns what it left behind,
// failed or not.
func (c *crmCase) run(env []string, stdin string, words ...string) commandResult {
	c.t.Helper()
	c.requireRunLevel(env)
	return runCommand(c.t, c.args(words...), env, stdin)
}

// mustRun runs a one-shot command and fails the case unless it exits 0.
func (c *crmCase) mustRun(stdin string, words ...string) commandResult {
	c.t.Helper()
	res := c.run(nil, stdin, words...)
	if res.err != nil {
		c.t.Fatalf("%s %s failed: %v\n%s", c.stack.name, strings.Join(words, " "), res.err, res.combined())
	}
	return res
}

// resultOf decodes what a command answered with: the last JSON line of its
// standard output. Records and the database module's own lines share the
// stream, and neither is a line of JSON.
func (c *crmCase) resultOf(res commandResult, v any) {
	c.t.Helper()
	lines := strings.Split(strings.TrimSpace(res.stdout), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "{") || strings.HasPrefix(line, "[") {
			if err := json.Unmarshal([]byte(line), v); err != nil {
				c.t.Fatalf("decoding the command's answer %q: %v", line, err)
			}
			return
		}
	}
	c.t.Fatalf("the command answered with no JSON line:\n%s", res.combined())
}

// migrate runs the stack's migrate command.
func (c *crmCase) migrate() {
	c.t.Helper()
	c.mustRun("", "migrate")
}

// tenantRef is a tenant a case registered, by identifier and alias.
type tenantRef struct {
	ID    string
	Alias string
}

// tenantNamed is the case's tenant of a given name: an identifier and an alias
// unique to the case.
func (c *crmCase) tenantNamed(name string) tenantRef {
	return tenantRef{ID: "t_" + name + "_" + c.suffix, Alias: name + "-" + c.suffix}
}

// createTenant registers an active tenant through the command line.
func (c *crmCase) createTenant(name string) tenantRef {
	c.t.Helper()
	ref := c.tenantNamed(name)
	var got tenantView
	c.resultOf(c.mustRun("", "tenant", "create", "--tenant-id", ref.ID, "--tenant-alias", ref.Alias), &got)
	if got.ID != ref.ID || got.Alias != ref.Alias || got.Status != "active" {
		c.t.Fatalf("tenant create answered %+v, want %+v active", got, ref)
	}
	return ref
}

// disableTenant disables a tenant through the command line.
func (c *crmCase) disableTenant(ref tenantRef) {
	c.t.Helper()
	var got tenantView
	c.resultOf(c.mustRun("", "tenant", "disable", "--tenant-id", ref.ID), &got)
	if got.Status != "disabled" {
		c.t.Fatalf("tenant disable answered %+v", got)
	}
}

// userRef is a CRM user a case created.
type userRef struct {
	ID       string
	Email    string
	Password string
}

// userNamed is the case's user of a given name.
func (c *crmCase) userNamed(name string) userRef {
	return userRef{Email: name + "@" + c.suffix + ".crm.test", Password: name + " has a password " + c.suffix}
}

// createUser creates a CRM user through the command line, the password going
// in on standard input.
func (c *crmCase) createUser(name string) userRef {
	c.t.Helper()
	ref := c.userNamed(name)
	var got userView
	c.resultOf(c.mustRun(ref.Password+"\n", "user", "create", "--email", ref.Email, "--display-name", name), &got)
	if got.ID == "" || got.Email != ref.Email {
		c.t.Fatalf("user create answered %+v for %s", got, ref.Email)
	}
	ref.ID = got.ID
	return ref
}

// grant gives a user a role in a tenant through the command line.
func (c *crmCase) grant(ref tenantRef, user userRef, role string) {
	c.t.Helper()
	var got grantView
	c.resultOf(c.mustRun("", "user", "grant", "--tenant-id", ref.ID, "--email", user.Email, "--role", role), &got)
	if got.TenantID != ref.ID || got.UserID != user.ID || got.Role != role {
		c.t.Fatalf("user grant answered %+v, want %s in %s as %s", got, user.ID, ref.ID, role)
	}
}

// setGlobal writes a global row of the settings surface's table before a run
// starts, for a case that needs a setting in place from the first request: a
// serving replica loads the table in its Init. The value is the text the
// settings surface stores for the declared type — for a duration, the form
// time.Duration writes.
func (c *crmCase) setGlobal(key, value string) {
	c.t.Helper()
	c.exec("INSERT INTO settings (tenant_id, key, value, version, updated_at) VALUES ('', ?, ?, 1, ?)",
		key, value, time.Now().UTC())
}

// setForTenant writes a tenant's row of the settings surface's table before a
// run starts, as setGlobal writes the global one.
func (c *crmCase) setForTenant(ref tenantRef, key, value string) {
	c.t.Helper()
	c.exec("INSERT INTO settings (tenant_id, key, value, version, updated_at) VALUES (?, ?, ?, 1, ?)",
		ref.ID, key, value, time.Now().UTC())
}

// crmHost is one serving replica.
type crmHost struct {
	*process
	replica int
	urls    map[string]string
}

// url is the base URL of one of the replica's endpoints.
func (h *crmHost) url(endpoint string) string { return "http://" + h.urls[endpoint] }

// rollout says how a set of replicas is started.
type rollout int

const (
	// together launches every replica before waiting for any, as a rollout
	// starts them. The replicas race each other through startup, and a case
	// whose property is that race uses this.
	together rollout = iota
	// firstThenRest launches the first replica, waits until it is ready, and
	// only then launches the rest. A case whose property is not the rollout
	// uses this, so a startup race it is not about cannot fail it.
	firstThenRest
)

// serve starts one serving replica and waits until it is ready.
func (c *crmCase) serve(env ...string) *crmHost {
	c.t.Helper()
	return c.serveReplicas(1, together, env...)[0]
}

// serveReplicas starts n serving replicas against the case's backends and waits
// until every one is ready. A stack with a backend that lives in one process
// runs a single replica, and asking for more fails the case.
func (c *crmCase) serveReplicas(n int, start rollout, env ...string) []*crmHost {
	c.t.Helper()
	if n > 1 && c.processLocal {
		c.t.Fatalf("stack %s keeps state in one process, so its replicas would not share it", c.stack.name)
	}
	c.requireRunLevel(env)
	first := c.takeReplicas(n)
	hosts := make([]*crmHost, n)
	launchFrom := func(from, to int) {
		for i := from; i < to; i++ {
			hosts[i] = c.launchReplica(first+i, env)
		}
		c.awaitReplicas(hosts[from:to], env)
	}
	switch start {
	case together:
		launchFrom(0, n)
	case firstThenRest:
		launchFrom(0, 1)
		launchFrom(1, n)
	}
	return hosts
}

// takeReplicas numbers the next n replicas and returns the first number.
func (c *crmCase) takeReplicas(n int) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	first := c.replicas
	c.replicas += n
	return first
}

// replicaName is how a replica is named in a failure message.
func (c *crmCase) replicaName(replica int) string {
	return c.stack.name + " " + serveCommand + " #" + strconv.Itoa(replica)
}

// launchReplica reserves a replica's addresses, writes its configuration and
// launches it.
func (c *crmCase) launchReplica(replica int, env []string) *crmHost {
	c.t.Helper()
	addrs := c.newAddresses()
	locator := c.writeConfig("config-"+strconv.Itoa(replica)+".yaml", addrs)
	args := []string{c.stack.name, serveCommand, "--config", locator}
	return &crmHost{
		process: launch(c.t, c.replicaName(replica), serveCommand, args, env),
		replica: replica,
		urls:    addrs,
	}
}

// portLost is how a failed bind reads in a run's output.
const portLost = "address already in use"

// awaitReplicas waits for every replica given and fails the case once, with the
// outcome and the output of every one of them, if any did not come up.
//
// A replica that lost one of its ports to another process between the
// reservation and the bind is launched again once, on fresh ports. That repeats
// the harness's own reservation, not anything a module did, and a replica that
// fails for any other reason is reported as it failed.
func (c *crmCase) awaitReplicas(hosts []*crmHost, env []string) {
	c.t.Helper()
	failures := awaitAll(hosts)
	relaunched := false
	for i, h := range hosts {
		if failures[i] != nil && strings.Contains(h.output(), portLost) {
			h.settle()
			hosts[i] = c.launchReplica(h.replica, env)
			relaunched = true
		}
	}
	if relaunched {
		failures = awaitAll(hosts)
	}
	if !slices.ContainsFunc(failures, func(err error) bool { return err != nil }) {
		return
	}
	var report strings.Builder
	for i, h := range hosts {
		outcome := "ready"
		if failures[i] != nil {
			outcome = failures[i].Error()
			h.settle()
		}
		fmt.Fprintf(&report, "\n%s: %s; it wrote:\n%s\n", h.name, outcome, tail(h.outputForReport()))
	}
	c.t.Fatalf("not every replica came up:%s", report.String())
}

// awaitAll waits for each replica's ready record, reporting per replica why it
// never came.
func awaitAll(hosts []*crmHost) []error {
	failures := make([]error, len(hosts))
	for i, h := range hosts {
		failures[i] = h.awaitReady()
	}
	return failures
}

// worker starts the stack's worker with the endpoint lists emptied, and waits
// until it is ready.
func (c *crmCase) worker(env ...string) *process {
	c.t.Helper()
	env = append(append([]string{}, workerEnv...), env...)
	c.requireRunLevel(env)
	p := launch(c.t, c.stack.name+" "+workerCommand, workerCommand, c.args(workerCommand), env)
	p.waitReady(c.t)
	return p
}

// sql is the harness's own connection to the case's database, for reading what
// a run left behind. The host never reads through it.
func (c *crmCase) sql() *sql.DB {
	c.t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.db != nil {
		return c.db
	}
	driver := "sqlite"
	if c.dialect == "postgres" {
		driver = "pgx"
	}
	handle, err := sql.Open(driver, c.dsn)
	if err != nil {
		c.t.Fatalf("opening the case's database: %v", err)
	}
	c.root.Cleanup(func() { _ = handle.Close() })
	c.db = handle
	return handle
}

// rebind turns the ? placeholders of a statement into PostgreSQL's numbered
// ones when the case runs on PostgreSQL.
func (c *crmCase) rebind(statement string) string {
	if c.dialect != "postgres" {
		return statement
	}
	var out strings.Builder
	n := 0
	for _, r := range statement {
		if r == '?' {
			n++
			out.WriteString("$" + strconv.Itoa(n))
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

// exec runs a statement on the case's database, for the few cases that put a
// row in a state no command reaches.
func (c *crmCase) exec(statement string, args ...any) {
	c.t.Helper()
	if _, err := c.sql().Exec(c.rebind(statement), args...); err != nil {
		c.t.Fatalf("running %q: %v", statement, err)
	}
}

// auditRow is one row of the journal's table.
type auditRow struct {
	source  string
	actor   string
	tenant  string
	action  string
	object  string
	outcome string
	purpose string
}

// journal reads every row of the journal's table, oldest first.
func (c *crmCase) journal() []auditRow {
	c.t.Helper()
	rows, err := c.sql().Query("SELECT source, actor, COALESCE(tenant_id, ''), action, object, outcome, purpose " +
		"FROM audit_entries ORDER BY id")
	if err != nil {
		c.t.Fatalf("reading the journal: %v", err)
	}
	defer rows.Close()
	var out []auditRow
	for rows.Next() {
		var r auditRow
		if err := rows.Scan(&r.source, &r.actor, &r.tenant, &r.action, &r.object, &r.outcome, &r.purpose); err != nil {
			c.t.Fatalf("scanning a journal row: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		c.t.Fatalf("reading the journal: %v", err)
	}
	return out
}

// awaitJournal waits for a journal row matching a predicate, because the
// request path writes its row once the handler has returned, after the
// response.
func (c *crmCase) awaitJournal(what string, match func(auditRow) bool) auditRow {
	c.t.Helper()
	var found auditRow
	if !eventually(settleLimit, func() bool {
		for _, row := range c.journal() {
			if match(row) {
				found = row
				return true
			}
		}
		return false
	}) {
		c.t.Fatalf("the journal holds no row for %s; it holds %+v", what, c.journal())
	}
	return found
}

// forEachStack runs a case on every stack the host defines, each as a parallel
// subtest with a case of its own. A stack whose backends need docker skips on
// a machine without it, unless CI is set.
func forEachStack(t *testing.T, body func(t *testing.T, c *crmCase)) {
	t.Helper()
	for _, s := range stacks {
		t.Run(s.name, func(t *testing.T) {
			t.Parallel()
			body(t, newCase(t, s))
		})
	}
}

// forEachSharedStack runs a case on every stack whose backends several
// processes can share, which is every stack that keeps no state in one process.
// The multi-replica cases run here; a stack whose state lives in one process has
// replicas that share nothing, so it is left out rather than skipped.
func forEachSharedStack(t *testing.T, body func(t *testing.T, c *crmCase)) {
	t.Helper()
	for _, s := range stacks {
		if !sharedStack(s) {
			continue
		}
		t.Run(s.name, func(t *testing.T) {
			t.Parallel()
			body(t, newCase(t, s))
		})
	}
}

// sharedStack reports whether none of a stack's modules keeps its state in one
// process.
func sharedStack(s stack) bool {
	for _, module := range []string{s.engine, s.kv, s.event, s.exporter} {
		if backends[module].processLocal {
			return false
		}
	}
	return true
}

// stackNamed is the host's stack of a given name.
func stackNamed(t *testing.T, name string) stack {
	t.Helper()
	for _, s := range stacks {
		if s.name == name {
			return s
		}
	}
	t.Fatalf("the host defines no stack named %q", name)
	return stack{}
}

// localStack is the stack that runs everywhere, for the cases whose property
// does not depend on which backends the host runs on.
const localStack = "sqlite-memory"
