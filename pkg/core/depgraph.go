package core

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
)

// Closure expands a list of module names into the set a command has to
// include: the names given, plus the modules that provide what those require
// at construction time, transitively. A provider that claims what it delivers
// exclusively is not expanded; the rule and why it is the exception are below.
//
// Including a module means including what it takes to build it, and the
// expansion lives here rather than in the caller because the matching rule
// does: a requirement meets a delivery when their element types are the same,
// never by assignability, exactly as Resolve matches. The command tree calls
// this while it is assembled and hands Drive the literal result, so the drive
// expands nothing.
//
// A requirement nothing delivers adds nothing, Optional or not: dependency
// resolution reports that during the drive, where the failure can name the
// module that required it and say what is missing. So does one whose providers
// all claim the capability exclusively: none of them is pulled in, and the
// failure says the host has to name one implementation on the command tree, or
// supply an instance for one. A name no module registered is
// ErrUnknownParticipant.
//
// Closure knows nothing of supplied instances. A module the host supplied an
// instance for is expanded, and expands its own requirements, like any other
// module it is given. It joins the participating set when the drive begins
// whether or not the result names it, and nothing is expanded for it then.
//
// The result is deduplicated and in name order.
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
			// An optional requirement does not pull its providers in. Its
			// meaning is that going without it is a legal configuration, and
			// a provider the host never listed, added here on the module's
			// behalf, would leave it no way to be absent.
			if req.Optional {
				continue
			}
			ct := capabilityType(req.Token, fmt.Sprintf("module %q Requires[%d]", name, i))
			for _, provider := range providers[ct] {
				// Neither does a provider that claims the capability
				// exclusively. Several implementations may each claim it
				// alone, and picking one for the host would be a decision:
				// the expansion rule introduces none, so which of them runs
				// is what the host names on the command tree. Two providers
				// of one capability nobody claims exclusively are the normal
				// case and are all added, which is what a set of
				// interchangeable backends relies on.
				if claimsExclusive(r.mods[provider].module, ct) {
					continue
				}
				pending = append(pending, provider)
			}
		}
	}
	return sortedKeys(included), nil
}

// planConstruction resolves Requires over the enabled set and returns the
// order construction follows.
//
// Resolution happens before any module is constructed: a required capability
// without a provider is ErrMissingProvider and a cycle is ErrDependencyCycle,
// and the drive has constructed nothing yet for either of them to roll back.
//
// A module the host supplied an instance for is a node like any other. It is in
// the enabled set, it provides what it declares and comes before the modules
// that require it, and its own Requires are resolved here as anyone's are.
// Whether its instance was supplied or is yet to be constructed does not enter
// the ordering.
//
// Exclusive conflicts are not re-checked here; resolution settled them. An
// Optional requirement whose provider is absent is skipped, while a present
// one still takes part in the ordering. A capability every provider of which
// claims it exclusively has no provider here unless the command named one, or
// the host supplied an instance for one — the closure never pulled one in — so
// it fails as a missing provider whose fix is to name an implementation or
// supply an instance rather than to import one.
//
// participants is what this command runs, and declarationsOnly and outside are
// the two halves of what it did not: modules it reached for their declarations
// alone, and modules it left registered but out. The second of those two is one
// of the ways a capability ends up without a provider. origins maps a module
// name to where the module came from, and a name missing from it loses only the
// half of the report that says so.
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

// dependencyGraph is the ordering Requires and Provides draw over a set of
// modules: an edge runs from each provider of a capability to every module that
// requires it, so a provider comes before the modules that need it. Only the
// modules in the set provide anything to each other here.
type dependencyGraph struct {
	nodes    []string // module names, in name order
	edges    map[string]map[string]bool
	indegree map[string]int
	// unmet lists, in node order and then declaration order, the requirements
	// no module of the set provides. The graph has no edge for them, and what to
	// make of them is the caller's: planning fails over one, and an ordering
	// that only has to place the modules ignores them.
	unmet []unmetRequirement
}

// unmetRequirement is a requirement of a module that no module in the graph
// provides.
type unmetRequirement struct {
	module     string
	capability reflect.Type
	optional   bool
}

// newDependencyGraph draws the graph over mods, in name order and with the ties
// of the ordering left to order.
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

// order is Kahn's algorithm, always taking the smallest ready name, so the same
// graph yields the same order whatever the registration order was. A module on a
// cycle, and any module behind one, has no place in it: the result is shorter
// than the node list exactly when the graph has a cycle.
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

// dependencyOrder orders modules by the Requires and Provides among them alone,
// by the rule construction follows. It validates nothing: a requirement none of
// them provides is ignored, and a cycle does not fail it. The modules on a cycle,
// and any behind one, come last in name order, since a cycle gives them no
// order but they are in the list all the same.
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

// missingProviderError tells the causes apart, because the fixing action
// differs: nothing delivers the capability, a provider is disabled, a provider
// exists but is constructed after the caller, a provider is registered and
// this command did not include it, every provider of it claims the capability
// exclusively and the command named none of them, or a provider has no New
// callback and the host supplied no instance for it.
//
// declarers are the modules this command included, notIncluded the ones it
// left out. Reporting the second group as any of the other three gives an
// instruction that does nothing: the module is not disabled, and it is not
// ordered late — it is not in this command at all.
//
// exclusive is the part of notIncluded that claims the capability exclusively.
// Those modules are left out by the expansion rule rather than by the host's
// writing, so the fix is a decision only the host can make: name one of them,
// or supply an instance for one.
//
// noNew is the part of all three groups that declares no New callback. Such a
// module builds no instance in any stage, so whether the command constructs it
// late, includes it or takes it in at run level changes nothing: it is reported
// for what is true of it at every moment, that its instance can only come from
// the host, and only with the fix that supplies one. A declarer disabled by
// resolution keeps the report of why it is disabled.
//
// requiredBy is the module whose Requires went unmet, and from where it came.
// Both are empty when the registry does not know them, which is the Resolve path:
// a lookup does not say who is making it. A requirer that has a supplied
// instance is in the command without the command tree's say and its requirements
// were not expanded with it, which changes what the host can do about it.
//
// nothingBuilt is set when the lookup is made while no module's New has run
// and says why: then an enabled declarer is not constructed later than the
// caller, it is not constructed at all yet, and ordering it by Requires cannot
// help. It is empty on the planning path, where every declarer this command
// runs and that has no instance is one resolution disabled.
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

// excluding drops the names in drop from names, keeping the order of the rest.
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

// requiredByClause names the module whose requirement went unmet, together with
// the package that registered it and, when the host supplied its instance, that
// too. The available set is decided by imports, so that module may be one a
// dependency pulled in rather than one the host wrote down, and a name on its
// own does not say where to look for it.
//
// trailing puts a comma after the clause, for the sentences that carry on past
// it rather than ending there.
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

// fixingActions lists what a host can do about a capability nothing delivers.
// Importing an implementation is one of them; leaving the requiring module out
// of the command is the other, and it only becomes actionable once the failure
// names that module: a host that never asked for it may want it gone rather
// than want a provider in. The command tree is the whole of that move — a
// module taken in at run level has to start, so no setting turns it back off.
//
// A requiring module that has a supplied instance is in the command whatever
// the command tree says, so the move is another: stop supplying it. Its
// requirements were not expanded with it either, so an implementation that is
// imported still has to be taken in on the command tree, or supplied in turn.
//
// The list carries no "enable one that is already imported". This failure is
// reached only when no registered module declared the capability, so an
// implementation already imported does not exist by construction.
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

// exclusiveAmong lists the modules of a group that claim a capability
// exclusively, in name order. These are the ones the expansion rule leaves
// out, so their absence from a command is the host's to fix by naming one.
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

// lackingNewAmong lists the modules of the groups that declare a capability and
// declare no New callback, in name order. These can only get an instance from
// the host.
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
