// Package login carries the login engine: the flow record and state machine, account resolution, second-factor orchestration, and the terminal actions that make a session or issue a token.
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
	"github.com/vislake/speed/pkg/authn/internal/cookiename"
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

// ModuleName is this module's name in the registry.
const ModuleName = "authn.login"

const diagnosticPrefix = "authn: "

const defaultEndpoint = "account"

//go:embed openapi/login.json
var loginFragment []byte

var loginSpec = speedhttp.Spec{
	Endpoint: defaultEndpoint,
	Prefix:   "api",
	Version:  "v1",
	Document: loginFragment,
}

func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers. It delivers the login-method switch and nothing else, every other capability coming from another module, and its state is StateEnabled, never StateAuto.
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
		Provides: []core.Provision{
			{Token: (*authn.LoginMethodSwitch)(nil), Exclusive: true},
		},
		Resources: []any{
			settingsSchema(),
			loginSpec,
			operations,
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

func productOf(instance any) (*product, bool) {
	p, ok := instance.(*product)
	return p, ok && p != nil
}

type product struct {
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

	tokens        authn.Tokens
	impersonation authn.Impersonation
	smsCodes      authn.SMSCodes
	emailSender   authn.EmailCodeSender
	emailReset    authn.EmailResetCodeSender
	sso           authn.SSOProviders
	provisioner   authn.Provisioner
	tenantMFA     authn.TenantMFARequirement
	store         settings.Store
	audit         audit.Audit
	bus           event.Bus
	meters        meters
	logger        *slog.Logger
	settings      *reader

	resolver    *resolve.Resolver
	selfservice *selfservice.Service
	resetMail   *resetMailer
	board       feature.Board
	methods     map[string]authn.LoginMethod
	methodNames []string

	trustedOrigins []string
	publicURL      string
	flowCookie     string
	resetCookie    string

	clock clock.Source
}

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
	if p.tenantMFA, err = optional[authn.TenantMFARequirement](reg, "the tenant multi-factor requirement"); err != nil {
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

func (p *product) DisabledMethods() ([]string, error) {
	return p.settings.disabledMethods()
}

var _ authn.LoginMethodSwitch = (*product)(nil)

func (p *product) takeBoard(reg *core.Registry) {
	if b, err := core.Resolve[feature.Board](reg); err == nil {
		p.board = b
	}
}

func (p *product) postFeature(name string) {
	if p.board != nil {
		p.board.Post(ModuleName, name)
	}
}

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
	publicURL := gate.Mount.PublicURL
	if publicURL == "" {
		publicURL = p.mounter.PublicURL()
	}
	p.nameCookies(publicURL)
	return nil
}

func (p *product) nameCookies(publicURL string) {
	p.publicURL = publicURL
	p.flowCookie = cookiename.Name(publicURL, cookiename.Flow, "")
	p.resetCookie = cookiename.Name(publicURL, cookiename.Reset, "")
}

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

func (p *product) signupEnabled() bool {
	return p.settings.signup() == signupOpen
}

func (p *product) passwordSignupEnabled() bool {
	return p.settings.passwordSignup() == signupOpen
}

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

func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: taking up the configuration reader: %w", diagnosticPrefix, ModuleName, err)
	}
	return reader, nil
}

func checkConfig(reader config.Reader) error {
	if _, err := readGate(reader); err != nil {
		return fmt.Errorf("%s%s: reading the gateway keys: %w", diagnosticPrefix, ModuleName, err)
	}
	return nil
}

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
		p.logger.Info(diagnosticPrefix+ModuleName+": the source-address pre-throttle is on",
			slog.String("setting", ModuleName+"."+ipLimitKey), slog.Int("per-minute", limit))
	}
	p.diagnoseSignup()
	p.diagnoseReset()
	p.checkResolutionEndpoints(reg)
}

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
