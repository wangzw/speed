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
	"github.com/vislake/speed/pkg/authn/provisioning"
)

const (
	displayNameMax = 100
	labelMax       = 100
	providerMax    = 128
	subjectMax     = 512
	sourceMax      = 128
	tenantMax      = 255
)

const secretPrefix = "spd_cs_"

func newID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("%w: minting an identifier: %w", authn.ErrUnavailable, err)
	}
	return id.String(), nil
}

// invalid names the class of argument refused and never the value.
func invalid(what string) error {
	return fmt.Errorf("%w: %s", authn.ErrInvalidArgument, what)
}

func checkDisplayName(name string) error {
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) > displayNameMax {
		return invalid("the display name is not valid text of at most 100 characters")
	}
	return nil
}

func displayNameOrDefault(name, username string) string {
	if name != "" {
		return name
	}
	return username
}

func plain(s string, max int) bool {
	return len(s) <= max && utf8.ValidString(s) && strings.IndexFunc(s, unicode.IsControl) < 0
}

func viaOf(v authn.Verification) (string, error) {
	if !v.Valid() {
		return "", invalid("the way the identifier was verified is not one of sms-code, email-code, invite, " +
			"idp:<provider>, admin, import-unverified and self")
	}
	return v.Via, nil
}

func eventVia(via string) (string, error) {
	switch via {
	case "":
		return byAdmin, nil
	case "admin", "jit", "signup", "import":
		return via, nil
	}
	return "", invalid("the way the account came about is not one of admin, jit, signup and import")
}

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
	ctx, after := provisioning.WithAfterCommit(ctx)
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
		if err = p.provisioning.ProvisionAccount(ctx, tx, accountFactsFor(row, plan.username)); err != nil {
			return err
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
	p.runAfterCommit(ctx, after)
	account, err := toAccount(row, phc != "")
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

type creation struct {
	row         accountRow
	identifiers []identifierRow
	claims      []pendingContactRow
	username    string
	via         string
}

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
	username := in.Username
	if username != "" {
		if username, err = NormalizeIdentifier(authn.IdentifierUsername, username); err != nil {
			return plan, err
		}
	}
	name := displayNameOrDefault(in.DisplayName, username)
	if err = checkDisplayName(name); err != nil {
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
	plan.username = username
	plan.row = accountRow{
		ID: id.String(), Kind: string(kind), Status: statusActive, SignupState: signupNone,
		DisplayName: name, Attributes: attrs, MFARequired: in.MFARequired,
		ValidAfter: now, CreatedAt: now, UpdatedAt: now,
	}

	if usernameRequired(kind) && username == "" {
		return plan, authn.UsernameShape(fmt.Errorf("%w: a user account is created with a username", authn.ErrInvalidIdentifier))
	}

	add := func(k authn.IdentifierKind, value string, via string, flags authn.IdentifierFlags, defaultContact bool) error {
		var normalized, rowID string
		if normalized, err = NormalizeIdentifier(k, value); err != nil {
			return err
		}
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
	if username != "" {
		usernameVia := in.UsernameVerifiedVia
		if usernameVia == (authn.Verification{}) {
			usernameVia = authn.Verification{Via: authn.VerifiedViaAdmin}
		}
		verified, err := viaOf(usernameVia)
		if err != nil {
			return plan, err
		}
		if err = add(authn.IdentifierUsername, username, verified,
			authn.IdentifierFlags{SignIn: true}, false); err != nil {
			return plan, err
		}
	}
	if in.Email != "" {
		verified, err := viaOf(in.EmailVerifiedVia)
		if err != nil {
			return plan, err
		}
		proved := authn.Verification{Via: verified}.Proves()
		flags := authn.IdentifierFlags{Contact: true}
		if via == byImport {
			flags.SignIn = in.EmailFlags.SignIn
		} else if proved {
			flags.SignIn = true
		}
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

func usernameRequired(kind authn.AccountKind) bool {
	return kind == authn.KindUser
}

func (p *product) newCredential(ctx context.Context, in authn.CreateAccount, plan creation) (string, error) {
	if in.Password == "" && in.PasswordHash == "" {
		if plan.via == bySignup {
			return "", fmt.Errorf("%w: a signup opens its account with a password", authn.ErrPasswordPolicy)
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

func (p *product) SetDisplayName(ctx context.Context, id authn.AccountID, name string) error {
	if err := checkDisplayName(name); err != nil {
		return err
	}
	return p.updateAccount(ctx, id, map[string]any{"display_name": name})
}

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

func (p *product) SetProtected(ctx context.Context, id authn.AccountID, protected bool) error {
	return p.updateAccount(ctx, id, map[string]any{"protected": protected})
}

func (p *product) SetMFARequired(ctx context.Context, id authn.AccountID, required bool) error {
	return p.updateAccount(ctx, id, map[string]any{"mfa_required": required})
}

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
		if kind == authn.IdentifierEmail {
			if err = writeEmailClaim(tx, id.String(), normalized, now); err != nil {
				return err
			}
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
				if err = tx.Model(&identifierRow{}).Where("id = ?", held[0].ID).
					Updates(map[string]any{"verified_at": now, "verified_via": verified, "contact": true}).Error; err != nil {
					return err
				}
				if via.Proves() {
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
// The insert of a missing row is the atomic arbiter: two writers cannot both
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
		if kind == authn.IdentifierUsername {
			return fmt.Errorf("%w: a username stays with its account", authn.ErrUsernameImmutable)
		}
		if err = tx.Where("id = ? AND account_id = ?", identifierID, id.String()).Delete(&identifierRow{}).Error; err != nil {
			return err
		}
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
		if !flags.Contact {
			return invalid("a registered address is always a contact")
		}
		proved := authn.Verification{Via: row.VerifiedVia}.Proves()
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

func checkLink(l authn.ExternalLink) error {
	if l.Provider == "" || l.Subject == "" || !plain(l.Provider, providerMax) || !plain(l.Subject, subjectMax) {
		return invalid("the provider and the subject of an external identity are text of bounded length")
	}
	if !plain(l.Email, emailMaxBytes) {
		return invalid("the email address of an external identity is text of at most 254 bytes")
	}
	return nil
}

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

func (p *product) HasLoginWayOtherThanPasskey(ctx context.Context, id authn.AccountID) (bool, error) {
	hasWay, err := loginWayHeld(p.conn(ctx), id.String())
	if err != nil {
		return false, failed("reading the account's ways to log in", err)
	}
	return hasWay, nil
}

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

func secretDigest(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

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
