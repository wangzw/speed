package config

import (
	"fmt"
	"os"
	"slices"
	"strings"
)

// bindEnv hands the layering tool the environment variable of every item that
// reads one. Each name is given in full, so deriveEnvName stays the one
// authority on how a path becomes a variable name and the tool derives
// nothing: its own derivation joins a prefix and then rewrites the whole name,
// which would reach inside a pinned name and rewrite that too. Nor is the tool
// allowed to read the environment for keys nobody bound, or an item would take
// a value from a source it never declared.
//
// The domain is the whole manifest and not the participants of a command: the
// environment names are claimed and checked against every declaration there
// is, so binding only what one command takes in would have the rest reported
// as variables nobody reads.
//
// An empty variable counts as a value. The design reads an empty list out of
// an empty variable, and somebody who wrote the variable did give a value;
// the tool's own default is to treat it as unset.
func (l *loader) bindEnv() error {
	l.v.AllowEmptyEnv(true)
	for _, item := range l.m.items {
		if item.envName == "" {
			continue
		}
		if err := l.v.BindEnv(item.path, item.envName); err != nil {
			return fmt.Errorf("%w: module %q reads %s for %q, which could not be bound: %w",
				ErrConfigConflict, item.module, item.envName, item.path, err)
		}
	}
	return nil
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
// The environment it scans is the process's own, which is the one the
// layering tool reads values from. Scanning any other would let the
// diagnostics and the values disagree.
func (l *loader) ReportUnclaimed() {
	for _, name := range unclaimedEnvNames(l.m, environMap(os.Environ())) {
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
