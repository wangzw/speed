package webauthn

import (
	"context"
	"encoding/base64"
	"errors"
	"math"
	"testing"

	libwebauthn "github.com/go-webauthn/webauthn/webauthn"
	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/step"
)

// The credential a passkey login may prove a first factor with is the one of an
// active passkey factor: a factor a reset disabled authenticates nothing, so its
// credential is not offered to the library and an assertion against it is
// refused. This holds the status filter on the account's factor rows.
func TestGapDisabledPasskeyFactorCannotProveAFirstFactor(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h)
		a := newAuthenticator(t)
		rec := r.enroll(t, a, profilePasskey, true)
		r.mfa.set(acct, authn.FactorRecord{ID: rec.ID, Account: acct, Kind: authn.FactorWebAuthn, Status: authn.FactorDisabled})
		handle := r.handle(t)

		opts, private := r.passkeyStart(t)
		got := r.answer(t, private, a.assertion(t, opts.Challenge, 1, true, handle))
		if step.KindOf(got) != step.KindRefused {
			t.Errorf("a disabled factor's passkey answered %v, want refused", step.KindOf(got))
		}
	})
}

// A security key is a second factor only, so its credential cannot prove a first
// factor: it is not a passkey and the login method does not offer it to the
// library.
func TestGapSecurityKeyCredentialCannotProveAFirstFactor(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h)
		a := newAuthenticator(t)
		rec := r.enroll(t, a, profileSecurityKey, false)
		r.mfa.set(acct, authn.FactorRecord{ID: rec.ID, Account: acct, Kind: authn.FactorWebAuthn, Status: authn.FactorActive})
		handle := r.handle(t)

		opts, private := r.passkeyStart(t)
		got := r.answer(t, private, a.assertion(t, opts.Challenge, 1, true, handle))
		if step.KindOf(got) != step.KindRefused {
			t.Errorf("a security key's credential answered %v as a first factor, want refused", step.KindOf(got))
		}
	})
}

// A credential registered under the security-key profile is stored as not a
// passkey, which is what the login method's first-factor filter reads.
func TestGapSecurityKeyProfileStoresNotAPasskey(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h)
		a := newAuthenticator(t)
		rec := r.enroll(t, a, profileSecurityKey, false)
		row, found, err := r.p.register.getCredential(context.Background(), rec.ID)
		if err != nil || !found {
			t.Fatalf("the credential was not written: found=%v err=%v", found, err)
		}
		if row.Passkey {
			t.Error("a credential registered under the security-key profile is recorded as a passkey")
		}
	})
}

// A flagged credential proves nothing at the factor's verification either: the
// flag is checked before the library is asked, so a second factor cannot be
// verified with a credential a clone warning already took out.
func TestGapVerifyRefusesFlaggedCredential(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h)
		a := newAuthenticator(t)
		rec := r.enroll(t, a, profilePasskey, true)
		opts, private := r.challenge(t, rec)
		if err := r.p.register.flagClone(context.Background(), rec.ID); err != nil {
			t.Fatalf("flagClone: %v", err)
		}
		got := r.verify(t, rec, private, a.assertion(t, opts.Challenge, 1, true, nil))
		if step.KindOf(got) != step.KindRefused {
			t.Errorf("a flagged credential's assertion answered %v, want refused", step.KindOf(got))
		}
	})
}

// A flagged credential proves nothing at the passkey login either: the login
// method refuses it after resolving the account, and the login is answered as
// one that failed.
func TestGapPasskeyRefusesFlaggedCredential(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h)
		a := newAuthenticator(t)
		rec := r.enroll(t, a, profilePasskey, true)
		r.mfa.set(acct, authn.FactorRecord{ID: rec.ID, Account: acct, Kind: authn.FactorWebAuthn, Status: authn.FactorActive})
		handle := r.handle(t)

		opts, private := r.passkeyStart(t)
		if err := r.p.register.flagClone(context.Background(), rec.ID); err != nil {
			t.Fatalf("flagClone: %v", err)
		}
		got := r.answer(t, private, a.assertion(t, opts.Challenge, 1, true, handle))
		if step.KindOf(got) != step.KindRefused {
			t.Errorf("a flagged credential's passkey answer gave %v, want refused", step.KindOf(got))
		}
	})
}

// The login method's step guard refuses a flow record whose step it did not make
// even when the submitted body and the ceremony state are otherwise valid, so a
// record cannot be walked into a proof through a step of another method.
func TestGapAnswerRefusesForeignStepWithValidBody(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h)
		a := newAuthenticator(t)
		rec := r.enroll(t, a, profilePasskey, true)
		r.mfa.set(acct, authn.FactorRecord{ID: rec.ID, Account: acct, Kind: authn.FactorWebAuthn, Status: authn.FactorActive})
		handle := r.handle(t)

		opts, private := r.passkeyStart(t)
		body := a.assertion(t, opts.Challenge, 1, true, handle)
		got, err := r.p.Answer(context.Background(),
			authn.FlowState{Purpose: authn.PurposeLogin, Step: "other", Private: private},
			authn.StepInput{Action: authn.InputSubmit, Fields: map[string]string{fieldCredential: string(body)}})
		if err != nil {
			t.Fatalf("Answer: %v", err)
		}
		if step.KindOf(got) != step.KindRefused {
			t.Errorf("Answer on a foreign step with a valid body gave %v, want refused", step.KindOf(got))
		}
	})
}

// The login method's purpose guard refuses a purpose a passkey does not prove
// even when the body and the ceremony state are otherwise valid.
func TestGapAnswerRefusesAForeignPurpose(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h)
		a := newAuthenticator(t)
		rec := r.enroll(t, a, profilePasskey, true)
		r.mfa.set(acct, authn.FactorRecord{ID: rec.ID, Account: acct, Kind: authn.FactorWebAuthn, Status: authn.FactorActive})
		handle := r.handle(t)

		opts, private := r.passkeyStart(t)
		body := a.assertion(t, opts.Challenge, 1, true, handle)
		got, err := r.p.Answer(context.Background(),
			authn.FlowState{Purpose: authn.PurposeLink, Step: stepPasskey, Private: private},
			authn.StepInput{Action: authn.InputSubmit, Fields: map[string]string{fieldCredential: string(body)}})
		if err != nil {
			t.Fatalf("Answer: %v", err)
		}
		if step.KindOf(got) != step.KindRefused {
			t.Errorf("Answer on a link purpose with a valid body gave %v, want refused", step.KindOf(got))
		}
	})
}

// Every entry point refuses a submission whose action is not a submit, even when
// the body and the ceremony state it is given are otherwise valid, so a first
// step that is not a client's submission cannot start or advance a ceremony.
func TestGapEntryPointsRefuseANonSubmitAction(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h)
		ctx := context.Background()
		a := newAuthenticator(t)
		rec := r.enroll(t, a, profilePasskey, true)
		r.mfa.set(acct, authn.FactorRecord{ID: rec.ID, Account: acct, Kind: authn.FactorWebAuthn, Status: authn.FactorActive})
		handle := r.handle(t)

		opts, passkeyPrivate := r.passkeyStart(t)
		passkeyBody := a.assertion(t, opts.Challenge, 1, true, handle)
		aopts, verifyPrivate := r.challenge(t, rec)
		verifyBody := a.assertion(t, aopts.Challenge, 1, true, nil)

		begin, err := r.p.beginEnroll(ctx, pending(), authn.StepInput{
			Action: authn.InputSubmit, Fields: map[string]string{fieldProfile: profilePasskey},
		})
		if err != nil {
			t.Fatalf("beginEnroll: %v", err)
		}
		enrollPrivate := step.PrivateOf(begin)
		enrollBody := a.registration(t, decodeCreation(t, step.DataOf(begin)).Challenge, true)

		// A non-submit action with a valid body: the action guard, not the body,
		// is what must refuse it.
		nonSubmit := func(body []byte) authn.StepInput {
			return authn.StepInput{Fields: map[string]string{fieldCredential: string(body)}}
		}

		cases := []struct {
			name string
			call func() authn.Step
		}{
			{"Answer", func() authn.Step {
				got, err := r.p.Answer(ctx,
					authn.FlowState{Purpose: authn.PurposeLogin, Step: stepPasskey, Private: passkeyPrivate}, nonSubmit(passkeyBody))
				if err != nil {
					t.Fatalf("Answer: %v", err)
				}
				return got
			}},
			{"Verify", func() authn.Step {
				got, err := r.p.Verify(ctx, authn.FlowState{Private: verifyPrivate}, rec, nonSubmit(verifyBody))
				if err != nil {
					t.Fatalf("Verify: %v", err)
				}
				return got
			}},
			{"beginEnroll", func() authn.Step {
				got, err := r.p.beginEnroll(ctx, pending(), authn.StepInput{
					Fields: map[string]string{fieldProfile: profilePasskey},
				})
				if err != nil {
					t.Fatalf("beginEnroll: %v", err)
				}
				return got
			}},
			{"finishEnroll", func() authn.Step {
				got, err := r.p.finishEnroll(ctx, pending(), authn.FlowState{Private: enrollPrivate}, nonSubmit(enrollBody))
				if err != nil {
					t.Fatalf("finishEnroll: %v", err)
				}
				return got
			}},
		}
		for _, tc := range cases {
			if k := step.KindOf(tc.call()); k != step.KindRefused {
				t.Errorf("%s with a non-submit action gave %v, want refused", tc.name, k)
			}
		}
	})
}

// factorIDs keeps only the account's factors of this module's kind, so another
// kind's factor row is never taken for one of these.
func TestGapFactorIDsKeepsOnlyWebAuthn(t *testing.T) {
	r := newRig(t, openDialect(t, sqliteDialect))
	r.mfa.set(acct,
		authn.FactorRecord{ID: "totp-1", Account: acct, Kind: authn.FactorTOTP, Status: authn.FactorActive},
		authn.FactorRecord{ID: "web-1", Account: acct, Kind: authn.FactorWebAuthn, Status: authn.FactorActive})
	ids, err := r.p.factorIDs(context.Background(), acct)
	if err != nil {
		t.Fatalf("factorIDs: %v", err)
	}
	if len(ids) != 1 || ids[0] != "web-1" {
		t.Errorf("factorIDs gave %v, want only the webauthn factor", ids)
	}
}

// Enroll with a submission and no pending row is refused and writes nothing: the
// pending-row guard is what refuses it, not the empty action the other case
// passes.
func TestGapEnrollWithNoPendingRowWritesNothing(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h)
		got, err := r.p.Enroll(context.Background(), authn.FlowState{Account: acct},
			authn.StepInput{Action: authn.InputSubmit, Fields: map[string]string{fieldProfile: profilePasskey}})
		if err != nil {
			t.Fatalf("Enroll: %v", err)
		}
		if step.KindOf(got) != step.KindRefused {
			t.Errorf("Enroll with no pending row gave %v, want refused", step.KindOf(got))
		}
		if _, found, _ := r.p.register.handleOf(context.Background(), acct); found {
			t.Error("Enroll with no pending row wrote a user handle")
		}
	})
}

// Remove drops the account's user handle with its last credential: the credential
// is read back through the multi-factor module's seam, and once none is left the
// handle goes with it.
func TestGapRemoveDropsHandleOnLastCredential(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h)
		a := newAuthenticator(t)
		rec := r.enroll(t, a, profilePasskey, true)
		r.mfa.set(acct, authn.FactorRecord{ID: rec.ID, Account: acct, Kind: authn.FactorWebAuthn, Status: authn.FactorActive})
		if err := r.p.Remove(context.Background(), acct, rec); err != nil {
			t.Fatalf("Remove: %v", err)
		}
		if _, found, _ := r.p.register.handleOf(context.Background(), acct); found {
			t.Error("the account's handle survived the removal of its last credential")
		}
	})
}

// A write-back that loses — a concurrent assertion advanced the counter first —
// is a replay and not a pass.
func TestGapLostWriteBackIsReplay(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h)
		seedCredential(t, r, "f1", 9, false)
		res := assertionResult{credential: &libwebauthn.Credential{
			Authenticator: libwebauthn.Authenticator{SignCount: 5},
		}}
		outcome, err := r.p.settleAssertion(context.Background(), credentialRow{FactorID: "f1", SignCount: 9}, res)
		if err != nil {
			t.Fatalf("settleAssertion: %v", err)
		}
		if outcome != assertionReplay {
			t.Errorf("a lost write-back was read as %v, want a replay", outcome)
		}
	})
}

// The BackupEligible bit a credential was registered with reaches the library:
// it compares the stored bit against the presented one, so a bit dropped on the
// way in would break login for a synchronizable passkey.
func TestGapToLibraryKeepsBackupEligible(t *testing.T) {
	row := credentialRow{
		CredentialID:   base64.RawURLEncoding.EncodeToString([]byte("cred")),
		PublicKey:      base64.RawURLEncoding.EncodeToString([]byte("pk")),
		AAGUID:         base64.RawURLEncoding.EncodeToString(make([]byte, 16)),
		BackupEligible: true,
	}
	c, err := row.toLibrary()
	if err != nil {
		t.Fatalf("toLibrary: %v", err)
	}
	if !c.Flags.BackupEligible {
		t.Error("a credential stored as backup-eligible reached the library without the bit")
	}
}

// A verification that did not verify the user reports no user verification: the
// bit comes from the ceremony, and an implementation that always reported it
// would escalate the assurance silently.
func TestGapFactorVerifyWithoutUserVerification(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h)
		a := newAuthenticator(t)
		rec := r.enroll(t, a, profilePasskey, true)
		opts, private := r.challenge(t, rec)
		got := r.verify(t, rec, private, a.assertion(t, opts.Challenge, 1, false, nil))
		if step.KindOf(got) != step.KindPassed {
			t.Fatalf("Verify answered %v, want passed", step.KindOf(got))
		}
		if step.PassOf(got).UserVerified {
			t.Error("an assertion without user verification was reported as verified")
		}
	})
}

// The low-level readers refuse the values this module did not write: an empty
// handle, an empty credential identifier and an empty public key, and a counter
// outside the width the protocol carries.
func TestGapMalformedStoredValuesAreRefused(t *testing.T) {
	if _, err := handleBytesOf(""); err == nil {
		t.Error("handleBytesOf accepted an empty handle")
	}
	if _, err := (credentialRow{
		PublicKey: base64.RawURLEncoding.EncodeToString([]byte("pk")),
	}).toLibrary(); err == nil {
		t.Error("toLibrary accepted an empty credential identifier")
	}
	if _, err := (credentialRow{
		CredentialID: base64.RawURLEncoding.EncodeToString([]byte("cred")),
	}).toLibrary(); err == nil {
		t.Error("toLibrary accepted an empty public key")
	}
	if _, err := signCountOf(math.MaxUint32); err != nil {
		t.Errorf("signCountOf(%d): %v, want it accepted", math.MaxUint32, err)
	}
	if _, err := signCountOf(math.MaxUint32 + 1); err == nil {
		t.Error("signCountOf accepted a counter above the 32-bit width")
	}
	if _, err := signCountOf(-1); err == nil {
		t.Error("signCountOf accepted a negative counter")
	}
}

// A credential bound to another relying party id is not handed to the library, so
// an assertion collected at another site cannot complete a ceremony here.
func TestGapCredentialsAreScopedByRelyingParty(t *testing.T) {
	r := newRig(t, openDialect(t, sqliteDialect))
	ctx := context.Background()
	credID := []byte("cred-elsewhere")
	seedCredentialUnderRP(t, r, "f1", "evil.example", credID)

	rows, err := r.p.register.credentialsFor(ctx, []string{"f1"}, testRPID)
	if err != nil {
		t.Fatalf("credentialsFor: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("credentialsFor handed over %d credentials of another relying party", len(rows))
	}
	if _, found, err := r.p.register.credentialByID(ctx, testRPID, credID); err != nil || found {
		t.Errorf("credentialByID found a credential of another relying party: found=%v err=%v", found, err)
	}
}

// probe reports ErrNotMigrated for this module's own tables when they are not
// there, rather than a plain read failure.
func TestGapProbeReportsAMissingTable(t *testing.T) {
	h := openDialect(t, sqliteDialect)
	r := newRig(t, h)
	for _, table := range []string{userTable, authenticatorTable} {
		if err := h.Exec("DROP TABLE " + table).Error; err != nil {
			t.Fatalf("dropping %s: %v", table, err)
		}
	}
	if err := r.p.register.probe(context.Background()); !errors.Is(err, authn.ErrNotMigrated) {
		t.Errorf("probe over missing tables returned %v, want ErrNotMigrated", err)
	}
}

// seedCredentialUnderRP writes a credential row bound to the relying party id
// given, for a case that must see the scoping read exclude it.
func seedCredentialUnderRP(t *testing.T, r *rig, factorID, rpID string, credID []byte) {
	t.Helper()
	err := r.p.register.insertCredential(context.Background(), credentialRow{
		FactorID:        factorID,
		RPID:            rpID,
		CredentialID:    base64.RawURLEncoding.EncodeToString(credID),
		PublicKey:       base64.RawURLEncoding.EncodeToString([]byte("pk")),
		AAGUID:          base64.RawURLEncoding.EncodeToString(make([]byte, 16)),
		AttestationType: "none",
	})
	if err != nil {
		t.Fatalf("seeding a credential under %s: %v", rpID, err)
	}
}
