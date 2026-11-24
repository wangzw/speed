package http

import (
	"cmp"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/vislake/speed/pkg/core"
)

type layer struct {
	mw       Middleware
	provides []core.Token
}

var declaredStages = [...]Stage{
	StageObserve,
	StageGuard,
	StageIdentify,
	StageThrottle,
	StageRecord,
	StageAuthorize,
}

type orderingRemark struct {
	// endpoint says which endpoint the declaration was made on; with layer it locates the
	// declaration.
	endpoint string
	// layer is what the line is about: the layer that declared the constraint.
	layer string
	// field is "After" or "Before" and at is the position in it, which is what locates the
	// one declaration among several on the same layer.
	field string
	at    int
	// capability is the type the token designates, rendered with its package path, so the
	// reader can go and look at who was meant to stand for it.
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
	// causeStageSatisfied is a constraint whose provider is in another stage, in the
	// direction the stage order already gives.
	causeStageSatisfied remarkCause = "the constraint is already given by the stage order"
)

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
	// A layer that declares a constraint on a capability it delivers itself names itself
	// among the providers.
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

	// Bucket by stage, solve each bucket, then concatenate.
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
		// No chain comes out of a cyclic graph, and the report describes an assembly that is
		// about to be abandoned; the failure is the only thing worth reading here.
		return nil, nil, cycleError(endpoint, layers, order, edges)
	}

	out := make([]layer, 0, len(order))
	for _, at := range order {
		out = append(out, layers[at])
	}
	return out, reported, nil
}

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
		// Unreachable through the walk above: a Kahn run that stalls leaves at least one
		// cycle in what it did not resolve.
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

func sortedTargets[V any](m map[int]V) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
