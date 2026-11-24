package core

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
)

// Closure expands a list of module names into the set a command has to include: the names given,
// plus the modules that provide what those require at construction time, transitively.
func (r *Registry) Closure(names []string) ([]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	providers := make(map[reflect.Type][]string)
	for _, name := range r.sortedNamesLocked() {
		for _, ct := range declaredCapabilities(r.mods[name].module) {
			providers[ct] = append(providers[ct], name)
		}
	}

	included := make(map[string]bool, len(names))
	pending := slices.Clone(names)
	for len(pending) > 0 {
		name := pending[0]
		pending = pending[1:]
		if included[name] {
			continue
		}
		reg, ok := r.mods[name]
		if !ok {
			return nil, unknownParticipantError(name)
		}
		included[name] = true
		for i, req := range reg.module.Requires {
			if req.Optional {
				continue
			}
			ct := capabilityType(req.Token, fmt.Sprintf("module %q Requires[%d]", name, i))
			for _, provider := range providers[ct] {
				if claimsExclusive(r.mods[provider].module, ct) {
					continue
				}
				pending = append(pending, provider)
			}
		}
	}
	return sortedKeys(included), nil
}

func planConstruction(enabled, participants, declarationsOnly, outside []Module, final map[string]Enablement, origins map[string]origin) ([]string, error) {
	graph := newDependencyGraph(enabled)
	for _, u := range graph.unmet {
		if u.optional {
			continue
		}
		notIncluded := declarersOf(outside, u.capability)
		return nil, missingProviderError(u.capability, declarersOf(participants, u.capability),
			declarersOf(declarationsOnly, u.capability), notIncluded,
			exclusiveAmong(outside, u.capability),
			lackingNewAmong(u.capability, participants, declarationsOnly, outside),
			final, u.module, origins[u.module], "")
	}

	order := graph.order()
	if len(order) != len(graph.nodes) {
		return nil, cycleError(graph.nodes, order, graph.edges)
	}
	return order, nil
}

type dependencyGraph struct {
	nodes    []string
	edges    map[string]map[string]bool
	indegree map[string]int
	unmet    []unmetRequirement
}

type unmetRequirement struct {
	module     string
	capability reflect.Type
	optional   bool
}

func newDependencyGraph(mods []Module) *dependencyGraph {
	nodes := make([]string, 0, len(mods))
	for _, m := range mods {
		nodes = append(nodes, m.Name)
	}
	slices.Sort(nodes)

	providers := make(map[reflect.Type][]string)
	for _, name := range nodes {
		for _, ct := range declaredCapabilities(moduleByName(mods, name)) {
			providers[ct] = append(providers[ct], name)
		}
	}

	g := &dependencyGraph{
		nodes:    nodes,
		edges:    make(map[string]map[string]bool, len(nodes)),
		indegree: make(map[string]int, len(nodes)),
	}
	for _, name := range nodes {
		g.edges[name] = make(map[string]bool)
		g.indegree[name] = 0
	}

	for _, name := range nodes {
		for i, req := range moduleByName(mods, name).Requires {
			ct := capabilityType(req.Token, fmt.Sprintf("module %q Requires[%d]", name, i))
			ps := providers[ct]
			if len(ps) == 0 {
				g.unmet = append(g.unmet, unmetRequirement{module: name, capability: ct, optional: req.Optional})
				continue
			}
			for _, p := range ps {
				g.addEdge(p, name)
			}
		}
	}
	return g
}

func (g *dependencyGraph) addEdge(from, to string) {
	if g.edges[from][to] {
		return
	}
	g.edges[from][to] = true
	g.indegree[to]++
}

// order runs Kahn's algorithm, always taking the smallest ready name, so the same graph yields the same order whatever the registration order was.
// A module on a cycle, and any module behind one, has no place in it.
func (g *dependencyGraph) order() []string {
	indegree := maps.Clone(g.indegree)
	ready := make([]string, 0, len(g.nodes))
	for _, name := range g.nodes {
		if indegree[name] == 0 {
			ready = append(ready, name)
		}
	}
	order := make([]string, 0, len(g.nodes))
	for len(ready) > 0 {
		slices.Sort(ready)
		name := ready[0]
		ready = ready[1:]
		order = append(order, name)
		for _, to := range sortedKeys(g.edges[name]) {
			indegree[to]--
			if indegree[to] == 0 {
				ready = append(ready, to)
			}
		}
	}
	return order
}

func dependencyOrder(mods []Module) []string {
	graph := newDependencyGraph(mods)
	order := graph.order()
	if len(order) == len(graph.nodes) {
		return order
	}
	for _, name := range graph.nodes {
		if !slices.Contains(order, name) {
			order = append(order, name)
		}
	}
	return order
}

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

func missingProviderError(ct reflect.Type, declarers, declarationsOnly, notIncluded, exclusive, noNew []string, final map[string]Enablement, requiredBy string, from origin, nothingBuilt string) error {
	if len(declarers) == 0 && len(declarationsOnly) == 0 && len(notIncluded) == 0 {
		return errorText{
			wrapped: ErrMissingProvider,
			text: fmt.Sprintf("%s: no module delivers capability %s%s. %s. The interface "+
				"lives in %s, which is not necessarily the package to import: an "+
				"implementation usually sits in a subpackage of its own",
				ErrMissingProvider, typeName(ct), requiredByClause(requiredBy, from, false),
				fixingActions(requiredBy, from.supplied), interfacePackage(ct)),
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

	var hostOnly []string
	for _, group := range [][]string{later, declarationsOnly, notIncluded} {
		for _, name := range group {
			if slices.Contains(noNew, name) {
				hostOnly = append(hostOnly, name)
			}
		}
	}
	slices.Sort(hostOnly)
	later = excluding(later, hostOnly)
	declarationsOnly = excluding(declarationsOnly, hostOnly)
	notIncluded = excluding(notIncluded, hostOnly)
	exclusive = excluding(exclusive, hostOnly)

	var b strings.Builder
	fmt.Fprintf(&b, "%s: capability %s%s has no provider to take up",
		ErrMissingProvider, typeName(ct), requiredByClause(requiredBy, from, true))
	if len(disabled) > 0 {
		fmt.Fprintf(&b, "; %s", strings.Join(disabled, "; "))
	}
	for _, name := range hostOnly {
		fmt.Fprintf(&b, "; %q declares it but has no New callback, and the host supplied no "+
			"instance for it: its instance can only come from the host, which hands it in with "+
			"Supply before the drive", name)
	}
	switch {
	case len(later) > 0 && nothingBuilt != "":
		fmt.Fprintf(&b, "; %s declares it, and %s", strings.Join(quoteAll(later), " and "), nothingBuilt)
	case len(later) > 0:
		fmt.Fprintf(&b, "; %s declares it but is constructed later, so declare the capability "+
			"in Requires to order it first, or take it up in Init instead",
			strings.Join(quoteAll(later), " and "))
	}
	if len(declarationsOnly) > 0 {
		fmt.Fprintf(&b, "; %s declares it but was taken in for its declarations alone, so it "+
			"is never constructed and cannot take the capability up. Take it in at run level "+
			"if this command is meant to have it",
			strings.Join(quoteAll(declarationsOnly), " and "))
	}
	if len(exclusive) > 0 {
		fmt.Fprintf(&b, "; it is claimed exclusively by %s, and the dependency closure does "+
			"not pull an exclusive provider into a command on its own: name one implementation "+
			"on the command tree of this command, and that one is what runs, or hand an "+
			"instance of one of them in with Supply before the drive",
			strings.Join(quoteAll(exclusive), " and "))
	}
	if rest := excluding(notIncluded, exclusive); len(rest) > 0 {
		if from.supplied {
			fmt.Fprintf(&b, "; %s declares it but this command does not include it, so add it "+
				"to the Include of this command, or hand an instance of it in with Supply before "+
				"the drive: the requirements of a module with a supplied instance are not "+
				"expanded for it",
				strings.Join(quoteAll(rest), " and "))
		} else {
			fmt.Fprintf(&b, "; %s declares it but this command does not include it, so add it "+
				"to the Include of the command that needs it",
				strings.Join(quoteAll(rest), " and "))
		}
	}
	return errorText{wrapped: ErrMissingProvider, text: b.String()}
}

func excluding(names, drop []string) []string {
	if len(drop) == 0 {
		return names
	}
	var out []string
	for _, name := range names {
		if !slices.Contains(drop, name) {
			out = append(out, name)
		}
	}
	return out
}

func requiredByClause(requiredBy string, from origin, trailing bool) string {
	if requiredBy == "" {
		return ""
	}
	clause := fmt.Sprintf(", required by module %q", requiredBy)
	if from.site != "" {
		clause += fmt.Sprintf(" registered from %s", from.site)
	}
	if from.supplied {
		clause += ", which has a supplied instance"
	}
	if trailing {
		clause += ","
	}
	return clause
}

func fixingActions(requiredBy string, supplied bool) string {
	if requiredBy != "" && supplied {
		return fmt.Sprintf("Import an implementation of it and take it in on the command tree, or "+
			"hand an instance of it in with Supply, because the requirements of a module with a "+
			"supplied instance are not expanded for it. Alternatively stop supplying an "+
			"instance to %q, by removing the Supply call that gave it one, because leaving it "+
			"out of the command tree does not take a module out of the command once it is "+
			"supplied", requiredBy)
	}
	actions := []string{"Import an implementation of it"}
	if requiredBy != "" {
		actions = append(actions, fmt.Sprintf("leave %q out of this command", requiredBy))
	}
	if len(actions) == 1 {
		return actions[0]
	}
	last := len(actions) - 1
	return strings.Join(actions[:last], ", ") + ", or " + actions[last]
}

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

func exclusiveAmong(mods []Module, ct reflect.Type) []string {
	var names []string
	for _, m := range mods {
		if claimsExclusive(m, ct) {
			names = append(names, m.Name)
		}
	}
	slices.Sort(names)
	return names
}

func lackingNewAmong(ct reflect.Type, groups ...[]Module) []string {
	var names []string
	for _, mods := range groups {
		for _, m := range mods {
			if m.New == nil && slices.Contains(declaredCapabilities(m), ct) {
				names = append(names, m.Name)
			}
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
