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
// registration that loses the username's unique constraint counts one as well.
// The email address is not checked before the write: the reservation row's unique
// constraint is the one atomic arbiter, and a registration that loses it is
// authn.ErrContactTaken, which says the address is already reserved rather than
// that the username exists. An unproved reservation is accepted: the first
// registration to claim an address owns it until it is proved or its account is
// deleted. A derivation that finds its capacity full and a transaction that fails
// for any other reason count nothing, since neither says anything about the name.
// The derivation runs under the self-registration width and no memory budget of
// its own; a full width is authn.ErrBusy.
//
// One transaction writes the enabled account with signup_state 1, a username
// identifier written through self, a password credential and one email
// reservation awaiting verification. No identifier awaits verification, no email
// is sent and no session is made.
func (p *product) Register(ctx context.Context, in authn.RegisterAccount) (authn.Account, error) {
	if p.passwordSignup() != signupOpen {
		return authn.Account{}, fmt.Errorf("%w", authn.ErrSignupClosed)
	}
	username, err := NormalizeIdentifier(authn.IdentifierUsername, in.Username)
	if err != nil {
		return authn.Account{}, err
	}
	if err = p.checkReservedUsername(username); err != nil {
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
			// The contact row is the only arbiter of the address. A duplicate
			// says another account reserved it, not that the username exists,
			// and the transaction rolls the account, identifier and credential
			// back with it.
			if duplicate(idErr) {
				return fmt.Errorf("%w", authn.ErrContactTaken)
			}
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
	account, err := toAccount(row, true)
	if err != nil {
		return authn.Account{}, err
	}
	if err := p.callHooks(ctx, account.ID, nil, func(hookCtx context.Context, h authn.AccountLifecycle) error {
		return h.AccountCreated(hookCtx, account.ID)
	}); err != nil {
		// The account is in being; only the hook that builds what shares its life
		// is unfinished, and the same call repeated finishes it.
		return account, err
	}
	return account, nil
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

// SignupStateOf reads how far an account's email verification has got. The
// state is judged from the identifiers and the contact reservations on every
// call; the signup_state column is not read. An account of a kind other than
// user has nothing to verify and reads SignupNone.
//
// Verified is a user account that holds at least one email identifier whose way
// of verification proves ownership. Its address is the one proved earliest,
// ties broken by identifier, and Since is signup_verified_at, or that
// identifier's own instant when the column is empty.
//
// Pending is a user account with no proved email address but with something to
// prove: an email identifier written through a channel that does not prove
// ownership (admin, import-unverified, self), or a contact reservation its
// registration left awaiting verification. Its address is the earliest of those
// two by creation instant, ties broken by identifier, and Since is
// signup_verified_at, which is the zero time for an account that never reached
// Verified.
//
// SignupNone is a user account with no email address at all, proved or
// unproved. There is nothing to verify, so nothing gates on it; Since is the
// zero time.
func (p *product) SignupStateOf(ctx context.Context, id authn.AccountID) (authn.SignupStatus, error) {
	conn := p.conn(ctx)
	row, err := loadAccount(conn, id)
	if err != nil {
		return authn.SignupStatus{}, failed("reading an account", err)
	}
	if authn.AccountKind(row.Kind) != authn.KindUser {
		return authn.SignupStatus{}, nil
	}
	emails, err := emailIdentifiersOf(conn, id.String())
	if err != nil {
		return authn.SignupStatus{}, failed("reading an account's identifiers", err)
	}
	if verified := earliestProvedEmail(emails); verified != nil {
		since := timeOf(row.SignupVerifiedAt)
		if since.IsZero() {
			since = verified.VerifiedAt
		}
		return authn.SignupStatus{State: authn.SignupVerified, Email: verified.Value, Since: since}, nil
	}
	email, found, err := earliestUnprovedEmail(conn, id.String(), emails)
	if err != nil {
		return authn.SignupStatus{}, failed("reading a pending contact", err)
	}
	if !found {
		// No email address at all is nothing to verify: the account is not held
		// to a proof it has no target for.
		return authn.SignupStatus{}, nil
	}
	return authn.SignupStatus{State: authn.SignupPending, Email: email, Since: timeOf(row.SignupVerifiedAt)}, nil
}

// emailIdentifiersOf reads every email identifier one account holds.
func emailIdentifiersOf(conn *gorm.DB, account string) ([]identifierRow, error) {
	var rows []identifierRow
	err := conn.Where("account_id = ? AND kind = ?", account, string(authn.IdentifierEmail)).Find(&rows).Error
	return rows, err
}

// earliestProvedEmail is the email identifier a verified account reports: the
// one proved earliest, and among those verified at the same instant the one
// whose identifier sorts first. No proved identifier is nil.
func earliestProvedEmail(rows []identifierRow) *identifierRow {
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
	return earliest
}

// unprovedEmail is one address a pending account may report, with the ordering
// key that decides which one it is: the instant it was written and its
// identifier.
type unprovedEmail struct {
	value string
	at    time.Time
	id    string
}

// earliestUnprovedEmail is the address a pending account reports: the earliest
// of its unproved email identifiers and its reservations still awaiting
// verification, by creation instant, ties broken by identifier. found is false
// when the account has no email address at all.
func earliestUnprovedEmail(conn *gorm.DB, account string, emails []identifierRow) (string, bool, error) {
	candidates := make([]unprovedEmail, 0, len(emails))
	for _, row := range emails {
		if (authn.Verification{Via: row.VerifiedVia}).Proves() {
			continue
		}
		candidates = append(candidates, unprovedEmail{value: row.Value, at: timeOf(&row.CreatedAt), id: row.ID})
	}
	var contacts []pendingContactRow
	if err := conn.Where("account_id = ? AND kind = ? AND verified_at IS NULL", account,
		string(authn.IdentifierEmail)).Find(&contacts).Error; err != nil {
		return "", false, err
	}
	for _, row := range contacts {
		candidates = append(candidates, unprovedEmail{value: row.Value, at: timeOf(&row.CreatedAt), id: row.ID})
	}
	var earliest *unprovedEmail
	for i := range candidates {
		c := &candidates[i]
		switch {
		case earliest == nil:
			earliest = c
		case c.at.Before(earliest.at):
			earliest = c
		case c.at.Equal(earliest.at) && c.id < earliest.id:
			earliest = c
		}
	}
	if earliest == nil {
		return "", false, nil
	}
	return earliest.value, true, nil
}

// PendingAccountByContact finds the one pending account a contact belongs to.
// kind is email or phone and value is normalized first; a caller never guesses
// an owner, so no owner and more than one owner are both ok false. The rows are
// read from authn_pending_contacts only, never from the identifier table. Only
// an unproved reservation belongs to a pending account: a row whose verified_at
// is set is a contact an account already holds, and that account is answered by
// the identifier table, not here. The account's stored signup state is not
// read, because an unproved reservation is what makes the contact pending.
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
		"WHERE c.kind = ? AND c.value = ? AND c.verified_at IS NULL AND a.status <> ?",
		string(identifierKind), normalized, statusDeleted).Scan(&ids).Error
	if err != nil {
		return "", false, failed("reading a pending contact", err)
	}
	switch len(ids) {
	case 0:
		return "", false, nil
	case 1:
		return ids[0], true, nil
	}
	// The table's unique index holds at most one row per contact, so more than
	// one owner can only come from a row written around it, and an owner is not
	// guessed.
	return "", false, nil
}
