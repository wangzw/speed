package core

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// ProcessRegistry is the process-level registry. Registrations made in init
// land here, so the available set is decided by what the binary imports.
var ProcessRegistry = New()

// Registry holds registrations, the instances of the registered modules, and the
// lifecycle driver.
//
// The descriptor part is written before Run and read-only afterwards. The
// driver itself is single-goroutine: stages are strictly ordered and callbacks
// within a stage run one at a time.
type Registry struct {
	mu sync.RWMutex

	order []string                 // registration order
	mods  map[string]*registration // by module name

	// instances holds each module's instance by module name: the product its
	// New constructed, or the instance the host supplied for it. A module with
	// neither has no entry, and a lookup finds nothing for it.
	instances map[string]any
	// supplied records, by module name, the instances the host handed in
	// before the drive. The instance itself is in instances from the moment
	// it is supplied; this keeps what a later conflict has to name, and tells
	// the drive which modules never run Prepare and New.
	supplied map[string]suppliedInstance
	// suppliedObjects maps the identity of each supplied object to the module
	// it was supplied for, which is how one object supplied twice is found.
	suppliedObjects map[objectKey]string
	// lifecycle lists the modules that entered construction, in the order
	// they did. Rollback walks it backwards. A module whose instance the host
	// supplied enters it when construction reaches the module, and one the
	// drive never reached is released as though it had entered last (see
	// constructionOrder).
	lifecycle []string

	// enablement holds the verdict resolution reached, keyed by module name.
	// It is nil until resolution has run.
	enablement map[string]Enablement

	// participating holds the modules this drive took in at run level and the
	// modules the host supplied an instance for, keyed by name. It is settled
	// when the drive begins, ahead of the stances, because the command tree
	// already knows it; nil until then. It is deliberately not the same thing
	// as the key set of enablement, which resolution fills later and which
	// reports a module the drive left out as disabled rather than absent.
	participating map[string]bool
	// declaringOnly holds the modules this drive reached for their
	// declarations alone, keyed by name, with the adjustments the host
	// made to each. A name in both maps is not possible: a participant is
	// a participant whatever else it was named as. Nil until a drive
	// begins.
	declaringOnly map[string]Declaration

	running bool
	// constructing records that this drive has begun constructing. Until it
	// has, no module's New has run, and a lookup that finds no instance is not
	// a matter of construction order.
	constructing bool
}

// registration is a descriptor plus where it was registered from. The other
// party to a name collision may have been imported by a dependency and be
// absent from the host's own code, so the module name alone cannot locate it.
type registration struct {
	module Module
	site   string // package path of the caller of Register
}

// New returns an independent registry. It does not inherit the registrations
// init made on ProcessRegistry: whatever is to be assembled in it has to be
// registered by hand, which is what makes it the way to assemble a small group
// of modules in a test.
func New() *Registry {
	return &Registry{
		mods:            make(map[string]*registration),
		instances:       make(map[string]any),
		supplied:        make(map[string]suppliedInstance),
		suppliedObjects: make(map[objectKey]string),
	}
}

// Register records a module descriptor. Modules call it from their own init.
//
// A duplicate name panics, and so does an illegal token in Requires or
// Provides: both are programming errors, and both surface at the moment of
// registration rather than somewhere downstream. So does a call made once the
// drive has started, from a stage, from the action or after the drive
// returned: descriptors and the resources they carry are fixed before the
// drive, and a query answered during it would otherwise change under the run
// that asked.
//
// Register does not look at the instances already supplied. An instance
// belongs to the module it was assigned to when Supply ran, and a module
// registered afterwards takes no part in that.
func (r *Registry) Register(m Module) {
	site := callerPackage(3)
	if m.Name == "" {
		panic(fmt.Sprintf("core: module registered from %s has an empty name", site))
	}
	for i, req := range m.Requires {
		capabilityType(req.Token, fmt.Sprintf("module %q Requires[%d]", m.Name, i))
	}
	for i, prov := range m.Provides {
		capabilityType(prov.Token, fmt.Sprintf("module %q Provides[%d]", m.Name, i))
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running {
		panic(fmt.Sprintf("core: module %q was registered from %s after the drive started. "+
			"Descriptors are written before the drive and read-only after it, so what "+
			"Modules and Resources answer stays fixed for the whole run; a module that has "+
			"to exist in this run is registered before it begins, and one that depends on "+
			"configuration is a runtime registration with another module's capability, "+
			"made in Init", m.Name, site))
	}
	if existing, ok := r.mods[m.Name]; ok {
		panic(fmt.Sprintf("core: duplicate module name %q: registered from %s and from %s. "+
			"The available set is decided by imports, so one of the two may have been "+
			"pulled in by a dependency rather than by the host itself",
			m.Name, existing.site, site))
	}
	r.mods[m.Name] = &registration{module: m, site: site}
	r.order = append(r.order, m.Name)
}

// origin is what the registry recorded about a module besides its descriptor:
// the package that called Register for it, and whether the host supplied its
// instance. A failure that names a module the host never wrote down can say
// where it came in, which the name alone does not.
type origin struct {
	site     string
	supplied bool
}

// origins maps each registered module to its origin.
func (r *Registry) origins() map[string]origin {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]origin, len(r.mods))
	for name, reg := range r.mods {
		_, supplied := r.supplied[name]
		out[name] = origin{site: reg.site, supplied: supplied}
	}
	return out
}

// Modules gives the full set of descriptors. A capability with a single
// provider can be traced back to its module from here; several providers give
// only the candidate set, and routing by name belongs to the capability
// interface itself. Cross-cutting mechanisms that collect by type use
// Resources instead.
func (r *Registry) Modules() []Module {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Module, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.mods[name].module)
	}
	return out
}

// Lookup returns the descriptor registered under a name. Descriptors do not
// change once registered, so it is usable in any stage.
func (r *Registry) Lookup(name string) (Module, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	reg, ok := r.mods[name]
	if !ok {
		return Module{}, false
	}
	return reg.module, true
}

// callerPackage reports the package path of the frame skip levels above
// runtime.Callers, which is the caller of the exported method that asked.
func callerPackage(skip int) string {
	var pcs [1]uintptr
	if runtime.Callers(skip, pcs[:]) == 0 {
		return "unknown package"
	}
	frame, _ := runtime.CallersFrames(pcs[:]).Next()
	if frame.Function == "" {
		return "unknown package"
	}
	return packageOf(frame.Function)
}

// packageOf extracts the import path of the package from a runtime function
// name, which reads like "pkg/path.Func", "pkg/path.(*T).Method" or
// "pkg/path.Func[...]". The path ends at the first dot after the last slash:
// the linker writes a dot in the last element of the path as %2e, so that dot
// is the separator and none is inside the path.
//
// The path comes back as it is written in an import, with those escapes undone.
// That is the form reflect.Type.PkgPath reports, which a registration site is
// compared with when an instance is claimed by the package of its type, and the
// form a message shows: the same package reads the same wherever it appears.
func packageOf(function string) string {
	dir, rest := "", function
	if i := strings.LastIndex(function, "/"); i >= 0 {
		dir, rest = function[:i+1], function[i+1:]
	}
	if i := strings.Index(rest, "."); i >= 0 {
		rest = rest[:i]
	}
	return unescapeSymbol(dir + rest)
}

// unescapeSymbol undoes the %xx escapes the linker writes into a symbol name
// for a dot in the last element of a package path, and for a space, a control
// byte, a quote, a percent sign and any byte outside ASCII.
func unescapeSymbol(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			if c, err := strconv.ParseUint(s[i+1:i+3], 16, 8); err == nil {
				b.WriteByte(byte(c))
				i += 2
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
