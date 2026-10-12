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

// The otpauth address a registration answers with carries the parameters the
// factor stores and validates: six digits, a thirty-second step, HMAC-SHA1 and
// the issuer. An address advertising other parameters would draw codes the
// factor never accepts, so enrollment confirmation could never succeed.
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
	if u.Path != "/speed:"+string(acct) {
		t.Errorf("the otpauth label is %q, want the issuer and the account", u.Path)
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

// advance is atomic: the conditional write, not a read, decides which of two
// attempts that present one counter is the one. Re-advancing to a counter that is
// already stored, or to a lower one, loses.
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

// advance and remove are scoped to the one factor: another factor whose counter
// is below the advanced one is untouched by an advance, and its seed survives a
// removal of a different factor.
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

// A seed write that fails is surfaced rather than swallowed: a registration whose
// write hits a duplicate factor row answers an error and not the otpauth address.
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

// The startup round trip reads back what it wrote; a serializer that seals on
// write but returns something else on read is caught there rather than at the
// first enrollment.
func TestGapStartRefusesWhenTheEncryptedColumnDoesNotReadBack(t *testing.T) {
	r := newRig(t)
	// The registration is process-wide; an assembly puts the real serializer back,
	// so one is run in cleanup.
	gormschema.RegisterSerializer("encrypted", faultySerializer{})
	t.Cleanup(func() { openHandle(t, rootKey) })

	if err := r.factor.register.roundTrip(context.Background()); err == nil {
		t.Error("the round trip accepted a column that did not read back what was written")
	}
}

// A confirmation code presented to two racers at once activates the factor
// exactly once: the conditional write lets one through and the other is a replay,
// which is finishEnroll's own lost-write-back branch.
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

// A confirmation step whose seed row is gone is a factor that is not enrolled,
// not a code mismatch, which the multi-factor module treats differently.
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

// A valid code wrapped in surrounding whitespace is still accepted, which is what
// the trim in match provides.
func TestGapVerifyAcceptsAPaddedCode(t *testing.T) {
	r := newRig(t)
	secret := generate(t)
	r.seed("f1", secret)
	code := codeAt(t, secret, base)
	if k := kindOf(t, r.verify(active("f1"), "  "+code+"\t")); k != step.KindPassed {
		t.Errorf("a code with surrounding whitespace answered %v, want a pass", k)
	}
}

// The clock guards refuse a code under a clock set before the epoch and keep the
// step below zero from being taken for the matched one at the epoch boundary, so
// a counter cannot wrap around to a huge value or move to a negative one.
func TestGapClockGuards(t *testing.T) {
	secret := generate(t)
	s := seed{FactorID: "f1", Secret: secret, Algorithm: algorithmSHA1, Digits: digitsOfSix, Period: periodSeconds}

	if _, ok := match(s, time.Unix(-30, 0), codeAt(t, secret, time.Unix(0, 0))); ok {
		t.Error("a code of the epoch's first step matched under a pre-epoch clock")
	}
	// The step below zero produces the same code as the first one, so what the
	// guard fixes is the counter the match is reported at: it must be the first
	// step and not a negative one that a replay check would then refuse.
	got, ok := match(s, time.Unix(0, 0), codeAt(t, secret, time.Unix(0, 0)))
	if !ok {
		t.Fatal("the first step's code did not match at the epoch's first step")
	}
	if got != 0 {
		t.Errorf("the first step's code matched the counter %d, want 0", got)
	}
}

// The stored algorithm column maps to the library's value; a row of another
// algorithm is verified with that algorithm and not the default.
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

// algorithmByName maps every algorithm the column can hold, and refuses one it
// does not generate codes for.
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

// faultySerializer stands in for the encrypted column's serializer and returns a
// value other than the one written, which is the read the startup check must
// catch. It is registered under the encrypted serializer's name for the length of
// one case.
type faultySerializer struct{}

func (faultySerializer) Value(context.Context, *gormschema.Field, reflect.Value, any) (any, error) {
	return "sealed", nil
}

func (faultySerializer) Scan(_ context.Context, field *gormschema.Field, dst reflect.Value, _ any) error {
	field.ReflectValueOf(context.Background(), dst).SetString("not what was written")
	return nil
}
