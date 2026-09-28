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
	keys   *keyTable
	handle *gorm.DB
	bus    event.Bus
	logger *slog.Logger
	config moduleSettings
	// cancelRefresh stops the ticker, and refreshDone is closed once its
	// goroutine has returned. Both are written in Start and read in Stop,
	// which the lifecycle driver calls one after the other on its own
	// goroutine, so they need no synchronisation of their own.
	cancelRefresh context.CancelFunc
	refreshDone   chan struct{}
	// reloadMu serialises the reload path, which is a read of the table
	// followed by a swap. Several goroutines reach it — a writer's, the
	// subscription's, the ticker's and the lifecycle driver's for the startup
	// loads — and without this,
	// a reload whose read returned an older state could swap after one that
	// read a newer state, putting the staler snapshot back. On the replica
	// that just wrote, that silently undoes a write Set has already reported
	// as done.
	reloadMu sync.Mutex
	snapshot atomic.Pointer[snapshot]
}

// Get serves a read out of the snapshot.
//
// The layer and the target are judged first, before the key: both are the
// call site's mistakes whatever key comes with them, and a check that waited
// for a declared key would let one line return an error on one run and panic
// on another. The key is looked up before the snapshot is touched, which is
// what makes a key with no declaration a certain failure instead of a value
// that reads as unset. Past that point the layers are tried in order and a row
// that failed to decode reports itself.
//
// A read hands out a value that shares nothing with the module's storage. The
// scalar kinds carry no reference, so the value resolved is already that. A
// type that carries its own text form may share storage between its copies in
// a way reflection cannot see — net.IP is a slice, and math/big.Int's methods
// write into the digits a copy shares — so its value is decoded afresh from
// the resolved text, the declared default's included. A caller computing with
// what it read cannot then rewrite the declaration or the snapshot for every
// other reader. The price is one decode per read of such an item.
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
		// Nothing has been read from the table yet, so every key would answer
		// with its declared default whatever the table holds, and the reader
		// would act on that value with nothing saying it was not the real one.
		return fmt.Errorf("%w: key %q was read before the %s module's Init loaded it. A module "+
			"reading settings in its own Init declares Requires (*settings.Store)(nil), which "+
			"initialises it after this one; a drive that reads them runs to Init at least",
			ErrNotLoaded, key, ModuleName)
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

// Set writes one value and republishes what follows from it.
//
// The order is fixed: validate, write the row, reload this replica's snapshot,
// publish the change. A failure before the statement touches nothing. A
// failure after it does not undo the write: the value is in the database, and
// reporting the write as failed because the event could not be sent would tell
// the caller the opposite of what happened.
//
// The layer is judged before anything else, for the reason Get gives.
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

// landed returns the context the work after a committed statement runs on:
// the caller's, with its values and without its cancellation.
//
// Once the statement has committed, the reload and the publish are this
// module's bookkeeping for a row already in the table rather than the caller's
// work. A caller whose request ended just then — a client that disconnected, a
// deadline that fell due — would otherwise get nil back from a write this
// replica goes on not serving and no other replica hears about. The price is
// that a write can outlast the caller's deadline by the time the reload and
// the publish take, which the database's and the bus's own timeouts bound.
func landed(ctx context.Context) context.Context { return context.WithoutCancel(ctx) }

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
//
// The old value is read back by the statement that removes the row, not taken
// from the snapshot. The snapshot holds no row for a key whose declaration is
// gone, which is the row this path exists to clear, and it trails the table
// when another replica wrote the row; the statement's own result also says
// whether a row was there at all. A delete that removed nothing publishes
// nothing, because nothing in the table changed.
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

// removedRow is what a delete reads back of the row it removed. The key of the
// table is the tenant column and the key column, so a delete removes one row
// at most.
type removedRow struct {
	Value string `gorm:"column:value"`
}

// deleteChange builds the payload of a delete.
//
// Delete does not refuse an undeclared key, so it may be clearing a row whose
// declaration is already gone. When the key is still declared its marker is
// honoured; when it is not, nothing says whether the declaration that is gone
// marked the value sensitive, and the payload reports it as an ordinary one
// rather than guessing. The sensitive marker is a property of the
// declaration, and a declaration that no longer exists cannot mark anything.
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
//
// The order is the keys' byte order, and it is put in place here rather than
// by the statement. A database orders text under its collation: SQLite
// compares bytes, while PostgreSQL uses the one the database was created with,
// and the common default there sets punctuation aside, so a-b sorts next to ab
// instead of before aa. The same rows would otherwise come back in an order
// that depends on the dialect a command runs on.
func (s *settingsStore) List(ctx context.Context, layer Layer) ([]Entry, error) {
	tenant := tenantOf(layer)
	sql := "SELECT tenant_id, key, value, version, updated_at FROM " + tableName +
		" WHERE tenant_id = ?"
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
	slices.SortFunc(entries, func(a, b Entry) int { return strings.Compare(a.Key, b.Key) })
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
//
// The statements in this module are written once for both dialects. The
// upsert and the RETURNING clause are syntax SQLite and PostgreSQL share, and
// the placeholders are GORM's: Raw rewrites each ? into the placeholder the
// handle's dialect uses, $n on PostgreSQL, before the statement reaches the
// driver.
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

// commit runs one write statement so that its result says whether it landed.
//
// A write statement here reads back what it wrote, so its result arrives after
// the server has executed it. Run on its own, it has committed by then on
// PostgreSQL, and a caller's context cancelled while the result is read makes
// the call fail for a change that is in the table — one this replica then
// does not reload and no replica hears about. The statement therefore runs in
// a transaction whose begin and commit are out of the caller's cancellation,
// while the statement itself stays inside it: a cancellation before the
// statement's result is in fails the statement and rolls the transaction
// back, and one after it no longer reaches the commit. Either way the error
// and the table agree.
//
// The one outcome left unknown is a connection lost during the commit itself,
// which only the database can settle.
func (s *settingsStore) commit(ctx context.Context, statement func(tx *gorm.DB) error) error {
	return s.handle.WithContext(landed(ctx)).Transaction(func(tx *gorm.DB) error {
		return statement(tx.WithContext(ctx))
	})
}

// reload replaces the snapshot with a freshly loaded one.
//
// It is the same path for every trigger after startup — a write of this
// replica, a delivered change event, the refresh ticker — because the
// operation is the same one: read the table, build a snapshot, swap it in. The
// swap is what makes the operation idempotent, and it has to be, since a
// replica's own event may be delivered back to it.
//
// A failure keeps the snapshot that is already in place and records the
// reason. Reads then serve a value that is behind rather than failing, which
// is what the design states as a non-guarantee and what the refresh path
// exists to close.
//
// A reload whose context was cancelled was withdrawn by whoever triggered it,
// and its failure is that cancellation rather than anything about the table:
// Stop cancels the ticker's, and a bus that stops cancels the context it
// delivers on. It is recorded at debug level only, since an error record would
// otherwise come with ordinary shutdowns. A write's reload runs on a context
// that is never cancelled, so its failures always reach the error level.
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

// swap loads the table and replaces the snapshot with what it read.
//
// The read and the replacement happen under one lock so that two concurrent
// loads cannot install their results out of order, which would leave a replica
// serving a state older than one it had already read.
//
// A replacement that brings different rows is recorded, and one that brings
// the same rows is not. The line's timestamp is the moment a snapshot took
// effect on this replica, which is what a reader needs when a value they wrote
// is not the one being served: it says how far behind this process is. Only
// the rows are compared, because the refresh path runs on a ticker and a write
// and an event, and a line per run would be a line per request that writes a
// setting.
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
		s.logger.Error("settings: publishing a change failed; "+s.convergence(),
			"key", change.Key, "tenant", tenant, "error", err)
	}
}

// convergence says how the other replicas come to serve a change whose event
// was never published: at their next refresh when it runs, and otherwise only
// through a later change's event or a restart. The interval it names is this
// replica's; each replica refreshes on its own configuration.
func (s *settingsStore) convergence() string {
	if s.config.refresh <= 0 {
		return "the periodic refresh is off, so other replicas serve the change only after a " +
			"later change's event reaches them or they restart"
	}
	return "other replicas serve the change at their next refresh (every " +
		s.config.refresh.String() + " here)"
}
