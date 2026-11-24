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

func (r *statementRecorder) take() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	taken := r.statements
	r.statements = nil
	return taken
}

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
		{"a name another tenant holds", tenant.ErrNameConflict, func() error {
			return r.Put(ctx, tenant.Tenant{ID: "t_new", Name: "acme", Status: tenant.StatusActive})
		}},
		{"an identifier already registered", tenant.ErrTenantExists, func() error {
			return r.Create(ctx, tenant.Tenant{ID: "t_acme", Name: "other", Status: tenant.StatusActive})
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

func TestTheRostersWritesHandGORMNoFailedStatement(t *testing.T) {
	r := rosterOver(t)
	recorder := &traceRecorder{}
	r.handle = r.handle.Session(&gorm.Session{Logger: recorder})
	ctx := context.Background()

	if err := r.Put(ctx, tenant.Tenant{ID: "t_1", Name: "acme", Status: tenant.StatusActive}); err != nil {
		t.Fatalf("putting a new tenant: %v", err)
	}
	if err := r.Put(ctx, tenant.Tenant{ID: "t_1", Name: "acme", Status: tenant.StatusDisabled}); err != nil {
		t.Fatalf("rewriting a tenant: %v", err)
	}
	if err := r.Create(ctx, tenant.Tenant{ID: "t_2", Name: "initech", Status: tenant.StatusActive}); err != nil {
		t.Fatalf("creating a new tenant: %v", err)
	}
	if err := r.Create(ctx, tenant.Tenant{ID: "t_2", Name: "hooli", Status: tenant.StatusActive}); !errors.Is(err, tenant.ErrTenantExists) {
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
