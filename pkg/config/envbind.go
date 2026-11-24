package config

import (
	"fmt"
	"slices"
	"strings"
)

// envValue is the value the environment gives an item, read out of the snapshot
// taken when loading began, and whether it gives one. An empty variable counts
// as a value.
func (l *loader) envValue(item *manifestItem) (string, bool) {
	if item.envName == "" {
		return "", false
	}
	value, set := l.env[item.envName]
	return value, set
}

// ReportUnclaimed writes out the variables under the host's prefix that no input
// item reads. They are a diagnostic and not a failure.
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

// environMap turns the NAME=VALUE form into a lookup. A later entry wins.
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
	name string
	// declared is the input item whose path derives this variable name, when one
	// is declared and does not read the environment.
	declared *manifestItem
}

// unclaimedEnvVariables lists the variables under the prefix that no input item
// reads, in name order.
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

// envUnreachableByName indexes the declared input items that do not read the
// environment by the variable name their path derives.
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
