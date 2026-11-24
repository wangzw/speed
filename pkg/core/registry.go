package core

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// ProcessRegistry is the process-level registry.
// Registrations made in init land here, so the available set is decided by what the binary
// imports.
var ProcessRegistry = New()

// Registry holds registrations, the instances of the registered modules, and the lifecycle driver.
// The descriptor part is written before Run and read-only afterwards.
type Registry struct {
	mu sync.RWMutex

	order []string
	mods  map[string]*registration

	instances       map[string]any
	supplied        map[string]suppliedInstance
	suppliedObjects map[objectKey]string
	lifecycle       []string

	enablement map[string]Enablement

	participating map[string]bool
	declaringOnly map[string]Declaration

	running      bool
	constructing bool
}

type registration struct {
	module Module
	site   string
}

// New returns an independent registry.
func New() *Registry {
	return &Registry{
		mods:            make(map[string]*registration),
		instances:       make(map[string]any),
		supplied:        make(map[string]suppliedInstance),
		suppliedObjects: make(map[objectKey]string),
	}
}

// Register records a module descriptor.
// Modules call it from their own init.
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

type origin struct {
	site     string
	supplied bool
}

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

// Modules gives the full set of descriptors.
func (r *Registry) Modules() []Module {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Module, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.mods[name].module)
	}
	return out
}

// Lookup returns the descriptor registered under a name.
// Descriptors do not change once registered, so it is usable in any stage.
func (r *Registry) Lookup(name string) (Module, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	reg, ok := r.mods[name]
	if !ok {
		return Module{}, false
	}
	return reg.module, true
}

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
