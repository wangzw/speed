//go:build integration

package postgres

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/event/internal/tracing"
	"github.com/vislake/speed/pkg/internal/pgpool"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

// TestAnEntryDeliveredWhileStoppingIsMarked pins what the stop stage leaves
// behind on this backend: the entry a subscriber was handling is marked for
// this replica. The replica's identity is this process's, so an entry it left
// unmarked is delivered on its behalf by no one. A handler still running when
// the drive's context is cancelled is the case that would leave one, were its
// mark written on a context already done.
func TestAnEntryDeliveredWhileStoppingIsMarked(t *testing.T) {
	dsn := pgtest.Acquire(t)
	ctx := context.Background()
	b := openBus(t, dsn)
	entered := make(chan struct{})
	release := make(chan struct{})
	b.Subscribe("slow", "test", func(context.Context, event.Event) error {
		close(entered)
		<-release
		return nil
	})
	drive, cancel := context.WithCancel(ctx)
	if err := b.begin(drive); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := b.Publish(ctx, event.Event{Type: "slow"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the subscriber to start")
	}
	cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- b.Stop(ctx) }()
	time.Sleep(200 * time.Millisecond)
	close(release)
	if err := <-stopped; err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if marks := marksOf(t, dsn, b.replica); marks != 1 {
		t.Fatalf("the replica holds %d delivery marks after the stop stage, want 1: the entry "+
			"delivered while stopping was left unmarked", marks)
	}
}

// TestAStopDuringTheRetentionPassReportsNothing pins that the stop stage is
// not reported as a failure. The first retention pass runs as soon as the
// reader starts, so a short-lived process stops in the middle of it; logging
// the pass the stop cancelled as a retention failure would write an error line
// on every ordinary shutdown.
//
// The stop lands at a slightly different moment on each round, so some rounds
// cut the pass mid-statement.
func TestAStopDuringTheRetentionPassReportsNothing(t *testing.T) {
	dsn := pgtest.Acquire(t)
	ctx := context.Background()
	records := &recorder{}
	for round := range 40 {
		opened, err := open(ctx, &pgpool.Config{DSN: dsn, MaxConns: 2}, slog.New(records), tracing.Noop())
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		b := opened.(*bus)
		if err := b.begin(ctx); err != nil {
			t.Fatalf("begin: %v", err)
		}
		time.Sleep(time.Duration(round%8) * 500 * time.Microsecond)
		if err := b.Stop(ctx); err != nil {
			t.Fatalf("Stop: %v", err)
		}
		if err := b.Close(ctx); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}
	if got := records.written(); got != "" {
		t.Fatalf("ordinary stops were reported as failures:\n%s", got)
	}
}

// marksOf counts the delivery marks a replica holds.
func marksOf(t *testing.T, dsn, replica string) int {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var marks int
	if err := conn.QueryRow(ctx,
		`SELECT count(*) FROM `+deliveredTable+` WHERE replica = $1`, replica).Scan(&marks); err != nil {
		t.Fatalf("counting the delivery marks: %v", err)
	}
	return marks
}

// recorder is a log handler that keeps every record it is handed.
type recorder struct {
	mu    sync.Mutex
	lines []string
}

func (r *recorder) Enabled(context.Context, slog.Level) bool { return true }

func (r *recorder) Handle(_ context.Context, rec slog.Record) error {
	line := rec.Message
	rec.Attrs(func(a slog.Attr) bool {
		line += " " + a.String()
		return true
	})
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, line)
	return nil
}

func (r *recorder) WithAttrs([]slog.Attr) slog.Handler { return r }

func (r *recorder) WithGroup(string) slog.Handler { return r }

func (r *recorder) written() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.lines, "\n")
}
