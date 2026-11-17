// Package coretest holds the value functions the Go test bounds go through:
// the packages of the pkg module and the demo server. The frontend and
// end-to-end tiers read GATE_TIME_SCALE through their own helpers and do not
// import this package.
//
// A test's bounds are written for an idle machine. Under the race detector, and
// on a machine that several gates share, the same work takes longer, and a bound
// meant to catch a hang must not decide a slow machine instead. Two functions
// apply two factors:
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
//     where the two meet, and the case states that point: the room is finite,
//     and a scale past it lets the wrong implementation pass.
//
// The race factor is 3 when the test binary is built with the race detector and
// 1 otherwise: the program under test runs several times slower there.
// GATE_TIME_SCALE is read from the environment and is 1 when unset, empty, or
// nothing but whitespace: the caller's declaration that this run is slow. It is
// never probed. A run that reads the machine's load decides differently on the
// same commit, which is what the concurrency work exists to rule out. Any other
// value must be a decimal number of at least 1, fractions and exponents
// included; anything else stops the run at startup with a message naming the
// requirement, because a caller that passes the wrong value has to learn it at
// once rather than get the default back. The wording is the one the frontend's
// readers of the same variable use.
//
// A lower bound -- "this took at least X" -- goes through neither function. A
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

// scale is the caller's declaration, read once at startup.
var scale = mustReadScale(os.Getenv(envVar))

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

// Scale returns d multiplied by the race factor and by GATE_TIME_SCALE. It is
// for a bound that waits for an event and returns as soon as it happens.
func Scale(d time.Duration) time.Duration {
	return time.Duration(float64(d) * float64(raceFactor) * scale)
}

// Declared returns d multiplied by GATE_TIME_SCALE alone, with no race factor.
// It is for the upper bound of a case that detects behaviour: the bound has to
// stay below the time a wrong implementation needs, and the race factor belongs
// to the program under test rather than to the difference being measured.
func Declared(d time.Duration) time.Duration {
	return time.Duration(float64(d) * scale)
}
