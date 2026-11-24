package account

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

type recordingMembershipHook struct {
	authn.MembershipLifecycle
	mu      sync.Mutex
	calls   []string
	fail    error
	failed  bool
	cleaned map[string]bool
}

func (h *recordingMembershipHook) MembershipRemoved(_ context.Context, id authn.AccountID, tenant string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, tenant+":"+id.String())
	if h.fail != nil && !h.failed {
		h.failed = true
		return h.fail
	}
	if h.cleaned == nil {
		h.cleaned = map[string]bool{}
	}
	h.cleaned[tenant+":"+id.String()] = true
	return nil
}

func (h *recordingMembershipHook) seen() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.calls...)
}

func (h *recordingMembershipHook) cleanedCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.cleaned)
}

type recordingMembershipGuard struct {
	mu      sync.Mutex
	calls   []string
	locked  []string
	err     error
	lockErr error
	release func()
}

func (g *recordingMembershipGuard) Lock(_ context.Context, tenant string) (func(), error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.lockErr != nil {
		return nil, g.lockErr
	}
	g.locked = append(g.locked, tenant)
	return func() {
		if g.release != nil {
			g.release()
		}
	}, nil
}

func (g *recordingMembershipGuard) lockedFor() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.locked...)
}

func (g *recordingMembershipGuard) CheckMembershipRemoval(_ context.Context, id authn.AccountID, tenant string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls = append(g.calls, tenant+":"+id.String())
	return g.err
}

func (g *recordingMembershipGuard) seen() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.calls...)
}

// RemoveMembershipForDeletion is the account-deletion path: it asks neither the guard nor the membership lifecycle hook.
func TestRemoveMembershipForDeletionSkipsTheGuardAndTheHook(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("wound-up-member", "")
		if err := h.p.Put(ctx, authn.Membership{Account: id, Tenant: "acme", Active: true, Source: authn.ReasonAdmin}); err != nil {
			t.Fatalf("joining: %v", err)
		}
		guard := &recordingMembershipGuard{err: authn.ErrLastTenantAdmin}
		hook := &recordingMembershipHook{}
		h.p.membershipGuards = []authn.MembershipGuard{guard}
		h.p.membershipHooks = []authn.MembershipLifecycle{hook}

		if err := h.p.RemoveMembershipForDeletion(ctx, id, "acme"); err != nil {
			t.Fatalf("the deletion's removal was refused: %v", err)
		}
		if n := h.count(tableMemberships, "account_id = ? AND tenant_id = ?", id.String(), "acme"); n != 0 {
			t.Errorf("%d membership row(s) left after the deletion's removal, want none", n)
		}
		if got := guard.seen(); len(got) != 0 {
			t.Errorf("the guard was asked about %v on the deletion path, want nothing", got)
		}
		if got := hook.seen(); len(got) != 0 {
			t.Errorf("the membership hook ran %v on the deletion path, want nothing", got)
		}
	})
}

// Remove calls the membership lifecycle hook after the removal commits.
func TestRemoveCallsTheMembershipHook(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("leaver", "")
		if err := h.p.Put(ctx, authn.Membership{Account: id, Tenant: "acme", Active: true, Source: authn.ReasonAdmin}); err != nil {
			t.Fatalf("joining: %v", err)
		}
		hook := &recordingMembershipHook{}
		h.p.membershipHooks = []authn.MembershipLifecycle{hook}

		if err := h.p.Remove(ctx, id, "acme"); err != nil {
			t.Fatalf("removing: %v", err)
		}
		want := []string{"acme:" + id.String()}
		if got := hook.seen(); !slices.Equal(got, want) {
			t.Errorf("the membership hook saw %v, want %v", got, want)
		}
	})
}

// A failing membership hook leaves the removal in force and answers ErrCleanupIncomplete, and repeating the call finishes the cleanup without repeating what the hook made.
func TestAFailingMembershipHookLeavesTheRemovalAndTheRetryCompletes(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("unlucky-leaver", "")
		if err := h.p.Put(ctx, authn.Membership{Account: id, Tenant: "acme", Active: true, Source: authn.ReasonAdmin}); err != nil {
			t.Fatalf("joining: %v", err)
		}
		hook := &recordingMembershipHook{fail: errors.New("the policy store is down")}
		h.p.membershipHooks = []authn.MembershipLifecycle{hook}

		if err := h.p.Remove(ctx, id, "acme"); !errors.Is(err, authn.ErrCleanupIncomplete) {
			t.Fatalf("a failing membership hook gave %v, want ErrCleanupIncomplete", err)
		}
		if n := h.count(tableMemberships, "account_id = ? AND tenant_id = ?", id.String(), "acme"); n != 0 {
			t.Errorf("%d membership row(s) left after the failed cleanup, want none: the removal is in force", n)
		}

		if err := h.p.Remove(ctx, id, "acme"); err != nil {
			t.Fatalf("retrying the removal: %v", err)
		}
		if got := hook.seen(); len(got) != 2 {
			t.Errorf("the membership hook ran %d time(s), want twice: once that failed and once on the retry", len(got))
		}
		if got := hook.cleanedCount(); got != 1 {
			t.Errorf("the membership hook holds %d cleaned membership(s), want one", got)
		}
	})
}

// The guard is asked before the removal; a refusal is the call's error and leaves the membership in place.
func TestTheGuardRefusesTheRemovalAndLeavesTheMembership(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("last-admin", "")
		if err := h.p.Put(ctx, authn.Membership{Account: id, Tenant: "acme", Active: true, Source: authn.ReasonAdmin}); err != nil {
			t.Fatalf("joining: %v", err)
		}
		guard := &recordingMembershipGuard{err: authn.ErrLastTenantAdmin}
		hook := &recordingMembershipHook{}
		h.p.membershipGuards = []authn.MembershipGuard{guard}
		h.p.membershipHooks = []authn.MembershipLifecycle{hook}

		if err := h.p.Remove(ctx, id, "acme"); !errors.Is(err, authn.ErrLastTenantAdmin) {
			t.Fatalf("a refused removal gave %v, want ErrLastTenantAdmin", err)
		}
		if n := h.count(tableMemberships, "account_id = ? AND tenant_id = ?", id.String(), "acme"); n != 1 {
			t.Errorf("%d membership row(s) left, want the one the guard protected", n)
		}
		want := []string{"acme:" + id.String()}
		if got := guard.seen(); !slices.Equal(got, want) {
			t.Errorf("the guard saw %v, want %v", got, want)
		}
		if got := hook.seen(); len(got) != 0 {
			t.Errorf("the lifecycle hook ran %v though the guard refused the removal", got)
		}
	})
}

// Every guard is asked, and one refusal is enough even when another allows the removal.
func TestEveryMembershipGuardIsAsked(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("two-guards", "")
		if err := h.p.Put(ctx, authn.Membership{Account: id, Tenant: "acme", Active: true, Source: authn.ReasonAdmin}); err != nil {
			t.Fatalf("joining: %v", err)
		}
		allow := &recordingMembershipGuard{}
		refuse := &recordingMembershipGuard{err: authn.ErrLastTenantAdmin}
		h.p.membershipGuards = []authn.MembershipGuard{allow, refuse}

		if err := h.p.Remove(ctx, id, "acme"); !errors.Is(err, authn.ErrLastTenantAdmin) {
			t.Fatalf("a removal one guard refused gave %v, want ErrLastTenantAdmin", err)
		}
		for name, guard := range map[string]*recordingMembershipGuard{"allowing": allow, "refusing": refuse} {
			if got := guard.seen(); len(got) != 1 {
				t.Errorf("the %s guard was asked %v, want one question", name, got)
			}
		}
		if n := h.count(tableMemberships, "account_id = ? AND tenant_id = ?", id.String(), "acme"); n != 1 {
			t.Errorf("%d membership row(s) left, want the one a guard protected", n)
		}
	})
}

type sharedGuardLock struct {
	gate chan struct{}
}

func newSharedGuardLock() *sharedGuardLock {
	return &sharedGuardLock{gate: make(chan struct{}, 1)}
}

func (l *sharedGuardLock) take(ctx context.Context) (func(), error) {
	select {
	case l.gate <- struct{}{}:
		var once sync.Once
		return func() { once.Do(func() { <-l.gate }) }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type sharingGuard struct {
	lock *sharedGuardLock
}

func (g *sharingGuard) MembershipLock() any { return g.lock }

func (g *sharingGuard) Lock(ctx context.Context, _ string) (func(), error) {
	return g.lock.take(ctx)
}

func (g *sharingGuard) CheckMembershipRemoval(context.Context, authn.AccountID, string) error {
	return nil
}

// Two guards wired to one lock object take it once for a removal.
func TestTwoGuardsSharingOneLockTakeItOnce(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("shared-lock-member", "")
		if err := h.p.Put(ctx, authn.Membership{Account: id, Tenant: "acme", Active: true, Source: authn.ReasonAdmin}); err != nil {
			t.Fatalf("joining: %v", err)
		}
		lock := newSharedGuardLock()
		h.p.membershipGuards = []authn.MembershipGuard{
			&sharingGuard{lock: lock},
			&sharingGuard{lock: lock},
		}

		bounded, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		defer cancel()
		if err := h.p.Remove(bounded, id, "acme"); err != nil {
			t.Fatalf("removing beside two guards sharing one lock: %v", err)
		}
		if n := h.count(tableMemberships, "account_id = ? AND tenant_id = ?", id.String(), "acme"); n != 0 {
			t.Errorf("%d membership row(s) left after the removal, want none", n)
		}
	})
}

// The guard's lock is taken before the check, held across the commit, and released before the lifecycle hooks run.
func TestTheRemovalTakesTheGuardLockBeforeTheCheckAndAheadOfTheCommit(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("ordered-leaver", "")
		if err := h.p.Put(ctx, authn.Membership{Account: id, Tenant: "acme", Active: true, Source: authn.ReasonAdmin}); err != nil {
			t.Fatalf("joining: %v", err)
		}
		events := &eventLog{}
		guard := &eventMembershipGuard{events: events}
		hook := &eventMembershipHook{events: events}
		h.p.membershipGuards = []authn.MembershipGuard{guard}
		h.p.membershipHooks = []authn.MembershipLifecycle{hook}

		if err := h.p.Remove(ctx, id, "acme"); err != nil {
			t.Fatalf("removing: %v", err)
		}
		want := []string{"lock", "check", "unlock", "hook"}
		if got := events.snapshot(); !slices.Equal(got, want) {
			t.Errorf("the removal ran %v, want %v", got, want)
		}
	})
}

// A lock that cannot be taken is unavailable: the guards are not asked and the membership stays.
func TestALockThatCannotBeTakenRefusesTheRemoval(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("unlockable", "")
		if err := h.p.Put(ctx, authn.Membership{Account: id, Tenant: "acme", Active: true, Source: authn.ReasonAdmin}); err != nil {
			t.Fatalf("joining: %v", err)
		}
		guard := &recordingMembershipGuard{lockErr: errors.New("the advisory lock is unavailable")}
		h.p.membershipGuards = []authn.MembershipGuard{guard}

		err := h.p.Remove(ctx, id, "acme")
		if !errors.Is(err, authn.ErrUnavailable) {
			t.Fatalf("a lock that could not be taken gave %v, want ErrUnavailable", err)
		}
		if got := guard.lockedFor(); len(got) != 0 {
			t.Errorf("the guard recorded locks %v, want none", got)
		}
		if got := guard.seen(); len(got) != 0 {
			t.Errorf("the guard was asked about %v though the lock was not taken", got)
		}
		if n := h.count(tableMemberships, "account_id = ? AND tenant_id = ?", id.String(), "acme"); n != 1 {
			t.Errorf("%d membership row(s) left, want the one the untaken lock protected", n)
		}
	})
}

type eventLog struct {
	mu     sync.Mutex
	events []string
}

func (l *eventLog) add(event string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, event)
}

func (l *eventLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.events...)
}

type eventMembershipGuard struct {
	events *eventLog
}

func (g *eventMembershipGuard) Lock(context.Context, string) (func(), error) {
	g.events.add("lock")
	return func() { g.events.add("unlock") }, nil
}

func (g *eventMembershipGuard) CheckMembershipRemoval(context.Context, authn.AccountID, string) error {
	g.events.add("check")
	return nil
}

type eventMembershipHook struct{ events *eventLog }

func (h *eventMembershipHook) MembershipRemoved(context.Context, authn.AccountID, string) error {
	h.events.add("hook")
	return nil
}

// A guard that cannot answer is unavailable and not a pass: the removal is not carried out.
func TestAGuardThatCannotAnswerRefusesTheRemoval(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("guarded", "")
		if err := h.p.Put(ctx, authn.Membership{Account: id, Tenant: "acme", Active: true, Source: authn.ReasonAdmin}); err != nil {
			t.Fatalf("joining: %v", err)
		}
		h.p.membershipGuards = []authn.MembershipGuard{&recordingMembershipGuard{err: errors.New("the policy store is down")}}

		err := h.p.Remove(ctx, id, "acme")
		if !errors.Is(err, authn.ErrUnavailable) {
			t.Fatalf("a guard that could not answer gave %v, want ErrUnavailable", err)
		}
		if errors.Is(err, authn.ErrLastTenantAdmin) {
			t.Error("a guard failure reads as the last-tenant-administrator refusal")
		}
		if n := h.count(tableMemberships, "account_id = ? AND tenant_id = ?", id.String(), "acme"); n != 1 {
			t.Errorf("%d membership row(s) left, want the one the failed check protected", n)
		}
	})
}

// Removing a membership that is not there asks no guard, because it takes nothing away.
func TestRemovingAnAbsentMembershipDoesNotAskTheGuard(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("absent", "")
		guard := &recordingMembershipGuard{err: authn.ErrLastTenantAdmin}
		hook := &recordingMembershipHook{}
		h.p.membershipGuards = []authn.MembershipGuard{guard}
		h.p.membershipHooks = []authn.MembershipLifecycle{hook}

		if err := h.p.Remove(ctx, id, "acme"); err != nil {
			t.Fatalf("removing a membership that is not there gave %v, want nil", err)
		}
		if got := guard.seen(); len(got) != 0 {
			t.Errorf("the guard was asked %v about a removal that takes nothing away", got)
		}
		if got := hook.seen(); len(got) != 1 {
			t.Errorf("the lifecycle hook ran %v, want one call so a failed cleanup can finish", got)
		}
	})
}

// Account deletion neither asks the membership guard nor calls the membership lifecycle hook.
func TestDeletingAnAccountDoesNotConsultTheMembershipGuardOrHook(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("deleted", "")
		if err := h.p.Put(ctx, authn.Membership{Account: id, Tenant: "acme", Active: true, Source: authn.ReasonAdmin}); err != nil {
			t.Fatalf("joining: %v", err)
		}
		guard := &recordingMembershipGuard{err: authn.ErrLastTenantAdmin}
		hook := &recordingMembershipHook{}
		h.p.membershipGuards = []authn.MembershipGuard{guard}
		h.p.membershipHooks = []authn.MembershipLifecycle{hook}

		if err := h.p.Delete(ctx, id, authn.ReasonAdmin); err != nil {
			t.Fatalf("deleting the account: %v", err)
		}
		if got := guard.seen(); len(got) != 0 {
			t.Errorf("the guard was asked %v on the account-deletion path", got)
		}
		if got := hook.seen(); len(got) != 0 {
			t.Errorf("the membership lifecycle ran %v on the account-deletion path", got)
		}
		if n := h.count(tableMemberships, "account_id = ?", id.String()); n != 0 {
			t.Errorf("%d membership row(s) survived the account's deletion, want none", n)
		}
	})
}

// Re-adding a removed member writes the membership again and runs no removal hook.
func TestReAddingARemovedMemberRunsNoRemovalHook(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("returning-member", "")
		hook := &recordingMembershipHook{}
		h.p.membershipHooks = []authn.MembershipLifecycle{hook}

		join := authn.Membership{Account: id, Tenant: "acme", Active: true, Source: authn.ReasonAdmin}
		if err := h.p.Put(ctx, join); err != nil {
			t.Fatalf("joining: %v", err)
		}
		if err := h.p.Remove(ctx, id, "acme"); err != nil {
			t.Fatalf("removing: %v", err)
		}
		if err := h.p.Put(ctx, join); err != nil {
			t.Fatalf("rejoining: %v", err)
		}

		want := []string{"acme:" + id.String()}
		if got := hook.seen(); !slices.Equal(got, want) {
			t.Errorf("the membership hook saw %v, want the removal alone %v", got, want)
		}
		if n := h.count(tableMemberships, "account_id = ? AND tenant_id = ?", id.String(), "acme"); n != 1 {
			t.Errorf("%d membership row(s) after the rejoin, want one", n)
		}
	})
}
