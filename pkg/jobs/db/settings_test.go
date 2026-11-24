package db

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event/memory"
	"github.com/vislake/speed/pkg/jobs"
	speedsettings "github.com/vislake/speed/pkg/settings"
)

const (
	pollIntervalSetting = "jobs.db.poll-interval"
	claimBatchSetting   = "jobs.db.claim-batch"
	leaseSetting        = "jobs.db.lease"
	retryBaseSetting    = "jobs.db.retry-base"
	retryMaxSetting     = "jobs.db.retry-max"
)

func stayingJobs() map[string]any {
	return map[string]any{
		workersItem:       1,
		shutdownGraceItem: "200ms",
	}
}

func queueSettingsModules() []core.Module {
	return []core.Module{memory.Module(), speedsettings.Module()}
}

// TestTheClaimBatchInTheStoreSetsHowManyOneClaimTakes checks that the batch size
// is read at each claim.
func TestTheClaimBatchInTheStoreSetsHowManyOneClaimTakes(t *testing.T) {
	e := engines[0]
	e.on(t, host{depth: core.StageInit, jobs: stayingJobs(), extra: queueSettingsModules()}).
		drive(t, func(_ context.Context, reg *core.Registry) error {
			writeQueueSetting(t, queueSettingsStoreOf(t, reg), claimBatchSetting, 2)
			for i := 0; i < 4; i++ {
				insertJob(t, reg, fmt.Sprintf("batch-%d", i), string(jobs.StatusPending), "", 0)
			}

			rows, err := product(t, reg).claim(&worker{token: "batch-executor"})
			if err != nil {
				t.Fatalf("claiming: %v", err)
			}
			if len(rows) != 2 {
				t.Errorf("one claim took %d jobs, want the stored batch of 2", len(rows))
			}
			return nil
		})
}

// TestTheLeaseInTheStoreSetsHowLongOneClaimHolds checks that the lease is read at
// each claim.
func TestTheLeaseInTheStoreSetsHowLongOneClaimHolds(t *testing.T) {
	e := engines[0]
	e.on(t, host{depth: core.StageInit, jobs: stayingJobs(), extra: queueSettingsModules()}).
		drive(t, func(_ context.Context, reg *core.Registry) error {
			writeQueueSetting(t, queueSettingsStoreOf(t, reg), leaseSetting, 37*time.Second)
			insertJob(t, reg, "leased", string(jobs.StatusPending), "", 0)

			before := time.Now()
			if _, err := product(t, reg).claim(&worker{token: "lease-executor"}); err != nil {
				t.Fatalf("claiming: %v", err)
			}
			after := time.Now()

			assertClaimDeadline(t, "the claim's lease", claimExpiryOf(t, reg, "leased"), before, after, 37*time.Second)
			return nil
		})
}

// TestThePollIntervalInTheStoreSetsTheIdleWait checks that the poll interval is
// read when a pass finds nothing due.
func TestThePollIntervalInTheStoreSetsTheIdleWait(t *testing.T) {
	e := engines[0]
	e.on(t, host{depth: core.StageInit, jobs: stayingJobs(), extra: queueSettingsModules()}).
		drive(t, func(_ context.Context, reg *core.Registry) error {
			writeQueueSetting(t, queueSettingsStoreOf(t, reg), pollIntervalSetting, 250*time.Millisecond)

			delay, failed := product(t, reg).pass(&worker{token: "poll-executor"})
			if failed {
				t.Fatalf("a pass over an empty table reported a read failure")
			}
			if delay != 250*time.Millisecond {
				t.Errorf("a pass over an empty table asks to read again in %s, want the stored 250ms", delay)
			}
			return nil
		})
}

// TestTheRetryDelaysInTheStoreDriveTheNextSettlement checks that the retry base
// and cap are read at the next failure settle.
func TestTheRetryDelaysInTheStoreDriveTheNextSettlement(t *testing.T) {
	e := engines[0]
	e.on(t, host{depth: core.StageInit, jobs: stayingJobs(), extra: queueSettingsModules()}).
		drive(t, func(_ context.Context, reg *core.Registry) error {
			store := queueSettingsStoreOf(t, reg)
			writeQueueSetting(t, store, retryBaseSetting, 40*time.Millisecond)
			writeQueueSetting(t, store, retryMaxSetting, time.Minute)

			insertJob(t, reg, "retried", string(jobs.StatusRunning), "retry-executor", 0)
			before := time.Now()
			product(t, reg).settleFailure(&worker{token: "retry-executor"},
				claimed{ID: "retried", Type: jobType, Attempts: 0, MaxRetries: 3}, errors.New("boom"))
			after := time.Now()

			assertClaimDeadline(t, "the retry's due time", stateOf(t, reg, jobs.JobID("retried")).available,
				before, after, 40*time.Millisecond)
			return nil
		})
}

// TestARetryCapBelowTheBaseIsClampedAtUse checks that a cap below the base is
// accepted and clamps the retry delay at the failure settle.
func TestARetryCapBelowTheBaseIsClampedAtUse(t *testing.T) {
	e := engines[0]
	e.on(t, host{depth: core.StageInit, jobs: stayingJobs(), extra: queueSettingsModules()}).
		drive(t, func(_ context.Context, reg *core.Registry) error {
			store := queueSettingsStoreOf(t, reg)
			writeQueueSetting(t, store, retryBaseSetting, 500*time.Millisecond)
			writeQueueSetting(t, store, retryMaxSetting, 50*time.Millisecond)

			insertJob(t, reg, "clamped", string(jobs.StatusRunning), "clamp-executor", 0)
			before := time.Now()
			product(t, reg).settleFailure(&worker{token: "clamp-executor"},
				claimed{ID: "clamped", Type: jobType, Attempts: 0, MaxRetries: 3}, errors.New("boom"))
			after := time.Now()

			assertClaimDeadline(t, "the clamped retry's due time", stateOf(t, reg, jobs.JobID("clamped")).available,
				before, after, 50*time.Millisecond)
			return nil
		})
}

// TestTheQueueDeclaresExactlyItsRuntimeTunables checks that exactly the five
// runtime tunables are settings and the construction values are not.
func TestTheQueueDeclaresExactlyItsRuntimeTunables(t *testing.T) {
	declared := declaredQueueSettings(t)
	for _, key := range []string{pollIntervalSetting, claimBatchSetting, leaseSetting, retryBaseSetting, retryMaxSetting} {
		if _, ok := declared[key]; !ok {
			t.Errorf("%s is not declared as a setting", key)
		}
	}
	for key := range declared {
		switch key {
		case pollIntervalSetting, claimBatchSetting, leaseSetting, retryBaseSetting, retryMaxSetting:
		default:
			t.Errorf("%s is declared as a setting, but only the poll interval, the claim batch, the lease "+
				"and the retry bounds take effect without a restart", key)
		}
	}
	for _, key := range []string{"jobs.db." + workersItem, "jobs.db." + shutdownGraceItem} {
		if _, ok := declared[key]; ok {
			t.Errorf("%s is declared as a setting, but it sizes the construction and a change still needs "+
				"a restart", key)
		}
	}
}

// TestAConfigurationCarryingARemovedQueueTunableFailsStartup checks that a
// configuration file still carrying a tunable fails as an unknown key.
func TestAConfigurationCarryingARemovedQueueTunableFailsStartup(t *testing.T) {
	for _, c := range []struct {
		item  string
		value any
		key   string
	}{
		{"poll-interval", "2s", pollIntervalSetting},
		{"claim-batch", 8, claimBatchSetting},
		{"lease", "30s", leaseSetting},
		{"retry-base", "50ms", retryBaseSetting},
		{"retry-max", "2s", retryMaxSetting},
	} {
		t.Run(c.item, func(t *testing.T) {
			err := loadJobsConfiguration(t, map[string]any{c.item: c.value})
			if !errors.Is(err, config.ErrUnknownKey) {
				t.Fatalf("a configuration carrying %s loaded with %v, want config.ErrUnknownKey", c.key, err)
			}
			if !strings.Contains(err.Error(), c.key) {
				t.Errorf("the refusal %q does not name the key %s", err, c.key)
			}
		})
	}

	t.Run("the construction values stay configuration items", func(t *testing.T) {
		if err := loadJobsConfiguration(t, stayingJobs()); err != nil {
			t.Fatalf("a configuration carrying workers and shutdown-grace was refused: %v; both size the "+
				"construction and stay configuration items", err)
		}
	})
}

// TestTheQueueTunablesUseTheirDeclaredDefaults checks the values a run with no
// stored row acts on.
func TestTheQueueTunablesUseTheirDeclaredDefaults(t *testing.T) {
	e := engines[0]
	e.on(t, host{depth: core.StageInit, jobs: stayingJobs(), extra: queueSettingsModules()}).
		drive(t, func(_ context.Context, reg *core.Registry) error {
			r := product(t, reg).tuned
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			if got := r.poll(logger); got != time.Second {
				t.Errorf("the poll interval with no stored row is %s, want 1s", got)
			}
			if got := r.batch(logger); got != 16 {
				t.Errorf("the claim batch with no stored row is %d, want 16", got)
			}
			if got := r.lease(logger); got != 2*time.Minute {
				t.Errorf("the lease with no stored row is %s, want 2m", got)
			}
			if got := r.retryBase(logger); got != time.Second {
				t.Errorf("the retry base with no stored row is %s, want 1s", got)
			}
			if got := r.retryMax(logger); got != time.Minute {
				t.Errorf("the retry cap with no stored row is %s, want 1m", got)
			}
			return nil
		})
}

// TestAQueueTunableReadFaultFallsBackToItsDeclaredDefault checks that an
// unreadable or out-of-domain setting answers its default and is reported.
func TestAQueueTunableReadFaultFallsBackToItsDeclaredDefault(t *testing.T) {
	var report bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&report, nil))
	store := faultyQueueStore{
		queueStore: newQueueStore(map[string]any{leasePath: time.Duration(0)}),
		key:        claimBatchPath,
		err:        errors.New("the store is down"),
	}
	r := newSettingsReader(store)

	if got := r.batch(logger); got != 16 {
		t.Errorf("a claim batch that could not be read is %d, want the declared 16", got)
	}
	if got := r.lease(logger); got != 2*time.Minute {
		t.Errorf("a lease outside its domain is %s, want the declared 2m", got)
	}
	logged := report.String()
	for _, key := range []string{claimBatchPath, leasePath} {
		if !strings.Contains(logged, key) {
			t.Errorf("the report does not name %s:\n%s", key, logged)
		}
	}
}

// TestASubMillisecondQueueTunableIsRefused checks that the lease and retry bounds
// refuse a sub-millisecond value at the write.
func TestASubMillisecondQueueTunableIsRefused(t *testing.T) {
	for _, key := range []string{leasePath, retryBasePath, retryMaxPath} {
		t.Run(key, func(t *testing.T) {
			e := engines[0]
			e.on(t, host{depth: core.StageInit, jobs: stayingJobs(), extra: queueSettingsModules()}).
				drive(t, func(_ context.Context, reg *core.Registry) error {
					store := queueSettingsStoreOf(t, reg)
					err := store.Set(context.Background(), speedsettings.Write{
						Layer: speedsettings.Global(), Key: key, Value: 500 * time.Microsecond, Actor: "test",
					})
					if !errors.Is(err, speedsettings.ErrInvalidValue) {
						t.Errorf("writing %s = 500us returned %v, want ErrInvalidValue", key, err)
					}
					return nil
				})
		})
	}
}

type faultyQueueStore struct {
	*queueStore
	key string
	err error
}

func (s faultyQueueStore) Get(layer speedsettings.Layer, key string, target any) error {
	if key == s.key {
		return s.err
	}
	return s.queueStore.Get(layer, key, target)
}

func declaredQueueSettings(t *testing.T) map[string]speedsettings.Item {
	t.Helper()
	for _, resource := range Module().Resources {
		if declared, ok := resource.(speedsettings.Schema); ok {
			return declared.Items
		}
	}
	t.Fatal("the module declares no settings.Schema, so its runtime tunables are not settings")
	return nil
}

func queueSettingsStoreOf(t *testing.T, reg *core.Registry) speedsettings.Store {
	t.Helper()
	store, err := core.Resolve[speedsettings.Store](reg)
	if err != nil {
		t.Fatalf("resolving the settings store: %v", err)
	}
	return store
}

func writeQueueSetting(t *testing.T, store speedsettings.Store, key string, value any) {
	t.Helper()
	write := speedsettings.Write{Layer: speedsettings.Global(), Key: key, Value: value, Actor: "test"}
	if err := store.Set(context.Background(), write); err != nil {
		t.Fatalf("writing %s: %v, want it declared as a setting", key, err)
	}
}

func claimExpiryOf(t *testing.T, reg *core.Registry, id string) int64 {
	t.Helper()
	database, err := core.Resolve[db.Database](reg)
	if err != nil {
		t.Fatalf("resolving the database: %v", err)
	}
	var at sql.NullInt64
	if err := database.DB().Raw("SELECT claim_expires_at FROM jobs WHERE id = ?", id).Row().Scan(&at); err != nil {
		t.Fatalf("reading the claim expiry of the job %s: %v", id, err)
	}
	if !at.Valid {
		t.Fatalf("the claimed job %s holds no claim expiry", id)
	}
	return at.Int64
}

func assertClaimDeadline(t *testing.T, what string, got int64, before, after time.Time, want time.Duration) {
	t.Helper()
	slack := 2 * time.Second
	lo := before.Add(want).Add(-slack).UnixMilli()
	hi := after.Add(want).Add(slack).UnixMilli()
	if got < lo || got > hi {
		t.Errorf("%s was written as %s, want about now + %s (between %s and %s)",
			what, msTime(got), want, msTime(lo), msTime(hi))
	}
}

func loadJobsConfiguration(t *testing.T, section map[string]any) error {
	t.Helper()
	document := map[string]any{"jobs": map[string]any{"db": section}}
	body, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encoding the configuration: %v", err)
	}
	locator := filepath.Join(t.TempDir(), "config.json")
	if writeErr := os.WriteFile(locator, body, 0o600); writeErr != nil {
		t.Fatalf("writing the primary config source: %v", writeErr)
	}

	reg := core.New()
	reg.Register(jsonformat.Module())
	reg.Register(filesource.Module())
	reg.Register(core.Module{
		Name:      "host",
		Resources: []any{config.HostIdentity{Prefix: "SPEEDJOBSSETTINGS", DefaultLocator: "file://" + locator}},
	})
	reg.Register(Module())
	_, err = config.Load(t.Context(), reg, nil)
	return err
}
