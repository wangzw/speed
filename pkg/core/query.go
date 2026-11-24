package core

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// Resolve returns the instance of the one module that declared the capability in Provides.
// Nothing to take up is ErrMissingProvider; more than one provider with an instance is
// ErrAmbiguousProvider.
func (r *Registry) Resolve(t Token) (any, error) {
	ct := capabilityType(t, "Resolve")

	r.mu.RLock()
	defer r.mu.RUnlock()

	declarers := r.declarersLocked(ct)
	instanced := r.instancedAmongLocked(declarers)
	switch len(instanced) {
	case 1:
		return r.instances[instanced[0]], nil
	case 0:
		running, declaringOnly, left := r.splitDeclarersLocked(declarers)
		return nil, missingProviderError(ct, running, declaringOnly, left,
			r.exclusiveAmongLocked(left, ct), r.lackingNewAmongLocked(declarers), r.enablement,
			"", origin{}, r.nothingBuiltLocked())
	default:
		return nil, fmt.Errorf("%w: capability %s is delivered by %s, and a single-valued "+
			"lookup cannot choose between them. Take them up together with ResolveAll, or "+
			"let the providers declare the capability Exclusive so the conflict is resolved "+
			"before construction",
			ErrAmbiguousProvider, typeName(ct), strings.Join(quoteAll(instanced), " and "))
	}
}

// ResolveAll returns every instance that delivers a capability, in module-name order.
// The matching domain is the same as Resolve's.
// No provider is an empty slice, not an error.
func (r *Registry) ResolveAll(t Token) []any {
	ct := capabilityType(t, "ResolveAll")

	r.mu.RLock()
	defer r.mu.RUnlock()

	names := r.instancedAmongLocked(r.declarersLocked(ct))
	out := make([]any, 0, len(names))
	for _, name := range names {
		out = append(out, r.instances[name])
	}
	return out
}

// Resources returns every declared resource matching the type the token designates, in module-name
// order, keeping the declaration order within a module. An interface target collects every
// implementation; a concrete target collects only values of that very type.
func (r *Registry) Resources(t Token) []Resource[any] {
	rt := resourceType(t, "Resources")

	r.mu.RLock()
	defer r.mu.RUnlock()

	out := []Resource[any]{}
	for _, name := range r.sortedNamesLocked() {
		for _, value := range r.mods[name].module.Resources {
			vt := reflect.TypeOf(value)
			if vt == nil || !matchesResourceType(vt, rt) {
				continue
			}
			out = append(out, Resource[any]{Module: name, Value: value})
		}
	}
	return out
}

// Enablement gives the verdict the registry reached for a module, not a live reading of its stance.
// It reports false for a name never registered and for any name before resolution has run; a
// registered module this drive left out reports StateDisabled with that as the reason.
func (r *Registry) Enablement(name string) (Enablement, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.enablement == nil {
		return Enablement{}, false
	}
	if e, ok := r.enablement[name]; ok {
		return e, true
	}
	if _, registered := r.mods[name]; registered {
		return Enablement{State: StateDisabled, Reason: notIncludedReason}, true
	}
	return Enablement{}, false
}

const notIncludedReason = "not in this command's participating set"

// Participating reports whether the module is in this drive's participating set: taken in at run
// level, pulled in by the dependency closure beside a module that was, or one the host supplied an
// instance for.
func (r *Registry) Participating(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.participating[name]
}

// Declaring reports whether the module's declarations are within reach of this drive.
func (r *Registry) Declaring(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.participating[name] {
		return true
	}
	_, ok := r.declaringOnly[name]
	return ok
}

// AllResourcesInForce reports whether this drive took the module in with every declaration in
// force, overriding the domain a consumption point would otherwise read.
func (r *Registry) AllResourcesInForce(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.declaringOnly[name].AllResources
}

func (r *Registry) nothingBuiltLocked() string {
	switch {
	case r.constructing:
		return ""
	case !r.running:
		return "no drive has started, so no module's New has run yet: an instance is " +
			"constructed in New, during a drive, unless the host supplied it before the drive"
	case r.enablement == nil:
		return "the lookup is made in Prepare, before any module's New has run: only an " +
			"instance the host supplied can be taken up there, so take this one up in New or " +
			"later, where declaring the capability in Requires orders its provider first"
	default:
		return "this drive constructs nothing, since it stops at StagePrepare: only an instance " +
			"the host supplied exists in it, and a command that takes the capability up from a " +
			"constructed one drives to StageNew or deeper"
	}
}

func (r *Registry) splitDeclarersLocked(declarers []string) (running, declaringOnly, left []string) {
	if r.participating == nil {
		return declarers, nil, nil
	}
	for _, name := range declarers {
		switch {
		case r.participating[name]:
			running = append(running, name)
		case r.declarationOf(name):
			declaringOnly = append(declaringOnly, name)
		default:
			left = append(left, name)
		}
	}
	return running, declaringOnly, left
}

func (r *Registry) declarationOf(name string) bool {
	_, ok := r.declaringOnly[name]
	return ok
}

func (r *Registry) exclusiveAmongLocked(names []string, ct reflect.Type) []string {
	var out []string
	for _, name := range names {
		if claimsExclusive(r.mods[name].module, ct) {
			out = append(out, name)
		}
	}
	return out
}

// Assignability alone would select values the typed assertion then panics on.
func matchesResourceType(vt, rt reflect.Type) bool {
	if rt.Kind() == reflect.Interface {
		return vt.AssignableTo(rt)
	}
	return vt == rt
}

func (r *Registry) declarersLocked(ct reflect.Type) []string {
	var names []string
	for _, name := range r.sortedNamesLocked() {
		if slices.Contains(declaredCapabilities(r.mods[name].module), ct) {
			names = append(names, name)
		}
	}
	return names
}

func (r *Registry) instancedAmongLocked(names []string) []string {
	var out []string
	for _, name := range names {
		if _, ok := r.instances[name]; ok {
			out = append(out, name)
		}
	}
	return out
}

func (r *Registry) lackingNewAmongLocked(names []string) []string {
	var out []string
	for _, name := range names {
		if r.mods[name].module.New == nil {
			out = append(out, name)
		}
	}
	return out
}

func (r *Registry) sortedNamesLocked() []string {
	names := slices.Clone(r.order)
	slices.Sort(names)
	return names
}

func interfacePackage(ct reflect.Type) string {
	if ct.PkgPath() != "" {
		return ct.PkgPath()
	}
	return "an unnamed interface type"
}

func quoteAll(names []string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, fmt.Sprintf("%q", name))
	}
	return out
}

type errorText struct {
	text    string
	wrapped error
}

func (e errorText) Error() string { return e.text }
func (e errorText) Unwrap() error { return e.wrapped }
