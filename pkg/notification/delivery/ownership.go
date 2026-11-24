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

// checkDestination refuses a dispatch whose destination the recipient
// does not own, and passes one whose destination is the recipient's own
// identifier.
func (p *pipeline) checkDestination(ctx context.Context, d notification.Dispatch) error {
	value, ok := d.Params[destinationParam]
	if !ok {
		return nil
	}
	to, err := stringify(value)
	if err != nil {
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
			// The destination is not an identifier of this kind or belongs to no
			// account; another kind may still match.
		default:
			return false, fmt.Errorf("%s: looking the destination up as a %s identifier: %w", ModuleName, kind, err)
		}
	}
	return false, nil
}
