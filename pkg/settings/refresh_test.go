package settings

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/core/coretest"
	"github.com/vislake/speed/pkg/event"
)

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

func holdReadsUntilCancelled(t *testing.T, handle *gorm.DB, entered chan<- struct{}, returned *atomic.Bool) {
	t.Helper()
	isRead := func(tx *gorm.DB) bool {
		return strings.HasPrefix(tx.Statement.SQL.String(), "SELECT tenant_id, key, value, updated_at FROM "+tableName)
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

// TestStoppingTheRefreshWaitsAndReportsNoFailure checks that Stop waits for the
// refresh and does not report the cancellation as a failure.
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
	case <-time.After(coretest.Scale(5 * time.Second)):
		t.Fatal("no refresh started")
	}
	store.stopRefresh()
	if !returned.Load() {
		t.Error("Stop returned while the refresh it cancelled was still reading the table")
	}

	time.Sleep(100 * time.Millisecond)
	if lines := recorder.errors(); len(lines) != 0 {
		t.Errorf("a refresh cut short by Stop was reported as a failure: %q", lines)
	}
}

// TestAFailedPublishSaysHowTheOthersConverge checks that the record says whether the refresh runs.
func TestAFailedPublishSaysHowTheOthersConverge(t *testing.T) {
	cases := []struct {
		name    string
		refresh time.Duration
		want    string
		refused string
	}{
		{"refresh on", 5 * time.Minute, "next refresh (every 5m0s here)", "refresh is off"},
		{"refresh off", 0, "refresh is off", "next refresh"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bus := newRecordingBus()
			bus.failWith = errors.New("the bus is unreachable")
			store, _ := storeOf(t, demoTable(t), bus)
			store.config.refresh = c.refresh
			recorder := &levelRecorder{}
			store.logger = slog.New(recorder)

			if err := store.Set(context.Background(), Write{Layer: Global(), Key: "demo.limit", Value: 3}); err != nil {
				t.Fatalf("writing: %v", err)
			}
			lines := recorder.errors()
			if len(lines) != 1 {
				t.Fatalf("want one error record for the failed publish, got %q", lines)
			}
			if !strings.Contains(lines[0], c.want) || strings.Contains(lines[0], c.refused) {
				t.Errorf("the record reads %q, want it to say %q and not %q", lines[0], c.want, c.refused)
			}
		})
	}
}

// TestAWithdrawnDeliveryReportsNoFailure checks that a delivery the bus withdrew is not reported as a failure.
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

	if err := store.handle.Exec("DROP TABLE " + tableName).Error; err != nil {
		t.Fatalf("dropping the table: %v", err)
	}
	store.reload(context.Background())
	if lines := recorder.errors(); len(lines) != 1 {
		t.Errorf("a reload that failed on its own wrote %q, want one error record", lines)
	}
}
