package delivery

import (
	"context"
	"errors"
	"fmt"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/notification"
)

// destinationKinds are the kinds of identifier an external channel can address:
// an email address and an E.164 phone number. A username is an account handle
// and never a destination, so it is not one of them.
var destinationKinds = []authn.IdentifierKind{authn.IdentifierEmail, authn.IdentifierPhone}

// checkDestination refuses a dispatch whose destination the recipient does not
// own, and passes one whose destination is the recipient's own identifier.
//
// The destination is the address an external channel would send to, carried in
// the dispatch's parameters under destinationParam. A dispatch that carries
// none is unaffected: a channel that needs no destination has nothing to check,
// and a channel that needs one and does not find it fails there, as it did
// before. A destination that cannot be read as text is likewise left to the
// channel, which refuses it on its own.
//
// The rule runs over the recipient's own identifier list — read through the
// accounts capability, the only place that knows which handles an account
// holds — and asks the account store which account the destination belongs to,
// so the comparison uses the store's own normalization rather than a second
// one. The destination has to belong to the recipient under a kind the
// recipient actually holds; a destination bound to another account, bound to no
// account, or named for a recipient that holds no address identifier is refused
// with notification.ErrInvalidRequest, and nothing is enqueued. A recipient that
// names no live account has no identifiers to own a destination and is refused
// the same way; a failure to read the store is an outage and not a pass.
func (p *pipeline) checkDestination(ctx context.Context, d notification.Dispatch) error {
	value, ok := d.Params[destinationParam]
	if !ok {
		return nil
	}
	to, err := stringify(value)
	if err != nil {
		// The channel refuses an unreadable destination when it sends, and the
		// ownership rule has no address to compare.
		return nil
	}
	owned, err := p.ownsDestination(ctx, authn.AccountID(d.Recipient.UserID), to)
	if err != nil {
		return err
	}
	if !owned {
		return fmt.Errorf("%w: the destination is not one of the recipient's identifiers",
			notification.ErrInvalidRequest)
	}
	return nil
}

// ownsDestination reports whether a destination is one of the recipient's own
// identifiers. The identifier list is read first, so a recipient with no
// identifier at all — and a recipient that names no live account — owns
// nothing; each kind the recipient holds is then asked of the account store,
// whose lookup normalizes the value the way the store wrote it.
func (p *pipeline) ownsDestination(ctx context.Context, recipient authn.AccountID, destination string) (bool, error) {
	identifiers, err := p.accounts.Identifiers(ctx, recipient)
	switch {
	case err == nil:
	case errors.Is(err, authn.ErrAccountNotFound), errors.Is(err, authn.ErrInvalidAccountID):
		return false, nil
	default:
		return false, fmt.Errorf("%s: reading the recipient's identifiers: %w", ModuleName, err)
	}
	held := make(map[authn.IdentifierKind]struct{}, len(destinationKinds))
	for _, identifier := range identifiers {
		held[identifier.Kind] = struct{}{}
	}
	for _, kind := range destinationKinds {
		if _, ok := held[kind]; !ok {
			continue
		}
		owner, err := p.accounts.Lookup(ctx, kind, destination)
		switch {
		case err == nil:
			if owner == recipient {
				return true, nil
			}
		case errors.Is(err, authn.ErrAccountNotFound), errors.Is(err, authn.ErrInvalidIdentifier):
			// The destination is not an identifier of this kind, or belongs to
			// no account; another kind may still match.
		default:
			return false, fmt.Errorf("%s: looking the destination up as a %s identifier: %w", ModuleName, kind, err)
		}
	}
	return false, nil
}
