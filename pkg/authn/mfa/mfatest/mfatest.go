// Package mfatest writes and reads the multi-factor register for a test, including a factor activated in the past and a sealed TOTP seed.
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

const seedLimit = 1 << 20

// SeedActiveFactor writes an active factor of kind for account at activatedAt and returns its identifier. It writes no kind-specific data.
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

// SeedTOTPFactor writes an active TOTP factor at activatedAt with the given base32 secret and returns its identifier; a failed seed write leaves no factor row.
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

// Factors lists the account's factors of every status.
func Factors(ctx context.Context, database db.Database, account authn.AccountID) ([]authn.FactorRecord, error) {
	recs, err := factorstore.New(database.DB()).List(ctx, account, -1)
	if err != nil {
		return nil, fmt.Errorf("mfatest: listing the factors: %w", err)
	}
	return recs, nil
}

// Seasoned reports whether the account has a non-SMS active factor older than minAge.
func Seasoned(ctx context.Context, database db.Database, account authn.AccountID, minAge time.Duration) (bool, error) {
	cutoff := clock.UTC(time.Now().Add(-minAge))
	seasoned, err := factorstore.New(database.DB()).Seasoned(ctx, account, cutoff)
	if err != nil {
		return false, fmt.Errorf("mfatest: reading the factor age: %w", err)
	}
	return seasoned, nil
}

// TOTPSecret reads a TOTP factor's seed through the encrypted model and reports false when there is none.
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
