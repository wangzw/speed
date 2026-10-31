package main

import (
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

// envPrefix is the environment variable prefix of this program. Every derived
// variable name starts with it — DEMO_DB__SQLITE__DSN — and so does the one
// that names the config source, DEMO_CONFIG.
const envPrefix = "DEMO"

// hostModuleName is the name the host's own module is registered under.
const hostModuleName = "host"

// The product's own traffic boundaries are the five endpoints api, account,
// admin, ops and web, and the two listeners that carry them — the backend
// listener carries api, account, admin and web, and the ops listener carries
// ops. Both are the platform defaults document compiled into the binary
// (defaults.go): the endpoint table and the bare run's listener addresses are
// values the host names on purpose, and a configuration file a run is pointed
// at replaces any leaf it writes. AGENTS.md explains the layering.

// init registers the host's own module. What a host hands the assembly travels
// through the module mechanism like everything else: the identity is a resource
// on a module the host registers, not an argument of the driver.
func init() { core.ProcessRegistry.Register(hostModule()) }

// hostModule carries the two host inputs that precede all configuration.
//
// HostIdentity is the environment prefix; platformDefaults is the defaults
// document compiled into the binary — this assembly's topology, the development
// values a bare run needs and its local seed. Both belong to the host rather
// than to any capability: the prefix names this program's variables, and the
// document describes this assembly.
//
// DefaultLocator is left empty: a bare run has no configuration file at all,
// and naming a file here would make every command — version included — depend
// on one being installed. A run pointed at a source through --config or
// DEMO_CONFIG layers that file above the document; one pointed at nothing runs
// on the document alone.
func hostModule() core.Module {
	return core.Module{
		Name:      hostModuleName,
		Resources: []any{config.HostIdentity{Prefix: envPrefix}, platformDefaults{}},
	}
}
