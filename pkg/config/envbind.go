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
// An empty variable counts as a value. Somebody who wrote the variable did give
// a value, and what it is follows the item's kind: the empty list or map for a
// container, and a diagnostic for a struct, whose members cannot be read out of
// nothing (see envContainer).
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
// A variable whose name spells the path of a declared item that does not read
// the environment is told apart from one that spells no path: the first names
// the item and the sources it accepts, so a deployment that set
// <PREFIX>_HTTP__CORS__ENDPOINTS learns that the item takes the primary config
// source alone rather than that nothing wanted the variable.
//
// The environment it scans is the snapshot the values are read from. Scanning
// any other would let the diagnostics and the values disagree.
func (l *loader) ReportUnclaimed() {
	for _, ignored := range unclaimedEnvVariables(l.m, l.env) {
		if item := ignored.declared; item != nil {
			_, _ = fmt.Fprintf(l.stderr, "%senvironment variable %s is set under the %q prefix and "+
				"names the input item %q, which only accepts %s, so it is ignored\n",
				diagnosticPrefix, ignored.name, l.m.host.Prefix, item.path, originNames(item.origins))
			continue
		}
		_, _ = fmt.Fprintf(l.stderr, "%senvironment variable %s is set under the %q prefix and no "+
			"input item reads it, so it is ignored\n", diagnosticPrefix, ignored.name, l.m.host.Prefix)
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

// unclaimedEnv is one variable under the prefix that no input item reads.
type unclaimedEnv struct {
	// name is the variable.
	name string
	// declared is the input item whose path derives this variable name, when
	// one is declared. The item does not read the environment, so the variable
	// reaches no value. Nil when no item sits at the spelled path.
	declared *manifestItem
}

// unclaimedEnvVariables lists the variables under the prefix that no input item
// reads, in name order, each with the declared item its name spells. The scan
// domain is the prefix, so a pinned name such as NO_COLOR is outside it; the
// config locator is inside it but claimed by this module, and is not reported.
func unclaimedEnvVariables(m *manifest, values map[string]string) []unclaimedEnv {
	if m.host.Prefix == "" {
		return nil
	}
	head := strings.ToUpper(m.host.Prefix) + "_"
	locator := locatorEnvName(m.host.Prefix)
	unreachable := m.envUnreachableByName()
	var unclaimed []unclaimedEnv
	for name := range values {
		if !strings.HasPrefix(name, head) || name == locator {
			continue
		}
		if _, claimed := m.byEnv[name]; claimed {
			continue
		}
		unclaimed = append(unclaimed, unclaimedEnv{name: name, declared: unreachable[name]})
	}
	slices.SortFunc(unclaimed, func(a, b unclaimedEnv) int { return strings.Compare(a.name, b.name) })
	return unclaimed
}

// envUnreachableByName indexes the declared input items by the variable name
// their path derives, taking only the items that do not read the environment.
// It is what tells a variable a declaration ignores from one no declaration
// sits under. An item that reads the environment is in byEnv instead, and a
// variable it reads is never reported at all.
func (m *manifest) envUnreachableByName() map[string]*manifestItem {
	if m.host.Prefix == "" {
		return nil
	}
	unreachable := make(map[string]*manifestItem)
	for _, item := range m.items {
		if item.origins.has(OriginEnv) {
			continue
		}
		unreachable[deriveEnvName(m.host.Prefix, item.path)] = item
	}
	return unreachable
}
