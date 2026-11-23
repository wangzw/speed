// Package mfatest writes and reads the multi-factor register for a test.
//
// The impersonation journey needs an operator whose non-SMS factor has been
// active past authn.mfa.sensitive-min-age, whose floor is one hour. A factor
// enrolled through the product is activated now, so a test that needs a seasoned
// one writes the row with a backdated activation instant. This package is that
// write, and the reads a test asserts it with, exposed without a build tag and
// without adding anything to the multi-factor module's own API.
//
// The statements are the module's: the factor rows go through
// pkg/authn/mfa/internal/factorstore and the TOTP seed through
// pkg/authn/mfa/internal/totpseed, so a renamed column or a status the register
// no longer writes breaks a case here rather than passing silently. The seed is
// sealed by the same encrypted model an enrollment uses and under the key the
// database handle was assembled with.
//
// Only test code and the end-to-end harness may import this package. Package
// authn's dependency test fails a production import.
package mfatest

import (
	"context"
	"fmt"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/clock"
	"github.com/vislake/speed/pkg/authn/mfa/internal/factorstore"
	"github.com/vislake/speed/pkg/authn/mfa/internal/totpseed"
	"github.com/vislake/speed/pkg/db"
)

// seedLimit is the per-kind cap a fixture writes under. A test does not
// exercise the limit, which the multi-factor module enforces on a registration;
// the value is high enough that a fixture never meets it and the statement still
// counts what the product counts.
const seedLimit = 1 << 20

// SeedActiveFactor writes an active factor of kind for account, activated at
// activatedAt, and answers its identifier. The row is written by the register's
// own pending insert and activation statement, at the instant the caller
// chooses, so a factor can be older than an enrollment could make it. A kind's
// own data, the TOTP seed for instance, is not written here.
func SeedActiveFactor(ctx context.Context, database db.Database, account authn.AccountID,
	kind authn.FactorKind, activatedAt time.Time,
) (factorID string, err error) {
	store := factorstore.New(database.DB())
	rec, err := store.InsertPending(ctx, account, kind, "", seedLimit, false, clock.UTC(activatedAt))
	if err != nil {
		return "", fmt.Errorf("mfatest: writing a pending %s factor: %w", kind, err)
	}
	won, err := store.Activate(ctx, account, rec.ID, activatedAt)
	if err != nil {
		return "", fmt.Errorf("mfatest: activating the %s factor %s: %w", kind, rec.ID, err)
	}
	if !won {
		return "", fmt.Errorf("mfatest: the pending %s factor %s was not there to activate", kind, rec.ID)
	}
	return rec.ID, nil
}

// SeedTOTPFactor writes an active TOTP factor activated at activatedAt, with
// secret (base32) as its seed, and answers the factor's identifier. The seed is
// written through the encrypted model, with the factor's fixed parameters, so a
// code computed from secret verifies through the ordinary login path. A write of
// the seed that fails takes the factor row back out, so a failed call leaves
// nothing behind.
func SeedTOTPFactor(ctx context.Context, database db.Database, account authn.AccountID,
	activatedAt time.Time, secret string,
) (factorID string, err error) {
	if secret == "" {
		return "", fmt.Errorf("mfatest: a TOTP seed is empty")
	}
	factorID, err = SeedActiveFactor(ctx, database, account, authn.FactorTOTP, activatedAt)
	if err != nil {
		return "", err
	}
	record := totpseed.Seed{
		FactorID:    factorID,
		Secret:      secret,
		Algorithm:   totpseed.AlgorithmSHA1,
		Digits:      totpseed.DigitsOfSix,
		Period:      totpseed.PeriodSeconds,
		LastCounter: 0,
	}
	if err := totpseed.New(database.DB()).Insert(ctx, record); err != nil {
		if _, removeErr := factorstore.New(database.DB()).Remove(ctx, account, factorID); removeErr != nil {
			return "", fmt.Errorf("mfatest: writing the TOTP seed: %w (and the factor row %s stayed: %w)",
				err, factorID, removeErr)
		}
		return "", fmt.Errorf("mfatest: writing the TOTP seed: %w", err)
	}
	return factorID, nil
}

// Factors lists the account's factors, every status, through the register's own
// read.
func Factors(ctx context.Context, database db.Database, account authn.AccountID) ([]authn.FactorRecord, error) {
	recs, err := factorstore.New(database.DB()).List(ctx, account, -1)
	if err != nil {
		return nil, fmt.Errorf("mfatest: listing the factors: %w", err)
	}
	return recs, nil
}

// Seasoned reports whether the account has a non-SMS active factor older than
// minAge. It is the register's own seasoned answer, the statement the
// impersonation gate reads, not a second copy of the query.
func Seasoned(ctx context.Context, database db.Database, account authn.AccountID, minAge time.Duration) (bool, error) {
	cutoff := clock.UTC(time.Now().Add(-minAge))
	seasoned, err := factorstore.New(database.DB()).Seasoned(ctx, account, cutoff)
	if err != nil {
		return false, fmt.Errorf("mfatest: reading the factor age: %w", err)
	}
	return seasoned, nil
}

// TOTPSecret reads back a TOTP factor's seed through the encrypted model, so the
// serializer opens it. It answers false for a factor with no seed row.
func TOTPSecret(ctx context.Context, database db.Database, factorID string) (secret string, found bool, err error) {
	record, found, err := totpseed.New(database.DB()).Load(ctx, factorID)
	if err != nil {
		return "", false, fmt.Errorf("mfatest: reading the TOTP seed: %w", err)
	}
	if !found {
		return "", false, nil
	}
	return record.Secret, true, nil
}
