package resolve

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"unicode"

	"github.com/vislake/speed/pkg/authn"
)

// displayNameMax is the most characters of a display name, as
// AccountAdmin.SetDisplayName states it.
const displayNameMax = 100

// The values of CreateAccount.Via the resolver writes.
const (
	viaJIT    = "jit"
	viaSignup = "signup"
)

// displayNameOrDefault is a name made fit to store: invalid UTF-8 and control
// characters dropped, the ends trimmed, and cut to displayNameMax characters. A
// name that empties out falls back to the username the creation carries. It
// decides nothing.
func displayNameOrDefault(name, username string) string {
	name = strings.ToValidUTF8(name, "")
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if runes := []rune(name); len(runes) > displayNameMax {
		name = string(runes[:displayNameMax])
	}
	if name == "" {
		return username
	}
	return name
}

// provision gives the host's Provisioner the account that is about to be created
// and returns it with the two fields the host may change. Anything else the hook
// returned is dropped: the account is created from what the resolver decided.
// A refusal is authn.ErrProvisionRefused; any other failure is a fault of the
// seam, which refuses the login in the same way and reads as an unavailable
// dependency. The display name is made fit to store after the hook has had its
// say, whichever source decided the account, and falls back to the username when
// nothing is left.
func (r *Resolver) provision(ctx context.Context, in authn.CreateAccount, via string) (authn.CreateAccount, error) {
	if r.provisioner != nil {
		out, err := r.provisioner.Provision(ctx, in.Clone(), via)
		switch {
		case errors.Is(err, authn.ErrProvisionRefused):
			return in, fmt.Errorf("resolve: %w", authn.ErrProvisionRefused)
		case err != nil:
			return in, &seamFault{seam: "the provisioner", cause: err}
		}
		in.DisplayName = out.DisplayName
		in.Attributes = maps.Clone(out.Attributes)
	}
	in.DisplayName = displayNameOrDefault(in.DisplayName, in.Username)
	return in, nil
}

// unwind deletes an account this login created and cannot finish. It runs
// whatever became of the login's context, because a half-made account holds a
// unique identifier and would turn the next attempt into a failure.
func (r *Resolver) unwind(ctx context.Context, id authn.AccountID) {
	// The account was made a moment ago by this login and nothing else holds it,
	// so a failure of the delete leaves only an account no login can reach.
	_ = r.admin.Delete(context.WithoutCancel(ctx), id, authn.ReasonSelf)
}

// newAccount is the account an external login creates and what binds it.
type newAccount struct {
	create authn.CreateAccount
	link   authn.ExternalLink
	// membership, when set, is written once the binding stands.
	membership *authn.Membership
}

// createBound creates an account for an external identity, binds the identity to
// it and writes the membership, in an order that lets the uniqueness constraint
// of the binding arbitrate a race: the account first, the binding second, the
// membership last. The loser of a race deletes the account it made and reads the
// winner's binding, so two concurrent first logins end in one account. A
// failure after the account exists deletes it, so that the next attempt starts
// clean.
//
// created is false when the login ended on an account another login made.
func (r *Resolver) createBound(ctx context.Context, n newAccount) (id authn.AccountID, created bool, err error) {
	acct, err := r.admin.Create(ctx, n.create)
	if errors.Is(err, authn.ErrAccountExists) {
		// A concurrent login of this identity made its account first; the login
		// carries on from the binding it made.
		winner, found, rerr := r.binding(ctx, n.link.Provider, n.link.Subject)
		if rerr != nil {
			return "", false, rerr
		}
		if found {
			return winner, false, nil
		}
		return "", false, err
	}
	if err != nil {
		return "", false, err
	}
	if err := r.admin.LinkExternal(ctx, acct.ID, n.link); err != nil {
		r.unwind(ctx, acct.ID)
		if !errors.Is(err, authn.ErrBindingConflict) {
			return "", false, err
		}
		winner, found, rerr := r.binding(ctx, n.link.Provider, n.link.Subject)
		switch {
		case rerr != nil:
			return "", false, rerr
		case !found:
			return "", false, err
		}
		return winner, false, nil
	}
	if n.membership != nil {
		m := *n.membership
		m.Account = acct.ID
		if err := r.memberships.Put(ctx, m); err != nil {
			r.unwind(ctx, acct.ID)
			return "", false, err
		}
	}
	return acct.ID, true, nil
}

// binding reads the account an external identity is bound to. found is false
// when there is none.
func (r *Resolver) binding(ctx context.Context, provider, subject string) (id authn.AccountID, found bool, err error) {
	id, err = r.accounts.ByExternal(ctx, provider, subject)
	switch {
	case err == nil:
		return id, true, nil
	case errors.Is(err, authn.ErrAccountNotFound):
		return "", false, nil
	}
	return "", false, err
}

// bindExisting binds an external identity to an account that exists. A binding
// another login made first for the same account is no failure and the login
// carries on from it; one made for another account is authn.ErrBindingConflict.
// bound is true when this call made the binding.
func (r *Resolver) bindExisting(ctx context.Context, id authn.AccountID, l authn.ExternalLink) (bound bool, err error) {
	err = r.admin.LinkExternal(ctx, id, l)
	switch {
	case err == nil:
		return true, nil
	case !errors.Is(err, authn.ErrBindingConflict):
		return false, err
	}
	owner, found, rerr := r.binding(ctx, l.Provider, l.Subject)
	switch {
	case rerr != nil:
		return false, rerr
	case found && owner == id:
		return false, nil
	}
	return false, err
}
