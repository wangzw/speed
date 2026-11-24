// Package retry paces the read-modify-write loops of the backends without native arithmetic.
package retry

import (
	"math/rand/v2"
	"time"
)

// Backoff waits a jittered moment before the next attempt.
func Backoff(attempt int) {
	ceiling := time.Duration(attempt+1) * 200 * time.Microsecond
	if ceiling > 5*time.Millisecond {
		ceiling = 5 * time.Millisecond
	}
	if ceiling > 0 {
		//nolint:gosec // the jitter only has to spread contenders out; nothing in a retry delay needs a value an attacker cannot predict
		time.Sleep(time.Duration(rand.Int64N(int64(ceiling))))
	}
}
