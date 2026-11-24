package catalog

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/tenant"
)

// TestTheRosterWorksOverAHandleTheHostOpened checks that the roster reads and
// writes over a handle the host opened itself with gorm.Open.
func TestTheRosterWorksOverAHandleTheHostOpened(t *testing.T) {
	handle, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "host.db")),
		&gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("opening a handle of the host's own: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(handle) })
	r := rosterOn(t, handle, "sqlite", insert("t_acme", "acme"))
	ctx := context.Background()
	if err := r.read(ctx); err != nil {
		t.Fatalf("reading the roster over the host's handle: %v", err)
	}
	if err := r.Put(ctx, tenant.Tenant{ID: "t_new", Name: "new", Status: tenant.StatusActive}); err != nil {
		t.Fatalf("Put over the host's handle: %v", err)
	}
	if err := r.SetStatus(ctx, "t_new", tenant.StatusDisabled); err != nil {
		t.Fatalf("SetStatus over the host's handle: %v", err)
	}
	if _, err := r.Enter(ctx, "t_new"); !errors.Is(err, tenant.ErrTenantDisabled) {
		t.Fatalf("entering the tenant just disabled over the host's handle gave %v, want ErrTenantDisabled", err)
	}
	if got, ok, err := r.Get(ctx, "t_acme"); err != nil || !ok || got.Name != "acme" {
		t.Fatalf("Get over the host's handle gave %+v, %v, %v, want the tenant read", got, ok, err)
	}
}

// TestASearchPathOfNoNamesReadsAsNone checks that blanks around an item and an
// empty setting read as PostgreSQL reads them.
func TestASearchPathOfNoNamesReadsAsNone(t *testing.T) {
	for setting, want := range map[string][]string{
		"":            nil,
		"   ":         nil,
		" a ,\tB ":    {"a", "b"},
		`"a b",c`:     {"a b", "c"},
		`"unfinished`: {"unfinished"},
	} {
		if got := searchPath(setting); !slices.Equal(got, want) {
			t.Errorf("search_path %q reads as %q, want %q", setting, got, want)
		}
	}
}
