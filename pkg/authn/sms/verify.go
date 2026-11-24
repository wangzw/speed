package sms

import (
	"context"
	"crypto/subtle"

	"github.com/vislake/speed/pkg/authn"
)

// Verify counts one attempt, compares the code against the keyed digest, and marks the row consumed with a conditional update. Every case that does not pass is ErrInvalidCredentials, and the attempt is counted before the comparison so a concurrent submitter cannot pass the limit.
func (p *product) Verify(ctx context.Context, challengeID, code string) error {
	now := p.clock.Now()
	candidate := p.database.BlindIndex(challengeID + "|" + code)

	counted, err := p.register.increment(ctx, challengeID, now)
	if err != nil {
		return p.unavailable(err)
	}
	// A missing, expired, spent or exhausted row keeps the decoy digest and is suppressed, so the comparison rejects it like a suppressed row.
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
