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

// reasonClassThrottled is the reason_class of the line that says a key has passed
// its free attempts. It is one of the closed values the events design registers
// for the field, and only a log line carries it.
const reasonClassThrottled = authn.ReasonThrottled

// validScope reports whether the scope is one of the five. The set is closed, and
// counters of different scopes never share a key space.
func validScope(scope authn.ThrottleScope) bool {
	switch scope {
	case authn.ScopeIdentifier, authn.ScopeAccount, authn.ScopeFactor, authn.ScopePhone, authn.ScopeClient:
		return true
	}
	return false
}

func badScope(scope authn.ThrottleScope) error {
	return fmt.Errorf("%w: %q is not a throttle scope", authn.ErrInvalidArgument, string(scope))
}

// keyHash is the form a key is kept in: the SHA-256 of the key as it was handed
// over, in hex. What the key holds is the caller's to normalize, and the table
// never holds it.
func keyHash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// Check reports how long the key still has to wait: zero means it may try now. A
// state that cannot be read is an error and never a zero, so that a caller that
// cannot tell does not verify.
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

// Fail records one failed attempt, and engaged says that this failure is the first
// one past the free attempts of the key, the one that started a back-off.
//
// The count moves in one statement that is atomic on both engines, so that
// failures that arrive together are all counted: the row is made, or its count
// is moved by one, or its count starts again at one when the window has gone by
// without a failure and the wait the earlier failures earned is over. The wait
// the new count earns is written after, and only ever later than the one already
// there, so the failure that has the highest count decides it however the
// statements of several failures interleave.
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

// Succeed clears the key's failures.
func (p *product) Succeed(ctx context.Context, scope authn.ThrottleScope, key string) error {
	if !validScope(scope) {
		return badScope(scope)
	}
	err := p.conn(ctx).Exec("DELETE FROM "+tableThrottles+" WHERE scope = ? AND key_hash = ?",
		string(scope), keyHash(key)).Error
	return failed("clearing a failure count", err)
}
