package totp

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/pquerna/otp"
	pquernaotp "github.com/pquerna/otp/totp"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/step"
)

// verify runs the factor's Verify over the record and code given.
func (r *rig) verify(rec authn.FactorRecord, code string) authn.Step {
	r.t.Helper()
	got, err := r.factor.Verify(context.Background(), flow(), rec, input(code))
	if err != nil {
		r.t.Fatalf("Verify: %v", err)
	}
	return got
}

// TestKindIsTOTP holds the one kind this module delivers.
func TestKindIsTOTP(t *testing.T) {
	r := newRig(t)
	if got := r.factor.Kind(); got != authn.FactorTOTP {
		t.Errorf("Kind = %q, want %q", got, authn.FactorTOTP)
	}
}

// TestVerifyAcceptsTheCodeOfTheCurrentStep holds the ordinary pass and the pass
// it hands over: the factor reports the otp method.
func TestVerifyAcceptsTheCodeOfTheCurrentStep(t *testing.T) {
	r := newRig(t)
	secret := generate(t)
	r.seed("f1", secret)

	got := r.verify(active("f1"), codeAt(t, secret, base))
	if k := kindOf(t, got); k != step.KindPassed {
		t.Fatalf("Verify answered %v, want a pass", k)
	}
	pass := step.PassOf(got)
	if len(pass.Methods) != 1 || pass.Methods[0] != authn.MethodTOTP {
		t.Errorf("the pass carries %v, want the otp method alone", pass.Methods)
	}
}

// TestVerifyAcceptsOneStepEitherSide holds the fixed window: a code one step
// behind or ahead of the current one is accepted, and a fresh row accepts each.
func TestVerifyAcceptsOneStepEitherSide(t *testing.T) {
	cases := []struct {
		name string
		at   time.Time
	}{
		{"one step behind", base.Add(-periodSeconds * time.Second)},
		{"one step ahead", base.Add(periodSeconds * time.Second)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			secret := generate(t)
			r.seed("f1", secret)
			if k := kindOf(t, r.verify(active("f1"), codeAt(t, secret, c.at))); k != step.KindPassed {
				t.Errorf("Verify answered %v, want a pass", k)
			}
		})
	}
}

// TestVerifyRefusesACodeTwoStepsOut holds the other side of the window: a code
// two steps either way does not match, and the refusal leaves the counter where
// it was, so the code of the current step still passes afterwards.
func TestVerifyRefusesACodeTwoStepsOut(t *testing.T) {
	for _, c := range []struct {
		name string
		at   time.Time
	}{
		{"two steps behind", base.Add(-2 * periodSeconds * time.Second)},
		{"two steps ahead", base.Add(2 * periodSeconds * time.Second)},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			secret := generate(t)
			r.seed("f1", secret)
			if k := kindOf(t, r.verify(active("f1"), codeAt(t, secret, c.at))); k != step.KindRefused {
				t.Fatalf("Verify answered %v, want a refusal", k)
			}
			if k := kindOf(t, r.verify(active("f1"), codeAt(t, secret, base))); k != step.KindPassed {
				t.Errorf("the current step's code was refused after a refusal two steps out: %v", k)
			}
		})
	}
}

// TestVerifyRefusesACodeBuiltWithOtherParameters holds the negative vectors: a
// code generated for another algorithm, another digit count or another period
// does not match the seed, whatever the seed's own parameters are.
func TestVerifyRefusesACodeBuiltWithOtherParameters(t *testing.T) {
	secret := generate(t)
	cases := []struct {
		name string
		opts pquernaotp.ValidateOpts
	}{
		{"sha256", pquernaotp.ValidateOpts{Period: periodSeconds, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA256}},
		{"sha512", pquernaotp.ValidateOpts{Period: periodSeconds, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA512}},
		{"eight digits", pquernaotp.ValidateOpts{Period: periodSeconds, Digits: otp.DigitsEight, Algorithm: otp.AlgorithmSHA1}},
		{"sixty second step", pquernaotp.ValidateOpts{Period: 60, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			r.seed("f1", secret)
			code := codeWith(t, secret, base, c.opts)
			if k := kindOf(t, r.verify(active("f1"), code)); k != step.KindRefused {
				t.Errorf("Verify answered %v for a code of other parameters, want a refusal", k)
			}
		})
	}
}

// TestVerifyRefusesAForgedCode holds that a well-formed code that is not the
// seed's is refused.
func TestVerifyRefusesAForgedCode(t *testing.T) {
	r := newRig(t)
	secret := generate(t)
	r.seed("f1", secret)
	forged := "000000"
	if realCode := codeAt(t, secret, base); realCode == forged {
		forged = "000001"
	}
	if k := kindOf(t, r.verify(active("f1"), forged)); k != step.KindRefused {
		t.Errorf("Verify answered %v for a forged code, want a refusal", k)
	}
}

// TestVerifyRefusesTheSameCodeTwiceAsAReplay holds the replay protection: the
// second presentation of the counter that just passed is ErrReplay.
func TestVerifyRefusesTheSameCodeTwiceAsAReplay(t *testing.T) {
	r := newRig(t)
	secret := generate(t)
	r.seed("f1", secret)
	code := codeAt(t, secret, base)

	if k := kindOf(t, r.verify(active("f1"), code)); k != step.KindPassed {
		t.Fatalf("the first presentation answered %v, want a pass", k)
	}
	got := r.verify(active("f1"), code)
	if k := kindOf(t, got); k != step.KindRefused {
		t.Fatalf("the replay answered %v, want a refusal", k)
	}
	if cause := step.CauseOf(got); !errors.Is(cause, authn.ErrReplay) {
		t.Errorf("the replay carried %v, want ErrReplay", cause)
	}
}

// TestTwoConcurrentVerificationsOfOneCodeLeaveExactlyOneTheWinner holds the
// design's assumption: the conditional write, not a read, decides which of two
// attempts that present one code is the one, on this engine.
func TestTwoConcurrentVerificationsOfOneCodeLeaveExactlyOneTheWinner(t *testing.T) {
	r := newSerialRig(t)
	secret := generate(t)
	r.seed("f1", secret)
	code := codeAt(t, secret, base)

	const racers = 2
	start := make(chan struct{})
	results := make([]authn.Step, racers)
	var wg sync.WaitGroup
	for i := range racers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			got, err := r.factor.Verify(context.Background(), flow(), active("f1"), input(code))
			if err != nil {
				t.Errorf("a concurrent Verify: %v", err)
				return
			}
			results[i] = got
		}(i)
	}
	close(start)
	wg.Wait()

	passes, replays := 0, 0
	for _, got := range results {
		switch kindOf(t, got) {
		case step.KindPassed:
			passes++
		case step.KindRefused:
			if errors.Is(step.CauseOf(got), authn.ErrReplay) {
				replays++
			}
		}
	}
	if passes != 1 || replays != racers-1 {
		t.Errorf("%d passes and %d replays, want one pass and %d replays", passes, replays, racers-1)
	}
}

// TestVerifyRefusesAFactorThatIsNotActive holds that a row that is not active is
// never used to verify, whatever its seed says: the zero status is pending.
func TestVerifyRefusesAFactorThatIsNotActive(t *testing.T) {
	r := newRig(t)
	secret := generate(t)
	r.seed("f1", secret)
	code := codeAt(t, secret, base)

	for _, status := range []authn.FactorStatus{authn.FactorPending, authn.FactorDisabled} {
		rec := active("f1")
		rec.Status = status
		got := r.verify(rec, code)
		if k := kindOf(t, got); k != step.KindRefused {
			t.Errorf("a factor of status %d answered %v, want a refusal", status, k)
		}
		if cause := step.CauseOf(got); !errors.Is(cause, authn.ErrFactorNotEnrolled) {
			t.Errorf("a factor of status %d carried %v, want ErrFactorNotEnrolled", status, cause)
		}
	}
}

// TestVerifyRefusesAFactorWhoseSeedIsGone holds that a row with no seed is a
// factor that is not enrolled, not an outage.
func TestVerifyRefusesAFactorWhoseSeedIsGone(t *testing.T) {
	r := newRig(t)
	got := r.verify(active("missing"), "123456")
	if k := kindOf(t, got); k != step.KindRefused {
		t.Fatalf("Verify answered %v, want a refusal", k)
	}
	if cause := step.CauseOf(got); !errors.Is(cause, authn.ErrFactorNotEnrolled) {
		t.Errorf("the refusal carried %v, want ErrFactorNotEnrolled", cause)
	}
}

// TestChallengeAsksForACodeAndSendsNothing holds that a TOTP has nothing to
// send: its challenge is a prompt for a code.
func TestChallengeAsksForACodeAndSendsNothing(t *testing.T) {
	r := newRig(t)
	got, err := r.factor.Challenge(context.Background(), flow(), active("f1"))
	if err != nil {
		t.Fatalf("Challenge: %v", err)
	}
	if k := kindOf(t, got); k != step.KindPrompt {
		t.Fatalf("Challenge answered %v, want a prompt", k)
	}
	// The field name is the client's contract, so it is pinned to the literal and
	// not to the constant this factor happens to read the code by.
	fields := step.FieldsOf(got)
	if len(fields) != 1 || fields[0].Name != "code" || !fields[0].Required {
		t.Errorf("the prompt asks for %+v, want a required %q", fields, "code")
	}
	if n := step.SendsOf(got); n != 0 {
		t.Errorf("Challenge reported %d sends, want none", n)
	}
}

// TestRemoveIsIdempotent holds that removing a factor's seed twice is not an
// error, because the row that names the factor is not this module's.
func TestRemoveIsIdempotent(t *testing.T) {
	r := newRig(t)
	secret := generate(t)
	r.seed("f1", secret)

	for i := 0; i < 2; i++ {
		if err := r.factor.Remove(context.Background(), acct, active("f1")); err != nil {
			t.Fatalf("Remove call %d: %v", i+1, err)
		}
	}
	var count int64
	if err := r.h.Raw("SELECT COUNT(*) FROM "+seedTable+" WHERE factor_id = ?", "f1").Scan(&count).Error; err != nil {
		t.Fatalf("counting the seeds: %v", err)
	}
	if count != 0 {
		t.Errorf("%d seed rows remain after two removals, want none", count)
	}
}
