package main

import (
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

// envPrefix is the environment variable prefix of this host. Every derived
// variable name starts with it, and so does the one that names the config
// source, AUTHHOST_CONFIG.
const envPrefix = "AUTHHOST"

// hostModuleName is the name the host's own module is registered under.
const hostModuleName = "host"

// init registers the host's own module. What a host hands the assembly travels
// through the module mechanism like everything else: the identity is a
// resource on a module the host registers, not an argument of the driver.
func init() { core.ProcessRegistry.Register(hostModule()) }

// hostModule carries the two inputs that precede all configuration.
//
// DefaultLocator is left empty: this host has no installed config file to fall
// back on. A run with no primary config source is legal and reaches the
// gateway's stand-down path, since the endpoints to guard can come from
// nowhere else.
func hostModule() core.Module {
	return core.Module{
		Name:      hostModuleName,
		Resources: []any{config.HostIdentity{Prefix: envPrefix}},
	}
}
