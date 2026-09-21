package settings

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
)

// maxValueBytes bounds the text of one value. The object the limit serves is
// the snapshot: every written row lives in memory, so one runaway write must
// not be able to drag the whole process down with it.
const maxValueBytes = 1 << 20

// settingsStore is the module's product and the implementation of Store.
//
// It holds the key table, which is built once and never changes, and the
// snapshot, which is replaced as a whole. The snapshot sits behind an atomic
// pointer because the replacement happens on a writer's goroutine, on a
// subscription's goroutine and on the refresh ticker's, while reads happen on
// every other goroutine a consumer has.
type settingsStore struct {
	keys    *keyTable
	handle  *gorm.DB
	dialect db.Dialect
	bus     event.Bus
	logger  *slog.Logger
	config  moduleSettings
	// cancelRefresh stops the ticker. It is written in Start and read in Stop,
	// which the lifecycle driver calls one after the other on its own
	// goroutine, so it needs no synchronisation of its own.
	cancelRefresh context.CancelFunc
	// reloadMu serialises the reload path, which is a read of the table
	// followed by a swap. Four goroutines reach it — a writer's, the
	// subscription's, the ticker's and the initial load's — and without this,
	// a reload whose read returned an older state could swap after one that
	// read a newer state, putting the staler snapshot back. On the replica
	// that just wrote, that silently undoes a write Set has already reported
	// as done.
	reloadMu sync.Mutex
	snapshot atomic.Pointer[snapshot]
}

// Get serves a read out of the snapshot.
//
// The key is looked up before the snapshot is touched, which is what makes a
// key with no declaration a certain failure instead of a value that reads as
// unset. Past that point the layers are tried in order and a row that failed
// to decode reports itself.
func (s *settingsStore) Get(layer Layer, key string, target any) error {
	entry, declared := s.keys.lookup(key)
	if !declared {
		return fmt.Errorf("%w: %q. Add a declaration covering it, or correct the key at the "+
			"call site", ErrUnknownKey, key)
	}
	value, err := s.snapshot.Load().value(entry, layer)
	if err != nil {
		return err
	}
	return assignTarget(entry, target, value)
}

// Set writes one value and republishes what follows from it.
//
// The order is fixed: validate, write the row, reload this replica's snapshot,
// publish the change. A failure before the statement touches nothing. A
// failure after it does not undo the write: the value is in the database, and
// reporting the write as failed because the event could not be sent would tell
// the caller the opposite of what happened.
func (s *settingsStore) Set(ctx context.Context, w Write) error {
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
	s.reload(ctx)
	s.publish(ctx, w.Layer, changeFor(entry, old, text, w.Actor, version, false))
	return nil
}

// changeFor builds the payload of one change.
//
// It is the single place a payload is built, because the sensitive rule is a
// property of the payload rather than of the call site: an event may reach a
// message backend and every subscriber on it, and a credential does not earn
// that reach by being audited. A sensitive item's change therefore carries the
// key and the fact that it changed, and both values are dropped here rather
// than at each caller.
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

// Delete removes one explicit row. A row that is not there is not an error:
// the caller asked for the row to be gone, and it is.
//
// Delete does not consult the key table. It is the path that clears rows left
// behind after a declaration is removed — the one window in which a row for an
// undeclared key can still be reached — and both Get and Set refuse such a key
// before the table is consulted at all.
func (s *settingsStore) Delete(ctx context.Context, layer Layer, key, actor string) error {
	tenant := tenantOf(layer)
	old, _ := s.snapshot.Load().explicitText(layer, key)
	sql := "DELETE FROM " + tableName + " WHERE tenant_id = " + bind(s.dialect, 1) +
		" AND key = " + bind(s.dialect, 2)
	if err := s.handle.WithContext(ctx).Exec(sql, tenant, key).Error; err != nil {
		return fmt.Errorf("settings: deleting key %q: %w", key, err)
	}
	s.reload(ctx)
	s.publish(ctx, layer, s.deleteChange(key, old, actor))
	return nil
}

// deleteChange builds the payload of a delete.
//
// Delete does not refuse an undeclared key, so it may be clearing a row whose
// declaration is already gone. When the key is still declared its marker is
// honoured; when it is not, nothing says whether the value used to be
// sensitive, and the payload reports it as an ordinary one rather than
// guessing. The sensitive marker is a property of the declaration, and a
// declaration that no longer exists cannot mark anything.
func (s *settingsStore) deleteChange(key, old, actor string) Change {
	entry, declared := s.keys.lookup(key)
	if !declared {
		return Change{Key: key, Old: old, Deleted: true, Actor: actor}
	}
	return changeFor(entry, old, "", actor, 0, true)
}

// List reports the explicit rows of one layer, in key order. It hands out what
// was written rather than what a read would resolve to: nothing falls back
// here.
func (s *settingsStore) List(ctx context.Context, layer Layer) ([]Entry, error) {
	tenant := tenantOf(layer)
	sql := "SELECT tenant_id, key, value, version, updated_at FROM " + tableName +
		" WHERE tenant_id = " + bind(s.dialect, 1) + " ORDER BY key"
	var rows []listRow
	if err := s.handle.WithContext(ctx).Raw(sql, tenant).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("settings: listing the rows of %s: %w",
			describeLayer(layer), err)
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
	return entries, nil
}

// listRow is one row of List's result.
type listRow struct {
	TenantID  string `gorm:"column:tenant_id"`
	Key       string `gorm:"column:key"`
	Value     string `gorm:"column:value"`
	Version   uint64 `gorm:"column:version"`
	UpdatedAt time.Time
}

// writeRow upserts one row and returns the version it now holds.
//
// The version is read back by the same statement that increments it. A second
// query would return whatever another writer stored in between, and the
// version in the change event would then not be this write's.
//
// The layer is flattened to the tenant column here, which is the last point
// before the statement: the column is what the row is keyed by, and the global
// layer is the row whose column is empty.
func (s *settingsStore) writeRow(ctx context.Context, layer Layer, key, text string) (uint64, error) {
	sql := "INSERT INTO " + tableName + " (tenant_id, key, value, version, updated_at) VALUES (" +
		bind(s.dialect, 1) + ", " + bind(s.dialect, 2) + ", " + bind(s.dialect, 3) + ", 1, " +
		bind(s.dialect, 4) + ") ON CONFLICT (tenant_id, key) DO UPDATE SET value = EXCLUDED.value, " +
		"version = " + tableName + ".version + 1, updated_at = EXCLUDED.updated_at RETURNING version"
	var version uint64
	err := s.handle.WithContext(ctx).Raw(sql, tenantOf(layer), key, text, time.Now().UTC()).
		Row().Scan(&version)
	if err != nil {
		return 0, fmt.Errorf("settings: writing key %q: %w", key, err)
	}
	return version, nil
}

// reload replaces the snapshot with a freshly loaded one.
//
// It is the same path for all three triggers — a write of this replica, a
// delivered change event, and the refresh ticker — because the operation is
// the same one: read the table, build a snapshot, swap it in. The swap is what
// makes the operation idempotent, and it has to be, since a replica's own
// event may be delivered back to it.
//
// A failure keeps the snapshot that is already in place and records the
// reason. Reads then serve a value that is behind rather than failing, which
// is what the design states as a non-guarantee and what the refresh path
// exists to close.
func (s *settingsStore) reload(ctx context.Context) {
	if _, err := s.swap(ctx); err != nil {
		s.logger.Error("settings: reloading the snapshot failed; the previous one stays in use",
			"error", err)
	}
}

// swap loads the table and replaces the snapshot with what it read.
//
// The read and the replacement happen under one lock so that two concurrent
// loads cannot install their results out of order, which would leave a replica
// serving a state older than one it had already read.
func (s *settingsStore) swap(ctx context.Context) (*snapshot, error) {
	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()
	loaded, err := loadSnapshot(ctx, s.handle, s.keys)
	if err != nil {
		return nil, err
	}
	s.snapshot.Store(loaded)
	return loaded, nil
}

// publish sends a change event.
//
// A failure here is recorded and goes no further. The value is in the
// database; rolling back a successful write because its notification could not
// be sent would make the caller see a failure for a write that happened.
// Publish returning nil is not a stronger statement either: it says the bus
// accepted the event, not that any replica has reloaded.
func (s *settingsStore) publish(ctx context.Context, layer Layer, change Change) {
	tenant := tenantOf(layer)
	evt := event.Event{
		Type: ChangedEventType,
		// The tenant the change applies to travels on the envelope; the global
		// row leaves it empty, which makes it a system-level event.
		TenantID: tenant,
		Payload:  change,
	}
	if err := s.bus.Publish(ctx, evt); err != nil {
		// Key and tenant only: a value never reaches a log line, which is why
		// the sensitive marker has nothing to do here.
		s.logger.Error("settings: publishing a change failed; other replicas converge on "+
			"their next refresh", "key", change.Key, "tenant", tenant, "error", err)
	}
}

// bind renders the placeholder for the nth argument of a statement.
//
// Native SQL reaches the driver unchanged, so the two dialects' placeholder
// syntaxes are this module's problem: PostgreSQL numbers its parameters,
// SQLite does not.
func bind(dialect db.Dialect, n int) string {
	if dialect == db.Postgres {
		return "$" + strconv.Itoa(n)
	}
	return "?"
}
