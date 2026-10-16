//go:build integration

package catalog

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/vislake/speed/pkg/tenant"
)

// Renaming two tenants onto one alias at once both finds it free, and the
// second update meets the column's unique index. That is the same conflict the
// check inside the write reports, and the caller is owed the same answer for
// it: ErrAliasConflict naming the holder, not the driver's constraint
// violation.
func TestAConcurrentSetAliasConflictIsReportedAsOne(t *testing.T) {
	r := rosterOverPostgres(t)
	ctx := context.Background()

	const pairs = 25
	for i := 0; i < pairs; i++ {
		for _, prefix := range []string{"a", "b"} {
			id := fmt.Sprintf("t_%s_%d", prefix, i)
			if err := r.handle.Exec(insert(id, id)).Error; err != nil {
				t.Fatalf("seeding %q: %v", id, err)
			}
		}
	}

	var wg sync.WaitGroup
	errs := make([][2]error, pairs)
	for i := 0; i < pairs; i++ {
		for side, prefix := range []string{"a", "b"} {
			wg.Add(1)
			go func(i, side int, prefix string) {
				defer wg.Done()
				errs[i][side] = r.SetAlias(ctx, tenant.ID(fmt.Sprintf("t_%s_%d", prefix, i)),
					fmt.Sprintf("alias_%d", i))
			}(i, side, prefix)
		}
	}
	wg.Wait()

	var other []error
	for i, pair := range errs {
		refused := 0
		for _, err := range pair {
			switch {
			case err == nil:
			case errors.Is(err, tenant.ErrAliasConflict):
				refused++
			default:
				refused++
				other = append(other, err)
			}
		}
		if refused != 1 {
			t.Fatalf("pair %d renamed onto one alias ended %v, want exactly one refused", i, pair)
		}
	}
	if len(other) > 0 {
		t.Fatalf("%d of %d concurrent conflicts were not reported as ErrAliasConflict; the first: %v",
			len(other), pairs, other[0])
	}
}
