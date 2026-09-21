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
		running, declaringOnly, left := r.splitDeclarersLocked(declarers)
		return nil, missingProviderError(ct, running, declaringOnly, left, r.enablement, "", "")
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
// not running, which is what a caller asking "is it running" needs. Why it
// is not running is a different question and this query does not answer it:
// a stance against running, a resolution that stood the module down, and a
// command that never asked for it all arrive here as the same state. A
// mechanism collecting declarations filters by its own domain instead, and
// Participating and Declaring are what it composes.
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

// notIncludedReason is the verdict on a registered module this drive did not
// take in at run level. Two situations arrive here and this query is not the
// place to tell them apart: a module the command never named, and one it
// reached for its declarations alone. Neither is in the participating set, and
// the reason is about that set -- saying such a module "is not included in this
// command" would be false of the second, which is included in it, and the host
// reads this reason to find out why its module is not running.
//
// It stays out of the startup diagnostics: those exist to catch an enabled set
// smaller than expected, and a module the host did not take in at run level is
// the host's own writing rather than a surprise.
const notIncludedReason = "not in this command's participating set"

// Participating reports whether this drive took the module in at run level.
//
// It is false for a module the drive reached for its declarations alone: that
// module is not running, and this query is about running. It is false for a
// name no module registered, and false for every name before a drive has
// begun, since until then there is no command whose set could be asked about.
//
// It publishes what the enablement map made unanswerable. That map holds the
// participating set and nothing else, so "in the map" and "participating" are
// the same fact; what it cannot say is which of a disabled module's several
// situations produced the verdict, and by reporting a module the command
// never asked for as disabled it made those situations look alike. A
// consumption point that has to tell "it stated it does not run" from "this
// command never asked" asks this beside Enablement.
func (r *Registry) Participating(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.participating[name]
}

// Declaring reports whether the module's declarations are within reach of
// this drive. It is true for a participant, for a module the dependency
// closure pulled in beside one, and for a module the drive reached for its
// declarations alone.
//
// This is the query a consumption point whose domain is the declaring set
// filters by. One whose domain is the enabled set asks Enablement, and one
// whose domain is every registered module asks neither.
//
// Like Participating it is false before a drive has begun and for a name no
// module registered. Neither answers whether the module runs: a module
// reached for its declarations alone never does, and one that is declaring
// and participating may still have stated that it does not.
func (r *Registry) Declaring(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.participating[name] {
		return true
	}
	_, ok := r.declaringOnly[name]
	return ok
}

// AllResourcesInForce reports whether this drive took the module in with
// every declaration in force, overriding the domain a consumption point would
// otherwise read.
//
// It is the host's override of a decision this package does not make: which
// set a declaration is collected from belongs to the consumption point, and
// this says to skip that and take the module's declarations whatever they
// are. A consumption point that reads the enabled set asks this beside its
// own reading, and a module for which it answers true is collected as though
// it were running.
func (r *Registry) AllResourcesInForce(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.declaringOnly[name].AllResources
}

// splitDeclarersLocked divides the modules declaring a capability by what this
// drive made of them: the ones it runs, the ones it reached for their
// declarations alone, and the ones it did not take in at all.
//
// The middle group is the one a report has to name separately. Such a module
// was taken in, so saying it was not is false and the fix that reading suggests
// -- add it to the command's Include -- constructs a module the command was
// written not to build. What it cannot do is deliver anything: it is never
// constructed, however much it declares.
//
// Before a drive has settled the sets there is no division to make, and every
// declarer counts as running. The caller holds the lock.
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

// declarationOf reports whether the name was taken in for its declarations
// alone. The caller holds the lock.
func (r *Registry) declarationOf(name string) bool {
	_, ok := r.declaringOnly[name]
	return ok
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
