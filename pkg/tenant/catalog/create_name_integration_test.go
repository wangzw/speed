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

func TestAConcurrentNameConflictAtCreationIsReportedAsOne(t *testing.T) {
	r := rosterOverPostgres(t)
	ctx := context.Background()

	const pairs = 25
	var wg sync.WaitGroup
	errs := make([][2]error, pairs)
	for i := 0; i < pairs; i++ {
		for side, prefix := range []string{"a", "b"} {
			wg.Add(1)
			go func(i, side int, prefix string) {
				defer wg.Done()
				errs[i][side] = r.Create(ctx, tenant.Tenant{
					ID:     tenant.ID(fmt.Sprintf("t-%s-%d", prefix, i)),
					Name:   fmt.Sprintf("name-%d", i),
					Status: tenant.StatusActive,
				})
			}(i, side, prefix)
		}
	}
	wg.Wait()

	var other []error
	for i, pair := range errs {
		won := 0
		for _, err := range pair {
			switch {
			case err == nil:
				won++
			case errors.Is(err, tenant.ErrNameConflict):
			default:
				won++
				other = append(other, err)
			}
		}
		if won != 1 {
			t.Fatalf("pair %d creating onto one name ended %v, want exactly one success", i, pair)
		}
	}
	if len(other) > 0 {
		t.Fatalf("%d of %d concurrent conflicts were not reported as ErrNameConflict; the first: %v",
			len(other), pairs, other[0])
	}
}
