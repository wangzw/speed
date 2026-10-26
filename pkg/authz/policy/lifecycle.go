package policy

import (
	"context"
	"fmt"

	"github.com/vislake/speed/pkg/authn"
)

// AccountCreated implements authn.AccountLifecycle. A new account holds no
// binding yet; the binding its creator is given belongs to the tenant that
// creator builds, not to this module. The hook is a no-op and idempotent.
func (p *product) AccountCreated(context.Context, authn.AccountID) error { return nil }

// AccountDisabled implements authn.AccountLifecycle. This module holds nothing
// keyed by an account's enabled state: a disabled account's bindings stay, and
// the account module is what refuses a disabled account before a request asks
// this module anything. The hook is a no-op and idempotent.
func (p *product) AccountDisabled(context.Context, authn.AccountID) error { return nil }

// AccountEnabled implements authn.AccountLifecycle. A disable leaves the
// bindings, so enabling has nothing to give back: the account module stops
// refusing the account and the bindings are in force again. The hook is a no-op
// and idempotent.
func (p *product) AccountEnabled(context.Context, authn.AccountID) error { return nil }

// AccountDeleted implements authn.AccountLifecycle: it removes every binding
// row the account holds, across every tenant.
//
// The cleanup is idempotent — deleting rows that are already gone is no work —
// and it crosses tenants, because an account is one subject in whichever
// tenants it holds roles and the account module's deletion is not scoped to
// one. A binding row that outlives the deletion names an account the
// authentication side can no longer produce, so the account is unusable there
// whether or not the row arrived; the cleanup is what keeps the table from
// growing rows for accounts that are gone.
func (p *product) AccountDeleted(ctx context.Context, id authn.AccountID) error {
	err := p.crossing(ctx).Where("account_id = ?", string(id)).Delete(&bindingRow{}).Error
	if err != nil {
		return fmt.Errorf("authz/policy: clearing the bindings of the deleted account %q: %w", id, err)
	}
	return p.afterWrite(ctx)
}

// ServiceSecretRevoked implements authn.AccountLifecycle. A service account's
// secrets are the authentication side's credentials and hold no policy of their
// own, so revoking one leaves this module's rows as they were.
func (p *product) ServiceSecretRevoked(context.Context, authn.AccountID, string) error { return nil }
