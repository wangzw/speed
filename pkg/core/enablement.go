package core

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// stance pairs a descriptor with the enablement its Prepare stated, or with
// StateEnabled for a module the host supplied an instance for, whose Prepare
// does not run. Every module in the participating set takes a stance, so
// resolution runs over that set and the enabled set is a subset of it.
type stance struct {
	module Module
	state  Enablement
	// supplied marks a module the host supplied an instance for. Resolution
	// treats its stance like any other; the mark is only there for a failure to
	// say which party it applies to.
	supplied bool
}

// resolveEnablement turns the stances into the final verdict per module.
//
// A module states StateAuto, StateEnabled or StateDisabled. StateDisabled is
// the module's own decision, taken from its configuration: it expects to sit
// this run out, and the reason it gives travels unchanged into the verdict.
// core reads that reason nowhere. It does not decide whether standing down was
// the right answer, nor which absences are ordinary and which are mistakes: it
// propagates the fact that the module is not running, and why as the module
// said it, to the diagnostics and to everything that reasons about the run.
//
// A module that stood itself down delivers nothing this run, so it is no
// provider for exclusive resolution: its claim is neither propagated against
// nor adjudicated, and it cannot enable or disable a module beside it. A
// capability whose only provider stood down leaves the enabled set without a
// provider; dependency resolution reports that to whoever requires it, carrying
// the stance and its reason into the report.
//
// For the modules still running, resolution looks only at exclusive delivery;
// several providers of a capability nobody claims exclusively is the normal
// case. It runs on the snapshot the stances form, in two steps, so the outcome
// does not depend on traversal order:
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
func resolveEnablement(stances []stance) (map[string]Enablement, error) {
	stances = slices.SortedFunc(slices.Values(stances), func(a, b stance) int {
		return strings.Compare(a.module.Name, b.module.Name)
	})

	for _, s := range stances {
		if s.state.State != StateDisabled || s.state.Reason != "" {
			continue
		}
		return nil, fmt.Errorf("%w: module %q. The reason reaches the startup diagnostics "+
			"and every module that requires one of its capabilities, so a module that stands "+
			"itself down has to say why", ErrDisabledWithoutReason, s.module.Name)
	}

	// A module that stood itself down takes no part in exclusive delivery: it
	// delivers nothing this run, so its claim is neither contested nor counted.
	providers := make(map[reflect.Type][]stance)
	var contested []reflect.Type
	for _, s := range stances {
		if s.state.State == StateDisabled {
			continue
		}
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
		var remaining, exclusive, supplied []string
		for _, s := range providers[ct] {
			if _, down := standDown[s.module.Name]; down {
				continue
			}
			remaining = append(remaining, s.module.Name)
			if claimsExclusive(s.module, ct) {
				exclusive = append(exclusive, s.module.Name)
			}
			if s.supplied {
				supplied = append(supplied, s.module.Name)
			}
		}
		if len(remaining) > 1 && len(exclusive) > 0 {
			return nil, fmt.Errorf("%w: capability %s is still delivered by %s after "+
				"resolution, while %s claims it exclusively. Disable all but one provider, "+
				"or have the default implementation declare the capability Exclusive so it "+
				"stands down when another provider is explicitly enabled%s",
				ErrExclusiveViolated, typeName(ct),
				strings.Join(quoteAll(remaining), " and "),
				strings.Join(quoteAll(exclusive), " and "),
				suppliedPartiesNote(supplied))
		}
	}

	final := make(map[string]Enablement, len(stances))
	for _, s := range stances {
		// A module's own decision comes first: resolution did not touch it,
		// and its own reason is the fact the rest of the run reads.
		if s.state.State == StateDisabled {
			final[s.module.Name] = Enablement{State: StateDisabled, Reason: s.state.Reason}
			continue
		}
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

// suppliedPartiesNote is what the failure over an exclusive conflict adds when
// some of its parties are modules the host supplied an instance for, and nothing
// when none is. Such a module has its stance fixed at StateEnabled, so it never
// stands down, and leaving it out of the command tree does not take it out of
// the command: the advice that fits the other parties does not reach it, and the
// way out that does is to supply it no instance.
func suppliedPartiesNote(supplied []string) string {
	if len(supplied) == 0 {
		return ""
	}
	have := "has"
	if len(supplied) > 1 {
		have = "each have"
	}
	return fmt.Sprintf(". %s %s a supplied instance, which fixes the stance of its module at "+
		"StateEnabled: the module does not stand down, and leaving it out of the command tree "+
		"does not take it out of this command. Of the providers that are not to stay, stop "+
		"supplying an instance to each that has one and leave the others out of this command",
		strings.Join(quoteAll(supplied), " and "), have)
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
