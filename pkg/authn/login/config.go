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

const (
	flowTTLKey                = "flow-ttl"
	callBudgetKey             = "call-budget"
	defaultReturnToKey        = "default-return-to"
	callbackErrorToKey        = "callback-error-return-to"
	disabledMethodsKey        = "disabled-methods"
	deliveryModesKey          = "delivery-modes"
	ipLimitKey                = "ip-limit"
	passwordResetKey          = "password-reset"
	passwordResetTicketTTLKey = "password-reset-ticket-ttl"
	emailCodeTTLKey           = "email-code-ttl"
	emailResendKey            = "email-resend-interval"
	emailPerAddressHourlyKey  = "email-per-address-hourly"
	emailPerAddressDailyKey   = "email-per-address-daily"
	emailPerAccountHourlyKey  = "email-per-account-hourly"
	emailPerAccountDailyKey   = "email-per-account-daily"
	emailPerSourceHourlyKey   = "email-per-source-hourly"
	emailPerSourceDailyKey    = "email-per-source-daily"
	emailGlobalHourlyKey      = "email-global-hourly"

	disabledMethodsPath        = ModuleName + "." + disabledMethodsKey
	deliveryModesPath          = ModuleName + "." + deliveryModesKey
	flowTTLPath                = ModuleName + "." + flowTTLKey
	ipLimitPath                = ModuleName + "." + ipLimitKey
	passwordResetPath          = ModuleName + "." + passwordResetKey
	passwordResetTicketTTLPath = ModuleName + "." + passwordResetTicketTTLKey

	passwordSignupKey     = "password-signup"
	signupIPLimitKey      = "signup-ip-limit"
	signupGlobalHourlyKey = "signup-global-hourly"
	accountNamespace      = "authn.account"

	passwordSignupPath     = accountNamespace + "." + passwordSignupKey
	signupIPLimitPath      = accountNamespace + "." + signupIPLimitKey
	signupGlobalHourlyPath = accountNamespace + "." + signupGlobalHourlyKey
)

const (
	minFlowTTL = time.Minute
	maxFlowTTL = 30 * time.Minute

	minCallBudget = time.Second
	maxCallBudget = 60 * time.Second

	minPasswordResetTicketTTL = time.Minute
	maxPasswordResetTicketTTL = 30 * time.Minute

	maxIPLimit = 10000

	minEmailCodeTTL = time.Minute
	maxEmailCodeTTL = 30 * time.Minute

	minEmailResend = 30 * time.Second
	maxEmailResend = 10 * time.Minute

	minEmailPerAddressHourly = 1
	maxEmailPerAddressHourly = 60

	minEmailPerAddressDaily = 1
	maxEmailPerAddressDaily = 200

	minEmailPerAccountHourly = 1
	maxEmailPerAccountHourly = 60

	minEmailPerAccountDaily = 1
	maxEmailPerAccountDaily = 1000

	minEmailPerSourceHourly = 1
	maxEmailPerSourceHourly = 10000

	minEmailPerSourceDaily = 1
	maxEmailPerSourceDaily = 100000

	minEmailGlobalHourly = 1
	maxEmailGlobalHourly = 1000000
)

const (
	deliverySession = "session"
	deliveryToken   = "token"

	maxReturnToBytes = 2048
)

const (
	signupPath = "authn.account.signup"

	signupClosed = "closed"
	signupOpen   = "open"
)

func signupInDomain(v string) bool { return v == signupClosed || v == signupOpen }

const (
	passwordResetClosed = "closed"
	passwordResetOpen   = "open"
)

func passwordResetInDomain(v string) bool { return v == passwordResetClosed || v == passwordResetOpen }

func passwordResetTicketTTLInDomain(d time.Duration) bool {
	return d >= minPasswordResetTicketTTL && d <= maxPasswordResetTicketTTL
}

const (
	signupIPLimitCeil         = 10000
	signupGlobalHourlyFloor   = 1
	signupGlobalHourlyCeil    = 1000000
	signupIPLimitDefault      = 10
	signupGlobalHourlyDefault = 100
)

func signupIPLimitInDomain(n int) bool { return n >= 0 && n <= signupIPLimitCeil }
func signupGlobalHourlyInDomain(n int) bool {
	return n >= signupGlobalHourlyFloor && n <= signupGlobalHourlyCeil
}

type options struct {
	FlowTTL                time.Duration `config:"flow-ttl"`
	CallBudget             time.Duration `config:"call-budget"`
	DefaultReturnTo        string        `config:"default-return-to"`
	CallbackErrorTo        string        `config:"callback-error-return-to"`
	DisabledMethods        string        `config:"disabled-methods"`
	DeliveryModes          string        `config:"delivery-modes"`
	IPLimit                int           `config:"ip-limit"`
	PasswordReset          string        `config:"password-reset"`
	PasswordResetTicketTTL time.Duration `config:"password-reset-ticket-ttl"`
	EmailCodeTTL           time.Duration `config:"email-code-ttl"`
	EmailResend            time.Duration `config:"email-resend-interval"`

	EmailPerAddressHourly int `config:"email-per-address-hourly"`
	EmailPerAddressDaily  int `config:"email-per-address-daily"`
	EmailPerAccountHourly int `config:"email-per-account-hourly"`
	EmailPerAccountDaily  int `config:"email-per-account-daily"`
	EmailPerSourceHourly  int `config:"email-per-source-hourly"`
	EmailPerSourceDaily   int `config:"email-per-source-daily"`
	EmailGlobalHourly     int `config:"email-global-hourly"`
}

// defaults is each setting's declared default; a failed tuning read falls back to it, except the mandatory disabled-methods switch.
var defaults = options{
	FlowTTL:                10 * time.Minute,
	CallBudget:             10 * time.Second,
	DefaultReturnTo:        "/",
	CallbackErrorTo:        "/",
	DisabledMethods:        "[]",
	DeliveryModes:          `["session","token"]`,
	IPLimit:                60,
	PasswordReset:          passwordResetClosed,
	PasswordResetTicketTTL: 10 * time.Minute,
	EmailCodeTTL:           10 * time.Minute,
	EmailResend:            60 * time.Second,

	EmailPerAddressHourly: 5,
	EmailPerAddressDaily:  10,
	EmailPerAccountHourly: 10,
	EmailPerAccountDaily:  100,
	EmailPerSourceHourly:  30,
	EmailPerSourceDaily:   100,
	EmailGlobalHourly:     300,
}

func flowTTLInDomain(d time.Duration) bool      { return d >= minFlowTTL && d <= maxFlowTTL }
func callBudgetInDomain(d time.Duration) bool   { return d >= minCallBudget && d <= maxCallBudget }
func ipLimitInDomain(n int) bool                { return n >= 0 && n <= maxIPLimit }
func emailCodeTTLInDomain(d time.Duration) bool { return d >= minEmailCodeTTL && d <= maxEmailCodeTTL }

func emailResendInDomain(d time.Duration) bool { return d >= minEmailResend && d <= maxEmailResend }

func emailPerAddressHourlyInDomain(n int) bool {
	return n >= minEmailPerAddressHourly && n <= maxEmailPerAddressHourly
}

func emailPerAddressDailyInDomain(n int) bool {
	return n >= minEmailPerAddressDaily && n <= maxEmailPerAddressDaily
}

func emailPerAccountHourlyInDomain(n int) bool {
	return n >= minEmailPerAccountHourly && n <= maxEmailPerAccountHourly
}

func emailPerAccountDailyInDomain(n int) bool {
	return n >= minEmailPerAccountDaily && n <= maxEmailPerAccountDaily
}

func emailPerSourceHourlyInDomain(n int) bool {
	return n >= minEmailPerSourceHourly && n <= maxEmailPerSourceHourly
}

func emailPerSourceDailyInDomain(n int) bool {
	return n >= minEmailPerSourceDaily && n <= maxEmailPerSourceDaily
}

func emailGlobalHourlyInDomain(n int) bool {
	return n >= minEmailGlobalHourly && n <= maxEmailGlobalHourly
}

const methodPassword = "password"

const methodSMS = "sms"

func deliveryModesTextInDomain(text string) bool {
	modes, err := parseList(text)
	if err != nil {
		return false
	}
	return deliveryModesInDomain(modes)
}

func disabledMethodsTextInDomain(text string) bool {
	list, err := parseList(text)
	if err != nil {
		return false
	}
	_, dropped := withoutPassword(list)
	return !dropped
}

// withoutPassword removes the password method from every read of the switch, so a value written around the settings surface cannot turn it off.
func withoutPassword(list []string) ([]string, bool) {
	if !slices.Contains(list, methodPassword) {
		return list, false
	}
	kept := make([]string, 0, len(list))
	for _, name := range list {
		if name != methodPassword {
			kept = append(kept, name)
		}
	}
	return kept, true
}

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
			callbackErrorToKey: {
				Description: "where a failed callback lands the user agent when the callback is a top-level " +
					"browser navigation, carrying the problem code in the URL fragment as " +
					"authn_error; a callback of a purpose other than login lands on its own return target " +
					"instead, and a request that is not a navigation is answered the problem document",
			},
			disabledMethodsKey: {
				Description: "the login methods turned off at run time, by name, as a JSON array of strings; " +
					"the password method is not among them and a write naming it is refused",
				Domain: settings.DomainOf("a JSON array of login method names other than password",
					disabledMethodsTextInDomain),
			},
			deliveryModesKey: {
				Description: "the delivery modes a login may ask for, as a JSON array of a non-empty subset " +
					"of session and token",
				Domain: settings.DomainOf("a JSON array of a non-empty subset of session and token",
					deliveryModesTextInDomain),
			},
			ipLimitKey: {
				Description: "a coarse cap on the verification messages one source address may trigger per " +
					"minute, zero turning the layer off",
				Domain: settings.DomainOf("whole numbers from 0 to 10000", ipLimitInDomain),
			},
			passwordResetKey: {
				Description: "whether the anonymous password reset is open: closed or open; open relaxes a " +
					"check",
				Domain: settings.DomainOf("closed or open", passwordResetInDomain),
			},
			passwordResetTicketTTLKey: {
				Description: "how long a proved anonymous password reset may wait before the new password is " +
					"written, from " + minPasswordResetTicketTTL.String() + " to " +
					maxPasswordResetTicketTTL.String(),
				Domain: settings.DomainOf("durations from "+minPasswordResetTicketTTL.String()+" to "+
					maxPasswordResetTicketTTL.String(), passwordResetTicketTTLInDomain),
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
			emailPerAddressHourlyKey: {
				Description: "how many email verification codes one normalized address may be sent in an hour, " +
					"across sessions and accounts",
				Domain: settings.DomainOf("whole numbers from "+
					fmt.Sprint(minEmailPerAddressHourly)+" to "+fmt.Sprint(maxEmailPerAddressHourly),
					emailPerAddressHourlyInDomain),
			},
			emailPerAddressDailyKey: {
				Description: "how many email verification codes one normalized address may be sent in a day, " +
					"across sessions and accounts",
				Domain: settings.DomainOf("whole numbers from "+
					fmt.Sprint(minEmailPerAddressDaily)+" to "+fmt.Sprint(maxEmailPerAddressDaily),
					emailPerAddressDailyInDomain),
			},
			emailPerAccountHourlyKey: {
				Description: "how many email verification codes one account may ask for in an hour, whatever " +
					"addresses it names",
				Domain: settings.DomainOf("whole numbers from "+
					fmt.Sprint(minEmailPerAccountHourly)+" to "+fmt.Sprint(maxEmailPerAccountHourly),
					emailPerAccountHourlyInDomain),
			},
			emailPerAccountDailyKey: {
				Description: "how many email verification codes one account may ask for in a UTC day, " +
					"whatever addresses it names",
				Domain: settings.DomainOf("whole numbers from "+
					fmt.Sprint(minEmailPerAccountDaily)+" to "+fmt.Sprint(maxEmailPerAccountDaily),
					emailPerAccountDailyInDomain),
			},
			emailPerSourceHourlyKey: {
				Description: "how many email verification codes one request source address may ask for in an " +
					"hour, whatever addresses, accounts or sessions it names",
				Domain: settings.DomainOf("whole numbers from "+
					fmt.Sprint(minEmailPerSourceHourly)+" to "+fmt.Sprint(maxEmailPerSourceHourly),
					emailPerSourceHourlyInDomain),
			},
			emailPerSourceDailyKey: {
				Description: "how many email verification codes one request source address may ask for in a " +
					"UTC day, whatever addresses, accounts or sessions it names",
				Domain: settings.DomainOf("whole numbers from "+
					fmt.Sprint(minEmailPerSourceDaily)+" to "+fmt.Sprint(maxEmailPerSourceDaily),
					emailPerSourceDailyInDomain),
			},
			emailGlobalHourlyKey: {
				Description: "how many email verification codes the whole deployment may send in an hour, " +
					"across every account and address",
				Domain: settings.DomainOf("whole numbers from "+
					fmt.Sprint(minEmailGlobalHourly)+" to "+fmt.Sprint(maxEmailGlobalHourly),
					emailGlobalHourlyInDomain),
			},
		},
	}
}

type reader struct {
	store  settings.Store
	logger *slog.Logger

	mu       sync.Mutex
	reported map[string]string
}

func newReader(store settings.Store, logger *slog.Logger) *reader {
	return &reader{store: store, logger: logger, reported: make(map[string]string)}
}

var errSettingRead = errors.New("the setting cannot be read")

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
	kept, dropped := withoutPassword(list)
	if dropped {
		r.report(disabledMethodsPath,
			"the switch of the disabled methods names password, which the deployment cannot turn off, so it is ignored", text)
	} else {
		r.report(disabledMethodsPath, "", "")
	}
	return kept, nil
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

func (r *reader) callbackErrorReturnTo() string {
	if r.store == nil {
		return defaults.CallbackErrorTo
	}
	var v string
	if err := r.store.Get(settings.Global(), ModuleName+"."+callbackErrorToKey, &v); err != nil {
		r.report(ModuleName+"."+callbackErrorToKey, "the callback failure landing cannot be read, so its declared default is used", err.Error())
		return defaults.CallbackErrorTo
	}
	r.report(ModuleName+"."+callbackErrorToKey, "", "")
	if v == "" {
		return defaults.CallbackErrorTo
	}
	return v
}

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

func (r *reader) passwordReset() string {
	return readTuned(r, passwordResetPath, passwordResetClosed, passwordResetInDomain)
}

func (r *reader) passwordResetTicketTTL() time.Duration {
	return readTuned(r, passwordResetTicketTTLPath, defaults.PasswordResetTicketTTL,
		passwordResetTicketTTLInDomain)
}

func (r *reader) signup() string {
	return readTuned(r, signupPath, signupClosed, signupInDomain)
}

func (r *reader) passwordSignup() string {
	return readTuned(r, passwordSignupPath, signupClosed, signupInDomain)
}

func (r *reader) signupIPLimit() int {
	return readTuned(r, signupIPLimitPath, signupIPLimitDefault, signupIPLimitInDomain)
}

func (r *reader) signupGlobalHourly() int {
	return readTuned(r, signupGlobalHourlyPath, signupGlobalHourlyDefault, signupGlobalHourlyInDomain)
}

func (r *reader) emailCodeTTL() time.Duration {
	return readTuned(r, ModuleName+"."+emailCodeTTLKey, defaults.EmailCodeTTL, emailCodeTTLInDomain)
}

func (r *reader) emailResend() time.Duration {
	return readTuned(r, ModuleName+"."+emailResendKey, defaults.EmailResend, emailResendInDomain)
}

func (r *reader) emailPerAddressHourly() int {
	return readTuned(r, ModuleName+"."+emailPerAddressHourlyKey,
		defaults.EmailPerAddressHourly, emailPerAddressHourlyInDomain)
}

func (r *reader) emailPerAddressDaily() int {
	return readTuned(r, ModuleName+"."+emailPerAddressDailyKey,
		defaults.EmailPerAddressDaily, emailPerAddressDailyInDomain)
}

func (r *reader) emailPerAccountHourly() int {
	return readTuned(r, ModuleName+"."+emailPerAccountHourlyKey,
		defaults.EmailPerAccountHourly, emailPerAccountHourlyInDomain)
}

func (r *reader) emailPerAccountDaily() int {
	return readTuned(r, ModuleName+"."+emailPerAccountDailyKey,
		defaults.EmailPerAccountDaily, emailPerAccountDailyInDomain)
}

func (r *reader) emailPerSourceHourly() int {
	return readTuned(r, ModuleName+"."+emailPerSourceHourlyKey,
		defaults.EmailPerSourceHourly, emailPerSourceHourlyInDomain)
}

func (r *reader) emailPerSourceDaily() int {
	return readTuned(r, ModuleName+"."+emailPerSourceDailyKey,
		defaults.EmailPerSourceDaily, emailPerSourceDailyInDomain)
}

func (r *reader) emailGlobalHourly() int {
	return readTuned(r, ModuleName+"."+emailGlobalHourlyKey,
		defaults.EmailGlobalHourly, emailGlobalHourlyInDomain)
}

func (r *reader) smsCodeTTL() time.Duration {
	const (
		path = "authn.sms.code-ttl"
		def  = 5 * time.Minute
		lo   = time.Minute
		hi   = 10 * time.Minute
	)
	return readTuned(r, path, def, func(d time.Duration) bool { return d >= lo && d <= hi })
}

func (r *reader) smsResend() time.Duration {
	const (
		path = "authn.sms.resend-interval"
		def  = 60 * time.Second
		lo   = 30 * time.Second
		hi   = 10 * time.Minute
	)
	return readTuned(r, path, def, func(d time.Duration) bool { return d >= lo && d <= hi })
}

func (r *reader) recentWindow() time.Duration {
	const (
		path = "authn.mfa.recent-window"
		def  = 10 * time.Minute
		lo   = time.Minute
		hi   = time.Hour
	)
	return readTuned(r, path, def, func(d time.Duration) bool { return d >= lo && d <= hi })
}

func (r *reader) perFlowSends() int {
	const path = "authn.sms.per-flow-sends"
	if r.store == nil {
		return defaultPerFlowSends
	}
	v, err := settings.Get[int](r.store, settings.Global(), path)
	if errors.Is(err, settings.ErrUnknownKey) {
		r.report(path, "", "")
		return defaultPerFlowSends
	}
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

const (
	defaultPerFlowSends = 3
	minPerFlowSends     = 1
	maxPerFlowSends     = 10
)

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

type gateConfig struct {
	Mount struct {
		PublicURL string `config:"public-url"`
	} `config:"mount"`
	TrustedOrigins []string `config:"trusted-origins"`
}

type resolveConfig struct {
	Endpoints []string `config:"endpoints"`
}

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
	if slices.Contains(trusted, origin) {
		return nil
	}
	return fmt.Errorf("%w: the return target's origin is not a trusted origin", authn.ErrInvalidArgument)
}

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
