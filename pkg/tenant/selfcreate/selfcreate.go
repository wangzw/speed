// Package selfcreate is the tenancy capability's self-service organization
// module: an eligible account opens a business organization of its own.
//
// The route is POST /api/v1/orgs on the account endpoint. That endpoint resolves
// no tenant and is where a caller who has not chosen one yet stands, so the
// creation is decided by the caller's own account and nothing else. The tenant
// itself is opened through tenant.Provisioner, the same seam the platform
// management surface uses: the creator becomes the tenant-admin, the role is
// marked builtin and carries the governed permission set, and the membership's
// source is the creator's.
//
// The eligibility rules and upper bounds are the product's: an unproved email
// address is refused, five active business organizations per account, three
// creations an hour per account, a thirty-second creation lock so one account's
// concurrent requests do not race each other, and a mandatory audit row. The two
// numeric limits are runtime settings (org.create.max-organizations and
// org.create.max-per-hour); the switch and the bounds' shape are configuration.
//
// The route is an exact route on the account endpoint, whose /api/v1/orgs
// claim is configuration and stays claimed either way: with this module's
// switch off the path reaches the endpoint's own 404. The personal tenant is
// written in the account's transaction and has no route of its own.
//
// The module owns no table. The roster row, the tenant-admin role and its
// grants, the creator's membership and binding, and the audit rows belong to
// the modules that declare them.
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

// ModuleName is this module's name in the registry. A host writes it into the
// command that assembles self-service creation. It is org.create, the name the
// host module used before the logic moved here: the feature document requires a
// feature's first segment to equal the declaring module's, and the feature name
// org.create is the one a frontend gates entry points on, so the registry name
// keeps that segment rather than the package path's tenant.
const ModuleName = "org.create"

// FeatureName is the feature this module serves while its configuration switch
// is on. It is kept as org.create, the name the host module declared before the
// logic moved here, so a frontend gating entry points on it keeps working.
const FeatureName = featureCompatibilityName

// featureCompatibilityName is the feature's name. It is spelled apart from
// ModuleName only so the compatibility rule is documented in one place; the two
// are the same string today and the feature document requires the segment to
// match.
const featureCompatibilityName = "org.create"

// ConfigNamespace is the section the switch is read from. It is the product's
// name for the capability rather than the module's, and it stays org.create for
// the same compatibility reason.
const ConfigNamespace = "org.create"

// SettingsNamespace is the namespace of the two numeric limits. They are
// platform settings: the schema does not declare itself tenant-facing, and no
// item carries a tenant access.
const SettingsNamespace = "org.create"

// The configuration item this module accepts, relative to its namespace.
const enabledItem = "enabled"

// The runtime-setting items the module declares. The keys are relative to the
// settings namespace.
const (
	maxOrganizationsItem = "max-organizations"
	maxPerHourItem       = "max-per-hour"
)

// The full paths of the two runtime settings, as the settings store names them.
const (
	maxOrganizationsPath = SettingsNamespace + "." + maxOrganizationsItem
	maxPerHourPath       = SettingsNamespace + "." + maxPerHourItem
)

// The declared defaults of the two limits.
const (
	maxOrganizationsStart = 5
	maxPerHourStart       = 3
)

// The listening endpoint the route is mounted on. It is fixed: the route is
// deliberately on the endpoint that resolves no tenant.
const endpointName = "account"

// init registers this module. The import is the wiring; the serve command's
// Include list is what puts it in a run.
func init() {
	core.ProcessRegistry.Register(Module())
}

// Module is the descriptor a host registers.
//
// The roster, the memberships, the signup-verification read, the reserved-name
// answer, the provisioner, the key-value store, the settings store and the
// audit capability are required: every decision the route makes reads one of
// them, and an assembly that names this module without one cannot answer
// correctly. The HTTP entry point, the feature board, the gateway's registration
// surface and the logger are optional.
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

// moduleConfig is the module's configuration.
type moduleConfig struct {
	// Enabled is the switch. Off, the route is not mounted and the feature is
	// not in force; the claimed /api/v1/orgs prefix then answers the account
	// endpoint's own 404.
	Enabled bool `config:"enabled"`
}

// configSchema declares the module's switch. The defaults document turns it on
// for a bare run; a deployment that turns it off mounts nothing.
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

// limitDefaults is the carrier of the two runtime settings. Its field values are
// the declared defaults.
type limitDefaults struct {
	MaxOrganizations int
	MaxPerHour       int
}

// settingsSchema declares the two runtime settings. Both are global items: the
// schema leaves TenantFacing at its zero value and each item leaves TenantAccess
// at TenantNone, so the tenant settings surface neither lists nor writes them
// and a tenant administrator cannot read or write any org.create key. Control,
// Danger and Effect stay at their zero values too: these are numbers a
// platform administrator edits, not a tenant-facing control.
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

// readConfig decodes the switch. Prepare and New both go through it so the two
// see one configuration.
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

// product is the module's instance: the seams each decision reads.
type product struct {
	config       moduleConfig
	roster       tenant.Catalog
	memberships  authn.Memberships
	verification authn.SignupVerification
	reservations authn.UsernameReservation
	settings     settings.Store
	kv           kv.KV
	provisioner  tenant.Provisioner
	audit        audit.Audit
	logger       *slog.Logger
	// now is the clock the hourly window and Retry-After are read from. It is a
	// field so a case can move the window without sleeping.
	now func() time.Time
	// newIdentifier mints one candidate identifier. It is a field for the same
	// reason: a case can force a collision the random generator would not.
	newIdentifier func() string
}

// productOf narrows an instance to this module's product.
func productOf(instance any) (*product, bool) {
	product, ok := instance.(*product)
	return product, ok && product != nil
}

// newProduct takes up what the descriptor requires.
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

// optional takes up a capability whose absence is a legal assembly.
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

// wire mounts the route and posts the feature when the switch is on.
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
	// The route is registered for an impersonating identity so the handler can
	// refuse it with a 403 rather than let the gateway answer its own 401. The
	// refusal is the product's: an impersonating operator does not open an
	// organization in the impersonated account's name.
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
