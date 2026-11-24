package policy

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/authz"
)

var sqliteSeq atomic.Int64

var tableDDL = []string{
	`CREATE TABLE authz_grants (
		tenant_id  TEXT NOT NULL CHECK (tenant_id <> ''),
		role       TEXT NOT NULL,
		resource   TEXT NOT NULL,
		action     TEXT NOT NULL,
		created_at DATETIME NOT NULL,
		PRIMARY KEY (tenant_id, role, resource, action))`,
	`CREATE TABLE authz_bindings (
		tenant_id  TEXT NOT NULL CHECK (tenant_id <> ''),
		account_id TEXT NOT NULL,
		role       TEXT NOT NULL,
		granted_at DATETIME NOT NULL,
		PRIMARY KEY (tenant_id, account_id, role))`,
}

func openHandle(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s_%d?mode=memory&cache=shared", t.Name(), sqliteSeq.Add(1))
	handle, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("opening the SQLite handle: %v", err)
	}
	pool, err := handle.DB()
	if err != nil {
		t.Fatalf("reaching the handle's pool: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	for _, statement := range tableDDL {
		if err := handle.Exec(statement).Error; err != nil {
			t.Fatalf("creating a policy table: %v", err)
		}
	}
	return handle
}

func seed(t *testing.T, handle *gorm.DB) {
	t.Helper()
	statements := []string{
		`INSERT INTO authz_grants (tenant_id, role, resource, action, created_at) VALUES
			('acme', 'roleA', 'account', 'read', '2020-01-01T00:00:00Z'),
			('globex', 'roleG', 'account', 'read', '2020-01-01T00:00:00Z')`,
		`INSERT INTO authz_bindings (tenant_id, account_id, role, granted_at) VALUES
			('acme', 'acc1', 'roleA', '2020-01-01T00:00:00Z'),
			('globex', 'acc2', 'roleG', '2020-01-01T00:00:00Z')`,
	}
	for _, statement := range statements {
		if err := handle.Exec(statement).Error; err != nil {
			t.Fatalf("seeding: %v", err)
		}
	}
}

// TestLoadReadsEveryTenantAndAnswersByDomain checks that load reads every tenant and answers by domain.
func TestLoadReadsEveryTenantAndAnswersByDomain(t *testing.T) {
	handle := openHandle(t)
	seed(t, handle)

	snapshot, err := Load(t.Context(), handle, authz.ModelText)
	if err != nil {
		t.Fatalf("loading the policy: %v", err)
	}

	cases := []struct {
		subject string
		domain  authz.Domain
		want    bool
	}{
		{"acc1", "acme", true},
		{"acc1", "globex", false},
		{"acc2", "globex", true},
		{"acc2", "acme", false},
		{"acc1", "acme", true},
	}
	for _, c := range cases {
		got, err := snapshot.Allowed(authz.Query{Subject: c.subject, Domain: c.domain, Object: "account", Action: "read"})
		if err != nil {
			t.Fatalf("Allowed(%s, %s): %v", c.subject, c.domain, err)
		}
		if got != c.want {
			t.Errorf("Allowed(%s, %s) = %t, want %t", c.subject, c.domain, got, c.want)
		}
	}
}

// TestLoadHonoursTheContext checks that load honours the context.
func TestLoadHonoursTheContext(t *testing.T) {
	handle := openHandle(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Load(ctx, handle, authz.ModelText); !errors.Is(err, context.Canceled) {
		t.Fatalf("Load gave %v, want context.Canceled", err)
	}
}

// TestHolderSwapsAtomically checks that holder swaps atomically.
func TestHolderSwapsAtomically(t *testing.T) {
	var h Holder
	if h.Get() != nil {
		t.Fatalf("a fresh holder handed back a snapshot")
	}
	first := &Snapshot{}
	if previous := h.Swap(first); previous != nil {
		t.Fatalf("the first swap replaced %v, want nothing", previous)
	}
	if h.Get() != first {
		t.Fatalf("the holder did not hand back the swapped snapshot")
	}
	second := &Snapshot{}
	if previous := h.Swap(second); previous != first {
		t.Fatalf("the second swap replaced %v, want the first snapshot", previous)
	}
	if h.Get() != second {
		t.Fatalf("the holder did not hand back the second snapshot")
	}
}

// TestConcurrentReadsSeeOneWholeSnapshot checks that concurrent reads see one whole snapshot.
func TestConcurrentReadsSeeOneWholeSnapshot(t *testing.T) {
	allows := openHandle(t)
	seed(t, allows)

	refuses := openHandle(t)

	first, err := Load(t.Context(), allows, authz.ModelText)
	if err != nil {
		t.Fatalf("loading the allowing snapshot: %v", err)
	}
	second, err := Load(t.Context(), refuses, authz.ModelText)
	if err != nil {
		t.Fatalf("loading the refusing snapshot: %v", err)
	}

	var holder Holder
	holder.Swap(first)
	query := authz.Query{Subject: "acc1", Domain: "acme", Object: "account", Action: "read"}

	const readers = 8
	const swaps = 5000
	var wg sync.WaitGroup
	done := make(chan struct{})

	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				snapshot := holder.Get()
				got, err := snapshot.Allowed(query)
				if err != nil {
					t.Errorf("reading the current snapshot: %v", err)
					return
				}
				var want bool
				switch snapshot {
				case first:
					want = true
				case second:
					want = false
				default:
					t.Errorf("a read handed back a snapshot that is neither version")
					return
				}
				if got != want {
					t.Errorf("a snapshot answered %t, want %t: a read saw a mixture of two versions", got, want)
					return
				}
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < swaps; i++ {
			if i%2 == 0 {
				holder.Swap(second)
			} else {
				holder.Swap(first)
			}
		}
		close(done)
	}()

	wg.Wait()
}

func heldFixture(t *testing.T) *gorm.DB {
	t.Helper()
	handle := openHandle(t)
	statements := []string{
		`INSERT INTO authz_grants (tenant_id, role, resource, action, created_at) VALUES
			('acme', 'roleA', 'account', 'read', '2020-01-01T00:00:00Z'),
			('acme', 'roleA', 'role', 'read', '2020-01-01T00:00:00Z'),
			('acme', 'roleB', 'account', 'read', '2020-01-01T00:00:00Z'),
			('globex', 'roleG', 'settings', 'write', '2020-01-01T00:00:00Z')`,
		`INSERT INTO authz_bindings (tenant_id, account_id, role, granted_at) VALUES
			('acme', 'acc1', 'roleA', '2020-01-01T00:00:00Z'),
			('acme', 'acc1', 'roleB', '2020-01-01T00:00:00Z'),
			('globex', 'acc2', 'roleG', '2020-01-01T00:00:00Z')`,
	}
	for _, statement := range statements {
		if err := handle.Exec(statement).Error; err != nil {
			t.Fatalf("seeding a held fixture: %v", err)
		}
	}
	return handle
}

// TestHeldAnswersTheUnionInOneDomain checks that held answers the union in one domain.
func TestHeldAnswersTheUnionInOneDomain(t *testing.T) {
	snapshot, err := Load(t.Context(), heldFixture(t), authz.ModelText)
	if err != nil {
		t.Fatalf("loading the policy: %v", err)
	}

	cases := []struct {
		name    string
		subject string
		domain  authz.Domain
		want    [][2]string
	}{
		{
			name:    "two roles are one union and a shared pair appears once",
			subject: "acc1",
			domain:  "acme",
			want:    [][2]string{{"account", "read"}, {"role", "read"}},
		},
		{
			name:    "a subject with no binding holds nothing",
			subject: "acc3",
			domain:  "acme",
			want:    [][2]string{},
		},
		{
			name:    "a binding in one tenant grants nothing in another",
			subject: "acc2",
			domain:  "acme",
			want:    [][2]string{},
		},
		{
			name:    "another tenant's grant is excluded from this tenant",
			subject: "acc1",
			domain:  "globex",
			want:    [][2]string{},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := snapshot.Held(c.domain, c.subject)
			if err != nil {
				t.Fatalf("Held(%s, %s): %v", c.domain, c.subject, err)
			}
			if len(got) != len(c.want) {
				t.Fatalf("Held(%s, %s) = %v, want %v", c.domain, c.subject, got, c.want)
			}
			for _, pair := range c.want {
				if !slices.Contains(got, pair) {
					t.Errorf("Held(%s, %s) = %v, missing %v", c.domain, c.subject, got, pair)
				}
			}
		})
	}
}
