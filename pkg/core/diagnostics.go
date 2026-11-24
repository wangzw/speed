package core

import (
	"fmt"
	"io"
)

const diagnosticPrefix = "core: "

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
