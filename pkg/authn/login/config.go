package login

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/settings"
)

// The setting keys of this module, relative to its namespace, and the full paths
// a read goes by. The module declares these; the ones belonging to another
// namespace are read, not declared, and their owners state their domains.
const (
	flowTTLKey         = "flow-ttl"
	callBudgetKey      = "call-budget"
	defaultReturnToKey = "default-return-to"
	disabledMethodsKey = "disabled-methods"
	deliveryModesKey   = "delivery-modes"
	ipLimitKey         = "ip-limit"
	emailCodeTTLKey    = "email-code-ttl"
	emailResendKey     = "email-resend-interval"

	disabledMethodsPath = ModuleName + "." + disabledMethodsKey
	deliveryModesPath   = ModuleName + "." + deliveryModesKey
	flowTTLPath         = ModuleName + "." + flowTTLKey
	ipLimitPath         = ModuleName + "." + ipLimitKey

	// The self-registration settings the account module declares and this
	// module only reads: the registration switch is the authority in
	// Signups.Register and a pre-check here; the two budgets bound the
	// anonymous route before an account is written. Each path is the account
	// namespace joined to the key name its owner declares.
	passwordSignupKey     = "password-signup"
	signupIPLimitKey      = "signup-ip-limit"
	signupGlobalHourlyKey = "signup-global-hourly"
	accountNamespace      = "authn.account"

	passwordSignupPath     = accountNamespace + "." + passwordSignupKey
	signupIPLimitPath      = accountNamespace + "." + signupIPLimitKey
	signupGlobalHourlyPath = accountNamespace + "." + signupGlobalHourlyKey
)

// The domains of the settings this module declares.
const (
	minFlowTTL = time.Minute
	maxFlowTTL = 30 * time.Minute

	minCallBudget = time.Second
	maxCallBudget = 60 * time.Second

	// maxIPLimit is the most source-address pre-throttle starts per minute, the
	// setting's own unit.
	maxIPLimit = 10000

	// The domain of the self-service email-code lifetime and of the interval
	// before another code may be asked for. They are the login module's, read by
	// the self-service email routes.
	minEmailCodeTTL = time.Minute
	maxEmailCodeTTL = 30 * time.Minute

	minEmailResend = 30 * time.Second
	maxEmailResend = 10 * time.Minute
)

// The closed set of delivery modes, and the two the value-name set names.
const (
	deliverySession = "session"
	deliveryToken   = "token"

	// maxReturnToBytes is the most a return_to may be, in bytes.
	maxReturnToBytes = 2048
)

// The two relaxation switches the account module declares and this module reads.
// A value outside the set the account module declares reads as the strict one.
const (
	signupPath   = "authn.account.signup"
	autoLinkPath = "authn.account.auto-link"

	signupClosed = "closed"
	signupOpen   = "open"

	autoLinkNever         = "never"
	autoLinkVerifiedEmail = "verified-email"
)

func signupInDomain(v string) bool { return v == signupClosed || v == signupOpen }

func autoLinkInDomain(v string) bool {
	return v == autoLinkNever || v == autoLinkVerifiedEmail
}

// The domain and the declared default of the two registration budgets, repeated
// here because this module only reads the values the account module declares: one
// source address may register at most signupIPLimitCeil times a minute, and the
// whole deployment at most signupHourlyCeil times an hour.
const (
	signupIPLimitCeil         = 10000
	signupGlobalHourlyFloor   = 1
	signupGlobalHourlyCeil    = 1000000
	signupIPLimitDefault      = 0
	signupGlobalHourlyDefault = 100
)

func signupIPLimitInDomain(n int) bool { return n >= 0 && n <= signupIPLimitCeil }
func signupGlobalHourlyInDomain(n int) bool {
	return n >= signupGlobalHourlyFloor && n <= signupGlobalHourlyCeil
}

// options carries the settings this module declares and their defaults, which
// the settings module reads off it and which it never receives a value for. The
// two lists are string items holding JSON, because a setting item is a scalar.
type options struct {
	FlowTTL         time.Duration `config:"flow-ttl"`
	CallBudget      time.Duration `config:"call-budget"`
	DefaultReturnTo string        `config:"default-return-to"`
	DisabledMethods string        `config:"disabled-methods"`
	DeliveryModes   string        `config:"delivery-modes"`
	IPLimit         int           `config:"ip-limit"`
	EmailCodeTTL    time.Duration `config:"email-code-ttl"`
	EmailResend     time.Duration `config:"email-resend-interval"`
}

// defaults is the declared default of each setting, and the value a tuning read
// that cannot be made falls back to. DisabledMethods is not read through that
// fallback: it is a mandatory switch, and a read that fails refuses.
var defaults = options{
	FlowTTL:         10 * time.Minute,
	CallBudget:      10 * time.Second,
	DefaultReturnTo: "/",
	DisabledMethods: "[]",
	DeliveryModes:   `["session","token"]`,
	IPLimit:         0,
	EmailCodeTTL:    10 * time.Minute,
	EmailResend:     60 * time.Second,
}

// The domains of the settings this module declares.
func flowTTLInDomain(d time.Duration) bool      { return d >= minFlowTTL && d <= maxFlowTTL }
func callBudgetInDomain(d time.Duration) bool   { return d >= minCallBudget && d <= maxCallBudget }
func ipLimitInDomain(n int) bool                { return n >= 0 && n <= maxIPLimit }
func emailCodeTTLInDomain(d time.Duration) bool { return d >= minEmailCodeTTL && d <= maxEmailCodeTTL }
func emailResendInDomain(d time.Duration) bool  { return d >= minEmailResend && d <= maxEmailResend }

// deliveryModesTextInDomain reports whether a JSON array of strings is a legal
// value of the delivery-modes setting.
func deliveryModesTextInDomain(text string) bool {
	modes, err := parseList(text)
	if err != nil {
		return false
	}
	return deliveryModesInDomain(modes)
}

// parseList reads a JSON array of strings from the text of a list setting. The
// empty string reads as the empty list.
func parseList(text string) ([]string, error) {
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	var out []string
	dec := json.NewDecoder(strings.NewReader(text))
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// deliveryModesInDomain reports whether every name is one of the closed set and
// none repeats; an empty list is outside the domain, since it names nothing a
// login could be delivered as.
func deliveryModesInDomain(modes []string) bool {
	if len(modes) == 0 {
		return false
	}
	seen := map[string]bool{}
	for _, m := range modes {
		if m != deliverySession && m != deliveryToken {
			return false
		}
		if seen[m] {
			return false
		}
		seen[m] = true
	}
	return true
}

// settingsSchema declares this module's runtime parameters.
func settingsSchema() settings.Schema {
	return settings.Schema{
		Namespace: ModuleName,
		Mounts:    []settings.Mount{{Value: &defaults}},
		Items: map[string]settings.Item{
			flowTTLKey: {
				Description: "how long a login flow lives in the key-value store before it expires, from " +
					minFlowTTL.String() + " to " + maxFlowTTL.String(),
				Domain: settings.DomainOf("durations from "+minFlowTTL.String()+" to "+maxFlowTTL.String(),
					flowTTLInDomain),
			},
			callBudgetKey: {
				Description: "the budget of one call to a login method, covering an external provider's " +
					"round trips, from " + minCallBudget.String() + " to " + maxCallBudget.String(),
				Domain: settings.DomainOf("durations from "+minCallBudget.String()+" to "+maxCallBudget.String(),
					callBudgetInDomain),
			},
			defaultReturnToKey: {
				Description: "where a callback lands the user agent when the flow recorded no return " +
					"target: an identity-provider-initiated SAML sign-in, a login the callback completes or " +
					"leaves needing a factor, and a callback of a purpose other than login",
			},
			disabledMethodsKey: {
				Description: "the login methods turned off at run time, by name, as a JSON array of strings",
			},
			deliveryModesKey: {
				Description: "the delivery modes a login may ask for, as a JSON array of a non-empty subset " +
					"of session and token",
				Domain: settings.DomainOf("a JSON array of a non-empty subset of session and token",
					deliveryModesTextInDomain),
			},
			ipLimitKey: {
				Description: "a coarse per-flow token starts per minute from one source address, zero " +
					"turning the layer off",
				Domain: settings.DomainOf("whole numbers from 0 to 10000", ipLimitInDomain),
			},
			emailCodeTTLKey: {
				Description: "how long a self-service email verification code lives, from " +
					minEmailCodeTTL.String() + " to " + maxEmailCodeTTL.String(),
				Domain: settings.DomainOf("durations from "+minEmailCodeTTL.String()+" to "+maxEmailCodeTTL.String(),
					emailCodeTTLInDomain),
			},
			emailResendKey: {
				Description: "how soon another self-service email verification code may be asked for, from " +
					minEmailResend.String() + " to " + maxEmailResend.String(),
				Domain: settings.DomainOf("durations from "+minEmailResend.String()+" to "+maxEmailResend.String(),
					emailResendInDomain),
			},
		},
	}
}

// reader reads this module's settings with the failure direction each has. The
// store is nil when the assembly has none, and every setting then reads as its
// declared default, which is a legal assembly.
type reader struct {
	store  settings.Store
	logger *slog.Logger

	mu       sync.Mutex
	reported map[string]string
}

func newReader(store settings.Store, logger *slog.Logger) *reader {
	return &reader{store: store, logger: logger, reported: make(map[string]string)}
}

// errSettingRead is what a failed read of the mandatory switch is wrapped in.
var errSettingRead = errors.New("the setting cannot be read")

// disabledMethods reads the mandatory switch of the methods turned off. A read
// that fails is an error and not the empty default: the default turns nothing
// off, so falling back to it would let a method the operator turned off answer.
func (r *reader) disabledMethods() ([]string, error) {
	if r.store == nil {
		return nil, nil
	}
	var text string
	if err := r.store.Get(settings.Global(), disabledMethodsPath, &text); err != nil {
		r.report(disabledMethodsPath, "the switch of the disabled methods cannot be read, so the request is refused", err.Error())
		return nil, fmt.Errorf("%w: %s: %w", errSettingRead, disabledMethodsPath, err)
	}
	list, err := parseList(text)
	if err != nil {
		r.report(disabledMethodsPath, "the switch of the disabled methods is not a JSON array of strings, so the request is refused", err.Error())
		return nil, fmt.Errorf("%w: %s is not a JSON array of strings", errSettingRead, disabledMethodsPath)
	}
	r.report(disabledMethodsPath, "", "")
	return list, nil
}

func (r *reader) flowTTL() time.Duration {
	return readTuned(r, ModuleName+"."+flowTTLKey, defaults.FlowTTL, flowTTLInDomain)
}

func (r *reader) callBudget() time.Duration {
	return readTuned(r, ModuleName+"."+callBudgetKey, defaults.CallBudget, callBudgetInDomain)
}

func (r *reader) defaultReturnTo() string {
	if r.store == nil {
		return defaults.DefaultReturnTo
	}
	var v string
	if err := r.store.Get(settings.Global(), ModuleName+"."+defaultReturnToKey, &v); err != nil {
		r.report(ModuleName+"."+defaultReturnToKey, "the default return target cannot be read, so its declared default is used", err.Error())
		return defaults.DefaultReturnTo
	}
	r.report(ModuleName+"."+defaultReturnToKey, "", "")
	if v == "" {
		return defaults.DefaultReturnTo
	}
	return v
}

// deliveryModes reads the closed set of delivery modes a login may ask for. A
// read that fails or lands outside the domain reads as the declared default,
// which offers both.
func (r *reader) deliveryModes() []string {
	fallback, _ := parseList(defaults.DeliveryModes)
	if r.store == nil {
		return fallback
	}
	var text string
	if err := r.store.Get(settings.Global(), deliveryModesPath, &text); err != nil {
		r.report(deliveryModesPath, "the delivery modes cannot be read, so the declared default is used", err.Error())
		return fallback
	}
	modes, err := parseList(text)
	if err != nil || !deliveryModesInDomain(modes) {
		r.report(deliveryModesPath, "the delivery modes hold a value outside their domain, so the declared default is used", text)
		return fallback
	}
	r.report(deliveryModesPath, "", "")
	return modes
}

func (r *reader) ipLimit() int {
	return readTuned(r, ModuleName+"."+ipLimitKey, defaults.IPLimit, ipLimitInDomain)
}

// signup reads the account module's registration switch. A read that fails or a
// value outside the declared set reads as closed.
func (r *reader) signup() string {
	return readTuned(r, signupPath, signupClosed, signupInDomain)
}

// passwordSignup reads the account module's switch of name-and-password
// self-registration. It is the authority in Signups.Register; the register route
// reads it only as a pre-check, so a read that fails or a value outside the
// declared set reads as closed and the route answers signup-closed.
func (r *reader) passwordSignup() string {
	return readTuned(r, passwordSignupPath, signupClosed, signupInDomain)
}

// signupIPLimit reads the account module's per-source registration budget, in
// registrations a minute; zero turns the budget off. A read that fails or a value
// outside the declared domain reads as the declared default.
func (r *reader) signupIPLimit() int {
	return readTuned(r, signupIPLimitPath, signupIPLimitDefault, signupIPLimitInDomain)
}

// signupGlobalHourly reads the account module's deployment-wide registration
// budget, in registrations an hour. A read that fails or a value outside the
// declared domain reads as the declared default.
func (r *reader) signupGlobalHourly() int {
	return readTuned(r, signupGlobalHourlyPath, signupGlobalHourlyDefault, signupGlobalHourlyInDomain)
}

// autoLink reads the account module's linking switch. A read that fails or a
// value outside the declared set reads as never.
func (r *reader) autoLink() string {
	return readTuned(r, autoLinkPath, autoLinkNever, autoLinkInDomain)
}

// recentWindow reads the multi-factor module's recent-window setting, which one
// step of this module's completion review uses and only reads. A failed read and
// a value outside its domain read as the declared default.
func (r *reader) recentWindow() time.Duration {
	const (
		path = "authn.mfa.recent-window"
		def  = 10 * time.Minute
		lo   = time.Minute
		hi   = time.Hour
	)
	return readTuned(r, path, def, func(d time.Duration) bool { return d >= lo && d <= hi })
}

// perFlowSends reads the per-flow SMS send limit, which the SMS module declares
// and this module only satisfies. Its absence, a failed read and a value outside
// its domain read as the declared default.
func (r *reader) perFlowSends() int {
	const path = "authn.sms.per-flow-sends"
	if r.store == nil {
		return defaultPerFlowSends
	}
	v, err := settings.Get[int](r.store, settings.Global(), path)
	if err != nil {
		r.report(path, "the per-flow send limit cannot be read, so the declared default is used", err.Error())
		return defaultPerFlowSends
	}
	if v < minPerFlowSends || v > maxPerFlowSends {
		r.report(path, "the per-flow send limit holds a value outside its domain, so the declared default is used", fmt.Sprint(v))
		return defaultPerFlowSends
	}
	r.report(path, "", "")
	return v
}

// The domain of authn.sms.per-flow-sends and its default, repeated here because
// this module only reads the value the SMS module declares.
const (
	defaultPerFlowSends = 3
	minPerFlowSends     = 1
	maxPerFlowSends     = 10
)

// readTuned reads a tuning setting: one that fails to read, and one outside its
// domain, read as the declared default, and the fault is reported at Error once
// per distinct fault.
func readTuned[T any](r *reader, path string, fallback T, inDomain func(T) bool) T {
	if r.store == nil {
		return fallback
	}
	v, err := settings.Get[T](r.store, settings.Global(), path)
	switch {
	case err != nil:
		r.report(path, "the setting cannot be read, so its declared default is used", err.Error())
		return fallback
	case !inDomain(v):
		r.report(path, "the setting holds a value outside its domain, so its declared default is used", fmt.Sprint(v))
		return fallback
	}
	r.report(path, "", "")
	return v
}

// report writes the Error line of a setting read in a way other than as it says,
// once per distinct fault.
func (r *reader) report(path, problem, detail string) {
	r.mu.Lock()
	last, reported := r.reported[path]
	switch {
	case problem == "":
		delete(r.reported, path)
	case !reported || last != detail:
		r.reported[path] = detail
	default:
		problem = ""
	}
	r.mu.Unlock()
	if problem != "" && r.logger != nil {
		r.logger.Error(ModuleName+": "+problem, slog.String("setting", path))
	}
}

// gateConfig is the part of the gateway's section this module reads: the public
// URL the redirect targets are built from and the origins a return_to may name,
// plus whether insecure use was allowed, which is what drops the __Host- prefix
// and the Secure attribute from the flow cookie.
type gateConfig struct {
	Mount struct {
		PublicURL     string `config:"public-url"`
		AllowInsecure bool   `config:"allow-insecure"`
	} `config:"mount"`
	TrustedOrigins []string `config:"trusted-origins"`
}

// resolveConfig is the part of the tenant resolution module's configuration the
// startup check reads: which endpoints it guards. The shape is read as a list of
// names; a value of any other shape reads as unreadable and only a Warn is
// written.
type resolveConfig struct {
	Endpoints []string `config:"endpoints"`
}

// readGate reads the gateway keys this module needs. A missing reader leaves the
// public URL empty and trusts no origin, which the return_to check states.
func readGate(reader config.Reader) (gateConfig, error) {
	var g gateConfig
	if reader == nil {
		return g, nil
	}
	if err := reader.Decode("authn.gate", &g); err != nil {
		return gateConfig{}, err
	}
	return g, nil
}

// readResolveEndpoints reads the endpoints the tenant resolution layer guards.
// ok is false when the shape cannot be read, which the startup check turns into
// a Warn and never a failure.
func readResolveEndpoints(reader config.Reader) (endpoints []string, ok bool) {
	if reader == nil {
		return nil, false
	}
	var r resolveConfig
	if err := reader.Decode("tenant.resolve", &r); err != nil {
		return nil, false
	}
	if r.Endpoints == nil {
		return nil, false
	}
	return r.Endpoints, true
}

// checkReturnTo holds a return target to the rule the engine applies at the
// start of a flow. An empty value is refused except where the caller has a
// default of its own: the caller substitutes its default and calls again.
func checkReturnTo(value string, trusted []string) error {
	if value == "" {
		return fmt.Errorf("%w: the return target is empty", authn.ErrInvalidArgument)
	}
	if len(value) > maxReturnToBytes {
		return fmt.Errorf("%w: the return target is longer than %d bytes", authn.ErrInvalidArgument, maxReturnToBytes)
	}
	if strings.HasPrefix(value, "/") {
		if relativeReturnTo(value) {
			return nil
		}
		return fmt.Errorf("%w: the return target is not a well-formed relative path", authn.ErrInvalidArgument)
	}
	origin, ok := originOf(value)
	if !ok {
		return fmt.Errorf("%w: the return target is neither a relative path nor an absolute URL", authn.ErrInvalidArgument)
	}
	// An absolute URL is accepted only when its origin is one the deployment
	// trusts, which keeps a login from being turned into an open redirect. The
	// comparison is by origin, so a path, query or fragment under a trusted
	// origin is accepted too.
	if slices.Contains(trusted, origin) {
		return nil
	}
	return fmt.Errorf("%w: the return target's origin is not a trusted origin", authn.ErrInvalidArgument)
}

// originOf returns the canonical origin of an absolute URL, with its path, query
// and fragment dropped. ok is false for a value that is not an absolute http or
// https URL.
func originOf(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Opaque != "" {
		return "", false
	}
	bare := *u
	bare.Path, bare.RawPath = "", ""
	bare.RawQuery, bare.ForceQuery = "", false
	bare.Fragment, bare.RawFragment = "", ""
	origin, err := mount.Origin(bare.String())
	if err != nil || origin == "" {
		return "", false
	}
	return origin, true
}

// relativeReturnTo reports whether a value beginning with a slash is a
// well-formed relative path: a single leading slash, no scheme-relative "//",
// no backslash, no control character and no "@".
func relativeReturnTo(value string) bool {
	if !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return false
	}
	for i := 0; i < len(value); i++ {
		ch := value[i]
		switch {
		case ch == '\\' || ch == '@':
			return false
		case ch < 0x20 || ch == 0x7f:
			return false
		}
	}
	return true
}
