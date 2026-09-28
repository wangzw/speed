package catalog

import (
	"context"
	"errors"
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
