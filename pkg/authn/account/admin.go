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
// A user account is created with a username: the three paths that restore an
// account rather than open one are exempt, an import that brings back an
// existing account and a service account that signs in with a service secret.
// A username is normalized and held to the reserved set like every other
// username write, except on the import path, whose name was given before
// this deployment's list existed. A missing or unusable one creates nothing.
// A username is always a login way.
//
// An email address and a phone number are written verified, and the way each
// was comes with it; no row awaits verification. Their two axes start false
// unless an import states its source record's. The exceptions are the phone an
// SMS first login signed in with, and a proved email address, which is the
// account's only one: both start as a login way, since the account has nothing
// else to compare against. A proved email address is the account's default
// contact in the same transaction, since a new account has none; an unproved
// one cannot be. An email address also reserves its contact in the same
// transaction: the reservation is written beside the identifier, so the contact
// is owned by this account from the moment the account exists. A password is
// judged by the policy, derived under the capacity the module allows before the
// transaction opens, and written with the account; a hash is read and bounded and
// written as it is, whatever its strength. An identifier, a login name or a
// binding of the account that breaks a uniqueness constraint is
// authn.ErrAccountExists; a contact reservation another account holds is
// authn.ErrContactTaken. Two calls that create one email address at once leave
// one account.
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

	// A user account is opened with a username, whichever path opens it. A
	// service account signs in with a service secret instead. An account that
	// names no username is refused here rather than left with no login name.
	if usernameRequired(kind) && in.Username == "" {
		// An absent username is a shape failure of the field, not a malformed
		// body: the answer names the username, the same as a badly formed one.
		return plan, authn.UsernameShape(fmt.Errorf("%w: a user account is created with a username", authn.ErrInvalidIdentifier))
	}

	add := func(k authn.IdentifierKind, value string, via string, flags authn.IdentifierFlags, defaultContact bool) error {
		var normalized, rowID string
		if normalized, err = NormalizeIdentifier(k, value); err != nil {
			return err
		}
		// A username is held to the reserved set whichever path writes it,
		// import included: an imported account is a user account with a login
		// name like any other.
		if k == authn.IdentifierUsername {
			if reservedErr := p.checkReservedUsername(normalized); reservedErr != nil {
				return reservedErr
			}
		}
		if rowID, err = newID(); err != nil {
			return err
		}
		plan.identifiers = append(plan.identifiers, identifierRow{
			ID: rowID, AccountID: id.String(), Kind: string(k), Value: normalized,
			VerifiedAt: now, VerifiedVia: via, SignIn: flags.SignIn, Contact: flags.Contact,
			DefaultContact: defaultContact, CreatedAt: now,
		})
		return nil
	}
	if in.Username != "" {
		// The way the username came about is the caller's to state, since a host
		// importing users and a first login land here too; the zero value means
		// an administrator wrote it. Create never writes a self-registered
		// account: Signups.Register is the only writer of signup_state. A
		// username is always a login way.
		usernameVia := in.UsernameVerifiedVia
		if usernameVia == (authn.Verification{}) {
			usernameVia = authn.Verification{Via: authn.VerifiedViaAdmin}
		}
		verified, err := viaOf(usernameVia)
		if err != nil {
			return plan, err
		}
		if err = add(authn.IdentifierUsername, in.Username, verified,
			authn.IdentifierFlags{SignIn: true}, false); err != nil {
			return plan, err
		}
	}
	if in.Email != "" {
		verified, err := viaOf(in.EmailVerifiedVia)
		if err != nil {
			return plan, err
		}
		// An email address is a contact from the moment it is written. Being a
		// sign-in way is a separate choice: an import keeps the source record's,
		// and the proved address an account opens with is chosen as a login way
		// since the holder has nothing to compare it against. The address a social
		// first login brings in has the same choice.
		proved := authn.Verification{Via: verified}.Proves()
		flags := authn.IdentifierFlags{Contact: true}
		if via == byImport {
			flags.SignIn = in.EmailFlags.SignIn
		} else if proved {
			flags.SignIn = true
		}
		// An unproved address is never the default contact. A proved one is the
		// default whether or not an import's source named it: an imported
		// account is new here and has no other.
		defaultContact := proved
		if via == byImport && in.DefaultContact && !proved {
			defaultContact = false
		}
		if err = add(authn.IdentifierEmail, in.Email, verified, flags, defaultContact); err != nil {
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
		// A phone number is a contact from the moment it is written. Being a
		// sign-in way is separate: an import keeps the source record's, and the
		// phone an SMS first login signs in with is a login way from the moment
		// the account exists.
		flags := authn.IdentifierFlags{Contact: true}
		if via == byImport {
			flags.SignIn = in.PhoneFlags.SignIn
		}
		if verified == authn.VerifiedViaSMSCode {
			flags.SignIn = true
		}
		if err = add(authn.IdentifierPhone, in.Phone, verified, flags, false); err != nil {
			return plan, err
		}
	}
	return plan, nil
}

// usernameRequired reports whether an account creation must name a username: a
// user account, whichever path opens it. A service account is exempt because a
// service secret, not a username, is how it signs in.
func usernameRequired(kind authn.AccountKind) bool {
	return kind == authn.KindUser
}

// newCredential reads or derives the credential of an account being created. It
// is the empty string when the account has none.
func (p *product) newCredential(ctx context.Context, in authn.CreateAccount, plan creation) (string, error) {
	if in.Password == "" && in.PasswordHash == "" {
		// A social first login opens its account with a password its person
		// chose; without one the account would be reachable through the provider
		// alone. The SMS first login, the only other signup path, is not asked
		// for one and is told apart by the phone it proved.
		if plan.via == bySignup && in.Phone == "" {
			return "", fmt.Errorf("%w: a social first login opens its account with a password", authn.ErrPasswordPolicy)
		}
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
// A registered email address or phone number is always a contact: a value the
// account owns is contact information from the moment it is written, whether or
// not its ownership is proved. A username is always a sign-in way and never a
// contact. A proved email address becomes the account's default contact when it
// has none, which is how the first proved address establishes the choice. It is
// also chosen as a login way when it is the account's only address: with one
// address there is nothing for the holder to compare it against. An account with
// several leaves that choice to its holder, and choosing it again changes
// nothing.
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
	if kind == authn.IdentifierUsername {
		if err = p.checkReservedUsername(normalized); err != nil {
			return err
		}
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
				alreadyProved := (authn.Verification{Via: held[0].VerifiedVia}).Proves()
				if alreadyProved && !via.Proves() {
					return nil
				}
				// The row is an address the account owns, so it is a contact from
				// here on even if a write made before the invariant left it false.
				if err = tx.Model(&identifierRow{}).Where("id = ?", held[0].ID).
					Updates(map[string]any{"verified_at": now, "verified_via": verified, "contact": true}).Error; err != nil {
					return err
				}
				if via.Proves() {
					// The address becomes a login way at its first proof alone,
					// and only while it is the account's only one; proving it
					// again is idempotent and changes neither axis.
					if !alreadyProved {
						if err = selectSoleEmailAsSignIn(tx, id.String(), held[0].ID); err != nil {
							return err
						}
					}
					if err = defaultContactIfNone(tx, id.String(), held[0].ID); err != nil {
						return err
					}
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
						Updates(map[string]any{"verified_at": now, "verified_via": verified, "contact": true}).Error
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
			VerifiedAt: now, VerifiedVia: verified,
			// A username is always a login way. An email address or phone number
			// is a contact from the moment it is registered.
			SignIn:    kind == authn.IdentifierUsername,
			Contact:   kind == authn.IdentifierEmail || kind == authn.IdentifierPhone,
			CreatedAt: now,
		}
		if err = tx.Create(&row).Error; err != nil {
			return err
		}
		if kind == authn.IdentifierEmail && via.Proves() {
			if err = selectSoleEmailAsSignIn(tx, id.String(), row.ID); err != nil {
				return err
			}
			if err = defaultContactIfNone(tx, id.String(), row.ID); err != nil {
				return err
			}
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

// defaultContactIfNone makes an email address the account's default contact when
// it has none, and leaves an existing choice alone. The writer that proves the
// first address establishes the default in the same transaction, so an account
// with a proved email always has exactly one.
func defaultContactIfNone(tx *gorm.DB, account, identifier string) error {
	var held int64
	if err := tx.Model(&identifierRow{}).
		Where("account_id = ? AND default_contact = ?", account, true).Count(&held).Error; err != nil {
		return err
	}
	if held > 0 {
		return nil
	}
	return tx.Model(&identifierRow{}).Where("id = ?", identifier).Update("default_contact", true).Error
}

// selectSoleEmailAsSignIn chooses the email address just proved as a sign-in way
// when it is the account's only address. With one address the holder has nothing
// to compare it against, and the address is the login name the recovery path for
// an address already held by an account asks for. An account with several
// addresses keeps the choice for its holder.
func selectSoleEmailAsSignIn(tx *gorm.DB, account, identifier string) error {
	var held int64
	if err := tx.Model(&identifierRow{}).
		Where("account_id = ? AND kind = ?", account, string(authn.IdentifierEmail)).
		Count(&held).Error; err != nil {
		return err
	}
	if held != 1 {
		return nil
	}
	return tx.Model(&identifierRow{}).Where("id = ?", identifier).Update("sign_in", true).Error
}

// transferDefaultContact hands the default contact to the earliest proved email
// address that remains, the identifier breaking a tie, which is what removing the
// default leaves. The row being removed is already gone, so an account with no
// proved address left simply has no default.
func transferDefaultContact(tx *gorm.DB, account string) error {
	var left []identifierRow
	if err := tx.Where("account_id = ? AND kind = ?", account, string(authn.IdentifierEmail)).
		Find(&left).Error; err != nil {
		return err
	}
	proved := make([]identifierRow, 0, len(left))
	for _, row := range left {
		if (authn.Verification{Via: row.VerifiedVia}).Proves() {
			proved = append(proved, row)
		}
	}
	if len(proved) == 0 {
		return nil
	}
	slices.SortFunc(proved, func(a, b identifierRow) int {
		if order := a.VerifiedAt.Compare(b.VerifiedAt); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	return tx.Model(&identifierRow{}).Where("id = ?", proved[0].ID).Update("default_contact", true).Error
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
// not have is already unbound, and the call is nil. A username is never unbound:
// it stays with the account for the account's whole life and the call is
// authn.ErrUsernameImmutable (REQ-account-070). Removing an email address
// also removes the account's reservation of that contact, releasing it to
// another account, and hands the default contact to the earliest proved email
// address that remains when the removed one was the default. Removing the last
// proved email address returns a verified self-registration to pending: the
// instant it first reached verified is kept, and no pending contact is made up in
// place of the address that was removed.
//
// An account that holds a password credential keeps at least one login name:
// removing its last chosen email address is
// authn.ErrIdentifierRequired, since the password would then authenticate
// nothing. That rule is about login names and is checked before the rule below.
// It only reaches a row that holds no username, which a write around this
// surface can leave.
//
// Removing any identifier that would leave the account with no way to log in
// that is not a factor is authn.ErrLastLoginMethod, and the transaction rolls
// back. The ways counted are a password credential, a proved phone number chosen
// as a sign-in way and an external identity (see loginWayHeld); an email address
// without a password authenticates nothing and does not count on its own, and
// neither does an unproved or unchosen phone number.
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
		kind := authn.IdentifierKind(row.Kind)
		// A username is part of the account's identity, not a login way the
		// holder may drop: it stays for the account's whole life. The refusal
		// comes before every other rule about what a removal would leave.
		if kind == authn.IdentifierUsername {
			return fmt.Errorf("%w: a username stays with its account", authn.ErrUsernameImmutable)
		}
		if err = tx.Where("id = ? AND account_id = ?", identifierID, id.String()).Delete(&identifierRow{}).Error; err != nil {
			return err
		}
		// An email address is released with the identifier: the contact is no
		// longer owned by this account, so another account may reserve it. When
		// it was the default contact, the earliest proved address that remains
		// takes over in the same transaction.
		if kind == authn.IdentifierEmail {
			if err = tx.Where("account_id = ? AND kind = ? AND value = ?", id.String(),
				string(authn.IdentifierEmail), row.Value).Delete(&pendingContactRow{}).Error; err != nil {
				return err
			}
			if row.DefaultContact {
				if err = transferDefaultContact(tx, id.String()); err != nil {
					return err
				}
			}
		}
		if kind == authn.IdentifierEmail {
			var hasPassword bool
			if hasPassword, err = hasCredential(tx, id.String()); err != nil {
				return err
			}
			if hasPassword {
				var left int
				if left, err = loginNameCount(tx, id.String()); err != nil {
					return err
				}
				if left == 0 {
					return fmt.Errorf("%w: removing the identifier would leave the account with no username or "+
						"chosen email address to log in with", authn.ErrIdentifierRequired)
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

// SetIdentifierFlags writes the two axes of one of the account's email addresses
// or phone numbers. Contact and SignIn are independent: SignIn may be set true
// only on a row whose ownership is proved, so an unproved row can be taken out of
// sign-in but not put into it. Contact is not a choice: a registered address is
// always a contact, so contact false is refused and contact true is accepted and
// changes nothing. A username has no two axes to set, and a row of another
// account is not this account's to write.
//
// Turning SignIn off is a change of how the account is proven, so it is checked
// against what would remain, in the order RemoveIdentifier uses: an account with
// a password credential that loses its last login name is
// authn.ErrIdentifierRequired, and one that loses every way to log in that is not
// a factor is authn.ErrLastLoginMethod. Either refusal rolls the write back.
func (p *product) SetIdentifierFlags(ctx context.Context, id authn.AccountID, identifierID string,
	flags authn.IdentifierFlags,
) error {
	err := p.conn(ctx).Transaction(func(tx *gorm.DB) error {
		if _, lockErr := p.lock(tx, id); lockErr != nil {
			return lockErr
		}
		var row identifierRow
		readErr := tx.Where("id = ? AND account_id = ?", identifierID, id.String()).Take(&row).Error
		switch {
		case errors.Is(readErr, gorm.ErrRecordNotFound):
			return invalid("the identifier is not one this account holds")
		case readErr != nil:
			return readErr
		}
		kind := authn.IdentifierKind(row.Kind)
		if kind != authn.IdentifierEmail && kind != authn.IdentifierPhone {
			return invalid("a username is always a sign-in way and has no two axes to set")
		}
		// A registered address is a contact, the default contact included, and
		// nothing takes that away.
		if !flags.Contact {
			return invalid("a registered address is always a contact")
		}
		proved := authn.Verification{Via: row.VerifiedVia}.Proves()
		// A row that is already a sign-in way keeps it, which is how a row the
		// earlier behaviour wrote and the migration backfilled is read; only
		// taking an unproved row into sign-in is refused.
		if flags.SignIn && !row.SignIn && !proved {
			return invalid("an identifier whose ownership is not proved cannot be a sign-in way")
		}
		if writeErr := tx.Model(&identifierRow{}).Where("id = ?", row.ID).
			Updates(map[string]any{"sign_in": flags.SignIn, "contact": flags.Contact}).Error; writeErr != nil {
			return writeErr
		}
		if !row.SignIn || flags.SignIn {
			return nil
		}
		if kind == authn.IdentifierEmail {
			hasPassword, credentialErr := hasCredential(tx, id.String())
			if credentialErr != nil {
				return credentialErr
			}
			if hasPassword {
				left, countErr := loginNameCount(tx, id.String())
				if countErr != nil {
					return countErr
				}
				if left == 0 {
					return fmt.Errorf("%w: no longer signing in with the identifier would leave the account with "+
						"no login name", authn.ErrIdentifierRequired)
				}
			}
		}
		hasWay, wayErr := loginWayHeld(tx, id.String())
		if wayErr != nil {
			return wayErr
		}
		if !hasWay {
			return fmt.Errorf("%w: no longer signing in with the identifier would leave the account with no way "+
				"to log in that is not a factor", authn.ErrLastLoginMethod)
		}
		return nil
	})
	return failed("setting the axes of an identifier", err)
}

// SetDefaultContact makes one proved email address of the account its default
// contact. The previous choice is cleared in the same transaction, so the partial
// unique index never sees two. The two axes are left as they are.
func (p *product) SetDefaultContact(ctx context.Context, id authn.AccountID, identifierID string) error {
	err := p.conn(ctx).Transaction(func(tx *gorm.DB) error {
		if _, lockErr := p.lock(tx, id); lockErr != nil {
			return lockErr
		}
		var row identifierRow
		readErr := tx.Where("id = ? AND account_id = ?", identifierID, id.String()).Take(&row).Error
		switch {
		case errors.Is(readErr, gorm.ErrRecordNotFound):
			return invalid("the identifier is not one this account holds")
		case readErr != nil:
			return readErr
		}
		if authn.IdentifierKind(row.Kind) != authn.IdentifierEmail ||
			!(authn.Verification{Via: row.VerifiedVia}).Proves() {
			return invalid("only a proved email address of the account is a default contact")
		}
		if clearErr := tx.Model(&identifierRow{}).
			Where("account_id = ? AND default_contact = ?", id.String(), true).
			Update("default_contact", false).Error; clearErr != nil {
			return clearErr
		}
		return tx.Model(&identifierRow{}).Where("id = ?", row.ID).Update("default_contact", true).Error
	})
	return failed("setting the default contact", err)
}

// loginNameCount is how many login names a password account holds: its usernames,
// which always sign in, and the email addresses its holder chose as sign-in ways.
func loginNameCount(tx *gorm.DB, accountID string) (int, error) {
	var rows []identifierRow
	if err := tx.Where("account_id = ?", accountID).Find(&rows).Error; err != nil {
		return 0, err
	}
	count := 0
	for _, row := range rows {
		if row.Kind == string(authn.IdentifierUsername) ||
			(row.Kind == string(authn.IdentifierEmail) && row.SignIn) {
			count++
		}
	}
	return count, nil
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
// proved and which its holder chose as a sign-in way, or an external identity. A
// passkey does not count, and neither does a username or an email address on its
// own, since without a password it authenticates nothing; an unproved or unchosen
// phone number does not count either. The factor modules hold what this module
// cannot read, so the answer is exactly the three ways loginWayHeld counts.
func (p *product) HasLoginWayOtherThanPasskey(ctx context.Context, id authn.AccountID) (bool, error) {
	hasWay, err := loginWayHeld(p.conn(ctx), id.String())
	if err != nil {
		return false, failed("reading the account's ways to log in", err)
	}
	return hasWay, nil
}

// loginWayHeld reports whether the account holds a way to log in that is not a
// factor: a password credential, a phone number whose ownership was proved and
// which its holder chose as a sign-in way, or an external identity. A phone
// number that is unproved (admin, import-unverified or self) or not chosen does
// not count; only a verification that Proves and a sign-in choice together do. A
// passkey or another factor lives in another module's tables and does not count
// here.
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
		return r.SignIn && authn.Verification{Via: r.VerifiedVia}.Proves()
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
