package core

import (
	"fmt"
	"io"
)

// diagnosticPrefix opens every diagnostic line core writes. Startup
// diagnostics have several writers and all of them go to os.Stderr, which a
// host cannot redirect, so each line leads with the party that produced it:
// core writes "core: ", a module writes its own name and a colon. Without it
// the reader cannot tell the lines apart on standard error.
const diagnosticPrefix = "core: "

// writeDiagnostics lists every module resolution kept out of the enabled set,
// one line each, in module-name order, with the reason resolution gave.
//
// Resolution is the only thing that can leave a module out of that set: a
// module a drive takes in at run level must start, so a provider stands down
// only when another provider of the same exclusive capability is explicitly
// enabled.
//
// A process that starts up missing a piece of functionality with nobody
// noticing is the most dangerous failure of configuration-driven assembly, and
// one line saying which module is not running, and why, puts it in front of
// the reader before any investigation starts.
func writeDiagnostics(w io.Writer, final map[string]Enablement) {
	for _, name := range sortedKeys(final) {
		e := final[name]
		if e.State != StateDisabled {
			continue
		}
		//nolint:errcheck // w is the diagnostic sink itself, so a failed write
		// has nowhere left to be reported, and refusing to start over an
		// unwritable stderr would trade a lost line for a dead process.
		fmt.Fprintf(w, "%smodule %q is not enabled: %s\n", diagnosticPrefix, name, e.Reason)
	}
}
