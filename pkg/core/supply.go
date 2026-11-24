package core

import (
	"fmt"
	"reflect"
	"strings"
)

type suppliedInstance struct {
	site string
}

type objectKey struct {
	typ  reflect.Type
	addr uintptr
}

// Supply hands the registry an instance the host built itself, as the instance of one registered
// module.
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

func implements(it, ct reflect.Type) bool {
	return it != nil && it.AssignableTo(ct)
}

func missingCapabilities(it reflect.Type, declared []reflect.Type) []reflect.Type {
	var missing []reflect.Type
	for _, ct := range declared {
		if !implements(it, ct) {
			missing = append(missing, ct)
		}
	}
	return missing
}

func typePackage(it reflect.Type) string {
	for it.Kind() == reflect.Pointer && it.Name() == "" {
		it = it.Elem()
	}
	return it.PkgPath()
}

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

func instanceTypeName(it reflect.Type) string {
	if it.Kind() == reflect.Pointer && it.Name() == "" {
		return "*" + instanceTypeName(it.Elem())
	}
	return typeName(it)
}

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
