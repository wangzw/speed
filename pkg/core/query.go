package core

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// Resolve returns the instance of the one module that delivers a capability.
//
// The matching domain is the set of modules that declared the capability in
// Provides. Of those, the ones with an instance count: one their New
// constructed, or one the host supplied, which exists from Supply on and which
// a lookup does not tell apart from a constructed one. An instance that
// happens to implement a capability its module never declared is invisible
// here: the declaration is the sole authority, so assembly-time validation
// and run-time lookup face the same set.
//
// Nothing to take up is ErrMissingProvider, which is also how an optional
// dependency reads its own absence. More than one provider with an instance is
// ErrAmbiguousProvider. In Prepare only the supplied instances exist, so a
// lookup that finds the one there can meet this error later, once construction
// has given another provider of the capability its instance.
//
// Lookup does not filter by Requires. A dependency of a dependency never
// appears in one's own declarations, and taking it up is legal; Requires
// decides construction order and carries the pre-construction validation, it
// does not limit visibility.
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

// ResolveAll returns every instance that delivers a capability, in module-name
// order. The matching domain is the same as Resolve's. No provider is an empty
// slice, not an error.
//
// The instances do not carry module names: routing by name needs a name that
// is a domain concept, and the capability interface itself is where that
// belongs.
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

// Enablement gives the verdict the registry reached for a module, not a live
// reading of its stance. A module that stated StateDisabled reports it with the
// reason the module gave; a provider resolution stood down reports StateDisabled
// with the reason resolution gave. core repeats either reason as it received it.
// It reports false for a name that was never registered, and for any name before
// resolution has run.
//
// A registered module this drive left out of its participating set reports
// StateDisabled with that as the reason. It is not running, which is what a
// caller asking "is it running" needs. Why it is not running is a different
// question and this query does not answer it: a module that stood itself down,
// a resolution that stood the module down, a module the command never asked
// for, and one it reached for its declarations alone all arrive here as the same
// state. A mechanism collecting declarations filters by its own domain instead,
// and Participating and Declaring are what it composes.
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

// Participating reports whether the module is in this drive's participating
// set: taken in at run level, pulled in by the dependency closure beside a
// module that was, or one the host supplied an instance for.
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
// consumption point that has to tell a module resolution stood down from one
// this command never asked for asks this beside Enablement.
func (r *Registry) Participating(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.participating[name]
}

// Declaring reports whether the module's declarations are within reach of
// this drive. It is true for a participant, for a module the dependency
// closure pulled in beside one, for a module the host supplied an instance
// for, and for a module the drive reached for its declarations alone.
//
// This is the query a consumption point whose domain is the declaring set
// filters by. One whose domain is the enabled set asks Enablement, and one
// whose domain is every registered module asks neither.
//
// Like Participating it is false before a drive has begun and for a name no
// module registered. Neither answers whether the module runs: a module
// reached for its declarations alone never does, and one taken in at run level
// can still come out of resolution with StateDisabled, when it stands down
// over an exclusive claim.
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
// otherwise read. It is false for every module in the participating set, a
// module the host supplied an instance for included: the override belongs to a
// module that is reached for its declarations alone.
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

// nothingBuiltLocked says why no module's New has run at the moment of a
// lookup, or returns "" once construction has begun.
//
// A lookup made before construction finds no instance but the ones the host
// supplied, and the order of construction has nothing to do with it: Requires
// orders construction and none is under way. Three moments come to this, and
// each has a fix of its own. The caller holds the lock.
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

// splitDeclarersLocked divides the modules declaring a capability by what this
// drive made of them: the ones it took in at run level, the ones it reached for
// their declarations alone, and the ones it did not take in at all. The first
// group still includes a module that stood itself down or was stood down in
// resolution; the caller reads the enablement verdict to tell those apart.
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

// exclusiveAmongLocked filters module names down to the ones that claim the
// capability exclusively. Those are the modules the dependency closure leaves
// out on its own, so a lookup that finds no provider among them has a fix of
// its own to report. The caller holds the lock.
func (r *Registry) exclusiveAmongLocked(names []string, ct reflect.Type) []string {
	var out []string
	for _, name := range names {
		if claimsExclusive(r.mods[name].module, ct) {
			out = append(out, name)
		}
	}
	return out
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

// instancedAmongLocked filters module names down to the ones that have an
// instance, whether the module's New constructed it or the host supplied it.
// The caller holds the lock.
func (r *Registry) instancedAmongLocked(names []string) []string {
	var out []string
	for _, name := range names {
		if _, ok := r.instances[name]; ok {
			out = append(out, name)
		}
	}
	return out
}

// lackingNewAmongLocked filters module names down to the ones that declare no
// New callback. A module like that builds no instance, so the one it has can only
// have come from the host. The caller holds the lock.
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

// errorText carries a composed message while staying comparable to its
// sentinel through errors.Is.
type errorText struct {
	text    string
	wrapped error
}

func (e errorText) Error() string { return e.text }
func (e errorText) Unwrap() error { return e.wrapped }
