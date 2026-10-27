package webauthn

import (
	"bytes"
	"context"
	"testing"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/step"
)

// A registration answers with the creation options, and the client's response
// registers a credential and activates the factor. The two steps are the whole
// of what this module does for a registration.
func TestRegistrationAnswersOptionsThenEnrolls(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h)
		a := newAuthenticator(t)
		rec := r.enroll(t, a, profilePasskey, true)
		row, found, err := r.p.register.getCredential(context.Background(), rec.ID)
		if err != nil || !found {
			t.Fatalf("the credential was not written: found=%v err=%v", found, err)
		}
		if !row.Passkey {
			t.Error("the credential registered under the passkey profile is not recorded as one")
		}
		if row.RPID != testRPID {
			t.Errorf("the credential records the relying party id %q, want %q", row.RPID, testRPID)
		}
	})
}

// A registration begun under the security-key profile asks for no discoverable
// credential and only prefers user verification, which is the whole difference
// between the two profiles.
func TestTheTwoProfilesDifferInTheOptions(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h)
		ctx := context.Background()

		passkeyOpts := creationOptionsOf(t, r, pending(), profilePasskey)
		if passkeyOpts.AuthenticatorSelection.ResidentKey != "required" {
			t.Errorf("the passkey profile asks residentKey = %q, want required",
				passkeyOpts.AuthenticatorSelection.ResidentKey)
		}
		if passkeyOpts.AuthenticatorSelection.UserVerification != "required" {
			t.Errorf("the passkey profile asks userVerification = %q, want required",
				passkeyOpts.AuthenticatorSelection.UserVerification)
		}
		if passkeyOpts.AuthenticatorSelection.RequireResidentKey == nil || !*passkeyOpts.AuthenticatorSelection.RequireResidentKey {
			t.Error("the passkey profile does not set requireResidentKey")
		}

		keyOpts := creationOptionsOf(t, r, pending(), profileSecurityKey)
		if keyOpts.AuthenticatorSelection.ResidentKey != "preferred" {
			t.Errorf("the security-key profile asks residentKey = %q, want preferred",
				keyOpts.AuthenticatorSelection.ResidentKey)
		}
		if keyOpts.AuthenticatorSelection.UserVerification != "preferred" {
			t.Errorf("the security-key profile asks userVerification = %q, want preferred",
				keyOpts.AuthenticatorSelection.UserVerification)
		}
		_ = ctx
	})
}

// A second-factor assertion keeps the relying party's preferred user
// verification: only the passkey login's assertion is raised to required.
func TestASecondFactorAssertionPrefersUserVerification(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h)
		a := newAuthenticator(t)
		rec := r.enroll(t, a, profilePasskey, true)
		opts, _ := r.challenge(t, rec)
		if opts.UserVerification != "preferred" {
			t.Errorf("the second-factor assertion asks userVerification = %q, want preferred", opts.UserVerification)
		}
	})
}

// A registration excludes the account's active credentials only: a disabled
// factor's credential is not offered, so the authenticator that holds it can be
// registered again. An active one is still excluded.
func TestRegistrationExcludesOnlyActiveCredentials(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h)
		a := newAuthenticator(t)
		first := r.enroll(t, a, profilePasskey, true)

		r.mfa.set(acct, authn.FactorRecord{ID: first.ID, Account: acct, Kind: authn.FactorWebAuthn, Status: authn.FactorDisabled})
		disabled := authn.FactorRecord{ID: "factor-2", Account: acct, Kind: authn.FactorWebAuthn, Status: authn.FactorPending}
		opts := creationOptionsOf(t, r, disabled, profilePasskey)
		if len(opts.ExcludeCredentials) != 0 {
			t.Errorf("a disabled factor's credential is still excluded: %+v", opts.ExcludeCredentials)
		}

		r.mfa.set(acct, authn.FactorRecord{ID: first.ID, Account: acct, Kind: authn.FactorWebAuthn, Status: authn.FactorActive})
		active := authn.FactorRecord{ID: "factor-3", Account: acct, Kind: authn.FactorWebAuthn, Status: authn.FactorPending}
		opts = creationOptionsOf(t, r, active, profilePasskey)
		if len(opts.ExcludeCredentials) != 1 {
			t.Errorf("an active factor's credential is not excluded: %+v", opts.ExcludeCredentials)
		}
	})
}

// A second registration for an account that already holds a credential excludes
// it, so the same authenticator is not registered twice.
func TestRegistrationExcludesExistingCredentials(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h)
		a := newAuthenticator(t)
		first := r.enroll(t, a, profilePasskey, true)
		r.mfa.set(acct, authn.FactorRecord{ID: first.ID, Account: acct, Kind: authn.FactorWebAuthn, Status: authn.FactorActive})

		opts := creationOptionsOf(t, r, authn.FactorRecord{ID: "factor-2", Account: acct, Kind: authn.FactorWebAuthn, Status: authn.FactorPending}, profilePasskey)
		if len(opts.ExcludeCredentials) != 1 {
			t.Fatalf("the second registration excludes %d credentials, want the first one", len(opts.ExcludeCredentials))
		}
		if opts.ExcludeCredentials[0].ID == "" {
			t.Error("the exclusion carries no credential identifier")
		}
	})
}

// The challenge answers with assertion options about the registered credential,
// and a valid assertion passes the factor with the methods the module reports.
func TestChallengeThenVerifyPassesAFactor(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h)
		a := newAuthenticator(t)
		rec := r.enroll(t, a, profilePasskey, true)

		opts, private := r.challenge(t, rec)
		if len(opts.AllowCredentials) != 1 {
			t.Fatalf("the challenge allows %d credentials, want the one registered", len(opts.AllowCredentials))
		}
		body := a.assertion(t, opts.Challenge, 1, true, nil)
		got := r.verify(t, rec, private, body)
		if step.KindOf(got) != step.KindPassed {
			t.Fatalf("Verify answered %v, want passed", step.KindOf(got))
		}
		pass := step.PassOf(got)
		if len(pass.Methods) != 1 || pass.Methods[0] != authn.MethodWebAuthn {
			t.Errorf("the pass reports the methods %v, want webauthn", pass.Methods)
		}
		if !pass.UserVerified {
			t.Error("an assertion that verified the user is reported as not verified")
		}
		if count, _ := r.storedCount(t, rec.ID); count != 1 {
			t.Errorf("the stored counter is %d, want 1", count)
		}
	})
}

// An assertion whose signature does not hold is refused, and the stored counter
// is left where it was.
func TestAnAssertionWithATamperedSignatureIsRefused(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h)
		a := newAuthenticator(t)
		rec := r.enroll(t, a, profilePasskey, true)
		opts, private := r.challenge(t, rec)
		body := tamperedAssertion(t, a, opts.Challenge, 1)
		got := r.verify(t, rec, private, body)
		if step.KindOf(got) != step.KindRefused {
			t.Fatalf("Verify answered %v, want refused", step.KindOf(got))
		}
		if count, _ := r.storedCount(t, rec.ID); count != 0 {
			t.Errorf("the stored counter is %d, want it untouched at 0", count)
		}
	})
}

// The same assertion presented twice is refused the second time: its counter has
// already been consumed, which the library reports as a clone warning. It is what
// stops the ceremony data being spent twice at this layer.
func TestASecondPresentationOfAnAssertionIsRefused(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h)
		a := newAuthenticator(t)
		rec := r.enroll(t, a, profilePasskey, true)
		opts, private := r.challenge(t, rec)
		body := a.assertion(t, opts.Challenge, 1, true, nil)

		if got := r.verify(t, rec, private, body); step.KindOf(got) != step.KindPassed {
			t.Fatalf("the first presentation answered %v, want passed", step.KindOf(got))
		}
		if got := r.verify(t, rec, private, body); step.KindOf(got) != step.KindRefused {
			t.Fatalf("the second presentation answered %v, want refused", step.KindOf(got))
		}
	})
}

// A counter that goes backwards is the library's clone warning: the factor is
// flagged, the assertion is refused, and a later call is refused before the factor
// is asked, so the credential is unusable until it is removed.
func TestACloneWarningRefusesAndFlagsTheFactor(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h)
		a := newAuthenticator(t)
		rec := r.enroll(t, a, profilePasskey, true)

		opts, private := r.challenge(t, rec)
		if got := r.verify(t, rec, private, a.assertion(t, opts.Challenge, 5, true, nil)); step.KindOf(got) != step.KindPassed {
			t.Fatalf("the assertion at counter 5 answered %v, want passed", step.KindOf(got))
		}
		if count, _ := r.storedCount(t, rec.ID); count != 5 {
			t.Fatalf("the stored counter is %d, want 5", count)
		}

		opts2, private2 := r.challenge(t, rec)
		if got := r.verify(t, rec, private2, a.assertion(t, opts2.Challenge, 3, true, nil)); step.KindOf(got) != step.KindRefused {
			t.Fatalf("the assertion whose counter went backwards answered %v, want refused", step.KindOf(got))
		}
		if _, flagged := r.storedCount(t, rec.ID); !flagged {
			t.Error("the credential was not flagged after its counter went backwards")
		}
		// A flagged factor is unavailable: the next challenge refuses without
		// even answering with options.
		got, err := r.p.Challenge(context.Background(), authn.FlowState{Account: acct}, rec)
		if err != nil {
			t.Fatalf("Challenge: %v", err)
		}
		if step.KindOf(got) != step.KindRefused {
			t.Errorf("a flagged factor answered %v, want refused", step.KindOf(got))
		}
	})
}

// The ceremony state this module keeps between steps fits inside the flow
// record's private bytes, which the login engine bounds at 8 KiB. A registration
// and an assertion each serialize to a small value, and the assertion's grows by a
// descriptor per allowed credential, so a credential list of the size an account
// can hold stays well inside the bound. The design lists this as an assumption
// with this test guarding it.
func TestTheCeremonyStateFitsTheFlowRecord(t *testing.T) {
	const privateBound = 8 << 10
	wa, err := newRelyingParty(testConfig())
	if err != nil {
		t.Fatalf("building the relying party: %v", err)
	}
	u := user{id: make([]byte, 32), name: "alice", display: "alice"}

	_, regState, err := beginRegistration(wa, u, true)
	if err != nil {
		t.Fatalf("beginRegistration: %v", err)
	}
	if len(regState) >= privateBound {
		t.Errorf("the registration state is %d bytes, at or above the %d-byte bound", len(regState), privateBound)
	}

	creds := make([]libraryCredential, 0, 20)
	for i := 0; i < 20; i++ {
		creds = append(creds, libraryCredential{ID: bytes.Repeat([]byte{byte(i)}, 16)})
	}
	u.creds = creds
	_, asState, err := beginAssertion(wa, u, creds)
	if err != nil {
		t.Fatalf("beginAssertion: %v", err)
	}
	if len(asState) >= privateBound {
		t.Errorf("the assertion state for 20 credentials is %d bytes, at or above the %d-byte bound",
			len(asState), privateBound)
	}
}

// A credential whose counter never moves — a synchronizable passkey reports 0 on
// every assertion — keeps passing. The library warns of a clone only when the
// presented count is not above the stored one and the two are not both zero, so a
// run of zero-count assertions is not mistaken for one.
func TestACredentialWhoseCounterStaysZeroKeepsPassing(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h)
		a := newAuthenticator(t)
		rec := r.enroll(t, a, profilePasskey, true)
		for i := 1; i <= 3; i++ {
			opts, private := r.challenge(t, rec)
			if got := r.verify(t, rec, private, a.assertion(t, opts.Challenge, 0, true, nil)); step.KindOf(got) != step.KindPassed {
				t.Fatalf("assertion %d at counter 0 answered %v, want passed", i, step.KindOf(got))
			}
			if _, flagged := r.storedCount(t, rec.ID); flagged {
				t.Fatalf("assertion %d at counter 0 was taken for a clone", i)
			}
		}
	})
}

// A ceremony whose session has expired is refused: the library reads the expiry
// this module records because the timeouts are enforced.
func TestAnExpiredCeremonyIsRefused(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h)
		a := newAuthenticator(t)
		rec := r.enroll(t, a, profilePasskey, true)

		opts, _ := r.challenge(t, rec)
		handle, _, err := r.p.register.handleOf(context.Background(), acct)
		if err != nil {
			t.Fatalf("handleOf: %v", err)
		}
		id, err := handleBytesOf(handle)
		if err != nil {
			t.Fatalf("handleBytesOf: %v", err)
		}
		private := expiredCeremony(t, opts.Challenge, id)
		if got := r.verify(t, rec, private, a.assertion(t, opts.Challenge, 1, true, nil)); step.KindOf(got) != step.KindRefused {
			t.Fatalf("an expired ceremony answered %v, want refused", step.KindOf(got))
		}
	})
}

// A call about a factor that has no credential is refused, which is a factor that
// was removed or never written here.
func TestAFactorWithoutACredentialIsRefused(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h)
		rec := authn.FactorRecord{ID: "missing", Account: acct, Kind: authn.FactorWebAuthn, Status: authn.FactorActive}
		got, err := r.p.Challenge(context.Background(), authn.FlowState{Account: acct}, rec)
		if err != nil {
			t.Fatalf("Challenge: %v", err)
		}
		if step.KindOf(got) != step.KindRefused {
			t.Errorf("Challenge answered %v, want refused", step.KindOf(got))
		}
		got = r.verify(t, rec, nil, []byte("{}"))
		if step.KindOf(got) != step.KindRefused {
			t.Errorf("Verify answered %v, want refused", step.KindOf(got))
		}
	})
}

// Enroll is refused when no pending row was handed to it, which is a call from
// something other than the multi-factor module; nothing is written.
func TestEnrollWithoutAPendingRowIsRefused(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h)
		got, err := r.p.Enroll(context.Background(), authn.FlowState{Account: acct}, authn.StepInput{})
		if err != nil {
			t.Fatalf("Enroll: %v", err)
		}
		if step.KindOf(got) != step.KindRefused {
			t.Errorf("Enroll answered %v, want refused", step.KindOf(got))
		}
	})
}

// Remove deletes the credential and, with the account's last one, its user
// handle; it is idempotent.
func TestRemoveDropsTheCredentialAndWithItTheHandle(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h)
		a := newAuthenticator(t)
		rec := r.enroll(t, a, profilePasskey, true)
		if _, found, _ := r.p.register.handleOf(context.Background(), acct); !found {
			t.Fatal("the account has no handle after a registration")
		}
		if err := r.p.Remove(context.Background(), acct, rec); err != nil {
			t.Fatalf("Remove: %v", err)
		}
		if _, found, _ := r.p.register.getCredential(context.Background(), rec.ID); found {
			t.Error("the credential survived its removal")
		}
		if _, found, _ := r.p.register.handleOf(context.Background(), acct); found {
			t.Error("the account's handle survived the removal of its last credential")
		}
		if err := r.p.Remove(context.Background(), acct, rec); err != nil {
			t.Errorf("a repeated Remove is not idempotent: %v", err)
		}
	})
}

// Kind is the factor kind the module delivers.
func TestKindIsWebAuthn(t *testing.T) {
	if got := (&product{}).Kind(); got != authn.FactorWebAuthn {
		t.Errorf("Kind = %q, want webauthn", got)
	}
}

// The name an authenticator shows for the account is its username identifier, or
// its account identifier when it has none. It decides nothing but the display.
func TestTheAuthenticatorNameIsTheUsernameOrTheAccount(t *testing.T) {
	r := newRig(t, openDialect(t, sqliteDialect))
	ctx := context.Background()
	if got := r.p.nameOf(ctx, acct); got != "alice" {
		t.Errorf("the name of an account with a username identifier is %q, want the identifier", got)
	}
	other := authn.AccountID("acct-2")
	if got := r.p.nameOf(ctx, other); got != string(other) {
		t.Errorf("the name of an account with no username identifier is %q, want the account identifier", got)
	}
}

// Ceremony data this module did not write is refused rather than handed to the
// library, whose decoder trusts a length prefix and would allocate from a tampered
// one.
func TestForeignCeremonyDataIsRefused(t *testing.T) {
	for _, b := range [][]byte{nil, []byte("not json"), []byte(`{"session":""}`)} {
		if _, _, err := parseCeremony(b); err == nil {
			t.Errorf("parseCeremony(%q) accepted data this module did not write", b)
		}
	}
}

// Name is the login method name the module reports.
func TestNameIsPasskey(t *testing.T) {
	if got := (&product{}).Name(); got != "passkey" {
		t.Errorf("Name = %q, want passkey", got)
	}
}

// challengeOptionsOf starts a registration and returns the creation options.
func creationOptionsOf(t *testing.T, r *rig, rec authn.FactorRecord, profile string) creationOptions {
	t.Helper()
	got, err := r.p.beginEnroll(context.Background(), rec, authn.StepInput{
		Action: authn.InputSubmit, Fields: map[string]string{fieldProfile: profile},
	})
	if err != nil {
		t.Fatalf("beginEnroll: %v", err)
	}
	if step.KindOf(got) != step.KindData {
		t.Fatalf("beginEnroll answered %v, want data", step.KindOf(got))
	}
	return decodeCreation(t, step.DataOf(got))
}

// challenge starts a second-factor assertion and returns the options and the
// private state that answers it.
func (r *rig) challenge(t *testing.T, rec authn.FactorRecord) (assertionOptions, []byte) {
	t.Helper()
	got, err := r.p.Challenge(context.Background(), authn.FlowState{Account: acct}, rec)
	if err != nil {
		t.Fatalf("Challenge: %v", err)
	}
	if step.KindOf(got) != step.KindData {
		t.Fatalf("Challenge answered %v, want data", step.KindOf(got))
	}
	return decodeAssertion(t, step.DataOf(got)), step.PrivateOf(got)
}

// verify answers a challenge with a credential body and returns the step.
func (r *rig) verify(t *testing.T, rec authn.FactorRecord, private, body []byte) authn.Step {
	t.Helper()
	got, err := r.p.Verify(context.Background(), authn.FlowState{Private: private}, rec,
		authn.StepInput{Action: authn.InputSubmit, Fields: map[string]string{fieldCredential: string(body)}})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	return got
}
