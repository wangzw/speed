package db

import (
	"errors"
	"fmt"
	"reflect"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/core"
)

// pluginInstall is one declared plugin together with the module that declared
// it, which is the pair every failure text below has to name: the module is who
// has to change the declaration, and this package cannot know how.
type pluginInstall struct {
	module string
	plugin gorm.Plugin
}

// collectPlugins takes the declared plugins out of the registry, in the order
// they are installed.
//
// A declaration from a module that does not run is dropped: a plugin acts on
// the statements this run issues, so what decides it is whether the module
// belongs to this run, and a module that is not part of the run has nothing in
// force here. Three things leave a module out -- resolution stood it down over
// an exclusive claim, this command never took it in, or it was taken in for its
// declarations alone. Stating that it does not run is not one of them: a module
// taken in at run level has to start, so a stance of StateDisabled is refused
// rather than honoured.
//
// This is where it parts company with the migration collection beside it,
// which reads the declaring set and drops only a module that ran and was
// disabled. The split is deliberate and it follows from what each describes:
// a plugin's lifetime is this run, and the database structure's is the binary.
// Neither filter can be derived from the other, so a change to one is not a
// reason to touch the other.
//
// The price is real and it is what "not enabled" means rather than a defect — a
// module whose whole work is declaring a plugin, the shape of a cross-cutting
// capability that needs nothing back from the database, takes that capability
// away from every dependant when it is disabled, and the statements those
// dependants go on to issue run unfiltered and unaudited with nothing said. An
// assembly that disabled the module did not ask for its plugin, and installing
// it anyway is the other reading, the one where a declaration of a module that
// never ran is in force.
//
// The order is the dependency order of the declaring modules, and declaration
// order inside one module. Install order is callback order in GORM: plugins
// registered to the same processor run in the order they were installed, so two
// modules that both act on a statement need their dependency to decide which
// acts first, and the two are ordered that way. Modules at the same place have
// no dependency between them; they fall back to module name so that one build
// installs them as the next build does.
func collectPlugins(reg *core.Registry) ([]pluginInstall, error) {
	rank := dependencyRank(reg)

	byModule := make(map[string][]Plugin)
	var modules []string
	for _, declared := range core.Resources[Plugin](reg) {
		// A host may put a module's declarations in force regardless of
		// whether it runs, and this is where that override lands. It is
		// not the default and it is not a second reading of enablement:
		// it is the host saying it wants this module's declarations on a
		// module that is not part of the run.
		if !reg.AllResourcesInForce(declared.Module) {
			if state, known := reg.Enablement(declared.Module); known && state.State == core.StateDisabled {
				continue
			}
		}
		if absent(declared.Value.Plugin) {
			// Nothing to install, and installing it anyway would
			// panic inside GORM on the first call rather than report
			// anything: a declaration of nil is a defect in the
			// declaring module, and it is named here.
			return nil, fmt.Errorf("%w: module %q declares a plugin that carries nothing to "+
				"install, and a plugin of nil cannot be installed on a handle",
				ErrPluginFailed, declared.Module)
		}
		if _, seen := byModule[declared.Module]; !seen {
			modules = append(modules, declared.Module)
		}
		byModule[declared.Module] = append(byModule[declared.Module], declared.Value)
	}
	inDependencyOrder(rank, modules)

	var plugins []pluginInstall
	for _, module := range modules {
		for _, declared := range byModule[module] {
			plugins = append(plugins, pluginInstall{module: module, plugin: declared.Plugin})
		}
	}
	return plugins, nil
}

// installPlugins installs every declared plugin on one handle, in order.
//
// It runs inside the assembly and before the handle is handed to anyone, so a
// dependant that resolves the capability can never see a handle missing a
// plugin: there is no moment at which one exists and the other does not. The
// same call serves a handle built with Open, which is why it takes a handle
// rather than a product — a second connection carries the same declared plugins
// as the delivered one, and a caller that moved to another database would
// otherwise lose tenant filtering or auditing silently.
//
// Carrying the plugins is not all a plugin needs there. One that writes rows of
// its own finds a database the module applied no migration to, so its table is
// usually missing; what it has to go on is OpenedFrom, which Open makes answer
// on that handle before this runs.
//
// A plugin that will not install fails the startup under ErrPluginFailed. This
// module cannot judge the plugin's content, so all it reports is which
// declaration failed and what the plugin itself said.
func installPlugins(session *gorm.DB, plugins []pluginInstall) error {
	declaredBy := make(map[string]string, len(plugins))
	for _, install := range plugins {
		name := install.plugin.Name()
		previous, taken := declaredBy[name]
		if err := session.Use(install.plugin); err != nil {
			// GORM reports a name that is already taken as
			// ErrRegistered and does not say whose it was, so which
			// module holds it is read from what this run already
			// installed. Both are named: a handle carries one plugin
			// per name, so one of the two declarations is redundant
			// and the operator picks which.
			if taken && errors.Is(err, gorm.ErrRegistered) {
				return fmt.Errorf("%w: modules %q and %q both declare a plugin named %q, and a "+
					"handle installs one plugin per name: %w",
					ErrPluginFailed, previous, install.module, name, err)
			}
			return fmt.Errorf("%w: installing the plugin %q declared by module %q failed: %w",
				ErrPluginFailed, name, install.module, err)
		}
		declaredBy[name] = install.module
	}
	return nil
}

// OpenedFrom reports the handle this capability delivered, for a session on a
// connection built from it with Open. The second result is false for a session
// on the delivered handle itself and for one on a handle this module did not
// build with Open.
//
// It is how a plugin tells the two kinds of connection apart. The plugin
// instances are shared by every handle built from one declaration — and by
// every registry in the process — so an instance cannot remember which handle it
// was installed on, and GORM hands a callback a session with nothing on it
// saying where it came from. A plugin that keeps a table of its own needs that
// answer: the declared migrations are applied to the database the configuration
// names and to no other, so a second connection usually reaches a database
// without that table.
//
// The answer holds for every session derived from the connection — a
// WithContext, a Session, a transaction — because it travels with the
// connection's configuration, which each of them shares, rather than with a
// context a caller replaces. It is in place before any declared plugin is
// installed, so a plugin's Initialize can ask it too.
func OpenedFrom(session *gorm.DB) (*gorm.DB, bool) {
	if session == nil || session.Config == nil {
		return nil, false
	}
	marker, ok := session.Plugins[openedMarkerName].(*openedMarker)
	if !ok {
		return nil, false
	}
	return marker.delivered, true
}

// openedMarkerName is the name the marker is installed under in a handle's
// plugin table. It is this package's own path with a suffix no declared plugin
// takes, so a declaration cannot collide with it by following the convention.
const openedMarkerName = "github.com/vislake/speed/pkg/db#opened-from"

// openedMarker is what Open installs on the connection it builds, ahead of the
// declared plugins: the delivered handle the connection was opened from.
//
// It sits in the plugin table because that table is the one piece of a handle
// every derived session shares by reference. A context value would be gone at
// the first WithContext, and a statement setting at the first new statement.
type openedMarker struct {
	delivered *gorm.DB
}

// Name is the marker's key in the plugin table.
func (*openedMarker) Name() string { return openedMarkerName }

// Initialize registers nothing: the marker is read, not run.
func (*openedMarker) Initialize(*gorm.DB) error { return nil }

// markOpened installs the marker on a connection Open built.
//
// The connection is fresh, so its plugin table is empty and the install has
// nothing to collide with; a failure here would be GORM's own, and it is
// reported under the sentinel the installation that follows reports under.
func markOpened(opened, delivered *gorm.DB) error {
	if err := opened.Use(&openedMarker{delivered: delivered}); err != nil {
		return fmt.Errorf("%w: marking a connection built with Open with the handle it was opened "+
			"from failed: %w", ErrPluginFailed, err)
	}
	return nil
}

// absent reports whether a declared plugin carries nothing to install: the nil
// interface, or a typed nil pointer inside it. The second is not the first, so
// a plain nil test would pass it through to a call that panics.
func absent(plugin gorm.Plugin) bool {
	if plugin == nil {
		return true
	}
	v := reflect.ValueOf(plugin)
	return v.Kind() == reflect.Pointer && v.IsNil()
}
