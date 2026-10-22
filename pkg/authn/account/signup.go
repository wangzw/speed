package account

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/model"
)

// signupLocaleAttr and signupTimeZoneAttr are the attribute keys a registration
// writes the caller's language and zone preferences to. They are the names the
// account attributes reserve for those two preferences.
const (
	signupLocaleAttr   = "i18n.locale"
	signupTimeZoneAttr = "i18n.tz"
)

// Register opens an account for anonymous self-registration.
//
// The password-signup setting is read here and is the authority: any value but
// open is authn.ErrSignupClosed, and a store that cannot be read reads as
// closed. The username and the email address are normalized first and the
// password is held to the policy; all of that happens before the back-off, and
// the back-off is read before the uniqueness check, so a request that cannot
// succeed costs no derivation. A username that is taken is authn.ErrAccountExists
// and counts one failure against the signup-username back-off; a concurrent
// registration that loses the unique constraint counts one as well. A derivation
// that finds its capacity full and a transaction that fails for any other reason
// count nothing, since neither says anything about the name. The derivation runs
// under the self-registration width and no memory budget of its own; a full width
// is authn.ErrBusy.
//
// One transaction writes the enabled account with signup_state 1, a username
// identifier written through self, a password credential and one email contact
// awaiting verification. No identifier awaits verification, no email is sent and
// no session is made.
func (p *product) Register(ctx context.Context, in authn.RegisterAccount) (authn.Account, error) {
	if p.passwordSignup() != signupOpen {
		return authn.Account{}, fmt.Errorf("%w", authn.ErrSignupClosed)
	}
	username, err := NormalizeIdentifier(authn.IdentifierUsername, in.Username)
	if err != nil {
		return authn.Account{}, err
	}
	email, err := NormalizeIdentifier(authn.IdentifierEmail, in.Email)
	if err != nil {
		return authn.Account{}, err
	}
	if err = checkDisplayName(in.DisplayName); err != nil {
		return authn.Account{}, err
	}
	attributes, err := signupAttributes(in)
	if err != nil {
		return authn.Account{}, err
	}
	password, err := normalizeNew(in.Password)
	if err != nil {
		return authn.Account{}, err
	}
	if err = p.policy(password, []string{username}); err != nil {
		return authn.Account{}, err
	}

	// The back-off is read before the uniqueness check and before any
	// derivation: a name under it is refused whatever its state.
	wait, err := p.Check(ctx, authn.ScopeSignupUsername, username)
	if err != nil {
		return authn.Account{}, err
	}
	if wait > 0 {
		return authn.Account{}, &authn.ThrottledError{RetryAfter: wait}
	}
	taken, err := usernameTaken(p.conn(ctx), username)
	if err != nil {
		return authn.Account{}, err
	}
	if taken {
		p.fail(ctx, authn.ScopeSignupUsername, username, "")
		return authn.Account{}, fmt.Errorf("%w", authn.ErrAccountExists)
	}

	phc, err := p.hashSignupPassword(ctx, password)
	if err != nil {
		return authn.Account{}, derivationFailed(err)
	}

	var row accountRow
	b := &batch{}
	err = p.conn(ctx).Transaction(func(tx *gorm.DB) error {
		now := p.at()
		var idErr error
		var id string
		if id, idErr = newID(); idErr != nil {
			return idErr
		}
		row = accountRow{
			ID: id, Kind: string(authn.KindUser), Status: statusActive, SignupState: signupPending,
			DisplayName: in.DisplayName, Attributes: attributes,
			ValidAfter: now, CreatedAt: now, UpdatedAt: now,
		}
		if idErr = tx.Create(&row).Error; idErr != nil {
			return idErr
		}
		identifierID, idErr := newID()
		if idErr != nil {
			return idErr
		}
		identifier := identifierRow{
			ID: identifierID, AccountID: id, Kind: string(authn.IdentifierUsername), Value: username,
			VerifiedAt: now, VerifiedVia: authn.VerifiedViaSelf, CreatedAt: now,
		}
		if idErr = tx.Create(&identifier).Error; idErr != nil {
			return idErr
		}
		if idErr = p.writeCredential(tx, id, phc, false); idErr != nil {
			return idErr
		}
		pendingID, idErr := newID()
		if idErr != nil {
			return idErr
		}
		pending := pendingContactRow{
			ID: pendingID, AccountID: id, Kind: string(authn.IdentifierEmail), Value: email,
			CreatedAt: now, UpdatedAt: now,
		}
		if idErr = tx.Create(&pending).Error; idErr != nil {
			return idErr
		}
		return p.emit(tx, b, accountCreated(authn.AccountID(id), authn.KindUser, bySignup), false)
	})
	switch {
	case duplicate(err):
		// The name was free when it was read and taken before the insert. The
		// unique constraint is the arbiter, and the loser counts the failure.
		p.fail(ctx, authn.ScopeSignupUsername, username, "")
		return authn.Account{}, fmt.Errorf("%w", authn.ErrAccountExists)
	case err != nil:
		return authn.Account{}, failed("registering an account", err)
	}
	p.publish(ctx, b)
	if err = p.Succeed(ctx, authn.ScopeSignupUsername, username); err != nil {
		p.logger.Warn(ModuleName+": the failure count of a username could not be cleared", slogCause(err))
	}
	return toAccount(row, true)
}

// signupAttributes is the attribute set a registration writes: the caller's
// language and zone when given, and nothing else. The bounds of an account's
// attribute set are judged here as on any other write.
func signupAttributes(in authn.RegisterAccount) (string, error) {
	attributes := map[string]string{}
	if in.Locale != "" {
		attributes[signupLocaleAttr] = in.Locale
	}
	if in.TimeZone != "" {
		attributes[signupTimeZoneAttr] = in.TimeZone
	}
	if err := model.CheckAccountAttributes(attributes); err != nil {
		return "", err
	}
	encoded, err := encodeAttributes(attributes)
	if err != nil {
		return "", invalid("the attributes cannot be written")
	}
	return encoded, nil
}

// hashSignupPassword derives the PHC text of a registration password under the
// self-registration width. A width that is full is authn.ErrBusy and takes no
// place from a password login; the memory one derivation takes is bounded by
// that width and is not budgeted separately.
func (p *product) hashSignupPassword(ctx context.Context, plaintext string) (string, error) {
	params, err := p.hashParams()
	if err != nil {
		return "", err
	}
	started := time.Now()
	phc, err := p.signupGov.Hash(ctx, params, plaintext, p.signupWait)
	if err != nil {
		return "", err
	}
	p.meters.derived(ctx, params.Algorithm, time.Since(started).Seconds())
	return phc, nil
}

// usernameTaken reports whether a normalized username is already an identifier.
func usernameTaken(conn *gorm.DB, username string) (bool, error) {
	var ids []string
	err := conn.Raw("SELECT account_id FROM "+tableIdentifiers+" WHERE kind = ? AND value = ?",
		string(authn.IdentifierUsername), username).Scan(&ids).Error
	if err != nil {
		return false, failed("reading a username", err)
	}
	return len(ids) > 0, nil
}

// SignupStateOf reads how far an account's self-registration has got. The state
// is read from the account row on every call; the address is the earliest-proved
// email identifier for Verified (ties broken by identifier), the pending email
// contact for Pending, and empty when there is nothing to report.
func (p *product) SignupStateOf(ctx context.Context, id authn.AccountID) (authn.SignupStatus, error) {
	conn := p.conn(ctx)
	row, err := loadAccount(conn, id)
	if err != nil {
		return authn.SignupStatus{}, failed("reading an account", err)
	}
	status := authn.SignupStatus{
		State: signupStateOf(row.SignupState),
		Since: timeOf(row.SignupVerifiedAt),
	}
	switch status.State {
	case authn.SignupVerified:
		email, readErr := earliestProvedEmail(conn, id.String())
		if readErr != nil {
			return authn.SignupStatus{}, failed("reading an account's identifiers", readErr)
		}
		status.Email = email
	case authn.SignupPending:
		var contacts []pendingContactRow
		if err = conn.Where("account_id = ? AND kind = ?", id.String(), string(authn.IdentifierEmail)).
			Limit(1).Find(&contacts).Error; err != nil {
			return authn.SignupStatus{}, failed("reading a pending contact", err)
		}
		if len(contacts) > 0 {
			status.Email = contacts[0].Value
		}
	}
	return status, nil
}

// earliestProvedEmail is the email identifier a verified registration reports:
// the one proved earliest, and among those verified at the same instant the one
// whose identifier sorts first. No such identifier is the empty string.
func earliestProvedEmail(conn *gorm.DB, account string) (string, error) {
	var rows []identifierRow
	err := conn.Where("account_id = ? AND kind = ?", account, string(authn.IdentifierEmail)).
		Find(&rows).Error
	if err != nil {
		return "", err
	}
	var earliest *identifierRow
	for i := range rows {
		r := &rows[i]
		if !(authn.Verification{Via: r.VerifiedVia}).Proves() {
			continue
		}
		switch {
		case earliest == nil:
			earliest = r
		case r.VerifiedAt.Before(earliest.VerifiedAt):
			earliest = r
		case r.VerifiedAt.Equal(earliest.VerifiedAt) && r.ID < earliest.ID:
			earliest = r
		}
	}
	if earliest == nil {
		return "", nil
	}
	return earliest.Value, nil
}

// PendingAccountByContact finds the one pending account a contact belongs to.
// kind is email or phone and value is normalized first; a caller never guesses
// an owner, so no owner and more than one owner are both ok false. The rows are
// read from authn_pending_contacts only, never from the identifier table.
func (p *product) PendingAccountByContact(ctx context.Context, kind string, value string) (accountID string, ok bool, err error) {
	identifierKind := authn.IdentifierKind(kind)
	if identifierKind != authn.IdentifierEmail && identifierKind != authn.IdentifierPhone {
		return "", false, fmt.Errorf("%w: a pending contact is an email address or a phone number", authn.ErrInvalidIdentifier)
	}
	normalized, err := NormalizeIdentifier(identifierKind, value)
	if err != nil {
		return "", false, err
	}
	var ids []string
	err = p.conn(ctx).Raw("SELECT a.id FROM "+tablePending+" c JOIN "+tableAccounts+" a ON a.id = c.account_id "+
		"WHERE c.kind = ? AND c.value = ? AND a.signup_state = ? AND a.status <> ?",
		string(identifierKind), normalized, signupPending, statusDeleted).Scan(&ids).Error
	if err != nil {
		return "", false, failed("reading a pending contact", err)
	}
	switch len(ids) {
	case 0:
		return "", false, nil
	case 1:
		return ids[0], true, nil
	}
	// The table holds at most one contact of a kind per account, so more than
	// one owner is more than one account, and an owner is not guessed.
	return "", false, nil
}
