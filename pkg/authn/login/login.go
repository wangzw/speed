// Package login carries the login engine of the authentication capability: the
// flow record, the state machine that advances it, the account resolution step,
// the second-factor orchestration and the terminal actions that make a session
// or issue a token.
//
// It is a process engine and a set of routes, not a login method. The kinds of
// first factor live in leaf packages that each deliver authn.LoginMethod beside
// one another, and the engine takes them all with core.ResolveAll in its Init.
// An assembly that names this module and delivers no method fails at startup
// with authn.ErrNoLoginMethod, and two methods of one name fail with
// authn.ErrMethodConflict.
//
// One login is one record in the key-value store, advanced by one
// compare-and-swap per step, so that a replay, a re-used authorization code and
// two concurrent answers are judged by the store's own atomicity and not by the
// engine. A callback is moved to processing before the method is asked, so the
// authorization code or the assertion is handled at most once. When the flow is
// ready, the terminal action swaps the record to a tombstone and only then makes
// the session or issues the token, so that a login completes at most once.
//
// The routes are mounted through mount.Mounter, which registers each route and
// its access level in one call. Account resolution is the work of the
// internal/resolve package and the account self-service routes are the work of
// the internal/selfservice package; this package builds both and calls them.
//
// The one place this package writes an identity rather than reading one is the
// audit row of a completed login: it derives a context carrying the account the
// login just proved, so that the row's actor is that account, and hands the
// context to the audit record alone. The derived context reaches no handler and
// no later step, so it does not flow back into the request chain.
package login

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/clock"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/login/internal/resolve"
	"github.com/vislake/speed/pkg/authn/login/internal/selfservice"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/observability"
	"github.com/vislake/speed/pkg/settings"
)

// ModuleName is this module's name in the registry. A host writes it into the
// command that takes the login engine in.
const ModuleName = "authn.login"

// diagnosticPrefix starts every line this module writes to the startup
// diagnostics, which have several writers; it is the gateway's, so that a reader
// scanning for what authentication did finds both.
const diagnosticPrefix = "authn: "

// init registers this module with the process registry.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// It delivers nothing: the engine is a process and a set of routes, and every
// capability it uses comes from another module. The route mounter, the
// key-value store, the account reads and writes, the password capability, the
// member reads, the failure back-off, the session store and the multi-factor
// module are required, and each is delivered exclusively by another module, so a
// command that names this one fails before anything is built unless it names
// them too. The settings store is required as optional only so that it is
// initialised first and its snapshot is loaded when Init reads a setting.
//
// The stance is StateEnabled and never StateAuto: the module is taken in at run
// level, so it has no way to stand itself down.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*mount.Mounter)(nil)},
			{Token: (*kv.KV)(nil)},
			{Token: (*authn.Accounts)(nil)},
			{Token: (*authn.AccountAdmin)(nil)},
			{Token: (*authn.Passwords)(nil)},
			{Token: (*authn.Memberships)(nil)},
			{Token: (*authn.Throttle)(nil)},
			{Token: (*authn.Sessions)(nil)},
			{Token: (*authn.MFA)(nil)},
			{Token: (*settings.Store)(nil), Optional: true},
		},
		Resources: []any{settingsSchema()},

		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			reader, err := readerOf(reg)
			if err != nil {
				return core.Enablement{}, err
			}
			if err := checkConfig(reader); err != nil {
				return core.Enablement{}, err
			}
			return core.Enablement{State: core.StateEnabled}, nil
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			return newProduct(reg)
		},

		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			p, ok := productOf(instance)
			if !ok {
				return nil
			}
			return p.wire(reg)
		},
	}
}

// productOf narrows an instance to this module's product. An instance of another
// shape is one a host supplied for this module, and the callbacks leave it
// alone.
func productOf(instance any) (*product, bool) {
	p, ok := instance.(*product)
	return p, ok && p != nil
}

// product is the module's instance.
type product struct {
	// Required capabilities, taken up by New.
	mounter     mount.Mounter
	kv          kv.KV
	accounts    authn.Accounts
	admin       authn.AccountAdmin
	passwords   authn.Passwords
	memberships authn.Memberships
	throttle    authn.Throttle
	sessions    authn.Sessions
	mfa         authn.MFA

	// Optional capabilities, taken up by Init and read-only afterwards.
	tokens      authn.Tokens
	smsCodes    authn.SMSCodes
	sso         authn.SSOProviders
	provisioner authn.Provisioner
	store       settings.Store
	audit       audit.Audit
	bus         event.Bus
	meters      meters
	logger      *slog.Logger
	settings    *reader

	// Assembled by Init.
	resolver    *resolve.Resolver
	selfservice *selfservice.Service
	methods     map[string]authn.LoginMethod
	methodNames []string

	// Configuration read once, in Init: the trusted origins a return_to may
	// name, whether the flow cookie keeps its __host- prefix and Secure
	// attribute, and the origin the deployment is reached at.
	trustedOrigins []string
	allowInsecure  bool

	clock clock.Source
}

// newProduct takes up the required capabilities. The descriptor declares each of
// them as required, so the registry fails before this runs unless every one of
// them is present; each resolution is still checked and its failure names the
// capability.
func newProduct(reg *core.Registry) (*product, error) {
	p := &product{
		methods: map[string]authn.LoginMethod{},
		logger:  log.Default(),
	}
	type step struct {
		name string
		take func() error
	}
	steps := []step{
		{"the route mounter", func() (err error) { p.mounter, err = core.Resolve[mount.Mounter](reg); return }},
		{"the key-value store", func() (err error) { p.kv, err = core.Resolve[kv.KV](reg); return }},
		{"the account reads", func() (err error) { p.accounts, err = core.Resolve[authn.Accounts](reg); return }},
		{"the account writes", func() (err error) { p.admin, err = core.Resolve[authn.AccountAdmin](reg); return }},
		{"the password capability", func() (err error) { p.passwords, err = core.Resolve[authn.Passwords](reg); return }},
		{"the member reads", func() (err error) { p.memberships, err = core.Resolve[authn.Memberships](reg); return }},
		{"the failure back-off", func() (err error) { p.throttle, err = core.Resolve[authn.Throttle](reg); return }},
		{"the session store", func() (err error) { p.sessions, err = core.Resolve[authn.Sessions](reg); return }},
		{"the multi-factor module", func() (err error) { p.mfa, err = core.Resolve[authn.MFA](reg); return }},
	}
	for _, s := range steps {
		if err := s.take(); err != nil {
			return nil, fmt.Errorf("%s%s: taking up %s: %w", diagnosticPrefix, ModuleName, s.name, err)
		}
	}
	return p, nil
}

// wire runs in Init, where every instance exists.
func (p *product) wire(reg *core.Registry) error {
	if delivered, err := core.Resolve[log.Logger](reg); err == nil {
		p.logger = delivered.Named(ModuleName)
	}
	var err error
	if p.tokens, err = optional[authn.Tokens](reg, "the token capability"); err != nil {
		return err
	}
	if p.smsCodes, err = optional[authn.SMSCodes](reg, "the SMS code capability"); err != nil {
		return err
	}
	if p.sso, err = optional[authn.SSOProviders](reg, "the enterprise provider register"); err != nil {
		return err
	}
	if p.provisioner, err = optional[authn.Provisioner](reg, "the provisioning hook"); err != nil {
		return err
	}
	if p.audit, err = optional[audit.Audit](reg, "the audit capability"); err != nil {
		return err
	}
	if p.bus, err = optional[event.Bus](reg, "the event bus"); err != nil {
		return err
	}
	if p.store, err = optional[settings.Store](reg, "the settings store"); err != nil {
		return err
	}
	p.settings = newReader(p.store, p.logger)
	p.subscribe()

	if err = p.takeMethods(core.ResolveAll[authn.LoginMethod](reg)); err != nil {
		return err
	}
	if len(p.methods) == 0 {
		return fmt.Errorf("%w: the login engine is taken in with no login method. Write the module of at "+
			"least one method into this command", authn.ErrNoLoginMethod)
	}

	reader, err := optional[config.Reader](reg, "the configuration reader")
	if err != nil {
		return err
	}
	if err = p.readConfig(reader); err != nil {
		return err
	}

	if err = p.buildResolver(reader); err != nil {
		return err
	}
	if err = p.buildSelfservice(); err != nil {
		return err
	}

	if provider, err := optional[observability.MeterProvider](reg, "the metric capability"); err != nil {
		return err
	} else if provider != nil {
		if p.meters, err = newMeters(provider); err != nil {
			return fmt.Errorf("%s%s: creating the instruments: %w", diagnosticPrefix, ModuleName, err)
		}
	}

	p.mountRoutes()
	p.diagnose(reg)
	return nil
}

// optional takes up a capability whose absence is a legal assembly.
func optional[T any](reg *core.Registry, what string) (T, error) {
	var zero T
	v, err := core.Resolve[T](reg)
	switch {
	case err == nil:
		return v, nil
	case errors.Is(err, core.ErrMissingProvider):
		return zero, nil
	default:
		return zero, fmt.Errorf("%s%s: taking up %s: %w", diagnosticPrefix, ModuleName, what, err)
	}
}

// takeMethods holds the methods taken in to the rules the engine needs of them:
// a name that is non-empty and unique. No method is a legal assembly until a
// request begins a flow, at which point it is ErrNoLoginMethod.
func (p *product) takeMethods(all []authn.LoginMethod) error {
	for _, m := range all {
		name := m.Name()
		if name == "" {
			return fmt.Errorf("%w: the method %T declares no name", authn.ErrMethodConflict, m)
		}
		if prior, dup := p.methods[name]; dup {
			return fmt.Errorf("%w: the name %q is declared by both %T and %T. One method has one name; "+
				"leave one of them out of this command", authn.ErrMethodConflict, name, prior, m)
		}
		p.methods[name] = m
	}
	p.methodNames = make([]string, 0, len(p.methods))
	for name := range p.methods {
		p.methodNames = append(p.methodNames, name)
	}
	slices.Sort(p.methodNames)
	return nil
}

// readConfig reads the gateway origins a return_to may name and the insecure-use
// switch, and refreshes the disabled-method set from the settings.
func (p *product) readConfig(reader config.Reader) error {
	gate, err := readGate(reader)
	if err != nil {
		return fmt.Errorf("%s%s: reading the gateway keys: %w", diagnosticPrefix, ModuleName, err)
	}
	origins, err := mount.TrustedOrigins(gate.Mount.PublicURL, gate.TrustedOrigins)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", authn.ErrInvalidConfig, ModuleName, err)
	}
	if len(origins) == 0 && p.mounter.PublicURL() != "" {
		origins = []string{p.mounter.PublicURL()}
	}
	p.trustedOrigins = origins
	p.allowInsecure = gate.Mount.AllowInsecure
	return nil
}

// buildResolver builds the account-resolution step from the membership, factor
// and provider capabilities and the two relaxation switches.
func (p *product) buildResolver(reader config.Reader) error {
	account, err := readAccount(reader)
	if err != nil {
		return fmt.Errorf("%s%s: reading the account switches: %w", diagnosticPrefix, ModuleName, err)
	}
	r, err := resolve.New(resolve.Config{
		Accounts:    p.accounts,
		Admin:       p.admin,
		Memberships: p.memberships,
		Factors:     p.mfa,
		SSO:         p.sso,
		Provisioner: p.provisioner,
		Signup:      resolve.Signup(account.Signup),
		AutoLink:    resolve.AutoLink(account.AutoLink),
	})
	if err != nil {
		return fmt.Errorf("%s%s: %w", diagnosticPrefix, ModuleName, err)
	}
	p.resolver = r
	return nil
}

// buildSelfservice builds the account self-service routes.
func (p *product) buildSelfservice() error {
	svc, err := selfservice.New(selfservice.Deps{
		Accounts:  p.accounts,
		Admin:     p.admin,
		Passwords: p.passwords,
		Sessions:  p.sessions,
		MFA:       p.mfa,
		SMSCodes:  p.smsCodes,
		KV:        p.kv,
		Throttle:  p.throttle,
		Settings:  p.store,
		Bus:       p.bus,
		Audit:     p.audit,
		Clock:     p.clock,
	})
	if err != nil {
		return fmt.Errorf("%s%s: %w", diagnosticPrefix, ModuleName, err)
	}
	p.selfservice = svc
	return nil
}

// readerOf takes up the configuration reader. Its absence means the host never
// handed one to the registry, which is core's own failure.
func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: taking up the configuration reader: %w", diagnosticPrefix, ModuleName, err)
	}
	return reader, nil
}

// checkConfig holds the configuration this module reads in Prepare to what it
// can run on. It reads the same keys wire reads, so the two see one
// configuration.
func checkConfig(reader config.Reader) error {
	if _, err := readGate(reader); err != nil {
		return fmt.Errorf("%s%s: reading the gateway keys: %w", diagnosticPrefix, ModuleName, err)
	}
	if _, err := readAccount(reader); err != nil {
		return fmt.Errorf("%s%s: reading the account switches: %w", diagnosticPrefix, ModuleName, err)
	}
	return nil
}

// diagnose writes the startup lines: which methods take part, whether the token
// delivery is available, whether the phone routes are mounted, whether the
// enterprise providers and the audit and event capabilities are present, and
// whether the end the routes are mounted on is one the tenant resolution layer
// guards.
func (p *product) diagnose(reg *core.Registry) {
	if len(p.methodNames) == 0 {
		p.logger.Warn(diagnosticPrefix + "no login method takes part, so no login flow can be started")
	} else {
		p.logger.Info(diagnosticPrefix + "the login methods taking part: " + strings.Join(p.methodNames, ", "))
	}
	delivery := p.settings.deliveryModes()
	if p.tokens == nil {
		p.logger.Info(diagnosticPrefix + "no token capability takes part, so delivery=token is not enabled")
	} else if !slices.Contains(delivery, deliveryToken) {
		p.logger.Info(diagnosticPrefix + "delivery=token is turned off by " + ModuleName + "." + deliveryModesKey)
	}
	if p.smsCodes == nil {
		p.logger.Info(diagnosticPrefix + "no SMS code capability takes part, so the phone self-service routes are not mounted")
	}
	if p.sso == nil {
		p.logger.Info(diagnosticPrefix + "no enterprise provider register takes part, so an IdP-initiated sign-in is refused")
	}
	if p.audit == nil {
		p.logger.Info(diagnosticPrefix + "no audit capability takes part, so a login writes no audit row")
	}
	if p.bus == nil {
		p.logger.Info(diagnosticPrefix + "no event bus takes part, so a login publishes no event")
	}
	if p.settings.ipLimit() == 0 {
		p.logger.Warn(diagnosticPrefix+ModuleName+": the source-address pre-throttle is off, because "+
			ModuleName+"."+ipLimitKey+" is 0",
			slog.String("setting", ModuleName+"."+ipLimitKey))
	} else {
		p.logger.Warn(diagnosticPrefix + ModuleName + ": the source-address pre-throttle is on; behind a proxy " +
			"every caller shares one bucket, because the entry point does not trust forwarded headers")
	}
	p.checkResolutionEndpoints(reg)
}

// checkResolutionEndpoints checks that the end the routes are mounted on is not
// one the tenant resolution layer guards, since a global account's login
// request carries no tenant name and a guarded end answers it a 404. The
// resolution layer's endpoint list is read from configuration; a value of
// another shape writes a Warn and never fails the startup.
func (p *product) checkResolutionEndpoints(reg *core.Registry) {
	reader, err := optional[config.Reader](reg, "the configuration reader")
	if err != nil || reader == nil {
		return
	}
	name := p.mounter.Endpoint()
	endpoints, ok := readResolveEndpoints(reader)
	if !ok {
		p.logger.Warn(diagnosticPrefix + "the endpoints the tenant resolution layer guards cannot be read, so " +
			"it is not checked whether the login routes are mounted on one of them")
		return
	}
	for _, guarded := range endpoints {
		if guarded == name {
			p.logger.Error(diagnosticPrefix+"the login routes are mounted on the endpoint "+name+
				", which the tenant resolution layer guards; a login request carries no tenant name and "+
				"would be answered a 404", slog.String("endpoint", name))
			return
		}
	}
}
