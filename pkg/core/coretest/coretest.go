// Package coretest holds the value functions the Go test bounds go through, scaling a bound by the race factor and by GATE_TIME_SCALE.
package coretest

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

const envVar = "GATE_TIME_SCALE"

func init() {
	mustReadScale(os.Getenv(envVar))
}

func mustReadScale(raw string) float64 {
	value, err := readScale(raw)
	if err != nil {
		panic(fmt.Errorf("coretest: %w", err))
	}
	return value
}

// Callers compute a bound when the case runs, never in a package-level
// variable, init or TestMain; tools/check_test_limits.py enforces it.
func readScale(raw string) (float64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 1, nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || !isDecimal(raw) || math.IsNaN(value) || math.IsInf(value, 0) || value < 1 {
		return 0, fmt.Errorf("GATE_TIME_SCALE must be a finite number of at least 1; got %q", raw)
	}
	return value, nil
}

func isDecimal(raw string) bool {
	unsigned := strings.TrimPrefix(strings.TrimPrefix(raw, "+"), "-")
	if strings.HasPrefix(unsigned, "0x") || strings.HasPrefix(unsigned, "0X") {
		return false
	}
	return !strings.ContainsRune(raw, '_')
}

// Scale returns d multiplied by the race factor and by GATE_TIME_SCALE, read on every call.
// It panics when the variable holds a value readScale refuses.
// It is for a bound that waits for an event and returns as soon as it happens.
func Scale(d time.Duration) time.Duration {
	return time.Duration(float64(d) * float64(raceFactor) * mustReadScale(os.Getenv(envVar)))
}

// Declared returns d multiplied by GATE_TIME_SCALE alone, with no race factor, read on every call.
// It panics when the variable holds a value readScale refuses.
func Declared(d time.Duration) time.Duration {
	return time.Duration(float64(d) * mustReadScale(os.Getenv(envVar)))
}

// Fataler is what a case is failed through: *testing.T satisfies it.
type Fataler interface {
	// Helper marks the calling function as a test helper.
	Helper()
	// Fatalf fails the case with the given format and arguments.
	Fatalf(format string, args ...any)
}

const meetingMargin = 10

// DeclaredAgainst returns fixed + declared*GATE_TIME_SCALE, the upper bound of a behaviour case,
// and fails the case when that bound reaches nine tenths of wrong, the time a wrong implementation
// needs.
func DeclaredAgainst(t Fataler, fixed, declared, wrong time.Duration) time.Duration {
	t.Helper()
	if declared <= 0 || wrong <= 0 || fixed < 0 {
		t.Fatalf("coretest: DeclaredAgainst needs a positive declared bound and wrong time and a non-negative "+
			"fixed part; got fixed %v, declared %v, wrong %v", fixed, declared, wrong)
	}
	scale := mustReadScale(os.Getenv(envVar))
	limit := fixed + time.Duration(float64(declared)*scale)
	if margin := wrong - wrong/meetingMargin; limit >= margin {
		t.Fatalf("coretest: GATE_TIME_SCALE=%v brings the bound to %v, which has passed nine tenths of the %v a "+
			"wrong implementation needs; the two meet at a scale of about %.2f, so at this scale the case can "+
			"no longer tell them apart", scale, limit, wrong, float64(wrong-fixed)/float64(declared))
	}
	return limit
}
