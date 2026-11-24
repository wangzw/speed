// Package coretest holds the value functions the Go test bounds go through:
// the packages of the pkg module and the demo server. The frontend and
// end-to-end tiers read GATE_TIME_SCALE through their own helpers and do not
// import this package.
//
// A test's bounds are written for an idle machine. Under the race detector, and
// on a machine that several gates share, the same work takes longer, and a bound
// meant to catch a hang must not decide a slow machine instead. The functions
// below apply two factors:
//
//   - Scale multiplies a bound by the race factor and by GATE_TIME_SCALE. It is
//     for a bound that waits for an event: a slow machine makes the event later,
//     and the case still passes as soon as it happens.
//   - Declared multiplies by GATE_TIME_SCALE alone. It is for the upper bound of
//     a case that tells a right implementation from a wrong one by how long it
//     took: the race detector slows the program under test, but widening that
//     bound by the same factor erases the difference the case exists to measure.
//     A case using Declared widens the wrong implementation's own time far
//     enough past the bound that the scale a run declares stays below the point
//     where the two meet. The room is finite, and a scale past that point lets
//     the wrong implementation pass.
//   - DeclaredAgainst is Declared for a bound whose wrong implementation has a
//     finite time the case can name. It returns the same scaled bound and fails
//     the case when that bound reaches nine tenths of the wrong time, which is
//     before the two meet: a run past the meeting point fails loudly instead of
//     passing the case silently.
//
// The race factor is 3 when the test binary is built with the race detector and
// 1 otherwise: the program under test runs several times slower there.
// GATE_TIME_SCALE is read from the environment and is 1 when unset, empty, or
// nothing but whitespace: the caller's declaration that this run is slow. It is
// never probed. A run that reads the machine's load decides differently on the
// same commit, which is what the concurrency work exists to rule out. Any other
// value must be a decimal number of at least 1, fractions and exponents
// included; anything else stops the run with a message naming the
// requirement, because a caller that passes the wrong value has to learn it at
// once rather than get the default back. The wording is the one the frontend's
// readers of the same variable use.
//
// The value is read on every call. A caller must compute its bounds when the
// case runs, never in a package-level variable, an init function or TestMain;
// tools/check_test_limits.py enforces it.
//
// init refuses a value the variable cannot mean before the first case runs.
//
// A lower bound -- "this took at least X" -- goes through none of them. A
// slow machine only makes such a bound easier to satisfy, and scaling it would
// stop it from holding the code to having waited at all.
package coretest

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

// envVar names the caller's scale. The Makefile and the frontend's test
// configuration carry the same name, so one value reaches every tier of a run.
const envVar = "GATE_TIME_SCALE"

// init refuses a value the variable cannot mean before the first case runs.
func init() {
	mustReadScale(os.Getenv(envVar))
}

// mustReadScale turns a refused value into the error that stops the binary.
func mustReadScale(raw string) float64 {
	value, err := readScale(raw)
	if err != nil {
		panic(fmt.Errorf("coretest: %w", err))
	}
	return value
}

// readScale parses GATE_TIME_SCALE, answering 1 for a value that is unset,
// empty, or nothing but whitespace. Any other value must be a decimal number of
// at least 1: the variable exists to raise a bound under load, and neither a
// value that is not a number nor one that lowers a bound may fall back to 1
// unnoticed. Hexadecimal, digit-separating underscores and non-finite values
// are refused as well: ParseFloat's extra literal forms are not the decimal
// numbers the variable means.
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

// isDecimal reports whether raw is a decimal number. Go's ParseFloat also reads
// hexadecimal floats (0x1p4) and digit-separating underscores (1_000); neither
// is a decimal number, and a caller who writes one asked for something the
// variable does not mean.
func isDecimal(raw string) bool {
	unsigned := strings.TrimPrefix(strings.TrimPrefix(raw, "+"), "-")
	if strings.HasPrefix(unsigned, "0x") || strings.HasPrefix(unsigned, "0X") {
		return false
	}
	return !strings.ContainsRune(raw, '_')
}

// Scale returns d multiplied by the race factor and by GATE_TIME_SCALE, read on
// every call. It panics when the variable holds a value readScale refuses. It is
// for a bound that waits for an event and returns as soon as it happens.
func Scale(d time.Duration) time.Duration {
	return time.Duration(float64(d) * float64(raceFactor) * mustReadScale(os.Getenv(envVar)))
}

// Declared returns d multiplied by GATE_TIME_SCALE alone, with no race factor,
// read on every call. It panics when the variable holds a value readScale
// refuses. It is for the upper bound of a case that detects behaviour: the bound
// has to stay below the time a wrong implementation needs, and the race factor
// belongs to the program under test rather than to the difference being measured.
func Declared(d time.Duration) time.Duration {
	return time.Duration(float64(d) * mustReadScale(os.Getenv(envVar)))
}

// Fataler is what a case is failed through: *testing.T satisfies it. The
// interface is what lets this package's own cases observe the failure instead
// of stopping the run: testing.TB carries a method no other package can
// implement, so a recorder could not stand in for it, and a case checking the
// points around the margin would have to fail the test that exercises them.
type Fataler interface {
	Helper()
	Fatalf(format string, args ...any)
}

// meetingMargin is the share of a wrong implementation's time at which
// DeclaredAgainst stops the case: nine tenths, because the wrong time is a
// coarse figure, a product default or a poll interval, and the elapsed time a
// case reads jitters. Failing at nine tenths leaves the tenth as a guard band,
// so the case fails before the scaled bound and the wrong time overlap rather
// than straddling the point where one run passes and the next does not.
const meetingMargin = 10

// DeclaredAgainst returns fixed + declared*GATE_TIME_SCALE, the upper bound of a
// behaviour case, and fails the case when that bound reaches nine tenths of
// wrong, the time a wrong implementation needs. It reads GATE_TIME_SCALE on
// every call and panics when the variable holds a value readScale refuses.
//
// fixed is the part of the bound the declaration does not scale, and declared
// is the part it does; a bound with no fixed part passes 0. wrong is what the
// wrong implementation costs, which the case widens far past the bound when it
// controls that time: a product default or a poll interval the case cannot
// move is the wrong time as it stands.
//
// The two meet at the scale where the whole bound equals wrong. A run at or
// past it can no longer tell the right implementation from the wrong one, so
// the function fails rather than returning a bound the case would pass
// silently. The failure names the scale, the bound, the wrong time and the
// meeting point.
func DeclaredAgainst(t Fataler, fixed, declared, wrong time.Duration) time.Duration {
	t.Helper()
	if declared <= 0 || wrong <= 0 || fixed < 0 {
		t.Fatalf("coretest: DeclaredAgainst needs a positive declared bound and wrong time and a non-negative "+
			"fixed part; got fixed %v, declared %v, wrong %v", fixed, declared, wrong)
	}
	// One read for the whole call: the bound and the failure both report the
	// scale this call used, not a second reading that could differ.
	scale := mustReadScale(os.Getenv(envVar))
	limit := fixed + time.Duration(float64(declared)*scale)
	if margin := wrong - wrong/meetingMargin; limit >= margin {
		t.Fatalf("coretest: GATE_TIME_SCALE=%v brings the bound to %v, which has passed nine tenths of the %v a "+
			"wrong implementation needs; the two meet at a scale of about %.2f, so at this scale the case can "+
			"no longer tell them apart", scale, limit, wrong, float64(wrong-fixed)/float64(declared))
	}
	return limit
}
