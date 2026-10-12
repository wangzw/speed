package sms

import (
	"context"
	"crypto/subtle"

	"github.com/vislake/speed/pkg/authn"
)

// Verify counts one attempt on the challenge, compares the code against the
// keyed digest and, on a match, marks the row consumed with a conditional
// update. Every case that does not pass is ErrInvalidCredentials: a missing row,
// an expired or spent one, a row whose attempts are exhausted and a suppressed
// row are told apart by nothing.
//
// The attempt is counted before the code is compared, so a concurrent submitter
// cannot get past the limit: the conditional update is the gate, and a row
// already at its limit is not incremented and cannot pass. A suppressed row is
// counted and compared like any other and never passes, so an unregistered
// number costs the same as a registered one.
//
// The comparison runs on every path: when the count did not happen or the row is
// not there, the digest is the decoy and the result is a refusal, so no path is
// the fast one by skipping the comparison. The result is always folded into the
// match that decides the answer, so the comparison's work is not optimized away
// as dead.
func (p *product) Verify(ctx context.Context, challengeID, code string) error {
	now := p.clock.Now()
	candidate := p.database.BlindIndex(challengeID + "|" + code)

	counted, err := p.register.increment(ctx, challengeID, now)
	if err != nil {
		return p.unavailable(err)
	}
	// A row that is missing, expired, spent or out of attempts keeps the default
	// here: its digest is the decoy and it is suppressed, so the comparison below
	// rejects it exactly as it rejects a suppressed row.
	row := challengeRow{CodeMAC: decoyMAC, Suppressed: true}
	if counted {
		read, found, readErr := p.register.read(ctx, challengeID)
		if readErr != nil {
			return p.unavailable(readErr)
		}
		if found {
			row = read
		}
	}

	matched := subtle.ConstantTimeCompare([]byte(candidate), []byte(row.CodeMAC)) == 1
	if row.Suppressed {
		matched = false
	}
	if !matched {
		return authn.ErrInvalidCredentials
	}

	consumed, err := p.register.consume(ctx, challengeID, now)
	if err != nil {
		return p.unavailable(err)
	}
	if !consumed {
		// Another submitter consumed the row between the count and here.
		return authn.ErrInvalidCredentials
	}
	return nil
}
