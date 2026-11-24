package totp

import (
	"context"
	"errors"
	"net/url"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/pquerna/otp"
	pquernaotp "github.com/pquerna/otp/totp"
	gormschema "gorm.io/gorm/schema"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/step"
)

// TestGapOTPAuthURICarriesTheStoredParameters checks that the otpauth address carries the parameters the factor stores.
func TestGapOTPAuthURICarriesTheStoredParameters(t *testing.T) {
	r := newRig(t)
	rec := authn.FactorRecord{ID: "f1", Account: acct, Kind: authn.FactorTOTP, Status: authn.FactorPending}
	got, err := r.factor.beginEnroll(context.Background(), rec)
	if err != nil {
		t.Fatalf("beginEnroll: %v", err)
	}
	options, ok := step.DataOf(got).(enrollmentOptions)
	if !ok {
		t.Fatalf("the registration payload is %T, want enrollmentOptions", step.DataOf(got))
	}
	u, err := url.Parse(options.OTPAuthURI)
	if err != nil {
		t.Fatalf("parsing the otpauth address %q: %v", options.OTPAuthURI, err)
	}
	if u.Host != "totp" {
		t.Errorf("the otpauth address names the type %q, want totp", u.Host)
	}
	if u.Path != "/speed:"+username {
		t.Errorf("the otpauth label is %q, want the issuer and the account's username", u.Path)
	}
	q := u.Query()
	if got := q.Get("digits"); got != "6" {
		t.Errorf("the address advertises digits=%q, want 6", got)
	}
	if got := q.Get("period"); got != "30" {
		t.Errorf("the address advertises period=%q, want 30", got)
	}
	if got := q.Get("algorithm"); got != "SHA1" {
		t.Errorf("the address advertises algorithm=%q, want SHA1", got)
	}
	if got := q.Get("issuer"); got != "speed" {
		t.Errorf("the address advertises issuer=%q, want speed", got)
	}
}

// TestGapAdvanceIsAtomic checks that advance wins only for a counter above the stored one.
func TestGapAdvanceIsAtomic(t *testing.T) {
	r := newRig(t)
	secret := generate(t)
	r.seed("f1", secret)
	ctx := context.Background()

	const counter = 5
	won, err := r.factor.register.advance(ctx, "f1", counter)
	if err != nil || !won {
		t.Fatalf("the first advance = %v, %v, want won", won, err)
	}
	won, err = r.factor.register.advance(ctx, "f1", counter)
	if err != nil {
		t.Fatalf("re-advancing: %v", err)
	}
	if won {
		t.Error("advancing to a counter already stored won, so the write is not scoped to a fresh counter")
	}
	won, err = r.factor.register.advance(ctx, "f1", counter-1)
	if err != nil {
		t.Fatalf("advancing lower: %v", err)
	}
	if won {
		t.Error("advancing to a lower counter won")
	}
}

// TestGapAdvanceAndRemoveAreScopedToTheFactor checks that advance and remove touch only the named factor.
func TestGapAdvanceAndRemoveAreScopedToTheFactor(t *testing.T) {
	r := newRig(t)
	secret := generate(t)
	r.seed("f1", secret)
	ctx := context.Background()
	if err := r.factor.register.insert(ctx, seed{
		FactorID: "f2", Secret: secret, Algorithm: algorithmSHA1, Digits: digitsOfSix, Period: periodSeconds, LastCounter: 2,
	}); err != nil {
		t.Fatalf("seeding the second factor: %v", err)
	}

	if _, err := r.factor.register.advance(ctx, "f1", 5); err != nil {
		t.Fatalf("advancing f1: %v", err)
	}
	other, found, err := r.factor.register.load(ctx, "f2")
	if err != nil || !found {
		t.Fatalf("reading f2: found=%v err=%v", found, err)
	}
	if other.LastCounter != 2 {
		t.Errorf("advancing f1 moved f2's counter to %d, want it left at 2", other.LastCounter)
	}

	if err := r.factor.Remove(ctx, acct, active("f1")); err != nil {
		t.Fatalf("removing f1: %v", err)
	}
	if _, found, err := r.factor.register.load(ctx, "f2"); err != nil || !found {
		t.Errorf("removing f1 took f2's seed with it: found=%v err=%v", found, err)
	}
}

// TestGapBeginEnrollSurfacesASeedWriteFailure checks that a failed seed write is answered as an error.
func TestGapBeginEnrollSurfacesASeedWriteFailure(t *testing.T) {
	r := newRig(t)
	rec := authn.FactorRecord{ID: "f1", Account: acct, Kind: authn.FactorTOTP, Status: authn.FactorPending}
	if _, err := r.factor.beginEnroll(context.Background(), rec); err != nil {
		t.Fatalf("the first beginEnroll: %v", err)
	}
	got, err := r.factor.beginEnroll(context.Background(), rec)
	if err == nil {
		t.Fatalf("a second beginEnroll for one factor answered %v and no error, want a duplicate-key failure",
			step.KindOf(got))
	}
}

// TestGapStartRefusesWhenTheEncryptedColumnDoesNotReadBack checks that a serializer that does not read back what it wrote fails the startup.
func TestGapStartRefusesWhenTheEncryptedColumnDoesNotReadBack(t *testing.T) {
	r := newRig(t)
	gormschema.RegisterSerializer("encrypted", faultySerializer{})
	t.Cleanup(func() { openHandle(t, rootKey) })

	if err := r.factor.register.roundTrip(context.Background()); err == nil {
		t.Error("the round trip accepted a column that did not read back what was written")
	}
}

// TestGapConcurrentConfirmationsOfOneCodeLeaveExactlyOneEnrolled checks that two concurrent confirmations of one code enroll the factor once.
func TestGapConcurrentConfirmationsOfOneCodeLeaveExactlyOneEnrolled(t *testing.T) {
	r := newSerialRig(t)
	rec := authn.FactorRecord{ID: "f1", Account: acct, Kind: authn.FactorTOTP, Status: authn.FactorPending}
	if _, err := r.factor.beginEnroll(context.Background(), rec); err != nil {
		t.Fatalf("beginEnroll: %v", err)
	}
	code := codeAt(t, secretOf(t, r.h, "f1"), base)

	const racers = 2
	start := make(chan struct{})
	results := make([]authn.Step, racers)
	var wg sync.WaitGroup
	for i := range racers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			got, err := r.factor.finishEnroll(context.Background(), rec, input(code))
			if err != nil {
				t.Errorf("a concurrent confirmation: %v", err)
				return
			}
			results[i] = got
		}(i)
	}
	close(start)
	wg.Wait()

	enrolled, replays := 0, 0
	for _, got := range results {
		switch kindOf(t, got) {
		case step.KindEnrolled:
			enrolled++
		case step.KindRefused:
			if errors.Is(step.CauseOf(got), authn.ErrReplay) {
				replays++
			}
		}
	}
	if enrolled != 1 || replays != racers-1 {
		t.Errorf("%d enrollments and %d replays, want one enrollment and %d replays", enrolled, replays, racers-1)
	}
}

// TestGapFinishEnrollRefusesWhenTheSeedIsGone checks that a confirmation without a seed row reads as not enrolled.
func TestGapFinishEnrollRefusesWhenTheSeedIsGone(t *testing.T) {
	r := newRig(t)
	rec := authn.FactorRecord{ID: "f1", Account: acct, Kind: authn.FactorTOTP, Status: authn.FactorPending}
	if _, err := r.factor.beginEnroll(context.Background(), rec); err != nil {
		t.Fatalf("beginEnroll: %v", err)
	}
	if err := r.h.Exec("DELETE FROM "+seedTable+" WHERE factor_id = ?", "f1").Error; err != nil {
		t.Fatalf("deleting the seed: %v", err)
	}
	got, err := r.factor.finishEnroll(context.Background(), rec, input("123456"))
	if err != nil {
		t.Fatalf("finishEnroll: %v", err)
	}
	if k := kindOf(t, got); k != step.KindRefused {
		t.Fatalf("finishEnroll over a missing seed answered %v, want a refusal", k)
	}
	if cause := step.CauseOf(got); !errors.Is(cause, authn.ErrFactorNotEnrolled) {
		t.Errorf("the refusal carried %v, want ErrFactorNotEnrolled", cause)
	}
}

// TestGapVerifyAcceptsAPaddedCode checks that a valid code with surrounding whitespace is accepted.
func TestGapVerifyAcceptsAPaddedCode(t *testing.T) {
	r := newRig(t)
	secret := generate(t)
	r.seed("f1", secret)
	code := codeAt(t, secret, base)
	if k := kindOf(t, r.verify(active("f1"), "  "+code+"\t")); k != step.KindPassed {
		t.Errorf("a code with surrounding whitespace answered %v, want a pass", k)
	}
}

// TestGapClockGuards checks that a code under a clock before the epoch and at the epoch boundary does not wrap the counter.
func TestGapClockGuards(t *testing.T) {
	secret := generate(t)
	s := seed{FactorID: "f1", Secret: secret, Algorithm: algorithmSHA1, Digits: digitsOfSix, Period: periodSeconds}

	if _, ok := match(s, time.Unix(-30, 0), codeAt(t, secret, time.Unix(0, 0))); ok {
		t.Error("a code of the epoch's first step matched under a pre-epoch clock")
	}
	got, ok := match(s, time.Unix(0, 0), codeAt(t, secret, time.Unix(0, 0)))
	if !ok {
		t.Fatal("the first step's code did not match at the epoch's first step")
	}
	if got != 0 {
		t.Errorf("the first step's code matched the counter %d, want 0", got)
	}
}

// TestGapVerifyUsesTheStoredAlgorithm checks that a seed is verified with its stored algorithm.
func TestGapVerifyUsesTheStoredAlgorithm(t *testing.T) {
	r := newRig(t)
	secret := generate(t)
	if err := r.factor.register.insert(context.Background(), seed{
		FactorID: "f1", Secret: secret, Algorithm: "SHA256", Digits: digitsOfSix, Period: periodSeconds,
	}); err != nil {
		t.Fatalf("seeding a SHA256 row: %v", err)
	}
	code := codeWith(t, secret, base, pquernaotp.ValidateOpts{
		Period: periodSeconds, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA256,
	})
	if k := kindOf(t, r.verify(active("f1"), code)); k != step.KindPassed {
		t.Errorf("a SHA256 code answered %v for a SHA256 row, want a pass", k)
	}
}

// TestGapAlgorithmByName checks that every algorithm the column admits maps, and an unknown one is refused.
func TestGapAlgorithmByName(t *testing.T) {
	cases := map[string]otp.Algorithm{
		"SHA1":   otp.AlgorithmSHA1,
		"":       otp.AlgorithmSHA1,
		"SHA256": otp.AlgorithmSHA256,
		"SHA512": otp.AlgorithmSHA512,
		"MD5":    otp.AlgorithmMD5,
	}
	for name, want := range cases {
		got, err := algorithmByName(name)
		if err != nil {
			t.Errorf("algorithmByName(%q): %v", name, err)
			continue
		}
		if got != want {
			t.Errorf("algorithmByName(%q) = %v, want %v", name, got, want)
		}
	}
	if _, err := algorithmByName("DES"); err == nil {
		t.Error("algorithmByName accepted an algorithm it does not generate codes for")
	}
}

type faultySerializer struct{}

func (faultySerializer) Value(context.Context, *gormschema.Field, reflect.Value, any) (any, error) {
	return "sealed", nil
}

func (faultySerializer) Scan(_ context.Context, field *gormschema.Field, dst reflect.Value, _ any) error {
	field.ReflectValueOf(context.Background(), dst).SetString("not what was written")
	return nil
}
