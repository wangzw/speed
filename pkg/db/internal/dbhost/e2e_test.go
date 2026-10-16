package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The cases run the host as a real child process. What they have to prove is
// what a real assembly does: which of two imported implementations the
// configuration selects, what a startup that cannot be served reports, whether a
// dependant finds the serializer in place by the time it is constructed, and
// whether two replicas starting at once against an empty database divide into
// one applier and one no-op. None of that exists inside a test binary, and a
// real assembly cannot be started in one at all — the configuration loader reads
// os.Args[1:], which go test has already filled with arguments of its own.

// hostBinary is the compiled host every case runs. It is built once, in
// TestMain, because building it is by far the slowest thing here.
var hostBinary string

// runLimit bounds one run of the host. It is generous: exceeding it means the
// host hung — a pool of one connection with an extra connection somewhere in the
// run, a mutex that is never acquired — and the case says so with both streams
// attached.
const runLimit = 90 * time.Second

// buildLimit bounds the build of the host binary, which compiles this module's
// dependencies on a cold cache.
const buildLimit = 5 * time.Minute

func TestMain(m *testing.M) {
	built, err := buildHost()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(built)
	os.Exit(code)
}

// buildHost compiles the host into a directory of its own and reports where it
// put it.
func buildHost() (string, error) {
	dir, err := os.MkdirTemp("", "dbhost")
	if err != nil {
		return "", fmt.Errorf("creating the build directory failed: %w", err)
	}
	hostBinary = filepath.Join(dir, "dbhost")
	ctx, cancel := context.WithTimeout(context.Background(), buildLimit)
	defer cancel()
	out, err := exec.CommandContext(ctx, "go", "build", "-o", hostBinary, ".").CombinedOutput()
	if err != nil {
		os.RemoveAll(dir)
		return "", fmt.Errorf("building the host failed: %w\n%s", err, out)
	}
	return dir, nil
}

// outcome is what one run of the host produced.
type outcome struct {
	stdout string
	stderr string
	code   int
}

// dbConfig is the configuration document a case writes, in the shape the
// implementations' schemas declare: each mounts its items under a namespace of
// its own, and a namespace with a dot in it is nested one level per segment.
type dbConfig struct {
	DB dbSection `json:"db"`
}

type dbSection struct {
	Postgres *engineSection `json:"postgres,omitempty"`
	SQLite   *engineSection `json:"sqlite,omitempty"`
}

// engineSection is one implementation's section. The keys are flat inside it,
// which is what an inlined carrier struct decodes from.
type engineSection struct {
	DSN string `json:"dsn,omitempty"`
	// MaxOpenConns is left out unless a case sets it, so that the default is
	// what most cases run with.
	MaxOpenConns int `json:"max-open-conns,omitempty"`
	// EncryptionKey is left out unless a case needs the encryption paths.
	EncryptionKey string `json:"encryption-key,omitempty"`
	// MigrationLockTimeout is declared by an implementation that supplies a
	// mutex, and setting it on one that does not is a startup failure. Only
	// the PostgreSQL cases set it.
	MigrationLockTimeout string `json:"migration-lock-timeout,omitempty"`
}

// writeConfig writes a configuration document and returns the locator the file
// transport serves it under.
func writeConfig(t *testing.T, doc any) string {
	t.Helper()
	body, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshalling the config document failed: %v", err)
	}
	path := filepath.Join(t.TempDir(), "dbhost.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("writing the config document failed: %v", err)
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}

// runHost runs one scenario to completion.
//
// The environment is exactly what the case gives and never the ambient one: a
// SPEED_DB_E2E_* or DBHOST_* variable on the machine running the tests would
// otherwise change what a case observes.
func runHost(t *testing.T, scenarioName string, args ...string) outcome {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), runLimit)
	defer cancel()

	cmd := exec.CommandContext(ctx, hostBinary, args...)
	cmd.Env = []string{scenarioEnv + "=" + scenarioName}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	code := 0
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("running the host failed: %v\nstdout:\n%s\nstderr:\n%s",
				err, stdout.String(), stderr.String())
		}
		code = exit.ExitCode()
	}
	return outcome{stdout: stdout.String(), stderr: stderr.String(), code: code}
}

// requireSuccess asserts that the run left cleanly, printing both streams when
// it did not.
func requireSuccess(t *testing.T, got outcome) {
	t.Helper()
	if got.code != 0 {
		t.Fatalf("the host left with status %d\nstdout:\n%s\nstderr:\n%s", got.code, got.stdout, got.stderr)
	}
}

// countOccurrences reports how many lines of a stream carry a piece of text.
func countOccurrences(stream, text string) int {
	n := 0
	for line := range strings.SplitSeq(strings.TrimRight(stream, "\n"), "\n") {
		if strings.Contains(line, text) {
			n++
		}
	}
	return n
}

// requireNoMentionOf asserts that no diagnostic line names the module.
//
// An engine the command never took in has no diagnostic line: it took no
// stance, so it is not in the enabled set the diagnostics report, and leaving it
// out was the host's own writing rather than a surprise the diagnostics exist to
// catch. The failure text a startup ends with is a different stream, and it does
// name the implementations claiming the capability exclusively — that is how the
// host learns which ones it has to choose between.
func requireNoMentionOf(t *testing.T, got outcome, module string) {
	t.Helper()
	want := `module "` + module + `"`
	if strings.Contains(got.stderr, want) || strings.Contains(got.stdout, want) {
		t.Errorf("the run names %s, which the command never took in:\nstdout:\n%s\nstderr:\n%s",
			module, got.stdout, got.stderr)
	}
}

// The case that needs no engine of its own: an encrypted field written and read
// back in a process that has done nothing else with the database.

func TestSerializerIsRegisteredBeforeDelivery(t *testing.T) {
	locator := writeConfig(t, dbConfig{DB: dbSection{SQLite: &engineSection{
		DSN:           filepath.Join(t.TempDir(), "dbhost.sqlite"),
		EncryptionKey: "dbhost-e2e-root-key",
	}}})
	got := runHost(t, caseSerializerRoundTrip, "--config="+locator)
	requireSuccess(t, got)

	if n := countOccurrences(got.stdout, roundTripMarker); n != 1 {
		t.Errorf("the encrypted field made the round trip %d times, want 1:\n%s", n, got.stdout)
	}
	// The mutation this case exists for reports itself as GORM refusing to
	// resolve the serializer, so the text is asserted here as well: an
	// implementation that registered it after delivering the handle would
	// otherwise fail for a reason the case does not name.
	if strings.Contains(got.stderr, "invalid serializer type") {
		t.Errorf("the serializer was not registered by the time the dependant was constructed:\n%s", got.stderr)
	}
}

// The two cases about one import pair: both implementations are carried, and the
// command tree names which of them runs.

func TestBothImplementationsImportedOneConfigured(t *testing.T) {
	locator := writeConfig(t, dbConfig{DB: dbSection{SQLite: &engineSection{
		DSN: filepath.Join(t.TempDir(), "dbhost.sqlite"),
	}}})
	got := runHost(t, caseOneEngineConfigured, "--config="+locator)
	requireSuccess(t, got)

	// The end-to-end form of what separate namespaces are for. Both
	// implementations declare their input items, and the manifest is collected
	// from every registered module, the ones this run does not enable included:
	// had the two shared a namespace they would have conflicted while it was
	// being collected, before exclusivity was ever resolved, and this run would
	// have died there rather than starting on the engine the command named.
	//
	// The engine the command did not name is in neither half of the output. It
	// was not taken in, so it took no stance and is not in the enabled set the
	// diagnostics report — and being left out of the command is the host's own
	// writing rather than a surprise, so it is not named there for that reason
	// either.
	requireNoMentionOf(t, got, "db.postgres")
	// The other half: the configured engine has to be the one that ran. A run
	// that named no engine would carry the same silence above and fail the
	// dependant's requirement instead, so a clean exit is what says the command
	// named the engine it configured.
	if countOccurrences(got.stderr, `module "db.sqlite"`) != 0 {
		t.Errorf("the configured implementation is reported as not running:\n%s", got.stderr)
	}
}

func TestBothConfiguredIsExclusiveViolated(t *testing.T) {
	// Neither locator has to name a reachable database: two enabled providers
	// are settled during resolution, and no connection is opened before that.
	locator := writeConfig(t, dbConfig{DB: dbSection{
		Postgres: &engineSection{DSN: "postgres://dbhost.invalid:5432/none"},
		SQLite:   &engineSection{DSN: filepath.Join(t.TempDir(), "unused.sqlite")},
	}})
	got := runHost(t, caseBothEnginesConfigured, "--config="+locator)

	if got.code == 0 {
		t.Fatalf("the startup succeeded with both implementations configured and each declaring the "+
			"capability exclusively\nstdout:\n%s\nstderr:\n%s", got.stdout, got.stderr)
	}
	// errors.Is does not survive the process boundary, so the host judged the
	// error itself and printed a fixed word.
	if !strings.Contains(got.stdout, exclusiveViolatedMarker) {
		t.Errorf("the startup failed for another reason than two enabled providers\nstdout:\n%s\nstderr:\n%s",
			got.stdout, got.stderr)
	}
	for _, module := range []string{"db.postgres", "db.sqlite"} {
		if !strings.Contains(got.stderr, module) {
			t.Errorf("the failure does not name %s, and both providers are what has to be settled:\n%s",
				module, got.stderr)
		}
	}
}

func TestNoEngineNamedIsMissingProvider(t *testing.T) {
	// A document that carries no database section at all, which is a legal
	// assembly, beside a command that names no engine: whether a run needs a
	// database is the run's decision, and this one's dependant makes it.
	locator := writeConfig(t, struct{}{})
	got := runHost(t, caseNoEngineConfigured, "--config="+locator)

	if got.code == 0 {
		t.Fatalf("the startup succeeded although a module required the database capability and the "+
			"command named no engine to deliver it\nstdout:\n%s\nstderr:\n%s", got.stdout, got.stderr)
	}
	if !strings.Contains(got.stdout, missingProviderMarker) {
		t.Errorf("the startup failed for another reason than an unmet requirement\nstdout:\n%s\nstderr:\n%s",
			got.stdout, got.stderr)
	}
	// Neither engine is in the command, so neither takes a stance: the failure
	// is the requirement with no provider, and the command tree is what says
	// what is absent rather than a diagnostic line about an engine that was
	// never asked to run.
	requireNoMentionOf(t, got, "db.sqlite")
	requireNoMentionOf(t, got, "db.postgres")
}

// The case about a command whose depth is not the serving stage.

// TestTheMigrateCommandStopsAtTheMigrationStage is the end-to-end form of a
// command that drives part of the lifecycle: `dbhost migrate` applies the
// migrations and returns, where the root command of the same binary would go
// on to serve.
//
// Two observations, because either on its own is met by the wrong
// implementation. The record table says the migrations were applied — a
// command that stopped short of the migration stage would leave it empty — and
// the absent serving marker says the run went no further. A depth of the
// serving stage applies exactly the same migrations, and the probe would then
// cancel the run from its own Serve, so the exit status is 0 either way and
// says nothing at all.
//
// It runs on SQLite, which needs no container: the depth belongs to the
// command tree rather than to an engine, and a case gated on docker would be
// unobserved wherever docker is absent.
func TestTheMigrateCommandStopsAtTheMigrationStage(t *testing.T) {
	locator := writeConfig(t, dbConfig{DB: dbSection{SQLite: &engineSection{
		DSN: filepath.Join(t.TempDir(), "dbhost.sqlite"),
	}}})
	got := runHost(t, caseMigrateSubcommand, "migrate", "--config="+locator)
	requireSuccess(t, got)

	applied := recordedMarker + "\t" + probeModuleName + "\t0001_widgets.sql"
	if n := countOccurrences(got.stdout, applied); n != 1 {
		t.Errorf("the record table reports the declared migration %d times, want 1:\n%s", n, got.stdout)
	}
	if strings.Contains(got.stdout, servedMarker) {
		t.Errorf("the run reached the serving stage, so the migrate command drove past the depth it "+
			"declares:\n%s", got.stdout)
	}
}
