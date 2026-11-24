package password

import (
	"bytes"
	"log"
	"log/slog"
	"sync"
	"testing"
)

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// captureLogs replaces the process-wide default logger, so a test that calls it
// must not run in parallel with another.
func captureLogs(t *testing.T) *lockedBuffer {
	t.Helper()
	out := &lockedBuffer{}
	prev := slog.Default()
	prevFlags, prevPrefix := log.Flags(), log.Prefix()
	slog.SetDefault(slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() {
		slog.SetDefault(prev)
		log.SetFlags(prevFlags)
		log.SetPrefix(prevPrefix)
	})
	return out
}
