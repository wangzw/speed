package admission

import (
	"fmt"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/tenant"
)

const (
	endpointsItem        = "endpoints"
	ssoExemptTenantsItem = "sso-exempt-tenants"
)

const (
	requireMFAItem = "require-mfa"
	requireSSOItem = "require-sso"
	checkTTLItem   = "check-ttl"

	requireMFAKey = ConfigNamespace + "." + requireMFAItem
	requireSSOKey = ConfigNamespace + "." + requireSSOItem
	checkTTLKey   = ConfigNamespace + "." + checkTTLItem
)

const maxCheckTTL = 60 * time.Second

const allEndpoints = "*"

var configDefaults = moduleConfig{}

type moduleConfig struct {
	// Endpoints names the listening endpoints this module guards, by the
	// entry point's own names. The list is a set of concrete names: the name
	// that stands for every endpoint is not accepted.
	Endpoints []string `config:"endpoints"`
	// SSOExemptTenants names the tenants the SSO requirement does not bind; the
	// platform's own tenant has no identity provider and would otherwise be
	// refused everywhere. The membership rows still bind.
	SSOExemptTenants []string `config:"sso-exempt-tenants"`
}

type options struct {
	RequireMFA bool          `config:"require-mfa"`
	RequireSSO bool          `config:"require-sso"`
	CheckTTL   time.Duration `config:"check-ttl"`
}

var defaults = options{CheckTTL: 5 * time.Second}

func checkTTLInDomain(d time.Duration) bool { return d >= 0 && d <= maxCheckTTL }

type resolved struct {
	endpoints        []string
	ssoExemptTenants []string
}

func (c moduleConfig) resolve() (resolved, error) {
	path := ConfigNamespace + "." + endpointsItem
	if len(c.Endpoints) == 0 {
		return resolved{}, fmt.Errorf("%w: %s is empty. The layer guards the endpoints it "+
			"is told to and none by default; list the endpoints that carry tenant traffic, or "+
			"leave this module out of the command", authn.ErrInvalidConfig, path)
	}
	seen := make(map[string]struct{}, len(c.Endpoints))
	for i, name := range c.Endpoints {
		switch name {
		case "":
			return resolved{}, fmt.Errorf("%w: %s holds an empty name at position %d",
				authn.ErrInvalidConfig, path, i)
		case allEndpoints:
			return resolved{}, fmt.Errorf("%w: %s holds %q at position %d. This layer takes "+
				"concrete endpoint names only: guarding every endpoint would cover the one the "+
				"authentication routes are mounted on, where a request that ends an "+
				"impersonation has no tenant and would be answered 404",
				authn.ErrInvalidConfig, path, allEndpoints, i)
		}
		if _, twice := seen[name]; twice {
			return resolved{}, fmt.Errorf("%s names the endpoint %q twice, which would register "+
				"the admission layer on it twice and run the membership check twice for every "+
				"request it carries: %w", path, name, tenant.ErrDuplicateEndpoint)
		}
		seen[name] = struct{}{}
	}
	for i, name := range c.SSOExemptTenants {
		if name == "" {
			return resolved{}, fmt.Errorf("%w: %s holds an empty name at position %d",
				authn.ErrInvalidConfig, ConfigNamespace+"."+ssoExemptTenantsItem, i)
		}
	}
	return resolved{
		endpoints:        c.Endpoints,
		ssoExemptTenants: c.SSOExemptTenants,
	}, nil
}

func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			endpointsItem: {
				Origins:  config.OriginPrimary,
				Required: true,
				Description: "the listening endpoints whose requests are checked for membership " +
					"of the tenant they resolved to, by the names the entry point's own " +
					"configuration gives them; concrete names only, never *",
			},
			ssoExemptTenantsItem: {
				Origins: config.OriginPrimary,
				Description: "the tenants the SSO requirement does not bind; their members are " +
					"still checked for membership, but they are not held to signing in through " +
					"one of the tenant's own identity providers",
			},
		},
	}
}

func settingsSchema() settings.Schema {
	return settings.Schema{
		Namespace:    ConfigNamespace,
		TenantFacing: true,
		Mounts:       []settings.Mount{{Value: &defaults}},
		Items: map[string]settings.Item{
			requireMFAItem: {
				Description: "whether a tenant's members must have authenticated with more " +
					"than one kind of factor to make a request in that tenant; per tenant",
				TitleKey:       ConfigNamespace + "." + requireMFAItem + ".title",
				DescriptionKey: ConfigNamespace + "." + requireMFAItem + ".description",
				Control:        settings.ControlSwitch,
				Danger:         settings.DangerCritical,
				Effect:         settings.EffectNextSignIn,
				TenantAccess:   settings.TenantReadWrite,
			},
			requireSSOItem: {
				Description: "whether a tenant's members must have signed in through one of " +
					"that tenant's own identity providers to make a request in that tenant; " +
					"per tenant",
				TitleKey:       ConfigNamespace + "." + requireSSOItem + ".title",
				DescriptionKey: ConfigNamespace + "." + requireSSOItem + ".description",
				Control:        settings.ControlSwitch,
				Danger:         settings.DangerCritical,
				Effect:         settings.EffectNextSignIn,
				TenantAccess:   settings.TenantReadWrite,
			},
			checkTTLItem: {
				Description: "how long a membership and a permission read from the account " +
					"module stay in the process, from 0, which switches the memory off, to " +
					maxCheckTTL.String() + "; global",
				Domain: settings.DomainOf("durations from 0 to "+maxCheckTTL.String(), checkTTLInDomain),
			},
		},
	}
}

func readConfig(reader config.Reader) (resolved, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return resolved{}, err
	}
	set, err := cfg.resolve()
	if err != nil {
		return resolved{}, err
	}
	if err := checkResolutionCoverage(reader, set.endpoints); err != nil {
		return resolved{}, err
	}
	return set, nil
}

const (
	resolutionNamespace     = "tenant.resolve"
	resolutionEndpointsItem = "endpoints"
)

func checkResolutionCoverage(reader config.Reader, endpoints []string) error {
	var shape struct {
		Endpoints []string `config:"endpoints"`
	}
	if err := reader.Decode(resolutionNamespace, &shape); err != nil {
		return nil
	}
	if len(shape.Endpoints) == 0 {
		return nil
	}
	resolved := make(map[string]struct{}, len(shape.Endpoints))
	for _, name := range shape.Endpoints {
		resolved[name] = struct{}{}
	}
	for _, name := range endpoints {
		if _, covered := resolved[name]; covered {
			continue
		}
		return fmt.Errorf("%w: %s.%s lists the endpoint %q, which %s.%s does not resolve a tenant "+
			"on. A request on that endpoint carries no tenant context, and this layer admits a "+
			"non-impersonating request without one, so the membership check would never run there. "+
			"Add the endpoint to %s.%s, or remove it from %s.%s",
			authn.ErrInvalidConfig, ConfigNamespace, endpointsItem, name,
			resolutionNamespace, resolutionEndpointsItem,
			resolutionNamespace, resolutionEndpointsItem, ConfigNamespace, endpointsItem)
	}
	return nil
}

func prepare(reader config.Reader) (core.Enablement, error) {
	if _, err := readConfig(reader); err != nil {
		return core.Enablement{}, err
	}
	return core.Enablement{State: core.StateEnabled}, nil
}
