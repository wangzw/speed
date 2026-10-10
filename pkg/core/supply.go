package core

import (
	"fmt"
	"reflect"
	"strings"
)

// suppliedInstance is what the registry keeps about an instance the host
// supplied, besides the instance itself.
type suppliedInstance struct {
	site string // package path of the caller of Supply
}

// objectKey is the identity of the object an instance refers to: the dynamic
// type of the reference together with the address it points at. The type is
// part of it because two references of different types can share an address,
// a struct and a pointer to its first field being the usual pair, and they are
// not the same object.
type objectKey struct {
	typ  reflect.Type
	addr uintptr
}

// Supply hands the registry an instance the host built itself, as the instance
// of one registered module. It stands where the instance that module's New
// would construct stands: the module's Prepare and New do not run, its stance
// is fixed at StateEnabled, and the module joins the participating set when the
// drive begins, whether or not the command tree names it. Everything else about
// the module is as it is for any other: its descriptor, Requires and Resources,
// and the callbacks from Migrate to Close, which receive this instance. The
// instance exists from this call on, so Resolve finds it in any stage, Prepare
// included, and the drive counts it as constructed from its first step:
// whatever stage the drive ends in, the module's Stop and Close run on it.
//
// Which module the instance belongs to is settled in one of two ways. Given a
// module name, the constant the package that registers the module exports, the
// name decides: the module has to be registered and the instance has to
// implement every capability the module declares in Provides, the test the
// registry makes of a constructed instance too. A module that declares none can
// be supplied this way as well. Without a name, the instance is claimed. The
// candidates are the registered modules that declare at least one capability
// and whose every capability the instance implements. A single candidate is the
// module. Among several, it is the one registered from the package that defines
// the instance's type, provided there is exactly one: that package defines the
// named type left once pointers are stripped from the dynamic type, and a
// module is registered from the package that called Register for it. Claiming
// by package rests on a packaging convention, that a module's product is
// defined in the package that registers it. Where it does not hold, the host
// names the module.
//
// An instance belongs to one module and a module has one instance. The same
// object cannot be supplied for two modules; an object is the same when it is
// a non-nil pointer to a type of non-zero size, a non-nil map or a non-nil
// channel, and the dynamic type and the address both match. Any other instance
// has no identity to compare and is not checked. The check covers one registry.
// Register does not revisit an instance already supplied, so a module is
// registered before an instance is supplied for it.
//
// Supply panics, naming the package of the caller, when the drive has started,
// when instance is nil, when more than one module is named, when the named
// module is not registered, when the instance does not implement what the
// module declares, when no module can be claimed or the candidates cannot be
// told apart by package, when the module already has a supplied instance, and
// when the same object was supplied for another module. Each is decided by this
// one call against what is already registered and supplied, which is the
// criterion the registration-time panics use.
func (r *Registry) Supply(instance any, module ...string) {
	site := callerPackage(3)

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.running {
		panic(fmt.Sprintf("core: Supply from %s was called after the drive started. A supplied "+
			"instance decides whether its module is in the participating set, and that set is "+
			"settled when the drive begins, so hand the instance in before the drive; a module "+
			"that supplied one from inside a stage would be reaching around the mechanism that "+
			"decides what the registry holds", site))
	}
	if instance == nil {
		panic(fmt.Sprintf("core: Supply from %s was handed a nil instance. Hand in the instance the "+
			"host built, the one the module's New would otherwise have put there; a typed nil "+
			"pointer is not nil here and is claimed by its dynamic type", site))
	}
	if len(module) > 1 {
		panic(fmt.Sprintf("core: Supply from %s names %d modules (%s). An instance belongs to "+
			"exactly one module, so give at most one name; an object that delivers the "+
			"capabilities of several modules is declared by one module, in its Provides",
			site, len(module), strings.Join(quoteAll(module), ", ")))
	}

	it := reflect.TypeOf(instance)
	var name string
	if len(module) == 1 {
		name = r.namedModuleLocked(module[0], it, site)
	} else {
		name = r.claimedModuleLocked(it, site)
	}

	if earlier, ok := r.supplied[name]; ok {
		panic(fmt.Sprintf("core: Supply from %s hands module %q an instance, and it already has "+
			"one, supplied from %s. A module has one instance, so a second has no place to go: "+
			"supply one instance for it, or name the module the second is meant for. The "+
			"available set is decided by imports, so one of the two calls may come from a "+
			"package the host imports rather than from the host itself",
			site, name, earlier.site))
	}
	key, hasIdentity := objectOf(instance)
	if hasIdentity {
		if owner, ok := r.suppliedObjects[key]; ok {
			panic(fmt.Sprintf("core: Supply from %s hands module %q the %s that was already "+
				"supplied for module %q, from %s. One object belongs to one module: declare the "+
				"capabilities it delivers in the Provides of one module and supply it once, or "+
				"supply a separate object for each",
				site, name, instanceTypeName(it), owner, r.supplied[owner].site))
		}
	}

	r.instances[name] = instance
	r.supplied[name] = suppliedInstance{site: site}
	if hasIdentity {
		r.suppliedObjects[key] = name
	}
}

// namedModuleLocked checks the module the host named against the instance and
// returns its name. The caller holds the lock.
//
// The name is the host's decision and is not cross-checked against the type of
// the instance, so a module that declares no capability can be named like any
// other. What the instance still has to do is deliver everything the module
// declares.
func (r *Registry) namedModuleLocked(name string, it reflect.Type, site string) string {
	reg, ok := r.mods[name]
	if !ok {
		panic(fmt.Sprintf("core: Supply from %s names module %q, which is not registered. A "+
			"module is registered before an instance is supplied for it: the process registry "+
			"is filled by init, so the package that registers the module has to be imported by "+
			"the host, and a registry made with New has the module registered by hand first. "+
			"The name is the one the registering package exports as a constant", site, name))
	}
	if missing := missingCapabilities(it, declaredCapabilities(reg.module)); len(missing) > 0 {
		panic(fmt.Sprintf("core: Supply from %s hands %s to module %q, but the instance does "+
			"not implement %s, which the module declares in Provides. A supplied instance "+
			"stands where the module's New would have put one, so hand in one that delivers "+
			"everything the module declares, or name the module this instance is for",
			site, instanceTypeName(it), name, capabilityList(missing)))
	}
	return name
}

// claimedModuleLocked finds the module an instance belongs to when the host
// named none, and returns its name. The caller holds the lock.
//
// Registered modules are visited in name order so that the answer and the
// message do not depend on the order they were registered in.
func (r *Registry) claimedModuleLocked(it reflect.Type, site string) string {
	type partial struct {
		name    string
		missing []reflect.Type
	}
	var candidates []string
	var near []partial
	for _, name := range r.sortedNamesLocked() {
		declared := declaredCapabilities(r.mods[name].module)
		if len(declared) == 0 {
			continue
		}
		missing := missingCapabilities(it, declared)
		switch {
		case len(missing) == 0:
			candidates = append(candidates, name)
		case len(missing) < len(declared):
			near = append(near, partial{name: name, missing: missing})
		}
	}

	switch len(candidates) {
	case 0:
		var b strings.Builder
		fmt.Fprintf(&b, "core: Supply from %s hands %s, and no registered module can take it: a "+
			"candidate declares at least one capability in Provides and the instance "+
			"implements all of them", site, instanceTypeName(it))
		if len(near) > 0 {
			var lacking []string
			for _, p := range near {
				lacking = append(lacking, fmt.Sprintf("%q registered from %s lacks %s",
					p.name, r.mods[p.name].site, capabilityList(p.missing)))
			}
			fmt.Fprintf(&b, ". It implements only part of what some modules declare: %s. Naming "+
				"one of them does not help: a module takes an instance only if the instance "+
				"implements every capability the module declares. Hand in an instance that "+
				"implements everything the module declares, or correct what the module declares "+
				"in Provides", strings.Join(lacking, "; "))
		} else {
			b.WriteString(". A module is registered before an instance is supplied for it, and " +
				"takes the instance only if the instance implements every capability the module " +
				"declares. Register the module first, or hand in an instance that implements " +
				"everything the module declares")
		}
		b.WriteString(". Naming the module, with the constant its package exports, supplies an " +
			"instance for a module that declares nothing")
		panic(b.String())
	case 1:
		return candidates[0]
	}

	pkg := typePackage(it)
	var here []string
	for _, name := range candidates {
		if r.mods[name].site == pkg {
			here = append(here, name)
		}
	}
	if len(here) == 1 {
		return here[0]
	}

	listed := make([]string, 0, len(candidates))
	for _, name := range candidates {
		listed = append(listed, fmt.Sprintf("%q registered from %s", name, r.mods[name].site))
	}
	var why string
	switch {
	case pkg == "":
		why = "its type is not a named type, so no package defines it"
	case len(here) == 0:
		why = fmt.Sprintf("its type is defined in %s and none of them is registered from there", pkg)
	default:
		why = fmt.Sprintf("its type is defined in %s and more than one of them is registered "+
			"from there: %s", pkg, strings.Join(quoteAll(here), " and "))
	}
	panic(fmt.Sprintf("core: Supply from %s hands %s, which several registered modules could take: "+
		"%s. Claiming it by the package that defines its type does not settle it, because %s. "+
		"Name the module, with the constant its package exports",
		site, instanceTypeName(it), strings.Join(listed, ", "), why))
}

// implements reports whether an instance of dynamic type it delivers the
// capability ct. It is the one test the registry applies to an instance, whether
// the module's New constructed it or the host supplied it.
func implements(it, ct reflect.Type) bool {
	return it != nil && it.AssignableTo(ct)
}

// missingCapabilities lists, in declaration order, the capabilities an instance
// of dynamic type it does not deliver.
func missingCapabilities(it reflect.Type, declared []reflect.Type) []reflect.Type {
	var missing []reflect.Type
	for _, ct := range declared {
		if !implements(it, ct) {
			missing = append(missing, ct)
		}
	}
	return missing
}

// typePackage reports the import path of the package that defines the named
// type an instance's dynamic type leads to once pointers are stripped. It is
// empty for a type no package defines: a built-in type, or one written out in
// place such as a slice, a map or a function type.
func typePackage(it reflect.Type) string {
	for it.Kind() == reflect.Pointer && it.Name() == "" {
		it = it.Elem()
	}
	return it.PkgPath()
}

// objectOf reports the identity of the object an instance refers to. Only a
// non-nil pointer to a type of non-zero size, a non-nil map and a non-nil channel
// refer to an object whose identity an address tells. A value is copied each
// time it is passed, so it has none to compare; a slice and a function value
// can share state while their addresses differ or coincide; a zero-size type
// has no storage, and the addresses of its variables may coincide; a nil
// reference refers to nothing.
func objectOf(instance any) (objectKey, bool) {
	v := reflect.ValueOf(instance)
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() || v.Type().Elem().Size() == 0 {
			return objectKey{}, false
		}
	case reflect.Map, reflect.Chan:
		if v.IsNil() {
			return objectKey{}, false
		}
	default:
		return objectKey{}, false
	}
	return objectKey{typ: v.Type(), addr: v.Pointer()}, true
}

// instanceTypeName renders the dynamic type of an instance for a message. A
// pointer is followed to the named type it leads to, which is printed with its
// import path: that package is what claiming by type goes by.
func instanceTypeName(it reflect.Type) string {
	if it.Kind() == reflect.Pointer && it.Name() == "" {
		return "*" + instanceTypeName(it.Elem())
	}
	return typeName(it)
}

// capabilityList renders capabilities for a message: "capability A", or
// "capabilities A, B and C".
func capabilityList(cts []reflect.Type) string {
	names := make([]string, 0, len(cts))
	for _, ct := range cts {
		names = append(names, typeName(ct))
	}
	if len(names) == 1 {
		return "capability " + names[0]
	}
	last := len(names) - 1
	return "capabilities " + strings.Join(names[:last], ", ") + " and " + names[last]
}
