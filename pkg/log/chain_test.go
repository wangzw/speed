package log

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func fileOutputs(paths ...string) []resolvedOutput {
	outputs := make([]resolvedOutput, 0, len(paths))
	for _, p := range paths {
		outputs = append(outputs, resolvedOutput{to: destFile, format: formatJSON, path: p})
	}
	return outputs
}

func newTestChain(t *testing.T, level slog.Level, paths ...string) *chain {
	t.Helper()
	assembled, err := newChain(resolvedConfig{level: level, outputs: fileOutputs(paths...)})
	if err != nil {
		t.Fatalf("assembling a chain over %v failed: %v", paths, err)
	}
	t.Cleanup(assembled.release)
	return assembled
}

func branchesOf(t *testing.T, assembled *chain) []slog.Handler {
	t.Helper()
	head, ok := assembled.handler.(*levelHandler)
	if !ok {
		t.Fatalf("the head of the chain is %T, want the level layer", assembled.handler)
	}
	redaction, ok := head.next.(*redactHandler)
	if !ok {
		t.Fatalf("below the level layer sits %T, want the redaction layer", head.next)
	}
	fanout, ok := redaction.next.(*fanoutHandler)
	if !ok {
		t.Fatalf("below the redaction layer sits %T, want the fan-out", redaction.next)
	}
	return fanout.branches
}

// TestDefaultConfigChainSharesTheBootstrapStdoutWriter checks that the default configuration reuses
// the bootstrap stdout writer and adds one reference to it.
func TestDefaultConfigChainSharesTheBootstrapStdoutWriter(t *testing.T) {
	resolved, err := configDefaults.resolve()
	if err != nil {
		t.Fatalf("the prototype configuration does not resolve: %v", err)
	}

	assembled, err := newChain(resolved)
	if err != nil {
		t.Fatalf("assembling the default chain failed: %v", err)
	}
	t.Cleanup(assembled.release)

	if len(assembled.writers) != 1 {
		t.Fatalf("the default chain opened %d writers, want the one standard output", len(assembled.writers))
	}
	if assembled.writers[0] != bootstrapStdoutWriter {
		t.Errorf("the stdout branch writes through %p, want the bootstrap writer %p",
			assembled.writers[0], bootstrapStdoutWriter)
	}
	if got := refsFor("stdout"); got != 2 {
		t.Errorf("standard output has %d references, want the bootstrap one plus this assembly", got)
	}
}

// TestReleaseKeepsTheBootstrapReference pins that closing an assembly does not take the default
// logger away.
func TestReleaseKeepsTheBootstrapReference(t *testing.T) {
	resolved, err := configDefaults.resolve()
	if err != nil {
		t.Fatalf("the prototype configuration does not resolve: %v", err)
	}
	assembled, err := newChain(resolved)
	if err != nil {
		t.Fatalf("assembling the default chain failed: %v", err)
	}

	assembled.release()

	if got := refsFor("stdout"); got != 1 {
		t.Errorf("standard output has %d references after the release, want the bootstrap one", got)
	}
	if !registered("stdout") {
		t.Fatal("releasing the assembly deregistered standard output")
	}
	bootstrapStdoutWriter.mu.Lock()
	closed := bootstrapStdoutWriter.closed
	bootstrapStdoutWriter.mu.Unlock()
	if closed {
		t.Fatal("releasing the assembly closed the bootstrap writer")
	}

	out := captureBootstrapStdout(t, func() { Default().Info("after the assembly closed") })
	if !strings.Contains(out, "after the assembly closed") {
		t.Errorf("the default logger stopped writing after an assembly was released: %q", out)
	}
}

// TestBranchHandlersDoNotFilterByLevel pins that the level is judged once, at the head, and that
// no branch carries a threshold of its own.
func TestBranchHandlersDoNotFilterByLevel(t *testing.T) {
	dir := t.TempDir()
	first, second := filepath.Join(dir, "first.log"), filepath.Join(dir, "second.log")
	assembled := newTestChain(t, slog.LevelDebug, first, second)

	ctx := context.Background()
	for i, branch := range branchesOf(t, assembled) {
		if !branch.Enabled(ctx, allLevels) {
			t.Errorf("branch %d filters by a level of its own", i)
		}
	}

	slog.New(assembled.handler).Debug("a debug record")

	for _, path := range []string{first, second} {
		if out := read(t, path); !strings.Contains(out, "a debug record") {
			t.Errorf("%s did not get the debug record: %q", filepath.Base(path), out)
		}
	}
}

// TestRecordIsRedactedOnceForManyOutputs pins the order of the layers: redaction above the
// fan-out, so a record is judged once however many outputs there are.
func TestRecordIsRedactedOnceForManyOutputs(t *testing.T) {
	isolateRules(t)
	var calls atomic.Int64
	processRedaction.AddPattern("counting", func(string) []Span {
		calls.Add(1)
		return nil
	})

	dir := t.TempDir()
	assembled := newTestChain(t, slog.LevelInfo,
		filepath.Join(dir, "a.log"), filepath.Join(dir, "b.log"), filepath.Join(dir, "c.log"))

	slog.New(assembled.handler).Info("three outputs", "value", "a string to judge")

	if got := calls.Load(); got != 1 {
		t.Errorf("the matcher ran %d times for one record over three outputs, want 1", got)
	}
}

// TestFormalChainLevelIsImmuneToBootstrapLevelVar pins that a chain's level is settled when it is
// assembled.
func TestFormalChainLevelIsImmuneToBootstrapLevelVar(t *testing.T) {
	keepBootstrapLevel(t)
	path := filepath.Join(t.TempDir(), "immune.log")
	assembled := newTestChain(t, slog.LevelInfo, path)

	bootstrapLevel.Set(slog.LevelDebug)

	lg := slog.New(assembled.handler)
	lg.Debug("must not be written")
	lg.Info("must be written")

	out := read(t, path)
	if strings.Contains(out, "must not be written") {
		t.Errorf("moving the bootstrap level re-filtered an assembled chain: %q", out)
	}
	if !strings.Contains(out, "must be written") {
		t.Errorf("the chain stopped writing records above its own level: %q", out)
	}
}

// TestTwoChainsFilterByTheirOwnConfiguredLevel pins the other side of the same rule: two
// assemblies coexist, each filtering by the level it was built with.
func TestTwoChainsFilterByTheirOwnConfiguredLevel(t *testing.T) {
	dir := t.TempDir()
	debugPath, warnPath := filepath.Join(dir, "debug.log"), filepath.Join(dir, "warn.log")
	debugChain := newTestChain(t, slog.LevelDebug, debugPath)
	warnChain := newTestChain(t, slog.LevelWarn, warnPath)

	slog.New(debugChain.handler).Info("an info record")
	slog.New(warnChain.handler).Info("an info record")

	if out := read(t, debugPath); !strings.Contains(out, "an info record") {
		t.Errorf("the chain configured at debug dropped an info record: %q", out)
	}
	if out := read(t, warnPath); strings.Contains(out, "an info record") {
		t.Errorf("the chain configured at warn wrote an info record: %q", out)
	}
}

// TestUnknownDestinationOrFormatFailsAssembly checks that an output naming an unknown format or
// destination fails assembly with that sentinel and leaves no writer registered.
func TestUnknownDestinationOrFormatFailsAssembly(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.log")

	for _, tc := range []struct {
		name string
		bad  resolvedOutput
		want error
	}{
		{
			name: "format",
			bad:  resolvedOutput{to: destFile, format: "yaml", path: filepath.Join(dir, "bad.log")},
			want: ErrUnknownFormat,
		},
		{
			name: "destination",
			bad:  resolvedOutput{to: "syslog", format: formatJSON, path: filepath.Join(dir, "bad.log")},
			want: ErrUnknownDestination,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := resolvedConfig{
				level:   slog.LevelInfo,
				outputs: append(fileOutputs(good), tc.bad),
			}
			assembled, err := newChain(cfg)
			if err == nil {
				assembled.release()
				t.Fatalf("an output naming an unknown %s assembled without an error", tc.name)
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("the assembly failed with %v, want %v", err, tc.want)
			}
			if registered(destinationID(fileOutputs(good)[0])) {
				t.Error("the output ahead of the bad one kept its writer after the failed assembly")
			}
		})
	}
}

// TestChainLayerOrderIsLevelThenRedactionThenFanout pins the order the layers sit in.
func TestChainLayerOrderIsLevelThenRedactionThenFanout(t *testing.T) {
	dir := t.TempDir()
	assembled := newTestChain(t, slog.LevelInfo, filepath.Join(dir, "a.log"), filepath.Join(dir, "b.log"))

	branches := branchesOf(t, assembled)
	if len(branches) != 2 {
		t.Errorf("the fan-out carries %d branches, want one per output", len(branches))
	}
	for i, branch := range branches {
		if _, ok := branch.(*redactHandler); ok {
			t.Errorf("branch %d carries a redaction layer of its own", i)
		}
	}
}

// TestEmptyOutputListAssemblesAnEmptyFanout checks that a chain with no outputs opens no writer and
// writes nothing.
func TestEmptyOutputListAssemblesAnEmptyFanout(t *testing.T) {
	assembled, err := newChain(resolvedConfig{level: slog.LevelInfo})
	if err != nil {
		t.Fatalf("assembling a chain with no outputs failed: %v", err)
	}
	t.Cleanup(assembled.release)

	if len(assembled.writers) != 0 {
		t.Errorf("a chain with no outputs opened %d writers", len(assembled.writers))
	}
	out := captureBootstrapStdout(t, func() { slog.New(assembled.handler).Error("nowhere") })
	if out != "" {
		t.Errorf("a chain with no outputs wrote to standard output: %q", out)
	}
}

// TestEmptyOutputListDisablesTheChainHead pins the short circuit an explicitly empty output list
// gets: the head reports the chain disabled, so a record is neither built nor judged.
func TestEmptyOutputListDisablesTheChainHead(t *testing.T) {
	isolateRules(t)
	var calls atomic.Int64
	processRedaction.AddPattern("counting", func(string) []Span {
		calls.Add(1)
		return nil
	})

	assembled, err := newChain(resolvedConfig{level: slog.LevelDebug})
	if err != nil {
		t.Fatalf("assembling a chain with no outputs failed: %v", err)
	}
	t.Cleanup(assembled.release)

	head, ok := assembled.handler.(*levelHandler)
	if !ok {
		t.Fatalf("the head of the chain is %T, want the level layer", assembled.handler)
	}
	if head.Enabled(context.Background(), slog.LevelError) {
		t.Error("the head of a chain with no outputs reports the chain enabled")
	}

	slog.New(assembled.handler).Info("nowhere", "value", "a string to judge")

	if got := calls.Load(); got != 0 {
		t.Errorf("the redaction layer ran %d times for a chain with nowhere to write, want 0", got)
	}
}

// TestEmptyOutputListKeepsDerivedLoggersDisabled checks that loggers derived with With or WithGroup
// from an output-less chain report every level disabled.
func TestEmptyOutputListKeepsDerivedLoggersDisabled(t *testing.T) {
	assembled, err := newChain(resolvedConfig{level: slog.LevelDebug})
	if err != nil {
		t.Fatalf("assembling a chain with no outputs failed: %v", err)
	}
	t.Cleanup(assembled.release)

	logger := slog.New(assembled.handler)
	for _, tc := range []struct {
		name    string
		derived *slog.Logger
	}{
		{name: "With", derived: logger.With("k", "v")},
		{name: "WithGroup", derived: logger.WithGroup("g")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.derived.Enabled(context.Background(), slog.LevelError) {
				t.Error("a logger derived from a chain with no outputs reports the chain enabled")
			}
		})
	}
}
