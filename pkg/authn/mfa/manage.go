package mfa

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/vislake/speed/pkg/authn"
)

// Factors lists the account's factors with their status. It carries nothing of a
// factor that is secret and nothing a kind keeps of its own: the register holds
// none of it.
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

// renameFactor writes one of the account's factors' label and answers the row as
// it now reads. The label is metadata and is written without any step-up: it is
// the caller's own factor, and a row that is not the account's is answered as
// not found rather than as a refusal, so another account's identifier says
// nothing about whether it exists.
func (m *product) renameFactor(ctx context.Context, id authn.AccountID, factorID, label string) (authn.FactorRecord, bool, error) {
	if id == "" {
		return authn.FactorRecord{}, false, authn.ErrInvalidAccountID
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

// Remove removes a factor: the factor of its kind removes what it keeps, and then
// the row goes. An account that requires a factor may not remove its last active
// one, which is ErrLastFactor; removing a row that is not active never takes the
// account's strength away, so it is always allowed. A factor that is not the
// account's, or not there, is ErrFactorNotEnrolled.
//
// Remove promises nothing about the account's sessions and ends none, rotates
// none. A caller that wants the other devices signed out and the requesting one
// given a new token does it after the removal of an active factor, keeping the
// session that asked; the user-facing route does that in
// removeFactorAndSettleSessions.
//
// A removal that fails in the factor leaves the row where it is and is an
// outage; repeating it finishes the job, because a factor's removal is
// idempotent.
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

// removeFactorAndSettleSessions removes the caller's factor, ends the account's
// other sessions and rotates the one that asked. It is the route's orchestration
// rather than MFA.Remove's work: the capability promises nothing about sessions,
// and a host that calls Remove directly leaves them alone.
//
// The factor is read before the removal, so a pending or disabled row, which never
// added to the account's strength, ends no session and rotates none. The removal's
// refusals and its failures are returned before any session is touched. A name
// that is not there is a removal that has already happened: the sessions are
// settled all the same and ErrFactorNotEnrolled is returned after them, so a
// retry after the session store failed finishes the revocation and the rotation
// the first call could not. The route turns that sentinel into the not-found
// code and carries the rotated cookie. When
// no session module is taken in, the factor is removed and a Warn says the
// sessions stay valid, as Reset does.
//
// The sessions are settled after the row is gone. The other sessions are ended
// first, and the requesting one is rotated only once that succeeded: a rotation
// whose new token the client cannot receive in a failed answer would sign the
// device out while leaving the failure unfixed. If either call fails the route
// answers 503 although the factor is already removed; repeating the request
// completes what is left, because the removal is idempotent.
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
	issued, err := m.settleSessions(ctx, id, current, authn.ReasonSelf, "a factor was removed")
	if err != nil {
		return authn.Issued{}, err
	}
	if !found {
		// The row may be gone because an earlier attempt removed it and
		// failed to settle the sessions. Settling here finishes that work;
		// the route answers the not-found code and carries the rotated
		// cookie the client needs to stay signed in.
		return issued, authn.ErrFactorNotEnrolled
	}
	return issued, nil
}

// settleSessions ends the account's other sessions and rotates the one named by
// keep, returning the token the caller writes as the response's cookie. An empty
// keep leaves RevokeAccount to end every session, as it did before rotation
// existed, and nothing is rotated. change names what happened, for the log line a
// module with no session store writes: the change stands and the sessions stay
// valid.
//
// The rotation follows the revocation, and a revocation that fails returns
// without one: a rotation whose new token the client cannot receive in a failed
// answer would sign the device out while leaving the failure unfixed.
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

// revokeSessions ends the account's sessions, keeping the one named by keep when
// it is not empty, under reason. change names what happened, for the log line a
// module with no session store writes: the change stands and the sessions stay
// valid.
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

// guardLast refuses to remove the last active factor of an account that requires
// one. Whether the account requires one is read from the account, and a read that
// fails is an outage, since the removal is not known to be allowed.
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

// removeKind has the factor of the row's kind remove what it keeps for the row. A
// kind with no implementation here has no one to ask, which is said in the log
// and is no reason to leave the row: the data stays with the module that is not
// taken in.
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

// Reset makes the account register its factors again, in an order that leaves a
// failure at any step with the account stricter and never looser:
//
//  1. mfa_required is set on the account;
//  2. every one of the account's factors is disabled, in one transaction;
//  3. the account's sessions are revoked, when a session module is taken in;
//  4. the change is published, after the transaction has committed.
//
// Any step that fails ends the call with its error, and the call can be made
// again: setting the flag, disabling a disabled row and revoking an empty set
// are all no-ops. removed is how many rows this call disabled, so a repeat that
// finds none reports zero. The authorization and the check of the strength of
// whoever asked are the administration handler's.
//
// The reason is not recorded. A free-text reason is the one value the event and
// the audit detail may not carry, and the sessions are revoked under the
// factor-reset class whatever it says.
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

// flagged disables the factor a clone warning came from and announces the change.
// The factor already refuses the flagged credential; this takes its row out of
// the account's factors and publishes authn.factor.changed with flagged, the way
// a reset does for every row. A failure to disable the row is an Error and the
// event is published anyway, because a client holding a clone must not keep a
// usable factor row.
func (m *product) flagged(ctx context.Context, rec authn.FactorRecord) {
	if err := m.register.disable(ctx, rec.Account, rec.ID); err != nil {
		m.logger.Error(ModuleName+": a flagged factor could not be disabled",
			slog.String("account", string(rec.Account)), slog.String("factor", rec.ID),
			slog.String("cause", err.Error()))
	}
	m.publishChanged(ctx, rec, changeFlagged, byClassSystem)
}

// Seasoned reports whether the account has an active factor of a kind other than
// SMS that has been active for at least sensitive-min-age. It reads the register
// and no session: the highest-privilege actions ask this of whoever is acting,
// and a factor registered during the session that asks does not count yet.
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

// AccountCreated changes nothing: a new account has no factor, and a factor is
// enrolled only when its owner asks for one. The hook is idempotent and returns
// nil.
func (m *product) AccountCreated(context.Context, authn.AccountID) error { return nil }

// AccountDisabled leaves the account's factors where they are: a disabled account
// that is enabled again has the factors it had.
func (m *product) AccountDisabled(context.Context, authn.AccountID) error { return nil }

// AccountEnabled changes nothing: a disable kept the factors, and the enabled
// account uses the same ones again.
func (m *product) AccountEnabled(context.Context, authn.AccountID) error { return nil }

// ServiceSecretRevoked has nothing to do: a service secret is not a factor.
func (m *product) ServiceSecretRevoked(context.Context, authn.AccountID, string) error { return nil }

// AccountDeleted removes everything the module holds for the account: each factor
// has its kind-specific data removed and then its row. A factor whose removal
// fails keeps its row, so that the same call repeated, or the event that follows
// the deletion, finishes the work; every failure is returned together once all
// the rows have been tried.
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
