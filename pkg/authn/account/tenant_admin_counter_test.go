package account

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
)

// fakeTenantAdminCounter scripts the host seam's answer and remembers the
// accounts it was asked about. It also holds the one tenant lock the deletion
// takes, so a case can see whether the check and the cleanup ran under it.
type fakeTenantAdminCounter struct {
	mu      sync.Mutex
	tenant  string
	sole    bool
	err     error
	lockErr error
	asked   []string

	locked           int
	releases         int
	checkedUnderLock bool
	cleanedUnderLock bool
}

func (f *fakeTenantAdminCounter) SoleTenantAdminTenant(_ context.Context, account string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, account)
	f.checkedUnderLock = f.locked > 0
	return f.tenant, f.sole, f.err
}

func (f *fakeTenantAdminCounter) LockAccountAdministration(context.Context, string) (func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.lockErr != nil {
		return nil, f.lockErr
	}
	f.locked++
	var once sync.Once
	return func() {
		once.Do(func() {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.locked--
			f.releases++
		})
	}, nil
}

func (f *fakeTenantAdminCounter) observeCleanup() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleanedUnderLock = f.locked > 0
}

var _ authz.TenantAdminCounter = (*fakeTenantAdminCounter)(nil)

// lockObservingHook is a lifecycle hook that records whether the deletion's
// tenant lock was still held when the cleanup ran.
type lockObservingHook struct{ counter *fakeTenantAdminCounter }

func (lockObservingHook) AccountCreated(context.Context, authn.AccountID) error  { return nil }
func (lockObservingHook) AccountDisabled(context.Context, authn.AccountID) error { return nil }
func (lockObservingHook) AccountEnabled(context.Context, authn.AccountID) error  { return nil }
func (h lockObservingHook) AccountDeleted(context.Context, authn.AccountID) error {
	h.counter.observeCleanup()
	return nil
}

func (lockObservingHook) ServiceSecretRevoked(context.Context, authn.AccountID, string) error {
	return nil
}

// accountStatus reads the stored status of an account.
func (h *harness) accountStatus(t *testing.T, id authn.AccountID) int16 {
	t.Helper()
	var row accountRow
	if err := h.handle.Where("id = ?", id.String()).Take(&row).Error; err != nil {
		t.Fatalf("reading the account %q: %v", id, err)
	}
	return row.Status
}

// TestDeletingTheLastTenantAdministratorIsRefused holds the guard: the seam
// answers that this account is the only tenant administrator of a business
// tenant, the account is not deleted, and no cleanup hook runs. The refusal is
// the deletion's own code, carrying the tenant a surface names.
func TestDeletingTheLastTenantAdministratorIsRefused(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("last-admin", "correct horse battery")
		counter := &fakeTenantAdminCounter{tenant: "acme", sole: true}
		h.p.tenantAdminCounter = counter

		err := h.p.Delete(ctx, id, authn.ReasonAdmin)
		if !errors.Is(err, authn.ErrAccountLastTenantAdmin) {
			t.Fatalf("Delete = %v, want ErrAccountLastTenantAdmin", err)
		}
		if errors.Is(err, authn.ErrLastTenantAdmin) {
			t.Error("the deletion's refusal is also the membership removal's sentinel, want a code of its own")
		}
		var refusal *authn.AccountLastTenantAdminError
		if !errors.As(err, &refusal) {
			t.Fatalf("Delete = %v, want an error carrying the tenant", err)
		}
		if refusal.Tenant != "acme" {
			t.Errorf("the refusal names tenant %q, want acme", refusal.Tenant)
		}
		if got := h.accountStatus(t, id); got == statusDeleted {
			t.Error("the account was tombstoned although it is the last tenant administrator")
		}
		if seen := h.hook.seen(); len(seen) != 0 {
			t.Errorf("the lifecycle hooks ran %v, want none", seen)
		}
		if seen := counter.asked; len(seen) != 1 || seen[0] != id.String() {
			t.Errorf("the counter was asked about %v, want [%q]", seen, id)
		}
		if !counter.checkedUnderLock {
			t.Error("the last-administrator check ran without the tenant lock held")
		}
		if counter.releases != 1 {
			t.Errorf("the tenant lock was released %d times, want once", counter.releases)
		}
	})
}

// TestTheDeletionHoldsTheTenantLockAcrossTheCheckAndTheCleanup holds the
// serialisation: the check is asked and the AccountDeleted cleanup runs while
// the account's tenant lock is held, and the lock is given back exactly once
// when the deletion returns. Two deletions in one tenant therefore cannot each
// read the other's binding as present and then together leave the tenant with
// no administrator.
func TestTheDeletionHoldsTheTenantLockAcrossTheCheckAndTheCleanup(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("spare-admin", "correct horse battery")
		counter := &fakeTenantAdminCounter{tenant: "acme", sole: false}
		h.p.tenantAdminCounter = counter
		h.p.hooks = append(h.p.hooks, lockObservingHook{counter: counter})

		if err := h.p.Delete(ctx, id, authn.ReasonAdmin); err != nil {
			t.Fatalf("Delete = %v, want nil", err)
		}
		if !counter.checkedUnderLock {
			t.Error("the last-administrator check ran without the tenant lock held")
		}
		if !counter.cleanedUnderLock {
			t.Error("the binding cleanup ran after the tenant lock was released")
		}
		if counter.releases != 1 {
			t.Errorf("the tenant lock was released %d times, want once", counter.releases)
		}
	})
}

// A lock that cannot be taken is ErrUnavailable and the deletion does not run:
// an unjudgeable floor is never permission.
func TestALockThatCannotBeTakenRefusesTheDeletion(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("unlockable", "correct horse battery")
		counter := &fakeTenantAdminCounter{lockErr: errors.New("the lock store is down")}
		h.p.tenantAdminCounter = counter

		err := h.p.Delete(ctx, id, authn.ReasonAdmin)
		if !errors.Is(err, authn.ErrUnavailable) {
			t.Fatalf("Delete = %v, want ErrUnavailable", err)
		}
		if got := h.accountStatus(t, id); got == statusDeleted {
			t.Error("the account was tombstoned although the tenant lock could not be taken")
		}
		if len(counter.asked) != 0 {
			t.Errorf("the counter was asked about %v although its lock failed", counter.asked)
		}
	})
}

// A second tenant administrator means the deletion may proceed.
func TestDeletingAnAccountWithASecondTenantAdministratorProceeds(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("spare-admin", "correct horse battery")
		h.p.tenantAdminCounter = &fakeTenantAdminCounter{tenant: "acme", sole: false}

		if err := h.p.Delete(ctx, id, authn.ReasonAdmin); err != nil {
			t.Fatalf("Delete = %v, want nil", err)
		}
		if got := h.accountStatus(t, id); got != statusDeleted {
			t.Errorf("the account status = %d, want deleted", got)
		}
	})
}

// An account whose only administrator binding is in a personal tenant is not
// protected: the host's counter answers "not sole" for it, and the deletion
// proceeds.
func TestAPersonalTenantDoesNotProtectTheAccount(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("personal-owner", "correct horse battery")
		h.p.tenantAdminCounter = &fakeTenantAdminCounter{tenant: "personal-" + id.String(), sole: false}

		if err := h.p.Delete(ctx, id, authn.ReasonAdmin); err != nil {
			t.Fatalf("Delete = %v, want nil", err)
		}
		if got := h.accountStatus(t, id); got != statusDeleted {
			t.Errorf("the account status = %d, want deleted", got)
		}
	})
}

// A seam that cannot answer is a 503-shaped failure, never a pass: an unknown
// floor must not let the deletion through.
func TestAnUnreadableCounterRefusesTheDeletion(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("unknown-floor", "correct horse battery")
		h.p.tenantAdminCounter = &fakeTenantAdminCounter{err: errors.New("the policy store is down")}

		err := h.p.Delete(ctx, id, authn.ReasonAdmin)
		if !errors.Is(err, authn.ErrUnavailable) {
			t.Fatalf("Delete = %v, want ErrUnavailable", err)
		}
		if got := h.accountStatus(t, id); got == statusDeleted {
			t.Error("the account was tombstoned although the counter could not answer")
		}
		if seen := h.hook.seen(); len(seen) != 0 {
			t.Errorf("the lifecycle hooks ran %v, want none", seen)
		}
	})
}

// An assembly that supplies no counter keeps the deletion behaviour it had
// before the seam existed.
func TestNoCounterKeepsTheExistingDeletionBehaviour(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("no-counter", "correct horse battery")
		h.p.tenantAdminCounter = nil

		if err := h.p.Delete(ctx, id, authn.ReasonAdmin); err != nil {
			t.Fatalf("Delete = %v, want nil", err)
		}
		if got := h.accountStatus(t, id); got != statusDeleted {
			t.Errorf("the account status = %d, want deleted", got)
		}
	})
}

// A repeated delete of a tombstone finishes the cleanup instead of being
// refused: there is no live account left whose administrator floor the guard
// protects.
func TestRepeatingTheDeleteOfATombstoneStillFinishesTheCleanup(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("already-gone", "correct horse battery")
		if err := h.p.Delete(ctx, id, authn.ReasonAdmin); err != nil {
			t.Fatalf("deleting: %v", err)
		}
		counter := &fakeTenantAdminCounter{tenant: "acme", sole: true}
		h.p.tenantAdminCounter = counter

		if err := h.p.Delete(ctx, id, authn.ReasonAdmin); err != nil {
			t.Fatalf("repeating the delete = %v, want nil", err)
		}
		if len(counter.asked) != 0 {
			t.Errorf("the counter was asked about a tombstone: %v", counter.asked)
		}
	})
}

// Deleting an account that does not exist stays ErrAccountNotFound, and the
// counter is not asked about it.
func TestDeletingAnUnknownAccountDoesNotAskTheCounter(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		counter := &fakeTenantAdminCounter{tenant: "acme", sole: true}
		h.p.tenantAdminCounter = counter

		err := h.p.Delete(context.Background(), authn.AccountID("acc-does-not-exist"), authn.ReasonAdmin)
		if !errors.Is(err, authn.ErrAccountNotFound) {
			t.Fatalf("Delete = %v, want ErrAccountNotFound", err)
		}
		if len(counter.asked) != 0 {
			t.Errorf("the counter was asked about an unknown account: %v", counter.asked)
		}
	})
}
