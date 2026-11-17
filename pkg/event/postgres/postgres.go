// Package postgres is the PostgreSQL implementation of the event-bus capability
// pkg/event declares.
//
// It is the sister of pkg/kv/postgres: both exist for a deployment that already
// runs PostgreSQL and does not want to bring up Redis or NATS for one more
// thing, and both keep their own table.
//
// An event goes into an outbox table and a NOTIFY announces it. The outbox is
// the source of truth and the notification is only a hint: NOTIFY has no way to
// reach a session that was not listening when it was sent, so a replica that
// was disconnected learns about what it missed by reading the table, not by
// receiving a notification.
//
// Each replica keeps a listening connection of its own — taken from outside the
// pool, never returned to it, because a connection running LISTEN would
// otherwise be handed out for ordinary queries. A replica is this process: its
// starting point is the database's snapshot when it opens, held in memory, and
// what it has delivered since is marked in a table under an identity of its
// own. A replica that restarts is a new one, starting from that moment, which
// is the same "no history for a late subscriber" behaviour the capability
// states.
//
// It needs PostgreSQL 13 or later: an outbox entry records the transaction
// that wrote it as an xid8, and a replica's starting point is a pg_snapshot,
// both of which arrived in that release. An older server fails the startup
// when the outbox is created.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/event/internal/dispatch"
	"github.com/vislake/speed/pkg/event/internal/replica"
	"github.com/vislake/speed/pkg/event/internal/tracing"
	"github.com/vislake/speed/pkg/internal/pgpool"
)

const (
	// ModuleName is this implementation's name in the registry.
	ModuleName = "event.postgres"
	// ConfigNamespace is where this implementation's input items are mounted.
	ConfigNamespace = ModuleName
	// systemName is this implementation's backend as it appears in the
	// messaging.system attribute of a delivery's span.
	systemName = "postgres"
	// table is this implementation's own outbox.
	table = "event_outbox"
	// channel is the NOTIFY channel. It is a fixed name rather than one per
	// assembly: PostgreSQL channels are per database, and two assemblies
	// sharing a database are already sharing the outbox below.
	channel = "speed_events"
	// pollInterval bounds how long a replica waits for a notification before
	// looking at the table anyway, which is what covers a notification that was
	// sent while it was not listening.
	pollInterval = 2 * time.Second
	// batchSize bounds how many rows one pass reads.
	batchSize = 64
	// deliveredTable records, per replica, which outbox entries that replica has
	// handed to its subscribers.
	//
	// It exists because a sequence is not a cursor. BIGSERIAL hands out ids in
	// the order transactions reach for them, not in the order they commit, so a
	// watermark that only moves forward skips whatever committed late behind
	// it — permanently, and silently, because the row is still there and looks
	// for all the world like one that had been read. Marking each entry per
	// replica has no such hole to fall into: an entry is delivered or it is
	// not, and a late commit is simply one that has not been marked yet.
	deliveredTable = "event_delivered"
	// retention is how long an outbox entry stays after it was published. It
	// bounds both tables, which would otherwise grow without limit. The window
	// is what a running replica can catch up across — a reader that has been
	// unable to reach the database for longer than this does not receive what
	// was published meanwhile — and it is nothing a restart catches up across:
	// a replica is this process, and a new process starts at the outbox's end.
	retention = 24 * time.Hour
	// retireEvery bounds how often the retention pass runs. Retiring on every
	// poll would turn two large DELETEs — one of them an anti-join over the
	// delivered table — into a standing load at the poll interval, while the
	// window they enforce is a day wide.
	retireEvery = time.Hour
	// createOutbox runs only when the outbox is missing, and the table being
	// there is this package's own record of having created it — this capability
	// is below the database module in the dependency graph and has no migration
	// machinery to borrow.
	//
	// xact is the transaction that wrote the entry. It is what a replica tells
	// an entry published before it opened from one published after by: see
	// bus.origin. identifier is the event's own identifier, which every
	// replica's subscribers receive; id is this table's row key and orders the
	// reads.
	createOutbox = `CREATE TABLE IF NOT EXISTS ` + table + ` (
		id            BIGSERIAL PRIMARY KEY,
		type          TEXT NOT NULL,
		tenant        TEXT NOT NULL DEFAULT '',
		payload       TEXT NOT NULL,
		created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
		xact          xid8 NOT NULL DEFAULT pg_current_xact_id(),
		identifier    TEXT NOT NULL DEFAULT '',
		trace_context TEXT NOT NULL DEFAULT ''
	)`
	// createDelivered runs only when the delivered table is missing.
	createDelivered = `CREATE TABLE IF NOT EXISTS ` + deliveredTable + ` (
		event_id BIGINT NOT NULL,
		replica  TEXT NOT NULL,
		PRIMARY KEY (event_id, replica)
	)`
	// replicaIndex is the delivered table's index by replica, which a replica
	// reading what it has not marked goes through. createReplicaIndex runs only
	// when it is missing.
	replicaIndex       = deliveredTable + `_replica`
	createReplicaIndex = `CREATE INDEX IF NOT EXISTS ` + replicaIndex + ` ON ` + deliveredTable + ` (replica, event_id)`
	// tablePresent asks the catalog whether the table named by its parameter is
	// there, and indexPresent whether the index named by its second is on the
	// table named by its first. Both resolve a table the way every other
	// statement here does: through the search path.
	tablePresent = `SELECT to_regclass($1) IS NOT NULL`
	indexPresent = `SELECT EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
		WHERE i.indrelid = to_regclass($1) AND c.relname = $2)`
	// columnPresent asks whether the table named by its first parameter has the
	// column named by its second, resolving the table the same way.
	columnPresent = `SELECT EXISTS (SELECT 1 FROM pg_attribute
		WHERE attrelid = to_regclass($1) AND attname = $2 AND attnum > 0 AND NOT attisdropped)`
	// schemaLock is the advisory lock the table creation runs under. Its value
	// is the bytes of "speed_ev", so it is recognisable in pg_locks and does
	// not collide with a lock another part of the assembly takes by number.
	schemaLock int64 = 0x73706565645f6576
)

// init registers this module with the process registry.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is this module's descriptor.
func Module() core.Module {
	defaults := pgpool.Defaults()
	m := event.NewModule(event.Spec[pgpool.Config]{
		ModuleName:      ModuleName,
		ConfigNamespace: ConfigNamespace,
		Config:          &defaults,
		Items:           pgpool.Items(),
		Stance:          stance,
		Trace:           tracing.Provider,
		Open:            open,
		Start:           start,
		// The connection string carries credentials; it has no shape to match,
		// so the factory registers the value itself as a literal redaction rule
		// before this module reaches the server.
		Secrets: func(cfg *pgpool.Config) []string { return []string{cfg.DSN} },
	})
	// The optional tracer requirement is appended here rather than declared by
	// the root package, which cannot name the telemetry provider's type.
	m.Requires = append(m.Requires, tracing.Requirement())
	return m
}

// stance states this implementation's stance, or reports what it cannot start
// without.
//
// A module taken in at run level has to start, so an absent connection string is
// a startup failure naming the item rather than a stand-down, and a configured
// one states StateEnabled, which is what makes a second configured backend fail
// the startup.
func stance(cfg *pgpool.Config) (core.Enablement, error) {
	if strings.TrimSpace(cfg.DSN) == "" {
		return core.Enablement{}, fmt.Errorf("%s.%s gives no connection string, and this "+
			"implementation cannot start without one: it was taken in at run level and delivers the "+
			"event capability, which it reaches over PostgreSQL. Set it, or leave this implementation "+
			"out of the command tree and let another backend deliver the capability",
			ConfigNamespace, pgpool.DSNKey)
	}
	return core.Enablement{State: core.StateEnabled}, nil
}

// open builds the pool, makes sure the outbox is there, and takes the listening
// connection.
func open(ctx context.Context, cfg *pgpool.Config, logger *slog.Logger, tracer event.TracerProvider) (event.Bus, error) {
	pool, err := pgpool.Connect(ctx, ModuleName, *cfg)
	if err != nil {
		return nil, err
	}
	if createErr := createTables(ctx, pool); createErr != nil {
		pool.Close()
		return nil, fmt.Errorf("%s: preparing %s and %s: %w", ModuleName, table, deliveredTable, createErr)
	}
	// The listening connection is outside the pool on purpose: a connection
	// running LISTEN would otherwise be handed to an ordinary query, where the
	// subscription would sit idle and the query would wait on it.
	//
	// It is built from the pool's own parsed configuration rather than from the
	// connection string again. The pool normalises that string before parsing
	// it; a second parse of the raw text would fail the startup on a trailing
	// newline, and would read leading whitespace as a keyword/value string
	// naming nothing, sending the listening session to the libpq defaults — a
	// different server or database from the one every publish writes to.
	listen, err := pgx.ConnectConfig(ctx, pool.Config().ConnConfig.Copy())
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("%s: taking the listening connection: %w", ModuleName, err)
	}
	if _, err := listen.Exec(ctx, "LISTEN "+channel); err != nil {
		_ = listen.Close(ctx)
		pool.Close()
		return nil, fmt.Errorf("%s: listening on %s: %w", ModuleName, channel, err)
	}
	// A subscriber that arrives now is not owed what was published before it.
	// What "before" means is fixed here, as the set of transactions the
	// database counts as committed at this moment: see bus.origin.
	var origin string
	if err := pool.QueryRow(ctx, `SELECT pg_current_snapshot()::text`).Scan(&origin); err != nil {
		_ = listen.Close(ctx)
		pool.Close()
		return nil, fmt.Errorf("%s: taking this replica's starting point: %w", ModuleName, err)
	}
	return &bus{
		pool:      pool,
		listen:    listen,
		logger:    logger,
		replica:   replica.Name(),
		origin:    origin,
		pollEvery: pollInterval,
		table:     dispatch.New("event/postgres", systemName, logger, tracer, tracing.Propagator()),
	}, nil
}

// createTables makes sure this implementation's tables are there, one replica
// at a time, creating only what is missing.
//
// IF NOT EXISTS does not make concurrent creation safe: two sessions that both
// find the table missing both go on to create it, and the one that commits
// second fails on the catalog's unique indexes. Replicas of one assembly
// starting together against a new database do exactly that, so every one of
// them but the first would fail its startup. The transaction-scoped advisory
// lock turns the creations into a queue: the first creates, and each after it
// finds the tables there once it holds the lock. It is released with the
// transaction, whatever becomes of it.
//
// The catalog is asked before anything is created, and nothing is created that
// is there. PostgreSQL checks CREATE on the schema before it looks for the
// table an IF NOT EXISTS names, and ownership of the table before it looks for
// an index: a start that ran the statements regardless would need both
// privileges on every start, and a service running under a role that may only
// read and write rows would fail on a database whose tables are all there. The
// lock and the catalog reads need no privilege of their own.
func createTables(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, schemaLock); err != nil {
		return fmt.Errorf("waiting for the schema lock: %w", err)
	}
	for _, object := range []struct {
		what, create string
		probe        string
		args         []any
	}{
		{table, createOutbox, tablePresent, []any{table}},
		{deliveredTable, createDelivered, tablePresent, []any{deliveredTable}},
		{replicaIndex, createReplicaIndex, indexPresent, []any{deliveredTable, replicaIndex}},
	} {
		var present bool
		if err := tx.QueryRow(ctx, object.probe, object.args...).Scan(&present); err != nil {
			return fmt.Errorf("asking whether %s is there: %w", object.what, err)
		}
		if present {
			continue
		}
		if _, err := tx.Exec(ctx, object.create); err != nil {
			return fmt.Errorf("creating %s: %w", object.what, err)
		}
	}
	if err := addMissingColumns(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// addedColumns are the outbox columns a table created before they existed
// lacks, with the definition each is added with. An outbox that is already
// there is not created again, so without this every statement naming one of
// them would fail on it.
var addedColumns = []struct{ name, definition string }{
	{"xact", "xid8 NOT NULL DEFAULT pg_current_xact_id()"},
	{"identifier", "TEXT NOT NULL DEFAULT ''"},
	{"trace_context", "TEXT NOT NULL DEFAULT ''"},
}

// addMissingColumns adds each of addedColumns the outbox lacks.
//
// The catalog is asked first rather than running ADD COLUMN IF NOT EXISTS on
// every start, for two reasons. ALTER TABLE takes the table's strongest lock
// before it looks, and that lock would queue behind every running publish and
// hold up every publish behind it, once per replica start, for a change that is
// already there. And it requires ownership of the table before it looks, which
// a role that may only read and write rows does not have.
func addMissingColumns(ctx context.Context, tx pgx.Tx) error {
	for _, column := range addedColumns {
		var present bool
		if err := tx.QueryRow(ctx, columnPresent, table, column.name).Scan(&present); err != nil {
			return fmt.Errorf("reading the columns of %s: %w", table, err)
		}
		if present {
			continue
		}
		if _, err := tx.Exec(ctx,
			`ALTER TABLE `+table+` ADD COLUMN IF NOT EXISTS `+column.name+` `+column.definition); err != nil {
			return fmt.Errorf("adding %s to %s: %w", column.name, table, err)
		}
	}
	return nil
}

// start freezes the table, opens the bus to publishing, and begins reading.
//
// It takes up this implementation's own bus and refuses any other: a bus the
// host supplied for the module has no table here to freeze and no readers here
// to start.
func start(ctx context.Context, b event.Bus) error {
	local, ok := b.(*bus)
	if !ok {
		return dispatch.ForeignBus(ModuleName, b)
	}
	return local.begin(ctx)
}

type bus struct {
	pool   *pgxpool.Pool
	listen *pgx.Conn
	logger *slog.Logger

	// replica is this instance's identity in the delivered table. It is what
	// makes each entry's delivery a fact about this replica rather than a
	// position in a sequence.
	replica string
	// origin is the database's snapshot at the moment this replica opened, in
	// its text form. An entry whose writing transaction is visible in it was
	// committed before this replica existed, and is not this replica's to
	// deliver; every other entry is, once it commits.
	//
	// It is what lets a replica start without writing anything. Marking every
	// entry already in the outbox as delivered would say the same, at the cost
	// of one mark per entry on every process start — every command that takes
	// this bus in, a short-lived one included — held until the entries retire.
	// And it keeps what the marks exist for: a transaction still in flight
	// when the replica opened is not visible in the snapshot, whatever its
	// entry's id, so an entry that commits late is still delivered.
	origin string

	// table holds the subscriptions, the stage, and the reader's lifetime.
	table *dispatch.Table
	// types is what the table held when it froze. The reader is the only one
	// that reads it, after begin has written it.
	types []string

	// lastRetire is when the retention pass last ran. Only the read goroutine
	// touches it, so it needs no lock of its own.
	lastRetire time.Time
	// pollEvery is how long this replica waits for a notification before
	// looking at the table anyway. It is pollInterval unless a test replaces
	// it, and the read goroutine reads it once it starts; a test sets it
	// before begin.
	pollEvery time.Duration
}

// Subscribe registers a handler for one event type, under the name its
// delivery's span is recorded under. It panics once the reader has started at
// the start stage, or when the name is empty.
func (b *bus) Subscribe(eventType string, name string, h event.Handler) {
	b.table.Subscribe(eventType, name, h)
}

// Publish writes the event to the outbox and announces it.
//
// Both happen in one transaction, and the order is the point: NOTIFY has no way
// to reach a session that is not listening, so the row has to be there before
// the announcement, or a replica that was between connections would never learn
// about it.
func (b *bus) Publish(ctx context.Context, evt event.Event) error {
	payload, err := json.Marshal(evt.Payload)
	if err != nil {
		return fmt.Errorf("%s: event %q carries a payload that cannot be serialized: %w",
			ModuleName, evt.Type, err)
	}
	if stageErr := b.table.Accepting(); stageErr != nil {
		return stageErr
	}
	evt = b.table.Stamp(ctx, evt)
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO `+table+` (identifier, type, tenant, payload, trace_context) `+
			`VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		evt.ID, evt.Type, evt.TenantID, string(payload),
		dispatch.EncodeTraceContext(evt.TraceContext)).Scan(&id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify($1, $2)`, channel, fmt.Sprint(id)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Stop is the stop stage: publishing is refused, the reader reads nothing
// more, and the batch it already read is delivered and marked within the
// table's grace.
func (b *bus) Stop(_ context.Context) error { return b.table.Stop() }

// Close stops the bus if the stop stage has not, and releases both
// connections.
func (b *bus) Close(ctx context.Context) error {
	stopErr := b.table.Close()
	err := b.listen.Close(ctx)
	b.pool.Close()
	return errors.Join(stopErr, err)
}

// begin freezes the table and starts the reader.
func (b *bus) begin(ctx context.Context) error {
	types, first := b.table.Freeze(ctx)
	if !first {
		return nil
	}
	b.types = types
	b.table.Go(b.read)
	return nil
}

// read delivers what the outbox holds for this replica, until the stop stage.
//
// It waits for a notification and reads anyway when one does not come. The
// timeout is not a fallback for a broken notification path: it is the path a
// notification sent while this replica was reconnecting takes, which is exactly
// the window the outbox exists for.
func (b *bus) read(reading, handling context.Context) {
	for {
		if reading.Err() != nil {
			return
		}
		// The retention pass runs whether or not this replica subscribes to
		// anything: a publish-only process ages its outbox out on the same
		// clock as any other. It sits here rather than at the end of drain,
		// because drain returns early when there are no subscribers. A failure
		// that is the stop stage cancelling the pass is not one.
		if err := b.retireIfDue(time.Now(), func() error { return b.runRetention(reading) }); err != nil &&
			reading.Err() == nil {
			b.logger.Error("event/postgres: retiring old entries failed; the next round retries",
				"err", err)
		}
		if err := b.drain(reading, handling); err != nil {
			if reading.Err() != nil {
				return
			}
			b.logger.Error("event/postgres: reading the outbox failed; retrying",
				"err", err)
			select {
			case <-reading.Done():
				return
			case <-time.After(b.pollEvery):
			}
			continue
		}
		if reading.Err() != nil {
			return
		}
		waitCtx, cancel := context.WithTimeout(reading, b.pollEvery)
		_, waitErr := b.listen.WaitForNotification(waitCtx)
		cancel()
		// The waiting connection is how this replica learns that something was
		// published. A failure here is not fatal — the read above still runs —
		// but it is the only signal that the notification path has degraded,
		// and without a pause it would turn this loop into a spin against the
		// database.
		if waitErr == nil || reading.Err() != nil || errors.Is(waitErr, context.DeadlineExceeded) {
			continue
		}
		if b.listen.IsClosed() {
			// The session is gone — terminated, or its network path dropped
			// — and every wait on it fails at once from here on. Waiting on
			// it again would leave this replica polling the table for the
			// rest of the process's life, so a new one is taken, from the
			// same configuration, and the loop goes straight back to reading
			// the table, which covers whatever was published in between.
			b.logger.Error("event/postgres: the listening connection was lost; taking a new one",
				"err", waitErr)
			if err := b.relisten(reading); err == nil {
				continue
			} else if reading.Err() == nil {
				b.logger.Error("event/postgres: taking a new listening connection failed; polling the "+
					"table until one is taken", "err", err)
			}
		} else {
			b.logger.Error("event/postgres: waiting for a notification failed; carrying on "+
				"by polling the table", "err", waitErr)
		}
		select {
		case <-reading.Done():
			return
		case <-time.After(b.pollEvery):
		}
	}
}

// relisten replaces a listening connection that was lost.
//
// Only the read goroutine calls it, and Close reads the field only once that
// goroutine has returned, so the field needs no lock of its own.
func (b *bus) relisten(ctx context.Context) error {
	conn, err := pgx.ConnectConfig(ctx, b.pool.Config().ConnConfig.Copy())
	if err != nil {
		return err
	}
	if _, err := conn.Exec(ctx, "LISTEN "+channel); err != nil {
		_ = conn.Close(ctx)
		return err
	}
	_ = b.listen.Close(ctx)
	b.listen = conn
	return nil
}

// drain reads one batch this replica has not delivered and delivers it.
//
// The batch is read under reading and delivered under handling: a batch the
// stop stage finds already read is delivered and marked within its grace,
// rather than left for a next run that never comes — a replica's identity is
// this process's, so what it leaves unmarked no one delivers on its behalf.
func (b *bus) drain(reading, handling context.Context) error {
	if len(b.types) == 0 {
		return nil
	}

	// Every entry published since this replica opened that it has not
	// delivered, whatever its id relative to anything else. Reading by what is
	// missing rather than by where a cursor stands is what makes a commit that
	// lands late just another entry.
	rows, err := b.pool.Query(reading,
		`SELECT o.id, o.identifier, o.type, o.tenant, o.payload, o.trace_context FROM `+table+` o
		 WHERE o.type = ANY($2)
		   AND NOT pg_visible_in_snapshot(o.xact, $4::pg_snapshot)
		   AND NOT EXISTS (SELECT 1 FROM `+deliveredTable+` d
		                   WHERE d.event_id = o.id AND d.replica = $1)
		 ORDER BY o.id LIMIT $3`,
		b.replica, b.types, batchSize, b.origin)
	if err != nil {
		return err
	}
	type row struct {
		id      int64
		evt     event.Event
		payload string
		// trace is the entry's stored tracing context, the text form
		// dispatch keeps it in; an entry written before the column existed
		// holds the empty string and decodes to an empty carrier.
		trace string
	}
	var batch []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.evt.ID, &r.evt.Type, &r.evt.TenantID, &r.payload, &r.trace); err != nil {
			rows.Close()
			return err
		}
		batch = append(batch, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, r := range batch {
		if handling.Err() != nil {
			// The stop stage's grace ran out. What is left of the batch stays
			// unmarked for this replica.
			return nil
		}
		// The payload's numbers keep their decimal text; see dispatch.Payload.
		if payload, err := dispatch.Payload([]byte(r.payload)); err == nil {
			r.evt.Payload = payload
		}
		// The tracing context the publisher's span was in, restored onto the
		// event so the delivery's span continues that trace.
		r.evt.TraceContext = dispatch.DecodeTraceContext(r.trace)
		b.table.Deliver(handling, r.evt)
		// Marked even for a type whose handlers failed: the entry has been
		// handed over, and unmarking would redeliver it forever with nobody
		// left to tell.
		if _, err := b.pool.Exec(handling,
			`INSERT INTO `+deliveredTable+` (event_id, replica) VALUES ($1, $2)
			 ON CONFLICT DO NOTHING`, r.id, b.replica); err != nil {
			return err
		}
	}
	return nil
}

// retireIfDue runs the retention pass when it is due, and records the run only
// when it succeeded: a failed pass leaves the schedule alone, so the next
// round tries again rather than the failure buying an hour of silence.
//
// run is a parameter so a case can drive the pass and its failure without a
// database, the way the pool fixture takes the pace of its wait as parameters.
//
// The caller is the single read goroutine, so the field it reads and writes
// needs no lock.
func (b *bus) retireIfDue(now time.Time, run func() error) error {
	if now.Sub(b.lastRetire) < retireEvery {
		return nil
	}
	if err := run(); err != nil {
		return err
	}
	b.lastRetire = now
	return nil
}

// runRetention drops what is past the retention window.
//
// Both tables grow with every event, and nothing else bounds them: the outbox
// by what was published, the delivered table by what each replica has marked.
// The cost is stated where the window is.
func (b *bus) runRetention(ctx context.Context) error {
	var errs []error
	if _, err := b.pool.Exec(ctx,
		`DELETE FROM `+table+` WHERE created_at < $1`, time.Now().Add(-retention)); err != nil {
		errs = append(errs, fmt.Errorf("retiring old outbox entries: %w", err))
	}
	// The marks of entries that are gone are gone too; without this the
	// delivered table would outlive the thing it describes.
	if _, err := b.pool.Exec(ctx,
		`DELETE FROM `+deliveredTable+` d WHERE NOT EXISTS (SELECT 1 FROM `+table+` o WHERE o.id = d.event_id)`,
	); err != nil {
		errs = append(errs, fmt.Errorf("retiring stale delivery marks: %w", err))
	}
	return errors.Join(errs...)
}
