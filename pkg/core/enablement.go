package core

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
)

type stance struct {
	module   Module
	state    Enablement
	supplied bool
}

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
		if s.state.State == StateDisabled {
			final[s.module.Name] = Enablement{State: StateDisabled, Reason: s.state.Reason}
			continue
		}
		if reason, down := standDown[s.module.Name]; down {
			final[s.module.Name] = Enablement{State: StateDisabled, Reason: reason}
			continue
		}
		final[s.module.Name] = Enablement{State: StateEnabled}
	}
	return final, nil
}

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

func claimsExclusive(m Module, ct reflect.Type) bool {
	for i, prov := range m.Provides {
		if prov.Exclusive && capabilityType(prov.Token, fmt.Sprintf("module %q Provides[%d]", m.Name, i)) == ct {
			return true
		}
	}
	return false
}
