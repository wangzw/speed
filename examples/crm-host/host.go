package main

import (
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

// envPrefix is the environment variable prefix of this host. Every derived
// variable name starts with it — CRM_DB__POSTGRES__DSN — and so does the one
// that names the config source, CRM_CONFIG.
const envPrefix = "CRM"

// hostModuleName is the name the host's own module is registered under.
const hostModuleName = "host"

// The four listening endpoints. Which guards each one carries is configuration,
// and what this host says about them is which traffic belongs where: the CRM API
// is scoped to a tenant, the account endpoint serves a user who has not chosen
// one, the admin endpoint serves the platform's operators, and the operations
// endpoint serves probes and documents with no guard at all.
const (
	apiEndpoint     = "api"
	accountEndpoint = "account"
	adminEndpoint   = "admin"
	opsEndpoint     = "ops"
)

// servedEndpoints lists the endpoints in the order the readiness check and the
// diagnostics read them.
var servedEndpoints = []string{apiEndpoint, accountEndpoint, adminEndpoint, opsEndpoint}

// init registers the host's own module. What a host hands the assembly travels
// through the module mechanism like everything else: the identity is a
// resource on a module the host registers, not an argument of the driver.
func init() { core.ProcessRegistry.Register(hostModule()) }

// hostModule carries the one input that precedes all configuration.
//
// DefaultLocator is left empty: this host has no installed config file to fall
// back on, and every command — version included — reads the source it is
// pointed at, so a default naming a file that is not there would fail commands
// that need no configuration at all.
func hostModule() core.Module {
	return core.Module{
		Name:      hostModuleName,
		Resources: []any{config.HostIdentity{Prefix: envPrefix}},
	}
}
