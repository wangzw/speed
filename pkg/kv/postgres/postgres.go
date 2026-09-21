// Package postgres is the PostgreSQL implementation of the key-value capability
// pkg/kv declares.
//
// It is the sister of pkg/event/postgres: both exist for a deployment that
// already runs PostgreSQL and does not want to bring up Redis for one more
// thing, and both keep their own table.
//
// The table is created here, by this package, rather than declared as a
// migration for pkg/db to apply. That is deliberate: this capability is below
// the database module in the dependency graph — it is what a host deploys
// *instead of* depending on a module stack — and taking a dependency on pkg/db
// to get one table created would make every host that wants a counter carry
// GORM.
//
// The two operations that have to be atomic are one transaction each. A
// transaction rather than a single statement because an expired row has to go
// before the write: PostgreSQL's ON CONFLICT would otherwise find the row and
// take the update branch, leaving a key that the capability says must read as
// absent.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/pgpool"
)

const (
	// ModuleName is this implementation's name in the registry.
	ModuleName = "kv.postgres"
	// ConfigNamespace is where this implementation's input items are mounted.
	ConfigNamespace = ModuleName
	// table is this implementation's own, and it is not configurable: a name a
	// host could point elsewhere would let two assemblies on one server
	// overwrite each other's entries with no sign that they had.
	table = "kv_entries"
	// createTable is idempotent, and it runs on every open rather than behind a
	// ledger: this package has no migration machinery of its own, and a
	// CREATE TABLE IF NOT EXISTS is its own record of having been applied.
	createTable = `CREATE TABLE IF NOT EXISTS ` + table + ` (
		key        TEXT PRIMARY KEY,
		value      BYTEA NOT NULL,
		expires_at TIMESTAMPTZ
	)`
)

// init registers this module with the process registry.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is this module's descriptor, exported for the registries that do not
// inherit the process-level registrations.
func Module() core.Module {
	defaults := pgpool.Defaults()
	return kv.NewModule(kv.Spec[pgpool.Config]{
		ModuleName:      ModuleName,
		ConfigNamespace: ConfigNamespace,
		Config:          &defaults,
		Items:           pgpool.Items(),
		Stance:          stance,
		Open:            open,
	})
}

// stance states whether this implementation runs, on the same terms as the other
// remote backends: an absent locator disables it with a reason, a configured one
// states StateEnabled so a second configured backend fails the startup.
func stance(cfg *pgpool.Config) core.Enablement {
	if strings.TrimSpace(cfg.DSN) == "" {
		return core.Enablement{
			State: core.StateDisabled,
			Reason: fmt.Sprintf("%s.%s gives no connection string, so this assembly runs without a "+
				"PostgreSQL key-value store; set it to run on PostgreSQL",
				ConfigNamespace, pgpool.DSNKey),
		}
	}
	return core.Enablement{State: core.StateEnabled}
}

// open builds the pool and makes sure the table is there.
func open(ctx context.Context, cfg *pgpool.Config) (kv.KV, error) {
	pool, err := pgpool.Connect(ctx, ModuleName, *cfg)
	if err != nil {
		return nil, err
	}
	if _, createErr := pool.Exec(ctx, createTable); createErr != nil {
		pool.Close()
		return nil, fmt.Errorf("%s: creating %s: %w", ModuleName, table, createErr)
	}
	return &store{pool: pool}, nil
}

// store is the capability over one pool.
type store struct {
	pool *pgxpool.Pool
}

// Close releases the pool.
func (s *store) Close(_ context.Context) error {
	s.pool.Close()
	return nil
}

// live is the predicate that makes an expired row read as absent. It is written
// out rather than kept in a view so that every statement that reads the table
// carries it, and a statement that forgot it would be a row a caller can see
// past its own ttl.
const live = `(expires_at IS NULL OR expires_at > now())`

// Get returns a copy of the value. pgx allocates it, so this implementation
// never holds a slice the caller can write into.
func (s *store) Get(ctx context.Context, key string) ([]byte, bool, error) {
	var value []byte
	err := s.pool.QueryRow(ctx,
		`SELECT value FROM `+table+` WHERE key = $1 AND `+live, key).Scan(&value)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return value, true, nil
}

// Set stores the value, replacing any existing one and its expiry.
func (s *store) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO `+table+` (key, value, expires_at) VALUES ($1, $2, $3)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, expires_at = EXCLUDED.expires_at`,
		key, value, expiresAt(ttl))
	return err
}

// Delete removes the key, whether or not it was there.
func (s *store) Delete(ctx context.Context, key string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM `+table+` WHERE key = $1`, key)
	return err
}

// IncrByFloat adds to the number under key, leaving the key's expiry alone.
func (s *store) IncrByFloat(ctx context.Context, key string, delta float64) (float64, error) {
	return s.increment(ctx, key, delta, 0, false)
}

// IncrByFloatWithTTL adds to the number under key and, when the key did not
// exist, gives it the expiry in the same transaction.
func (s *store) IncrByFloatWithTTL(ctx context.Context, key string, delta float64, ttl time.Duration) (float64, error) {
	return s.increment(ctx, key, delta, ttl, true)
}

// increment is both increment operations.
//
// The arithmetic runs in the database rather than in this process: reading a
// value, adding to it and writing it back would let a concurrent increment land
// between the read and the write, which is the loss the capability's own
// documentation names. The value travels as its decimal text in both directions
// — the column holds bytes and PostgreSQL's numeric type does not read them, so
// the conversion happens in the statement.
func (s *store) increment(ctx context.Context, key string, delta float64, ttl time.Duration, withTTL bool) (float64, error) {
	tx, beginErr := s.pool.Begin(ctx)
	if beginErr != nil {
		return 0, beginErr
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// An expired row has to go first, or ON CONFLICT would find it and take the
	// update branch, and the key would keep an expiry the capability says it no
	// longer has.
	if _, deleteErr := tx.Exec(ctx,
		`DELETE FROM `+table+` WHERE key = $1 AND expires_at IS NOT NULL AND expires_at <= now()`,
		key); deleteErr != nil {
		return 0, deleteErr
	}

	var expires any
	if withTTL {
		expires = expiresAt(ttl)
	}
	var text string
	// Every statement in this transaction names its own error rather than
	// reusing one: the transaction spans several, and a single name would be
	// reassigned by each, leaving a reader to work out which failure a return
	// belongs to.
	incrErr := tx.QueryRow(ctx,
		`INSERT INTO `+table+` (key, value, expires_at) VALUES ($1, convert_to($2, 'UTF8'), $3)
		 ON CONFLICT (key) DO UPDATE
		 SET value = convert_to((convert_from(`+table+`.value, 'UTF8')::numeric + $2::numeric)::text, 'UTF8')
		 RETURNING convert_from(value, 'UTF8')`,
		key, formatFloat(delta), expires).Scan(&text)
	if incrErr != nil {
		return 0, asNotNumeric(incrErr)
	}
	if commitErr := tx.Commit(ctx); commitErr != nil {
		return 0, commitErr
	}
	got, parseErr := parseFloat(text)
	if parseErr != nil {
		return 0, fmt.Errorf("%s: the counter for %q holds %q, which is not a number", ModuleName, key, text)
	}
	return got, nil
}

// CompareAndSwap writes only on a match, and never touches the expiry.
//
// The insert branch carries the match too: a key that is not there matches an
// empty old, and anything else must not be written. Without that condition an
// absent key would be created whatever old was.
func (s *store) CompareAndSwap(ctx context.Context, key string, old, newVal []byte) (bool, error) {
	// The capability spells "empty" two ways — a nil slice and a zero-length
	// one — and means the same thing by both. Folded together here rather than
	// inside the comparison, because a nil argument reaches PostgreSQL as SQL
	// NULL and NULL equals nothing: the two spellings would otherwise give two
	// answers to one question.
	if old == nil {
		old = []byte{}
	}

	tx, beginErr := s.pool.Begin(ctx)
	if beginErr != nil {
		return false, beginErr
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, deleteErr := tx.Exec(ctx,
		`DELETE FROM `+table+` WHERE key = $1 AND expires_at IS NOT NULL AND expires_at <= now()`,
		key); deleteErr != nil {
		return false, deleteErr
	}

	// The swap is two statements rather than one, and that is what the
	// semantics need. A single INSERT ... ON CONFLICT cannot express it: the
	// conflict branch only runs when an insert is attempted, and the insert has
	// to be conditional on the key being absent, so a guarded INSERT never
	// attempts one and its ON CONFLICT never fires.
	//
	// The comparison casts explicitly. Left to itself the server infers a text
	// parameter from the comparison against a bytea column and then refuses it:
	// `operator does not exist: bytea = text`.
	var one int
	updateErr := tx.QueryRow(ctx,
		`UPDATE `+table+` SET value = $2 WHERE key = $1 AND value = $3::bytea RETURNING 1`,
		key, newVal, old).Scan(&one)
	if updateErr == nil {
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return false, commitErr
		}
		return true, nil
	}
	if !errors.Is(updateErr, pgx.ErrNoRows) {
		return false, updateErr
	}

	// Nothing matched. A key that is not there matches an empty old, which is
	// what makes this set-if-absent; anything else must not be written.
	if len(old) > 0 {
		return false, nil
	}
	insertErr := tx.QueryRow(ctx,
		`INSERT INTO `+table+` (key, value) VALUES ($1, $2)
		 ON CONFLICT (key) DO NOTHING
		 RETURNING 1`,
		key, newVal).Scan(&one)
	if errors.Is(insertErr, pgx.ErrNoRows) {
		// Something took the key between the two statements — or it is there
		// with a value that is not empty — so the swap did not happen. An
		// expected outcome, not a failure.
		return false, nil
	}
	if insertErr != nil {
		return false, insertErr
	}
	if commitErr := tx.Commit(ctx); commitErr != nil {
		return false, commitErr
	}
	return true, nil
}

// expiresAt turns a ttl into the instant a row stops being visible, or nil for
// no expiry.
func expiresAt(ttl time.Duration) any {
	if ttl <= 0 {
		return nil
	}
	return time.Now().Add(ttl)
}

// formatFloat renders a delta as the shortest text that round-trips, which is
// what the statement parses back into its own numeric type.
func formatFloat(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }

// parseFloat reads back what the statement produced.
//
// PostgreSQL writes its own numeric text, so what comes back is not necessarily
// the shortest form this package would have written — but the capability says a
// caller parses a counter rather than comparing its text, and the value the
// caller receives is the float64 this returns.
func parseFloat(text string) (float64, error) { return strconv.ParseFloat(text, 64) }

// asNotNumeric maps PostgreSQL's complaint about a value that is not a number
// onto this capability's sentinel.
//
// The match is on the server's own wording, which is the only signal it gives,
// and it is narrow so that everything else stays as it came: a caller has to be
// able to tell a bad value from a connection that dropped.
// It matches two complaints, not one. A value that is not a number is the
// obvious case; a value that is not valid UTF-8 is the other, because the
// statement has to decode the bytes before it can ask whether they are a
// number, and the capability's values are opaque bytes that are allowed not to
// be text at all. Both mean the same thing to a caller — this key does not hold
// a counter — so both become the sentinel rather than one of them escaping as a
// raw statement failure.
func asNotNumeric(err error) error {
	if err != nil && (strings.Contains(err.Error(), "invalid input syntax for type numeric") ||
		strings.Contains(err.Error(), "invalid byte sequence for encoding")) {
		return fmt.Errorf("%w: %s", kv.ErrNotNumeric, err.Error())
	}
	return err
}
