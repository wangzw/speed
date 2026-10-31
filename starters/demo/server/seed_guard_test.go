package main

// The seed guard's cases: the four deployment shapes it has to tell apart, the
// fallback for a reader the layer query cannot answer about, the module's own
// Prepare path, and the two commands' runs over the real binary.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

// seedGuardListeners renders the JSON value DEMO_HTTP__LISTENERS takes.
func seedGuardListeners(backend, ops string) string {
	return fmt.Sprintf(`{"backend":{"address":%q},"ops":{"address":%q}}`, backend, ops)
}

// loadSeedGuardReader loads the configuration of a run declared by args and env
// through the same entry point the binary uses, over the process registry. The
// process's own DEMO_ and OTEL_ variables are cleared first, so a case's env is
// the whole of what the reader sees.
func loadSeedGuardReader(t *testing.T, args []string, env map[string]string) config.Reader {
	t.Helper()
	clearHostEnvironment(t)
	for name, value := range env {
		t.Setenv(name, value)
	}
	reader, loadErr := config.Load(t.Context(), core.ProcessRegistry, args)
	if loadErr != nil {
		t.Fatalf("loading the guard's configuration: %v", loadErr)
	}
	return reader
}

// seedGuardRegistry is a registry whose only capability is the configuration
// reader the guard's Prepare takes up.
func seedGuardRegistry(t *testing.T, reader config.Reader) *core.Registry {
	t.Helper()
	reg := core.New()
	reg.Register(core.Module{
		Name:     "test.seed.config",
		Provides: []core.Provision{{Token: (*config.Reader)(nil)}},
	})
	reg.Supply(reader, "test.seed.config")
	return reg
}

// TestSeedGuardDecidesTheDeploymentShapes holds the four combinations to their
// verdicts: loopback with the compiled seed passes, an external listener with a
// seed from either the configuration file or the environment passes, and an
// external listener with the compiled seed is refused. A guard that checked
// only the listener, only the seed, or only one of the two override layers
// fails one of these rows.
func TestSeedGuardDecidesTheDeploymentShapes(t *testing.T) {
	loopback := seedGuardListeners("127.0.0.1:8080", "127.0.0.1:8083")
	external := seedGuardListeners("0.0.0.0:8080", "127.0.0.1:8083")
	const deploymentSeed = `[{"identifier":"deployment-operator","password":"deployment-secret","platform-admin":true}]`
	const deploymentConfig = `seed:
  accounts:
    - identifier: "deployment-operator"
      password: "deployment-secret"
      platform-admin: true
`

	cases := []struct {
		name       string
		configBody string
		env        map[string]string
		refuse     bool
	}{
		{
			name: "a loopback listener with the compiled seed",
			env:  map[string]string{"DEMO_HTTP__LISTENERS": loopback},
		},
		{
			name:       "an external listener with a configuration seed",
			configBody: deploymentConfig,
			env:        map[string]string{"DEMO_HTTP__LISTENERS": external},
		},
		{
			name:   "an external listener with the compiled seed",
			env:    map[string]string{"DEMO_HTTP__LISTENERS": external},
			refuse: true,
		},
		{
			name: "an external listener with an environment seed",
			env: map[string]string{
				"DEMO_HTTP__LISTENERS": external,
				"DEMO_SEED__ACCOUNTS":  deploymentSeed,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var args []string
			if tc.configBody != "" {
				args = []string{"--config", writeConfigFile(t, tc.configBody)}
			}
			reader := loadSeedGuardReader(t, args, tc.env)
			checkErr := checkSeedCoverage(reader)
			if !tc.refuse {
				if checkErr != nil {
					t.Fatalf("the run was refused: %v", checkErr)
				}
				return
			}
			if checkErr == nil {
				t.Fatalf("the run was accepted on an external listener with the compiled seed")
			}
			for _, want := range []string{"built-in defaults", "not a loopback", "Override the seed"} {
				if !strings.Contains(checkErr.Error(), want) {
					t.Errorf("the refusal does not name %q: %v", want, checkErr)
				}
			}
		})
	}
}

// TestSeedGuardFallsBackToComparingTheSeed holds the path taken when the layer
// query cannot answer about the reader: the effective account list is compared
// with the compiled one, an empty list counts as the default, and an unreadable
// seed is an error rather than a verdict.
func TestSeedGuardFallsBackToComparingTheSeed(t *testing.T) {
	external := map[string]seedGuardListener{"backend": {Address: "0.0.0.0:8080"}}
	loopback := map[string]seedGuardListener{"backend": {Address: "127.0.0.1:8080"}}
	overridden := []seedAccount{{
		Identifier: "deployment-operator", Password: "deployment-secret", PlatformAdmin: true,
	}}

	cases := []struct {
		name    string
		reader  seedGuardStubReader
		refuse  bool
		wantErr bool
	}{
		{
			name:   "the compiled accounts on an external listener",
			reader: seedGuardStubReader{accounts: demoSeedAccounts(), listeners: external},
			refuse: true,
		},
		{
			name:   "an overridden seed on an external listener",
			reader: seedGuardStubReader{accounts: overridden, listeners: external},
		},
		{
			name:   "an undeclared account list on an external listener",
			reader: seedGuardStubReader{listeners: external},
			refuse: true,
		},
		{
			name:   "the compiled accounts on a loopback listener",
			reader: seedGuardStubReader{accounts: demoSeedAccounts(), listeners: loopback},
		},
		{
			name:    "an unreadable seed",
			reader:  seedGuardStubReader{accountsErr: errors.New("the seed is unreadable"), listeners: external},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checkErr := checkSeedCoverage(tc.reader)
			if tc.wantErr {
				if checkErr == nil {
					t.Fatalf("an unreadable seed was accepted")
				}
				return
			}
			if !tc.refuse {
				if checkErr != nil {
					t.Fatalf("the run was refused: %v", checkErr)
				}
				return
			}
			if checkErr == nil {
				t.Fatalf("the run was accepted")
			}
			if !strings.Contains(checkErr.Error(), "built-in defaults") {
				t.Errorf("the refusal does not name the compiled seed: %v", checkErr)
			}
		})
	}
}

// seedGuardStubReader answers the two paths the guard reads, and nothing else.
// It cannot answer EffectiveLayer, because that query accepts only a reader this
// package produced, which is what puts the guard on its fallback path.
type seedGuardStubReader struct {
	accounts    []seedAccount
	listeners   map[string]seedGuardListener
	accountsErr error
}

func (r seedGuardStubReader) Decode(path string, target any) error {
	switch path {
	case seedNamespace:
		view, ok := target.(*seedAccountsView)
		if !ok {
			return fmt.Errorf("seed guard stub: unexpected target %T for %q", target, path)
		}
		if r.accountsErr != nil {
			return r.accountsErr
		}
		view.Accounts = r.accounts
		return nil
	case seedGuardHTTPNamespace:
		cfg, ok := target.(*seedGuardHTTPConfig)
		if !ok {
			return fmt.Errorf("seed guard stub: unexpected target %T for %q", target, path)
		}
		cfg.Listeners = r.listeners
		return nil
	}
	return fmt.Errorf("seed guard stub: unexpected path %q", path)
}

// TestSeedGuardPrepareHoldsTheCheck holds the module's own path: Prepare refuses
// the compiled seed on an external listener and enables the module only when the
// run is safe. A build whose Prepare skipped the check fails here without the
// binary cases.
func TestSeedGuardPrepareHoldsTheCheck(t *testing.T) {
	external := map[string]string{
		"DEMO_HTTP__LISTENERS": seedGuardListeners("0.0.0.0:8080", "127.0.0.1:8083"),
	}
	exposed := loadSeedGuardReader(t, nil, external)
	if _, prepareErr := seedGuardModule().Prepare(context.Background(), seedGuardRegistry(t, exposed)); prepareErr == nil {
		t.Fatalf("Prepare accepted the compiled seed on an external listener")
	}

	loopback := map[string]string{
		"DEMO_HTTP__LISTENERS": seedGuardListeners("127.0.0.1:8080", "127.0.0.1:8083"),
	}
	safe := loadSeedGuardReader(t, nil, loopback)
	enablement, prepareErr := seedGuardModule().Prepare(context.Background(), seedGuardRegistry(t, safe))
	if prepareErr != nil {
		t.Fatalf("Prepare refused a loopback run on the compiled seed: %v", prepareErr)
	}
	if enablement.State != core.StateEnabled {
		t.Errorf("Prepare answered state %v, want enabled", enablement.State)
	}
}

// TestSeedGuardRefusesServeOnAnExternalListener runs the real binary's serve
// command on an external listener over the compiled seed: the startup fails
// before a socket is bound and names the seed, the listener and the remedy.
func TestSeedGuardRefusesServeOnAnExternalListener(t *testing.T) {
	dir := t.TempDir()
	ports := freePorts(t, listenerCount)
	listeners := seedGuardListeners(
		fmt.Sprintf("0.0.0.0:%d", ports[backendListener]),
		fmt.Sprintf("127.0.0.1:%d", ports[opsListener]))

	serve := startArgs(t, dir, []string{"sqlite", serveCommand},
		[]string{"DEMO_HTTP__LISTENERS=" + listeners})
	select {
	case <-serve.done:
	case <-serve.ready:
		t.Fatalf("serve reached its ready record on an external listener with the compiled seed:\n%s",
			serve.output())
	case <-time.After(startLimit):
		t.Fatalf("serve neither refused nor reached its ready record within %s\n%s\n%s",
			startLimit, serve.output(), serve.stderr())
	}
	if serve.waitErr == nil {
		t.Fatalf("serve exited successfully on an external listener with the compiled seed")
	}
	for _, want := range []string{"built-in defaults", "not a loopback", `"backend"`} {
		if !strings.Contains(serve.stderr(), want) {
			t.Errorf("the refusal does not name %q:\n%s", want, serve.stderr())
		}
	}
}

// TestSeedGuardRefusesTheSeedCommandOnAnExternalListener is the same refusal in
// the command that would write the demonstration account, and it holds that
// nothing was written: the guard runs before the database is created.
func TestSeedGuardRefusesTheSeedCommandOnAnExternalListener(t *testing.T) {
	dir := t.TempDir()
	ports := freePorts(t, listenerCount)
	listeners := seedGuardListeners(
		fmt.Sprintf("0.0.0.0:%d", ports[backendListener]),
		fmt.Sprintf("127.0.0.1:%d", ports[opsListener]))

	seeded := runCommandArgs(t, hostBinary, dir, []string{"sqlite", seedCommand},
		[]string{"DEMO_HTTP__LISTENERS=" + listeners})
	if seeded.err == nil {
		t.Fatalf("seed ran on an external listener over the compiled seed:\n%s", seeded.combined())
	}
	if !strings.Contains(seeded.stderr, "built-in defaults") {
		t.Errorf("the refusal does not name the compiled seed:\n%s", seeded.combined())
	}
	if _, statErr := os.Stat(filepath.Join(dir, "var", "demo.db")); statErr == nil {
		t.Errorf("the refused seed created the database it was never meant to touch")
	}
}

// TestSeedGuardServesWithAnOverriddenSeedOnAnExternalListener is the positive
// counterpart: the same external listener starts once the environment replaces
// the demonstration account list. The tenant list is replaced with an empty one
// exactly as the deployment template does, because the compiled tenant names
// the compiled administrator the replacement account no longer carries. A guard
// that refused every external listener would pass the refusal cases and fail
// here.
func TestSeedGuardServesWithAnOverriddenSeedOnAnExternalListener(t *testing.T) {
	dir := t.TempDir()
	ports := freePorts(t, listenerCount)
	listeners := seedGuardListeners(
		fmt.Sprintf("0.0.0.0:%d", ports[backendListener]),
		fmt.Sprintf("127.0.0.1:%d", ports[opsListener]))
	env := []string{
		"DEMO_HTTP__LISTENERS=" + listeners,
		`DEMO_SEED__ACCOUNTS=[{"identifier":"deployment-operator","password":"deployment-secret","platform-admin":true}]`,
		"DEMO_SEED__TENANTS=[]",
	}

	seeded := runCommandArgs(t, hostBinary, dir, []string{"sqlite", seedCommand}, env)
	if seeded.err != nil {
		t.Fatalf("seed on the overridden seed failed: %v\n%s", seeded.err, seeded.combined())
	}

	serve := startArgs(t, dir, []string{"sqlite", serveCommand}, env)
	serve.waitReady(t)
}
