// Package counter holds the rule every key-value backend's increments share:
// a counter holds a finite number.
//
// Each backend does its arithmetic where its machinery puts it — in this
// process for some, in the server for others — so the rule is checked at
// different points, but it is one rule with one wording, and it lives here so
// the backends cannot drift apart on either.
package counter

import (
	"fmt"
	"math"
)

// CheckDelta refuses a delta that is not a finite number, before any backend
// is touched.
//
// NaN and the infinities are not an amount to add. Left to the backends, one
// would store the result as the text "NaN", another would reject the delta as
// though the stored value were at fault, and a key that was never written
// would come into existence holding something no increment can move again.
func CheckDelta(module, key string, delta float64) error {
	if math.IsNaN(delta) || math.IsInf(delta, 0) {
		return fmt.Errorf("%s: %v is not an amount a counter can be incremented by (key %q)", module, delta, key)
	}
	return nil
}

// CheckResult refuses an increment whose result is not a finite float64. A
// backend calls it before it writes, so the refusal leaves the counter as it
// was.
//
// The error is not the non-numeric sentinel: the stored value is a number, and
// it is the sum that no float64 holds.
func CheckResult(module, key string, result float64) error {
	if math.IsNaN(result) || math.IsInf(result, 0) {
		return OutOfRange(module, key)
	}
	return nil
}

// OutOfRange is the refusal CheckResult gives, for a backend whose server does
// the arithmetic and reports the overflow itself.
func OutOfRange(module, key string) error {
	return fmt.Errorf("%s: the increment would leave the counter under %q past what a float64 holds, "+
		"so the counter is left as it was", module, key)
}
