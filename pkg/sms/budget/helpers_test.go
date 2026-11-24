package budget

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/internal/dbtest"
)

type dialect struct {
	name string
	open func(t *testing.T, items dbtest.Items) db.Database
}

var dialects = []dialect{{
	name: "sqlite",
	open: func(t *testing.T, items dbtest.Items) db.Database {
		dsn := filepath.Join(t.TempDir(), "budget.db") + "?_pragma=busy_timeout(5000)"
		return dbtest.SQLite(t, dsn, items)
	},
}}

func forEachDialect(t *testing.T, run func(t *testing.T, store Store)) {
	t.Helper()
	for _, d := range dialects {
		d := d
		t.Run(d.name, func(t *testing.T) {
			run(t, openLedger(t, d))
		})
	}
}

func openLedger(t *testing.T, d dialect) Store {
	t.Helper()
	handle := d.open(t, nil).DB()
	handle = handle.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
	applyMigrations(t, handle, d.name)
	return New(handle)
}

func applyMigrations(t *testing.T, handle *gorm.DB, dialect string) {
	t.Helper()
	entries, err := fs.ReadDir(migrationsFS, dialect)
	if err != nil {
		t.Fatalf("listing the %s migrations: %v", dialect, err)
	}
	for _, entry := range entries {
		body, err := fs.ReadFile(migrationsFS, dialect+"/"+entry.Name())
		if err != nil {
			t.Fatalf("reading %s: %v", entry.Name(), err)
		}
		for _, statement := range splitStatements(string(body)) {
			if err := handle.Exec(statement).Error; err != nil {
				t.Fatalf("applying %s: %v", entry.Name(), err)
			}
		}
	}
}

func splitStatements(sql string) []string {
	var body strings.Builder
	for _, line := range strings.Split(sql, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		body.WriteString(line)
		body.WriteByte('\n')
	}
	var out []string
	for _, part := range strings.Split(body.String(), ";") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

var at = time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)

func attempt(channel Channel, purpose, source, destination, tenant string, when time.Time) Attempt {
	a := Attempt{
		Channel:        channel,
		Purpose:        purpose,
		DestinationMAC: destination,
		TenantID:       tenant,
		AttemptedAt:    when,
		Outcome:        OutcomeSent,
	}
	if source != "" {
		a.SourceMAC = &source
	}
	return a
}

func suppressedAttempt(channel Channel, purpose, source, destination, tenant string, when time.Time) Attempt {
	a := attempt(channel, purpose, source, destination, tenant, when)
	a.Suppressed = true
	refunded := when
	a.RefundedAt = &refunded
	return a
}

func withAccount(a Attempt, account string) Attempt {
	a.AccountID = &account
	return a
}

func record(t *testing.T, store Store, a Attempt) ID {
	t.Helper()
	id, err := store.Record(context.Background(), a)
	if err != nil {
		t.Fatalf("recording an attempt: %v", err)
	}
	return id
}

func count(t *testing.T, store Store, q Query) int64 {
	t.Helper()
	n, err := store.Count(context.Background(), q)
	if err != nil {
		t.Fatalf("counting attempts: %v", err)
	}
	return n
}

func distinct(t *testing.T, store Store, q Query) int64 {
	t.Helper()
	n, err := store.CountDistinctDestinations(context.Background(), q)
	if err != nil {
		t.Fatalf("counting distinct destinations: %v", err)
	}
	return n
}

func refund(t *testing.T, store Store, id ID, when time.Time) {
	t.Helper()
	if err := store.MarkRefunded(context.Background(), id, when); err != nil {
		t.Fatalf("refunding an attempt: %v", err)
	}
}

func assertCount(t *testing.T, got, want int64, q Query) {
	t.Helper()
	if got != want {
		t.Errorf("count for %+v = %d, want %d", q, got, want)
	}
}

func requireInvalidAttempt(t *testing.T, store Store, a Attempt) {
	t.Helper()
	before := count(t, store, Query{Since: at.Add(-time.Hour), IncludeRefunded: true})
	if _, err := store.Record(context.Background(), a); !errors.Is(err, ErrInvalidAttempt) {
		t.Fatalf("Record(%+v) returned %v, want ErrInvalidAttempt", a, err)
	}
	after := count(t, store, Query{Since: at.Add(-time.Hour), IncludeRefunded: true})
	if after != before {
		t.Errorf("a refused attempt was written: the count went from %d to %d", before, after)
	}
}
