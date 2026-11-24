package log

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func recordCount(c *captureHandler) int {
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	return len(c.state.records)
}

// TestLevelLayerStopsRecordConstruction pins that a filtered record never reaches the downstream
// at all.
func TestLevelLayerStopsRecordConstruction(t *testing.T) {
	capture := newCapture()
	lg := slog.New(newLevelHandler(slog.LevelInfo, capture))

	lg.Debug("below the threshold")
	if got := recordCount(capture); got != 0 {
		t.Fatalf("a record below the threshold reached the downstream %d times, want 0", got)
	}
	if lg.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("the chain reports debug as enabled while the level is info")
	}

	lg.Info("above the threshold")
	if got := recordCount(capture); got != 1 {
		t.Fatalf("the downstream took %d records, want the one that was above the threshold", got)
	}
}

// TestLevelLayerReadsItsOwnLeveler pins that the threshold is read from the Leveler the layer was
// built with, on every judgement rather than once.
func TestLevelLayerReadsItsOwnLeveler(t *testing.T) {
	level := new(slog.LevelVar)
	level.Set(slog.LevelWarn)
	capture := newCapture()
	lg := slog.New(newLevelHandler(level, capture))

	lg.Info("filtered out at warn")
	if got := recordCount(capture); got != 0 {
		t.Fatalf("an info record got through a warn level: %d records downstream", got)
	}

	level.Set(slog.LevelDebug)
	lg.Debug("let through at debug")
	if got := recordCount(capture); got != 1 {
		t.Fatalf("the downstream took %d records after the level moved to debug, want 1", got)
	}
}

// TestLevelLayerPassesAttrsAndGroupsDown pins that a derived logger keeps both the binding and the
// threshold.
func TestLevelLayerPassesAttrsAndGroupsDown(t *testing.T) {
	var buf bytes.Buffer
	encoder := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: allLevels})
	lg := slog.New(newLevelHandler(slog.LevelInfo, encoder)).WithGroup("request").With("id", "r-1")

	lg.Debug("still filtered")
	if buf.Len() != 0 {
		t.Fatalf("the derived logger stopped filtering: %s", buf.String())
	}

	lg.Info("served")
	out := buf.String()
	if !strings.Contains(out, "request.id=r-1") {
		t.Errorf("the bound attribute did not reach the encoder under its group: %s", out)
	}
}

// TestLevelLayerWithEmptyGroupReturnsReceiver pins the Handler contract: an empty group name opens
// nothing, so the receiver is handed back as it is.
func TestLevelLayerWithEmptyGroupReturnsReceiver(t *testing.T) {
	layer := newLevelHandler(slog.LevelInfo, newCapture())
	if got := layer.WithGroup(""); got != slog.Handler(layer) {
		t.Errorf("WithGroup(\"\") returned %p, want the receiver %p", got, layer)
	}
}
