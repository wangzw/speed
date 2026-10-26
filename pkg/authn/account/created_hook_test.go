package account

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// Create calls the created hook after the account is committed, so a module
// that holds something with the account's lifecycle builds it when the account
// begins to exist.
func TestCreateCallsTheCreatedHook(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		account, err := h.p.Create(context.Background(), authn.CreateAccount{
			Username: "newcomer", Email: "newcomer@example.com",
			EmailVerifiedVia: authn.Verification{Via: authn.VerifiedViaAdmin},
			Via:              "admin",
		})
		if err != nil {
			t.Fatalf("creating: %v", err)
		}
		want := []string{"created:" + account.ID.String()}
		if seen := h.hook.seen(); !slices.Equal(seen, want) {
			t.Errorf("the hooks saw %v, want %v", seen, want)
		}
	})
}

// Register calls the created hook too: a self-registered account begins to
// exist exactly as one an administrator creates.
func TestRegisterCallsTheCreatedHook(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		h.openSelfRegistration()
		account := h.mustRegister("newcomer", "newcomer@example.com", "correct horse battery")
		want := []string{"created:" + account.ID.String()}
		if seen := h.hook.seen(); !slices.Equal(seen, want) {
			t.Errorf("the hooks saw %v, want %v", seen, want)
		}
	})
}

// createdOnceHook models an implementer's idempotent creation: the first call
// may fail, and every call for one account leaves one entry.
type createdOnceHook struct {
	authn.AccountLifecycle
	failures int
	calls    int
	owned    map[authn.AccountID]struct{}
}

func (h *createdOnceHook) AccountCreated(_ context.Context, id authn.AccountID) error {
	h.calls++
	if h.failures > 0 {
		h.failures--
		return errors.New("the personal tenant store is down")
	}
	if h.owned == nil {
		h.owned = map[authn.AccountID]struct{}{}
	}
	h.owned[id] = struct{}{}
	return nil
}

// A failing created hook leaves the account in being and the call is
// ErrCleanupIncomplete; the retry of the same hook call completes without
// creating anything a second time.
func TestAFailingCreatedHookLeavesTheAccountAndTheRetryCompletes(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		text, err := newID()
		if err != nil {
			t.Fatalf("minting an identifier: %v", err)
		}
		id := authn.AccountID(text)
		hook := &createdOnceHook{failures: 1}
		h.p.hooks = []authn.AccountLifecycle{hook}
		account, err := h.p.Create(ctx, authn.CreateAccount{
			ID: id, Username: "unlucky-create", Email: "unlucky-create@example.com",
			EmailVerifiedVia: authn.Verification{Via: authn.VerifiedViaAdmin},
			Via:              "admin",
		})
		if !errors.Is(err, authn.ErrCleanupIncomplete) {
			t.Fatalf("a failing created hook gave %v, want ErrCleanupIncomplete", err)
		}
		if account.ID != id {
			t.Errorf("the created account is %q, want %q: the account exists though its hook failed", account.ID, id)
		}
		if _, err := h.p.Get(ctx, id); err != nil {
			t.Fatalf("the account does not exist though its created hook failed: %v", err)
		}
		// The retry is the same hook call for the same account, which is what an
		// idempotent ensure makes.
		if err := h.p.callHooks(ctx, id, nil, func(hookCtx context.Context, hk authn.AccountLifecycle) error {
			return hk.AccountCreated(hookCtx, id)
		}); err != nil {
			t.Fatalf("retrying the created hook: %v", err)
		}
		if hook.calls != 2 || len(hook.owned) != 1 {
			t.Errorf("the hook ran %d times and holds %d entries, want two calls and one entry", hook.calls, len(hook.owned))
		}
	})
}

// deletedBeforeCreatedHook deletes the account on its first creation call, which
// is the deletion a retry of the creation hook can arrive after, and then does
// what an implementation does: reads the tombstone and returns nil.
type deletedBeforeCreatedHook struct {
	authn.AccountLifecycle
	admin    authn.AccountAdmin
	accounts authn.Accounts
	calls    int
	gone     bool
}

func (h *deletedBeforeCreatedHook) AccountCreated(ctx context.Context, id authn.AccountID) error {
	h.calls++
	if h.calls == 1 {
		if err := h.admin.Delete(ctx, id, authn.ReasonAdmin); err != nil {
			return err
		}
	}
	_, err := h.accounts.Get(ctx, id)
	switch {
	case errors.Is(err, authn.ErrAccountNotFound):
		h.gone = true
		return nil
	case err != nil:
		return err
	}
	return nil
}

func (h *deletedBeforeCreatedHook) AccountDeleted(context.Context, authn.AccountID) error { return nil }

// An account deleted before the created hook runs is no work for the hook: the
// creation call succeeds, and the hook reads a tombstone.
func TestTheCreatedHookToleratesAnAccountDeletedBeforeItRuns(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		hook := &deletedBeforeCreatedHook{admin: h.p, accounts: h.p, AccountLifecycle: h.hook}
		h.p.hooks = []authn.AccountLifecycle{hook}
		account, err := h.p.Create(ctx, authn.CreateAccount{
			Username: "gone-before", Email: "gone-before@example.com",
			EmailVerifiedVia: authn.Verification{Via: authn.VerifiedViaAdmin},
			Via:              "admin",
		})
		if err != nil {
			t.Fatalf("creating an account whose created hook finds a tombstone: %v", err)
		}
		if hook.calls != 1 || !hook.gone {
			t.Errorf("the created hook ran %d times and saw the tombstone %v, want one call and true",
				hook.calls, hook.gone)
		}
		if _, err := h.p.Get(ctx, account.ID); !errors.Is(err, authn.ErrAccountNotFound) {
			t.Errorf("the account reads %v, want the tombstone the hook wrote", err)
		}
	})
}
