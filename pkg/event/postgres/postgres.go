// Package postgres is the PostgreSQL implementation of the event-bus
// capability pkg/event declares.
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
	systemName      = "postgres"
	table           = "event_outbox"
	channel         = "speed_events"
	// pollInterval bounds how long a replica waits for a notification before
	// looking at the table anyway, which is what covers a notification that was
	// sent while it was not listening.
	pollInterval = 2 * time.Second
	batchSize    = 64
	// deliveredTable records, per replica, which outbox entries that replica
	// has handed to its subscribers.
	deliveredTable = "event_delivered"
	retention      = 24 * time.Hour
	// retireEvery bounds how often the retention pass runs.
	retireEvery = time.Hour
	// createOutbox runs only when the outbox is missing: this capability is below
	// the database module and has no migration machinery to borrow.
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
	tablePresent       = `SELECT to_regclass($1) IS NOT NULL`
	indexPresent       = `SELECT EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
		WHERE i.indrelid = to_regclass($1) AND c.relname = $2)`
	columnPresent = `SELECT EXISTS (SELECT 1 FROM pg_attribute
		WHERE attrelid = to_regclass($1) AND attname = $2 AND attnum > 0 AND NOT attisdropped)`
	schemaLock int64 = 0x73706565645f6576
)

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
	// running LISTEN would otherwise be handed to an ordinary query, where
	// the subscription would sit idle and the query would wait on it.
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

// createTables makes sure this implementation's tables are there, one replica at a
// time, creating only what is missing.
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

var addedColumns = []struct{ name, definition string }{
	{"xact", "xid8 NOT NULL DEFAULT pg_current_xact_id()"},
	{"identifier", "TEXT NOT NULL DEFAULT ''"},
	{"trace_context", "TEXT NOT NULL DEFAULT ''"},
}

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

	replica string
	// origin is the database's snapshot at the moment this replica opened, in
	// its text form.
	origin string

	table *dispatch.Table
	// types is what the table held when it froze. The reader is the only one
	// that reads it, after begin has written it.
	types []string

	// lastRetire is when the retention pass last ran. Only the read goroutine
	// touches it, so it needs no lock of its own.
	lastRetire time.Time
	// pollEvery is how long this replica waits for a notification before
	// looking at the table anyway.
	pollEvery time.Duration
}

// Subscribe registers a handler for one event type, under the name its
// delivery's span is recorded under. It panics once the reader has started at
// the start stage, or when the name is empty.
func (b *bus) Subscribe(eventType string, name string, h event.Handler) {
	b.table.Subscribe(eventType, name, h)
}

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

func (b *bus) Close(ctx context.Context) error {
	stopErr := b.table.Close()
	err := b.listen.Close(ctx)
	b.pool.Close()
	return errors.Join(stopErr, err)
}

func (b *bus) begin(ctx context.Context) error {
	types, first := b.table.Freeze(ctx)
	if !first {
		return nil
	}
	b.types = types
	b.table.Go(b.read)
	return nil
}

func (b *bus) read(reading, handling context.Context) {
	for {
		if reading.Err() != nil {
			return
		}
		// The retention pass runs whether or not this replica subscribes to
		// anything: a publish-only process ages its outbox out on the same clock
		// as any other.
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
		// published.
		if waitErr == nil || reading.Err() != nil || errors.Is(waitErr, context.DeadlineExceeded) {
			continue
		}
		if b.listen.IsClosed() {
			// The session is gone — terminated, or its network path dropped — and
			// every wait on it fails at once from here on.
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

func (b *bus) drain(reading, handling context.Context) error {
	if len(b.types) == 0 {
		return nil
	}

	// Reading by what is not yet delivered rather than by a cursor makes a commit
	// that lands late just another entry.
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
			return nil
		}
		if payload, err := dispatch.Payload([]byte(r.payload)); err == nil {
			r.evt.Payload = payload
		}
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

// retireIfDue runs the retention pass when due, and records the run only when it
// succeeded, so a failure leaves the next round to try again.
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
