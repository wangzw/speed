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
// was comes with it; no row awaits verification. A password is judged by the
// policy, derived under the capacity the module allows before the transaction
// opens, and written with the account; a hash is read and bounded and written
// as it is, whatever its strength. An identifier, a login name or an identifier
// of the account that breaks a uniqueness constraint is authn.ErrAccountExists,
// and two calls that create one email address at once leave one account.
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
	return toAccount(row, phc != "")
}

// creation is what Create has judged before it writes anything.
type creation struct {
	row         accountRow
	identifiers []identifierRow
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
		ID: id.String(), Kind: string(kind), Status: statusActive, DisplayName: in.DisplayName,
		Attributes: attrs, MFARequired: in.MFARequired, ValidAfter: now, CreatedAt: now, UpdatedAt: now,
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
		if err = add(authn.IdentifierUsername, in.Username, authn.VerifiedViaAdmin); err != nil {
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
// number again changes nothing but the proof it was written with.
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
		if _, err = p.lock(tx, id); err != nil {
			return err
		}
		now := p.at()
		if kind == authn.IdentifierPhone {
			var held []identifierRow
			if err = tx.Where("account_id = ? AND kind = ?", id.String(), string(kind)).Find(&held).Error; err != nil {
				return err
			}
			for _, old := range held {
				if old.Value == normalized {
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

// RemoveIdentifier unbinds one identifier by its identifier. One the account does
// not have is already unbound, and the call is nil.
func (p *product) RemoveIdentifier(ctx context.Context, id authn.AccountID, identifierID string) error {
	b := &batch{}
	err := p.conn(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := p.lock(tx, id); err != nil {
			return err
		}
		var row identifierRow
		err := tx.Where("id = ? AND account_id = ?", identifierID, id.String()).Take(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if err = tx.Where("id = ? AND account_id = ?", identifierID, id.String()).Delete(&identifierRow{}).Error; err != nil {
			return err
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
// are a password, a phone number and another binding: what the factor modules
// hold is not this module's to read, so an account whose only other way is a
// factor is refused too.
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
		left := len(bound) - 1
		if left == 0 {
			hasPassword, err := passwordSet(tx, id.String())
			if err != nil {
				return err
			}
			var phones int64
			err = tx.Model(&identifierRow{}).Where("account_id = ? AND kind = ?", id.String(), string(authn.IdentifierPhone)).
				Count(&phones).Error
			if err != nil {
				return err
			}
			if !hasPassword && phones == 0 {
				return fmt.Errorf("%w", authn.ErrLastLoginMethod)
			}
		}
		return tx.Where("id = ? AND account_id = ?", externalID, id.String()).Delete(&externalRow{}).Error
	})
	return failed("unbinding an external identity", err)
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
