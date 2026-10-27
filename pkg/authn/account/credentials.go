package account

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/password"
)

// hasCredential reports whether the account holds a password credential. It is
// the row's presence: an entry this module cannot read still means the account
// signs in with a password.
func hasCredential(tx *gorm.DB, id string) (bool, error) {
	var n int64
	if err := tx.Raw("SELECT COUNT(*) FROM "+tableCredentials+" WHERE account_id = ?", id).Scan(&n).Error; err != nil {
		return false, err
	}
	return n > 0, nil
}

// storedEntry is the credential an account has, read and bounded: ok is false when
// the account has none or when what it has cannot be read, which no check can use
// and which is reported without the entry's text.
func (p *product) storedEntry(tx *gorm.DB, id string) (entry password.Entry, ok bool, err error) {
	var rows []struct{ PHC string }
	if err = tx.Raw("SELECT phc FROM "+tableCredentials+" WHERE account_id = ?", id).Scan(&rows).Error; err != nil {
		return password.Entry{}, false, err
	}
	if len(rows) == 0 {
		return password.Entry{}, false, nil
	}
	entry, perr := password.Parse(rows[0].PHC)
	if perr != nil {
		p.logger.Error(ModuleName + ": a stored password entry cannot be read, so it is not compared with a new password")
		return password.Entry{}, false, nil
	}
	return entry, true, nil
}

// writeCredential writes the credential of an account, replacing the one there.
// It is a raw statement: a hash is not an encrypted column, and a write of it
// leaves no audit row whose detail could name it.
func (p *product) writeCredential(tx *gorm.DB, id, phc string, mustChange bool) error {
	return tx.Exec("INSERT INTO "+tableCredentials+" (account_id, phc, changed_at, must_change) VALUES (?, ?, ?, ?)"+
		" ON CONFLICT (account_id) DO UPDATE SET phc = EXCLUDED.phc, changed_at = EXCLUDED.changed_at,"+
		" must_change = EXCLUDED.must_change", id, phc, p.at(), mustChange).Error
}

// attribution resolves the event attribution of a password write and holds it to
// the closed sets the payload carries. An empty By is inferred from whether the
// call carries the current password, and an empty Channel is the non-reset case.
func attribution(opt authn.SetPasswordOptions) (by, channel string, err error) {
	by = opt.By
	if by == "" {
		by = byAdmin
		if opt.Current != "" {
			by = bySelf
		}
	}
	switch by {
	case bySelf, byAdmin, byReset:
	default:
		return "", "", fmt.Errorf("%w: the attribution of a password change is self, admin or reset", authn.ErrInvalidArgument)
	}
	switch opt.Channel {
	case "", channelEmail, channelSMS:
		return by, opt.Channel, nil
	default:
		return "", "", fmt.Errorf("%w: the channel of a password change is email or sms", authn.ErrInvalidArgument)
	}
}

// requireInTx holds a password write to the state its caller requires, inside the
// write transaction: the account still enabled, a credential row already there,
// and an identifier row that still belongs to the account. Any of them
// unsatisfied is authn.ErrAccountUnavailable. The requirement is read under the
// account's lock, so a status change or a removal committed before it is seen.
func (p *product) requireInTx(tx *gorm.DB, row accountRow, opt authn.SetPasswordOptions) error {
	if opt.RequireActive && statusOf(row.Status) != authn.StatusActive {
		return fmt.Errorf("%w: the account is not enabled", authn.ErrAccountUnavailable)
	}
	if opt.RequireExisting {
		has, err := hasCredential(tx, row.ID)
		if err != nil {
			return err
		}
		if !has {
			return fmt.Errorf("%w: the account has no password credential", authn.ErrAccountUnavailable)
		}
	}
	if opt.RequireIdentifier != "" {
		var n int64
		if err := tx.Raw("SELECT COUNT(*) FROM "+tableIdentifiers+" WHERE id = ? AND account_id = ?",
			opt.RequireIdentifier, row.ID).Scan(&n).Error; err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("%w: the identifier does not belong to the account", authn.ErrAccountUnavailable)
		}
	}
	return nil
}

// Set writes a password.
//
// The password is normalized and judged by the policy: no fewer characters than
// the setting asks, at most 128 bytes, not an identifier of the account, and not
// the one the account has, each of which is authn.ErrPasswordPolicy, and an
// account with no username and no email address to log in with is
// authn.ErrIdentifierRequired. A call that carries the current password checks it
// first, and a wrong one is authn.ErrInvalidCredentials and counts against the
// account like a failed login does. No session is revoked here: the caller
// does that once the change is made, which is why this module does not depend on
// the session module.
func (p *product) Set(ctx context.Context, id authn.AccountID, plaintext string, opt authn.SetPasswordOptions) error {
	by, channel, err := attribution(opt)
	if err != nil {
		return err
	}
	conn := p.conn(ctx)
	row, err := loadAccount(conn, id)
	if err != nil {
		return failed("reading an account", err)
	}
	if row.Kind != string(authn.KindUser) {
		return invalid("only a user account has a password")
	}
	identifiers, login, err := loginIdentifiers(conn, row.ID)
	if err != nil {
		return failed("reading an account's identifiers", err)
	}
	if !login {
		return fmt.Errorf("%w: the account has no username or email address to log in with", authn.ErrIdentifierRequired)
	}
	normalized, err := normalizeNew(plaintext)
	if err != nil {
		return err
	}
	if err = p.policy(normalized, identifiers); err != nil {
		return err
	}

	current, hasCurrent, err := p.storedEntry(conn, row.ID)
	if err != nil {
		return failed("reading an account's credential", err)
	}
	same := false
	if opt.Current != "" {
		if same, err = p.confirm(ctx, id, current, hasCurrent, opt.Current, normalized); err != nil {
			return err
		}
	} else if hasCurrent {
		held, derr := p.verifyEntry(ctx, current, normalized)
		if derr != nil {
			return derivationFailed(derr)
		}
		same = held
	}
	if same {
		return fmt.Errorf("%w: the new password is the current one", authn.ErrPasswordPolicy)
	}

	phc, err := p.hashPassword(ctx, normalized)
	if err != nil {
		return derivationFailed(err)
	}
	b := &batch{}
	err = conn.Transaction(func(tx *gorm.DB) error {
		locked, lerr := p.lock(tx, id)
		if lerr != nil {
			return lerr
		}
		if lerr = p.requireInTx(tx, locked, opt); lerr != nil {
			return lerr
		}
		if lerr = p.writeCredential(tx, locked.ID, phc, opt.MustChange); lerr != nil {
			return lerr
		}
		if opt.AdvanceValidAfter {
			now := p.at()
			if lerr = tx.Model(&accountRow{}).Where("id = ?", locked.ID).Updates(map[string]any{
				"valid_after": laterOf(utc(locked.ValidAfter), ceilSecond(now)),
				"updated_at":  now,
			}).Error; lerr != nil {
				return lerr
			}
		}
		return p.emit(tx, b, passwordChanged(id, by, channel), false)
	})
	if err != nil {
		return failed("writing a password", err)
	}
	p.publish(ctx, b)
	return nil
}

// confirm checks the current password a self-service change carries, against the
// credential the account has. A wrong one, and an account with no credential to
// confirm against, are authn.ErrInvalidCredentials; a wrong one is a failed attempt
// of the account, and the account-level back-off holds this check as it holds a
// login, silently. It reports whether the new password is the one that was
// confirmed.
func (p *product) confirm(ctx context.Context, id authn.AccountID, entry password.Entry, has bool, current, newer string) (bool, error) {
	wait, err := p.Check(ctx, authn.ScopeAccount, id.String())
	if err != nil {
		return false, err
	}
	candidate, tooLong := p.candidate(current)
	held := false
	switch {
	case !has || tooLong:
		// Nothing to confirm against, or nothing that could match: the check
		// derives against the decoy so that the two cost the same.
		decoy, derr := p.decoy.For(p.decoyParams())
		if derr != nil {
			return false, fmt.Errorf("%w: making the decoy entry: %w", authn.ErrUnavailable, derr)
		}
		if _, derr = p.verifyEntry(ctx, decoy, candidate); derr != nil {
			return false, derivationFailed(derr)
		}
	default:
		var derr error
		if held, derr = p.verifyEntry(ctx, entry, candidate); derr != nil {
			return false, derivationFailed(derr)
		}
	}
	if !held || wait > 0 {
		p.fail(ctx, authn.ScopeAccount, id.String(), id)
		return false, fmt.Errorf("%w", authn.ErrInvalidCredentials)
	}
	if err = p.Succeed(ctx, authn.ScopeAccount, id.String()); err != nil {
		p.logger.Warn(ModuleName+": the failure count of an account could not be cleared", slogCause(err))
	}
	return candidate == newer, nil
}

// ImportHash writes a PHC or legacy entry as the account's password credential. A
// legacy entry is converted without loss, the salt and the key being the same
// bytes under another wrapper, and a form that cannot be read, or is out of the
// bounds, is refused as unreadable. The strength of what is imported is not
// judged: it was written elsewhere and is as strong as it is, and the first check
// that holds writes it again at the current cost if it is weaker.
func (p *product) ImportHash(ctx context.Context, id authn.AccountID, encoded string, mustChange bool) error {
	phc, err := p.importable(encoded)
	if err != nil {
		return err
	}
	b := &batch{}
	err = p.conn(ctx).Transaction(func(tx *gorm.DB) error {
		var row accountRow
		if row, err = p.lock(tx, id); err != nil {
			return err
		}
		if row.Kind != string(authn.KindUser) {
			return invalid("only a user account has a password")
		}
		var login bool
		if _, login, err = loginIdentifiers(tx, row.ID); err != nil {
			return err
		}
		if !login {
			return fmt.Errorf("%w: the account has no username or email address to log in with", authn.ErrIdentifierRequired)
		}
		if err = p.writeCredential(tx, row.ID, phc, mustChange); err != nil {
			return err
		}
		return p.emit(tx, b, passwordChanged(id, byImport, ""), false)
	})
	if err != nil {
		return failed("importing a password entry", err)
	}
	p.publish(ctx, b)
	return nil
}

// Clear deletes the account's password credential, so that it has none and has to
// set one again. An account with none is already clear, and the call is nil.
func (p *product) Clear(ctx context.Context, id authn.AccountID) error {
	err := p.conn(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := p.lock(tx, id); err != nil {
			return err
		}
		return tx.Exec("DELETE FROM "+tableCredentials+" WHERE account_id = ?", id.String()).Error
	})
	return failed("clearing a password", err)
}

// secretTouchEvery is how often at most the time a secret was last used is
// written, so that a client that presents its secret on every call does not make
// a write of every one.
const secretTouchEvery = time.Minute

// VerifyServiceSecret checks a service account's client credential.
//
// The row is found by the SHA-256 of the secret and has to be neither revoked nor
// expired, and its account has to equal the client identifier, which is compared
// in constant time, and be an enabled service account. The secret is 256 random
// bits and is not derived from: a dictionary cannot be run against it, so there is
// no derivation to be made to run in numbers. Every refusal is
// authn.ErrInvalidCredentials. It returns the account, the secret's identifier and
// the expiry, where the zero time means it does not expire.
func (p *product) VerifyServiceSecret(ctx context.Context, clientID, secret string) (authn.AccountID, string, time.Time, error) {
	refused := func() (authn.AccountID, string, time.Time, error) {
		return "", "", time.Time{}, fmt.Errorf("%w", authn.ErrInvalidCredentials)
	}
	if !strings.HasPrefix(secret, secretPrefix) {
		return refused()
	}
	now := p.at()
	var rows []struct {
		ID        string
		AccountID string
		ExpiresAt *time.Time
		Status    int16
		Kind      string
	}
	err := p.conn(ctx).Raw("SELECT s.id AS id, s.account_id AS account_id, s.expires_at AS expires_at, "+
		"a.status AS status, a.kind AS kind FROM "+tableServiceKeys+" s JOIN "+tableAccounts+" a ON a.id = s.account_id "+
		"WHERE s.secret_hash = ? AND s.revoked_at IS NULL AND (s.expires_at IS NULL OR s.expires_at > ?)",
		secretDigest(secret), now).Scan(&rows).Error
	if err != nil {
		return "", "", time.Time{}, failed("looking a service secret up", err)
	}
	if len(rows) == 0 {
		return refused()
	}
	row := rows[0]
	if subtle.ConstantTimeCompare([]byte(row.AccountID), []byte(clientID)) != 1 ||
		row.Status != statusActive || row.Kind != string(authn.KindService) {
		return refused()
	}
	err = p.conn(ctx).Exec("UPDATE "+tableServiceKeys+" SET last_used_at = ? WHERE id = ? AND (last_used_at IS NULL OR last_used_at < ?)",
		now, row.ID, now.Add(-secretTouchEvery)).Error
	if err != nil && !errors.Is(err, context.Canceled) {
		p.logger.Warn(ModuleName+": the time a service secret was last used could not be written", slogCause(err))
	}
	return authn.AccountID(row.AccountID), row.ID, timeOf(row.ExpiresAt), nil
}
