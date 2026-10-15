package jobs

import "time"

// Schedule is a periodic job a business module declares as a resource. The
// module puts one in its own resources, and the scheduler module collects them
// and enqueues one job per window.
//
// It is a resource rather than a call because the declaration has to match what
// this run actually drives: a declaration describes work to do, its effect
// depends on the declaring module being driven and its handler being
// registered, so a module that was only named statically is not built, has no
// handler, and its declaration does not fire. The set a schedule is drawn from
// is therefore the participating set, and a declaration whose module loses
// resolution drops out.
//
// A module declares a periodic job where a per-replica loop would not do: a
// per-replica loop keeps each replica's own state or sweeps each replica's own
// rows, while a Schedule is for work that must happen once across replicas,
// which the scheduler reconciles through the job table's window idempotency key
// rather than through leader election.
type Schedule struct {
	// Key is a globally unique name for this declaration, of the form
	// "<module>.<purpose>". It names the declaration and it is one input to the
	// window idempotency key, so it has to be unique across the declarations in
	// force.
	Key string
	// Type is the job type to enqueue, and it has to match the Type of a
	// registered Handler.
	Type string
	// Interval is the period, and it is also the length of a window. It has to
	// be greater than zero.
	Interval time.Duration
	// Payload is the data each enqueued job carries. Like a Task's payload, it
	// is opaque to this capability.
	Payload []byte
}
