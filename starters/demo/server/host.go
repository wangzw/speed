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
// ops. Both the endpoints and the listeners are configuration, declared under
// server/testdata and explained in AGENTS.md; the program names none of them in
// code.

// init registers the host's own module. What a host hands the assembly travels
// through the module mechanism like everything else: the identity is a resource
// on a module the host registers, not an argument of the driver.
func init() { core.ProcessRegistry.Register(hostModule()) }

// hostModule carries the one input that precedes all configuration.
//
// DefaultLocator is left empty: this program has no installed config file to
// fall back on, and every command — version included — reads the source it is
// pointed at through --config or DEMO_CONFIG, so a default naming a file that is
// not there would fail commands that need no configuration at all.
func hostModule() core.Module {
	return core.Module{
		Name:      hostModuleName,
		Resources: []any{config.HostIdentity{Prefix: envPrefix}},
	}
}
