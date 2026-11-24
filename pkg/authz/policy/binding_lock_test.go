package policy

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/core"
)

// TestTheSuppliedTenantLockIsTheOneTheBindingDeleteTakes checks that the supplied tenant lock is the one the binding delete takes.
func TestTheSuppliedTenantLockIsTheOneTheBindingDeleteTakes(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	mustPutRole(t, ctx, f, "acme", string(authz.TenantAdminRole))
	mustPutBinding(t, ctx, f, "acme", "acct-1", string(authz.TenantAdminRole))
	mustPutBinding(t, ctx, f, "acme", "acct-2", string(authz.TenantAdminRole))

	supplied := authz.NewInProcessTenantLock()
	reg := core.New()
	reg.Register(core.Module{
		Name:     "test.tenant-lock",
		Provides: []core.Provision{{Token: (*authz.TenantLock)(nil)}},
	})
	reg.Supply(supplied, "test.tenant-lock")
	if err := f.wire(reg); err != nil {
		t.Fatalf("wiring the host-supplied tenant lock: %v", err)
	}

	release, err := supplied.Lock(ctx, "acme")
	if err != nil {
		t.Fatalf("taking the supplied lock: %v", err)
	}
	defer release()

	waiting, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	err = f.DeleteBinding(waiting, "acme", "acct-2", authz.TenantAdminRole)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a binding delete under the supplied lock = %v, want it to wait until the context ends", err)
	}
	if got := f.bindingsOf(t, "acme", "acct-2"); len(got) != 1 {
		t.Fatalf("the binding delete ran under the supplied lock: %+v", got)
	}
}

// TestABindingDeleteWaitsForTheMembershipRemovalLock checks that a binding delete waits for the membership removal lock.
func TestABindingDeleteWaitsForTheMembershipRemovalLock(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	mustPutRole(t, ctx, f, "acme", string(authz.TenantAdminRole))
	mustPutBinding(t, ctx, f, "acme", "acct-1", string(authz.TenantAdminRole))
	mustPutBinding(t, ctx, f, "acme", "acct-2", string(authz.TenantAdminRole))

	release, err := f.Lock(ctx, "acme")
	if err != nil {
		t.Fatalf("taking the membership-removal lock: %v", err)
	}
	defer release()

	waiting, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	err = f.DeleteBinding(waiting, "acme", "acct-2", authz.TenantAdminRole)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a binding delete under the held removal lock = %v, want it to wait until the context ends", err)
	}
	if got := f.bindingsOf(t, "acme", "acct-2"); len(got) != 1 {
		t.Fatalf("the binding delete ran while the removal lock was held: %+v", got)
	}

	if err = f.ClearAccountBindings(ctx, "acme", "acct-1"); err != nil {
		t.Fatalf("clearing the departing account's bindings: %v", err)
	}
	release()

	err = f.DeleteBinding(ctx, "acme", "acct-2", authz.TenantAdminRole)
	if !errors.Is(err, authz.ErrLastTenantAdmin) {
		t.Fatalf("deleting the remaining tenant administrator after the cascade = %v, want ErrLastTenantAdmin", err)
	}
	if got := f.bindingsOf(t, "acme", "acct-2"); len(got) != 1 {
		t.Fatalf("the tenant's last administrator binding was removed: %+v", got)
	}
}
