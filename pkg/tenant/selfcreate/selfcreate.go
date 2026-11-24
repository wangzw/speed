// Package selfcreate mounts POST /api/v1/orgs on the account endpoint, which lets an
// eligible account open a business organization through tenant.Provisioner.
package selfcreate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/feature"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/tenant"
)

// ModuleName is this module's registry name, org.create rather than the package path, so
// that it matches the feature's first segment.
const ModuleName = "org.create"

// FeatureName is the feature in force while the org.create.enabled switch is on.
const FeatureName = featureCompatibilityName

const featureCompatibilityName = "org.create"

// ConfigNamespace is the configuration namespace of the org.create.enabled switch.
const ConfigNamespace = "org.create"

// SettingsNamespace is the namespace of the two platform-level numeric limits.
const SettingsNamespace = "org.create"

const enabledItem = "enabled"

const (
	maxOrganizationsItem = "max-organizations"
	maxPerHourItem       = "max-per-hour"
)

const (
	maxOrganizationsPath = SettingsNamespace + "." + maxOrganizationsItem
	maxPerHourPath       = SettingsNamespace + "." + maxPerHourItem
)

const (
	maxOrganizationsStart = 5
	maxPerHourStart       = 3
)

const endpointName = "account"

func init() {
	core.ProcessRegistry.Register(Module())
}

// Module is the descriptor a host registers. It requires the roster, memberships, signup
// verification, username reservation, provisioner, key-value store, settings store and
// audit, and takes the HTTP entry point, feature board, gateway and logger optionally.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*tenant.Catalog)(nil)},
			{Token: (*authn.Memberships)(nil)},
			{Token: (*authn.SignupVerification)(nil)},
			{Token: (*authn.UsernameReservation)(nil)},
			{Token: (*tenant.Provisioner)(nil)},
			{Token: (*kv.KV)(nil)},
			{Token: (*settings.Store)(nil)},
			{Token: (*audit.Audit)(nil)},
			{Token: (*speedhttp.Router)(nil), Optional: true},
			{Token: (*feature.Board)(nil), Optional: true},
			{Token: (*authn.Gate)(nil), Optional: true},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Resources: []any{
			configSchema(),
			settingsSchema(),
			spec,
			operations,
			feature.Spec{Name: FeatureName, Conditional: true},
		},

		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			cfg, err := readConfig(reg)
			if err != nil {
				return core.Enablement{}, err
			}
			if !cfg.Enabled {
				return core.Enablement{
					State:  core.StateDisabled,
					Reason: ModuleName + ": " + ConfigNamespace + "." + enabledItem + " is off",
				}, nil
			}
			return core.Enablement{State: core.StateEnabled}, nil
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			return newProduct(reg)
		},

		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			product, ok := productOf(instance)
			if !ok {
				return nil
			}
			return product.wire(reg)
		},
	}
}

type moduleConfig struct {
	// Enabled is the switch: when false the route is not mounted and the feature is not in force.
	Enabled bool `config:"enabled"`
}

func configSchema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &moduleConfig{}}},
		Items: map[string]config.Item{
			enabledItem: {
				Origins: config.OriginPrimary | config.OriginEnv,
				Description: "whether an account may create a business organization of its own; off, " +
					"POST /api/v1/orgs is not mounted and the feature org.create is absent",
			},
		},
	}
}

type limitDefaults struct {
	MaxOrganizations int
	MaxPerHour       int
}

// Leave TenantFacing, TenantAccess, Control, Danger and Effect at their zero values: they
// keep both limits platform-only.
func settingsSchema() settings.Schema {
	return settings.Schema{
		Namespace: SettingsNamespace,
		Mounts: []settings.Mount{{Value: &limitDefaults{
			MaxOrganizations: maxOrganizationsStart,
			MaxPerHour:       maxPerHourStart,
		}}},
		Items: map[string]settings.Item{
			maxOrganizationsItem: {
				Description: "how many active business organizations one account may hold before a " +
					"creation is refused; a personal tenant and a disabled tenant are not counted",
				Domain: settings.DomainOf("the positive integers", func(limit int) bool { return limit > 0 }),
			},
			maxPerHourItem: {
				Description: "how many organizations one account may create in one hour, a count " +
					"consumed by every request that passes all preliminary checks and reaches the " +
					"provisioner, whether the provisioner succeeds or not",
				Domain: settings.DomainOf("the positive integers", func(limit int) bool { return limit > 0 }),
			},
		},
	}
}

func readConfig(reg *core.Registry) (moduleConfig, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return moduleConfig{}, fmt.Errorf("%s: taking up the configuration reader: %w", ModuleName, err)
	}
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return moduleConfig{}, err
	}
	return cfg, nil
}

type product struct {
	config        moduleConfig
	roster        tenant.Catalog
	memberships   authn.Memberships
	verification  authn.SignupVerification
	reservations  authn.UsernameReservation
	settings      settings.Store
	kv            kv.KV
	provisioner   tenant.Provisioner
	audit         audit.Audit
	logger        *slog.Logger
	now           func() time.Time
	newIdentifier func() string
}

func productOf(instance any) (*product, bool) {
	product, ok := instance.(*product)
	return product, ok && product != nil
}

func newProduct(reg *core.Registry) (*product, error) {
	cfg, err := readConfig(reg)
	if err != nil {
		return nil, err
	}
	roster, err := core.Resolve[tenant.Catalog](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the tenant roster: %w", ModuleName, err)
	}
	memberships, err := core.Resolve[authn.Memberships](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the memberships: %w", ModuleName, err)
	}
	verification, err := core.Resolve[authn.SignupVerification](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the signup verification read: %w", ModuleName, err)
	}
	reservations, err := core.Resolve[authn.UsernameReservation](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the reserved-username answer: %w", ModuleName, err)
	}
	provisioner, err := core.Resolve[tenant.Provisioner](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the tenant provisioner: %w", ModuleName, err)
	}
	counters, err := core.Resolve[kv.KV](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the key-value store: %w", ModuleName, err)
	}
	store, err := core.Resolve[settings.Store](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the settings store: %w", ModuleName, err)
	}
	auditor, err := core.Resolve[audit.Audit](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the audit capability: %w", ModuleName, err)
	}
	product := &product{
		config:        cfg,
		roster:        roster,
		memberships:   memberships,
		verification:  verification,
		reservations:  reservations,
		settings:      store,
		kv:            counters,
		provisioner:   provisioner,
		audit:         auditor,
		logger:        log.Default(),
		now:           time.Now,
		newIdentifier: newIdentifier,
	}
	if delivered, found, takeErr := optional[log.Logger](reg, "the logger"); takeErr != nil {
		return nil, takeErr
	} else if found {
		product.logger = delivered.Named(ModuleName)
	}
	return product, nil
}

func optional[T any](reg *core.Registry, what string) (T, bool, error) {
	var zero T
	value, err := core.Resolve[T](reg)
	switch {
	case err == nil:
		return value, true, nil
	case errors.Is(err, core.ErrMissingProvider):
		return zero, false, nil
	default:
		return zero, false, fmt.Errorf("%s: taking up %s: %w", ModuleName, what, err)
	}
}

func (p *product) wire(reg *core.Registry) error {
	if !p.config.Enabled {
		p.logger.Info(ModuleName + ": the self-service creation is off; the route is not mounted")
		return nil
	}
	router, found, err := optional[speedhttp.Router](reg, "the HTTP entry point")
	if err != nil {
		return err
	}
	if !found {
		p.logger.Info(ModuleName + ": the self-service creation is on but the run has no HTTP entry point; nothing is mounted")
		return nil
	}
	endpoint, err := router.Endpoint(endpointName)
	if err != nil {
		return fmt.Errorf("%s: %w: %q: %w", ModuleName, speedhttp.ErrUnknownEndpoint, endpointName, err)
	}
	endpoint.Route(createOperation.Method, RoutePath, createOperation.Handle(p.handleCreate))
	// Registered for impersonating identities so the handler answers 403 itself instead of the
	// gateway's 401.
	if gate, gateFound, gateErr := optional[authn.Gate](reg, "the authentication gateway"); gateErr != nil {
		return gateErr
	} else if gateFound {
		gate.Impersonable(endpointName, http.MethodPost, RoutePath)
	}
	if board, boardFound, boardErr := optional[feature.Board](reg, "the feature board"); boardErr != nil {
		return boardErr
	} else if boardFound {
		board.Post(ModuleName, FeatureName)
	}
	p.logger.Info(ModuleName+": the self-service creation is on",
		"endpoint", endpointName, "route", RoutePath)
	return nil
}
