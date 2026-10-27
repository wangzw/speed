package account

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/model"
	"github.com/vislake/speed/pkg/authn/internal/password"
)

// The bounds of the free text and the keys a write takes.
const (
	displayNameMax = 100
	labelMax       = 100
	providerMax    = 128
	subjectMax     = 512
	sourceMax      = 128
	tenantMax      = 255
)

// secretPrefix starts every service secret, which is the prefix and 256 random
// bits in base64. A secret is recognizable by it, to a person who finds one and
// to a scanner that looks for them.
const secretPrefix = "spd_cs_"

// newID is a fresh UUIDv7 text, which any replica can mint without asking and
// which sorts close to the order of creation.
func newID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("%w: minting an identifier: %w", authn.ErrUnavailable, err)
	}
	return id.String(), nil
}

// invalid is an authn.ErrInvalidArgument that says which class of argument was
// refused and never the value.
func invalid(what string) error {
	return fmt.Errorf("%w: %s", authn.ErrInvalidArgument, what)
}

// checkDisplayName refuses a display name that is not text or that is longer
// than the column.
func checkDisplayName(name string) error {
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) > displayNameMax {
		return invalid("the display name is not valid text of at most 100 characters")
	}
	return nil
}

// plain reports whether s is valid text of at most max bytes with no control
// character.
func plain(s string, max int) bool {
	return len(s) <= max && utf8.ValidString(s) && strings.IndexFunc(s, unicode.IsControl) < 0
}

// viaOf is the way ownership of an identifier was established, checked against
// the closed set.
func viaOf(v authn.Verification) (string, error) {
	if !v.Valid() {
		return "", invalid("the way the identifier was verified is not one of sms-code, email-code, invite, " +
			"idp:<provider>, admin, import-unverified and self")
	}
	return v.Via, nil
}

// eventVia is the Via of a created account as the event payload carries it. An
// empty one is a host's own command.
func eventVia(via string) (string, error) {
	switch via {
	case "":
		return byAdmin, nil
	case "admin", "jit", "signup", "import":
		return via, nil
	}
	return "", invalid("the way the account came about is not one of admin, jit, signup and import")
}

// Create writes an enabled account.
//
// An email address and a phone number are written verified, and the way each
// was comes with it; no row awaits verification. An email address also reserves
// its contact in the same transaction: the reservation is written beside the
// identifier, so the contact is owned by this account from the moment the
// account exists. A password is judged by the policy, derived under the capacity
// the module allows before the transaction opens, and written with the account;
// a hash is read and bounded and written as it is, whatever its strength. An
// identifier, a login name or a binding of the account that breaks a uniqueness
// constraint is authn.ErrAccountExists; a contact reservation another account
// holds is authn.ErrContactTaken. Two calls that create one email address at
// once leave one account.
func (p *product) Create(ctx context.Context, in authn.CreateAccount) (authn.Account, error) {
	in = in.Clone()
	plan, err := p.planCreate(in)
	if err != nil {
		return authn.Account{}, err
	}
	phc, err := p.newCredential(ctx, in, plan)
	if err != nil {
		return authn.Account{}, err
	}

	b := &batch{}
	row := plan.row
	err = p.conn(ctx).Transaction(func(tx *gorm.DB) error {
		if err = tx.Create(&row).Error; err != nil {
			return err
		}
		for i := range plan.claims {
			if err = tx.Create(&plan.claims[i]).Error; err != nil {
				if duplicate(err) {
					return fmt.Errorf("%w", authn.ErrContactTaken)
				}
				return err
			}
		}
		for i := range plan.identifiers {
			if err = tx.Create(&plan.identifiers[i]).Error; err != nil {
				return err
			}
		}
		if phc != "" {
			if err = tx.Exec("INSERT INTO "+tableCredentials+" (account_id, phc, changed_at, must_change) VALUES (?, ?, ?, ?)",
				row.ID, phc, row.CreatedAt, in.MustChangePassword).Error; err != nil {
				return err
			}
		}
		return p.emit(tx, b, accountCreated(authn.AccountID(row.ID), authn.AccountKind(row.Kind), plan.via), false)
	})
	switch {
	case duplicate(err):
		return authn.Account{}, fmt.Errorf("%w", authn.ErrAccountExists)
	case err != nil:
		return authn.Account{}, failed("creating an account", err)
	}
	p.publish(ctx, b)
	account, err := toAccount(row, phc != "")
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

// creation is what Create has judged before it writes anything.
type creation struct {
	row         accountRow
	identifiers []identifierRow
	claims      []pendingContactRow
	via         string
}

// planCreate checks every argument of Create and builds the rows.
func (p *product) planCreate(in authn.CreateAccount) (creation, error) {
	var plan creation
	kind := in.Kind
	if kind == "" {
		kind = authn.KindUser
	}
	if !kind.Valid() {
		return plan, invalid("the kind of account is not user or service")
	}
	via, err := eventVia(in.Via)
	if err != nil {
		return plan, err
	}
	plan.via = via
	if err = checkDisplayName(in.DisplayName); err != nil {
		return plan, err
	}
	if err = model.CheckAccountAttributes(in.Attributes); err != nil {
		return plan, err
	}
	attrs, err := encodeAttributes(in.Attributes)
	if err != nil {
		return plan, invalid("the attributes cannot be written")
	}
	if in.Password != "" && in.PasswordHash != "" {
		return plan, invalid("a password and a password hash are alternatives")
	}

	id := in.ID
	if id != "" {
		if err = checkID(id); err != nil {
			return plan, err
		}
		if id.IsStatic() {
			return plan, fmt.Errorf("%w: an account of the library does not begin with the static prefix", authn.ErrInvalidAccountID)
		}
	} else {
		var text string
		if text, err = newID(); err != nil {
			return plan, err
		}
		id = authn.AccountID(text)
	}

	now := p.at()
	plan.row = accountRow{
		ID: id.String(), Kind: string(kind), Status: statusActive, SignupState: signupNone,
		DisplayName: in.DisplayName, Attributes: attrs, MFARequired: in.MFARequired,
		ValidAfter: now, CreatedAt: now, UpdatedAt: now,
	}

	add := func(k authn.IdentifierKind, value string, via string) error {
		var normalized, rowID string
		if normalized, err = NormalizeIdentifier(k, value); err != nil {
			return err
		}
		if rowID, err = newID(); err != nil {
			return err
		}
		plan.identifiers = append(plan.identifiers, identifierRow{
			ID: rowID, AccountID: id.String(), Kind: string(k), Value: normalized,
			VerifiedAt: now, VerifiedVia: via, CreatedAt: now,
		})
		return nil
	}
	if in.Username != "" {
		// The way the username came about is the caller's to state, since a host
		// importing users and a first login land here too; the zero value means
		// an administrator wrote it. Create never writes a self-registered
		// account: Signups.Register is the only writer of signup_state.
		usernameVia := in.UsernameVerifiedVia
		if usernameVia == (authn.Verification{}) {
			usernameVia = authn.Verification{Via: authn.VerifiedViaAdmin}
		}
		verified, err := viaOf(usernameVia)
		if err != nil {
			return plan, err
		}
		if err = add(authn.IdentifierUsername, in.Username, verified); err != nil {
			return plan, err
		}
	}
	if in.Email != "" {
		verified, err := viaOf(in.EmailVerifiedVia)
		if err != nil {
			return plan, err
		}
		if err = add(authn.IdentifierEmail, in.Email, verified); err != nil {
			return plan, err
		}
		email, err := NormalizeIdentifier(authn.IdentifierEmail, in.Email)
		if err != nil {
			return plan, err
		}
		claimID, err := newID()
		if err != nil {
			return plan, err
		}
		plan.claims = append(plan.claims, pendingContactRow{
			ID: claimID, AccountID: id.String(), Kind: string(authn.IdentifierEmail), Value: email,
			VerifiedAt: &now, CreatedAt: now, UpdatedAt: now,
		})
	}
	if in.Phone != "" {
		verified, err := viaOf(in.PhoneVerifiedVia)
		if err != nil {
			return plan, err
		}
		if err = add(authn.IdentifierPhone, in.Phone, verified); err != nil {
			return plan, err
		}
	}
	return plan, nil
}

// newCredential reads or derives the credential of an account being created. It
// is the empty string when the account has none.
func (p *product) newCredential(ctx context.Context, in authn.CreateAccount, plan creation) (string, error) {
	if in.Password == "" && in.PasswordHash == "" {
		return "", nil
	}
	if plan.row.Kind != string(authn.KindUser) {
		return "", invalid("only a user account has a password")
	}
	var login bool
	identifiers := make([]string, 0, len(plan.identifiers))
	for _, r := range plan.identifiers {
		identifiers = append(identifiers, r.Value)
		if r.Kind == string(authn.IdentifierUsername) || r.Kind == string(authn.IdentifierEmail) {
			login = true
		}
	}
	if !login {
		return "", fmt.Errorf("%w: the account has no username or email address to log in with", authn.ErrIdentifierRequired)
	}
	if in.PasswordHash != "" {
		return p.importable(in.PasswordHash)
	}
	normalized, err := normalizeNew(in.Password)
	if err != nil {
		return "", err
	}
	if err = p.policy(normalized, identifiers); err != nil {
		return "", err
	}
	phc, err := p.hashPassword(ctx, normalized)
	if err != nil {
		return "", derivationFailed(err)
	}
	return phc, nil
}

// importable reads an entry handed over for import and returns it in the PHC form.
// The strength is not judged: what an import brings in was written elsewhere and
// is as strong as it is. A form that cannot be read, or that is out of the
// bounds, is refused without any of its text, and is reported at Error.
func (p *product) importable(encoded string) (string, error) {
	entry, err := password.Parse(encoded)
	if err == nil {
		var phc string
		if phc, err = entry.PHC(); err == nil {
			return phc, nil
		}
	}
	p.logger.Error(ModuleName + ": a password entry handed over for import is not one this module reads")
	return "", unreadable(err)
}

// SetDisplayName sets the display name, at most 100 characters.
func (p *product) SetDisplayName(ctx context.Context, id authn.AccountID, name string) error {
	if err := checkDisplayName(name); err != nil {
		return err
	}
	return p.updateAccount(ctx, id, map[string]any{"display_name": name})
}

// SetAttributes replaces the attribute set, which the bounds of the model hold.
func (p *product) SetAttributes(ctx context.Context, id authn.AccountID, attrs map[string]string) error {
	if err := model.CheckAccountAttributes(attrs); err != nil {
		return err
	}
	text, err := encodeAttributes(attrs)
	if err != nil {
		return invalid("the attributes cannot be written")
	}
	return p.updateAccount(ctx, id, map[string]any{"attributes": text})
}

// SetProtected sets whether the account may be impersonated.
func (p *product) SetProtected(ctx context.Context, id authn.AccountID, protected bool) error {
	return p.updateAccount(ctx, id, map[string]any{"protected": protected})
}

// SetMFARequired sets whether the account must register a factor.
func (p *product) SetMFARequired(ctx context.Context, id authn.AccountID, required bool) error {
	return p.updateAccount(ctx, id, map[string]any{"mfa_required": required})
}

// updateAccount writes columns of an account that is not a tombstone.
func (p *product) updateAccount(ctx context.Context, id authn.AccountID, columns map[string]any) error {
	if err := checkID(id); err != nil {
		return err
	}
	columns["updated_at"] = p.at()
	res := p.conn(ctx).Model(&accountRow{}).Where("id = ? AND status <> ?", id.String(), statusDeleted).Updates(columns)
	if res.Error != nil {
		return failed("updating an account", res.Error)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("%w", authn.ErrAccountNotFound)
	}
	return nil
}

// AddIdentifier binds an identifier to an account, normalized first. A phone
// number replaces the one the account has, since an account has one; the same
// number again rewrites its way only when that does not take a proof away.
//
// An email address the account already holds as an identifier is rewritten in
// place instead of being added again: no identifier-changed event is published
// and the call is nil. A way that does not prove ownership applies only to a row
// that is new or not yet proved, so an already-proved row keeps its proof. An
// address another account holds is authn.ErrContactTaken, which the contact
// reservation's unique index arbitrates.
func (p *product) AddIdentifier(ctx context.Context, id authn.AccountID, kind authn.IdentifierKind, value string,
	via authn.Verification,
) error {
	verified, err := viaOf(via)
	if err != nil {
		return err
	}
	normalized, err := NormalizeIdentifier(kind, value)
	if err != nil {
		return err
	}
	rowID, err := newID()
	if err != nil {
		return err
	}
	b := &batch{}
	err = p.conn(ctx).Transaction(func(tx *gorm.DB) error {
		account, lockErr := p.lock(tx, id)
		if lockErr != nil {
			return lockErr
		}
		now := p.at()
		// An email address is a contact an account owns, so the same transaction
		// records the reservation. A row of this account for the value is brought
		// to proved now; one of another account answers ErrContactTaken, and the
		// unique index on the contact is the arbiter of two writers. A phone
		// number has no reservation: proving one consumes the pending target a
		// registration or a flow wrote for it.
		if kind == authn.IdentifierEmail {
			if err = writeEmailClaim(tx, id.String(), normalized, now); err != nil {
				return err
			}
			// The address may already be an identifier of this account, written
			// by an administrator, an import or the account itself. A proof
			// brings that row up to proved and keeps it single; an unproved way
			// never takes a proof away, and only rewrites a row without one.
			var held []identifierRow
			if err = tx.Where("account_id = ? AND kind = ? AND value = ?", id.String(), string(kind), normalized).
				Find(&held).Error; err != nil {
				return err
			}
			if len(held) > 0 {
				if (authn.Verification{Via: held[0].VerifiedVia}).Proves() && !via.Proves() {
					return nil
				}
				if err = tx.Model(&identifierRow{}).Where("id = ?", held[0].ID).
					Updates(map[string]any{"verified_at": now, "verified_via": verified}).Error; err != nil {
					return err
				}
				if via.Proves() {
					return noteEmailProved(tx, account, now)
				}
				return nil
			}
		} else if kind == authn.IdentifierPhone && via.Proves() {
			if err = tx.Where("account_id = ? AND kind = ? AND value = ?", id.String(), string(kind), normalized).
				Delete(&pendingContactRow{}).Error; err != nil {
				return err
			}
		}
		if kind == authn.IdentifierPhone {
			var held []identifierRow
			if err = tx.Where("account_id = ? AND kind = ?", id.String(), string(kind)).Find(&held).Error; err != nil {
				return err
			}
			for _, old := range held {
				if old.Value == normalized {
					// A rewrite keeps a proof the row already has: an unproved way
					// applies only to a row without one.
					if (authn.Verification{Via: old.VerifiedVia}).Proves() && !via.Proves() {
						return nil
					}
					return tx.Model(&identifierRow{}).Where("id = ?", old.ID).
						Updates(map[string]any{"verified_at": now, "verified_via": verified}).Error
				}
			}
			if len(held) > 0 {
				if err = tx.Where("account_id = ? AND kind = ?", id.String(), string(kind)).Delete(&identifierRow{}).Error; err != nil {
					return err
				}
				if err = p.emit(tx, b, identifierChanged(id, kind, changeRemoved), false); err != nil {
					return err
				}
			}
		}
		row := identifierRow{
			ID: rowID, AccountID: id.String(), Kind: string(kind), Value: normalized,
			VerifiedAt: now, VerifiedVia: verified, CreatedAt: now,
		}
		if err = tx.Create(&row).Error; err != nil {
			return err
		}
		if kind == authn.IdentifierEmail && via.Proves() {
			if err = noteEmailProved(tx, account, now); err != nil {
				return err
			}
		}
		return p.emit(tx, b, identifierChanged(id, kind, changeAdded), false)
	})
	switch {
	case duplicate(err):
		return fmt.Errorf("%w", authn.ErrAccountExists)
	case err != nil:
		return failed("binding an identifier", err)
	}
	p.publish(ctx, b)
	return nil
}

// noteEmailProved records that the account now holds a proved email address: a
// pending row is moved to verified, and the first instant the account reached
// verified is written once and kept afterwards. An account that was never a
// self-registration keeps its state and gains the instant all the same, which is
// what Since reads.
func noteEmailProved(tx *gorm.DB, account accountRow, now time.Time) error {
	update := map[string]any{}
	if signupStateOf(account.SignupState) == authn.SignupPending {
		update["signup_state"] = signupVerified
	}
	if account.SignupVerifiedAt == nil {
		update["signup_verified_at"] = now
	}
	if len(update) == 0 {
		return nil
	}
	update["updated_at"] = now
	return tx.Model(&accountRow{}).Where("id = ?", account.ID).Updates(update).Error
}

// writeEmailClaim records that an account owns an email address as a contact.
// A row of the same account and value is brought to proved now. Any other row
// for the value belongs to another account and is authn.ErrContactTaken; the
// insert of a missing row is the atomic arbiter, so two writers cannot both
// reserve one address.
func writeEmailClaim(tx *gorm.DB, account, value string, now time.Time) error {
	kind := string(authn.IdentifierEmail)
	var held []pendingContactRow
	if err := tx.Where("account_id = ? AND kind = ? AND value = ?", account, kind, value).Find(&held).Error; err != nil {
		return err
	}
	if len(held) > 0 {
		return tx.Model(&pendingContactRow{}).Where("id = ?", held[0].ID).
			Updates(map[string]any{"verified_at": now, "updated_at": now}).Error
	}
	id, err := newID()
	if err != nil {
		return err
	}
	claim := pendingContactRow{
		ID: id, AccountID: account, Kind: kind, Value: value,
		VerifiedAt: &now, CreatedAt: now, UpdatedAt: now,
	}
	if err = tx.Create(&claim).Error; err != nil {
		if duplicate(err) {
			return fmt.Errorf("%w", authn.ErrContactTaken)
		}
		return err
	}
	return nil
}

// RemoveIdentifier unbinds one identifier by its identifier. One the account does
// not have is already unbound, and the call is nil. Removing an email address
// also removes the account's reservation of that contact, releasing it to
// another account. Removing the last proved email address returns a verified
// self-registration to pending: the instant it first reached verified is kept,
// and no pending contact is made up in place of the address that was removed.
//
// An account that holds a password credential keeps at least one login name:
// removing its last username or email address is authn.ErrIdentifierRequired,
// since the password would then authenticate nothing. That rule is about login
// names and is checked first, before the rule below, so it is unchanged.
//
// Removing any identifier that would leave the account with no way to log in
// that is not a factor is authn.ErrLastLoginMethod, and the transaction rolls
// back. The ways counted are a password credential, a phone number whose
// ownership was proved and an external identity (see loginWayHeld); a username
// or an email address without a password authenticates nothing and does not
// count on its own, and neither does an unproved phone number.
func (p *product) RemoveIdentifier(ctx context.Context, id authn.AccountID, identifierID string) error {
	b := &batch{}
	err := p.conn(ctx).Transaction(func(tx *gorm.DB) error {
		account, err := p.lock(tx, id)
		if err != nil {
			return err
		}
		var row identifierRow
		err = tx.Where("id = ? AND account_id = ?", identifierID, id.String()).Take(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if err = tx.Where("id = ? AND account_id = ?", identifierID, id.String()).Delete(&identifierRow{}).Error; err != nil {
			return err
		}
		kind := authn.IdentifierKind(row.Kind)
		// An email address is released with the identifier: the contact is no
		// longer owned by this account, so another account may reserve it.
		if kind == authn.IdentifierEmail {
			if err = tx.Where("account_id = ? AND kind = ? AND value = ?", id.String(),
				string(authn.IdentifierEmail), row.Value).Delete(&pendingContactRow{}).Error; err != nil {
				return err
			}
		}
		if kind == authn.IdentifierUsername || kind == authn.IdentifierEmail {
			var hasPassword bool
			if hasPassword, err = hasCredential(tx, id.String()); err != nil {
				return err
			}
			if hasPassword {
				var left []identifierRow
				if err = tx.Where("account_id = ? AND kind IN ?", id.String(),
					[]string{string(authn.IdentifierUsername), string(authn.IdentifierEmail)}).
					Find(&left).Error; err != nil {
					return err
				}
				if len(left) == 0 {
					return fmt.Errorf("%w: removing the identifier would leave the account with no username or "+
						"email address to log in with", authn.ErrIdentifierRequired)
				}
			}
		}
		// The login-name rule above is checked first and keeps its answer. This
		// one covers every identifier: the account must keep a way to log in
		// that is not a factor.
		var hasWay bool
		if hasWay, err = loginWayHeld(tx, id.String()); err != nil {
			return err
		}
		if !hasWay {
			return fmt.Errorf("%w: removing the identifier would leave the account with no way to log in "+
				"that is not a factor", authn.ErrLastLoginMethod)
		}
		if kind == authn.IdentifierEmail &&
			(authn.Verification{Via: row.VerifiedVia}).Proves() &&
			signupStateOf(account.SignupState) == authn.SignupVerified {
			var left []identifierRow
			if err = tx.Where("account_id = ? AND kind = ?", id.String(), string(authn.IdentifierEmail)).
				Find(&left).Error; err != nil {
				return err
			}
			proved := slices.ContainsFunc(left, func(r identifierRow) bool {
				return authn.Verification{Via: r.VerifiedVia}.Proves()
			})
			if !proved {
				if err = tx.Model(&accountRow{}).Where("id = ?", id.String()).
					Updates(map[string]any{"signup_state": signupPending, "updated_at": p.at()}).Error; err != nil {
					return err
				}
			}
		}
		return p.emit(tx, b, identifierChanged(id, authn.IdentifierKind(row.Kind), changeRemoved), false)
	})
	if err != nil {
		return failed("unbinding an identifier", err)
	}
	p.publish(ctx, b)
	return nil
}

// checkLink refuses an external identity whose key is not text that is bounded.
func checkLink(l authn.ExternalLink) error {
	if l.Provider == "" || l.Subject == "" || !plain(l.Provider, providerMax) || !plain(l.Subject, subjectMax) {
		return invalid("the provider and the subject of an external identity are text of bounded length")
	}
	if !plain(l.Email, emailMaxBytes) {
		return invalid("the email address of an external identity is text of at most 254 bytes")
	}
	return nil
}

// LinkExternal binds an external identity to an account. One that another
// account holds is authn.ErrBindingConflict, and one the account holds already is
// the same binding, whose display fields it brings up to date. Two accounts that
// bind one provider and subject at once leave one binding, and the other call is
// the conflict.
func (p *product) LinkExternal(ctx context.Context, id authn.AccountID, l authn.ExternalLink) error {
	if err := checkLink(l); err != nil {
		return err
	}
	rowID, err := newID()
	if err != nil {
		return err
	}
	err = p.conn(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err = p.lock(tx, id); err != nil {
			return err
		}
		row := externalRow{
			ID: rowID, AccountID: id.String(), Provider: l.Provider, Subject: l.Subject,
			Email: l.Email, EmailVerified: l.EmailVerified, CreatedAt: p.at(),
		}
		return tx.Create(&row).Error
	})
	if !duplicate(err) {
		return failed("binding an external identity", err)
	}
	// The key is taken, by this account or by another, and which of the two is
	// what the row says.
	var holder []externalRow
	if err = p.conn(ctx).Where("provider = ? AND subject = ?", l.Provider, l.Subject).Find(&holder).Error; err != nil {
		return failed("reading an external identity", err)
	}
	if len(holder) == 0 || holder[0].AccountID != id.String() {
		return fmt.Errorf("%w", authn.ErrBindingConflict)
	}
	err = p.conn(ctx).Model(&externalRow{}).Where("id = ?", holder[0].ID).
		Updates(map[string]any{"email": l.Email, "email_verified": l.EmailVerified}).Error
	return failed("updating an external identity", err)
}

// UnlinkExternal removes a binding. When it is the last way the account has to log
// in, the call is authn.ErrLastLoginMethod and nothing changes. The ways counted
// are a password, a phone number whose ownership was proved and another
// binding: what the factor modules hold is not this module's to read, so an
// account whose only other way is a factor is refused too.
func (p *product) UnlinkExternal(ctx context.Context, id authn.AccountID, externalID string) error {
	err := p.conn(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := p.lock(tx, id); err != nil {
			return err
		}
		var bound []externalRow
		if err := tx.Where("account_id = ?", id.String()).Find(&bound).Error; err != nil {
			return err
		}
		if !slices.ContainsFunc(bound, func(r externalRow) bool { return r.ID == externalID }) {
			return nil
		}
		if err := tx.Where("id = ? AND account_id = ?", externalID, id.String()).Delete(&externalRow{}).Error; err != nil {
			return err
		}
		hasWay, err := loginWayHeld(tx, id.String())
		if err != nil {
			return err
		}
		if !hasWay {
			return fmt.Errorf("%w", authn.ErrLastLoginMethod)
		}
		return nil
	})
	return failed("unbinding an external identity", err)
}

// HasLoginWayOtherThanPasskey reports whether the account can log in by a way
// other than a factor: a password credential, a phone number whose ownership was
// proved or an external identity. A passkey does not count, and neither does a
// username or an email address on its own, since without a password it
// authenticates nothing; an unproved phone number does not count either. The
// factor modules hold what this module cannot read, so the answer is exactly
// the three ways loginWayHeld counts.
func (p *product) HasLoginWayOtherThanPasskey(ctx context.Context, id authn.AccountID) (bool, error) {
	hasWay, err := loginWayHeld(p.conn(ctx), id.String())
	if err != nil {
		return false, failed("reading the account's ways to log in", err)
	}
	return hasWay, nil
}

// loginWayHeld reports whether the account holds a way to log in that is not a
// factor: a password credential, a phone number whose ownership was proved, or
// an external identity. An unproved phone number (admin, import-unverified or
// self) does not count; only a verification that Proves does. A passkey or
// another factor lives in another module's tables and does not count here.
func loginWayHeld(tx *gorm.DB, accountID string) (bool, error) {
	hasPassword, err := hasCredential(tx, accountID)
	if err != nil || hasPassword {
		return hasPassword, err
	}
	var phones []identifierRow
	if err = tx.Where("account_id = ? AND kind = ?", accountID,
		string(authn.IdentifierPhone)).Find(&phones).Error; err != nil {
		return false, err
	}
	if slices.ContainsFunc(phones, func(r identifierRow) bool {
		return authn.Verification{Via: r.VerifiedVia}.Proves()
	}) {
		return true, nil
	}
	var externals int64
	if err = tx.Model(&externalRow{}).Where("account_id = ?", accountID).Count(&externals).Error; err != nil {
		return false, err
	}
	return externals > 0, nil
}

// CreateServiceSecret makes a secret for a service account: 256 random bits
// under the prefix spd_cs_, of which only the SHA-256 is kept. The secret is in
// the return value once.
func (p *product) CreateServiceSecret(ctx context.Context, id authn.AccountID, label string, expires *time.Time) (string, string, error) {
	if !plain(label, labelMax) {
		return "", "", invalid("the label of a service secret is text of at most 100 bytes")
	}
	now := p.at()
	var expiry *time.Time
	if expires != nil {
		if utc(*expires).IsZero() || !utc(*expires).After(now) {
			return "", "", invalid("the expiry of a service secret is later than now")
		}
		expiry = timePtr(*expires)
	}
	secretID, err := newID()
	if err != nil {
		return "", "", err
	}
	var raw [32]byte
	if _, err = rand.Read(raw[:]); err != nil {
		return "", "", fmt.Errorf("%w: drawing a secret: %w", authn.ErrUnavailable, err)
	}
	secret := secretPrefix + base64.RawURLEncoding.EncodeToString(raw[:])

	err = p.conn(ctx).Transaction(func(tx *gorm.DB) error {
		var row accountRow
		if row, err = p.lock(tx, id); err != nil {
			return err
		}
		if row.Kind != string(authn.KindService) {
			return invalid("only a service account has service secrets")
		}
		return tx.Create(&secretRow{
			ID: secretID, AccountID: id.String(), SecretHash: secretDigest(secret), Label: label,
			CreatedAt: now, ExpiresAt: expiry,
		}).Error
	})
	if err != nil {
		return "", "", failed("creating a service secret", err)
	}
	return secretID, secret, nil
}

// secretDigest is what a service secret is found by: the SHA-256 of the secret as
// it is presented, in hex.
func secretDigest(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// Touch records the time of the account's last login. It is a raw write that
// leaves no audit row.
func (p *product) Touch(ctx context.Context, id authn.AccountID, at time.Time) error {
	if err := checkID(id); err != nil {
		return err
	}
	if at.IsZero() {
		at = p.now.Now()
	}
	res := p.conn(ctx).Exec("UPDATE "+tableAccounts+" SET last_login_at = ? WHERE id = ? AND status <> ?",
		utc(at), id.String(), statusDeleted)
	if res.Error != nil {
		return failed("recording a login", res.Error)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("%w", authn.ErrAccountNotFound)
	}
	return nil
}
