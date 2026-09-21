package core

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// stance pairs a descriptor with the enablement its Prepare stated. Only the
// modules this drive includes take a stance, so resolution runs over the
// participating set and the enabled set is a subset of it.
type stance struct {
	module Module
	state  Enablement
}

// resolveEnablement turns the stances into the final verdict per module.
//
// Resolution only looks at exclusive delivery; several providers of a
// capability nobody claims exclusively is the normal case. It runs on the
// snapshot the stances form, in two steps, so the outcome does not depend on
// traversal order:
//
//  1. propagation: for a contested capability with an explicitly enabled
//     provider, the providers that claim it exclusively while stating
//     StateAuto stand down.
//  2. adjudication: among the providers still enabled, more than one with an
//     exclusive claimant among them is a failure.
//
// Standing down is an exclusive claim restraining itself: a default
// implementation that claims exclusivity says "I take it alone unless someone
// shows up". A provider that never claimed exclusivity has nothing to restrain,
// so propagation leaves it alone; disabling it too would let a doomed
// exclusive default drag down the non-exclusive defaults beside it.
//
// Every stance here belongs to a module this drive took in at run level, and
// such a module has to start. A stance of StateDisabled is therefore refused
// rather than honoured: the module is misusing the API, and the startup stops
// with that named, where any other premise it cannot meet would stop it.
func resolveEnablement(stances []stance) (map[string]Enablement, error) {
	stances = slices.SortedFunc(slices.Values(stances), func(a, b stance) int {
		return strings.Compare(a.module.Name, b.module.Name)
	})

	for _, s := range stances {
		if s.state.State != StateDisabled {
			continue
		}
		return nil, fmt.Errorf("%w: %q. A module "+
			"taken in at run level must start, so it cannot stand itself down: when it cannot "+
			"start — a missing setting, an unmet premise — it returns an error saying what is "+
			"absent, and the startup fails with that. StateDisabled is not a stance; it is only "+
			"the verdict resolution gives when a provider stands down over an exclusive claim",
			ErrDisabledStance, s.module.Name)
	}

	providers := make(map[reflect.Type][]stance)
	var contested []reflect.Type
	for _, s := range stances {
		for _, ct := range declaredCapabilities(s.module) {
			providers[ct] = append(providers[ct], s)
		}
	}
	for ct, group := range providers {
		for _, s := range group {
			if claimsExclusive(s.module, ct) {
				contested = append(contested, ct)
				break
			}
		}
	}
	slices.SortFunc(contested, func(a, b reflect.Type) int {
		return strings.Compare(typeName(a), typeName(b))
	})

	// Propagation.
	standDown := make(map[string]string)
	for _, ct := range contested {
		var explicit []string
		for _, s := range providers[ct] {
			if s.state.State == StateEnabled {
				explicit = append(explicit, s.module.Name)
			}
		}
		if len(explicit) == 0 {
			continue
		}
		for _, s := range providers[ct] {
			if s.state.State != StateAuto || !claimsExclusive(s.module, ct) {
				continue
			}
			if _, already := standDown[s.module.Name]; already {
				continue
			}
			standDown[s.module.Name] = fmt.Sprintf(
				"stood down: capability %s already has an explicitly enabled provider (%s)",
				typeName(ct), strings.Join(quoteAll(explicit), ", "))
		}
	}

	// Adjudication. What counts is whether an exclusive claimant is among
	// the providers still enabled, not the shape of their stances: a
	// provider propagation disabled no longer claims anything.
	for _, ct := range contested {
		var remaining, exclusive []string
		for _, s := range providers[ct] {
			if _, down := standDown[s.module.Name]; down {
				continue
			}
			remaining = append(remaining, s.module.Name)
			if claimsExclusive(s.module, ct) {
				exclusive = append(exclusive, s.module.Name)
			}
		}
		if len(remaining) > 1 && len(exclusive) > 0 {
			return nil, fmt.Errorf("%w: capability %s is still delivered by %s after "+
				"resolution, while %s claims it exclusively. Disable all but one provider, "+
				"or have the default implementation declare the capability Exclusive so it "+
				"stands down when another provider is explicitly enabled",
				ErrExclusiveViolated, typeName(ct),
				strings.Join(quoteAll(remaining), " and "),
				strings.Join(quoteAll(exclusive), " and "))
		}
	}

	final := make(map[string]Enablement, len(stances))
	for _, s := range stances {
		// Membership is the verdict, not the reason's content: a provider
		// propagation stood down is disabled whatever its reason text turned
		// out to be.
		if reason, down := standDown[s.module.Name]; down {
			final[s.module.Name] = Enablement{State: StateDisabled, Reason: reason}
			continue
		}
		final[s.module.Name] = Enablement{State: StateEnabled}
	}
	return final, nil
}

// declaredCapabilities lists the capability types a module delivers, without
// repetition.
func declaredCapabilities(m Module) []reflect.Type {
	var out []reflect.Type
	for i, prov := range m.Provides {
		ct := capabilityType(prov.Token, fmt.Sprintf("module %q Provides[%d]", m.Name, i))
		if !slices.Contains(out, ct) {
			out = append(out, ct)
		}
	}
	return out
}

// claimsExclusive reports whether a module claims a capability for itself.
func claimsExclusive(m Module, ct reflect.Type) bool {
	for i, prov := range m.Provides {
		if prov.Exclusive && capabilityType(prov.Token, fmt.Sprintf("module %q Provides[%d]", m.Name, i)) == ct {
			return true
		}
	}
	return false
}
