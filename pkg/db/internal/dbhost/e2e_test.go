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

var hostBinary string

// runLimit bounds one run of the host. It is generous: exceeding it means the host hung, and the case says so with both streams attached.
const runLimit = 90 * time.Second

const buildLimit = 5 * time.Minute

// TestMain builds the host binary every case drives and removes it afterwards.
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

type outcome struct {
	stdout string
	stderr string
	code   int
}

type dbConfig struct {
	DB dbSection `json:"db"`
}

type dbSection struct {
	Postgres *engineSection `json:"postgres,omitempty"`
	SQLite   *engineSection `json:"sqlite,omitempty"`
}

type engineSection struct {
	DSN           string `json:"dsn,omitempty"`
	MaxOpenConns  int    `json:"max-open-conns,omitempty"`
	EncryptionKey string `json:"encryption-key,omitempty"`
	// MigrationLockTimeout is declared by an implementation that supplies a mutex, and setting it on one that does not is a startup failure; only the PostgreSQL cases set it.
	MigrationLockTimeout string `json:"migration-lock-timeout,omitempty"`
}

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

// runHost runs one scenario to completion. The environment is exactly what the case gives and never the ambient one: a SPEED_DB_E2E_* or DBHOST_* variable on the machine would otherwise change what a case observes.
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

func requireSuccess(t *testing.T, got outcome) {
	t.Helper()
	if got.code != 0 {
		t.Fatalf("the host left with status %d\nstdout:\n%s\nstderr:\n%s", got.code, got.stdout, got.stderr)
	}
}

func countOccurrences(stream, text string) int {
	n := 0
	for line := range strings.SplitSeq(strings.TrimRight(stream, "\n"), "\n") {
		if strings.Contains(line, text) {
			n++
		}
	}
	return n
}

func requireNoMentionOf(t *testing.T, got outcome, module string) {
	t.Helper()
	want := `module "` + module + `"`
	if strings.Contains(got.stderr, want) || strings.Contains(got.stdout, want) {
		t.Errorf("the run names %s, which the command never took in:\nstdout:\n%s\nstderr:\n%s",
			module, got.stdout, got.stderr)
	}
}

// TestSerializerIsRegisteredBeforeDelivery checks that an encrypted field written and read back in
// a fresh process was sealed by a serializer registered before the handle was delivered.
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
	// The mutation this case exists for reports itself as GORM refusing to resolve the serializer, so the text is asserted too: an implementation that registered it after delivering the handle would otherwise fail unnamed.
	if strings.Contains(got.stderr, "invalid serializer type") {
		t.Errorf("the serializer was not registered by the time the dependant was constructed:\n%s", got.stderr)
	}
}

// TestBothImplementationsImportedOneConfigured checks that carrying both implementations and
// configuring one starts on the configured engine.
func TestBothImplementationsImportedOneConfigured(t *testing.T) {
	locator := writeConfig(t, dbConfig{DB: dbSection{SQLite: &engineSection{
		DSN: filepath.Join(t.TempDir(), "dbhost.sqlite"),
	}}})
	got := runHost(t, caseOneEngineConfigured, "--config="+locator)
	requireSuccess(t, got)

	requireNoMentionOf(t, got, "db.postgres")
	// The configured engine has to be the one that ran: a clean exit is what says the command named the engine it configured, rather than meeting the dependant's requirement.
	if countOccurrences(got.stderr, `module "db.sqlite"`) != 0 {
		t.Errorf("the configured implementation is reported as not running:\n%s", got.stderr)
	}
}

// TestBothConfiguredIsExclusiveViolated checks that configuring both engines fails the startup as
// an exclusive violation naming both.
func TestBothConfiguredIsExclusiveViolated(t *testing.T) {
	// Neither locator has to name a reachable database: two enabled providers are settled during resolution, and no connection is opened before that.
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

// TestNoEngineNamedIsMissingProvider checks that a command naming no engine fails the dependant's
// requirement as a missing provider.
func TestNoEngineNamedIsMissingProvider(t *testing.T) {
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
	// Neither engine is in the command, so neither takes a stance: the failure is the requirement with no provider, and the command tree rather than a diagnostic names what is absent.
	requireNoMentionOf(t, got, "db.sqlite")
	requireNoMentionOf(t, got, "db.postgres")
}

// TestTheMigrateCommandStopsAtTheMigrationStage is the end-to-end form of a command that drives
// part of the lifecycle: `dbhost migrate` applies the migrations and returns.
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
