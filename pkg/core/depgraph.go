package core

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// planConstruction resolves Requires over the enabled set and returns the
// order construction follows.
//
// Resolution happens before any business module is constructed: a required
// capability without a provider is ErrMissingProvider, a cycle is
// ErrDependencyCycle, and at that point the only constructed instance is the
// config module, which is closed by the rollback rule.
//
// Exclusive conflicts are not re-checked here; resolution settled them. An
// Optional requirement whose provider is absent is skipped, while a present
// one still takes part in the ordering.
//
// sites maps a module name to the package that registered it, and a name
// missing from it loses only that half of the report.
func planConstruction(enabled, all []Module, final map[string]Enablement, sites map[string]string) ([]string, error) {
	nodes := make([]string, 0, len(enabled))
	for _, m := range enabled {
		nodes = append(nodes, m.Name)
	}
	slices.Sort(nodes)

	providers := make(map[reflect.Type][]string)
	for _, name := range nodes {
		m := moduleByName(enabled, name)
		for _, ct := range declaredCapabilities(m) {
			providers[ct] = append(providers[ct], name)
		}
	}

	edges := make(map[string]map[string]bool, len(nodes))
	indegree := make(map[string]int, len(nodes))
	for _, name := range nodes {
		edges[name] = make(map[string]bool)
		indegree[name] = 0
	}
	addEdge := func(from, to string) {
		if edges[from][to] {
			return
		}
		edges[from][to] = true
		indegree[to]++
	}

	// The config module is an implicit dependency of every other module, so
	// it is ordered first without anyone declaring it.
	if slices.Contains(nodes, configModuleName) {
		for _, name := range nodes {
			if name != configModuleName {
				addEdge(configModuleName, name)
			}
		}
	}

	for _, name := range nodes {
		m := moduleByName(enabled, name)
		for i, req := range m.Requires {
			ct := capabilityType(req.Token, fmt.Sprintf("module %q Requires[%d]", name, i))
			ps := providers[ct]
			if len(ps) == 0 {
				if req.Optional {
					continue
				}
				return nil, missingProviderError(ct, declarersOf(all, ct), final, name, sites[name])
			}
			for _, p := range ps {
				addEdge(p, name)
			}
		}
	}

	// Kahn's algorithm, always taking the smallest ready name, so the same
	// graph yields the same order whatever the registration order was.
	ready := make([]string, 0, len(nodes))
	for _, name := range nodes {
		if indegree[name] == 0 {
			ready = append(ready, name)
		}
	}
	order := make([]string, 0, len(nodes))
	for len(ready) > 0 {
		slices.Sort(ready)
		name := ready[0]
		ready = ready[1:]
		order = append(order, name)
		for _, to := range sortedKeys(edges[name]) {
			indegree[to]--
			if indegree[to] == 0 {
				ready = append(ready, to)
			}
		}
	}
	if len(order) != len(nodes) {
		return nil, cycleError(nodes, order, edges)
	}
	return order, nil
}

// cycleError names the modules on a cycle. The construction order is only
// solvable on an acyclic graph; modules that genuinely reference each other
// leave the relation out of Requires and take the other side up in Init, where
// every instance already exists.
func cycleError(nodes, ordered []string, edges map[string]map[string]bool) error {
	left := make(map[string]bool, len(nodes))
	for _, name := range nodes {
		left[name] = true
	}
	for _, name := range ordered {
		delete(left, name)
	}

	const (
		white = 0
		grey  = 1
		black = 2
	)
	color := make(map[string]int, len(left))
	var stack []string
	var cycle []string

	var visit func(name string) bool
	visit = func(name string) bool {
		color[name] = grey
		stack = append(stack, name)
		for _, to := range sortedKeys(edges[name]) {
			if !left[to] {
				continue
			}
			switch color[to] {
			case grey:
				at := slices.Index(stack, to)
				cycle = append(slices.Clone(stack[at:]), to)
				return true
			case white:
				if visit(to) {
					return true
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[name] = black
		return false
	}

	for _, name := range sortedKeys(left) {
		if color[name] == white && visit(name) {
			break
		}
	}
	if len(cycle) == 0 {
		cycle = sortedKeys(left)
	}

	return fmt.Errorf("%w: %s. Construction follows the dependency order, so the graph must "+
		"be acyclic: leave the relation out of Requires and take the other side up in Init, "+
		"where every instance already exists",
		ErrDependencyCycle, strings.Join(quoteAll(cycle), " -> "))
}

// missingProviderError tells the three causes apart, because the fixing action
// differs: nothing delivers the capability, a provider is disabled, or a
// provider exists but is constructed after the caller.
//
// requiredBy is the module whose Requires went unmet, and site the package that
// registered it. Both are empty when the registry does not know them, which is
// the Resolve path: a lookup does not say who is making it.
func missingProviderError(ct reflect.Type, declarers []string, final map[string]Enablement, requiredBy, site string) error {
	if len(declarers) == 0 {
		return errorText{
			wrapped: ErrMissingProvider,
			text: fmt.Sprintf("%s: no module delivers capability %s%s. %s. The interface "+
				"lives in %s, which is not necessarily the package to import: an "+
				"implementation usually sits in a subpackage of its own",
				ErrMissingProvider, typeName(ct), requiredByClause(requiredBy, site, false),
				fixingActions(requiredBy), interfacePackage(ct)),
		}
	}

	var disabled, later []string
	for _, name := range declarers {
		if e, ok := final[name]; ok && e.State == StateDisabled {
			disabled = append(disabled, fmt.Sprintf("%q is not enabled: %s", name, e.Reason))
			continue
		}
		later = append(later, name)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s: capability %s%s has no provider to take up",
		ErrMissingProvider, typeName(ct), requiredByClause(requiredBy, site, true))
	if len(disabled) > 0 {
		fmt.Fprintf(&b, "; %s", strings.Join(disabled, "; "))
	}
	if len(later) > 0 {
		fmt.Fprintf(&b, "; %s declares it but is constructed later, so declare the capability "+
			"in Requires to order it first, or take it up in Init instead",
			strings.Join(quoteAll(later), " and "))
	}
	return errorText{wrapped: ErrMissingProvider, text: b.String()}
}

// requiredByClause names the module whose requirement went unmet, together with
// the package that registered it. The available set is decided by imports, so
// that module may be one a dependency pulled in rather than one the host wrote
// down, and a name on its own does not say where to look for it.
//
// trailing puts a comma after the clause, for the sentences that carry on past
// it rather than ending there.
func requiredByClause(requiredBy, site string, trailing bool) string {
	if requiredBy == "" {
		return ""
	}
	clause := fmt.Sprintf(", required by module %q", requiredBy)
	if site != "" {
		clause += fmt.Sprintf(" registered from %s", site)
	}
	if trailing {
		clause += ","
	}
	return clause
}

// fixingActions lists what a host can do about a capability nothing delivers.
// Taking the requiring module out is one of them, and it only becomes
// actionable once the failure names that module: a host that never asked for it
// may want it gone rather than want a provider in.
func fixingActions(requiredBy string) string {
	actions := []string{"Import an implementation of it", "enable one that is already imported"}
	if requiredBy != "" {
		actions = append(actions, fmt.Sprintf("disable %q", requiredBy))
	}
	last := len(actions) - 1
	return strings.Join(actions[:last], ", ") + ", or " + actions[last]
}

// declarersOf lists the modules that declared a capability, in name order.
func declarersOf(mods []Module, ct reflect.Type) []string {
	var names []string
	for _, m := range mods {
		if slices.Contains(declaredCapabilities(m), ct) {
			names = append(names, m.Name)
		}
	}
	slices.Sort(names)
	return names
}

func moduleByName(mods []Module, name string) Module {
	for _, m := range mods {
		if m.Name == name {
			return m
		}
	}
	return Module{}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
