package account

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

const reasonClassThrottled = authn.ReasonThrottled

func validScope(scope authn.ThrottleScope) bool {
	switch scope {
	case authn.ScopeIdentifier, authn.ScopeAccount, authn.ScopeFactor, authn.ScopePhone, authn.ScopeClient,
		authn.ScopeSignupUsername:
		return true
	}
	return false
}

func badScope(scope authn.ThrottleScope) error {
	return fmt.Errorf("%w: %q is not a throttle scope", authn.ErrInvalidArgument, string(scope))
}

// keyHash keeps the table from holding a key as it was handed over.
func keyHash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// Check reports how long the key still has to wait, zero for now. A state that
// cannot be read is an error and never a zero, so a caller that cannot tell does
// not verify.
func (p *product) Check(ctx context.Context, scope authn.ThrottleScope, key string) (time.Duration, error) {
	if !validScope(scope) {
		return 0, badScope(scope)
	}
	var rows []struct{ LockedUntil *time.Time }
	err := p.conn(ctx).Raw("SELECT locked_until FROM "+tableThrottles+" WHERE scope = ? AND key_hash = ?",
		string(scope), keyHash(key)).Scan(&rows).Error
	if err != nil {
		return 0, failed("reading a failure count", err)
	}
	if len(rows) == 0 || rows[0].LockedUntil == nil {
		return 0, nil
	}
	return max(0, timeOf(rows[0].LockedUntil).Sub(p.now.Now())), nil
}

// Fail records one failed attempt; engaged reports that this one started the
// back-off. The count moves in one atomic statement on both engines.
func (p *product) Fail(ctx context.Context, scope authn.ThrottleScope, key string) (bool, error) {
	if !validScope(scope) {
		return false, badScope(scope)
	}
	params := p.throttleParams()
	now := p.now.Now().Truncate(time.Microsecond)
	horizon := now.Add(-params.decay)
	hash := keyHash(key)

	const lapsed = "(" + tableThrottles + ".last_failure_at < ? AND (" +
		tableThrottles + ".locked_until IS NULL OR " + tableThrottles + ".locked_until <= ?))"
	statement := "INSERT INTO " + tableThrottles +
		" (scope, key_hash, failures, first_failure_at, last_failure_at, locked_until) VALUES (?, ?, 1, ?, ?, NULL)" +
		" ON CONFLICT (scope, key_hash) DO UPDATE SET" +
		" failures = CASE WHEN " + lapsed + " THEN 1 ELSE " + tableThrottles + ".failures + 1 END," +
		" first_failure_at = CASE WHEN " + lapsed + " THEN EXCLUDED.first_failure_at ELSE " + tableThrottles + ".first_failure_at END," +
		" locked_until = CASE WHEN " + lapsed + " THEN NULL ELSE " + tableThrottles + ".locked_until END," +
		" last_failure_at = EXCLUDED.last_failure_at" +
		" RETURNING failures"
	var failures int
	err := p.conn(ctx).Raw(statement, string(scope), hash, now, now,
		horizon, now, horizon, now, horizon, now).Row().Scan(&failures)
	if err != nil {
		return false, failed("counting a failed attempt", err)
	}

	if wait := params.delay(failures); wait > 0 {
		until := now.Add(wait)
		err = p.conn(ctx).Exec("UPDATE "+tableThrottles+" SET locked_until = ? WHERE scope = ? AND key_hash = ?"+
			" AND (locked_until IS NULL OR locked_until < ?)", until, string(scope), hash, until).Error
		if err != nil {
			return false, failed("writing a back-off", err)
		}
	}
	engaged := failures == params.free+1
	if engaged {
		p.logger.Info(ModuleName+": a key has failed enough attempts to be held back; further attempts are "+
			"refused without a check until its back-off ends",
			slog.String("scope", string(scope)), slog.String("reason_class", reasonClassThrottled))
	}
	return engaged, nil
}

func (p *product) Succeed(ctx context.Context, scope authn.ThrottleScope, key string) error {
	if !validScope(scope) {
		return badScope(scope)
	}
	err := p.conn(ctx).Exec("DELETE FROM "+tableThrottles+" WHERE scope = ? AND key_hash = ?",
		string(scope), keyHash(key)).Error
	return failed("clearing a failure count", err)
}
