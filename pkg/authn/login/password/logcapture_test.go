package password

import (
	"bytes"
	"log"
	"log/slog"
	"sync"
	"testing"
)

// lockedBuffer is a buffer a handler writes to while the test reads it.
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

// captureLogs points the process default logger, and the standard library's
// log package that follows it, at a buffer for the length of the test. A case
// that calls it does not run in parallel: the default logger is process state.
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
