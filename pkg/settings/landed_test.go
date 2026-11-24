package settings

import (
	"context"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

// TestAWritesResultMatchesWhatLanded checks that a write's result matches what
// landed even when the caller's context is cancelled while the result is read.
func TestAWritesResultMatchesWhatLanded(t *testing.T) {
	for _, d := range dialectsUnderTest {
		t.Run(d.name+"/set", func(t *testing.T) {
			handle := d.open(t)
			bus := newRecordingBus()
			store := storeOn(t, handle, d.dialect, demoTable(t), bus)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cancelWhileReadingTheResult(t, handle, "INSERT INTO", cancel)

			err := store.Set(ctx, Write{Layer: Global(), Key: "demo.limit", Value: 99})
			landed := countRows(t, store) == 1
			if (err == nil) != landed {
				t.Fatalf("Set returned %v, and the row landed = %v", err, landed)
			}
			if err == nil {
				assertServedAndPublished(t, store, bus, 99)
			}
		})
		t.Run(d.name+"/delete", func(t *testing.T) {
			handle := d.open(t)
			bus := newRecordingBus()
			store := storeOn(t, handle, d.dialect, demoTable(t), bus)
			if err := store.Set(context.Background(), Write{Layer: Global(), Key: "demo.limit", Value: 5}); err != nil {
				t.Fatalf("writing the row to delete: %v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cancelWhileReadingTheResult(t, handle, "DELETE FROM", cancel)

			err := store.Delete(ctx, Global(), "demo.limit", "operator")
			removed := countRows(t, store) == 0
			if (err == nil) != removed {
				t.Fatalf("Delete returned %v, and the row was removed = %v", err, removed)
			}
			if err == nil {
				assertServedAndPublished(t, store, bus, 100)
			}
		})
	}
}

func cancelWhileReadingTheResult(t *testing.T, handle *gorm.DB, prefix string, cancel context.CancelFunc) {
	t.Helper()
	err := handle.Callback().Row().After("gorm:row").Register("test:cancel-while-reading-the-result",
		func(tx *gorm.DB) {
			if strings.HasPrefix(tx.Statement.SQL.String(), prefix) {
				cancel()
				time.Sleep(50 * time.Millisecond)
			}
		})
	if err != nil {
		t.Fatalf("registering the callback: %v", err)
	}
}

func assertServedAndPublished(t *testing.T, store *settingsStore, bus *recordingBus, want int) {
	t.Helper()
	if got, _ := Get[int](store, Global(), "demo.limit"); got != want {
		t.Errorf("the writer reads %v, want %v", got, want)
	}
	if events := bus.events(); len(events) == 0 {
		t.Error("a write reported as done published nothing")
	}
}
