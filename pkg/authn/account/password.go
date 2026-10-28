package account

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/password"
	"github.com/vislake/speed/pkg/event"
)

// maxPasswordBytes is the longest a password is once it is normalized. A password
// is the input of a derivation that costs what the entry says, and one without a
// bound would be a way to make the process read and normalize whatever a caller
// sends.
const maxPasswordBytes = 128

// maxPasswordInput bounds what is normalized at all: NFKC can grow a string by a
// factor of a few, so a password longer than this cannot come within the bound
// once normalized and is refused before the work of normalizing it.
const maxPasswordInput = 4 * maxPasswordBytes

// credentialRow is the password credential of one account, as the verification
// reads it together with the account's standing and the choice the identifier
// carries.
type credentialRow struct {
	AccountID  string
	Status     int16
	Kind       string
	SignIn     bool
	PHC        *string
	MustChange bool
}

// errUnreadableEntry is the failure of an entry that cannot be read. It is an
// invalid credential to the caller of a check and an invalid argument to the
// caller that handed the entry over, and it carries the class of the fault and
// none of the entry.
var errUnreadableEntry = fmt.Errorf("%w, and so is the argument: the password entry is not one this module reads: %w",
	authn.ErrInvalidCredentials, authn.ErrInvalidArgument)

// unreadable is the error for an entry that Parse refused. The entry is not in it,
// and neither is any part of the parser's text, which can carry a number the
// entry stated.
func unreadable(cause error) error {
	if errors.Is(cause, password.ErrMalformedHash) {
		return fmt.Errorf("%w: %w", errUnreadableEntry, password.ErrMalformedHash)
	}
	return errUnreadableEntry
}

// LoginName reads the identifier of an identifier-and-password credential: one
// containing an @ is an email address and any other a username, and a phone
// number is no login name. The second result is the normalized form, which is
// the form the identifier is stored and looked up in.
//
// It is exported because whoever *writes* an account that is to be signed into
// by a password has to read its identifier the way the password surface reads
// it: a host command that seeds the first operator is the caller this exists
// for, and a second copy of the rule there would be one that drifts.
func LoginName(identifier string) (authn.IdentifierKind, string, error) {
	kind := authn.IdentifierUsername
	if strings.Contains(identifier, "@") {
		kind = authn.IdentifierEmail
	}
	normalized, err := NormalizeIdentifier(kind, identifier)
	return kind, normalized, err
}

// identifierKey is the key the identifier-level back-off of a password login is
// kept under. It is namespaced to this check, so that a caller keeping a count of
// its own on the same scope never shares a row with it and the two never count
// one failure twice.
func identifierKey(kind authn.IdentifierKind, normalized string) string {
	return "password\x00" + string(kind) + "\x00" + normalized
}

// verifyEntry runs one derivation of a check under the capacity the module allows
// and reports how long it took to the metric capability.
func (p *product) verifyEntry(ctx context.Context, e password.Entry, plaintext string) (bool, error) {
	started := time.Now()
	held, err := p.gov.Verify(ctx, e, plaintext, p.wait)
	if err == nil {
		p.meters.derived(ctx, e.Params().Algorithm, time.Since(started).Seconds())
	}
	return held, err
}

// hashPassword derives the PHC text of a normalized password under the current
// parameters. A parameter that cannot be read is the error of the call: the
// algorithm is not replaced by its declared default.
func (p *product) hashPassword(ctx context.Context, plaintext string) (string, error) {
	params, err := p.hashParams()
	if err != nil {
		return "", err
	}
	started := time.Now()
	phc, err := p.gov.Hash(ctx, params, plaintext, p.wait)
	if err != nil {
		return "", err
	}
	p.meters.derived(ctx, params.Algorithm, time.Since(started).Seconds())
	return phc, nil
}

// derivationFailed is the error a failed derivation is carried out as: busy as it
// is, and anything else as the dependency it is.
func derivationFailed(err error) error {
	if errors.Is(err, authn.ErrBusy) {
		return err
	}
	return fmt.Errorf("%w: a password derivation did not run: %w", authn.ErrUnavailable, err)
}

// Verify checks a password.
//
// The identifier is normalized, and a name that cannot be, a phone number, an
// unknown name, a disabled account and an account with no password all cost one
// derivation at the current parameters and are all authn.ErrInvalidCredentials,
// as is a wrong password; what separates them is nothing a caller can observe
// but the time the call takes, and the derivation is what keeps that equal. An
// email address its holder did not choose as a sign-in way is no login name and
// is answered like an unknown one, at the same cost.
//
// Two back-offs count. The one on the submitted name, whether or not the name is
// known, is visible: while it holds, nothing is derived and the call is a
// *authn.ThrottledError. The one on the account, which every spelling of every
// identifier reaches, is not: past its threshold the password is still derived
// and the answer is authn.ErrInvalidCredentials, even for the right one, because
// a different answer would tell a caller who changed the spelling that the
// account exists. A success clears both, unless the account-level one was
// holding.
func (p *product) Verify(ctx context.Context, identifier, plaintext string) (authn.AccountID, authn.VerifyResult, error) {
	kind, name, nameErr := LoginName(identifier)
	var key string
	if nameErr == nil {
		key = identifierKey(kind, name)
		wait, err := p.Check(ctx, authn.ScopeIdentifier, key)
		if err != nil {
			return "", authn.VerifyResult{}, err
		}
		if wait > 0 {
			return "", authn.VerifyResult{}, &authn.ThrottledError{RetryAfter: wait}
		}
	}

	var row credentialRow
	known := false
	if nameErr == nil {
		var err error
		if row, known, err = p.credentialFor(ctx, kind, name); err != nil {
			return "", authn.VerifyResult{}, err
		}
	}
	usable := known && row.Status == statusActive && row.Kind == string(authn.KindUser) && row.PHC != nil
	accountID := authn.AccountID(row.AccountID)

	accountHeld := false
	if known {
		wait, err := p.Check(ctx, authn.ScopeAccount, row.AccountID)
		if err != nil {
			return "", authn.VerifyResult{}, err
		}
		accountHeld = wait > 0
	}

	candidate, tooLong := p.candidate(plaintext)
	entry, err := p.entryFor(row, usable)
	if err != nil {
		return "", authn.VerifyResult{}, err
	}
	matched := false
	if tooLong {
		// A password beyond the bound cannot have been written, so it matches
		// nothing and costs nothing to say so. What it was meant to match is not
		// told by the time the call takes: the caller chose the length.
		matched = false
	} else {
		held, derr := p.verifyEntry(ctx, entry, candidate)
		if derr != nil {
			if errors.Is(derr, authn.ErrBusy) {
				p.meters.verified(ctx, outcomeBusy)
			}
			return "", authn.VerifyResult{}, derivationFailed(derr)
		}
		matched = held
	}

	if !matched || !usable || accountHeld {
		p.meters.verified(ctx, outcomeBad)
		p.countFailure(ctx, nameErr == nil, key, known, accountID)
		return "", authn.VerifyResult{}, fmt.Errorf("%w", authn.ErrInvalidCredentials)
	}

	p.clearFailures(ctx, key, accountID)
	p.meters.verified(ctx, outcomeOK)
	p.upgrade(ctx, accountID, *row.PHC, entry, candidate)
	return accountID, authn.VerifyResult{MustChange: row.MustChange}, nil
}

// candidate is the form a password is checked in: NFKC. A password that cannot be
// within the bound once normalized is reported as too long, and nothing is
// derived from it.
func (p *product) candidate(plaintext string) (string, bool) {
	if len(plaintext) > maxPasswordInput {
		return "", true
	}
	normalized := password.Normalize(plaintext)
	if len(normalized) > maxPasswordBytes {
		return "", true
	}
	return normalized, false
}

// credentialFor finds the account an identifier is bound to, with its standing and
// its credential, in one statement. known is false when no account holds it, and
// also when the identifier is an email address its holder did not choose as a
// sign-in way: such an address is no login name, so it is answered like one
// nobody holds. A username is always a login name.
func (p *product) credentialFor(ctx context.Context, kind authn.IdentifierKind, name string) (credentialRow, bool, error) {
	var rows []credentialRow
	err := p.conn(ctx).Raw("SELECT a.id AS account_id, a.status AS status, a.kind AS kind, "+
		"i.sign_in AS sign_in, c.phc AS phc, COALESCE(c.must_change, FALSE) AS must_change "+
		"FROM "+tableIdentifiers+" i JOIN "+tableAccounts+" a ON a.id = i.account_id "+
		"LEFT JOIN "+tableCredentials+" c ON c.account_id = a.id "+
		"WHERE i.kind = ? AND i.value = ?", string(kind), name).Scan(&rows).Error
	if err != nil {
		return credentialRow{}, false, failed("looking a login name up", err)
	}
	if len(rows) == 0 {
		return credentialRow{}, false, nil
	}
	if kind == authn.IdentifierEmail && !rows[0].SignIn {
		return credentialRow{}, false, nil
	}
	return rows[0], true, nil
}

// entryFor is the entry a check derives against: the account's own when it can be
// checked at all, and the decoy for the current parameters when it cannot. An
// entry that is stored and cannot be read counts as no entry, is reported at
// Error without any of its text, and is checked against the decoy as well.
func (p *product) entryFor(row credentialRow, usable bool) (password.Entry, error) {
	if usable {
		entry, err := password.Parse(*row.PHC)
		if err == nil {
			return entry, nil
		}
		p.logger.Error(ModuleName+": a stored password entry cannot be read, so the account cannot log in with "+
			"a password until it is written again",
			slog.String("account", row.AccountID))
	}
	entry, err := p.decoy.For(p.decoyParams())
	if err != nil {
		return password.Entry{}, fmt.Errorf("%w: making the decoy entry: %w", authn.ErrUnavailable, err)
	}
	return entry, nil
}

// countFailure records a failed check on the keys it is counted under. A failed
// write of the count is logged and changes nothing the caller sees: the answer is
// the refusal it already is.
func (p *product) countFailure(ctx context.Context, named bool, key string, known bool, id authn.AccountID) {
	if named {
		p.fail(ctx, authn.ScopeIdentifier, key, "")
	}
	if known {
		p.fail(ctx, authn.ScopeAccount, id.String(), id)
	}
}

// clearFailures forgets the failures a check that held no longer has a use for.
func (p *product) clearFailures(ctx context.Context, key string, id authn.AccountID) {
	if key != "" {
		if err := p.Succeed(ctx, authn.ScopeIdentifier, key); err != nil {
			p.logger.Warn(ModuleName+": the failure count of a name could not be cleared", slog.String("cause", describe(err)))
		}
	}
	if err := p.Succeed(ctx, authn.ScopeAccount, id.String()); err != nil {
		p.logger.Warn(ModuleName+": the failure count of an account could not be cleared", slog.String("cause", describe(err)))
	}
}

// fail counts one failure and, when it started a back-off, says so in the log, in
// an event and in a counter: the account-level back-off is silent to the caller,
// and this is how an operator learns of it.
func (p *product) fail(ctx context.Context, scope authn.ThrottleScope, key string, id authn.AccountID) {
	engaged, err := p.Fail(ctx, scope, key)
	if err != nil {
		p.logger.Warn(ModuleName+": a failed attempt could not be counted", slog.String("scope", string(scope)),
			slog.String("cause", describe(err)))
		return
	}
	if !engaged {
		return
	}
	p.meters.engaged(ctx, scope)
	b := &batch{after: []event.Event{throttleEngaged(scope, id)}}
	p.publish(ctx, b)
}

// upgrade writes a stored entry again with the current parameters when it is
// weaker than they are, or is in the legacy form, after a check that held. The
// write is a compare-and-swap on the old entry, so two checks that both found it
// weak write it once, and a failure of any step is a line in the log and nothing
// to the login that triggered it.
func (p *product) upgrade(ctx context.Context, id authn.AccountID, stored string, entry password.Entry, plaintext string) {
	params, err := p.hashParams()
	if err != nil {
		// The algorithm setting cannot be read, so this entry is left as it is.
		// The read reported the fault; the login that triggered the upgrade
		// still succeeds.
		return
	}
	if !entry.Legacy() && !entry.Params().Weaker(params) {
		return
	}
	// The wait is nothing: a login is not made to wait for the capacity to write
	// its own entry again, and the next login tries.
	phc, err := p.gov.Hash(ctx, params, plaintext, 0)
	if err != nil {
		p.logger.Warn(ModuleName+": a password entry weaker than the current cost could not be written again",
			slog.String("account", id.String()), slog.String("cause", describe(err)))
		return
	}
	res := p.conn(ctx).Exec("UPDATE "+tableCredentials+" SET phc = ? WHERE account_id = ? AND phc = ?",
		phc, id.String(), stored)
	if res.Error != nil {
		p.logger.Warn(ModuleName+": a password entry weaker than the current cost could not be written again",
			slog.String("account", id.String()), slog.String("cause", describe(res.Error)))
		return
	}
	if res.RowsAffected == 1 {
		p.meters.verified(ctx, outcomeUpgraded)
	}
}

// policy judges a new password for an account known by these identifiers: no
// fewer characters than the setting says, no more than the bound, and not the
// account's own identifier in any of the forms it is stored. It reads the
// normalized text.
func (p *product) policy(normalized string, identifiers []string) error {
	switch {
	case utf8.RuneCountInString(normalized) < p.minLength():
		return fmt.Errorf("%w: the password is shorter than the policy asks", authn.ErrPasswordPolicy)
	case len(normalized) > maxPasswordBytes:
		return fmt.Errorf("%w: the password is longer than %d bytes", authn.ErrPasswordPolicy, maxPasswordBytes)
	}
	folded := strings.ToLower(normalized)
	for _, own := range identifiers {
		if own != "" && (normalized == own || folded == strings.ToLower(own)) {
			return fmt.Errorf("%w: the password is one of the account's own identifiers", authn.ErrPasswordPolicy)
		}
	}
	return nil
}

// normalizeNew is the normalized form of a new password, or the policy error when
// it cannot be within the bound at all.
func normalizeNew(plaintext string) (string, error) {
	if len(plaintext) > maxPasswordInput {
		return "", fmt.Errorf("%w: the password is longer than %d bytes", authn.ErrPasswordPolicy, maxPasswordBytes)
	}
	return password.Normalize(plaintext), nil
}

// loginIdentifiers reads the identifiers of an account that can serve as a login
// name, and every identifier it holds for the policy to compare against.
func loginIdentifiers(tx *gorm.DB, id string) (all []string, login bool, err error) {
	var rows []identifierRow
	if err = tx.Where("account_id = ?", id).Find(&rows).Error; err != nil {
		return nil, false, err
	}
	for _, r := range rows {
		all = append(all, r.Value)
		// A password login name is the username or an email the holder chose as
		// a sign-in way. A phone never is one, and an email that is only a
		// contact does not count.
		if r.SignIn && (r.Kind == string(authn.IdentifierUsername) || r.Kind == string(authn.IdentifierEmail)) {
			login = true
		}
	}
	return all, login, nil
}
