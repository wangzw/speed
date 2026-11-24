package log

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

func captureBootstrapStdout(t *testing.T, fn func()) string {
	t.Helper()
	w := bootstrapStdoutWriter
	collector := &recordingSink{}

	w.mu.Lock()
	previous := w.sink
	w.sink = collector
	w.mu.Unlock()
	t.Cleanup(func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		w.sink = previous
	})

	fn()

	w.mu.Lock()
	defer w.mu.Unlock()
	return strings.Join(collector.taken, "")
}

// TestDefaultWritesThroughTheBootstrapStdoutWriter pins where the process default logger puts its
// bytes: the standard output writer the root package registered, not a stream of its own.
func TestDefaultWritesThroughTheBootstrapStdoutWriter(t *testing.T) {
	out := captureBootstrapStdout(t, func() {
		Default().Info("through the bootstrap writer")
	})
	if !strings.Contains(out, "through the bootstrap writer") {
		t.Fatalf("the default logger did not write through the bootstrap writer, got %q", out)
	}
	if !strings.Contains(out, "level=INFO") {
		t.Errorf("the bootstrap chain does not encode as text: %q", out)
	}
}

// TestBootstrapChainContainsRedactionLayer pins that the default logger is covered by the
// registered rules.
func TestBootstrapChainContainsRedactionLayer(t *testing.T) {
	isolateRules(t)
	const secret = "bootstrap-plaintext-credential"
	processRedaction.AddKeys("bootstrap-chain-key")

	out := captureBootstrapStdout(t, func() {
		Default().Info("assembling", "bootstrap-chain-key", secret)
	})

	if strings.Contains(out, secret) {
		t.Errorf("the default logger wrote the credential in the clear: %q", out)
	}
	if !strings.Contains(out, MaskedText) {
		t.Errorf("nothing was masked on the bootstrap chain: %q", out)
	}
}

// TestDefaultLevelIsInfoBeforeAnyPrepare pins the level the bootstrap chain starts at.
func TestDefaultLevelIsInfoBeforeAnyPrepare(t *testing.T) {
	if got := bootstrapLevel.Level(); got != slog.LevelInfo {
		t.Fatalf("the bootstrap level is %v, want info before any Prepare has set it", got)
	}
	ctx := context.Background()
	if !Default().Enabled(ctx, slog.LevelInfo) {
		t.Error("the default logger filters out info")
	}
	if Default().Enabled(ctx, slog.LevelDebug) {
		t.Error("the default logger lets debug through before any Prepare")
	}
}

// TestDefaultIsOneSingleton pins that every caller gets the same logger.
func TestDefaultIsOneSingleton(t *testing.T) {
	first, second := Default(), Default()
	if first != second {
		t.Error("two calls to Default returned different loggers")
	}
}

// TestDefaultIsSafeForConcurrentUse pins the chain against the race detector.
func TestDefaultIsSafeForConcurrentUse(t *testing.T) {
	const writers, each = 8, 50
	out := captureBootstrapStdout(t, func() {
		var wg sync.WaitGroup
		for w := range writers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range each {
					Default().Info("concurrent", "writer", w, "n", i)
				}
			}()
		}
		wg.Wait()
	})
	if got := strings.Count(out, "\n"); got != writers*each {
		t.Errorf("the bootstrap writer took %d records, want %d", got, writers*each)
	}
}
