package journal

import (
	"errors"
	"strconv"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/tenant"
	"github.com/vislake/speed/pkg/tenant/catalog"
)

func readHandle(t *testing.T) *gorm.DB {
	t.Helper()
	handle := sharedMemory(t)
	applyMigrations(t, handle, auditDialect)
	installTenancy(t, handle)
	return handle
}

// installTenancy installs the plugin the tenancy capability declares, taken from
// the module that declares it so that the case runs against the plugin an
// assembly installs and not a copy of it.
func installTenancy(t *testing.T, handle *gorm.DB) {
	t.Helper()
	for _, res := range catalog.Module().Resources {
		plugin, ok := res.(db.Plugin)
		if !ok {
			continue
		}
		if err := handle.Use(plugin.Plugin); err != nil {
			t.Fatalf("installing the tenancy plugin: %v", err)
		}
		return
	}
	t.Fatal("the tenancy module declares no database plugin")
}

func insertEntry(t *testing.T, handle *gorm.DB, e row) uint64 {
	t.Helper()
	if e.Source == "" {
		e.Source = string(audit.SourceSeam)
	}
	if e.Detail == "" {
		e.Detail = "{}"
	}
	if e.OccurredAt.IsZero() {
		e.OccurredAt = time.Now().UTC()
	}
	if err := handle.Create(&e).Error; err != nil {
		t.Fatalf("inserting an audit row: %v", err)
	}
	return e.ID
}

func ptr(s string) *string { return &s }

// Query reads newest first and pages by an opaque cursor.
func TestQueryReadsNewestFirstAndPagesByCursor(t *testing.T) {
	handle := readHandle(t)
	first := insertEntry(t, handle, row{Action: "a.create", Actor: "alice"})
	second := insertEntry(t, handle, row{Action: "a.update", Actor: "bob"})
	third := insertEntry(t, handle, row{Action: "a.remove", Actor: "carol"})
	journal := newTestJournal(handle, settings{})
	ctx := tenant.CrossTenant(t.Context())

	page, next, err := journal.Query(ctx, Query{Limit: 2})
	if err != nil {
		t.Fatalf("the first page failed: %v", err)
	}
	if len(page) != 2 {
		t.Fatalf("the first page holds %d rows, want 2", len(page))
	}
	if page[0].ID != third || page[1].ID != second {
		t.Errorf("the first page is %v, want the two newest rows %d then %d", []uint64{page[0].ID, page[1].ID}, third, second)
	}
	if next == "" {
		t.Fatalf("the first page carries no cursor, though a row follows it")
	}

	page, next, err = journal.Query(ctx, Query{Limit: 2, After: next})
	if err != nil {
		t.Fatalf("the second page failed: %v", err)
	}
	if len(page) != 1 || page[0].ID != first {
		t.Fatalf("the second page is %d row(s), want the one oldest row %d", len(page), first)
	}
	if next != "" {
		t.Errorf("the last page carries the cursor %q, want none", next)
	}
}

// The reader hands out a cursor it can read back, and refuses one it did not.
func TestQueryRefusesACursorItDidNotHandOut(t *testing.T) {
	handle := readHandle(t)
	insertEntry(t, handle, row{Action: "a.create"})
	journal := newTestJournal(handle, settings{})

	_, _, err := journal.Query(tenant.CrossTenant(t.Context()), Query{After: "not-a-cursor"})
	if !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("a cursor this read face never handed out gave %v, want ErrInvalidQuery", err)
	}
}

// A non-zero identifier reads exactly that row, which is how a route addresses
// one entry.
func TestQueryReadsOneRowByID(t *testing.T) {
	handle := readHandle(t)
	insertEntry(t, handle, row{Action: "a.create"})
	wanted := insertEntry(t, handle, row{Action: "a.update", Actor: "bob"})
	journal := newTestJournal(handle, settings{})
	ctx := tenant.CrossTenant(t.Context())

	page, next, err := journal.Query(ctx, Query{ID: wanted, Limit: 1})
	if err != nil {
		t.Fatalf("reading one row failed: %v", err)
	}
	if len(page) != 1 || page[0].ID != wanted {
		t.Fatalf("reading %d gave %d row(s), want the one row %d", wanted, len(page), wanted)
	}
	if next != "" {
		t.Errorf("reading one row carries the cursor %q, want none", next)
	}

	page, _, err = journal.Query(ctx, Query{ID: wanted + 1000, Limit: 1})
	if err != nil {
		t.Fatalf("reading a missing row failed: %v", err)
	}
	if len(page) != 0 {
		t.Fatalf("reading a missing row gave %d row(s), want none", len(page))
	}
}

// The time window is [Since, Until) and the action filter is a set; both are
// applied to the table, and a row outside the window or the set is left out.
func TestQueryNarrowsByTimeAndAction(t *testing.T) {
	handle := readHandle(t)
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	early := insertEntry(t, handle, row{OccurredAt: base.Add(-2 * time.Hour), Action: "a.create"})
	middle := insertEntry(t, handle, row{OccurredAt: base, Action: "a.update"})
	late := insertEntry(t, handle, row{OccurredAt: base.Add(2 * time.Hour), Action: "a.create"})
	journal := newTestJournal(handle, settings{})
	ctx := tenant.CrossTenant(t.Context())

	since := base.Add(-time.Hour)
	until := base.Add(time.Hour)
	page, _, err := journal.Query(ctx, Query{Since: since, Until: until})
	if err != nil {
		t.Fatalf("the bounded read failed: %v", err)
	}
	if len(page) != 1 || page[0].ID != middle {
		t.Fatalf("the window read %d row(s), want only the row %d inside it", len(page), middle)
	}

	page, _, err = journal.Query(ctx, Query{Actions: []string{"a.create"}})
	if err != nil {
		t.Fatalf("the action-filtered read failed: %v", err)
	}
	got := map[uint64]bool{}
	for _, r := range page {
		got[r.ID] = true
	}
	if len(got) != 2 || !got[early] || !got[late] {
		t.Fatalf("the action filter read %v, want the two create rows %d and %d", got, early, late)
	}
}

// The outcome and actor filters are sets: a row outside the named set is left
// out, and each combines with the other and the action set.
func TestQueryNarrowsByOutcomeAndActor(t *testing.T) {
	handle := readHandle(t)
	okAlice := insertEntry(t, handle, row{Action: "a.create", Actor: "alice", Outcome: "ok"})
	failedAlice := insertEntry(t, handle, row{Action: "a.create", Actor: "alice", Outcome: "failed"})
	okBob := insertEntry(t, handle, row{Action: "a.update", Actor: "bob", Outcome: "ok"})
	system := insertEntry(t, handle, row{Action: "a.create", Actor: "", Outcome: "ok"})
	journal := newTestJournal(handle, settings{})
	ctx := tenant.CrossTenant(t.Context())

	ids := func(q Query) map[uint64]bool {
		t.Helper()
		page, _, err := journal.Query(ctx, q)
		if err != nil {
			t.Fatalf("query %+v failed: %v", q, err)
		}
		got := map[uint64]bool{}
		for _, r := range page {
			got[r.ID] = true
		}
		return got
	}
	same := func(got map[uint64]bool, want ...uint64) bool {
		if len(got) != len(want) {
			return false
		}
		for _, id := range want {
			if !got[id] {
				return false
			}
		}
		return true
	}

	if got := ids(Query{Outcomes: []string{"failed"}}); !same(got, failedAlice) {
		t.Errorf("the outcome filter read %v, want only the failed row %d", got, failedAlice)
	}
	if got := ids(Query{Outcomes: []string{"ok"}}); !same(got, okAlice, okBob, system) {
		t.Errorf("the outcome filter read %v, want the three ok rows", got)
	}
	if got := ids(Query{Actors: []string{"alice"}}); !same(got, okAlice, failedAlice) {
		t.Errorf("the actor filter read %v, want alice's two rows", got)
	}
	if got := ids(Query{Actors: []string{"bob", "carol"}}); !same(got, okBob) {
		t.Errorf("the actor set read %v, want only bob's row %d", got, okBob)
	}
	// An actor of the empty string is a row with no actor, so an empty item
	// selects exactly those rows; the management route refuses an empty item
	// before the read, so it is reachable through this read surface alone.
	if got := ids(Query{Actors: []string{""}}); !same(got, system) {
		t.Errorf("the actor filter read %v for an empty actor, want the row with no actor %d", got, system)
	}
	if got := ids(Query{Outcomes: []string{"failed"}, Actors: []string{"alice"}}); !same(got, failedAlice) {
		t.Errorf("the outcome and actor filters read %v, want only %d", got, failedAlice)
	}
	if got := ids(Query{Actions: []string{"a.update"}, Outcomes: []string{"ok"}, Actors: []string{"bob"}}); !same(got, okBob) {
		t.Errorf("the action, outcome and actor filters read %v, want only bob's update %d", got, okBob)
	}
	if got := ids(Query{Outcomes: []string{"panic"}, Actors: []string{"nobody"}}); len(got) != 0 {
		t.Errorf("a filter no row matches read %v, want nothing", got)
	}
}

// A cross-tenant read reaches every tenant's rows and the rows that belong to
// no tenant, and a row written without a tenant reads back with an empty one.
func TestCrossTenantReadsEveryTenantAndNoTenant(t *testing.T) {
	handle := readHandle(t)
	alpha := insertEntry(t, handle, row{Action: "a.create", TenantID: ptr("alpha")})
	beta := insertEntry(t, handle, row{Action: "a.create", TenantID: ptr("beta")})
	none := insertEntry(t, handle, row{Action: "a.create"})
	journal := newTestJournal(handle, settings{})

	page, _, err := journal.Query(tenant.CrossTenant(t.Context()), Query{})
	if err != nil {
		t.Fatalf("the cross-tenant read failed: %v", err)
	}
	tenants := map[uint64]string{}
	for _, r := range page {
		tenants[r.ID] = r.Tenant
	}
	if len(tenants) != 3 {
		t.Fatalf("the cross-tenant read reached %d rows, want all three", len(tenants))
	}
	if tenants[alpha] != "alpha" || tenants[beta] != "beta" {
		t.Errorf("the named tenants read back as %q and %q, want alpha and beta", tenants[alpha], tenants[beta])
	}
	if tenants[none] != "" {
		t.Errorf("a row with no tenant read back as %q, want the empty string", tenants[none])
	}
}

// A read whose context neither carries a tenant nor crosses is refused by
// the tenancy capability.
func TestQueryWithoutATenantIsRefused(t *testing.T) {
	handle := readHandle(t)
	insertEntry(t, handle, row{Action: "a.create", TenantID: ptr("alpha")})
	journal := newTestJournal(handle, settings{})

	_, _, err := journal.Query(t.Context(), Query{})
	if !errors.Is(err, tenant.ErrNoTenantInContext) {
		t.Fatalf("a read with no tenant in its context gave %v, want the tenancy capability's refusal", err)
	}
}

// The page size takes the default when it is not asked for and is capped at the
// maximum when it is asked for too large.
func TestQueryPageLimitDefaultsAndCaps(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		handle := readHandle(t)
		for i := 0; i <= DefaultPageSize; i++ {
			insertEntry(t, handle, row{Action: "a.create"})
		}
		journal := newTestJournal(handle, settings{})

		page, next, err := journal.Query(tenant.CrossTenant(t.Context()), Query{})
		if err != nil {
			t.Fatalf("the default read failed: %v", err)
		}
		if len(page) != DefaultPageSize {
			t.Fatalf("a query that asked for no page size read %d rows, want the default %d", len(page), DefaultPageSize)
		}
		if next == "" {
			t.Errorf("the page holds a full default page and more rows follow it, but carries no cursor")
		}
	})

	t.Run("cap", func(t *testing.T) {
		handle := readHandle(t)
		for i := 0; i <= MaxPageSize; i++ {
			insertEntry(t, handle, row{Action: "a.create"})
		}
		journal := newTestJournal(handle, settings{})

		page, next, err := journal.Query(tenant.CrossTenant(t.Context()), Query{Limit: MaxPageSize + 100})
		if err != nil {
			t.Fatalf("the capped read failed: %v", err)
		}
		if len(page) != MaxPageSize {
			t.Fatalf("a query that asked for more than the maximum read %d rows, want the maximum %d", len(page), MaxPageSize)
		}
		if next != "" && next != strconv.FormatUint(page[len(page)-1].ID, 10) {
			t.Errorf("the cursor %q is not the last row's identifier", next)
		}
	})
}
