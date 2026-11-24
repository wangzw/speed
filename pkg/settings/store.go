package settings

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
)

const maxValueBytes = 1 << 20

type settingsStore struct {
	keys          *keyTable
	handle        *gorm.DB
	bus           event.Bus
	logger        *slog.Logger
	config        moduleSettings
	cancelRefresh context.CancelFunc
	refreshDone   chan struct{}
	// reloadMu serialises the reload path, a read of the table followed by a swap;
	// without it a staler read could swap last and undo a write already reported done.
	reloadMu sync.Mutex
	snapshot atomic.Pointer[snapshot]
	// connectWithin is how long a call waits for a connection; a test widens it to
	// tell the connection wait apart from the engine busy timeout.
	connectWithin time.Duration
}

// Get serves a read out of the snapshot. The layer and target are judged first and
// the key before the snapshot is touched; a type that carries its own text form is
// decoded afresh so a read shares no storage with the module.
func (s *settingsStore) Get(layer Layer, key string, target any) error {
	tenantOf(layer)
	entry, declared := s.keys.lookup(key)
	requireTarget(target, key, entry, declared)
	if !declared {
		return fmt.Errorf("%w: %q. Add a declaration covering it, or correct the key at the "+
			"call site", ErrUnknownKey, key)
	}
	current := s.snapshot.Load()
	if current == nil {
		return notLoaded(key)
	}
	resolved, err := current.value(entry, layer)
	if err != nil {
		return err
	}
	value := resolved.value
	if entry.kind == kindText {
		if value, err = decodeValue(entry, resolved.text); err != nil {
			return err
		}
	}
	return assignTarget(entry, target, value)
}

// WrittenAt serves a revision out of the snapshot, judging the layer and key first
// as Get does; a layer holding no row answers absent.
func (s *settingsStore) WrittenAt(layer Layer, key string) (time.Time, bool, error) {
	tenantOf(layer)
	if _, declared := s.keys.lookup(key); !declared {
		return time.Time{}, false, fmt.Errorf("%w: %q. Add a declaration covering it, or correct "+
			"the key at the call site", ErrUnknownKey, key)
	}
	current := s.snapshot.Load()
	if current == nil {
		return time.Time{}, false, notLoaded(key)
	}
	at, present := current.writtenAt(key, layer)
	return at, present, nil
}

// Read serves several layers from one snapshot, judging layers, key and targets
// first. A layer with no row answers the declared default with Present false.
func (s *settingsStore) Read(key string, reads ...LayerRead) ([]LayerReadResult, error) {
	for _, read := range reads {
		tenantOf(read.Layer)
	}
	entry, declared := s.keys.lookup(key)
	for _, read := range reads {
		requireTarget(read.Target, key, entry, declared)
	}
	if !declared {
		return nil, fmt.Errorf("%w: %q. Add a declaration covering it, or correct the key at the "+
			"call site", ErrUnknownKey, key)
	}
	current := s.snapshot.Load()
	if current == nil {
		return nil, notLoaded(key)
	}
	results := make([]LayerReadResult, len(reads))
	for i, read := range reads {
		row, found, err := current.layer(entry.key, read.Layer)
		if err != nil {
			return nil, err
		}
		text, value := entry.defText, entry.def
		if found {
			text, value = row.text, row.value
		}
		if entry.kind == kindText {
			if value, err = decodeValue(entry, text); err != nil {
				return nil, err
			}
		}
		if err := assignTarget(entry, read.Target, value); err != nil {
			return nil, err
		}
		results[i] = LayerReadResult{WrittenAt: row.updatedAt, Present: found}
	}
	return results, nil
}

func notLoaded(key string) error {
	return fmt.Errorf("%w: key %q was read before the %s module's Init loaded it. A module "+
		"reading settings in its own Init declares Requires (*settings.Store)(nil), which "+
		"initialises it after this one; a drive that reads them runs to Init at least",
		ErrNotLoaded, key, ModuleName)
}

// Set writes one value: validate, write the row, reload this replica, publish; a
// failure after the write does not undo it.
func (s *settingsStore) Set(ctx context.Context, w Write) error {
	tenantOf(w.Layer)
	entry, declared := s.keys.lookup(w.Key)
	if !declared {
		return fmt.Errorf("%w: %q. Add a declaration covering it, or correct the key at the "+
			"call site", ErrUnknownKey, w.Key)
	}
	text, err := encodeValue(entry, w.Value)
	if err != nil {
		return err
	}
	if len(text) > maxValueBytes {
		return fmt.Errorf("%w: key %q holds %d bytes, and the limit is %d. Every written row "+
			"is kept in memory, so one value cannot be allowed to grow without bound",
			ErrInvalidValue, w.Key, len(text), maxValueBytes)
	}

	old, _ := s.snapshot.Load().explicitText(w.Layer, w.Key)
	version, err := s.writeRow(ctx, w.Layer, w.Key, text)
	if err != nil {
		return err
	}
	after := landed(ctx)
	s.reload(after)
	s.publish(after, w.Layer, changeFor(entry, old, text, w.Actor, version, false))
	return nil
}

// landed returns the context the work after a committed statement runs on: the
// caller's values without its cancellation.
func landed(ctx context.Context) context.Context { return context.WithoutCancel(ctx) }

// changeFor builds the payload of one change, the single place it is built; a
// sensitive item's values are dropped here.
func changeFor(entry keyEntry, old, fresh, actor string, version uint64, deleted bool) Change {
	change := Change{
		Key:       entry.key,
		Old:       old,
		New:       fresh,
		Deleted:   deleted,
		Actor:     actor,
		Version:   version,
		Sensitive: entry.item.Sensitive,
	}
	if entry.item.Sensitive {
		change.Old = ""
		change.New = ""
	}
	return change
}

// Delete removes one explicit row; a missing row is not an error. It does not
// consult the key table, and the old value is read back by the deleting statement.
func (s *settingsStore) Delete(ctx context.Context, layer Layer, key, actor string) error {
	tenant := tenantOf(layer)
	sql := "DELETE FROM " + tableName + " WHERE tenant_id = ? AND key = ? RETURNING value"
	var removed []removedRow
	err := s.commit(ctx, func(tx *gorm.DB) error {
		return tx.Raw(sql, tenant, key).Scan(&removed).Error
	})
	if err != nil {
		return fmt.Errorf("settings: deleting key %q: %w", key, err)
	}
	after := landed(ctx)
	s.reload(after)
	if len(removed) == 0 {
		return nil
	}
	s.publish(after, layer, s.deleteChange(key, removed[0].Value, actor))
	return nil
}

type removedRow struct {
	Value string `gorm:"column:value"`
}

// deleteChange builds the payload of a delete; a declaration that is gone cannot
// mark the value sensitive, so it is reported as ordinary.
func (s *settingsStore) deleteChange(key, old, actor string) Change {
	entry, declared := s.keys.lookup(key)
	if !declared {
		return Change{Key: key, Old: old, Deleted: true, Actor: actor}
	}
	return changeFor(entry, old, "", actor, 0, true)
}

// List reports the explicit rows of one layer in the keys' byte order, put in place
// here rather than by the statement.
func (s *settingsStore) List(ctx context.Context, layer Layer) ([]Entry, error) {
	tenant := tenantOf(layer)
	sql := "SELECT tenant_id, key, value, version, updated_at FROM " + tableName +
		" WHERE tenant_id = ?"
	var rows []listRow
	if err := s.onConnection(ctx, func(session *gorm.DB) error {
		return session.Raw(sql, tenant).Scan(&rows).Error
	}); err != nil {
		return nil, fmt.Errorf("settings: listing the rows of %s: %w", describeLayer(layer), err)
	}
	entries := make([]Entry, 0, len(rows))
	for _, r := range rows {
		entries = append(entries, Entry{
			Tenant:    r.TenantID,
			Key:       r.Key,
			Value:     r.Value,
			Version:   r.Version,
			UpdatedAt: r.UpdatedAt,
		})
	}
	slices.SortFunc(entries, func(a, b Entry) int { return strings.Compare(a.Key, b.Key) })
	return entries, nil
}

// Declared reports the key table's listing; the copy is the caller's own.
func (s *settingsStore) Declared() []DeclaredItem {
	return slices.Clone(s.keys.listing)
}

type listRow struct {
	TenantID  string `gorm:"column:tenant_id"`
	Key       string `gorm:"column:key"`
	Value     string `gorm:"column:value"`
	Version   uint64 `gorm:"column:version"`
	UpdatedAt time.Time
}

func (s *settingsStore) writeRow(ctx context.Context, layer Layer, key, text string) (uint64, error) {
	sql := "INSERT INTO " + tableName + " (tenant_id, key, value, version, updated_at) " +
		"VALUES (?, ?, ?, 1, ?) ON CONFLICT (tenant_id, key) DO UPDATE SET value = EXCLUDED.value, " +
		"version = " + tableName + ".version + 1, updated_at = EXCLUDED.updated_at RETURNING version"
	var version uint64
	err := s.commit(ctx, func(tx *gorm.DB) error {
		return tx.Raw(sql, tenantOf(layer), key, text, time.Now().UTC()).Row().Scan(&version)
	})
	if err != nil {
		return 0, fmt.Errorf("settings: writing key %q: %w", key, err)
	}
	return version, nil
}

// commit runs one write statement in a transaction whose begin and commit are out
// of the caller's cancellation while the statement stays inside it; the connection
// is taken first, under that cancellation and within connectionWait.
func (s *settingsStore) commit(ctx context.Context, statement func(tx *gorm.DB) error) error {
	return s.onConnection(ctx, func(session *gorm.DB) error {
		return session.WithContext(landed(ctx)).Transaction(func(tx *gorm.DB) error {
			return statement(tx.WithContext(ctx))
		})
	})
}

func (s *settingsStore) onConnection(ctx context.Context, fn func(session *gorm.DB) error) error {
	return db.Connection(ctx, s.handle, s.connectWithin, fn)
}

// A lock another connection holds is waited for as the engine waits; this bound
// does not reach it.
const connectionWait = 5 * time.Second

// reload replaces the snapshot with a freshly loaded one, keeping the one in place
// on failure; a cancelled reload is a withdrawn trigger and is logged at debug.
func (s *settingsStore) reload(ctx context.Context) {
	if _, err := s.swap(ctx); err != nil {
		if ctx.Err() != nil {
			s.logger.Debug("settings: a reload was withdrawn before it finished; the previous "+
				"snapshot stays in use", "error", err)
			return
		}
		s.logger.Error("settings: reloading the snapshot failed; the previous one stays in use",
			"error", err)
	}
}

func (s *settingsStore) swap(ctx context.Context) (*snapshot, error) {
	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()
	loaded, err := loadSnapshot(ctx, s.handle, s.keys)
	if err != nil {
		return nil, err
	}
	previous := s.snapshot.Swap(loaded)
	if previous == nil || !sameRows(previous, loaded) {
		s.logger.Info("settings: the snapshot now holds different rows",
			"rows", len(loaded.values), "undecodable", len(loaded.broken))
	}
	return loaded, nil
}

// publish sends a change event; a failure is recorded and goes no further, since
// the value is already in the database.
func (s *settingsStore) publish(ctx context.Context, layer Layer, change Change) {
	tenant := tenantOf(layer)
	evt := event.Event{
		Type:     ChangedEventType,
		TenantID: tenant,
		Payload:  change,
	}
	if err := s.bus.Publish(ctx, evt); err != nil {
		s.logger.Error("settings: publishing a change failed; "+s.convergence(),
			"key", change.Key, "tenant", tenant, "error", err)
	}
}

func (s *settingsStore) convergence() string {
	if s.config.refresh <= 0 {
		return "the periodic refresh is off, so other replicas serve the change only after a " +
			"later change's event reaches them or they restart"
	}
	return "other replicas serve the change at their next refresh (every " +
		s.config.refresh.String() + " here)"
}
