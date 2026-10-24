//go:build review

package mfa

// Review-only fixture seams for the demo's review assembly.
//
// The review build seeds an operator's second factors so the impersonation
// journey can be walked without hand-enrolling them in a browser first. The
// factor row is written by the register itself (insertPending and the same
// activation statement the product's Enroll runs), and the reads below are the
// register's own statements, so a review case that reads them catches a rename
// of a column or a status the register no longer writes.
//
// The activation instant is the fixture's choice: Enroll activates at the
// current time, and the impersonation journey needs a factor that has been
// active past authn.mfa.sensitive-min-age. The fixture therefore hands the
// register's own activation statement a backdated instant. A kind's own secret
// is never written here: totp.ReviewSeedFactor writes it through that module's
// encrypted seed register.

import (
	"context"
	"fmt"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/clock"
	"github.com/vislake/speed/pkg/db"
)

// ReviewSeedActiveFactor writes an active factor row of kind for account, with
// activated_at and created_at set to at, and answers its identifier. It is a
// review-only fixture helper: the row goes through the register's own
// insertPending and activate statements, at the instant the fixture chooses,
// not through raw SQL of its own. The caller writes the kind's own data (a TOTP
// seed, for instance) against the returned identifier.
func ReviewSeedActiveFactor(ctx context.Context, database db.Database, account authn.AccountID,
	kind authn.FactorKind, at time.Time,
) (string, error) {
	register := &register{handle: database.DB()}
	rec, err := register.insertPending(ctx, account, kind, defaults.MaxPerKind, clock.UTC(at))
	if err != nil {
		return "", fmt.Errorf("review fixture: writing a pending %s factor: %w", kind, err)
	}
	won, err := register.activate(ctx, account, rec.ID, at)
	if err != nil {
		return "", fmt.Errorf("review fixture: activating the %s factor %s: %w", kind, rec.ID, err)
	}
	if !won {
		return "", fmt.Errorf("review fixture: the pending %s factor %s was not there to activate",
			kind, rec.ID)
	}
	return rec.ID, nil
}

// ReviewRemoveFactor removes one factor row of the account, so a seeding step
// that fails after the row is written leaves nothing behind. It is idempotent
// and answers no error for a row that is already gone.
func ReviewRemoveFactor(ctx context.Context, database db.Database, account authn.AccountID,
	factorID string,
) error {
	if _, err := (&register{handle: database.DB()}).remove(ctx, account, factorID); err != nil {
		return fmt.Errorf("review fixture: removing the factor %s: %w", factorID, err)
	}
	return nil
}

// ReviewFactors lists the account's factors, every status, through the
// register's own read.
func ReviewFactors(ctx context.Context, database db.Database,
	account authn.AccountID,
) ([]authn.FactorRecord, error) {
	recs, err := (&register{handle: database.DB()}).list(ctx, account, -1)
	if err != nil {
		return nil, fmt.Errorf("review fixture: listing the factors: %w", err)
	}
	return recs, nil
}

// ReviewSeasoned reports the register's seasoned answer for the account: whether
// it holds an active non-SMS factor activated at or before now minus minAge. It
// is the same statement the impersonation gate reads through the multi-factor
// capability, so a case that asserts it is asserting the product's own
// judgement and not a second copy of the query.
func ReviewSeasoned(ctx context.Context, database db.Database, account authn.AccountID,
	minAge time.Duration,
) (bool, error) {
	cutoff := clock.UTC(time.Now().Add(-minAge))
	seasoned, err := (&register{handle: database.DB()}).seasoned(ctx, account, cutoff)
	if err != nil {
		return false, fmt.Errorf("review fixture: reading the factor age: %w", err)
	}
	return seasoned, nil
}
