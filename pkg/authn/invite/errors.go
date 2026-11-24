package invite

import (
	"errors"
	"fmt"

	"github.com/vislake/speed/pkg/authn"
)

// domainErrors are the failures this module judges for itself; any other
// failure of a statement becomes authn.ErrUnavailable.
var domainErrors = []error{
	ErrInvitationNotFound,
	ErrInvitationExpired,
	ErrInvitationRevoked,
	ErrInvitationAlreadyAccepted,
	ErrInvitationPending,
	ErrInvitationMember,
	ErrInvitationTargetInvalid,
	ErrInvitationExpiryTooLong,
	ErrInvitationUnauthenticated,
	ErrInvitationEmailUnproved,
	ErrInvitationMembershipInactive,
	ErrInvitationTenantInactive,
	ErrInvitationRoleWithdrawn,
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
