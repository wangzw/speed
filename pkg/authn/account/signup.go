package account

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/model"
	"github.com/vislake/speed/pkg/authn/provisioning"
)

const (
	signupLocaleAttr   = "i18n.locale"
	signupTimeZoneAttr = "i18n.tz"
)

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
	name := displayNameOrDefault(in.DisplayName, username)
	if err = checkDisplayName(name); err != nil {
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

	// The back-off is read before the uniqueness check and before any derivation.
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
	ctx, after := provisioning.WithAfterCommit(ctx)
	err = p.conn(ctx).Transaction(func(tx *gorm.DB) error {
		now := p.at()
		var idErr error
		var id string
		if id, idErr = newID(); idErr != nil {
			return idErr
		}
		row = accountRow{
			ID: id, Kind: string(authn.KindUser), Status: statusActive, SignupState: signupPending,
			DisplayName: name, Attributes: attributes,
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
			VerifiedAt: now, VerifiedVia: authn.VerifiedViaSelf,
			SignIn: true, CreatedAt: now,
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
			// The contact row is the only arbiter of the address.
			if duplicate(idErr) {
				return fmt.Errorf("%w", authn.ErrContactTaken)
			}
			return idErr
		}
		// The provisioning seam runs in this same transaction, after the account, its
		// username identifier and its credential.
		if idErr = p.provisioning.ProvisionAccount(ctx, tx, accountFactsFor(row, username)); idErr != nil {
			return idErr
		}
		return p.emit(tx, b, accountCreated(authn.AccountID(id), authn.KindUser, bySignup), false)
	})
	switch {
	case duplicate(err):
		// The name was free when read and taken before the insert; the unique
		// constraint is the arbiter.
		p.fail(ctx, authn.ScopeSignupUsername, username, "")
		return authn.Account{}, fmt.Errorf("%w", authn.ErrAccountExists)
	case err != nil:
		return authn.Account{}, failed("registering an account", err)
	}
	p.publish(ctx, b)
	p.runAfterCommit(ctx, after)
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
		return account, err
	}
	return account, nil
}

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

func usernameTaken(conn *gorm.DB, username string) (bool, error) {
	var ids []string
	err := conn.Raw("SELECT account_id FROM "+tableIdentifiers+" WHERE kind = ? AND value = ?",
		string(authn.IdentifierUsername), username).Scan(&ids).Error
	if err != nil {
		return false, failed("reading a username", err)
	}
	return len(ids) > 0, nil
}

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
		return authn.SignupStatus{}, nil
	}
	return authn.SignupStatus{State: authn.SignupPending, Email: email, Since: timeOf(row.SignupVerifiedAt)}, nil
}

func emailIdentifiersOf(conn *gorm.DB, account string) ([]identifierRow, error) {
	var rows []identifierRow
	err := conn.Where("account_id = ? AND kind = ?", account, string(authn.IdentifierEmail)).Find(&rows).Error
	return rows, err
}

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

type unprovedEmail struct {
	value string
	at    time.Time
	id    string
}

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
	// The unique index holds at most one row per contact, so more than one owner
	// comes only from a row written around it.
	return "", false, nil
}
