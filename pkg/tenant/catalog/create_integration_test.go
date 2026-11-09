//go:build integration

package catalog

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/tenant"
)

// arriveTogether holds the first n inserts into the roster's table until all n
// have arrived, so that n writes have each looked the table up before any of
// them inserts — the interleaving a race produces only some of the time.
func arriveTogether(t *testing.T, r *roster, n int) {
	t.Helper()
	var mu sync.Mutex
	arrived := 0
	all := make(chan struct{})
	err := r.handle.Callback().Create().Before("gorm:create").Register("test:arrive-together",
		func(tx *gorm.DB) {
			if tx.Statement.Table != tableName {
				return
			}
			mu.Lock()
			arrived++
			if arrived == n {
				close(all)
			}
			late := arrived > n
			mu.Unlock()
			if late {
				return
			}
			select {
			case <-all:
			case <-time.After(10 * time.Second):
			}
		})
	if err != nil {
		t.Fatalf("registering the callback: %v", err)
	}
}

// Creations of one identifier on two replicas all find it free before any has
// committed. The table decides: one tenant, and every other creation told
// ErrTenantExists rather than the driver's constraint violation — or, worse, a
// success that rewrote the winner's row.
func TestConcurrentCreationsOfOneIdentifierAreOneTenant(t *testing.T) {
	first := rosterOverPostgres(t)
	if err := first.read(context.Background()); err != nil {
		t.Fatalf("reading: %v", err)
	}
	replicas := []*roster{first, secondReplica(t, first)}

	const writers = 4
	arriveTogether(t, first, writers)
	var wg sync.WaitGroup
	errs := make([]error, writers)
	for i := range writers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = replicas[i%2].Create(context.Background(), tenant.Tenant{
				ID: "t_acme", Name: fmt.Sprintf("acme-%d", i), Status: tenant.StatusActive,
			})
		}(i)
	}
	wg.Wait()

	won := -1
	for i, err := range errs {
		switch {
		case err == nil && won < 0:
			won = i
		case err == nil:
			t.Fatalf("creations %d and %d of one identifier both succeeded", won, i)
		case !errors.Is(err, tenant.ErrTenantExists):
			t.Fatalf("creation %d gave %v, want ErrTenantExists", i, err)
		}
	}
	if won < 0 {
		t.Fatal("no creation of the identifier succeeded")
	}
	held, ok := rowOf(t, first, "t_acme")
	if !ok || held.Name != fmt.Sprintf("acme-%d", won) {
		t.Fatalf("the table holds %+v, want the row creation %d wrote", held, won)
	}
}
