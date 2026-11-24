package settings

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

// TestTheRefreshIntervalDefaultsToFiveMinutes checks that the declared default arrives through the real loader rather than a stub.
func TestTheRefreshIntervalDefaultsToFiveMinutes(t *testing.T) {
	reg := core.New()
	reg.Register(Module())

	reader, err := config.Load(context.Background(), reg, nil)
	if err != nil {
		t.Fatalf("loading the configuration: %v", err)
	}
	got, err := readSettings(reader)
	if err != nil {
		t.Fatalf("reading this module's settings: %v", err)
	}
	if got.refresh != 5*time.Minute {
		t.Errorf("the refresh interval defaults to %v, want 5m", got.refresh)
	}
}

// TestANegativeRefreshIntervalIsRefused checks that a negative interval is refused at resolve.
func TestANegativeRefreshIntervalIsRefused(t *testing.T) {
	_, err := moduleConfig{RefreshInterval: -time.Second}.resolve()
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("resolving a negative interval returned %v, want ErrInvalidValue", err)
	}
}

// TestZeroIsALegalInterval checks that zero is accepted.
func TestZeroIsALegalInterval(t *testing.T) {
	got, err := moduleConfig{RefreshInterval: 0}.resolve()
	if err != nil {
		t.Fatalf("resolving a zero interval returned %v, want nil", err)
	}
	if got.refresh != 0 {
		t.Errorf("a zero interval resolved to %v", got.refresh)
	}
}

// TestAZeroIntervalStartsNoRefresher checks that a zero interval starts no ticker.
func TestAZeroIntervalStartsNoRefresher(t *testing.T) {
	s := &settingsStore{config: moduleSettings{refresh: 0}}
	s.startRefresh(context.Background())
	if s.cancelRefresh != nil {
		t.Error("a zero refresh interval started a ticker")
	}
	s.stopRefresh()
}

// TestTheRefreshTickerReloadsTheSnapshot checks that a tick reloads the snapshot, the backstop for a replica the bus never reached.
func TestTheRefreshTickerReloadsTheSnapshot(t *testing.T) {
	keys := demoTable(t)
	handle := open(t)
	store := newSettingsStore(keys, stubDatabase{handle: handle, dialect: demoDialect()},
		newRecordingBus(), moduleSettings{refresh: 5 * time.Millisecond})
	store.logger = discardLogger()
	if err := store.load(context.Background()); err != nil {
		t.Fatalf("loading: %v", err)
	}

	if err := handle.Exec("INSERT INTO settings (tenant_id, key, value, version, updated_at) "+
		"VALUES ('', 'demo.limit', '777', 1, ?)", time.Now().UTC()).Error; err != nil {
		t.Fatalf("planting a row: %v", err)
	}

	store.startRefresh(t.Context())
	defer store.stopRefresh()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got, _ := Get[int](store, Global(), "demo.limit"); got == 777 {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Error("the refresh ticker never reloaded the snapshot")
}

// TestAnIntervalStartsARefresherAndStopCancelsIt checks that an interval starts a refresher and Stop cancels it.
func TestAnIntervalStartsARefresherAndStopCancelsIt(t *testing.T) {
	s := &settingsStore{config: moduleSettings{refresh: time.Hour}}
	s.startRefresh(context.Background())
	if s.cancelRefresh == nil {
		t.Fatal("a configured interval started no ticker")
	}
	s.stopRefresh()
	if s.cancelRefresh != nil {
		t.Error("Stop left the ticker's cancel function behind")
	}
}
