//go:build integration

package account

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/db"
)

// Runs on the PostgreSQL leg alone: SQLite answers a busy database instead of arbitrating on the unique index.
func postgresOnly(t *testing.T, run func(t *testing.T, h *harness)) {
	t.Helper()
	t.Run(string(db.Postgres), func(t *testing.T) {
		run(t, newHarness(t, db.Postgres))
	})
}

// Concurrent registrations of one username leave one account, the unique index on (kind, value) arbitrating.
func TestConcurrentRegistrationsOfOneUsernameLeaveOneAccount(t *testing.T) {
	postgresOnly(t, func(t *testing.T, h *harness) {
		h.openSelfRegistration()
		const attempts = 8
		var wg sync.WaitGroup
		results := make([]error, attempts)
		start := make(chan struct{})
		for i := range attempts {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				_, results[i] = h.register("racer", fmt.Sprintf("racer-%d@example.com", i), "password123")
			}(i)
		}
		close(start)
		wg.Wait()

		succeeded := 0
		for i, err := range results {
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, authn.ErrAccountExists):
			default:
				t.Errorf("attempt %d gave %v, want nil or ErrAccountExists", i, err)
			}
		}
		if succeeded != 1 {
			t.Errorf("%d of %d concurrent registrations succeeded, want one", succeeded, attempts)
		}
		if n := h.count(tableAccounts, ""); n != 1 {
			t.Errorf("the concurrent attempts left %d account(s), want one", n)
		}
		if n := h.count(tableIdentifiers, "kind = ? AND value = ?",
			string(authn.IdentifierUsername), "racer"); n != 1 {
			t.Errorf("the concurrent attempts left %d username identifier(s), want one", n)
		}
	})
}

// Concurrent registrations of one email leave one reservation, the winner owning it and the rest ErrContactTaken.
func TestConcurrentRegistrationsOfOneEmailLeaveOneAccount(t *testing.T) {
	postgresOnly(t, func(t *testing.T, h *harness) {
		h.openSelfRegistration()
		const attempts = 8
		var wg sync.WaitGroup
		results := make([]error, attempts)
		start := make(chan struct{})
		for i := range attempts {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				_, results[i] = h.register(fmt.Sprintf("racer-%d", i), "shared@example.com", "password123")
			}(i)
		}
		close(start)
		wg.Wait()

		succeeded := 0
		for i, err := range results {
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, authn.ErrContactTaken):
			default:
				t.Errorf("attempt %d gave %v, want nil or ErrContactTaken", i, err)
			}
		}
		if succeeded != 1 {
			t.Errorf("%d of %d concurrent registrations succeeded, want one", succeeded, attempts)
		}
		if n := h.count(tableAccounts, ""); n != 1 {
			t.Errorf("the concurrent attempts left %d account(s), want one", n)
		}
		if n := h.count(tablePending, "kind = ? AND value = ? AND verified_at IS NULL",
			string(authn.IdentifierEmail), "shared@example.com"); n != 1 {
			t.Errorf("the concurrent attempts left %d reservation(s), want one", n)
		}
	})
}
