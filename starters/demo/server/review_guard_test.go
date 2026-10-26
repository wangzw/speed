//go:build review

package main

// The review build's loopback guard: the address classification, the refusal
// the guard states, and the run that carries it.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

// stubReader answers Decode for the guard's one path with the listener set the
// case gives.
type stubReader struct {
	listeners map[string]reviewListenerConfig
	err       error
}

func (r stubReader) Decode(path string, target any) error {
	if r.err != nil {
		return r.err
	}
	if path != reviewHTTPConfigNamespace {
		return fmt.Errorf("unexpected path %q", path)
	}
	cfg, ok := target.(*reviewHTTPConfig)
	if !ok {
		return fmt.Errorf("unexpected target %T", target)
	}
	cfg.Listeners = r.listeners
	return nil
}

// TestReviewLoopbackAddresses pins the classification: an IP literal is asked
// whether it is loopback, localhost is the conventional loopback name, and a
// name that merely starts with localhost is not it. An empty host is every
// interface, and a value that is not host:port is refused.
func TestReviewLoopbackAddresses(t *testing.T) {
	cases := []struct {
		address string
		want    bool
	}{
		{"127.0.0.1:8080", true},
		{"127.0.0.2:8080", true},
		{"[::1]:8080", true},
		{"localhost:8080", true},
		{"LOCALHOST:8080", true},
		{":8080", false},
		{"0.0.0.0:8080", false},
		{"[::]:8080", false},
		{"192.168.1.10:8080", false},
		{"example.com:8080", false},
		{"localhost.evil.test:8080", false},
		{"localhostx:8080", false},
		{"localhost.:8080", false},
		{"127.0.0.1", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := loopbackAddress(tc.address); got != tc.want {
			t.Errorf("loopbackAddress(%q) = %v, want %v", tc.address, got, tc.want)
		}
	}
}

// TestReviewGuardRefusesANonLoopbackListener holds the refusal: one listener off
// loopback fails the guard, and a set of loopback listeners passes.
func TestReviewGuardRefusesANonLoopbackListener(t *testing.T) {
	loopback := stubReader{listeners: map[string]reviewListenerConfig{
		"backend": {Address: "127.0.0.1:8080"},
		"ops":     {Address: "[::1]:8083"},
	}}
	if err := checkReviewListeners(loopback); err != nil {
		t.Fatalf("the loopback set was refused: %v", err)
	}

	exposed := stubReader{listeners: map[string]reviewListenerConfig{
		"backend": {Address: "127.0.0.1:8080"},
		"ops":     {Address: "0.0.0.0:8083"},
	}}
	err := checkReviewListeners(exposed)
	if err == nil {
		t.Fatalf("a listener off loopback was accepted")
	}
	if !strings.Contains(err.Error(), "loopback") {
		t.Errorf("the refusal does not name the loopback rule: %v", err)
	}

	unreadable := stubReader{err: errors.New("the configuration is unreadable")}
	if err := checkReviewListeners(unreadable); err == nil {
		t.Errorf("an unreadable configuration was accepted")
	}
}

// TestReviewGuardCheckIsDeterministic holds the refusal over several listeners
// in name order, every loopback one ahead of the exposed ones. The guard sorts
// the names, so a check that returned nil on meeting the first loopback
// listener instead of continuing fails here on every run; map iteration order
// no longer decides whether such a mutation is noticed. The named listener is
// pinned too: the first exposed one in name order is the one reported.
func TestReviewGuardCheckIsDeterministic(t *testing.T) {
	reader := stubReader{listeners: map[string]reviewListenerConfig{
		"a-loopback": {Address: "127.0.0.1:8080"},
		"b-loopback": {Address: "[::1]:8081"},
		"y-exposed":  {Address: "0.0.0.0:8082"},
		"z-exposed":  {Address: "192.168.1.10:8083"},
	}}
	err := checkReviewListeners(reader)
	if err == nil {
		t.Fatalf("a set holding exposed listeners was accepted")
	}
	if !strings.Contains(err.Error(), `"y-exposed"`) {
		t.Errorf("the refusal does not name the first exposed listener in name order: %v", err)
	}
}

// TestReviewPrepareChecksTheListeners holds the guard's own path: Prepare
// refuses a non-loopback listener and enables the module only for a loopback
// set. A build whose Prepare skipped the check fails here without the binary
// case.
func TestReviewPrepareChecksTheListeners(t *testing.T) {
	exposed := reviewRegistry(t, stubReader{listeners: map[string]reviewListenerConfig{
		"backend": {Address: "0.0.0.0:8080"},
	}})
	if _, err := reviewGuardModule().Prepare(context.Background(), exposed); err == nil {
		t.Fatalf("Prepare accepted a listener off loopback")
	}

	loopback := reviewRegistry(t, stubReader{listeners: map[string]reviewListenerConfig{
		"backend": {Address: "127.0.0.1:8080"},
	}})
	enablement, err := reviewGuardModule().Prepare(context.Background(), loopback)
	if err != nil {
		t.Fatalf("Prepare refused a loopback listener: %v", err)
	}
	if enablement.State != core.StateEnabled {
		t.Errorf("Prepare answered state %v, want enabled", enablement.State)
	}
}

// reviewRegistry is a registry whose only capability is the configuration
// reader the guard takes up.
func reviewRegistry(t *testing.T, reader stubReader) *core.Registry {
	t.Helper()
	reg := core.New()
	reg.Register(core.Module{
		Name:     "test.review.config",
		Provides: []core.Provision{{Token: (*config.Reader)(nil)}},
	})
	reg.Supply(reader, "test.review.config")
	return reg
}

// reviewBinary builds the review assembly into a directory of its own.
func reviewBinary(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "demo-review")
	build := exec.Command("go", "build", "-tags", "review", "-o", binary, ".")
	build.Env = childEnv()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the review binary: %v\n%s", err, out)
	}
	return binary
}

// reviewConfig writes the review configuration with the listener addresses a
// case chooses and with its relative paths under a directory of the case's own:
// the file's own values would bind fixed ports and write into the source tree.
// The token signing key is minted at the path the written file names.
func reviewConfig(t *testing.T, backendAddress, opsAddress string) string {
	t.Helper()
	root := t.TempDir()
	body := reviewConfigBody(t, root, backendAddress, opsAddress,
		filepath.Join(root, "review.db"))
	path := filepath.Join(root, "review.yaml")
	if writeErr := os.WriteFile(path, []byte(body), 0o600); writeErr != nil {
		t.Fatalf("writing the configuration: %v", writeErr)
	}
	return path
}

// reviewConfigBody returns the review configuration with the listener addresses
// a case chooses, its token signing key and object store under root, and the
// database locator the caller gives. It is the review assembly's own file —
// server/review/sqlite.yaml — because every module the review tag adds reads its
// configuration from it: a case that runs that assembly over a baseline template
// starts modules whose required items have no value.
func reviewConfigBody(t *testing.T, root, backendAddress, opsAddress, dsn string) string {
	t.Helper()
	review, err := os.ReadFile(filepath.Join("review", "sqlite.yaml"))
	if err != nil {
		t.Fatalf("reading the review configuration: %v", err)
	}
	key := filepath.Join(root, "token-signing-key.pem")
	if err := ensureSigningKey(key); err != nil {
		t.Fatalf("minting the case's token signing key: %v", err)
	}
	body := string(review)
	for _, edit := range [][2]string{
		{`address: "127.0.0.1:8080"`, fmt.Sprintf("address: %q", backendAddress)},
		{`address: "127.0.0.1:8083"`, fmt.Sprintf("address: %q", opsAddress)},
		// The needles carry the YAML key: the file's header comments name the
		// same paths, and replacing the first occurrence would leave the
		// configured locator pointing into the source tree.
		{`dsn: "./var/review.db"`, fmt.Sprintf("dsn: %q", dsn)},
		{`signing-key-file: var/token-signing-key.pem`, fmt.Sprintf("signing-key-file: %q", key)},
		{`root: "./var/objects"`, fmt.Sprintf("root: %q", filepath.Join(root, "objects"))},
	} {
		if !strings.Contains(body, edit[0]) {
			t.Fatalf("the review configuration holds no %q to replace", edit[0])
		}
		body = strings.Replace(body, edit[0], edit[1], 1)
	}
	return body
}

// seedReview runs the seed command of the review binary over the configuration
// the case wrote, so the run that follows finds the rows this build needs.
func seedReview(t *testing.T, binary, config string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), commandLimit)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--config", config, "sqlite", seedCommand)
	cmd.Env = childEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("seeding the review configuration: %v\n%s", err, out)
	}
}

// startReview runs the review build's serve command the way demo_test's start
// runs the baseline one.
func startReview(t *testing.T, binary, config string) *process {
	t.Helper()
	cmd := exec.Command(binary, "--config", config, "sqlite", serveCommand)
	cmd.Env = childEnv()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("opening the review child's standard output: %v", err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the review build: %v", err)
	}
	p := &process{cmd: cmd, ready: make(chan struct{}), done: make(chan struct{})}
	go p.consume(stdout)
	go func() {
		p.waitErr = cmd.Wait()
		close(p.done)
	}()
	t.Cleanup(func() {
		p.stop(t)
		if strings.Contains(stderr.String(), raceWarning) {
			t.Errorf("the review build reported a data race:\n%s", stderr.String())
		}
	})
	return p
}

// TestReviewRunRefusesANonLoopbackListener builds the review binary and runs
// it over the review configuration with the backend listener moved off
// loopback: the startup fails with the guard's message, and no socket is
// served.
func TestReviewRunRefusesANonLoopbackListener(t *testing.T) {
	binary := reviewBinary(t)
	ports := freePorts(t, listenerCount)
	path := reviewConfig(t,
		fmt.Sprintf("0.0.0.0:%d", ports[backendListener]),
		fmt.Sprintf("127.0.0.1:%d", ports[opsListener]))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--config", "file://"+path, "sqlite", serveCommand)
	cmd.Env = childEnv()
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("the review build served a non-loopback listener:\n%s", out)
	}
	if !strings.Contains(string(out), "loopback") {
		t.Errorf("the startup failed without naming the loopback rule:\n%s", out)
	}
}

// TestReviewRunAcceptsALoopbackListener is the positive counterpart: the real
// binary over the review configuration with both listeners on loopback reaches
// its ready record, and the ops listener answers a probe. A guard that refused
// every address would pass the refusal case and fail here.
func TestReviewRunAcceptsALoopbackListener(t *testing.T) {
	binary := reviewBinary(t)
	ports := freePorts(t, listenerCount)
	path := reviewConfig(t,
		fmt.Sprintf("127.0.0.1:%d", ports[backendListener]),
		fmt.Sprintf("127.0.0.1:%d", ports[opsListener]))
	config := "file://" + path
	seedReview(t, binary, config)

	serve := startReview(t, binary, config)
	serve.waitReady(t)

	probe := get(t, fmt.Sprintf("http://127.0.0.1:%d/health", ports[opsListener]))
	if probe.status != http.StatusOK {
		t.Fatalf("the ops listener answered %d, want 200:\n%s", probe.status, probe.body)
	}
}
