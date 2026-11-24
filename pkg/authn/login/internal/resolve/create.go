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

const displayNameMax = 100

const (
	viaJIT    = "jit"
	viaSignup = "signup"
)

// displayNameOrDefault makes a name fit to store: invalid UTF-8 and control characters
// dropped, the ends trimmed, cut to displayNameMax characters, falling back to the username
// when it empties out.
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

// provision gives the host's Provisioner the account about to be created and returns it with
// the two fields the host may change; anything else is dropped. A refusal is
// authn.ErrProvisionRefused, any other failure an unavailable dependency.
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

// unwind deletes an account this login created and cannot finish, under any context, because a
// half-made account holds a unique identifier.
func (r *Resolver) unwind(ctx context.Context, id authn.AccountID) {
	// The account was just created and nothing else holds it, so a failed delete
	// leaves only one no login can reach.
	_ = r.admin.Delete(context.WithoutCancel(ctx), id, authn.ReasonSelf)
}

type newAccount struct {
	create     authn.CreateAccount
	link       authn.ExternalLink
	membership *authn.Membership
}

// createBound creates an account for an external identity, then binds it, then writes the
// membership, so the binding's uniqueness arbitrates a race: a loser deletes its account and
// reads the winner's binding. A failure after the account exists deletes it.
func (r *Resolver) createBound(ctx context.Context, n newAccount) (id authn.AccountID, created bool, err error) {
	acct, err := r.admin.Create(ctx, n.create)
	if errors.Is(err, authn.ErrAccountExists) {
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

// bindExisting binds an external identity to an existing account. A binding another login
// made for the same account is no failure; one for another account is authn.ErrBindingConflict.
// bound reports whether this call made it.
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
