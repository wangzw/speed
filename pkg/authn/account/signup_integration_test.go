//go:build integration

package account

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/db"
)

// postgresOnly runs a case on the PostgreSQL leg alone. A case here needs real
// concurrent writers, which the SQLite file is not made for: several connections
// answer a busy database where PostgreSQL arbitrates on the unique index.
func postgresOnly(t *testing.T, run func(t *testing.T, h *harness)) {
	t.Helper()
	t.Run(string(db.Postgres), func(t *testing.T) {
		run(t, newHarness(t, db.Postgres))
	})
}

// Concurrent registrations of one username leave one account: the unique index
// on (kind, value) is the arbiter and every other call reads the name as taken.
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
				_, results[i] = h.register("racer", "racer@example.com", "password123")
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

// One address may be the pending target of several accounts, and the identifier
// table arbitrates the first one that proves it: the other call meets the unique
// constraint and keeps its pending contact.
func TestVerificationArbitratesBetweenAccountsSharingAnAddress(t *testing.T) {
	postgresOnly(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		h.openSelfRegistration()
		first := h.mustRegister("first", "shared@example.com", "password123")
		second := h.mustRegister("second", "shared@example.com", "password123")
		if n := h.count(tablePending, "kind = ? AND value = ?",
			string(authn.IdentifierEmail), "shared@example.com"); n != 2 {
			t.Fatalf("the shared address is %d pending rows, want 2", n)
		}

		if err := h.p.AddIdentifier(ctx, first.ID, authn.IdentifierEmail, "shared@example.com",
			authn.Verification{Via: authn.VerifiedViaEmailCode}); err != nil {
			t.Fatalf("the first account proving the address: %v", err)
		}
		if err := h.p.AddIdentifier(ctx, second.ID, authn.IdentifierEmail, "shared@example.com",
			authn.Verification{Via: authn.VerifiedViaEmailCode}); !errors.Is(err, authn.ErrAccountExists) {
			t.Fatalf("the second account proving the same address gave %v, want ErrAccountExists", err)
		}
		if n := h.count(tablePending, "account_id = ? AND value = ?", second.ID.String(), "shared@example.com"); n != 1 {
			t.Errorf("the losing account kept %d pending contact(s), want its one", n)
		}
		status, err := h.p.SignupStateOf(ctx, second.ID)
		if err != nil {
			t.Fatalf("reading the losing account: %v", err)
		}
		if status.State != authn.SignupPending {
			t.Errorf("the losing account reads %v, want pending", status.State)
		}

		// The address is now an identifier, so the reverse lookup answers the
		// account still listing it as a pending target.
		owner, ok, err := h.p.PendingAccountByContact(ctx, "email", "shared@example.com")
		if err != nil || !ok || owner != second.ID.String() {
			t.Errorf("the reverse lookup gave (%q, %v, %v), want the losing account alone", owner, ok, err)
		}
	})
}
