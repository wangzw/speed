package settings

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/event"
)

// levelRecorder keeps the level and message of every record a store writes.
type levelRecorder struct {
	mu      sync.Mutex
	records []slog.Record
}

func (r *levelRecorder) Enabled(context.Context, slog.Level) bool { return true }
func (r *levelRecorder) WithAttrs([]slog.Attr) slog.Handler       { return r }
func (r *levelRecorder) WithGroup(string) slog.Handler            { return r }

func (r *levelRecorder) Handle(_ context.Context, record slog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, record)
	return nil
}

// errors returns the messages of the records written at error level.
func (r *levelRecorder) errors() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, record := range r.records {
		if record.Level >= slog.LevelError {
			out = append(out, record.Message)
		}
	}
	return out
}

// holdReadsUntilCancelled makes every read of the table wait until its
// statement's context is done, reports through entered when one has started
// waiting, and sets returned once such a read has come back. It stands for a
// reload still in flight when the process begins to stop.
func holdReadsUntilCancelled(t *testing.T, handle *gorm.DB, entered chan<- struct{}, returned *atomic.Bool) {
	t.Helper()
	isRead := func(tx *gorm.DB) bool {
		return strings.HasPrefix(tx.Statement.SQL.String(), "SELECT tenant_id, key, value FROM "+tableName)
	}
	var once sync.Once
	if err := handle.Callback().Row().Before("gorm:row").Register("test:hold", func(tx *gorm.DB) {
		if isRead(tx) {
			once.Do(func() { close(entered) })
			<-tx.Statement.Context.Done()
		}
	}); err != nil {
		t.Fatalf("registering the holding callback: %v", err)
	}
	if err := handle.Callback().Row().After("gorm:row").Register("test:returned", func(tx *gorm.DB) {
		if isRead(tx) {
			returned.Store(true)
		}
	}); err != nil {
		t.Fatalf("registering the returning callback: %v", err)
	}
}

// Stop waits for the refresh to finish, and a refresh that Stop cut short is
// not reported as a failure.
//
// A reload in flight when Stop cancels it fails with the cancellation. That is
// the stop working, not the refresh failing, and an error record for it would
// appear on an ordinary shutdown whenever a tick happened to be running. Left
// running past Stop, the same read would also go on using the database handle
// while the database module, which stops and closes after this one, releases
// it.
func TestStoppingTheRefreshWaitsAndReportsNoFailure(t *testing.T) {
	store, handle := storeOf(t, demoTable(t), newRecordingBus())
	recorder := &levelRecorder{}
	store.logger = slog.New(recorder)
	entered := make(chan struct{})
	var returned atomic.Bool
	holdReadsUntilCancelled(t, handle, entered, &returned)

	store.config.refresh = time.Millisecond
	store.startRefresh(context.Background())
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("no refresh started")
	}
	store.stopRefresh()
	if !returned.Load() {
		t.Error("Stop returned while the refresh it cancelled was still reading the table")
	}

	// Anything the refresh still had to write would have arrived by now.
	time.Sleep(100 * time.Millisecond)
	if lines := recorder.errors(); len(lines) != 0 {
		t.Errorf("a refresh cut short by Stop was reported as a failure: %q", lines)
	}
}

// A reload whose delivery the bus withdrew is not reported as a failure
// either. A transport bus cancels the context it delivers on when it stops,
// which happens after this module has stopped, so a delivery in flight then
// fails with the cancellation on every such shutdown.
func TestAWithdrawnDeliveryReportsNoFailure(t *testing.T) {
	bus := newRecordingBus()
	store, _ := storeOf(t, demoTable(t), bus)
	recorder := &levelRecorder{}
	store.logger = slog.New(recorder)
	store.subscribe()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	bus.mu.Lock()
	handlers := append([]event.Handler(nil), bus.handlers[ChangedEventType]...)
	bus.mu.Unlock()
	for _, h := range handlers {
		if err := h(ctx, eventOf("demo.limit")); err != nil {
			t.Fatalf("the handler failed: %v", err)
		}
	}
	if lines := recorder.errors(); len(lines) != 0 {
		t.Errorf("a delivery the bus withdrew was reported as a failure: %q", lines)
	}

	// A reload that fails on its own is still reported: the table is gone.
	if err := store.handle.Exec("DROP TABLE " + tableName).Error; err != nil {
		t.Fatalf("dropping the table: %v", err)
	}
	store.reload(context.Background())
	if lines := recorder.errors(); len(lines) != 1 {
		t.Errorf("a reload that failed on its own wrote %q, want one error record", lines)
	}
}
