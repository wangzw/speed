//go:build integration

package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/db/internal/dbsuite"
	"github.com/vislake/speed/pkg/db/postgres"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

// The case about what a refused connection leaves on the process's streams.

// TestARefusedConnectionIsReportedOnceOnTheDiagnosticStream starts on a server
// that refuses the login. The startup fails with the database module's own
// error on standard error, and nothing else reports it: a connectivity check of
// GORM's own, which gorm.Open runs unless told not to, reports its failure
// through the handle's logger, and the module's logger writes that as a "gorm
// error" record on standard output, with the driver's reason in its detail — the
// user, the database and the address it tried — on the stream a host's log
// pipeline reads. The module opens its handles without that check, and standard
// output is searched for the words of GORM's message and of the driver's reason,
// whichever attribute of a record would carry them. Neither stream carries the
// password.
func TestARefusedConnectionIsReportedOnceOnTheDiagnosticStream(t *testing.T) {
	parsed, err := url.Parse(pgtest.Acquire(t))
	if err != nil {
		t.Fatalf("reading the fixture's locator: %v", err)
	}
	const wrongPassword = "not-the-password-7d2e"
	parsed.User = url.UserPassword(parsed.User.Username(), wrongPassword)
	locator := writeConfig(t, dbConfig{DB: dbSection{Postgres: &engineSection{DSN: parsed.String()}}})
	got := runHost(t, caseConnectRefused, "--config="+locator)

	if got.code == 0 {
		t.Fatalf("the startup succeeded with a password the server refuses\nstdout:\n%s\nstderr:\n%s",
			got.stdout, got.stderr)
	}
	if !strings.Contains(got.stderr, "db: connection could not be established") {
		t.Errorf("standard error does not carry the connect failure:\n%s", got.stderr)
	}
	for _, text := range []string{"failed to initialize database", "failed to connect", parsed.User.Username()} {
		if strings.Contains(got.stdout, text) {
			t.Errorf("standard output reports the refused connection (%q):\n%s", text, got.stdout)
		}
	}
	if strings.Contains(got.stdout+got.stderr, wrongPassword) {
		t.Errorf("the run echoes the password\nstdout:\n%s\nstderr:\n%s", got.stdout, got.stderr)
	}
}

// The concurrency cases: two replicas of one deployment starting at once
// against an empty database.

// applyDuration is how long the migration the replicas apply keeps the applier
// inside its transaction, and therefore how wide the window the other replica has
// to reach its own acquisition in is. It is the pg_sleep in
// migrations/postgres/0001_widgets.sql, and the overlap case asserts the applier
// really spent this long inside, so that a fixture that stopped widening the
// window fails loudly instead of turning the overlap assertion into a coin toss.
const applyDuration = 3 * time.Second

// concurrentRun holds the one pair of replicas both concurrency cases read.
//
// They are one run rather than two because the two properties only mean anything
// together: the division says the replicas were serialised, and the overlap says
// the second one was there while the first was working. A second replica that
// arrived late reads a complete record table and reports noop — with a mutex or
// without one — so a run whose two processes never overlapped proves nothing
// about the mutex and would still pass a division assertion. Reading the overlap
// off a different run than the division would leave exactly that hole open.
var concurrentRun struct {
	once    sync.Once
	results []replicaRun
}

// runReplicas returns the pair both cases read, starting it on first use.
//
// A skip or a fatal inside Do leaves it incomplete, so the second case re-enters
// and reports the same thing rather than reading a run that was never started.
func runReplicas(t *testing.T) []replicaRun {
	t.Helper()
	concurrentRun.once.Do(func() { concurrentRun.results = startReplicas(t) })
	return concurrentRun.results
}

// startReplicas runs the pair, and returns what each of them did.
func startReplicas(t *testing.T) []replicaRun {
	t.Helper()
	// The fixture fails the case rather than skipping it when the server is not
	// there: this is the gate the cross-process mutex is held by, and a gate that
	// is skipped wherever it is meant to run is not a gate.
	dsn := pgtest.Acquire(t)
	rendezvous := t.TempDir()
	roles := []string{"replica-0", "replica-1"}

	// The documents are written before the processes start: t.TempDir and
	// t.Fatalf belong to the test's own goroutine.
	locators := make([]string, len(roles))
	for i, role := range roles {
		locators[i] = replicaConfig(t, dsn, role)
	}

	results := make([]replicaRun, len(roles))
	var running sync.WaitGroup
	for i, role := range roles {
		running.Add(1)
		go func() {
			defer running.Done()
			results[i] = runReplica(role, locators[i], rendezvous)
		}()
	}
	running.Wait()

	for _, result := range results {
		if result.startErr != nil {
			t.Fatalf("%s could not be started: %v", result.role, result.startErr)
		}
		t.Logf("%s exited %d\nstdout:\n%s\nstderr:\n%s", result.role, result.exit, result.stdout, result.stderr)
	}
	return results
}

// replicaConfig writes one replica's configuration.
//
// The locator carries the replica's own application_name, and that is what the
// migration records: one file is applied by one of the two, and the row it
// writes is the only thing about the finished database that says which. The pool
// is held to a single connection, which is the smallest a host can configure and
// therefore the widest statement of what the migration run needs: the mutex, the
// record table and every statement travel on one connection, so a replica that
// needed a second one anywhere would stop here instead of passing quietly.
func replicaConfig(t *testing.T, dsn, role string) string {
	t.Helper()
	return writeConfig(t, dbConfig{DB: dbSection{Postgres: &engineSection{
		DSN:                  dsn + "&application_name=" + role,
		MaxOpenConns:         1,
		MigrationLockTimeout: "1m",
	}}})
}

// replicaRun is what one replica process did.
type replicaRun struct {
	role     string
	exit     int
	stdout   string
	stderr   string
	startErr error
}

// marked reports whether the replica printed a marker.
func (r replicaRun) marked(name string) bool {
	_, err := r.at(name)
	return err == nil
}

// at reads the moment the replica reached a marker.
func (r replicaRun) at(name string) (time.Time, error) {
	for _, line := range strings.Split(r.stdout, "\n") {
		marker, stamp, found := strings.Cut(strings.TrimSpace(line), "\t")
		if !found || marker != name {
			continue
		}
		moment, err := time.Parse(time.RFC3339Nano, stamp)
		if err != nil {
			return time.Time{}, fmt.Errorf("the %q marker carries %q, which is not a timestamp: %w",
				name, stamp, err)
		}
		return moment, nil
	}
	return time.Time{}, fmt.Errorf("%s printed no %q marker", r.role, name)
}

// runReplica starts one replica as a separate process and collects what it did.
func runReplica(role, locator, rendezvous string) replicaRun {
	ctx, cancel := context.WithTimeout(context.Background(), runLimit)
	defer cancel()
	cmd := exec.CommandContext(ctx, hostBinary, "--config="+locator)
	cmd.Env = []string{
		scenarioEnv + "=" + caseFirstMigration,
		roleEnv + "=" + role,
		rendezvousEnv + "=" + rendezvous,
	}
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	run := replicaRun{role: role}
	err := cmd.Run()
	run.stdout, run.stderr = stdout.String(), stderr.String()
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		run.exit = exit.ExitCode()
	default:
		run.startErr = err
	}
	return run
}

// refusedStatements are the records on a replica's standard output of a
// statement the database refused, the "statement failed" records, whatever
// classes they carry. A correct run refuses none: the migration mutex lets one
// replica apply the migration while the other waits, so a statement that fails
// is the collision the mutex is there to prevent. Without the mutex the failure
// is whichever the race produces, a duplicate key in the catalogue as often as
// an object that already exists, so no one SQLSTATE stands for it.
func refusedStatements(t *testing.T, run replicaRun) []dbsuite.Record {
	t.Helper()
	var refused []dbsuite.Record
	for _, record := range dbsuite.Records(t, run.role+"'s standard output", run.stdout) {
		if record.Message == "statement failed" {
			refused = append(refused, record)
		}
	}
	return refused
}

// divide sorts the replicas by what they reported, and fails when the pair did
// not come out as exactly one applier and one no-op.
func divide(t *testing.T, results []replicaRun) (applier, noop replicaRun) {
	t.Helper()
	var appliers, noops []replicaRun
	for _, result := range results {
		if result.exit != 0 {
			t.Errorf("%s exited %d, and both replicas have to start successfully:\nstdout:\n%s\nstderr:\n%s",
				result.role, result.exit, result.stdout, result.stderr)
		}
		// Each stream is read for what it carries of the collision. The driver's
		// report of it, the words "already exists", reaches standard error with
		// the startup failure it causes. The database module's logger writes
		// the failed statement to standard output, on the stream the markers
		// arrive on, as a "statement failed" record: this assembly names no
		// log module, so the records go through the process default logger,
		// which writes to standard output. A correct run has no such record,
		// so any one is the collision, whatever the classes it carries.
		if strings.Contains(result.stderr, "already exists") {
			t.Errorf("%s failed on an object that already exists, which is the collision the migration "+
				"mutex is there to prevent:\nstdout:\n%s\nstderr:\n%s",
				result.role, result.stdout, result.stderr)
		}
		if refused := refusedStatements(t, result); len(refused) > 0 {
			t.Errorf("%s had %d statement(s) refused by the database, and a correct run has none: a refused "+
				"statement is the collision the migration mutex is there to prevent, the first being %s\n"+
				"stdout:\n%s\nstderr:\n%s",
				result.role, len(refused), refused[0].Line, result.stdout, result.stderr)
		}
		switch {
		case result.marked(markApplied):
			appliers = append(appliers, result)
		case result.marked(markNoop):
			noops = append(noops, result)
		default:
			t.Errorf("%s printed neither %q nor %q, so it never finished its migration:\nstdout:\n%s\nstderr:\n%s",
				result.role, markApplied, markNoop, result.stdout, result.stderr)
		}
	}
	if len(appliers) != 1 || len(noops) != 1 {
		t.Fatalf("%d replicas applied the migration and %d found it already applied, and exactly one of "+
			"each is what the migration mutex has to produce", len(appliers), len(noops))
	}
	requireSlowMigrationRecord(t, appliers[0])
	return appliers[0], noops[0]
}

// requireSlowMigrationRecord asserts that the replica that applied the migration
// wrote the migration as a "slow statement" record on standard output, stamped
// with the module that ran it. The file holds the replica's session for the
// seconds it sleeps, far beyond the logger's threshold, so the record is there on
// every run.
//
// It is what gives the search for refused statements in divide something to
// find: a correct run has none, so a logger that wrote its records to another
// stream, or in another form, would leave that search reading nothing and a run
// that collided would look the same as one that did not.
func requireSlowMigrationRecord(t *testing.T, applier replicaRun) {
	t.Helper()
	for _, record := range dbsuite.Records(t, applier.role+"'s standard output", applier.stdout) {
		if record.Level == "WARN" && record.Message == "slow statement" &&
			record.Attrs["module"] == postgres.ModuleName && strings.Contains(record.Attrs["statement"], "pg_sleep") {
			return
		}
	}
	t.Errorf("%s applied the migration and left no \"slow statement\" record of it, stamped with module=%s, on "+
		"standard output, so the records this case reads are not where it reads them:\nstdout:\n%s",
		applier.role, postgres.ModuleName, applier.stdout)
}

// TestConcurrentFirstMigrationElectsOneApplier is the hard gate on the
// cross-process mutex, seen from a real assembly: two hosts, each importing both
// implementations and configured on PostgreSQL, go for the same empty database at
// the same moment after meeting at a rendezvous.
//
// Exactly one of them has to apply the migration, and the other has to find the
// work already done — not fail on an object that already exists, which is what
// happens with no mutex and is the failure a real deployment hits on its first
// multi-replica start.
func TestConcurrentFirstMigrationElectsOneApplier(t *testing.T) {
	applier, noop := divide(t, runReplicas(t))
	t.Logf("%s applied the migration and %s found it already applied", applier.role, noop.role)
}

// TestConcurrentFirstMigrationProvesTheWindowOverlapped asserts, in the same run
// the division is read from, that the two replicas were in flight together.
//
// Without it the gate above is empty rather than merely weak. The fixture only
// guarantees that the applying replica is slow; nothing guarantees that the
// other one was there while it was working. A cold stack, the second
// integration leg, a slow exec — any of them make the second replica late, and a late
// replica reads a complete record table and prints noop whether there is a mutex
// or not.
func TestConcurrentFirstMigrationProvesTheWindowOverlapped(t *testing.T) {
	results := runReplicas(t)
	applier, noop := divide(t, results)

	startedWaiting, err := noop.at(markStart)
	if err != nil {
		t.Fatalf("reading when %s reached its migration stage: %v", noop.role, err)
	}
	finishedApplying, err := applier.at(markApplied)
	if err != nil {
		t.Fatalf("reading when %s finished applying: %v", applier.role, err)
	}
	if !startedWaiting.Before(finishedApplying) {
		t.Fatalf("%s only reached its migration stage at %s, after %s had finished applying at %s: the two "+
			"never overlapped, so this run proves nothing about the mutex",
			noop.role, startedWaiting.Format(time.RFC3339Nano),
			applier.role, finishedApplying.Format(time.RFC3339Nano))
	}

	// The window has to be the one the fixture put there. A migration that no
	// longer sleeps leaves the replicas racing over milliseconds, and the
	// assertion above would then hold or fail by luck rather than by the mutex.
	startedApplying, err := applier.at(markStart)
	if err != nil {
		t.Fatalf("reading when %s reached its migration stage: %v", applier.role, err)
	}
	if window := finishedApplying.Sub(startedApplying); window < applyDuration {
		t.Fatalf("%s was inside its migration for %s, and the migration is written to hold it for %s: "+
			"the window this case needs is not there", applier.role, window, applyDuration)
	}
}
