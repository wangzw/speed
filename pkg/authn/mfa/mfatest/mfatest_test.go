package mfatest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/mfa/mfatest"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/internal/dbtest"
)

const rootKey = "mfatest-root-key-0123456789-abcdefghijklmnop"

const account = authn.AccountID("acct-mfatest")

const totpSeed = "JBSWY3DPEHPK3PXP"

func open(t *testing.T, withSeedTable bool) db.Database {
	t.Helper()
	database := dbtest.SQLite(t, filepath.Join(t.TempDir(), "mfatest.db"),
		dbtest.Items{"encryption-key": rootKey})
	applyMigration(t, database, "../migrations/sqlite/0001_authn_factors.sql")
	if withSeedTable {
		applyMigration(t, database, "../totp/migrations/sqlite/0001_authn_totp_secrets.sql")
	}
	return database
}

func applyMigration(t *testing.T, database db.Database, path string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	for _, statement := range strings.Split(string(raw), ";") {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if err := database.DB().Exec(statement).Error; err != nil {
			t.Fatalf("applying %s: %v", path, err)
		}
	}
}

// TestASeededFactorIsActiveAtTheInstantGiven checks that a seeded factor is active at the activation instant given.
func TestASeededFactorIsActiveAtTheInstantGiven(t *testing.T) {
	ctx := t.Context()
	database := open(t, true)
	activatedAt := time.Now().Add(-2 * time.Hour)

	factorID, err := mfatest.SeedActiveFactor(ctx, database, account, authn.FactorSMS, activatedAt)
	if err != nil {
		t.Fatalf("seeding the factor: %v", err)
	}
	factors, err := mfatest.Factors(ctx, database, account)
	if err != nil {
		t.Fatalf("listing the factors: %v", err)
	}
	if len(factors) != 1 {
		t.Fatalf("the account holds %d factors, want the one written: %+v", len(factors), factors)
	}
	got := factors[0]
	if got.ID != factorID || got.Kind != authn.FactorSMS || got.Status != authn.FactorActive {
		t.Errorf("the seeded factor reads as %+v, want %s active of kind %s", got, factorID, authn.FactorSMS)
	}
	if !got.ActivatedAt.Equal(activatedAt) {
		t.Errorf("the factor was activated at %s, want the instant given, %s", got.ActivatedAt, activatedAt)
	}
}

// TestATOTPFactorBackdatedTwoHoursIsSeasoned checks that a non-SMS factor activated two hours ago is seasoned at a one-hour minimum.
func TestATOTPFactorBackdatedTwoHoursIsSeasoned(t *testing.T) {
	ctx := t.Context()
	database := open(t, true)

	if _, err := mfatest.SeedTOTPFactor(ctx, database, account, time.Now().Add(-2*time.Hour), totpSeed); err != nil {
		t.Fatalf("seeding the TOTP factor: %v", err)
	}
	seasoned, err := mfatest.Seasoned(ctx, database, account, time.Hour)
	if err != nil {
		t.Fatalf("reading the seasoned answer: %v", err)
	}
	if !seasoned {
		t.Error("a TOTP factor activated two hours ago is not seasoned at a one-hour minimum age")
	}
}

// TestATOTPFactorActivatedNowIsNotSeasoned checks that a factor activated now is not seasoned at a one-hour minimum.
func TestATOTPFactorActivatedNowIsNotSeasoned(t *testing.T) {
	ctx := t.Context()
	database := open(t, true)

	if _, err := mfatest.SeedTOTPFactor(ctx, database, account, time.Now(), totpSeed); err != nil {
		t.Fatalf("seeding the TOTP factor: %v", err)
	}
	seasoned, err := mfatest.Seasoned(ctx, database, account, time.Hour)
	if err != nil {
		t.Fatalf("reading the seasoned answer: %v", err)
	}
	if seasoned {
		t.Error("a TOTP factor activated now is seasoned at a one-hour minimum age")
	}
}

// TestAnSMSFactorAloneIsNotSeasoned checks that an SMS factor alone is never seasoned.
func TestAnSMSFactorAloneIsNotSeasoned(t *testing.T) {
	ctx := t.Context()
	database := open(t, true)

	if _, err := mfatest.SeedActiveFactor(ctx, database, account, authn.FactorSMS, time.Now().Add(-2*time.Hour)); err != nil {
		t.Fatalf("seeding the SMS factor: %v", err)
	}
	seasoned, err := mfatest.Seasoned(ctx, database, account, time.Hour)
	if err != nil {
		t.Fatalf("reading the seasoned answer: %v", err)
	}
	if seasoned {
		t.Error("an SMS factor alone is seasoned, and the impersonation gate is supposed to need a non-SMS one")
	}
}

// TestTheTOTPSeedReadsBackAsWritten checks that a TOTP seed reads back as written through the encrypted model.
func TestTheTOTPSeedReadsBackAsWritten(t *testing.T) {
	ctx := t.Context()
	database := open(t, true)

	factorID, err := mfatest.SeedTOTPFactor(ctx, database, account, time.Now().Add(-2*time.Hour), totpSeed)
	if err != nil {
		t.Fatalf("seeding the TOTP factor: %v", err)
	}
	secret, found, err := mfatest.TOTPSecret(ctx, database, factorID)
	if err != nil {
		t.Fatalf("reading the seed: %v", err)
	}
	if !found {
		t.Fatalf("the seeded factor %s holds no seed row", factorID)
	}
	if secret != totpSeed {
		t.Errorf("the seed reads back as %q, want %q", secret, totpSeed)
	}
}

// TestASeedWriteThatFailsLeavesNoFactorRow checks that a failed seed write leaves no factor row.
func TestASeedWriteThatFailsLeavesNoFactorRow(t *testing.T) {
	ctx := t.Context()
	database := open(t, false)

	if _, err := mfatest.SeedTOTPFactor(ctx, database, account, time.Now().Add(-2*time.Hour), totpSeed); err == nil {
		t.Fatal("a seed write with no seed table succeeded")
	}
	factors, err := mfatest.Factors(ctx, database, account)
	if err != nil {
		t.Fatalf("listing the factors: %v", err)
	}
	if len(factors) != 0 {
		t.Errorf("a failed seed left %d factor rows behind: %+v", len(factors), factors)
	}
}

// TestAnEmptySeedIsRefused checks that an empty seed is refused and writes no row.
func TestAnEmptySeedIsRefused(t *testing.T) {
	ctx := t.Context()
	database := open(t, true)

	if _, err := mfatest.SeedTOTPFactor(ctx, database, account, time.Now(), ""); err == nil {
		t.Fatal("an empty TOTP seed was accepted")
	}
	factors, err := mfatest.Factors(ctx, database, account)
	if err != nil {
		t.Fatalf("listing the factors: %v", err)
	}
	if len(factors) != 0 {
		t.Errorf("an empty seed wrote %d factor rows", len(factors))
	}
}
