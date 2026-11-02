package mfa

import (
	"io/fs"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"gorm.io/gorm"
)

// TestTheSingleAuthenticatorMigrationKeepsTheNewestActiveRow holds the one-off
// convergence: an account that already holds several active authenticator rows
// keeps the most recently activated one, every other active one becomes
// disabled and stays in the register, and a second run changes nothing.
func TestTheSingleAuthenticatorMigrationKeepsTheNewestActiveRow(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, dialect string) {
		r := newRig(t, h, newFactor(authn.FactorTOTP), newFactor(authn.FactorWebAuthn))
		older := base.Add(-2 * time.Hour)
		newer := base.Add(-time.Hour)
		oldID := r.insertRow(acct, authn.FactorTOTP, authn.FactorActive, older, &older)
		newID := r.insertRow(acct, authn.FactorTOTP, authn.FactorActive, newer, &newer)
		disabledID := r.insertRow(acct, authn.FactorTOTP, authn.FactorDisabled, older, &older)
		pendingID := r.insertRow(acct, authn.FactorTOTP, authn.FactorPending, newer, nil)
		passkeyID := r.insertRow(acct, authn.FactorWebAuthn, authn.FactorActive, older, &older)
		// Another account keeps its own newest row: the statement groups by
		// account, not by the table.
		otherOld := r.insertRow("other", authn.FactorTOTP, authn.FactorActive, older, &older)
		otherNew := r.insertRow("other", authn.FactorTOTP, authn.FactorActive, newer, &newer)

		script, err := fs.ReadFile(migrationsFS, dialect+"/0002_totp_single.sql")
		if err != nil {
			t.Fatalf("reading the migration: %v", err)
		}
		if err := h.Exec(string(script)).Error; err != nil {
			t.Fatalf("running the migration: %v", err)
		}

		want := map[string]authn.FactorStatus{
			oldID:      authn.FactorDisabled,
			newID:      authn.FactorActive,
			disabledID: authn.FactorDisabled,
			pendingID:  authn.FactorPending,
			passkeyID:  authn.FactorActive,
			otherOld:   authn.FactorDisabled,
			otherNew:   authn.FactorActive,
		}
		for id, wantStatus := range want {
			if got, there := r.status(id); !there || got != wantStatus {
				t.Errorf("the row %s reads %v (there %v), want %v", id, got, there, wantStatus)
			}
		}

		// Idempotent: running it again finds nothing to retire.
		if err := h.Exec(string(script)).Error; err != nil {
			t.Fatalf("running the migration a second time: %v", err)
		}
		for id, wantStatus := range want {
			if got, there := r.status(id); !there || got != wantStatus {
				t.Errorf("after the second run the row %s reads %v (there %v), want %v", id, got, there, wantStatus)
			}
		}
	})
}
