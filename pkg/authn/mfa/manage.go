package mfa

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/vislake/speed/pkg/authn"
)

// Factors lists the account's factors with their status, carrying nothing a factor keeps for itself.
func (m *product) Factors(ctx context.Context, id authn.AccountID) ([]authn.FactorRecord, error) {
	if id == "" {
		return nil, authn.ErrInvalidAccountID
	}
	recs, err := m.register.list(ctx, id, -1)
	if err != nil {
		return nil, m.unavailable(err)
	}
	return recs, nil
}

func (m *product) renameFactor(ctx context.Context, id authn.AccountID, factorID, label string) (authn.FactorRecord, bool, error) {
	if id == "" {
		return authn.FactorRecord{}, false, authn.ErrInvalidAccountID
	}
	// Read before writing: a kind that takes no name refuses one without a write.
	existing, found, err := m.register.find(ctx, id, factorID)
	if err != nil {
		return authn.FactorRecord{}, false, m.unavailable(err)
	}
	if !found {
		return authn.FactorRecord{}, false, nil
	}
	if !factorTakesName(existing.Kind) && label != "" {
		return authn.FactorRecord{}, true, fmt.Errorf("%w: a %s factor carries no name", authn.ErrInvalidArgument, existing.Kind)
	}
	renamed, err := m.register.updateLabel(ctx, id, factorID, label)
	if err != nil {
		return authn.FactorRecord{}, false, m.unavailable(err)
	}
	if !renamed {
		return authn.FactorRecord{}, false, nil
	}
	rec, found, err := m.register.find(ctx, id, factorID)
	if err != nil {
		return authn.FactorRecord{}, false, m.unavailable(err)
	}
	return rec, found, nil
}

// Kinds lists the kinds of factor taken in, in a stable order.
func (m *product) Kinds() []authn.FactorKind { return slices.Clone(m.kinds) }

// Remove removes a factor: its kind removes its own data, then the row goes. It returns ErrLastFactor when the account requires a factor and this is its last active one, and ErrFactorNotEnrolled for a row that is not the account's or not there. It ends and rotates no session; repeating a removal is idempotent.
func (m *product) Remove(ctx context.Context, id authn.AccountID, factorID string) error {
	if id == "" {
		return authn.ErrInvalidAccountID
	}
	rec, found, err := m.register.find(ctx, id, factorID)
	if err != nil {
		return m.unavailable(err)
	}
	if !found {
		return authn.ErrFactorNotEnrolled
	}
	if rec.Status == authn.FactorActive {
		if err := m.guardLast(ctx, rec); err != nil {
			return err
		}
	}
	if err := m.removeKind(ctx, rec); err != nil {
		return m.unavailable(err)
	}
	if _, err := m.register.remove(ctx, id, rec.ID); err != nil {
		return m.unavailable(err)
	}
	byClass := by(ctx, id)
	m.publishChanged(ctx, rec, changeRemoved, byClass)
	m.record(ctx, actionRemove, outcomeSuccess, rec, byClass)
	return nil
}

func (m *product) removeFactorAndSettleSessions(ctx context.Context, id authn.AccountID, factorID string, current authn.SessionID) (authn.Issued, error) {
	rec, found, err := m.register.find(ctx, id, factorID)
	if err != nil {
		return authn.Issued{}, m.unavailable(err)
	}
	err = m.Remove(ctx, id, factorID)
	if err != nil && !errors.Is(err, authn.ErrFactorNotEnrolled) {
		return authn.Issued{}, err
	}
	if found && rec.Status != authn.FactorActive {
		return authn.Issued{}, nil
	}
	return m.settleSessions(ctx, id, current, authn.ReasonSelf, "a factor was removed")
}

func (m *product) settleSessions(ctx context.Context, id authn.AccountID, keep authn.SessionID, reason, change string) (authn.Issued, error) {
	if err := m.revokeSessions(ctx, id, keep, reason, change); err != nil {
		return authn.Issued{}, err
	}
	if keep == "" || m.sessions == nil {
		return authn.Issued{}, nil
	}
	issued, err := m.sessions.Rotate(ctx, keep)
	if err != nil {
		return authn.Issued{}, m.unavailable(err)
	}
	return issued, nil
}

func (m *product) revokeSessions(ctx context.Context, id authn.AccountID, keep authn.SessionID, reason, change string) error {
	if m.sessions == nil {
		m.logger.Warn(ModuleName+": "+change+" and no session module is taken in, so the account's sessions "+
			"stay valid", slog.String("account", string(id)))
		return nil
	}
	if _, err := m.sessions.RevokeAccount(ctx, id, keep, reason); err != nil {
		return m.unavailable(err)
	}
	return nil
}

func (m *product) guardLast(ctx context.Context, rec authn.FactorRecord) error {
	account, err := m.accounts.Get(ctx, rec.Account)
	if err != nil {
		if errors.Is(err, authn.ErrAccountNotFound) {
			return err
		}
		return m.unavailable(err)
	}
	if !account.MFARequired {
		return nil
	}
	active, err := m.register.list(ctx, rec.Account, int(authn.FactorActive))
	if err != nil {
		return m.unavailable(err)
	}
	for _, other := range active {
		if other.ID != rec.ID {
			return nil
		}
	}
	m.record(ctx, actionRemove, outcomeDenied, rec, by(ctx, rec.Account))
	return authn.ErrLastFactor
}

func (m *product) removeKind(ctx context.Context, rec authn.FactorRecord) error {
	factor, present := m.factors[rec.Kind]
	if !present {
		m.logger.Warn(ModuleName+": a factor is removed whose kind has no implementation here, so what that "+
			"kind keeps for it stays",
			slog.String("account", string(rec.Account)), slog.String("factor", rec.ID))
		return nil
	}
	return factor.Remove(ctx, rec.Account, rec)
}

// Reset makes the account register its factors again: it sets mfa_required, disables every factor, revokes the sessions when a session store is present, then publishes the change. A failure at any step leaves the account stricter and the call can be repeated. removed counts the rows this call disabled.
func (m *product) Reset(ctx context.Context, id authn.AccountID, _ string) (int, error) {
	if id == "" {
		return 0, authn.ErrInvalidAccountID
	}
	if err := m.admin.SetMFARequired(ctx, id, true); err != nil {
		if errors.Is(err, authn.ErrAccountNotFound) {
			return 0, err
		}
		return 0, m.unavailable(err)
	}
	removed, err := m.register.disableAll(ctx, id)
	if err != nil {
		return 0, m.unavailable(err)
	}
	if err := m.revokeSessions(ctx, id, "", authn.ReasonFactorReset, "factors were reset"); err != nil {
		return removed, err
	}
	byClass := byClassSystem
	if _, ok := authn.FromContext(ctx); ok {
		byClass = byClassAdmin
	}
	m.publishChanged(ctx, authn.FactorRecord{Account: id}, changeReset, byClass)
	return removed, nil
}

func (m *product) flagged(ctx context.Context, rec authn.FactorRecord) {
	if err := m.register.disable(ctx, rec.Account, rec.ID); err != nil {
		m.logger.Error(ModuleName+": a flagged factor could not be disabled",
			slog.String("account", string(rec.Account)), slog.String("factor", rec.ID),
			slog.String("cause", err.Error()))
	}
	m.publishChanged(ctx, rec, changeFlagged, byClassSystem)
}

// Seasoned reports whether the account has an active non-SMS factor that has been active for sensitive-min-age.
func (m *product) Seasoned(ctx context.Context, id authn.AccountID) (bool, error) {
	if id == "" {
		return false, authn.ErrInvalidAccountID
	}
	cutoff := m.clock.Now().Add(-m.settings.sensitiveMinAge())
	ok, err := m.register.seasoned(ctx, id, cutoff)
	if err != nil {
		return false, m.unavailable(err)
	}
	return ok, nil
}

// AccountCreated changes nothing: a new account has no factor.
func (m *product) AccountCreated(context.Context, authn.AccountID) error { return nil }

// AccountDisabled leaves the factors where they are.
func (m *product) AccountDisabled(context.Context, authn.AccountID) error { return nil }

// AccountEnabled changes nothing: the factors a disable kept stay usable.
func (m *product) AccountEnabled(context.Context, authn.AccountID) error { return nil }

// ServiceSecretRevoked has nothing to do: a service secret is not a factor.
func (m *product) ServiceSecretRevoked(context.Context, authn.AccountID, string) error { return nil }

// AccountDeleted removes every factor's kind data and row for the account, returns every failure together, and finishes the work when repeated.
func (m *product) AccountDeleted(ctx context.Context, id authn.AccountID) error {
	if id == "" {
		return authn.ErrInvalidAccountID
	}
	recs, err := m.register.list(ctx, id, -1)
	if err != nil {
		return m.unavailable(err)
	}
	var failed []error
	for _, rec := range recs {
		if err := m.removeKind(ctx, rec); err != nil {
			failed = append(failed, fmt.Errorf("removing what a factor keeps: %w", err))
			continue
		}
		if _, err := m.register.remove(ctx, id, rec.ID); err != nil {
			failed = append(failed, err)
		}
	}
	if len(failed) > 0 {
		return m.unavailable(errors.Join(failed...))
	}
	return nil
}
