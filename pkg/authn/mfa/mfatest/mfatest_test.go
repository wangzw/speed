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

// rootKey is a root key for the field encryption of the test handles. It is
// longer than the floor and is not a secret of any deployment.
const rootKey = "mfatest-root-key-0123456789-abcdefghijklmnop"

// account is the account the cases write factors for.
const account = authn.AccountID("acct-mfatest")

// totpSeed is the base32 seed the cases write, from the RFC 6238 test vectors.
const totpSeed = "JBSWY3DPEHPK3PXP"

// open delivers a real database handle with the encryption key set and the
// factor register's table built from the module's own migration. The TOTP seed
// table is built only when withSeedTable is true, which is how a case reaches a
// seed write that fails after the factor row is written.
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

// applyMigration runs the statements of one of the modules' migration files, so
// the tables a case writes are the ones the modules ship.
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

// TestASeededFactorIsActiveAtTheInstantGiven writes an active factor and holds
// both halves of that: the register reads it back active, and its activation
// instant is the one asked for rather than the moment of the write.
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

// TestATOTPFactorBackdatedTwoHoursIsSeasoned is the acceptance the impersonation
// journey rests on: a non-SMS factor activated two hours ago is seasoned at a
// one-hour minimum age.
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

// TestATOTPFactorActivatedNowIsNotSeasoned holds the direction the minimum age
// is for: a factor activated at the moment of the write is not seasoned at a
// one-hour minimum age, so the backdated case above rests on the instant the
// fixture chose and not on the write itself.
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

// TestAnSMSFactorAloneIsNotSeasoned holds the other direction: the SMS kind does
// not count toward the seasoned answer, however old it is.
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

// TestTheTOTPSeedReadsBackAsWritten holds the encrypted round trip: the seed a
// case writes is the seed TOTPSecret answers, read through the serializer.
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

// TestASeedWriteThatFailsLeavesNoFactorRow holds the rollback: with no seed
// table, the write fails and the factor row it wrote first is taken back out.
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

// TestAnEmptySeedIsRefused holds the input check: an empty secret is an error
// and writes no factor row at all.
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
