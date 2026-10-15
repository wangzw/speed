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

// The five listening endpoints. This is the product's own choice of traffic
// boundary, and what this file says about them is which class of traffic
// belongs where: the CRM-style API is scoped to a tenant, the account endpoint
// serves a caller who has not chosen one, the admin endpoint serves the
// platform's operators, the operations endpoint serves probes and documents,
// and the web endpoint carries the frontend bundle. Which guards each one
// carries is configuration, and a skeleton that has shipped no business module
// yet mounts no guarded route on any of them.
const (
	apiEndpoint     = "api"
	accountEndpoint = "account"
	adminEndpoint   = "admin"
	opsEndpoint     = "ops"
	webEndpoint     = "web"
)

// servedEndpoints lists the endpoints in the order the readiness probe reads
// them, and the order the assembly binds them in.
var servedEndpoints = []string{apiEndpoint, accountEndpoint, adminEndpoint, opsEndpoint, webEndpoint}

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
