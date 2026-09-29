package catalog

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/tenant"
)

// traceRecorder is a GORM logger that keeps every error a statement ended with
// and every message logged at error level. GORM's default logger prints each of
// those to standard output as an error, outside the logging module, so a roster
// path that hands GORM one writes an error line into the process's output.
type traceRecorder struct {
	mu     sync.Mutex
	errors []string
}

func (r *traceRecorder) LogMode(logger.LogLevel) logger.Interface { return r }

func (r *traceRecorder) Info(context.Context, string, ...interface{}) {}

func (r *traceRecorder) Warn(context.Context, string, ...interface{}) {}

func (r *traceRecorder) Error(_ context.Context, msg string, _ ...interface{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errors = append(r.errors, msg)
}

func (r *traceRecorder) Trace(_ context.Context, _ time.Time, fc func() (string, int64), err error) {
	if err == nil {
		return
	}
	sql, _ := fc()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errors = append(r.errors, err.Error()+": "+sql)
}

func (r *traceRecorder) recorded() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.errors...)
}

// statementRecorder is a GORM logger that keeps the text of every statement
// run.
type statementRecorder struct {
	mu         sync.Mutex
	statements []string
}

func (r *statementRecorder) LogMode(logger.LogLevel) logger.Interface { return r }

func (r *statementRecorder) Info(context.Context, string, ...interface{}) {}

func (r *statementRecorder) Warn(context.Context, string, ...interface{}) {}

func (r *statementRecorder) Error(context.Context, string, ...interface{}) {}

func (r *statementRecorder) Trace(_ context.Context, _ time.Time, fc func() (string, int64), _ error) {
	sql, _ := fc()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.statements = append(r.statements, sql)
}

// take returns the statements recorded since the last call.
func (r *statementRecorder) take() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	taken := r.statements
	r.statements = nil
	return taken
}

// The roster's own answers about a tenant — an alias another tenant holds, an
// identifier already registered, an identifier no row carries — are what the
// table was read to give, not a statement that failed: the database is not
// asked afterwards whether the table is there, and the answer does not read as
// a failed statement.
func TestTheRostersOwnAnswersAskTheTableNothingFurther(t *testing.T) {
	r := rosterOver(t, insert("t_acme", "acme"))
	recorder := &statementRecorder{}
	r.handle = r.handle.Session(&gorm.Session{Logger: recorder})
	ctx := context.Background()
	for _, answer := range []struct {
		what     string
		sentinel error
		call     func() error
	}{
		{"an alias another tenant holds", tenant.ErrAliasConflict, func() error {
			return r.Put(ctx, tenant.Tenant{ID: "t_new", Alias: "acme", Status: tenant.StatusActive})
		}},
		{"an identifier already registered", tenant.ErrTenantExists, func() error {
			return r.Create(ctx, tenant.Tenant{ID: "t_acme", Alias: "other", Status: tenant.StatusActive})
		}},
		{"an identifier no row carries", tenant.ErrInvalidTenant, func() error {
			return r.SetStatus(ctx, "t_ghost", tenant.StatusDisabled)
		}},
	} {
		recorder.take()
		err := answer.call()
		if !errors.Is(err, answer.sentinel) {
			t.Errorf("%s gave %v, want %v", answer.what, err, answer.sentinel)
			continue
		}
		if strings.Contains(err.Error(), "a statement on table") {
			t.Errorf("%s was answered as a failed statement: %v", answer.what, err)
		}
		for _, stmt := range recorder.take() {
			if strings.Contains(stmt, "sqlite_master") {
				t.Errorf("%s was followed by asking the database for its tables: %s", answer.what, stmt)
			}
		}
	}
}

// The roster's lookups inside a write find nothing as a matter of course — a
// new tenant holds no alias yet, and a status change may name a tenant that is
// not there — and finding nothing is an answer, not a failed statement. Written
// as a failed statement, it reaches GORM's logger as an error on every tenant a
// command, an admin route or a seed creates.
func TestTheRostersWritesHandGORMNoFailedStatement(t *testing.T) {
	r := rosterOver(t)
	recorder := &traceRecorder{}
	r.handle = r.handle.Session(&gorm.Session{Logger: recorder})
	ctx := context.Background()

	if err := r.Put(ctx, tenant.Tenant{ID: "t_1", Alias: "acme", Status: tenant.StatusActive}); err != nil {
		t.Fatalf("putting a new tenant: %v", err)
	}
	if err := r.Put(ctx, tenant.Tenant{ID: "t_1", Alias: "acme2", Status: tenant.StatusActive}); err != nil {
		t.Fatalf("renaming a tenant: %v", err)
	}
	if err := r.Create(ctx, tenant.Tenant{ID: "t_2", Alias: "initech", Status: tenant.StatusActive}); err != nil {
		t.Fatalf("creating a new tenant: %v", err)
	}
	if err := r.Create(ctx, tenant.Tenant{ID: "t_2", Alias: "hooli", Status: tenant.StatusActive}); !errors.Is(err, tenant.ErrTenantExists) {
		t.Fatalf("creating a tenant that is there gave %v, want ErrTenantExists", err)
	}
	if err := r.SetStatus(ctx, "t_1", tenant.StatusDisabled); err != nil {
		t.Fatalf("disabling a tenant: %v", err)
	}
	if err := r.SetStatus(ctx, "t_nobody", tenant.StatusDisabled); !errors.Is(err, tenant.ErrInvalidTenant) {
		t.Fatalf("disabling a tenant that is not there gave %v, want ErrInvalidTenant", err)
	}
	if got := recorder.recorded(); len(got) != 0 {
		t.Fatalf("the roster's writes handed GORM's logger these errors: %q", got)
	}
}
