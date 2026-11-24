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

const maxPasswordBytes = 128

const maxPasswordInput = 4 * maxPasswordBytes

type credentialRow struct {
	AccountID  string
	Status     int16
	Kind       string
	SignIn     bool
	PHC        *string
	MustChange bool
}

// errUnreadableEntry is the failure of an entry that cannot be read: invalid
// credentials to a check, invalid argument to the caller that handed it over. It
// carries the class of the fault and none of the entry.
var errUnreadableEntry = fmt.Errorf("%w, and so is the argument: the password entry is not one this module reads: %w",
	authn.ErrInvalidCredentials, authn.ErrInvalidArgument)

func unreadable(cause error) error {
	if errors.Is(cause, password.ErrMalformedHash) {
		return fmt.Errorf("%w: %w", errUnreadableEntry, password.ErrMalformedHash)
	}
	return errUnreadableEntry
}

// LoginName returns the kind and normalized form of an identifier-and-password
// login name: one containing an @ is an email address and any other a username,
// and a phone number is no login name.
func LoginName(identifier string) (authn.IdentifierKind, string, error) {
	kind := authn.IdentifierUsername
	if strings.Contains(identifier, "@") {
		kind = authn.IdentifierEmail
	}
	normalized, err := NormalizeIdentifier(kind, identifier)
	return kind, normalized, err
}

// identifierKey namespaces the login's back-off keys, so a caller counting on the
// same scope never shares a row with it.
func identifierKey(kind authn.IdentifierKind, normalized string) string {
	return "password\x00" + string(kind) + "\x00" + normalized
}

func (p *product) verifyEntry(ctx context.Context, e password.Entry, plaintext string) (bool, error) {
	started := time.Now()
	held, err := p.gov.Verify(ctx, e, plaintext, p.wait)
	if err == nil {
		p.meters.derived(ctx, e.Params().Algorithm, time.Since(started).Seconds())
	}
	return held, err
}

// hashPassword does not fall back to another algorithm when a parameter cannot be
// read; the read's error is the call's.
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

func derivationFailed(err error) error {
	if errors.Is(err, authn.ErrBusy) {
		return err
	}
	return fmt.Errorf("%w: a password derivation did not run: %w", authn.ErrUnavailable, err)
}

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

func (p *product) countFailure(ctx context.Context, named bool, key string, known bool, id authn.AccountID) {
	if named {
		p.fail(ctx, authn.ScopeIdentifier, key, "")
	}
	if known {
		p.fail(ctx, authn.ScopeAccount, id.String(), id)
	}
}

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

func (p *product) upgrade(ctx context.Context, id authn.AccountID, stored string, entry password.Entry, plaintext string) {
	params, err := p.hashParams()
	if err != nil {
		return
	}
	if !entry.Legacy() && !entry.Params().Weaker(params) {
		return
	}
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

func normalizeNew(plaintext string) (string, error) {
	if len(plaintext) > maxPasswordInput {
		return "", fmt.Errorf("%w: the password is longer than %d bytes", authn.ErrPasswordPolicy, maxPasswordBytes)
	}
	return password.Normalize(plaintext), nil
}

func loginIdentifiers(tx *gorm.DB, id string) (all []string, login bool, err error) {
	var rows []identifierRow
	if err = tx.Where("account_id = ?", id).Find(&rows).Error; err != nil {
		return nil, false, err
	}
	for _, r := range rows {
		all = append(all, r.Value)
		if r.SignIn && (r.Kind == string(authn.IdentifierUsername) || r.Kind == string(authn.IdentifierEmail)) {
			login = true
		}
	}
	return all, login, nil
}
