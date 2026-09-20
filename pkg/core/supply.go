package core

import (
	"fmt"
	"reflect"
	"slices"
)

// Supply hands the registry a product the host built itself, under the
// capability the token designates. Any module reaches it with Resolve, in any
// stage, and taking it up needs no Requires declaration: it exists before the
// first module is constructed, so there is nothing to order it against.
//
// The configuration reader arrives this way. The registry does not know what
// it was handed; it holds the instance and answers lookups for that one
// capability.
//
// A supplied product is not a module. It has no descriptor, takes no stance,
// plays no part in resolution, is absent from Modules, and has no stage
// callbacks. Nothing here writes to the lifecycle list either, which is what
// keeps rollback off it: the party that supplied it releases it, and a drive
// closes only the instances it constructed itself.
//
// Supply panics on a capability already supplied, on one a registered module
// declares in Provides, on a product that does not implement the capability,
// and on a call made once the drive has started. Each is decided by this one
// call against what is already registered, which is the same criterion the
// registration-time panics use.
func (r *Registry) Supply(t Token, instance any) {
	ct := capabilityType(t, "Supply")
	site := callerPackage(3)

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.running {
		panic(fmt.Sprintf("core: Supply from %s for capability %s was called after the drive "+
			"started. The host hands a product in before the drive begins: a module that "+
			"supplied one from inside a stage would be reaching around the mechanism that "+
			"decides what the registry holds", site, typeName(ct)))
	}
	if it := reflect.TypeOf(instance); it == nil || !it.AssignableTo(ct) {
		got := "nothing"
		if it != nil {
			got = typeName(it)
		}
		panic(fmt.Sprintf("core: Supply from %s hands %s under capability %s, which it does "+
			"not implement. A supplied product answers every lookup for that capability, "+
			"so it has to satisfy it", site, got, typeName(ct)))
	}
	if earlier, ok := r.suppliedSites[ct]; ok {
		panic(fmt.Sprintf("core: capability %s is supplied twice: from %s and from %s. "+
			"A supplied product holds the capability on its own, so the second one has "+
			"no place to go", typeName(ct), earlier, site))
	}
	for _, name := range r.sortedNamesLocked() {
		reg := r.mods[name]
		if !slices.Contains(declaredCapabilities(reg.module), ct) {
			continue
		}
		panic(fmt.Sprintf("core: capability %s is supplied from %s and declared in Provides "+
			"by module %q registered from %s. A supplied product does not go through "+
			"construction, so the two cannot both answer a lookup for it; the available "+
			"set is decided by imports, so that module may have been pulled in by a "+
			"dependency rather than by the host itself",
			typeName(ct), site, name, reg.site))
	}

	r.supplied[ct] = instance
	r.suppliedSites[ct] = site
}

// suppliedCapabilities snapshots the capabilities that are already in the
// registry without going through construction. Dependency resolution treats
// them as met and orders nothing against them.
func (r *Registry) suppliedCapabilities() map[reflect.Type]bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[reflect.Type]bool, len(r.supplied))
	for ct := range r.supplied {
		out[ct] = true
	}
	return out
}
