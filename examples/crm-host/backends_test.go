package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib" // the database/sql driver the harness reads PostgreSQL through
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/redis/go-redis/v9"

	dbpostgres "github.com/vislake/speed/pkg/db/postgres"
	"github.com/vislake/speed/pkg/db/sqlite"
	eventmemory "github.com/vislake/speed/pkg/event/memory"
	eventnats "github.com/vislake/speed/pkg/event/nats"
	eventpostgres "github.com/vislake/speed/pkg/event/postgres"
	eventredis "github.com/vislake/speed/pkg/event/redis"
	kvmemcached "github.com/vislake/speed/pkg/kv/memcached"
	kvmemory "github.com/vislake/speed/pkg/kv/memory"
	kvnats "github.com/vislake/speed/pkg/kv/nats"
	kvpostgres "github.com/vislake/speed/pkg/kv/postgres"
	kvredis "github.com/vislake/speed/pkg/kv/redis"
	"github.com/vislake/speed/pkg/observability/otlp"
	"github.com/vislake/speed/pkg/observability/stdout"
)

// What the stacks run on. Every backend is one named container per machine,
// started by the first case that needs it and reused by every later one and by
// every later run, on a port docker chooses and this file reads back. Isolation
// is per case, inside the shared server: a database of its own on PostgreSQL, a
// database index of its own on Redis, a namespace and a bucket of its own on
// NATS. Memcached has nothing to isolate by, so the cases keep apart there by
// the keys alone: every key the host writes carries a tenant identifier or a
// random component, and the cases name their tenants and users uniquely.
//
// Every argument given to the docker CLI is one of this file's own constants,
// or derived from them alone; nothing a case supplies reaches a command line.

// backend is how the harness configures one module a stack names.
type backend struct {
	// processLocal says the module's state lives in one process — an
	// in-memory backend, or an engine on a local file — so several replicas
	// cannot share it.
	processLocal bool
	// configure writes the module's section of the case's configuration,
	// bringing up whatever server it needs.
	configure func(c *crmCase)
}

// backends configures every module a stack can name, by module name. A stack
// naming a module that is not here fails its cases at once rather than running
// on a configuration nobody wrote.
var backends = map[string]backend{
	sqlite.ModuleName: {processLocal: true, configure: func(c *crmCase) {
		c.dialect = "sqlite"
		c.sqlitePath = filepath.Join(c.dir, "crm.db")
		c.dsn = c.sqlitePath + "?" + sqliteLocator
		c.config.set("db.sqlite.dsn", c.dsn)
	}},
	dbpostgres.ModuleName: {configure: func(c *crmCase) {
		c.dialect = "postgres"
		c.dsn = c.postgresDatabase()
		c.config.set("db.postgres.dsn", c.through(databaseLink, c.dsn))
		c.config.set("db.postgres.max-open-conns", casePostgresConns)
	}},
	kvmemory.ModuleName:    {processLocal: true, configure: func(*crmCase) {}},
	eventmemory.ModuleName: {processLocal: true, configure: func(*crmCase) {}},
	kvredis.ModuleName: {configure: func(c *crmCase) {
		addr, index := c.redisDatabase()
		c.config.set("kv.redis.addr", c.through(storeLink, addr))
		c.config.set("kv.redis.db", index)
	}},
	eventredis.ModuleName: {configure: func(c *crmCase) {
		addr, index := c.redisDatabase()
		c.config.set("event.redis.addr", c.through(busLink, addr))
		c.config.set("event.redis.db", index)
	}},
	kvpostgres.ModuleName: {configure: func(c *crmCase) {
		c.config.set("kv.postgres.dsn", c.through(storeLink, c.postgresDatabase()))
		c.config.set("kv.postgres.max-conns", casePoolConns)
	}},
	eventpostgres.ModuleName: {configure: func(c *crmCase) {
		c.config.set("event.postgres.dsn", c.through(busLink, c.postgresDatabase()))
		c.config.set("event.postgres.max-conns", casePoolConns)
	}},
	kvnats.ModuleName: {configure: func(c *crmCase) {
		url, name := c.natsSpace()
		c.config.set("kv.nats.url", c.through(storeLink, url))
		c.config.set("kv.nats.bucket", name)
	}},
	eventnats.ModuleName: {configure: func(c *crmCase) {
		url, name := c.natsSpace()
		c.config.set("event.nats.url", c.through(busLink, url))
		c.config.set("event.nats.namespace", name)
	}},
	kvmemcached.ModuleName: {configure: func(c *crmCase) {
		c.config.set("kv.memcached.servers", []string{c.through(storeLink, memcachedServer.address(c.t))})
	}},
	stdout.ModuleName: {configure: func(*crmCase) {}},
	otlp.ModuleName: {configure: func(c *crmCase) {
		c.receiver = startOTLPReceiver(c.t)
		c.config.set("observability.otlp.endpoint", c.receiver.addr)
		c.config.set("observability.otlp.protocol", otlp.ProtocolHTTP)
	}},
}

// sqliteLocator is the parameters of the SQLite stack's locator. They are the
// driver's, and the database module hands the locator to the driver as it is,
// so they are the deployment's to write; the host and the harness read the
// database through the same locator.
//
// foreign_keys(1) turns on the reference checks SQLite leaves off by default,
// so a reference one module's table makes to another's is enforced on the
// SQLite stack as PostgreSQL enforces it.
//
// The locator names no transaction mode. The database module begins every
// SQLite transaction IMMEDIATE unless a locator says otherwise, holding the
// database's write lock from its BEGIN and waiting for it there within the
// busy timeout, so a transaction that checks and then writes waits out a
// concurrent writer on the SQLite stack as it does on PostgreSQL.
const sqliteLocator = "_pragma=foreign_keys(1)"

// The pools one process of a PostgreSQL stack opens. The server is shared by
// every case on the machine, and a process left at the modules' defaults opens
// some forty connections; a case that loads the host raises these with
// config.set.
const (
	casePostgresConns = 8 // db.postgres.max-open-conns
	casePoolConns     = 4 // kv.postgres.max-conns and event.postgres.max-conns
)

// The containers.
const (
	postgresContainer = "speed-crmtest-postgres"
	postgresImage     = "postgres:17-alpine"
	postgresPort      = "5432"
	// postgresSetting and postgresMaxConnections are the server's own
	// arguments raising its connection limit above the default hundred:
	// the server is shared by every case and every worktree on the machine.
	postgresSetting        = "-c"
	postgresMaxConnections = "max_connections=1000"
	// postgresUser and postgresPassword are the container's own credentials.
	// They guard a throwaway server bound to the loopback interface and are
	// not secrets.
	postgresUser     = "postgres"
	postgresPassword = "crmtest"
	// postgresMaintenance is the database the create and drop statements are
	// issued from: neither runs inside the database it acts on.
	postgresMaintenance = "postgres"

	redisContainer = "speed-crmtest-redis"
	redisImage     = "redis:7-alpine"
	redisPort      = "6379"

	natsContainer = "speed-crmtest-nats"
	natsImage     = "nats:2-alpine"
	natsPort      = "4222"
	// natsJetStream is the server's own argument turning JetStream on; both
	// NATS backends need it.
	natsJetStream = "-js"

	memcachedContainer = "speed-crmtest-memcached"
	memcachedImage     = "memcached:1.6-alpine"
	memcachedPort      = "11211"

	// loopbackPublish publishes a container port on the loopback interface,
	// on a host port docker chooses.
	loopbackPublish = "127.0.0.1::"

	dockerTimeout = 2 * time.Minute
	readyTimeout  = 3 * time.Minute
	runningFormat = "{{.State.Running}}"
	runningState  = "true"
	// specLabel is the label every container is created with, holding a
	// digest of the image and arguments it was created from, and specFormat
	// reads it back.
	specLabel  = "speed-crmtest.spec"
	specFormat = `{{index .Config.Labels "speed-crmtest.spec"}}`
	// bringUpBound bounds the wait that follows a create lost to another test
	// binary starting the same container at the same moment.
	bringUpBound = 30 * time.Second
)

// container is one shared server. Its address is looked up once per test
// binary; a failure is not remembered, because docker may answer a moment
// later.
type container struct {
	name    string
	image   string
	port    string
	runArgs []string // between the publish argument and the image
	cmdArgs []string // after the image
	ready   func(addr string) error

	mu   sync.Mutex
	addr string
}

var (
	postgresServer = &container{
		name: postgresContainer, image: postgresImage, port: postgresPort,
		runArgs: []string{"--env", "POSTGRES_PASSWORD=" + postgresPassword, "--env", "POSTGRES_USER=" + postgresUser},
		cmdArgs: []string{postgresSetting, postgresMaxConnections},
		ready:   pingPostgres,
	}
	redisServer = &container{
		name: redisContainer, image: redisImage, port: redisPort,
		ready: pingRedis,
	}
	natsServer = &container{
		name: natsContainer, image: natsImage, port: natsPort,
		cmdArgs: []string{natsJetStream},
		ready:   dialTCP,
	}
	memcachedServer = &container{
		name: memcachedContainer, image: memcachedImage, port: memcachedPort,
		ready: dialTCP,
	}
)

// address returns the loopback address the server is published on, starting or
// reusing its container, and skips the case where there is no docker.
func (c *container) address(t *testing.T) string {
	t.Helper()
	requireDocker(t)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.addr != "" {
		return c.addr
	}
	if err := c.ensure(); err != nil {
		t.Fatalf("bringing up the %s container: %v", c.name, err)
	}
	addr, err := c.published()
	if err != nil {
		t.Fatalf("reading the port of the %s container: %v", c.name, err)
	}
	deadline := time.Now().Add(readyTimeout)
	for {
		readyErr := c.ready(addr)
		if readyErr == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the %s container at %s did not answer within %s: %v", c.name, addr, readyTimeout, readyErr)
		}
		time.Sleep(250 * time.Millisecond)
	}
	c.addr = addr
	return addr
}

// ensure leaves the container running, whichever state it was in. A container
// that is there and stopped is started rather than removed: it is either an
// interrupted run's leftover or another test binary's container a moment
// before it comes up, and removing it would take the other binary's server
// with it.
//
// A container that is there is used only if it was created from this file's
// specification. One created from another — an older harness, before a
// constant here changed — would run every case on a server configured the old
// way with nothing saying so, and machines would differ in what their cases
// ran on. It is reported with the command that removes it, and not removed
// here, for the reason above.
func (c *container) ensure() error {
	if state, err := docker("inspect", "--format", runningFormat, c.name); err == nil {
		if err := c.checkSpec(); err != nil {
			return err
		}
		if strings.TrimSpace(state) == runningState {
			return nil
		}
		_, startErr := docker("start", c.name)
		return startErr
	}
	args := []string{
		"run", "--detach", "--name", c.name, "--label", specLabel + "=" + c.spec(),
		"--publish", loopbackPublish + c.port,
	}
	args = append(args, c.runArgs...)
	args = append(args, c.image)
	args = append(args, c.cmdArgs...)
	_, createErr := docker(args...)
	if createErr == nil {
		return nil
	}
	// The create can lose to another test binary: docker refuses the
	// duplicate name, and the container that won is the one to use.
	deadline := time.Now().Add(bringUpBound)
	for time.Now().Before(deadline) {
		if state, err := docker("inspect", "--format", runningFormat, c.name); err == nil &&
			strings.TrimSpace(state) == runningState {
			return c.checkSpec()
		}
		time.Sleep(150 * time.Millisecond)
	}
	return createErr
}

// spec is the digest of everything the container is created from, which the
// container carries as its specLabel.
func (c *container) spec() string {
	h := sha256.New()
	for _, part := range slices.Concat([]string{c.image, c.port}, c.runArgs, []string{"--"}, c.cmdArgs) {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// checkSpec fails when the container there was created from another
// specification than this file's.
func (c *container) checkSpec() error {
	label, err := docker("inspect", "--format", specFormat, c.name)
	if err != nil {
		return err
	}
	if got := strings.TrimSpace(label); got != c.spec() {
		return fmt.Errorf("the container %s was created from another specification than this harness's "+
			"(its %s label is %q, this harness's is %q): remove it with `docker rm -f %s` and run again; "+
			"it is not removed here, because another test binary may be using it",
			c.name, specLabel, got, c.spec(), c.name)
	}
	return nil
}

// published reads the host port docker mapped the server's port onto.
func (c *container) published() (string, error) {
	out, err := docker("port", c.name, c.port)
	if err != nil {
		return "", err
	}
	// "127.0.0.1:55012", one line per binding; the last colon separates the
	// port.
	line := strings.TrimSpace(strings.Split(strings.TrimSpace(out), "\n")[0])
	i := strings.LastIndex(line, ":")
	if i < 0 {
		return "", fmt.Errorf("docker port printed %q, which names no port", out)
	}
	port, err := strconv.ParseUint(line[i+1:], 10, 16)
	if err != nil {
		return "", fmt.Errorf("docker port printed %q, whose port is not a number", out)
	}
	return "127.0.0.1:" + strconv.FormatUint(port, 10), nil
}

// docker runs one docker invocation and returns its standard output, with
// docker's own words carried into the error.
func docker(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.Output()
	if err != nil {
		return string(out), fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

// dockerState is decided once per test binary: whether docker is there to be
// driven.
var dockerState = sync.OnceValue(func() string {
	if _, err := exec.LookPath("docker"); err != nil {
		return "docker is not on PATH"
	}
	if _, err := docker("info", "--format", "{{.ServerVersion}}"); err != nil {
		return "the docker daemon is not reachable: " + err.Error()
	}
	return ""
})

// requireDocker skips the case on a machine without docker, and fails it
// instead where CI is set, as pkg/internal/natstest does: a gate that is
// silently skipped wherever it is meant to run is not a gate.
func requireDocker(t *testing.T) {
	t.Helper()
	if reason := dockerState(); reason != "" {
		if os.Getenv("CI") != "" {
			t.Fatalf("CI is set, so a skip here would leave this stack unguarded: %s", reason)
		}
		t.Skipf("this stack's backends run in docker, and %s", reason)
	}
}

// dialTCP is the readiness check of a server that answers once it accepts.
func dialTCP(addr string) error {
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return err
	}
	return conn.Close()
}

// postgresDSN is the locator of one database on the shared server.
func postgresDSN(addr, database string) string {
	return fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable&connect_timeout=5",
		postgresUser, postgresPassword, addr, database)
}

// pingPostgres waits for a real connection rather than for the port: the
// image's entrypoint starts the server once on a local socket to initialise it,
// and a client that connects in that window is dropped when it restarts.
func pingPostgres(addr string) error {
	pool, err := sql.Open("pgx", postgresDSN(addr, postgresMaintenance))
	if err != nil {
		return err
	}
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return pool.PingContext(ctx)
}

// pingRedis waits for the server to answer a PING.
func pingRedis(addr string) error {
	client := redis.NewClient(&redis.Options{Addr: addr})
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return client.Ping(ctx).Err()
}

// postgresDatabaseName starts the name of every database the harness creates,
// followed by the owning process id, which is what lets a later run drop the
// ones a killed run left behind.
const postgresDatabaseName = "crmtest_p"

var (
	databaseCounter atomic.Int64
	staleDropped    sync.Once
)

// createPostgresDatabase creates an empty database for one case and drops it
// when the case ends.
func createPostgresDatabase(t *testing.T) string {
	t.Helper()
	addr := postgresServer.address(t)
	staleDropped.Do(func() { dropStaleDatabases(addr) })
	name := fmt.Sprintf("%s%d_n%d", postgresDatabaseName, os.Getpid(), databaseCounter.Add(1))
	if err := maintenance(addr, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatalf("creating the database %s: %v", name, err)
	}
	t.Cleanup(func() {
		// FORCE disconnects whatever a run left connected; without it a
		// pool that outlived the case keeps the database alive.
		if err := maintenance(addr, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Errorf("dropping the database %s: %v", name, err)
		}
	})
	return postgresDSN(addr, name)
}

// maintenance runs one statement on the maintenance database.
func maintenance(addr, statement string) error {
	pool, err := sql.Open("pgx", postgresDSN(addr, postgresMaintenance))
	if err != nil {
		return err
	}
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err = pool.ExecContext(ctx, statement)
	return err
}

// dropStaleDatabases removes the databases of harness processes that are gone.
// It is housekeeping: a failure costs disk, not correctness, and is not
// reported.
func dropStaleDatabases(addr string) {
	pool, err := sql.Open("pgx", postgresDSN(addr, postgresMaintenance))
	if err != nil {
		return
	}
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rows, err := pool.QueryContext(ctx, "SELECT datname FROM pg_database WHERE datname LIKE $1", postgresDatabaseName+"%")
	if err != nil {
		return
	}
	defer rows.Close()
	var stale []string
	for rows.Next() {
		var name string
		if rows.Scan(&name) != nil {
			break
		}
		digits, _, _ := strings.Cut(strings.TrimPrefix(name, postgresDatabaseName), "_n")
		pid, convErr := strconv.Atoi(digits)
		if convErr != nil || pid == os.Getpid() || processAlive(pid) {
			continue
		}
		stale = append(stale, name)
	}
	if rows.Err() != nil {
		return
	}
	for _, name := range stale {
		_ = maintenance(addr, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
	}
}

// processAlive reports whether a process id is in use. Signal 0 performs the
// existence check without delivering anything; EPERM means it exists and is
// someone else's.
func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}

// The Redis databases. A default server has sixteen; one holds the leases and
// is never handed out.
const (
	redisDatabases = 16
	redisLeaseDB   = redisDatabases - 1
	// redisLeaseTTL bounds a claim, so a binary killed mid-case does not hold
	// a database for the rest of the machine's uptime.
	redisLeaseTTL = 15 * time.Minute
	// redisClaimWait bounds the wait for a database to come free.
	redisClaimWait = 2 * time.Minute
	redisLeaseKey  = "crmtest:lease:"
)

// leaseCounter makes every claim's owner value unique within the binary.
var leaseCounter atomic.Int64

// releaseLease deletes a lease only while it still holds the owner value the
// claim wrote. A lease whose bound passed may have been claimed again by
// another case, and deleting it unconditionally would free that case's claim.
var releaseLease = redis.NewScript(`if redis.call("GET", KEYS[1]) == ARGV[1] then
	return redis.call("DEL", KEYS[1])
end
return 0`)

// claimRedisDatabase takes a database index nobody else holds, empties it and
// gives it back when the case ends. The claim is a key written with NX in the
// lease database, which is atomic on the server: two cases asking at once, in
// one binary or in two, cannot both be handed one index.
func claimRedisDatabase(t *testing.T) (string, int) {
	t.Helper()
	addr := redisServer.address(t)
	ctx, cancel := context.WithTimeout(context.Background(), redisClaimWait)
	defer cancel()
	leases := redis.NewClient(&redis.Options{Addr: addr, DB: redisLeaseDB})
	defer leases.Close()
	owner := fmt.Sprintf("pid:%d:claim:%d:%s", os.Getpid(), leaseCounter.Add(1), t.Name())
	for {
		for n := range redisLeaseDB {
			won, err := leases.SetNX(ctx, redisLeaseKey+strconv.Itoa(n), owner, redisLeaseTTL).Result()
			if err != nil {
				t.Fatalf("claiming a Redis database: %v", err)
			}
			if !won {
				continue
			}
			t.Cleanup(func() {
				releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer releaseCancel()
				client := redis.NewClient(&redis.Options{Addr: addr, DB: redisLeaseDB})
				defer client.Close()
				if err := releaseLease.Run(releaseCtx, client, []string{redisLeaseKey + strconv.Itoa(n)}, owner).Err(); err != nil {
					t.Logf("giving Redis database %d back: %v", n, err)
				}
			})
			db := redis.NewClient(&redis.Options{Addr: addr, DB: n})
			defer db.Close()
			if err := db.FlushDB(ctx).Err(); err != nil {
				t.Fatalf("emptying Redis database %d: %v", n, err)
			}
			return addr, n
		}
		select {
		case <-ctx.Done():
			t.Fatalf("every Redis database stayed claimed for %s", redisClaimWait)
		case <-time.After(150 * time.Millisecond):
		}
	}
}

// natsBucketStream starts the name of the stream JetStream keeps a key-value
// bucket in; the bucket's own name follows it.
const natsBucketStream = "KV_"

// natsNamePrefix starts every namespace and bucket name the harness claims,
// followed by the owning process id, which is what lets a later run remove the
// streams a killed run left behind.
const natsNamePrefix = "crm_p"

var staleStreamsDropped sync.Once

// natsName is the namespace and bucket name of one case.
func natsName(suffix string) string {
	return natsNamePrefix + strconv.Itoa(os.Getpid()) + "_" + suffix
}

// natsNamespace claims a namespace and a bucket name on the shared NATS server
// for one case, and removes what the case left there when it ends.
//
// The event backend names its streams <namespace>_<type in hex>, and the
// key-value backend keeps its bucket in the stream KV_<bucket>, so the one name
// used for both is what identifies the case's streams. The server keeps
// streams in file storage across restarts; without the removal every run would
// add to what every later run's server carries.
func natsNamespace(t *testing.T, name string) string {
	t.Helper()
	addr := natsServer.address(t)
	staleStreamsDropped.Do(func() { dropStaleNATSStreams(addr) })
	t.Cleanup(func() {
		if err := dropNATSStreams(addr, name); err != nil {
			t.Errorf("removing the NATS streams of %s: %v", name, err)
		}
	})
	return "nats://" + addr
}

// dropNATSStreams deletes the streams a namespace and a bucket of one name
// left on the server.
func dropNATSStreams(addr, name string) error {
	conn, err := nats.Connect("nats://"+addr, nats.Timeout(5*time.Second))
	if err != nil {
		return err
	}
	defer conn.Close()
	js, err := jetstream.New(conn)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	lister := js.StreamNames(ctx)
	var doomed []string
	for stream := range lister.Name() {
		if strings.HasPrefix(stream, name+"_") || stream == natsBucketStream+name {
			doomed = append(doomed, stream)
		}
	}
	if err := lister.Err(); err != nil {
		return err
	}
	for _, stream := range doomed {
		if err := js.DeleteStream(ctx, stream); err != nil && !errors.Is(err, jetstream.ErrStreamNotFound) {
			return fmt.Errorf("deleting the stream %s: %w", stream, err)
		}
	}
	return nil
}

// dropStaleNATSStreams removes the streams of harness processes that are gone.
// It is housekeeping: a failure costs storage, not correctness, and is not
// reported.
func dropStaleNATSStreams(addr string) {
	conn, err := nats.Connect("nats://"+addr, nats.Timeout(5*time.Second))
	if err != nil {
		return
	}
	defer conn.Close()
	js, err := jetstream.New(conn)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	lister := js.StreamNames(ctx)
	var stale []string
	for stream := range lister.Name() {
		rest, ok := strings.CutPrefix(strings.TrimPrefix(stream, natsBucketStream), natsNamePrefix)
		if !ok {
			continue
		}
		digits, _, _ := strings.Cut(rest, "_")
		pid, convErr := strconv.Atoi(digits)
		if convErr != nil || pid == os.Getpid() || processAlive(pid) {
			continue
		}
		stale = append(stale, stream)
	}
	if lister.Err() != nil {
		return
	}
	for _, stream := range stale {
		_ = js.DeleteStream(ctx, stream)
	}
}
