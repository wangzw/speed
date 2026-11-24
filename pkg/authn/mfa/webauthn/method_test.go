package webauthn

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/step"
)

// TestAPasskeyStartRequiresUserVerification checks that a passkey start requires user verification.
func TestAPasskeyStartRequiresUserVerification(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h)
		opts, _ := r.passkeyStart(t)
		if opts.UserVerification != "required" {
			t.Errorf("the passkey assertion asks userVerification = %q, want required", opts.UserVerification)
		}
	})
}

// TestPasskeyLoginProvesAnAccount checks that a passkey login proves the account the client picks.
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

// TestAPasskeyWithoutUserVerificationIsRefused checks that an assertion without user verification is refused.
func TestAPasskeyWithoutUserVerificationIsRefused(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h)
		a := newAuthenticator(t)
		rec := r.enroll(t, a, profilePasskey, true)
		r.mfa.set(acct, authn.FactorRecord{ID: rec.ID, Account: acct, Kind: authn.FactorWebAuthn, Status: authn.FactorActive})
		handle := r.handle(t)

		opts, private := r.passkeyStart(t)
		got := r.answer(t, private, a.assertion(t, opts.Challenge, 1, false, handle))
		if step.KindOf(got) != step.KindRefused {
			t.Fatalf("an assertion without user verification gave %v, want refused", step.KindOf(got))
		}
	})
}

// TestASecondPasskeyAssertionIsRefused checks that the same passkey assertion presented twice is refused.
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

// TestAPasskeyWithATamperedSignatureIsRefused checks that an assertion signed by another key is refused.
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

// TestAPasskeyWithAnUnknownHandleIsRefused checks that a handle nobody holds resolves to no account.
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

// TestTheMethodServesLoginAndStepUpOnly checks that the method serves login and step-up only.
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

// TestTheMethodRefusesAStepItDidNotMake checks that the method refuses a flow record whose step it did not write.
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

func (r *rig) answer(t *testing.T, private, body []byte) authn.Step {
	t.Helper()
	got, err := r.p.Answer(context.Background(), authn.FlowState{Purpose: authn.PurposeLogin, Step: stepPasskey, Private: private},
		authn.StepInput{Action: authn.InputSubmit, Fields: map[string]string{fieldCredential: string(body)}})
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	return got
}

func (r *rig) passkeyAnswer(t *testing.T, private, body []byte) authn.Proof {
	t.Helper()
	got := r.answer(t, private, body)
	if step.KindOf(got) != step.KindProven {
		t.Fatalf("the answer gave %v, want proven", step.KindOf(got))
	}
	return step.ProofOf(got)
}

// TestAssertionOutcomesWriteSecurityLog checks that a clone warning writes a Warn and a replay an Info on the passkey login path.
func TestAssertionOutcomesWriteSecurityLog(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h)
		a := newAuthenticator(t)
		rec := r.enroll(t, a, profilePasskey, true)
		r.mfa.set(acct, authn.FactorRecord{ID: rec.ID, Account: acct, Kind: authn.FactorWebAuthn, Status: authn.FactorActive})
		handle := r.handle(t)
		var logs bytes.Buffer
		r.p.logger = slog.New(slog.NewTextHandler(&logs, nil))

		opts, private := r.passkeyStart(t)
		r.passkeyAnswer(t, private, a.assertion(t, opts.Challenge, 10, true, handle))
		opts, private = r.passkeyStart(t)
		if got := r.answer(t, private, a.assertion(t, opts.Challenge, 5, true, handle)); step.KindOf(got) != step.KindRefused {
			t.Fatalf("an assertion whose counter went backwards gave %v, want refused", step.KindOf(got))
		}
		r.p.answerAssertion(acct, rec.ID, assertionResult{}, assertionReplay)

		lines := logs.String()
		if !strings.Contains(lines, "level=WARN") || !strings.Contains(lines, "possible clone") {
			t.Errorf("the clone warning wrote no Warn line: %s", lines)
		}
		if !strings.Contains(lines, "level=INFO") || !strings.Contains(lines, "presented again") {
			t.Errorf("the replay wrote no Info line: %s", lines)
		}
		if !strings.Contains(lines, "account=acct-1") {
			t.Errorf("the security lines do not name the account: %s", lines)
		}
	})
}
