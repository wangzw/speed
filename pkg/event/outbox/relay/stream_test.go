package relay

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
)

// A steady stream of stagings on this replica brings the relay's reads of
// the table forward and never puts one off.
func TestASteadyStreamOfStagingsNeverStarvesTheRelay(t *testing.T) {
	onEachEngine(t, func(t *testing.T, e engine, dsn string) {
		database := migrated(t, e, dsn)
		l := &ledger{}
		streamed := quick
		streamed.burst, streamed.poll = 500*time.Millisecond, time.Hour
		r := newQuick(t, database, newRecordingBus("only", l), streamed)

		// The first event is staged before the relay starts and published by
		// its first reads; the stream begins once those are over.
		first := event.NewID()
		if err := placeOrder(t.Context(), database, r, orderID(0), first, false); err != nil {
			t.Fatalf("placing the first order: %v", err)
		}
		r.start(t.Context())
		if !eventually(func() bool { return l.count() == 1 && stagedRows(t, database) == 0 }) {
			t.Fatalf("the relay never published the event staged before it started")
		}

		s := startStream(database, r)
		defer s.end(t)
		if !eventually(func() bool { return len(s.staged()) >= 10 }) {
			t.Fatalf("the stream staged %d events", len(s.staged()))
		}
		want := s.staged()[9]
		if !eventually(func() bool { return slices.Contains(idsOf(l.events()), want) }) {
			t.Fatalf("while %d events were staged in a steady stream the relay published %d, never the "+
				"tenth, %s", len(s.staged()), l.count()-1, want)
		}
	})
}

const streamPause = 20 * time.Millisecond

type stream struct {
	ended chan struct{}
	done  chan error

	mu  sync.Mutex
	ids []string
}

func startStream(database db.Database, r *relay) *stream {
	s := &stream{ended: make(chan struct{}), done: make(chan error, 1)}
	go func() {
		for i := 1; ; i++ {
			select {
			case <-s.ended:
				s.done <- nil
				return
			default:
			}
			id := event.NewID()
			if err := placeOrder(context.Background(), database, r, orderID(i), id, false); err != nil {
				s.done <- err
				return
			}
			s.mu.Lock()
			s.ids = append(s.ids, id)
			s.mu.Unlock()
			time.Sleep(streamPause)
		}
	}()
	return s
}

func (s *stream) staged() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.ids)
}

func (s *stream) end(t *testing.T) {
	t.Helper()
	close(s.ended)
	if err := <-s.done; err != nil {
		t.Errorf("the stream failed to stage: %v", err)
	}
}
