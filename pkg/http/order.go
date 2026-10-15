package http

import (
	"cmp"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/vislake/speed/pkg/core"
)

// layer is one middleware as an endpoint recorded it: the registration itself,
// and the capabilities it stands for on the chain.
//
// provides is what an After or a Before constraint resolves against. A
// constraint names a capability, and the layers standing for that capability
// are the ones this layer is placed against; a layer that stands for nothing
// is still ordered, it is just never the target of anyone's constraint. The
// ordering reads the field and nothing about where it came from.
type layer struct {
	mw       Middleware
	provides []core.Token
}

// declaredStages lists the stages in the order the chain runs them, outermost
// first — the order they are declared in on Stage, written out so the ordering
// walks them by name rather than by their numeric values. Registration refuses
// a layer whose stage is not one of these, so they are the only buckets the
// ordering can meet.
var declaredStages = [...]Stage{
	StageObserve,
	StageGuard,
	StageIdentify,
	StageThrottle,
	StageRecord,
	StageAuthorize,
}

// orderingRemark is one thing the ordering noticed about an endpoint's
// declarations that it reports rather than acts on: a constraint that drew no
// edge, or a constraint the stage order already satisfies. Neither is a failure
// — the assembly carries on — but neither is observable anywhere else either,
// so they come back for the caller to write out.
//
// The ways of arriving here share one shape in the graph — nothing moved, and
// without the report nothing is written at all — which is why one type carries
// them. The cause tells them apart, because the fixing actions differ; see
// remarkCause, and remarkCause.level and remarkCause.message for how each is
// written.
type orderingRemark struct {
	// endpoint says which endpoint the declaration was made on; with layer it
	// locates the declaration.
	endpoint string
	// layer is what the line is about: the layer that declared the constraint.
	layer string
	// field is "After" or "Before" and at is the position in it, which is
	// what locates the one declaration among several on the same layer.
	field string
	at    int
	// capability is the type the token designates, rendered with its
	// package path, so the reader can go and look at who was meant to
	// stand for it.
	capability string
	// cause is why the line is there.
	cause remarkCause
}

// remarkCause is why a remark is written. The causes are told apart because
// the fixing action differs, so a report that does not name the cause sends
// half its readers after the wrong thing. The level and the words travel with
// the cause too, because they differ the same way: a redundant cross-stage
// constraint is not the failure an unplaced constraint may be.
type remarkCause string

const (
	// causeNoProvider is a capability no layer on this endpoint stands for:
	// the module that would stand for it is not in this process, or it is
	// and does not guard this endpoint, or its layer is here and left its
	// Provides empty. The graph cannot tell those apart, and a module that is
	// absent or does not guard the endpoint is a legal configuration.
	causeNoProvider remarkCause = "no layer on this endpoint stands for the capability"
	// causeSelfOnly is a capability whose only representative is the layer
	// declaring the constraint. The self edge is dropped, so the constraint
	// places nothing; nobody is absent, and the declaration is what is wrong.
	// A capability another layer stands for as well is not this case: that
	// part of the claim holds, so the constraint did what it said.
	causeSelfOnly remarkCause = "the declaring layer is the only layer standing for the capability"
	// causeStageSatisfied is a constraint whose provider is in another stage,
	// in the direction the stage order already gives. It drew no edge: a
	// position across stages is the framework's to decide, and an edge cannot
	// move a layer past a stage boundary. The declaration repeats what the
	// boundary already says, so it is redundant rather than wrong, and it is
	// reported at Debug — it is not a position the layer failed to get, and
	// nothing about the assembly needs changing.
	causeStageSatisfied remarkCause = "the constraint is already given by the stage order"
)

// orderLayers returns an endpoint's middleware from outermost to innermost,
// together with the things it noticed and did not act on. The layers arrive in
// registration order.
//
// Stage decides the order between categories and nothing else does: the
// layers are bucketed by stage, each bucket is solved on its own, and the
// buckets are concatenated in declared-stage order, so every layer of an outer
// stage comes out before any layer of an inner one. That is what makes a
// category constraint hold for layers this module has never heard of, and what
// makes a cross-stage After or Before redundant — the boundary, not the edge,
// places the layer. A cross-stage constraint that agrees with the boundary is
// recorded for Debug; one that points against it is refused with
// ErrStageConflict, because it asks for the opposite of where the stages put
// the two layers.
//
// Within a bucket, After and Before give the graph its edges: After C places
// this layer inside the middleware standing for C, so an edge runs from each
// provider of C to this layer, the provider coming out first and landing
// further out. Before C is the same edge reversed. A constraint a provider
// stands beside places the layer; one that does not places nothing, and comes
// back in the report for the caller to write out with the reason.
//
// Order decides only within the ready set — among the layers whose constraints
// are already satisfied at that point — so it can never select a place a
// constraint forbids, and a setting and a constraint cannot contradict each
// other. A smaller Order comes out before a larger one, and equal Order falls
// back to registration order, which makes (Order, registration) a total order
// within a stage and the result unique.
//
// The graph is built per endpoint, per assembly, and thrown away with the
// call. It costs O(V+E) in the number of layers and the constraints that hold
// between them, once per endpoint at startup.
func orderLayers(endpoint string, layers []layer) ([]layer, []orderingRemark, error) {
	providers := providersByCapability(endpoint, layers)

	var reported []orderingRemark
	// note records a constraint that placed nothing, with the reason. The
	// declaring layer's own standing is what the answer turns on: the edge
	// to it is dropped, so a layer that is the only one standing for the
	// capability it names places nothing either, and a layer another
	// provider stands beside still draws that edge.
	note := func(self int, name, field string, at int, key reflect.Type) {
		cause, unplaced := unplacedCause(providers[key], self)
		if !unplaced {
			return
		}
		reported = append(reported, orderingRemark{
			endpoint:   endpoint,
			layer:      name,
			field:      field,
			at:         at,
			capability: typeName(key),
			cause:      cause,
		})
	}

	edges := make([]map[int]bool, len(layers))
	indegree := make([]int, len(layers))
	for i := range layers {
		edges[i] = make(map[int]bool)
	}
	// A layer that declares a constraint on a capability it delivers itself
	// names itself among the providers. Drawing that edge would be a self
	// loop, which no order can satisfy, and the cycle it forms is an
	// artefact of the edge rather than a contradiction between two
	// declarations; the same edge can also be drawn twice, by one layer's
	// After and the other's Before, and counting it twice in the indegree
	// leaves a layer that never becomes ready and reports a cycle that is
	// not there.
	addEdge := func(from, to int) {
		if from == to || edges[from][to] {
			return
		}
		edges[from][to] = true
		indegree[to]++
	}
	// place resolves one provider of one constraint. A provider in the
	// declaring layer's own stage draws the edge; one in another stage draws
	// nothing, and instead either repeats what the stage order gives or
	// contradicts it, which the caller cannot be given a position for.
	place := func(i, p int, field string, at int, key reflect.Type) error {
		if layers[p].mw.Stage == layers[i].mw.Stage {
			if field == "After" {
				addEdge(p, i)
			} else {
				addEdge(i, p)
			}
			return nil
		}
		// After asks to sit inside the named provider, Before outside it. The
		// stage order gives the declarer one of those two, and a constraint
		// that asks for the other cannot be honoured by an edge.
		wantInside := field == "After"
		providerInside := layers[p].mw.Stage > layers[i].mw.Stage
		if wantInside != providerInside {
			reported = append(reported, orderingRemark{
				endpoint:   endpoint,
				layer:      layers[i].mw.Name,
				field:      field,
				at:         at,
				capability: typeName(key),
				cause:      causeStageSatisfied,
			})
			return nil
		}
		return stageConflict(endpoint, layers[i], field, at, layers[p], key)
	}

	for i, l := range layers {
		for j, token := range l.mw.After {
			key := capabilityKey(token, declarationSite(endpoint, l.mw.Name, "After", j))
			note(i, l.mw.Name, "After", j, key)
			for _, p := range providers[key] {
				if p == i {
					continue
				}
				if err := place(i, p, "After", j, key); err != nil {
					return nil, nil, err
				}
			}
		}
		for j, token := range l.mw.Before {
			key := capabilityKey(token, declarationSite(endpoint, l.mw.Name, "Before", j))
			note(i, l.mw.Name, "Before", j, key)
			for _, p := range providers[key] {
				if p == i {
					continue
				}
				if err := place(i, p, "Before", j, key); err != nil {
					return nil, nil, err
				}
			}
		}
	}

	// Bucket by stage, solve each bucket, then concatenate. Every edge the
	// constraints drew is within one stage — a cross-stage constraint drew
	// none — so a bucket's indegree counts only its own members, and the loop
	// below is the same Kahn run the ordering has always done, over one stage
	// at a time. The ready set is re-sorted each round rather than kept as a
	// heap: it is sorted by (Order, registration) on every round anyway, and
	// the round count is the number of layers in one stage.
	buckets := make(map[Stage][]int, len(declaredStages))
	for i := range layers {
		buckets[layers[i].mw.Stage] = append(buckets[layers[i].mw.Stage], i)
	}

	order := make([]int, 0, len(layers))
	for _, st := range declaredStages {
		members := buckets[st]
		if len(members) == 0 {
			continue
		}
		ready := make([]int, 0, len(members))
		for _, m := range members {
			if indegree[m] == 0 {
				ready = append(ready, m)
			}
		}
		for len(ready) > 0 {
			slices.SortFunc(ready, func(a, b int) int {
				if c := cmp.Compare(layers[a].mw.Order, layers[b].mw.Order); c != 0 {
					return c
				}
				return cmp.Compare(a, b)
			})
			at := ready[0]
			ready = ready[1:]
			order = append(order, at)
			for _, to := range sortedTargets(edges[at]) {
				indegree[to]--
				if indegree[to] == 0 {
					ready = append(ready, to)
				}
			}
		}
	}
	if len(order) != len(layers) {
		// No chain comes out of a cyclic graph, and the report describes an
		// assembly that is about to be abandoned; the failure is the only
		// thing worth reading here.
		return nil, nil, cycleError(endpoint, layers, order, edges)
	}

	out := make([]layer, 0, len(order))
	for _, at := range order {
		out = append(out, layers[at])
	}
	return out, reported, nil
}

// stageConflict builds the failure for a constraint whose provider is in
// another stage, pointing against the stage order. It is not a position the
// caller can be given quietly: the constraint asks for the opposite of where
// the stages put the two layers, and the fix is an edit to one of them. The
// text names both layers, both stages and the declaration, which is everything
// the reader needs to choose between moving a layer and dropping the
// constraint.
func stageConflict(endpoint string, declarer layer, field string, at int, provider layer, key reflect.Type) error {
	// After asks to sit inside the named layer, Before outside it. The
	// contradiction is that the stage order puts the declarer on the other
	// side, so the two words name opposite sides and the text has to keep them
	// apart.
	asked, placed := "inside", "outside"
	if field == "Before" {
		asked, placed = "outside", "inside"
	}
	return fmt.Errorf("%w: endpoint %q: middleware %q in stage %s declares %s[%d] %s, asking to "+
		"sit %s the layer standing for %s, but that layer, %q, is in stage %s, and the stage "+
		"order places %q %s %q: the two point opposite ways. A constraint across stages "+
		"holds only when it agrees with the stage order, because the boundary between stages is "+
		"the framework's and no edge moves a layer across it. Move one of the two layers to a "+
		"stage that matches, or drop the constraint if the stages already place the layer where "+
		"it wants to be",
		ErrStageConflict, endpoint, declarer.mw.Name, stageName(declarer.mw.Stage), field, at,
		typeName(key), asked, typeName(key), provider.mw.Name, stageName(provider.mw.Stage),
		declarer.mw.Name, placed, provider.mw.Name)
}

// unplacedCause reports whether a constraint placed nothing, and why. The
// declaring layer is skipped when its own edge is dropped, so what decides it
// is whether any other layer stands for the capability: if one does, the
// constraint binds to that one and placed the layer; if none does, the
// constraint placed nothing, and the capability having no representative at
// all is a different fixing action from the declaring layer being its only
// representative.
func unplacedCause(providers []int, self int) (remarkCause, bool) {
	selfOnly := false
	for _, p := range providers {
		if p != self {
			return "", false
		}
		selfOnly = true
	}
	if selfOnly {
		return causeSelfOnly, true
	}
	return causeNoProvider, true
}

// providersByCapability indexes the layers by the capabilities they stand for.
// Two tokens designating one capability land on one key, whoever wrote them,
// which is what lets a constraint reach a layer registered by a module it
// never names.
func providersByCapability(endpoint string, layers []layer) map[reflect.Type][]int {
	providers := make(map[reflect.Type][]int)
	for i, l := range layers {
		for j, token := range l.provides {
			key := capabilityKey(token, declarationSite(endpoint, l.mw.Name, "Provides", j))
			providers[key] = append(providers[key], i)
		}
	}
	return providers
}

// cycleError names the layers on a cycle along with their own declarations.
// Naming the members alone would leave the reader to hunt for the constraints
// that tied them together, and dropping one of those constraints is the whole
// of the fixing action.
//
// The walk starts from the lowest registration index still unresolved and
// follows successors in the same order, so one graph yields one text.
func cycleError(endpoint string, layers []layer, ordered []int, edges []map[int]bool) error {
	left := make(map[int]bool, len(layers))
	for i := range layers {
		left[i] = true
	}
	for _, at := range ordered {
		delete(left, at)
	}

	const (
		white = 0
		grey  = 1
		black = 2
	)
	color := make([]int, len(layers))
	var stack, cycle []int

	var visit func(at int) bool
	visit = func(at int) bool {
		color[at] = grey
		stack = append(stack, at)
		for _, to := range sortedTargets(edges[at]) {
			if !left[to] {
				continue
			}
			switch color[to] {
			case grey:
				cycle = slices.Clone(stack[slices.Index(stack, to):])
				return true
			case white:
				if visit(to) {
					return true
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[at] = black
		return false
	}
	for _, at := range sortedTargets(left) {
		if color[at] == white && visit(at) {
			break
		}
	}
	if len(cycle) == 0 {
		// Unreachable through the walk above: a Kahn run that stalls
		// leaves at least one cycle in what it did not resolve. Reporting
		// the whole remainder keeps the message from going empty if that
		// ever stops holding.
		cycle = sortedTargets(left)
	}

	described := make([]string, 0, len(cycle)+1)
	for _, at := range cycle {
		described = append(described, describeLayer(endpoint, layers[at]))
	}
	described = append(described, fmt.Sprintf("%q", layers[cycle[0]].mw.Name))

	return fmt.Errorf("%w: endpoint %q: %s. After and Before are hard constraints, so they have "+
		"to form an acyclic graph: drop one of the constraints on this cycle, or state an "+
		"Order — it only picks among the places the remaining constraints already allow",
		ErrMiddlewareCycle, endpoint, strings.Join(described, " -> "))
}

// describeLayer renders one layer of a cycle: its name, and the constraints it
// declared for itself.
func describeLayer(endpoint string, l layer) string {
	var parts []string
	if names := capabilityNames(endpoint, l, "After", l.mw.After); len(names) > 0 {
		parts = append(parts, "After "+strings.Join(names, ", "))
	}
	if names := capabilityNames(endpoint, l, "Before", l.mw.Before); len(names) > 0 {
		parts = append(parts, "Before "+strings.Join(names, ", "))
	}
	if len(parts) == 0 {
		return fmt.Sprintf("%q (declares no constraint of its own)", l.mw.Name)
	}
	return fmt.Sprintf("%q (%s)", l.mw.Name, strings.Join(parts, "; "))
}

// capabilityNames renders the capabilities a field designates, keeping the
// package path, so the reader can find the declaration the text asks them to
// drop.
func capabilityNames(endpoint string, l layer, field string, tokens []core.Token) []string {
	names := make([]string, 0, len(tokens))
	for j, token := range tokens {
		names = append(names, typeName(capabilityKey(token, declarationSite(endpoint, l.mw.Name, field, j))))
	}
	return names
}

// declarationSite locates a token for the panic an illegal one raises: which
// endpoint, which layer, which field, which position in it.
func declarationSite(endpoint, name, field string, at int) string {
	return fmt.Sprintf("endpoint %q middleware %q %s[%d]", endpoint, name, field, at)
}

// sortedTargets renders the keys of an edge set in ascending order, which is
// what keeps a walk over the graph independent of Go's map iteration order.
func sortedTargets[V any](m map[int]V) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
