package core

import (
	"fmt"
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
// A requirement a supplied product satisfies adds nothing, since no module
// holds it. One nothing delivers adds nothing either, Optional or not:
// dependency resolution reports that during the drive, where the failure can
// name the module that required it and say what is missing. So does one whose
// providers all claim the capability exclusively: none of them is pulled in,
// and the failure says the host has to name one implementation on the command
// tree. A name no module registered is ErrUnknownParticipant.
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
// and nothing has been built yet for either of them to roll back.
//
// A requirement a supplied product satisfies is met without an edge. Supplied
// products exist before the first construction, so there is nothing to order
// against, and an edge would point at a node the graph does not have.
//
// Exclusive conflicts are not re-checked here; resolution settled them. An
// Optional requirement whose provider is absent is skipped, while a present
// one still takes part in the ordering. A capability every provider of which
// claims it exclusively has no provider here unless the command named one —
// the closure never pulled one in — so it fails as a missing provider whose
// fix is to name an implementation rather than to import one.
//
// participants is what this command runs, and declarationsOnly and outside are
// the two halves of what it did not: modules it reached for their declarations
// alone, and modules it left registered but out. The second of those two is one
// of the ways a capability ends up without a provider. sites maps a module name
// to the package that registered it, and a name missing from it loses only that
// half of the report.
func planConstruction(enabled, participants, declarationsOnly, outside []Module, final map[string]Enablement, sites map[string]string, supplied map[reflect.Type]bool) ([]string, error) {
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

	for _, name := range nodes {
		m := moduleByName(enabled, name)
		for i, req := range m.Requires {
			ct := capabilityType(req.Token, fmt.Sprintf("module %q Requires[%d]", name, i))
			if supplied[ct] {
				continue
			}
			ps := providers[ct]
			if len(ps) == 0 {
				if req.Optional {
					continue
				}
				notIncluded := declarersOf(outside, ct)
				return nil, missingProviderError(ct, declarersOf(participants, ct),
					declarersOf(declarationsOnly, ct), notIncluded,
					exclusiveAmong(outside, ct), final, name, sites[name], "")
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

// missingProviderError tells the causes apart, because the fixing action
// differs: nothing delivers the capability, a provider is disabled, a provider
// exists but is constructed after the caller, a provider is registered and
// this command did not include it, or every provider of it claims the
// capability exclusively and the command named none of them.
//
// declarers are the modules this command included, notIncluded the ones it
// left out. Reporting the second group as any of the other three gives an
// instruction that does nothing: the module is not disabled, and it is not
// ordered late — it is not in this command at all.
//
// exclusive is the part of notIncluded that claims the capability exclusively.
// Those modules are left out by the expansion rule rather than by the host's
// writing, so the fix is a decision only the host can make: name one of them.
//
// requiredBy is the module whose Requires went unmet, and site the package that
// registered it. Both are empty when the registry does not know them, which is
// the Resolve path: a lookup does not say who is making it.
//
// nothingBuilt is set when the lookup is made while no module's product exists
// and says why: then an enabled declarer is not constructed later than the
// caller, it is not constructed at all yet, and ordering it by Requires cannot
// help. It is empty on the planning path, where every declarer this command
// runs and that has no product is one resolution disabled.
func missingProviderError(ct reflect.Type, declarers, declarationsOnly, notIncluded, exclusive []string, final map[string]Enablement, requiredBy, site, nothingBuilt string) error {
	if len(declarers) == 0 && len(declarationsOnly) == 0 && len(notIncluded) == 0 {
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
			"on the command tree of this command, and that one is what runs",
			strings.Join(quoteAll(exclusive), " and "))
	}
	if rest := excluding(notIncluded, exclusive); len(rest) > 0 {
		fmt.Fprintf(&b, "; %s declares it but this command does not include it, so add it "+
			"to the Include of the command that needs it",
			strings.Join(quoteAll(rest), " and "))
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
// Importing an implementation is one of them; leaving the requiring module out
// of the command is the other, and it only becomes actionable once the failure
// names that module: a host that never asked for it may want it gone rather
// than want a provider in. The command tree is the whole of that move — a
// module taken in at run level has to start, so no setting turns it back off.
//
// The list carries no "enable one that is already imported". This failure is
// reached only when no registered module declared the capability, so an
// implementation already imported does not exist by construction.
func fixingActions(requiredBy string) string {
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
