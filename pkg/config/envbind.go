package config

import (
	"fmt"
	"slices"
	"strings"
)

// envValue is the value the environment gives an item, read out of the
// snapshot taken when loading began, and whether it gives one.
//
// The name looked up is the item's own, derived or pinned, so deriveEnvName
// stays the one authority on how a path becomes a variable name. An item that
// reads no variable is given nothing here, whatever the environment holds, or
// it would take a value from a source it never declared.
//
// An empty variable counts as a value. The design reads an empty list out of
// an empty variable, and somebody who wrote the variable did give a value.
func (l *loader) envValue(item *manifestItem) (string, bool) {
	if item.envName == "" {
		return "", false
	}
	value, set := l.env[item.envName]
	return value, set
}

// ReportUnclaimed writes out the variables under the host's prefix that no
// input item reads.
//
// They are a diagnostic and not a failure: the process environment is not the
// host's alone, and an orchestrator injecting a variable of its own must not
// be able to stop the process. The layering tool has no such report of its
// own -- it pulls the variables it was told about and never says which ones
// nobody wanted -- so the enumeration is this module's to make.
//
// The environment it scans is the snapshot the values are read from. Scanning
// any other would let the diagnostics and the values disagree.
func (l *loader) ReportUnclaimed() {
	for _, name := range unclaimedEnvNames(l.m, l.env) {
		//nolint:errcheck // l.stderr is the diagnostic sink itself: a failed
		// write has nowhere to be reported, and this line is explicitly not a
		// failure condition -- see the doc comment above.
		fmt.Fprintf(l.stderr, "%senvironment variable %s is set under the %q prefix and no input "+
			"item reads it, so it is ignored\n", diagnosticPrefix, name, l.m.host.Prefix)
	}
}

// environMap turns the NAME=VALUE form into a lookup. A later entry wins, as
// it does for the process itself.
func environMap(environ []string) map[string]string {
	values := make(map[string]string, len(environ))
	for _, entry := range environ {
		name, value, ok := strings.Cut(entry, "=")
		if !ok || name == "" {
			continue
		}
		values[name] = value
	}
	return values
}

// unclaimedEnvNames lists the variables under the prefix that no input item
// reads, in name order. The scan domain is the prefix, so a pinned name such
// as NO_COLOR is outside it; the config locator is inside it but claimed by
// this module, and is not reported.
func unclaimedEnvNames(m *manifest, values map[string]string) []string {
	if m.host.Prefix == "" {
		return nil
	}
	head := strings.ToUpper(m.host.Prefix) + "_"
	locator := locatorEnvName(m.host.Prefix)
	var unclaimed []string
	for name := range values {
		if !strings.HasPrefix(name, head) || name == locator {
			continue
		}
		if _, claimed := m.byEnv[name]; claimed {
			continue
		}
		unclaimed = append(unclaimed, name)
	}
	slices.Sort(unclaimed)
	return unclaimed
}
