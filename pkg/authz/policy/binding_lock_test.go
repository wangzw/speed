package policy

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authz"
)

// A binding delete is one of the two paths that decide the tenant's
// tenant-administrator floor, and it must take the same tenant lock an explicit
// membership removal holds.
//
// The case stands for the removal path: it takes the lock, runs the cascade
// that clearing the membership performs, and releases it. A binding delete that
// ignored the lock would remove the other administrator while the cascade was
// in flight; the domain would be left with neither binding even though each
// path had seen enough of them.
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

	// The membership removal's cascade clears the departing account's bindings
	// without taking the lock again, which is what keeps it from re-entering the
	// lock its removal already holds.
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
