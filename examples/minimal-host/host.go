package main

import (
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

// envPrefix is the environment variable prefix of this host. Every derived
// variable name starts with it, and so does the one that names the config
// source, MINIHOST_CONFIG.
const envPrefix = "MINIHOST"

// hostModuleName is the name the host's own module is registered under.
const hostModuleName = "host"

// init registers the host's own module. What a host hands the assembly travels
// through the module mechanism like everything else: the identity is a
// resource on a module the host registers, not an argument of the driver, so
// the driver's signature does not grow as hosts declare more.
func init() { core.ProcessRegistry.Register(hostModule()) }

// hostModule carries the two inputs that precede all configuration and can
// therefore come from nowhere else.
//
// No command names this module, and none has to. Resources are collected from
// every registered module before the first command runs, whichever command the
// run selects, which is what the identity has to be available for. Naming the
// module on the root would buy nothing and would put a name in every command's
// participating set — enough to stop the one command that assembles nothing
// from assembling nothing. serve has it in its set all the same, since taking
// in every available module takes in the ones that carry resources alone, and
// it costs that command nothing: this module contributes no argument and has
// nothing to construct.
//
// DefaultLocator is left empty on purpose: this host has no installed config
// file to fall back on, and a run with no primary config source is a legal
// one. The locator then comes from MINIHOST_CONFIG or --config, or from
// nowhere, and the defaults, the environment and the command line carry the
// run on their own.
func hostModule() core.Module {
	return core.Module{
		Name:      hostModuleName,
		Resources: []any{config.HostIdentity{Prefix: envPrefix}},
	}
}
