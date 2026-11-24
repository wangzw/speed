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

// TestARefusedConnectionIsReportedOnceOnTheDiagnosticStream starts on a server that refuses the
// login.
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

const applyDuration = 3 * time.Second

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

func startReplicas(t *testing.T) []replicaRun {
	t.Helper()
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

func replicaConfig(t *testing.T, dsn, role string) string {
	t.Helper()
	return writeConfig(t, dbConfig{DB: dbSection{Postgres: &engineSection{
		DSN:                  dsn + "&application_name=" + role,
		MaxOpenConns:         1,
		MigrationLockTimeout: "1m",
	}}})
}

type replicaRun struct {
	role     string
	exit     int
	stdout   string
	stderr   string
	startErr error
}

func (r replicaRun) marked(name string) bool {
	_, err := r.at(name)
	return err == nil
}

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

func divide(t *testing.T, results []replicaRun) (applier, noop replicaRun) {
	t.Helper()
	var appliers, noops []replicaRun
	for _, result := range results {
		if result.exit != 0 {
			t.Errorf("%s exited %d, and both replicas have to start successfully:\nstdout:\n%s\nstderr:\n%s",
				result.role, result.exit, result.stdout, result.stderr)
		}
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

// TestConcurrentFirstMigrationElectsOneApplier checks that two hosts starting at once against an
// empty PostgreSQL database elect one applier.
func TestConcurrentFirstMigrationElectsOneApplier(t *testing.T) {
	applier, noop := divide(t, runReplicas(t))
	t.Logf("%s applied the migration and %s found it already applied", applier.role, noop.role)
}

// TestConcurrentFirstMigrationProvesTheWindowOverlapped asserts, in the same run the division is
// read from, that the two replicas were in flight together.
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
