package webauthn

import (
	"context"
	"testing"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/step"
)

// A passkey login proves the account the client picks: the discoverable assertion
// yields a proof naming the account and the webauthn method, and user
// verification when the assertion carried it.
func TestPasskeyLoginProvesAnAccount(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h)
		a := newAuthenticator(t)
		rec := r.enroll(t, a, profilePasskey, true)
		r.mfa.set(acct, authn.FactorRecord{ID: rec.ID, Account: acct, Kind: authn.FactorWebAuthn, Status: authn.FactorActive})
		handle := r.handle(t)

		opts, private := r.passkeyStart(t)
		proof := r.passkeyAnswer(t, private, a.assertion(t, opts.Challenge, 1, true, handle))
		if proof.Account != acct {
			t.Errorf("the proof names the account %q, want %q", proof.Account, acct)
		}
		if len(proof.Methods) != 1 || proof.Methods[0] != authn.MethodWebAuthn {
			t.Errorf("the proof reports the methods %v, want webauthn", proof.Methods)
		}
		if !proof.UserVerified {
			t.Error("an assertion that verified the user does not set UserVerified")
		}
	})
}

// A passkey whose assertion did not verify the user reports no user verification,
// which is what leaves it a single factor.
func TestAPasskeyWithoutUserVerificationReportsNoUserVerification(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h)
		a := newAuthenticator(t)
		rec := r.enroll(t, a, profilePasskey, true)
		r.mfa.set(acct, authn.FactorRecord{ID: rec.ID, Account: acct, Kind: authn.FactorWebAuthn, Status: authn.FactorActive})
		handle := r.handle(t)

		opts, private := r.passkeyStart(t)
		proof := r.passkeyAnswer(t, private, a.assertion(t, opts.Challenge, 1, false, handle))
		if proof.UserVerified {
			t.Error("an assertion without user verification sets UserVerified")
		}
	})
}

// The same passkey assertion presented twice is refused the second time, because
// its counter was already spent.
func TestASecondPasskeyAssertionIsRefused(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h)
		a := newAuthenticator(t)
		rec := r.enroll(t, a, profilePasskey, true)
		r.mfa.set(acct, authn.FactorRecord{ID: rec.ID, Account: acct, Kind: authn.FactorWebAuthn, Status: authn.FactorActive})
		handle := r.handle(t)

		opts, private := r.passkeyStart(t)
		body := a.assertion(t, opts.Challenge, 1, true, handle)
		if got := r.answer(t, private, body); step.KindOf(got) != step.KindProven {
			t.Fatalf("the first answer gave %v, want proven", step.KindOf(got))
		}
		if got := r.answer(t, private, body); step.KindOf(got) != step.KindRefused {
			t.Fatalf("the second answer gave %v, want refused", step.KindOf(got))
		}
	})
}

// An assertion signed by another key is refused, and it names no account.
func TestAPasskeyWithATamperedSignatureIsRefused(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h)
		a := newAuthenticator(t)
		rec := r.enroll(t, a, profilePasskey, true)
		r.mfa.set(acct, authn.FactorRecord{ID: rec.ID, Account: acct, Kind: authn.FactorWebAuthn, Status: authn.FactorActive})

		opts, private := r.passkeyStart(t)
		if got := r.answer(t, private, tamperedAssertion(t, a, opts.Challenge, 1)); step.KindOf(got) != step.KindRefused {
			t.Fatalf("an assertion signed elsewhere gave %v, want refused", step.KindOf(got))
		}
	})
}

// A handle nobody holds resolves to no account, and the login is refused.
func TestAPasskeyWithAnUnknownHandleIsRefused(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h)
		a := newAuthenticator(t)
		rec := r.enroll(t, a, profilePasskey, true)
		r.mfa.set(acct, authn.FactorRecord{ID: rec.ID, Account: acct, Kind: authn.FactorWebAuthn, Status: authn.FactorActive})

		opts, private := r.passkeyStart(t)
		stranger := make([]byte, 32)
		if got := r.answer(t, private, a.assertion(t, opts.Challenge, 1, true, stranger)); step.KindOf(got) != step.KindRefused {
			t.Fatalf("an unknown handle gave %v, want refused", step.KindOf(got))
		}
	})
}

// The method answers a login and a step-up, and refuses a purpose a passkey does
// not prove.
func TestTheMethodServesLoginAndStepUpOnly(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h)
		for _, purpose := range []string{authn.PurposeLogin, authn.PurposeStepUp} {
			got, err := r.p.Begin(context.Background(), authn.Start{Purpose: purpose})
			if err != nil {
				t.Fatalf("Begin(%s): %v", purpose, err)
			}
			if step.KindOf(got) != step.KindData {
				t.Errorf("Begin(%s) gave %v, want data", purpose, step.KindOf(got))
			}
		}
		for _, purpose := range []string{authn.PurposeLink, authn.PurposeEnroll} {
			got, err := r.p.Begin(context.Background(), authn.Start{Purpose: purpose})
			if err != nil {
				t.Fatalf("Begin(%s): %v", purpose, err)
			}
			if step.KindOf(got) != step.KindRefused {
				t.Errorf("Begin(%s) gave %v, want refused", purpose, step.KindOf(got))
			}
		}
	})
}

// A step the method never named is refused, so a flow record it did not write
// cannot be walked into a proof.
func TestTheMethodRefusesAStepItDidNotMake(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h)
		got, err := r.p.Answer(context.Background(), authn.FlowState{Purpose: authn.PurposeLogin, Step: "other"},
			authn.StepInput{Action: authn.InputSubmit, Fields: map[string]string{fieldCredential: "{}"}})
		if err != nil {
			t.Fatalf("Answer: %v", err)
		}
		if step.KindOf(got) != step.KindRefused {
			t.Errorf("Answer on an unknown step gave %v, want refused", step.KindOf(got))
		}
	})
}

// handle is the account's stored user handle decoded to bytes, the value a
// discoverable assertion returns.
func (r *rig) handle(t *testing.T) []byte {
	t.Helper()
	h, found, err := r.p.register.handleOf(context.Background(), acct)
	if err != nil || !found {
		t.Fatalf("handleOf: found=%v err=%v", found, err)
	}
	b, err := handleBytesOf(h)
	if err != nil {
		t.Fatalf("handleBytesOf: %v", err)
	}
	return b
}

// passkeyStart begins a passkey login and returns the options and the private
// state that answers it.
func (r *rig) passkeyStart(t *testing.T) (assertionOptions, []byte) {
	t.Helper()
	got, err := r.p.Begin(context.Background(), authn.Start{Purpose: authn.PurposeLogin})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if step.KindOf(got) != step.KindData {
		t.Fatalf("Begin gave %v, want data", step.KindOf(got))
	}
	return decodeAssertion(t, step.DataOf(got)), step.PrivateOf(got)
}

// answer submits a credential body to the login method and returns the step.
func (r *rig) answer(t *testing.T, private, body []byte) authn.Step {
	t.Helper()
	got, err := r.p.Answer(context.Background(), authn.FlowState{Purpose: authn.PurposeLogin, Step: stepPasskey, Private: private},
		authn.StepInput{Action: authn.InputSubmit, Fields: map[string]string{fieldCredential: string(body)}})
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	return got
}

// passkeyAnswer submits a credential body and returns the proof it carried.
func (r *rig) passkeyAnswer(t *testing.T, private, body []byte) authn.Proof {
	t.Helper()
	got := r.answer(t, private, body)
	if step.KindOf(got) != step.KindProven {
		t.Fatalf("the answer gave %v, want proven", step.KindOf(got))
	}
	return step.ProofOf(got)
}
