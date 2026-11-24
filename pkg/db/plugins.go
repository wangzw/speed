package db

import (
	"errors"
	"fmt"
	"reflect"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/core"
)

type pluginInstall struct {
	module string
	plugin gorm.Plugin
}

// collectPlugins takes the declared plugins out of the registry, in the order
// they are installed: the declaring modules' dependency order, declaration
// order within one module. Install order is callback order in GORM.
//
// A declaration from a module that does not run is dropped. This differs from
// the migration collection beside it, which reads the declaring set: a plugin's
// lifetime is this run, the database structure's is the binary. Neither filter
// can be derived from the other.
func collectPlugins(reg *core.Registry) ([]pluginInstall, error) {
	rank := dependencyRank(reg)

	byModule := make(map[string][]Plugin)
	var modules []string
	for _, declared := range core.Resources[Plugin](reg) {
		if !reg.AllResourcesInForce(declared.Module) {
			if state, known := reg.Enablement(declared.Module); known && state.State == core.StateDisabled {
				continue
			}
		}
		if absent(declared.Value.Plugin) {
			// Installing a nil plugin would panic inside GORM, so the
			// declaring module is named here instead.
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

// installPlugins installs every declared plugin on one handle, in order, before
// the handle is handed to anyone. The same call serves a handle built with Open,
// which carries the same declared plugins as the delivered one. A plugin that
// will not install fails the startup under ErrPluginFailed.
func installPlugins(session *gorm.DB, plugins []pluginInstall) error {
	declaredBy := make(map[string]string, len(plugins))
	for _, install := range plugins {
		name := install.plugin.Name()
		previous, taken := declaredBy[name]
		if err := session.Use(install.plugin); err != nil {
			// GORM reports a taken name as ErrRegistered without saying
			// whose it was, so both declaring modules are named.
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
// The answer travels with the connection's configuration, so it holds for every
// session derived from it, and it is in place before any declared plugin is
// installed.
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

const openedMarkerName = "github.com/vislake/speed/pkg/db#opened-from"

// openedMarker is the delivered handle a connection built by Open came from. It
// sits in the plugin table because that is the one piece of a handle every
// derived session shares by reference.
type openedMarker struct {
	delivered *gorm.DB
}

func (*openedMarker) Name() string { return openedMarkerName }

func (*openedMarker) Initialize(*gorm.DB) error { return nil }

func markOpened(opened, delivered *gorm.DB) error {
	if err := opened.Use(&openedMarker{delivered: delivered}); err != nil {
		return fmt.Errorf("%w: marking a connection built with Open with the handle it was opened "+
			"from failed: %w", ErrPluginFailed, err)
	}
	return nil
}

// absent reports whether a declared plugin carries nothing to install. A typed
// nil pointer is not the nil interface, so a plain nil test would pass it
// through to a call that panics.
func absent(plugin gorm.Plugin) bool {
	if plugin == nil {
		return true
	}
	v := reflect.ValueOf(plugin)
	return v.Kind() == reflect.Pointer && v.IsNil()
}
