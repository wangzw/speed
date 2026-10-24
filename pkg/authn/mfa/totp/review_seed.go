//go:build review

package totp

// Review-only fixture seams for the demo's review assembly.
//
// ReviewSeedFactor registers an active TOTP factor through the multi-factor
// register and writes the seed it is given through this module's own encrypted
// seed register: the secret goes into the column carrying serializer:encrypted,
// exactly as a browser enrollment would write it. ReviewSecret reads it back
// through the same model, so a case that computes a code from the answer is
// exercising the encryption seam and not a copy of the table. The secret is a
// parameter because the demo's walkthrough publishes a fixture secret a person
// can import into an authenticator; it is a review fixture and not a credential.
//
// Neither function exists in a default build. They are named as review fixtures
// so that the seeding path in the demo cannot be mistaken for a product API.

import (
	"context"
	"fmt"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/mfa"
	"github.com/vislake/speed/pkg/db"
)

// ReviewSeedFactor grants the account an active TOTP factor activated at at and
// writes secret as its seed through the encrypted column. It answers the
// factor's identifier. The row records the factor's fixed parameters — SHA-1,
// six digits, a thirty-second step — so a code computed from the secret verifies
// through the ordinary login path.
func ReviewSeedFactor(ctx context.Context, database db.Database, account authn.AccountID,
	at time.Time, secret string,
) (string, error) {
	if secret == "" {
		return "", fmt.Errorf("review fixture: a TOTP seed is empty")
	}
	factorID, err := mfa.ReviewSeedActiveFactor(ctx, database, account, authn.FactorTOTP, at)
	if err != nil {
		return "", err
	}
	record := seed{
		FactorID:    factorID,
		Secret:      secret,
		Algorithm:   algorithmSHA1,
		Digits:      digitsOfSix,
		Period:      periodSeconds,
		LastCounter: 0,
	}
	if err := (&register{handle: database.DB()}).insert(ctx, record); err != nil {
		_ = mfa.ReviewRemoveFactor(ctx, database, account, factorID)
		return "", fmt.Errorf("review fixture: writing the TOTP seed: %w", err)
	}
	return factorID, nil
}

// ReviewSecret reads a factor's seed back through the model, so the encrypted
// serializer opens it. It answers false for a factor with no seed row.
func ReviewSecret(ctx context.Context, database db.Database, factorID string) (string, bool, error) {
	record, found, err := (&register{handle: database.DB()}).load(ctx, factorID)
	if err != nil {
		return "", false, fmt.Errorf("review fixture: reading the TOTP seed: %w", err)
	}
	if !found {
		return "", false, nil
	}
	return record.Secret, true, nil
}
