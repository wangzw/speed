package account

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/password"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/settings"
)

// testPasswordAlgorithmKey is the setting this module carries for the algorithm
// a new hash is written with. It is written out here rather than read from a
// constant so that the case names the key the design lists.
const testPasswordAlgorithmKey = "authn.account.password-algorithm"

// storedPHC is the credential text a case reads straight from the table.
func storedPHC(t *testing.T, h *harness, id authn.AccountID) string {
	t.Helper()
	var row struct{ PHC string }
	if err := h.handle.Raw("SELECT phc FROM "+tableCredentials+" WHERE account_id = ?", id.String()).Scan(&row).Error; err != nil {
		t.Fatalf("reading the credential: %v", err)
	}
	return row.PHC
}

// TestAStoredPasswordAlgorithmShapesTheNextEnrollment holds the runtime read: a
// value written after the module started shapes the hash the next enrollment
// writes, and an entry already written with argon2id still verifies.
func TestAStoredPasswordAlgorithmShapesTheNextEnrollment(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		h.withSettings().set(testPasswordAlgorithmKey, string(password.PBKDF2SHA256))

		account, err := h.p.Create(ctx, authn.CreateAccount{
			Username: "enrolled", Email: "enrolled@example.com",
			EmailVerifiedVia: authn.Verification{Via: authn.VerifiedViaAdmin},
			Via:              "admin",
			Password:         "correct horse battery",
		})
		if err != nil {
			t.Fatalf("enrolling an account: %v", err)
		}
		entry, err := password.Parse(storedPHC(t, h, account.ID))
		if err != nil {
			t.Fatalf("reading the written entry: %v", err)
		}
		if entry.Params().Algorithm != password.PBKDF2SHA256 {
			t.Errorf("the enrollment was written with %s, want the stored %s",
				entry.Params().Algorithm, password.PBKDF2SHA256)
		}

		// An entry written with argon2id before the move still verifies.
		id := h.user("argon", "correct horse battery")
		if arg := storedPHC(t, h, id); !strings.HasPrefix(arg, "$argon2id$") {
			t.Fatalf("the existing entry is %q, want an argon2id one", arg)
		}
		if _, _, err := h.p.Verify(ctx, "argon", "correct horse battery"); err != nil {
			t.Errorf("an existing argon2id entry no longer verifies: %v", err)
		}
	})
}

// TestAPasswordWriteRefusesAnUnusableAlgorithm holds that an algorithm that
// cannot be read is not replaced by another one: a write fails instead of
// silently falling back to the declared algorithm.
func TestAPasswordWriteRefusesAnUnusableAlgorithm(t *testing.T) {
	for _, c := range []struct {
		name      string
		user      string
		configure func(*fakeSettings)
	}{
		{"the read fails", "refused-read", func(s *fakeSettings) {
			s.failOn(testPasswordAlgorithmKey, errors.New("the store is down"))
		}},
		{"the value is out of domain", "refused-domain", func(s *fakeSettings) {
			s.set(testPasswordAlgorithmKey, "scrypt")
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			forEachDialect(t, func(t *testing.T, h *harness) {
				id := h.user(c.user, "")
				c.configure(h.withSettings())
				err := h.p.Set(context.Background(), id, "correct horse battery", authn.SetPasswordOptions{})
				if !errors.Is(err, authn.ErrUnavailable) {
					t.Errorf("a password write with an unusable algorithm setting gave %v, want ErrUnavailable", err)
				}
			})
		})
	}
}

// TestAnEnrollmentRefusesAnUnusableAlgorithm holds the other hash write: an
// enrollment whose algorithm cannot be read answers unavailable rather than
// writing with the declared algorithm.
func TestAnEnrollmentRefusesAnUnusableAlgorithm(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		h.withSettings().failOn(testPasswordAlgorithmKey, errors.New("the store is down"))

		_, err := h.p.Create(ctx, authn.CreateAccount{
			Username: "enroll-refused", Email: "enroll-refused@example.com",
			EmailVerifiedVia: authn.Verification{Via: authn.VerifiedViaAdmin},
			Via:              "admin",
			Password:         "correct horse battery",
		})
		if !errors.Is(err, authn.ErrUnavailable) {
			t.Errorf("an enrollment with an unusable algorithm setting gave %v, want ErrUnavailable", err)
		}
	})
}

// TestAnUndeclaredAlgorithmKeyIsReported holds that a key the store answers as
// undeclared is reported rather than silently falling back: the store reserves
// ErrUnknownKey for a key no declaration covers, which is a defect in this
// module's own schema. The write proceeds on the declared algorithm, and one
// Error names the setting so the defect is visible. The fake store answers a key
// with no value as ErrUnknownKey, which is how this case reaches that direction.
func TestAnUndeclaredAlgorithmKeyIsReported(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		id := h.user("undeclared", "")
		h.withSettings()
		if err := h.p.Set(context.Background(), id, "correct horse battery", authn.SetPasswordOptions{}); err != nil {
			t.Fatalf("a password write with an undeclared algorithm setting failed: %v", err)
		}
		if lines := h.logs.lines("level=ERROR", "setting="+testPasswordAlgorithmKey); len(lines) != 1 {
			t.Errorf("the log holds %d Error lines naming %s, want one:\n%s",
				len(lines), testPasswordAlgorithmKey, h.logs.String())
		}
	})
}

// TestThePostLoginUpgradeIsSkippedWhenTheAlgorithmCannotBeRead holds the second
// hash write: a login that would rewrite a weak entry leaves it alone and says
// so at Error once, and the login itself still succeeds.
func TestThePostLoginUpgradeIsSkippedWhenTheAlgorithmCannotBeRead(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("upgrade", "")
		if err := h.p.ImportHash(ctx, id, cheapHash(t, "correct horse battery"), false); err != nil {
			t.Fatalf("importing a weak entry: %v", err)
		}
		before := storedPHC(t, h, id)
		h.withSettings().failOn(testPasswordAlgorithmKey, errors.New("the store is down"))

		if _, _, err := h.p.Verify(ctx, "upgrade", "correct horse battery"); err != nil {
			t.Errorf("the login failed when the upgrade could not read the algorithm: %v", err)
		}
		if after := storedPHC(t, h, id); after != before {
			t.Errorf("the credential was rewritten (%q to %q) with an algorithm that could not be read", before, after)
		}
		if lines := h.logs.lines("level=ERROR", "setting="+testPasswordAlgorithmKey); len(lines) != 1 {
			t.Errorf("the log holds %d Error lines naming %s, want one:\n%s",
				len(lines), testPasswordAlgorithmKey, h.logs.String())
		}
	})
}

// TestMovingTheAlgorithmToPBKDF2WritesAWarnAndAnAuditRecord holds the visibility
// of a downgrade: the module's subscription answers the change with one Warn
// naming the key and the direction and never the value, and one best-effort
// audit record with the relaxed action. A move back to argon2id writes nothing.
func TestMovingTheAlgorithmToPBKDF2WritesAWarnAndAnAuditRecord(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		audit := h.withAudit()
		h.withSettings()

		deliverChange(t, h, settings.Change{
			Key: testPasswordAlgorithmKey, Old: string(password.Argon2id), New: string(password.PBKDF2SHA256),
		})

		logs := h.logs.String()
		if n := strings.Count(logs, "level=WARN"); n != 1 {
			t.Fatalf("the log holds %d Warn lines, want 1:\n%s", n, logs)
		}
		lines := h.logs.lines("level=WARN", "setting="+testPasswordAlgorithmKey)
		if len(lines) != 1 {
			t.Fatalf("the Warn line does not name the setting %s:\n%s", testPasswordAlgorithmKey, logs)
		}
		if !strings.Contains(lines[0], "direction=downgrade") {
			t.Errorf("the Warn line does not carry the direction downgrade:\n%s", lines[0])
		}
		if strings.Contains(lines[0], string(password.PBKDF2SHA256)) {
			t.Errorf("the Warn line names the value:\n%s", lines[0])
		}

		entries := audit.written()
		if len(entries) != 1 {
			t.Fatalf("the audit holds %d rows, want 1", len(entries))
		}
		if entries[0].Action != relaxedAction || entries[0].Object != testPasswordAlgorithmKey || entries[0].Outcome != relaxedOutcome {
			t.Errorf("audit row = %+v, want the relaxed action on %s", entries[0], testPasswordAlgorithmKey)
		}
		if got := entries[0].Detail["change"]; got != "downgrade" {
			t.Errorf("the audit detail change = %q, want downgrade", got)
		}

		// A move back to the stronger algorithm is not a downgrade.
		deliverChange(t, h, settings.Change{
			Key: testPasswordAlgorithmKey, Old: string(password.PBKDF2SHA256), New: string(password.Argon2id),
		})
		if len(audit.written()) != 1 {
			t.Errorf("a move back to argon2id wrote %d audit rows, want the one from the downgrade", len(audit.written()))
		}
		if n := strings.Count(h.logs.String(), "level=WARN"); n != 1 {
			t.Errorf("a move back to argon2id left %d Warn lines, want the one from the downgrade", n)
		}
	})
}

// TestAConfigurationCarryingTheRemovedAlgorithmFailsStartup holds the move out of
// configuration: a configuration file that still carries the algorithm is an
// unknown key and refuses the startup, naming the key.
func TestAConfigurationCarryingTheRemovedAlgorithmFailsStartup(t *testing.T) {
	body := `{"authn":{"account":{"password-algorithm":"pbkdf2-sha256"}}}`
	err := loadAccountConfiguration(t, body)
	if !errors.Is(err, config.ErrUnknownKey) {
		t.Fatalf("a configuration carrying %s loaded with %v, want config.ErrUnknownKey", testPasswordAlgorithmKey, err)
	}
	if !strings.Contains(err.Error(), testPasswordAlgorithmKey) {
		t.Errorf("the refusal %q does not name the key %s", err, testPasswordAlgorithmKey)
	}
}
