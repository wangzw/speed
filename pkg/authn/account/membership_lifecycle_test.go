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
	mu    sync.Mutex
	calls []string
	err   error
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
