package core

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// Resolve returns the instance of the one source that delivers a capability.
//
// The matching domain is the set of modules that declared the capability in
// Provides, together with the products the host supplied. A product that
// happens to implement a capability its module never declared is invisible
// here: the declaration is the sole authority, so assembly-time validation
// and run-time lookup face the same set. A supplied product comes from no
// module and states the capability it stands for as it is handed in.
//
// Nothing to take up is ErrMissingProvider, which is also how an optional
// dependency reads its own absence. More than one constructed provider is
// ErrAmbiguousProvider.
//
// Lookup does not filter by Requires. A dependency of a dependency never
// appears in one's own declarations, and taking it up is legal; Requires
// decides construction order and carries the pre-construction validation, it
// does not limit visibility.
func (r *Registry) Resolve(t Token) (any, error) {
	ct := capabilityType(t, "Resolve")

	r.mu.RLock()
	defer r.mu.RUnlock()

	// A supplied product and a declaring module cannot both hold the same
	// capability: Supply and Register reject that pair where it is made.
	// The two sources therefore never mix, and there is no second
	// ambiguity rule to state.
	if instance, ok := r.supplied[ct]; ok {
		return instance, nil
	}

	declarers := r.declarersLocked(ct)
	constructed := r.constructedAmongLocked(declarers)
	switch len(constructed) {
	case 1:
		return r.instances[constructed[0]], nil
	case 0:
		included, left := r.splitByParticipationLocked(declarers)
		return nil, missingProviderError(ct, included, left, r.enablement, "", "")
	default:
		return nil, fmt.Errorf("%w: capability %s is delivered by %s, and a single-valued "+
			"lookup cannot choose between them. Take them up together with ResolveAll, or "+
			"let the providers declare the capability Exclusive so the conflict is resolved "+
			"before construction",
			ErrAmbiguousProvider, typeName(ct), strings.Join(quoteAll(constructed), " and "))
	}
}

// ResolveAll returns every constructed instance that delivers a capability, in
// module-name order. The matching domain is the same as Resolve's. No provider
// is an empty slice, not an error.
//
// A capability held by a supplied product yields that one product. Name order
// says nothing about it and does not have to: the two kinds of source cannot
// hold the same capability, so a supplied product is never ordered against a
// module's.
//
// The instances do not carry module names: routing by name needs a name that
// is a domain concept, and the capability interface itself is where that
// belongs.
func (r *Registry) ResolveAll(t Token) []any {
	ct := capabilityType(t, "ResolveAll")

	r.mu.RLock()
	defer r.mu.RUnlock()

	if instance, ok := r.supplied[ct]; ok {
		return []any{instance}
	}

	names := r.constructedAmongLocked(r.declarersLocked(ct))
	out := make([]any, 0, len(names))
	for _, name := range names {
		out = append(out, r.instances[name])
	}
	return out
}

// Resources returns every declared resource matching the type the token
// designates, in module-name order, keeping the declaration order within a
// module. An interface target collects every implementation; a concrete target
// collects only values of that very type.
//
// The token designates a resource type, not a capability: the notation is the
// same but any element type is legal here.
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

// Enablement gives the verdict resolution reached for a module, not the stance
// the module itself took: a module resolution disabled reports StateDisabled
// with the reason resolution gave. It reports false for a name that was never
// registered, and for any name before resolution has run.
//
// A registered module this drive left out of its participating set reports
// StateDisabled with that as the reason. It never took a stance, and it is
// not running, which is what a caller filtering by enablement is asking
// about: a mechanism that collects declarations across the registry drops
// the ones belonging to modules this command did not include.
//
// Startup diagnostics are written to os.Stderr and the host cannot redirect
// them, since handing in a sink would need either a Run parameter or a
// package-level setter. This query is how a host feeds the same information
// into its own log.
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

// notIncludedReason is the verdict on a registered module this drive left
// out. It stays out of the startup diagnostics: those exist to catch an
// enabled set smaller than expected, and a module the host did not include is
// the host's own writing rather than a surprise.
const notIncludedReason = "not included in this command"

// splitByParticipationLocked divides declaring modules into the ones this
// drive included and the ones it left out. The enablement map holds exactly
// the participating set, so before resolution has run there is no such
// division to make and every declarer counts as included.
func (r *Registry) splitByParticipationLocked(declarers []string) (included, left []string) {
	if r.enablement == nil {
		return declarers, nil
	}
	for _, name := range declarers {
		if _, ok := r.enablement[name]; ok {
			included = append(included, name)
			continue
		}
		left = append(left, name)
	}
	return included, left
}

// matchesResourceType decides whether a declared resource satisfies the queried
// target. An interface target matches by assignability, so every implementation
// counts; a concrete target matches by type identity.
//
// Assignability alone would be wider than the assertion the typed wrapper then
// performs on what was selected: a []string is assignable to a named type over
// []string, and a bidirectional channel to a receive-only one, while neither is
// assertable to it. Selecting those would turn a legal declaration into a panic
// inside the library.
func matchesResourceType(vt, rt reflect.Type) bool {
	if rt.Kind() == reflect.Interface {
		return vt.AssignableTo(rt)
	}
	return vt == rt
}

// declarersLocked lists the modules that declared a capability, in name order.
func (r *Registry) declarersLocked(ct reflect.Type) []string {
	var names []string
	for _, name := range r.sortedNamesLocked() {
		if slices.Contains(declaredCapabilities(r.mods[name].module), ct) {
			names = append(names, name)
		}
	}
	return names
}

func (r *Registry) constructedAmongLocked(names []string) []string {
	var out []string
	for _, name := range names {
		if _, ok := r.instances[name]; ok {
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

// errorText carries a composed message while staying comparable to its
// sentinel through errors.Is.
type errorText struct {
	text    string
	wrapped error
}

func (e errorText) Error() string { return e.text }
func (e errorText) Unwrap() error { return e.wrapped }
