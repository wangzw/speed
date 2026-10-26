package account

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// recordingMembershipHook records the removals it is called for and keeps what
// its cleanup made, so a case can tell a second call that finished the cleanup
// from one that built the same thing twice.
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

// recordingMembershipGuard records the removals it was asked about and answers
// with the error a case scripts.
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

// RemoveMembershipForDeletion is the account-deletion path: it neither asks the
// guard nor calls the membership lifecycle hook. The deletion's own cleanup
// withdraws what the modules hold, and a guard keeps a domain that stays in
// service governable rather than one whose owner is being erased.
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

// Remove calls the membership lifecycle hook after the removal commits, so a
// module that holds data keyed by the membership cleans it on the ordinary path.
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

// A failing membership hook leaves the removal in force and answers
// ErrCleanupIncomplete; repeating the same call finishes the cleanup without
// repeating what the hook already made.
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

		// The retry is the same call for the same membership, which is what an
		// idempotent cleanup makes.
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

// The guard is asked before the removal; a refusal is the call's error and
// leaves the membership in place. The lifecycle hook is not reached.
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

// The guards stand side by side: every one is asked, and one refusal is enough
// even when another allows the removal.
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

// The guard's lock is taken before the check, held across the removal's
// commit, and released before the lifecycle hooks run.
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

// A lock that cannot be taken is an unavailable dependency: the guards are not
// asked and the membership stays.
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

// eventLog is a mutex-protected list of steps that a guard, the removal and a
// hook append to, so their order is observable.
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

// eventMembershipGuard records its lock and the check. Its release records the
// unlock, which the product calls after the removal's commit.
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

// eventMembershipHook records that the lifecycle hook ran.
type eventMembershipHook struct{ events *eventLog }

func (h *eventMembershipHook) MembershipRemoved(context.Context, authn.AccountID, string) error {
	h.events.add("hook")
	return nil
}

// A guard that cannot answer is an unavailable dependency and not a pass: the
// removal is not carried out.
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

// Removing a membership that is not there is already removed: no guard decision
// is needed for a removal that takes nothing away. The lifecycle hook is still
// called, because a previous call's cleanup is what the retry finishes.
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

// Account deletion is the documented exception: it is not an explicit membership
// removal, so it neither asks the guard nor calls the membership lifecycle. The
// AccountDeleted hook and the membership events are the deletion's own paths.
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
