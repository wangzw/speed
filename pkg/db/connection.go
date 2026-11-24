package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"gorm.io/gorm"
)

// Connection runs fn, under ctx, on one connection of the handle's pool, taken within wait; only that wait is bounded. The session fn receives and anything derived from it are pinned to the connection, and what fn leaves open there is closed when it returns or panics, with fn's own error returned as it is. A wait that is not positive, and a nil handle, are refused; no connection within wait wraps context.DeadlineExceeded while ctx has not ended.
func Connection(ctx context.Context, handle *gorm.DB, wait time.Duration, fn func(session *gorm.DB) error) error {
	if wait <= 0 {
		return fmt.Errorf("db: Connection was given a wait of %v, and a bound on waiting has to be positive", wait)
	}
	if handle == nil || handle.Config == nil {
		return errors.New("db: Connection was handed no handle to take a connection from")
	}
	session := handle.Session(&gorm.Session{NewDB: true, Context: ctx})
	pool, pooled := pooledOn(handle)
	if !pooled {
		return fn(session)
	}
	taking, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	conn, err := pool.Conn(taking)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return fmt.Errorf("db: no connection of the pool came free within %v; on a database held to "+
				"one connection, a transaction or a result set on the same handle holds it, and waiting "+
				"longer would be waiting for whoever holds it to be done with this statement: %w",
				wait, err)
		}
		return fmt.Errorf("db: taking a connection of the pool: %w", err)
	}
	released, release := context.WithCancel(context.Background())
	pinned := &pinnedConn{conn: conn, released: released}
	// Deferred, so that it runs on a panic too: nothing fn left open holds the
	// connection back from the pool, and closing it cannot wait for fn.
	defer func() {
		pinned.end()
		release()
		_ = conn.Close()
	}()
	session.Statement.ConnPool = pinned
	return fn(session)
}

func pooledOn(handle *gorm.DB) (*sql.DB, bool) {
	on := handle.ConnPool
	if handle.Statement != nil && handle.Statement.ConnPool != nil {
		on = handle.Statement.ConnPool
	}
	switch pool := on.(type) {
	case *sql.DB:
		return pool, pool != nil
	case gorm.TxCommitter:
		return nil, false
	case gorm.GetDBConnector:
		db, err := pool.GetDBConn()
		return db, err == nil && db != nil
	default:
		return nil, false
	}
}

// pinnedConn is the connection Connection took, keeping within reach what fn could leave open on it. It answers GORM's TxBeginner rather than TxCommitter, so a session on it is outside any transaction.
type pinnedConn struct {
	conn     *sql.Conn
	released context.Context

	mu    sync.Mutex
	begun []*sql.Tx
}

func (p *pinnedConn) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	return p.conn.PrepareContext(ctx, query)
}

func (p *pinnedConn) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return p.conn.ExecContext(ctx, query, args...)
}

func (p *pinnedConn) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return p.conn.QueryContext(p.untilReleased(ctx), query, args...)
}

func (p *pinnedConn) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return p.conn.QueryRowContext(p.untilReleased(ctx), query, args...)
}

func (p *pinnedConn) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	tx, err := p.conn.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.begun = append(p.begun, tx)
	p.mu.Unlock()
	return tx, nil
}

// untilReleased ends with the connection: a result set left open holds the
// connection until its context ends.
func (p *pinnedConn) untilReleased(ctx context.Context) context.Context {
	bounded, cancel := context.WithCancel(ctx)
	context.AfterFunc(p.released, cancel)
	return bounded
}

func (p *pinnedConn) end() {
	p.mu.Lock()
	begun := p.begun
	p.begun = nil
	p.mu.Unlock()
	for _, tx := range begun {
		_ = tx.Rollback()
	}
}

const engineMarkerName = "github.com/vislake/speed/pkg/db#engine"

// engineMarker is the engine behind the handle, carried in the plugin table so a callback handed nothing but a statement can read the statement's identifiers by that engine's rules (namesColumn).
type engineMarker struct {
	dialect Dialect
}

func (*engineMarker) Name() string { return engineMarkerName }

func (*engineMarker) Initialize(*gorm.DB) error { return nil }

func markEngine(session *gorm.DB, spec Spec) error {
	if err := session.Use(&engineMarker{dialect: spec.Dialect}); err != nil {
		return fmt.Errorf("db: marking a %s connection with its engine failed: %w", spec.Dialect, err)
	}
	return nil
}

func engineOf(session *gorm.DB) (*engineMarker, bool) {
	if session == nil || session.Config == nil {
		return nil, false
	}
	marker, ok := session.Plugins[engineMarkerName].(*engineMarker)
	return marker, ok
}
