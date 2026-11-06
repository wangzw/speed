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
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/clock"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/internal/slug"
	"github.com/vislake/speed/pkg/authn/login/internal/resolve"
	"github.com/vislake/speed/pkg/authn/login/internal/selfservice"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/feature"
	speedhttp "github.com/vislake/speed/pkg/http"
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

// defaultEndpoint is the endpoint this module's fragment is declared on: the
// account endpoint under the gateway's base path, which is where the reference
// assembly mounts the authentication capability's own routes. A resource is a
// static declaration fixed before configuration is read, so the field carries
// this default; the merged document takes it as the endpoint of these
// operations. A deployment that mounts the routes elsewhere is drift, which the
// fragment is allowed to have and which produces no failure signal (see pkg/ops).
const defaultEndpoint = "account"

//go:embed openapi/login.json
var loginFragment []byte

// loginSpec is the API root this module's fragment and routes hang off. Its
// prefix and version are the common ones; the fragment writes the capability's
// own segment, so a route's declared path begins with the part of the mounter's
// base that lies below this root.
var loginSpec = speedhttp.Spec{
	Endpoint: defaultEndpoint,
	Prefix:   "api",
	Version:  "v1",
	Document: loginFragment,
}

// init registers this module with the process registry.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// It delivers nothing: the engine is a process and a set of routes, and every
// capability it uses comes from another module. The route mounter, the
// key-value store, the account reads and writes, the self-registration and its
// verification read, the password capability, the member reads, the failure
// back-off, the session store and the multi-factor module are required, and each
// is delivered exclusively by another module, so a command that names this one
// fails before anything is built unless it names them too. The human-verification
// gate is required: the humanverify module delivers one in every mode, off
// included, so the requirement forces no mechanism, while an engine without one
// would send ungated whenever the configuration names a mode. The settings store
// is required as optional only so that it is initialised first and its snapshot
// is loaded when Init reads a setting, and the impersonation capability is
// optional so that an assembly without one refuses the completion of a flow an
// impersonating identity started.
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
			{Token: (*authn.Signups)(nil)},
			{Token: (*authn.SignupVerification)(nil)},
			{Token: (*authn.Passwords)(nil)},
			{Token: (*authn.Memberships)(nil)},
			{Token: (*authn.Throttle)(nil)},
			{Token: (*authn.Sessions)(nil)},
			{Token: (*authn.MFA)(nil)},
			{Token: (*authn.HumanVerifier)(nil)},
			{Token: (*settings.Store)(nil), Optional: true},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Resources: []any{
			settingsSchema(),
			loginSpec,
			operations,
			// The optional account self-service surfaces. Each is posted in
			// Init from the same judgement that mounts it; the name constants
			// live beside that judgement.
			feature.Spec{Name: selfservice.FeaturePhone, Conditional: true},
			feature.Spec{Name: selfservice.FeatureEmail, Conditional: true},
		},

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
	mounter      mount.Mounter
	kv           kv.KV
	accounts     authn.Accounts
	admin        authn.AccountAdmin
	signups      authn.Signups
	verification authn.SignupVerification
	passwords    authn.Passwords
	memberships  authn.Memberships
	throttle     authn.Throttle
	sessions     authn.Sessions
	mfa          authn.MFA
	human        authn.HumanVerifier

	// Optional capabilities, taken up by Init and read-only afterwards.
	tokens        authn.Tokens
	impersonation authn.Impersonation
	smsCodes      authn.SMSCodes
	emailSender   authn.EmailCodeSender
	emailReset    authn.EmailResetCodeSender
	sso           authn.SSOProviders
	provisioner   authn.Provisioner
	store         settings.Store
	audit         audit.Audit
	bus           event.Bus
	meters        meters
	logger        *slog.Logger
	settings      *reader

	// Assembled by Init.
	resolver    *resolve.Resolver
	selfservice *selfservice.Service
	resetMail   *resetMailer
	board       feature.Board
	methods     map[string]authn.LoginMethod
	methodNames []string

	// Configuration read once, in Init: the trusted origins a return_to may
	// name, whether the flow cookie keeps its __host- prefix and Secure
	// attribute, and the origin the deployment is reached at. The registration
	// and the registration switch are settings, read at each use.
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
		{"the self-registration", func() (err error) { p.signups, err = core.Resolve[authn.Signups](reg); return }},
		{"the signup verification reads", func() (err error) {
			p.verification, err = core.Resolve[authn.SignupVerification](reg)
			return
		}},
		{"the password capability", func() (err error) { p.passwords, err = core.Resolve[authn.Passwords](reg); return }},
		{"the member reads", func() (err error) { p.memberships, err = core.Resolve[authn.Memberships](reg); return }},
		{"the failure back-off", func() (err error) { p.throttle, err = core.Resolve[authn.Throttle](reg); return }},
		{"the session store", func() (err error) { p.sessions, err = core.Resolve[authn.Sessions](reg); return }},
		{"the multi-factor module", func() (err error) { p.mfa, err = core.Resolve[authn.MFA](reg); return }},
		{"the human-verification gate", func() (err error) { p.human, err = core.Resolve[authn.HumanVerifier](reg); return }},
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
	if p.impersonation, err = optional[authn.Impersonation](reg, "the impersonation capability"); err != nil {
		return err
	}
	if p.smsCodes, err = optional[authn.SMSCodes](reg, "the SMS code capability"); err != nil {
		return err
	}
	if p.emailSender, err = optional[authn.EmailCodeSender](reg, "the email code channel"); err != nil {
		return err
	}
	if p.emailReset, err = optional[authn.EmailResetCodeSender](reg, "the password reset email channel"); err != nil {
		return err
	}
	p.resetMail = newResetMailer(resetMailWorkers)
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

	if err = p.buildResolver(); err != nil {
		return err
	}
	if err = p.buildSelfservice(); err != nil {
		return err
	}
	p.takeBoard(reg)

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

// takeBoard takes up the feature board when this process publishes one. It is
// not a requirement: a process that serves no frontend has no board, and the
// report it would receive is skipped.
func (p *product) takeBoard(reg *core.Registry) {
	if b, err := core.Resolve[feature.Board](reg); err == nil {
		p.board = b
	}
}

// postFeature reports one conditional feature of this module to the board, when
// the process has one. It is handed to the account self-service mount, which
// calls it in the judgement that decides the surface's presence.
func (p *product) postFeature(name string) {
	if p.board != nil {
		p.board.Post(ModuleName, name)
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

// buildResolver builds the account-resolution step from the membership and
// provider capabilities. The first-login switch is read through the settings
// store at each resolution, so a change takes effect without a restart.
func (p *product) buildResolver() error {
	r, err := resolve.New(resolve.Config{
		Accounts:          p.accounts,
		Admin:             p.admin,
		Memberships:       p.memberships,
		SSO:               p.sso,
		Provisioner:       p.provisioner,
		NormalizeUsername: slug.Normalize,
		Switches: func() resolve.Signup {
			return resolve.Signup(p.settings.signup())
		},
	})
	if err != nil {
		return fmt.Errorf("%s%s: %w", diagnosticPrefix, ModuleName, err)
	}
	p.resolver = r
	return nil
}

// signupEnabled is whether a first login may open an account, as the settings
// state it now.
func (p *product) signupEnabled() bool {
	return p.settings.signup() == signupOpen
}

// passwordSignupEnabled is whether the anonymous name-and-password registration
// route is open, as the settings state it now. It is a pre-check only: the
// authority is Signups.Register, which reads the same key.
func (p *product) passwordSignupEnabled() bool {
	return p.settings.passwordSignup() == signupOpen
}

// buildSelfservice builds the account self-service routes.
func (p *product) buildSelfservice() error {
	svc, err := selfservice.New(selfservice.Deps{
		Accounts:    p.accounts,
		Admin:       p.admin,
		Passwords:   p.passwords,
		Sessions:    p.sessions,
		MFA:         p.mfa,
		SMSCodes:    p.smsCodes,
		KV:          p.kv,
		Throttle:    p.throttle,
		EmailSender: p.emailSender,
		Signup:      p.verification,
		Settings:    p.store,
		Bus:         p.bus,
		Audit:       p.audit,
		Human:       p.human,
		Clock:       p.clock,
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
	if p.emailSender == nil {
		p.logger.Info(diagnosticPrefix + "no email code channel takes part, so an email address cannot be added on the " +
			"self-service face")
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
	if limit := p.settings.ipLimit(); limit == 0 {
		p.logger.Warn(diagnosticPrefix+ModuleName+": the source-address pre-throttle is off, because "+
			ModuleName+"."+ipLimitKey+" is 0",
			slog.String("setting", ModuleName+"."+ipLimitKey))
	} else {
		// The layer being on is the ordinary state; the relaxation is what a
		// reader has to be able to find, so only that one is a warning.
		p.logger.Info(diagnosticPrefix+ModuleName+": the source-address pre-throttle is on",
			slog.String("setting", ModuleName+"."+ipLimitKey), slog.Int("per-minute", limit))
	}
	p.diagnoseSignup()
	p.diagnoseReset()
	p.checkResolutionEndpoints(reg)
}

// diagnoseReset writes the startup lines of the anonymous password reset: whether
// the switch is open, and which channels this assembly can carry. The switch is
// read once here to report a deployment that starts already relaxed, and the
// channels are the same judgement that publishes them in the discovery document.
func (p *product) diagnoseReset() {
	if p.settings.passwordReset() != passwordResetOpen {
		p.logger.Info(diagnosticPrefix + "the anonymous password reset is closed, so its routes answer 403 " +
			"password-reset-closed, because " + passwordResetPath + " is not open")
		return
	}
	p.logger.Warn(diagnosticPrefix+"the anonymous password reset is open, so its routes answer anonymous "+
		"callers, because "+passwordResetPath+" is open",
		slog.String("setting", passwordResetPath))
	if p.audit == nil {
		p.logger.Warn(diagnosticPrefix + "the anonymous password reset is not published because no audit " +
			"capability takes part")
	}
	if p.emailReset == nil {
		p.logger.Info(diagnosticPrefix + "no password reset email channel takes part, so the reset's email " +
			"channel is not published")
	}
	if p.smsCodes == nil {
		p.logger.Info(diagnosticPrefix + "no SMS code capability takes part, so the reset's SMS channel is " +
			"not published")
	}
}

// diagnoseSignup writes the startup lines of anonymous self-registration: whether
// the route is open, and whether the per-source budget that bounds it is off. The
// switch itself is the account module's, read here only to report it.
func (p *product) diagnoseSignup() {
	if !p.passwordSignupEnabled() {
		p.logger.Info(diagnosticPrefix + "name-and-password self-registration is closed, so the register route " +
			"answers 403 signup-closed, because " + passwordSignupPath + " is not open")
	} else {
		p.logger.Info(diagnosticPrefix + "name-and-password self-registration is open")
	}
	if limit := p.settings.signupIPLimit(); limit == 0 {
		p.logger.Warn(diagnosticPrefix+ModuleName+": the per-source budget of a registration is off, because "+
			signupIPLimitPath+" is 0", slog.String("setting", signupIPLimitPath))
	} else {
		p.logger.Info(diagnosticPrefix+ModuleName+": the per-source budget of a registration is on",
			slog.String("setting", signupIPLimitPath), slog.Int("per-minute", limit))
	}
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
