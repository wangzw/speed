package account

import (
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
)

// domainErrors are the failures this module judges for itself. A failure that
// carries one of them leaves as it is; any other failure of a statement is the
// database not answering, and leaves as authn.ErrUnavailable with the database's own
// error in its chain, which goes to the log and never to a client.
var domainErrors = []error{
	authn.ErrAccountNotFound,
	authn.ErrAccountExists,
	authn.ErrContactTaken,
	authn.ErrInvalidArgument,
	authn.ErrInvalidIdentifier,
	authn.ErrInvalidAccountID,
	authn.ErrInvalidCredentials,
	authn.ErrPasswordPolicy,
	authn.ErrIdentifierRequired,
	authn.ErrBindingConflict,
	authn.ErrLastLoginMethod,
	authn.ErrCleanupIncomplete,
	authn.ErrThrottled,
	authn.ErrBusy,
	authn.ErrUnavailable,
}

// failed is the error a call returns for a failure of its own steps: a failure of
// this module's own judgement as it is, and anything else as an unavailable
// dependency. op says what was being done and is a fixed phrase.
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

// duplicate reports whether a statement was refused by a unique constraint, which
// the database module classifies the same way on both engines.
func duplicate(err error) bool { return errors.Is(err, gorm.ErrDuplicatedKey) }
