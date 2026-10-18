//go:build review

package main

// The review build's loopback guard: the address classification, the refusal
// the guard states, and the run that carries it.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// TestLoopbackAddresses pins the classification: an IP literal is asked whether
// it is loopback, localhost is the conventional loopback name, an empty host is
// every interface, and a value that is not host:port is refused.
func TestLoopbackAddresses(t *testing.T) {
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
		{"127.0.0.1", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := loopbackAddress(tc.address); got != tc.want {
			t.Errorf("loopbackAddress(%q) = %v, want %v", tc.address, got, tc.want)
		}
	}
}

// TestTheGuardRefusesANonLoopbackListener holds the refusal: one listener off
// loopback fails the guard, and a set of loopback listeners passes.
func TestTheGuardRefusesANonLoopbackListener(t *testing.T) {
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

// TestTheReviewRunRefusesANonLoopbackListener builds the review binary and runs
// it over the review configuration with the backend listener moved off
// loopback: the startup fails with the guard's message, and no socket is
// served. The review configuration is the one the build reads, so every module
// the review adds has its item; only the listener address and the relative
// paths, which would otherwise land in the source tree, are rewritten.
func TestTheReviewRunRefusesANonLoopbackListener(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "demo-review")
	build := exec.Command("go", "build", "-tags", "review", "-o", binary, ".")
	build.Env = childEnv()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the review binary: %v\n%s", err, out)
	}

	review, err := os.ReadFile("review/sqlite.yaml")
	if err != nil {
		t.Fatalf("reading the review configuration: %v", err)
	}
	root := t.TempDir()
	body := string(review)
	for _, edit := range [][2]string{
		{`address: "127.0.0.1:8080"`, `address: "0.0.0.0:8080"`},
		{`./var/review.db`, filepath.Join(root, "review.db")},
		{`var/token-signing-key.pem`, filepath.Join(root, "token-signing-key.pem")},
		{`./var/objects`, filepath.Join(root, "objects")},
	} {
		if !strings.Contains(body, edit[0]) {
			t.Fatalf("the review configuration holds no %q to replace", edit[0])
		}
		body = strings.Replace(body, edit[0], edit[1], 1)
	}
	path := filepath.Join(root, "review.yaml")
	if writeErr := os.WriteFile(path, []byte(body), 0o600); writeErr != nil {
		t.Fatalf("writing the configuration: %v", writeErr)
	}

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
