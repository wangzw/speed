package authn

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"
)

// fakeSettings is a SettingsReader whose answers are scripted per key, so
// the resolution helpers' arms -- explicit row, unset row, read failure --
// can each be pinned without a config module.
type fakeSettings struct {
	durations map[string]time.Duration
	ints      map[string]int64
	strings   map[string]string
	set       map[string]bool // keys with ok=true
	failKeys  map[string]error
}

func newFakeSettings() *fakeSettings {
	return &fakeSettings{
		durations: map[string]time.Duration{},
		ints:      map[string]int64{},
		strings:   map[string]string{},
		set:       map[string]bool{},
		failKeys:  map[string]error{},
	}
}

func (f *fakeSettings) Duration(_ context.Context, key string) (time.Duration, bool, error) {
	if err := f.failKeys[key]; err != nil {
		return 0, false, err
	}
	return f.durations[key], f.set[key], nil
}

func (f *fakeSettings) Int(_ context.Context, key string) (int64, bool, error) {
	if err := f.failKeys[key]; err != nil {
		return 0, false, err
	}
	return f.ints[key], f.set[key], nil
}

func (f *fakeSettings) String(_ context.Context, key string) (string, bool, error) {
	if err := f.failKeys[key]; err != nil {
		return "", false, err
	}
	return f.strings[key], f.set[key], nil
}

// TestDurationSetting_FallbackArms pins the three fallback arms shared by
// every duration read: no reader, no explicit row, and a failed read all
// leave the construction-time value in place; an explicit positive row wins.
func TestDurationSetting_FallbackArms(t *testing.T) {
	ctx := context.Background()
	fallback := 30 * time.Minute

	if got := durationSetting(ctx, nil, ConfigKeySessionTTL, fallback); got != fallback {
		t.Fatalf("no reader = %v; want the fallback %v", got, fallback)
	}

	reader := newFakeSettings()
	if got := durationSetting(ctx, reader, ConfigKeySessionTTL, fallback); got != fallback {
		t.Fatalf("unset row = %v; want the fallback %v", got, fallback)
	}

	reader.failKeys[ConfigKeySessionTTL] = errors.New("store down")
	if got := durationSetting(ctx, reader, ConfigKeySessionTTL, fallback); got != fallback {
		t.Fatalf("failed read = %v; want the fallback %v", got, fallback)
	}

	delete(reader.failKeys, ConfigKeySessionTTL)
	reader.durations[ConfigKeySessionTTL] = 2 * time.Hour
	reader.set[ConfigKeySessionTTL] = true
	if got := durationSetting(ctx, reader, ConfigKeySessionTTL, fallback); got != 2*time.Hour {
		t.Fatalf("explicit row = %v; want the row's 2h", got)
	}

	// A non-positive explicit row is nonsense for these keys and is
	// refused back to the fallback rather than applied.
	reader.durations[ConfigKeySessionTTL] = 0
	if got := durationSetting(ctx, reader, ConfigKeySessionTTL, fallback); got != fallback {
		t.Fatalf("non-positive row = %v; want the fallback %v", got, fallback)
	}
}

// TestTrustedProviderList_SecurityArms pins the one read that does NOT fall
// back on error: the trusted-provider list. An explicit row wins (empty
// meaning "trust nothing"), an unset row falls back to the construction-time
// list, and a FAILED READ fails closed to nothing trusted.
func TestTrustedProviderList_SecurityArms(t *testing.T) {
	ctx := context.Background()
	svc := &Service{trustedProviders: []string{"google"}}

	if got := svc.trustedProviderList(ctx); len(got) != 1 || got[0] != "google" {
		t.Fatalf("no reader = %v; want the construction-time list", got)
	}

	reader := newFakeSettings()
	svc.settings = reader
	if got := svc.trustedProviderList(ctx); len(got) != 1 || got[0] != "google" {
		t.Fatalf("unset row = %v; want the construction-time fallback", got)
	}

	reader.set[ConfigKeyTrustedProviders] = true
	reader.strings[ConfigKeyTrustedProviders] = "github   feishu"
	got := svc.trustedProviderList(ctx)
	if len(got) != 2 || got[0] != "github" || got[1] != "feishu" {
		t.Fatalf("explicit row = %v; want [github feishu]", got)
	}

	// An explicit EMPTY row disables linking, overriding the
	// construction-time list.
	reader.strings[ConfigKeyTrustedProviders] = ""
	if got := svc.trustedProviderList(ctx); len(got) != 0 {
		t.Fatalf("explicit empty row = %v; want nothing trusted", got)
	}

	// A read FAILURE fails closed: not the construction-time fallback,
	// which could resurrect auto-linking an operator had turned off.
	reader.failKeys[ConfigKeyTrustedProviders] = errors.New("store down")
	if got := svc.trustedProviderList(ctx); len(got) != 0 {
		t.Fatalf("failed read = %v; want the fail-closed empty list", got)
	}
}

// TestPasswordPolicyFor_CoherenceGuard pins the policy resolution: each
// bound reads independently, and an incoherent pair (min > max, or a
// non-positive bound) keeps the whole construction-time policy instead of
// half-applying a policy that would refuse every password.
func TestPasswordPolicyFor_CoherenceGuard(t *testing.T) {
	ctx := context.Background()
	svc := &Service{policy: PasswordPolicy{MinLength: 12, MaxLength: 128}}

	reader := newFakeSettings()
	svc.settings = reader
	if got := svc.passwordPolicyFor(ctx); got.MinLength != svc.policy.MinLength || got.MaxLength != svc.policy.MaxLength {
		t.Fatalf("no rows = %+v; want the construction-time policy %+v", got, svc.policy)
	}

	reader.set[ConfigKeyPasswordMinLength] = true
	reader.ints[ConfigKeyPasswordMinLength] = 16
	reader.set[ConfigKeyPasswordMaxLength] = true
	reader.ints[ConfigKeyPasswordMaxLength] = 64
	if got := svc.passwordPolicyFor(ctx); got.MinLength != 16 || got.MaxLength != 64 {
		t.Fatalf("explicit pair = %+v; want 16/64", got)
	}

	// min above max: the whole resolved policy is refused.
	reader.ints[ConfigKeyPasswordMinLength] = 100
	if got := svc.passwordPolicyFor(ctx); got.MinLength != svc.policy.MinLength || got.MaxLength != svc.policy.MaxLength {
		t.Fatalf("incoherent pair = %+v; want the construction-time policy", got)
	}

	// A non-positive bound is incoherent too.
	reader.ints[ConfigKeyPasswordMinLength] = 0
	if got := svc.passwordPolicyFor(ctx); got.MinLength != svc.policy.MinLength || got.MaxLength != svc.policy.MaxLength {
		t.Fatalf("non-positive min = %+v; want the construction-time policy", got)
	}
}

// TestPasswordPolicyFor_UnrepresentableBounds pins the refusal of a bound
// that does not fit an int on every supported platform: the whole
// construction-time policy stays. On a 32-bit platform the narrowing would
// truncate such a bound into a plausible-looking length that passes the
// coherence check, so the refusal cannot be left to that check.
func TestPasswordPolicyFor_UnrepresentableBounds(t *testing.T) {
	ctx := context.Background()
	svc := &Service{policy: PasswordPolicy{MinLength: 12, MaxLength: 128}}

	reader := newFakeSettings()
	svc.settings = reader
	reader.set[ConfigKeyPasswordMinLength] = true
	reader.ints[ConfigKeyPasswordMinLength] = 1<<32 + 100
	reader.set[ConfigKeyPasswordMaxLength] = true
	reader.ints[ConfigKeyPasswordMaxLength] = 1<<32 + 200

	if got := svc.passwordPolicyFor(ctx); got.MinLength != svc.policy.MinLength || got.MaxLength != svc.policy.MaxLength {
		t.Fatalf("unrepresentable bounds = %+v; want the construction-time policy %+v", got, svc.policy)
	}

	// The maximum's own guard, with the minimum representable: the
	// minimum's refusal must not be what pins the pair's outcome.
	reader.ints[ConfigKeyPasswordMinLength] = 16
	if got := svc.passwordPolicyFor(ctx); got.MinLength != svc.policy.MinLength || got.MaxLength != svc.policy.MaxLength {
		t.Fatalf("unrepresentable max = %+v; want the construction-time policy %+v", got, svc.policy)
	}

	// And the minimum's own guard, against a maximum it would stay
	// coherent with after a 32-bit truncation (100 against 1024): there
	// the coherence check below could not catch the narrowed bound.
	reader.ints[ConfigKeyPasswordMinLength] = 1<<32 + 100
	reader.ints[ConfigKeyPasswordMaxLength] = 1024
	if got := svc.passwordPolicyFor(ctx); got.MinLength != svc.policy.MinLength || got.MaxLength != svc.policy.MaxLength {
		t.Fatalf("unrepresentable min = %+v; want the construction-time policy %+v", got, svc.policy)
	}
}

// TestIntSetting_UnrepresentableValueFallsBack pins the arm that keeps the
// narrowing total on every platform: a row inside the read's envelope but
// bigger than an int holds on a 32-bit one keeps the fallback. The envelope
// here is deliberately wide -- the SMS budget's own envelope refuses such a
// row a step earlier.
func TestIntSetting_UnrepresentableValueFallsBack(t *testing.T) {
	ctx := context.Background()
	reader := newFakeSettings()
	reader.set[ConfigKeySMSCodeMaxAttempts] = true
	reader.ints[ConfigKeySMSCodeMaxAttempts] = 1 << 40

	if got := intSetting(ctx, reader, ConfigKeySMSCodeMaxAttempts, 5, 1, math.MaxInt64); got != 5 {
		t.Fatalf("unrepresentable row = %d; want the fallback 5", got)
	}
}

// TestSMSCodeMaxAttemptsFor_RowsOutsideTheAcceptedRange pins the range arm
// every intSetting read shares: a row outside the accepted envelope --
// including one no int can hold, which this call site's 1..1000 envelope
// refuses before any narrowing sees it -- keeps the construction-time
// budget, and an in-range row is returned as read.
func TestSMSCodeMaxAttemptsFor_RowsOutsideTheAcceptedRange(t *testing.T) {
	ctx := context.Background()
	svc := &Service{smsCodeMaxAttempts: 5}

	reader := newFakeSettings()
	svc.settings = reader
	reader.set[ConfigKeySMSCodeMaxAttempts] = true

	for _, row := range []int64{0, 5000, 1 << 32} {
		reader.ints[ConfigKeySMSCodeMaxAttempts] = row
		if got := svc.smsCodeMaxAttemptsFor(ctx); got != svc.smsCodeMaxAttempts {
			t.Errorf("row %d = %d; want the construction-time budget %d", row, got, svc.smsCodeMaxAttempts)
		}
	}

	reader.ints[ConfigKeySMSCodeMaxAttempts] = 7
	if got := svc.smsCodeMaxAttemptsFor(ctx); got != 7 {
		t.Errorf("in-range row = %d; want 7", got)
	}
}

// TestProviderCredentialsFor_Arms pins the credential resolution: both keys
// must carry explicit non-empty rows for the pair to win; unset, half-set or
// failed reads report "no override" so the provider keeps its own pair.
func TestProviderCredentialsFor_Arms(t *testing.T) {
	ctx := context.Background()
	svc := &Service{}
	reader := newFakeSettings()
	svc.settings = reader

	if _, _, ok := svc.providerCredentialsFor(ctx, ProviderGoogle); ok {
		t.Fatal("no rows: ok = true; want the provider to keep its own credentials")
	}

	reader.set[ConfigKeyGoogleClientID] = true
	reader.strings[ConfigKeyGoogleClientID] = "client-1"
	if _, _, ok := svc.providerCredentialsFor(ctx, ProviderGoogle); ok {
		t.Fatal("half-configured (id only): ok = true; want no override")
	}

	reader.set[ConfigKeyGoogleClientSecret] = true
	reader.strings[ConfigKeyGoogleClientSecret] = "secret-1"
	id, secret, ok := svc.providerCredentialsFor(ctx, ProviderGoogle)
	if !ok || id != "client-1" || secret != "secret-1" {
		t.Fatalf("full pair = (%q, %q, %v); want (client-1, secret-1, true)", id, secret, ok)
	}

	reader.failKeys[ConfigKeyGoogleClientSecret] = errors.New("store down")
	if _, _, ok := svc.providerCredentialsFor(ctx, ProviderGoogle); ok {
		t.Fatal("failed read: ok = true; want no override")
	}
	delete(reader.failKeys, ConfigKeyGoogleClientSecret)

	// A channel this module did not ship has no declared keys.
	if _, _, ok := svc.providerCredentialsFor(ctx, "custom-channel"); ok {
		t.Fatal("unmapped channel: ok = true; want no override")
	}
}
