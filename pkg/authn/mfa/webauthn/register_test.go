package webauthn

import (
	"context"
	"encoding/base64"
	"testing"

	"gorm.io/gorm"
)

func seedCredential(t *testing.T, r *rig, factorID string, signCount int64, cloneWarning bool) {
	t.Helper()
	err := r.p.register.insertCredential(context.Background(), credentialRow{
		FactorID:        factorID,
		RPID:            testRPID,
		CredentialID:    base64.RawURLEncoding.EncodeToString([]byte("cred-" + factorID)),
		PublicKey:       base64.RawURLEncoding.EncodeToString([]byte("pk-" + factorID)),
		AAGUID:          base64.RawURLEncoding.EncodeToString(make([]byte, 16)),
		AttestationType: "none",
		SignCount:       signCount,
		CloneWarning:    cloneWarning,
	})
	if err != nil {
		t.Fatalf("seeding a credential: %v", err)
	}
}

// TestWriteBackWinsOnlyAtOrAboveTheStoredCounter checks that the write-back wins only at or above the stored counter.
func TestWriteBackWinsOnlyAtOrAboveTheStoredCounter(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h)
		ctx := context.Background()
		seedCredential(t, r, "f1", 5, false)

		cases := []struct {
			presented int64
			want      bool
		}{
			{5, true}, {3, false}, {7, true},
		}
		for _, tc := range cases {
			won, err := r.p.register.writeBack(ctx, "f1", credentialRow{SignCount: tc.presented})
			if err != nil {
				t.Fatalf("writeBack(%d): %v", tc.presented, err)
			}
			if won != tc.want {
				t.Errorf("writeBack at counter %d over a stored 5 returned won=%v, want %v", tc.presented, won, tc.want)
			}
			if _, err := r.p.register.writeBack(ctx, "f1", credentialRow{SignCount: 5}); err != nil {
				t.Fatalf("resetting the counter: %v", err)
			}
		}
	})
}

// TestWriteBackStoresTheFlags checks that the write-back stores the assertion's flags.
func TestWriteBackStoresTheFlags(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h)
		ctx := context.Background()
		seedCredential(t, r, "f1", 1, false)
		won, err := r.p.register.writeBack(ctx, "f1", credentialRow{SignCount: 2, UserVerified: true, BackupState: true})
		if err != nil || !won {
			t.Fatalf("writeBack = %v, %v, want won", won, err)
		}
		row, found, err := r.p.register.getCredential(ctx, "f1")
		if err != nil || !found {
			t.Fatalf("getCredential: found=%v err=%v", found, err)
		}
		if !row.UserVerified || !row.BackupState || row.SignCount != 2 {
			t.Errorf("the row reads back as %+v, want the counter and flags written", row)
		}
	})
}

// TestFlagCloneSetsTheFlag checks that the clone flag is written independently of the counter.
func TestFlagCloneSetsTheFlag(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h)
		ctx := context.Background()
		seedCredential(t, r, "f1", 5, false)
		if err := r.p.register.flagClone(ctx, "f1"); err != nil {
			t.Fatalf("flagClone: %v", err)
		}
		row, found, err := r.p.register.getCredential(ctx, "f1")
		if err != nil || !found {
			t.Fatalf("getCredential: found=%v err=%v", found, err)
		}
		if !row.CloneWarning {
			t.Error("the credential was not flagged")
		}
		if row.SignCount != 5 {
			t.Errorf("the counter is %d, want it left at 5", row.SignCount)
		}
	})
}

// TestWriteBackDoesNotClearACloneFlag checks that a write-back without a warning does not clear a concurrent clone flag.
func TestWriteBackDoesNotClearACloneFlag(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h)
		ctx := context.Background()
		seedCredential(t, r, "f1", 5, false)
		if err := r.p.register.flagClone(ctx, "f1"); err != nil {
			t.Fatalf("flagClone: %v", err)
		}
		won, err := r.p.register.writeBack(ctx, "f1", credentialRow{SignCount: 6})
		if err != nil || !won {
			t.Fatalf("writeBack = %v, %v, want won", won, err)
		}
		row, found, err := r.p.register.getCredential(ctx, "f1")
		if err != nil || !found {
			t.Fatalf("getCredential: found=%v err=%v", found, err)
		}
		if !row.CloneWarning {
			t.Error("the write-back cleared a clone warning another writer set")
		}
		if row.SignCount != 6 {
			t.Errorf("the counter is %d, want it advanced to 6", row.SignCount)
		}
	})
}

// TestWriteBackSetsACloneFlag checks that a write-back carrying the clone warning sets it.
func TestWriteBackSetsACloneFlag(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h)
		ctx := context.Background()
		seedCredential(t, r, "f1", 1, false)
		if _, err := r.p.register.writeBack(ctx, "f1", credentialRow{SignCount: 2, CloneWarning: true}); err != nil {
			t.Fatalf("writeBack: %v", err)
		}
		row, found, err := r.p.register.getCredential(ctx, "f1")
		if err != nil || !found {
			t.Fatalf("getCredential: found=%v err=%v", found, err)
		}
		if !row.CloneWarning {
			t.Error("a write-back carrying the clone warning did not set it")
		}
	})
}

// TestProbeReportsAMissingTable checks that probe reports ErrNotMigrated for a missing table.
func TestProbeReportsAMissingTable(t *testing.T) {
	h := openDialect(t, sqliteDialect)
	r := newRig(t, h)
	if err := r.p.register.probe(context.Background()); err != nil {
		t.Errorf("probe over the migrated tables: %v, want nil", err)
	}
}
