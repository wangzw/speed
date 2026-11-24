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

	"github.com/vislake/speed/pkg/log"
)

var hostBinary string

const runLimit = 60 * time.Second

// TestMain builds the loghost binary once and runs the end-to-end cases against it.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "loghost")
	if err != nil {
		fmt.Fprintln(os.Stderr, "creating the build directory failed:", err)
		os.Exit(1)
	}
	hostBinary = filepath.Join(dir, "loghost")
	if out, err := exec.Command("go", "build", "-o", hostBinary, ".").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building the host failed: %v\n%s", err, out)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type outcome struct {
	stdout string
	stderr string
	code   int
}

type logConfig struct {
	Log logSection `json:"log"`
}

type logSection struct {
	Outputs []logOutput `json:"outputs"`
}

type logOutput struct {
	To     string `json:"to"`
	Format string `json:"format"`
	Path   string `json:"path,omitempty"`
}

func writeConfig(t *testing.T, doc any) string {
	t.Helper()
	body, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshalling the config document failed: %v", err)
	}
	path := filepath.Join(t.TempDir(), "loghost.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("writing the config document failed: %v", err)
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}

func runHost(t *testing.T, scenarioName string, args ...string) outcome {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), runLimit)
	defer cancel()

	cmd := exec.CommandContext(ctx, hostBinary, args...)
	cmd.Env = []string{scenarioEnv + "=" + scenarioName}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

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
		t.Fatalf("the host left with status %d\nstdout:\n%s\nstderr:\n%s",
			got.code, got.stdout, got.stderr)
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

func readFileConfigured(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path) //nolint:gosec // the path is the one this case just handed the host.
	if err != nil {
		t.Fatalf("reading the configured destination failed: %v", err)
	}
	return string(body)
}

// TestAbsentLogSectionYieldsOneStdoutTextOutputAtInfo checks that a host with no log section writes
// one text record to stdout at info, drops the debug one and masks the sensitive value.
func TestAbsentLogSectionYieldsOneStdoutTextOutputAtInfo(t *testing.T) {
	locator := writeConfig(t, struct{}{})
	got := runHost(t, caseDefault, "--config="+locator)
	requireSuccess(t, got)

	if n := countOccurrences(got.stdout, "msg="+infoMessage); n != 1 {
		t.Errorf("the Info record appears %d times on stdout, want 1:\n%s", n, got.stdout)
	}
	if strings.Contains(got.stdout, `"msg"`) {
		t.Errorf("stdout carries JSON records although the default format is text:\n%s", got.stdout)
	}
	if !strings.Contains(got.stdout, "level=INFO") {
		t.Errorf("stdout carries no text-rendered level:\n%s", got.stdout)
	}
	if strings.Contains(got.stdout, debugMessage) {
		t.Errorf("the Debug record reached stdout although the default level is info:\n%s", got.stdout)
	}
	if strings.Contains(got.stdout, plaintextValue) {
		t.Errorf("the sensitive value reached stdout in plaintext:\n%s", got.stdout)
	}
	if !strings.Contains(got.stdout, sensitiveKey+"="+log.MaskedText) {
		t.Errorf("stdout carries no masked value under %q:\n%s", sensitiveKey, got.stdout)
	}
}

func fallbackRun(t *testing.T) (outcome, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "app.log")
	locator := writeConfig(t, logConfig{Log: logSection{Outputs: []logOutput{
		{To: "file", Format: "json", Path: path},
	}}})
	got := runHost(t, caseFallback, "--config="+locator)
	requireSuccess(t, got)
	return got, path
}

// TestFallbackDoesNotReachTheConfiguredFile checks that a record written with no logger in context
// does not reach the configured file.
func TestFallbackDoesNotReachTheConfiguredFile(t *testing.T) {
	got, path := fallbackRun(t)

	if n := countOccurrences(readFileConfigured(t, path), orphanMessage); n != 0 {
		t.Errorf("the record written with no logger in context reached the configured file %d times\nstdout:\n%s",
			n, got.stdout)
	}
}

// TestFallbackAppearsOnStdout checks that a record written with no logger in context appears once
// on stdout.
func TestFallbackAppearsOnStdout(t *testing.T) {
	got, _ := fallbackRun(t)

	if n := countOccurrences(got.stdout, orphanMessage); n != 1 {
		t.Errorf("the record written with no logger in context appears %d times on stdout, want 1:\n%s",
			n, got.stdout)
	}
}

// TestNamedLoggerReachesTheConfiguredFile checks that an injected named logger writes its record
// once to the configured file with the module name and not to stdout.
func TestNamedLoggerReachesTheConfiguredFile(t *testing.T) {
	got, path := fallbackRun(t)
	body := readFileConfigured(t, path)

	if n := countOccurrences(body, injectedMessage); n != 1 {
		t.Errorf("the injected logger's record appears %d times in the configured file, want 1:\n%s",
			n, body)
	}
	if !strings.Contains(body, `"`+log.ModuleAttrKey+`":"`+appModuleName+`"`) {
		t.Errorf("the configured file carries no record from the named logger:\n%s", body)
	}
	if n := countOccurrences(got.stdout, injectedMessage); n != 0 {
		t.Errorf("the injected logger's record also reached stdout %d times, want 0:\n%s",
			n, got.stdout)
	}
}

// TestExplicitEmptyOutputsWritesOneNotice checks that an explicit empty output list writes one
// log-module notice to stderr and no record anywhere.
func TestExplicitEmptyOutputsWritesOneNotice(t *testing.T) {
	locator := writeConfig(t, logConfig{Log: logSection{Outputs: []logOutput{}}})
	got := runHost(t, caseEmptyOutputs, "--config="+locator)
	requireSuccess(t, got)

	if n := countOccurrences(got.stderr, "log: "); n != 1 {
		t.Errorf("the startup wrote %d notices from the log module, want exactly 1:\n%s",
			n, got.stderr)
	}
	if strings.Contains(got.stdout, silencedMessage) {
		t.Errorf("a record reached stdout although the output list is empty:\n%s", got.stdout)
	}
	if strings.Contains(got.stderr, silencedMessage) {
		t.Errorf("a record reached stderr although the output list is empty:\n%s", got.stderr)
	}
}

// TestSecondEnabledLoggerProviderFailsTheStartup checks that two enabled providers of the logging
// capability fail the startup with the exclusive marker and the rival's name.
func TestSecondEnabledLoggerProviderFailsTheStartup(t *testing.T) {
	got := runHost(t, caseRivalProvider)

	if got.code == 0 {
		t.Fatalf("the startup succeeded with two enabled providers of the logging capability\nstdout:\n%s\nstderr:\n%s",
			got.stdout, got.stderr)
	}
	if !strings.Contains(got.stdout, exclusiveMarker) {
		t.Errorf("the startup failed for another reason than two enabled providers\nstdout:\n%s\nstderr:\n%s",
			got.stdout, got.stderr)
	}
	if !strings.Contains(got.stderr, rivalModuleName) {
		t.Errorf("the failure does not name the rival provider:\n%s", got.stderr)
	}
}

// TestConcurrentDefaultAndConfiguredWritesDoNotInterleave checks that interleaved writes through the
// bootstrap chain and a JSON chain arrive as whole lines of their own shape.
func TestConcurrentDefaultAndConfiguredWritesDoNotInterleave(t *testing.T) {
	locator := writeConfig(t, logConfig{Log: logSection{Outputs: []logOutput{
		{To: "stdout", Format: "json"},
	}}})
	got := runHost(t, caseConcurrent, "--config="+locator)
	requireSuccess(t, got)

	lines := strings.Split(strings.TrimRight(got.stdout, "\n"), "\n")
	if len(lines) != 2*concurrentRecords {
		t.Errorf("stdout carries %d lines, want %d", len(lines), 2*concurrentRecords)
	}
	payload := strings.Repeat("x", payloadSize)
	for i, line := range lines {
		if strings.HasPrefix(line, "{") {
			var record struct {
				Msg     string `json:"msg"`
				Payload string `json:"payload"`
			}
			if err := json.Unmarshal([]byte(line), &record); err != nil {
				t.Fatalf("line %d is not a whole JSON record: %v\n%.200s", i, err, line)
			}
			if record.Msg != chainMessage || record.Payload != payload {
				t.Fatalf("line %d is a JSON record with the wrong content: msg=%q payload length %d",
					i, record.Msg, len(record.Payload))
			}
			continue
		}
		if !strings.HasPrefix(line, "time=") || !strings.HasSuffix(line, "payload="+payload) {
			t.Fatalf("line %d is not a whole text record:\n%.200s", i, line)
		}
		if !strings.Contains(line, "msg="+bootstrapMessage) {
			t.Fatalf("line %d is a text record with the wrong content:\n%.200s", i, line)
		}
	}
}
