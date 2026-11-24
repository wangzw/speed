package jobs

import "time"

// Schedule is a periodic job a business module declares as a resource; the
// scheduler module collects the declarations and enqueues one job per window.
type Schedule struct {
	// Key is a globally unique name for this declaration, of the form
	// "<module>.<purpose>", and one input to the window idempotency key.
	Key string
	// Type is the job type to enqueue, matching a registered Handler's Type.
	Type string
	// Interval is the period and the length of a window; it must be greater than
	// zero.
	Interval time.Duration
	// Payload is the data each enqueued job carries.
	Payload []byte
}
