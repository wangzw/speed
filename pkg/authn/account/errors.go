package account

import (
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
)

// domainErrors are the failures this module judges for itself. A failure that
// carries one of them leaves as it is; any other failure of a statement is the
// database not answering and leaves as authn.ErrUnavailable.
var domainErrors = []error{
	authn.ErrAccountNotFound,
	authn.ErrAccountExists,
	authn.ErrContactTaken,
	authn.ErrInvalidArgument,
	authn.ErrInvalidIdentifier,
	authn.ErrUsernameReserved,
	authn.ErrInvalidAccountID,
	authn.ErrInvalidCredentials,
	authn.ErrPasswordPolicy,
	authn.ErrIdentifierRequired,
	authn.ErrUsernameImmutable,
	authn.ErrBindingConflict,
	authn.ErrLastLoginMethod,
	authn.ErrLastTenantAdmin,
	authn.ErrAccountLastTenantAdmin,
	authn.ErrAccountOwnsOrganization,
	authn.ErrAccountUnavailable,
	authn.ErrCleanupIncomplete,
	authn.ErrThrottled,
	authn.ErrBusy,
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

func duplicate(err error) bool { return errors.Is(err, gorm.ErrDuplicatedKey) }
