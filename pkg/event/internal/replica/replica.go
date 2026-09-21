// Package replica names one process among the replicas of an assembly.
//
// The backends that have to tell replicas apart — redis (its consumer groups),
// nats (its durable consumers) and postgres (its delivery marks) — name them
// the same way, and a name is what an operator reads on a server or in a table
// row, so the rule lives here rather than once per backend.
package replica

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"time"
)

// Name mints a replica name for this process: the host, a hyphen, and eight
// random bytes in hex.
//
// Each call produces a fresh name. The caller takes one when it needs it — the
// name of this process's consumer group, its durable consumer, its row in the
// delivered table — and holds it for as long as that name has to live; calling
// twice and expecting the same answer is not what this is for.
//
// The host makes a replica recognizable to an operator looking at the server;
// the random part keeps two processes on one host apart. A hostname or random
// source that fails yields a time-based fallback rather than an error: the name
// only has to be unique enough to be a replica of its own, and there is nothing
// an operator could act on in a failure to generate one.
func Name() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "replica"
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%s-%d", host, time.Now().UnixNano())
	}
	return host + "-" + hex.EncodeToString(b[:])
}
