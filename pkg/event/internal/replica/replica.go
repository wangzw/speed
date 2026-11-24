// Package replica names one process among the replicas of an assembly.
package replica

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"time"
)

// Name mints a replica name for this process: the host, a hyphen, and
// eight random bytes in hex.
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
