package invite

import (
	"errors"
	"fmt"

	"github.com/vislake/speed/pkg/authn"
)

// domainErrors are the failures this module judges for itself, either its own
// sentinels or the account capability's. A failure that carries one of them
// leaves as it is; any other failure of a statement is the database not
// answering and leaves as authn.ErrUnavailable with the database's own error in
// its chain, which goes to the log and never to a client.
var domainErrors = []error{
	ErrInvitationNotFound,
	ErrInvitationExpired,
	ErrInvitationRevoked,
	ErrInvitationAlreadyAccepted,
	ErrInvitationPending,
	ErrInvitationUnauthenticated,
	ErrInvitationEmailUnproved,
	ErrInvitationMembershipInactive,
	ErrInvitationTenantInactive,
	ErrInvalidEmail,
	ErrInvite,
	authn.ErrAccountNotFound,
	authn.ErrAccountExists,
	authn.ErrContactTaken,
	authn.ErrInvalidArgument,
	authn.ErrInvalidIdentifier,
	authn.ErrUsernameReserved,
	authn.ErrBindingConflict,
	authn.ErrCleanupIncomplete,
	authn.ErrThrottled,
	authn.ErrUnavailable,
}

// failed is the error a call returns for a failure of its own steps: a failure
// this module judged as it is, and anything else as an unavailable dependency.
// op says what was being done and is a fixed phrase.
func failed(op string, err error) error {
	if err == nil {
		return nil
	}
	for _, domain := range domainErrors {
		if errors.Is(err, domain) {
			return err
		}
	}
	return fmt.Errorf("%w: %s: %w", authn.ErrUnavailable, op, err)
}
