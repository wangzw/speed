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
// ops. Both are configuration, and the program names none of them in code: the
// endpoint table is the platform defaults document compiled into the binary
// (server/defaults.yaml), and the listeners are declared by the configuration
// file a run is pointed at, whose samples live under server/testdata. AGENTS.md
// explains both.

// init registers the host's own module. What a host hands the assembly travels
// through the module mechanism like everything else: the identity is a resource
// on a module the host registers, not an argument of the driver.
func init() { core.ProcessRegistry.Register(hostModule()) }

// hostModule carries the two host inputs that precede all configuration.
//
// HostIdentity is the environment prefix; platformDefaults is the topology
// document compiled into the binary. Both belong to the host rather than to any
// capability: the prefix names this program's variables, and the document
// describes this assembly.
//
// DefaultLocator is left empty: this program has no installed config file to
// fall back on, and every command — version included — reads the source it is
// pointed at through --config or DEMO_CONFIG, so a default naming a file that is
// not there would fail commands that need no configuration at all.
func hostModule() core.Module {
	return core.Module{
		Name:      hostModuleName,
		Resources: []any{config.HostIdentity{Prefix: envPrefix}, platformDefaults{}},
	}
}
