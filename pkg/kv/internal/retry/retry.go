// Package retry is the pacing of the retry loops that the backends without
// native arithmetic run their swaps and increments in.
//
// Two of them — nats and memcached — read, add and write back under a revision
// or a cas id, and both need contenders spread out rather than retrying in
// lockstep. The pace is one rule, so it lives here rather than once per
// backend.
package retry

import (
	"math/rand/v2"
	"time"
)

// Backoff waits a jittered moment before the next attempt.
//
// Without it the contenders retry in lockstep and keep colliding: each round
// produces one winner and the rest immediately try again, which is the shape
// that once made a swap loop exhaust its budget under fifty writers. The wait
// grows with the attempt count and is capped, so a quiet key pays nothing and
// a hot one spreads its writers out.
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
