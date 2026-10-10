package core

import "errors"

// The sentinel errors the lifecycle driver returns. Callers tell the classes
// apart with errors.Is; the error text carries what is needed to locate the
// cause and states the fixing action.
var (
	// ErrMissingProvider reports that a required capability has no provider
	// that can be taken up.
	ErrMissingProvider = errors.New("core: capability has no available provider")
	// ErrAmbiguousProvider reports that a single-valued lookup found more
	// than one provider with an instance.
	ErrAmbiguousProvider = errors.New("core: capability has more than one provider with an instance")
	// ErrExclusiveViolated reports that a capability kept more than one
	// enabled provider through resolution while at least one of them claims
	// exclusivity.
	ErrExclusiveViolated = errors.New("core: exclusive capability has more than one enabled provider")
	// ErrDisabledStance reports that a module stated StateDisabled in
	// Prepare. Only resolution disables a module, and only by giving that
	// verdict to a provider that stands down over an exclusive claim: a
	// module taken in at run level has to start, so one that cannot start
	// returns an error naming what is absent instead of standing itself
	// down.
	ErrDisabledStance = errors.New("core: a module stated StateDisabled in Prepare, which " +
		"is not a stance a module may take")
	// ErrDependencyCycle reports a cycle in the graph the Requires
	// declarations form.
	ErrDependencyCycle = errors.New("core: module dependency graph has a cycle")
	// ErrUndeliveredCapability reports a module whose instance does not
	// implement a capability it declared in Provides, which includes a module
	// with no New callback that the host supplied no instance for.
	ErrUndeliveredCapability = errors.New("core: module did not deliver a declared capability")
	// ErrUnknownParticipant reports a name a drive was assembled from --
	// in its participating set, its declaring set, or the closure either
	// one expands to -- that no module registered under. The text carries
	// the guidance because the two ways it happens look alike from a host:
	// the package that registers the module was not imported, or the name
	// is not the one it registers under. Writing a module-name constant
	// without importing its package compiles, so the mismatch has to come
	// out as a diagnosable startup failure rather than as a command that
	// runs one module short with nobody the wiser.
	ErrUnknownParticipant = errors.New("core: no module registered under this name. The " +
		"available set is decided by imports, so either the package that registers the " +
		"module is not imported by the host, or the name is not the one it registers under; " +
		"the registering package exports that name as a constant")
)
