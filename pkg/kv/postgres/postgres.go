// Package postgres is the PostgreSQL implementation of the key-value capability pkg/kv declares.
package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/pgpool"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/kv/internal/counter"
)

const (
	// ModuleName is this implementation's name in the registry.
	ModuleName = "kv.postgres"
	// ConfigNamespace is where this implementation's input items are mounted.
	ConfigNamespace = ModuleName
	table           = "kv_entries"
	createTable     = `CREATE TABLE IF NOT EXISTS ` + table + ` (
		key        BYTEA PRIMARY KEY,
		value      BYTEA NOT NULL,
		expires_at TIMESTAMPTZ
	)`
	expiryIndex       = table + `_expires_at`
	createExpiryIndex = `CREATE INDEX IF NOT EXISTS ` + expiryIndex + ` ON ` + table +
		` (expires_at) WHERE expires_at IS NOT NULL`
	tablePresent = `SELECT to_regclass('` + table + `') IS NOT NULL`
	indexPresent = `SELECT EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
		WHERE i.indrelid = to_regclass('` + table + `') AND c.relname = '` + expiryIndex + `')`
	keyColumnType = `SELECT format_type(atttypid, atttypmod) FROM pg_attribute
		WHERE attrelid = to_regclass('` + table + `') AND attname = 'key'`
	migrateTextKeys = `ALTER TABLE ` + table + ` ALTER COLUMN key TYPE BYTEA USING convert_to(key, 'UTF8')`
	respellLongKeys = `UPDATE ` + table + ` SET key = substring(key FROM 1 FOR $1) || sha256(key)
		WHERE length(key) > $1`
)

const maxPlainKeyLen = 1024

func spell(key string) []byte {
	if len(key) <= maxPlainKeyLen {
		return []byte(key)
	}
	sum := sha256.Sum256([]byte(key))
	out := make([]byte, 0, maxPlainKeyLen+len(sum))
	out = append(out, key[:maxPlainKeyLen]...)
	return append(out, sum[:]...)
}

func init() { core.ProcessRegistry.Register(Module()) }

// Module is this module's descriptor, exported for the registries that do not inherit the
// process-level registrations.
func Module() core.Module {
	defaults := pgpool.Defaults()
	return kv.NewModule(kv.Spec[pgpool.Config]{
		ModuleName:      ModuleName,
		ConfigNamespace: ConfigNamespace,
		Config:          &defaults,
		Items:           pgpool.Items(),
		Stance:          stance,
		Open:            open,
		Secrets:         func(cfg *pgpool.Config) []string { return []string{cfg.DSN} },
	})
}

func stance(cfg *pgpool.Config) (core.Enablement, error) {
	if strings.TrimSpace(cfg.DSN) == "" {
		return core.Enablement{}, fmt.Errorf("%s.%s gives no connection string, and this implementation "+
			"reaches PostgreSQL at one: with none set there is nothing to start against. Set it, or "+
			"leave this implementation out of the command", ConfigNamespace, pgpool.DSNKey)
	}
	return core.Enablement{State: core.StateEnabled}, nil
}

func open(ctx context.Context, cfg *pgpool.Config) (kv.KV, error) {
	pool, err := pgpool.Connect(ctx, ModuleName, *cfg)
	if err != nil {
		return nil, err
	}
	if prepareErr := prepareTable(ctx, pool); prepareErr != nil {
		pool.Close()
		return nil, fmt.Errorf("%s: preparing %s: %w", ModuleName, table, prepareErr)
	}
	sweepCtx, stopSweeps := context.WithCancel(context.Background())
	return &store{pool: pool, sweepCtx: sweepCtx, stopSweeps: stopSweeps}, nil
}

const schemaLock = "speed:kv:" + table

func prepareTable(ctx context.Context, pool *pgxpool.Pool) error {
	tx, beginErr := pool.Begin(ctx)
	if beginErr != nil {
		return beginErr
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, lockErr := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, schemaLock); lockErr != nil {
		return lockErr
	}
	var present bool
	if presentErr := tx.QueryRow(ctx, tablePresent).Scan(&present); presentErr != nil {
		return fmt.Errorf("asking whether the table is there: %w", presentErr)
	}
	if !present {
		if _, createErr := tx.Exec(ctx, createTable); createErr != nil {
			return fmt.Errorf("creating the table: %w", createErr)
		}
	}
	var keyType string
	if typeErr := tx.QueryRow(ctx, keyColumnType).Scan(&keyType); typeErr != nil {
		return fmt.Errorf("reading the key column's type: %w", typeErr)
	}
	switch keyType {
	case "bytea":
	case "text":
		if _, alterErr := tx.Exec(ctx, migrateTextKeys); alterErr != nil {
			return fmt.Errorf("converting the key column to bytes: %w", alterErr)
		}
		if _, respellErr := tx.Exec(ctx, respellLongKeys, maxPlainKeyLen); respellErr != nil {
			return fmt.Errorf("respelling the long keys: %w", respellErr)
		}
	default:
		return fmt.Errorf("the key column is %s, and this implementation stores keys as bytea", keyType)
	}
	var indexed bool
	if indexedErr := tx.QueryRow(ctx, indexPresent).Scan(&indexed); indexedErr != nil {
		return fmt.Errorf("asking whether the expiry index is there: %w", indexedErr)
	}
	if !indexed {
		if _, indexErr := tx.Exec(ctx, createExpiryIndex); indexErr != nil {
			return fmt.Errorf("creating the expiry index: %w", indexErr)
		}
	}
	return tx.Commit(ctx)
}

type store struct {
	pool *pgxpool.Pool

	sweepCtx   context.Context
	stopSweeps context.CancelFunc
	sweeping   atomic.Bool
	nextSweep  atomic.Int64
	mu         sync.Mutex
	closed     bool
	sweeps     sync.WaitGroup
}

func (s *store) Close(_ context.Context) error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.stopSweeps()
	s.sweeps.Wait()
	s.pool.Close()
	return nil
}

const sweepBatch = 1000

const sweepEvery = time.Second

const sweepExpired = `DELETE FROM ` + table + ` WHERE key IN (
	SELECT key FROM ` + table + ` WHERE expires_at <= now()
	ORDER BY expires_at LIMIT $1 FOR UPDATE SKIP LOCKED)`

func (s *store) afterExpiringWrite() {
	if time.Now().UnixNano() < s.nextSweep.Load() {
		return
	}
	if !s.sweeping.CompareAndSwap(false, true) {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		s.sweeping.Store(false)
		return
	}
	s.sweeps.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.sweeps.Done()
		defer s.sweeping.Store(false)
		wait := sweepEvery
		tag, err := s.pool.Exec(s.sweepCtx, sweepExpired, sweepBatch)
		if err == nil && tag.RowsAffected() == sweepBatch {
			wait = 0
		}
		s.nextSweep.Store(time.Now().Add(wait).UnixNano())
	}()
}

const live = `(expires_at IS NULL OR expires_at > now())`

func (s *store) Get(ctx context.Context, key string) ([]byte, bool, error) {
	var value []byte
	err := s.pool.QueryRow(ctx,
		`SELECT value FROM `+table+` WHERE key = $1 AND `+live, spell(key)).Scan(&value)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return value, true, nil
}

func (s *store) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO `+table+` (key, value, expires_at) VALUES ($1, $2, `+expiryFrom("$3")+`)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, expires_at = EXCLUDED.expires_at`,
		spell(key), nonNull(value), ttlMicros(ttl))
	if err == nil && ttl > 0 {
		s.afterExpiringWrite()
	}
	return err
}

func nonNull(value []byte) []byte {
	if value == nil {
		return []byte{}
	}
	return value
}

func (s *store) Delete(ctx context.Context, key string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM `+table+` WHERE key = $1`, spell(key))
	return err
}

func (s *store) IncrByFloat(ctx context.Context, key string, delta float64) (float64, error) {
	return s.increment(ctx, key, delta, 0, false)
}

func (s *store) IncrByFloatWithTTL(ctx context.Context, key string, delta float64, ttl time.Duration) (float64, error) {
	return s.increment(ctx, key, delta, ttl, true)
}

func (s *store) increment(ctx context.Context, key string, delta float64, ttl time.Duration, withTTL bool) (float64, error) {
	if deltaErr := counter.CheckDelta(ModuleName, key, delta); deltaErr != nil {
		return 0, deltaErr
	}
	tx, beginErr := s.pool.Begin(ctx)
	if beginErr != nil {
		return 0, beginErr
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, deleteErr := tx.Exec(ctx,
		`DELETE FROM `+table+` WHERE key = $1 AND expires_at IS NOT NULL AND expires_at <= now()`,
		spell(key)); deleteErr != nil {
		return 0, deleteErr
	}

	var expires int64
	if withTTL {
		expires = ttlMicros(ttl)
	}
	var text string
	incrErr := tx.QueryRow(ctx,
		`INSERT INTO `+table+` (key, value, expires_at) VALUES ($1, convert_to($2, 'UTF8'), `+expiryFrom("$3")+`)
		 ON CONFLICT (key) DO UPDATE
		 SET value = convert_to((convert_from(`+table+`.value, 'UTF8')::numeric + $2::numeric)::text, 'UTF8')
		 RETURNING convert_from(value, 'UTF8')`,
		spell(key), formatFloat(delta), expires).Scan(&text)
	if incrErr != nil {
		return 0, asNotNumeric(incrErr, key)
	}
	got, parseErr := parseFloat(text)
	if parseErr != nil && !errors.Is(parseErr, strconv.ErrRange) {
		return 0, fmt.Errorf("%s: the increment of %q produced %q, which is not a number", ModuleName, key, text)
	}
	if resultErr := counter.CheckResult(ModuleName, key, got); resultErr != nil {
		return 0, resultErr
	}
	if commitErr := tx.Commit(ctx); commitErr != nil {
		return 0, commitErr
	}
	if withTTL && ttl > 0 {
		s.afterExpiringWrite()
	}
	return got, nil
}

func (s *store) CompareAndSwap(ctx context.Context, key string, old, newVal []byte) (bool, error) {
	old, newVal = nonNull(old), nonNull(newVal)
	var one int
	var err error
	if len(old) > 0 {
		err = s.pool.QueryRow(ctx,
			`UPDATE `+table+` SET value = $2 WHERE key = $1 AND value = $3::bytea AND `+live+` RETURNING 1`,
			spell(key), newVal, old).Scan(&one)
	} else {
		err = s.pool.QueryRow(ctx, swapFromEmpty, spell(key), newVal).Scan(&one)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

const swapFromEmpty = `INSERT INTO ` + table + ` (key, value) VALUES ($1, $2)
	ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value,
		expires_at = CASE WHEN ` + table + `.expires_at <= now() THEN NULL ELSE ` + table + `.expires_at END
	WHERE ` + table + `.value = ''::bytea OR ` + table + `.expires_at <= now()
	RETURNING 1`

func (s *store) SetIfAbsent(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	var one int
	err := s.pool.QueryRow(ctx,
		`INSERT INTO `+table+` (key, value, expires_at) VALUES ($1, $2, `+expiryFrom("$3")+`)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, expires_at = EXCLUDED.expires_at
		 WHERE `+table+`.expires_at <= now()
		 RETURNING 1`,
		spell(key), nonNull(value), ttlMicros(ttl)).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if ttl > 0 {
		s.afterExpiringWrite()
	}
	return true, nil
}

func expiryFrom(param string) string {
	return `CASE WHEN ` + param + `::bigint > 0 THEN now() + ` + param +
		`::bigint * interval '1 microsecond' END`
}

func ttlMicros(ttl time.Duration) int64 {
	if ttl <= 0 {
		return 0
	}
	micros := ttl.Microseconds()
	if time.Duration(micros)*time.Microsecond < ttl {
		micros++
	}
	return micros
}

func formatFloat(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }

func parseFloat(text string) (float64, error) { return strconv.ParseFloat(text, 64) }

const (
	invalidTextRepresentation = "22P02"
	characterNotInRepertoire  = "22021"
)

func asNotNumeric(err error, key string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) &&
		(pgErr.Code == invalidTextRepresentation || pgErr.Code == characterNotInRepertoire) {
		return fmt.Errorf("%w (key %q)", kv.ErrNotNumeric, key)
	}
	return err
}
