// Package sms carries the SMS login method: the first factor that proves the
// ownership of a phone number by a verification code it received.
//
// The method holds no challenge state of its own. The allowed-prefix table, the
// budgets, the challenge row and the check of a code are the SMS module's, which
// the method takes through authn.SMSCodes; the method answers a Step and does
// nothing else. It reads the number's back-off before it checks a code, counts
// the outcome against the same key, and says what the login engine is to do
// next. It touches no HTTP, session, token or flow record. The engine logs the
// cause of an answer; the method itself writes one Error when the registration
// switch cannot be read or holds a value outside its domain, so a fault is
// distinguishable from registration being closed, and no record here names a
// phone number or a code.
//
// A code is sent to a number registered to an enabled account, or to any number
// while registration is open; every other number gets a suppressed challenge,
// which is counted against every budget but the deployment-wide one and answered
// exactly as a real one, so that the answer tells nobody whether a number is
// registered. A code that holds completes a login only for a number no account
// holds or one its account chose as a sign-in way; an unchosen number is refused
// as a code that did not hold.
//
// A host takes the method in by writing ModuleName into a command alongside the
// login engine, an SMS module and an account module. It delivers the login
// method capability non-exclusively, so it stands beside the other methods.
package sms

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sync"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
	smssend "github.com/vislake/speed/pkg/sms"
)

// ModuleName is this module's name in the registry.
const ModuleName = "authn.login.sms"

// methodName is the name this method reports. It is a constant of the code: it
// appears in routes, flow records and metric attributes, and a published name
// never changes.
const methodName = "sms"

// The method's own names for its two steps, which the engine keeps in the flow
// record and hands back as FlowState.Step, and for the fields it asks for.
const (
	stepPhone = "phone"
	stepCode  = "code"

	fieldPhone = "phone"
	fieldCode  = "code"
)

// phoneShape is E.164: a plus sign, a country code that does not begin with zero
// and at most fifteen digits in all. The number is not parsed and not completed;
// the client writes it in international form, as the account module stores it.
var phoneShape = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)

// stateVersion is the version of the state kept between the step that sent a
// code and the step that answers it.
const stateVersion = 1

// signupPath is the account module's registration switch, which this method
// reads and does not declare. signupOpen is the value that lets a first login
// create an account, and signupClosed the strict one; any other value is outside
// the declared domain.
const (
	signupPath   = "authn.account.signup"
	signupOpen   = "open"
	signupClosed = "closed"
)

// method is this module's product.
type method struct {
	codes    authn.SMSCodes
	throttle authn.Throttle
	accounts authn.Accounts
	// store is the settings store the registration switch is read from, nil
	// when the assembly carries none.
	store settings.Store
	// logger writes the one record this method makes: a read of the switch that
	// failed, reported once for as long as the same fault stands.
	logger *slog.Logger

	mu       sync.Mutex
	reported string
}

// method must satisfy the capability it delivers.
var _ authn.LoginMethod = (*method)(nil)

// init registers this module with the process registry.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// SMSCodes, Throttle and Accounts are required: each is delivered exclusively by
// another module, so a command that took this method in without an SMS module
// and an account module fails before anything is built. The settings store is
// optional and only read, which is where the account module's registration
// switch lives; without one the switch reads as closed. The logging capability
// is optional and declared so that it is constructed first; without one the
// method logs through the process default. The login method capability is
// delivered without being claimed, which is what lets several methods stand side
// by side. The module declares no input item.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*authn.SMSCodes)(nil)},
			{Token: (*authn.Throttle)(nil)},
			{Token: (*authn.Accounts)(nil)},
			{Token: (*settings.Store)(nil), Optional: true},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Provides: []core.Provision{{Token: (*authn.LoginMethod)(nil)}},

		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			off, reason, err := smssend.OffBySelection(reg)
			if err != nil {
				return core.Enablement{}, fmt.Errorf("%s: reading the SMS carrier selection: %w", ModuleName, err)
			}
			if off {
				return core.Enablement{State: core.StateDisabled, Reason: reason}, nil
			}
			missing, reason, err := smssend.OffByMissingTemplates(reg)
			if err != nil {
				return core.Enablement{}, fmt.Errorf("%s: reading the SMS template set: %w", ModuleName, err)
			}
			if missing {
				return core.Enablement{State: core.StateDisabled, Reason: reason}, nil
			}
			return core.Enablement{State: core.StateEnabled}, nil
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			codes, err := core.Resolve[authn.SMSCodes](reg)
			if err != nil {
				return nil, fmt.Errorf("%s: taking up the SMS code capability: %w", ModuleName, err)
			}
			throttle, err := core.Resolve[authn.Throttle](reg)
			if err != nil {
				return nil, fmt.Errorf("%s: taking up the back-off capability: %w", ModuleName, err)
			}
			accounts, err := core.Resolve[authn.Accounts](reg)
			if err != nil {
				return nil, fmt.Errorf("%s: taking up the account read capability: %w", ModuleName, err)
			}
			store, err := optionalStore(reg)
			if err != nil {
				return nil, err
			}
			logger, err := optionalLogger(reg)
			if err != nil {
				return nil, err
			}
			return &method{codes: codes, throttle: throttle, accounts: accounts, store: store, logger: logger}, nil
		},
	}
}

// optionalLogger takes up the logging capability, whose absence is a legal
// assembly; the process default stands in for it.
func optionalLogger(reg *core.Registry) (*slog.Logger, error) {
	delivered, err := core.Resolve[log.Logger](reg)
	switch {
	case err == nil:
		return delivered.Named(ModuleName), nil
	case errors.Is(err, core.ErrMissingProvider):
		return log.Default(), nil
	default:
		return nil, fmt.Errorf("%s: taking up the logging capability: %w", ModuleName, err)
	}
}

// optionalStore takes up the settings store, whose absence is a legal assembly.
func optionalStore(reg *core.Registry) (settings.Store, error) {
	store, err := core.Resolve[settings.Store](reg)
	switch {
	case err == nil:
		return store, nil
	case errors.Is(err, core.ErrMissingProvider):
		return nil, nil
	default:
		return nil, fmt.Errorf("%s: taking up the settings store: %w", ModuleName, err)
	}
}

// Name is the method's stable name.
func (m *method) Name() string { return methodName }

// Describe reports the SMS method: a form whose first step asks for a phone
// number. A code sent to a number nobody holds opens an account while the
// account module's registration switch is open.
func (m *method) Describe() authn.MethodDescriptor {
	return authn.MethodDescriptor{
		Name:         methodName,
		Kind:         authn.MethodKindForm,
		Fields:       descriptorFields(),
		OpensAccount: true,
	}
}

// Begin starts a flow. The start request's own phone field is the number when the
// client sent it; otherwise the method asks for it.
func (m *method) Begin(ctx context.Context, s authn.Start) (authn.Step, error) {
	if !servesPurpose(s.Purpose) {
		return refusePurpose(s.Purpose), nil
	}
	return m.phone(ctx, s.Fields, s.SendsLeft, s.Locale, s.Client.IP, s.Proof), nil
}

// Answer handles the client's answer to the previous step: the phone the method
// asked for, or the code a sent message carries. A callback and a step the
// method never named are refused, so that a flow record this method did not write
// cannot be walked into a proof.
func (m *method) Answer(ctx context.Context, f authn.FlowState, in authn.StepInput) (authn.Step, error) {
	if in.Action != authn.InputSubmit {
		return authn.StepRefused(errNotSubmitted), nil
	}
	if !servesPurpose(f.Purpose) {
		return refusePurpose(f.Purpose), nil
	}
	switch f.Step {
	case stepPhone:
		return m.phone(ctx, in.Fields, f.SendsLeft, f.Locale, in.Client.IP, f.Proof), nil
	case stepCode:
		return m.code(ctx, f, in.Fields), nil
	default:
		return authn.StepRefused(errUnknownStep), nil
	}
}

// servesPurpose reports whether the method answers a flow of this purpose. An SMS
// code proves the ownership of a phone number, which is what a login and a
// step-up need from a first factor; a link binds an external identity and an
// enroll registers a factor, and neither is something a code proves. The zero
// purpose is none.
func servesPurpose(purpose string) bool {
	return purpose == authn.PurposeLogin || purpose == authn.PurposeStepUp
}

// The causes the method puts on an answer. None carries a value the client sent.
var (
	// errRefused is the cause of every refusal of a code, whatever made it fail:
	// a wrong code, a code that expired or was used, and a suppressed challenge
	// all read the same here, in the log as on the wire.
	errRefused = fmt.Errorf("authn.login.sms: %w", authn.ErrInvalidCredentials)

	errNotSubmitted = errors.New("authn.login.sms: the answer is not a submitted form")
	errUnknownStep  = errors.New("authn.login.sms: the flow is at a step this method did not name")
	errBadState     = errors.New("authn.login.sms: the flow carries no challenge")
)

// refusePurpose refuses a flow of a purpose the method does not serve, before it
// reads or sends anything.
func refusePurpose(purpose string) authn.Step {
	return authn.StepRefused(fmt.Errorf("authn.login.sms: purpose %q is not served", purpose))
}

// phone sends a code to the number the client gave and asks for that code, or
// asks for the number when none was given. It checks the per-flow send budget
// before it sends, so a flow that has spent it is answered without a send. The
// number the code was sent to and the challenge that carries it are kept in the
// private bytes, so that the answer step needs neither from the client. source
// is the request's client address, passed to the code engine as the send's
// source; an empty one means the engine has no trustworthy origin and the code
// engine applies no source budget. proof is the human-verification proof the
// request presented; it travels to the code engine, which checks and consumes
// it once before spending any budget.
func (m *method) phone(ctx context.Context, fields map[string]string, sendsLeft int, locale, source, proof string) authn.Step {
	phone := fields[fieldPhone]
	if phone == "" {
		return promptPhone()
	}
	if !phoneShape.MatchString(phone) {
		return authn.StepRefused(fmt.Errorf("%w: the phone is not an E.164 number", authn.ErrInvalidIdentifier))
	}
	if sendsLeft <= 0 {
		return authn.StepThrottled(0)
	}
	if err := ctx.Err(); err != nil {
		return authn.StepUnavailable(err)
	}

	deliver, err := m.deliver(ctx, phone)
	if err != nil {
		return unavailable("reading whether the number is registered", err)
	}
	challenge, err := m.codes.Send(ctx, authn.SendCode{
		Purpose: authn.SMSPurposeLogin,
		Phone:   phone,
		Locale:  locale,
		Deliver: deliver,
		Source:  source,
		Proof:   proof,
	})
	var throttled *authn.ThrottledError
	switch {
	case err == nil:
		return promptCode(writeState(phone, challenge)).WithSends(1)
	case errors.As(err, &throttled):
		return authn.StepThrottled(throttled.RetryAfter)
	case errors.Is(err, authn.ErrHumanVerificationRequired), errors.Is(err, authn.ErrInvalidArgument):
		// The deployment requires a proof and this send presented none, an
		// invalid one, or one over its limit. The code engine checked it before
		// spending any budget; the cause reaches the engine's answer, which is
		// the 403 or the 400 rather than a failed login.
		return authn.StepRefused(fmt.Errorf("authn.login.sms: %w", err))
	case errors.Is(err, authn.ErrUnsupportedRegion):
		// The number's prefix is not one the deployment allows. The cause reaches
		// the log, and the engine answers it 400 unsupported-region.
		return authn.StepRefused(fmt.Errorf("authn.login.sms: %w", authn.ErrUnsupportedRegion))
	case errors.Is(err, authn.ErrThrottled):
		return authn.StepThrottled(0)
	default:
		return unavailable("sending a code", err)
	}
}

// code checks the code the client answered with. It reads the number's back-off
// before it checks anything, so a number in back-off is answered without a
// comparison; a wrong code is counted against the same key, and a code that
// holds clears it. The number is not looked up for the challenge: it is the one
// the challenge was sent to, taken from the private bytes, so a suppressed
// challenge is checked exactly as a real one.
//
// A code that holds completes a login only when the number may sign in: a number
// no account holds, which a first login may open an account for, or one its
// account's holder chose as a sign-in way. A number the account holds but did not
// choose answers as a code that did not hold, so the choice is not something an
// answer tells. A step-up and an enrollment prove a number the account already
// signed in with, so the choice is not read there.
func (m *method) code(ctx context.Context, f authn.FlowState, fields map[string]string) authn.Step {
	st, ok := readState(f.Private)
	if !ok {
		return authn.StepRefused(errBadState)
	}
	code := fields[fieldCode]
	if code == "" {
		return promptCode(f.Private)
	}
	if err := ctx.Err(); err != nil {
		return authn.StepUnavailable(err)
	}

	key := phoneThrottleKey(st.Phone)
	wait, err := m.throttle.Check(ctx, authn.ScopePhone, key)
	if err != nil {
		return unavailable("reading the back-off", err)
	}
	if wait != 0 {
		return authn.StepThrottled(wait)
	}
	// The comparison is the step a guess is made at, so a call that ended while
	// the back-off was read does not go on to it.
	if done := ctx.Err(); done != nil {
		return authn.StepUnavailable(done)
	}

	switch err := m.codes.Verify(ctx, st.Challenge, code); {
	case err == nil:
		if f.Purpose == authn.PurposeLogin {
			signsIn, cerr := m.signsIn(ctx, st.Phone)
			if cerr != nil {
				return unavailable("reading whether the number signs in", cerr)
			}
			if !signsIn {
				return authn.StepRefused(errRefused)
			}
		}
		if serr := m.throttle.Succeed(ctx, authn.ScopePhone, key); serr != nil {
			return unavailable("clearing the back-off", serr)
		}
		return authn.StepProven(authn.Proof{
			Identifier: &authn.Identifier{Kind: authn.IdentifierPhone, Value: st.Phone},
			Methods:    []authn.Method{authn.MethodSMS},
		})
	case errors.Is(err, authn.ErrInvalidCredentials):
		if _, ferr := m.throttle.Fail(ctx, authn.ScopePhone, key); ferr != nil {
			return unavailable("counting the failure", ferr)
		}
		return authn.StepRefused(errRefused)
	default:
		return unavailable("checking the code", err)
	}
}

// signsIn reports whether a number may complete a login: it belongs to no
// account, which is a first login the resolution decides the outcome of, or the
// account that holds it chose it as a sign-in way. The account lookup keeps
// answering for every number an account holds, which is what the paths that only
// need the owner rely on; the choice is read from the account's identifiers.
func (m *method) signsIn(ctx context.Context, phone string) (bool, error) {
	id, err := m.accounts.Lookup(ctx, authn.IdentifierPhone, phone)
	switch {
	case errors.Is(err, authn.ErrAccountNotFound), errors.Is(err, authn.ErrInvalidIdentifier):
		return true, nil
	case err != nil:
		return false, err
	}
	if id == "" {
		return true, nil
	}
	held, err := m.accounts.Identifiers(ctx, id)
	if err != nil {
		return false, err
	}
	for _, identifier := range held {
		if identifier.Kind == authn.IdentifierPhone && identifier.Value == phone && identifier.SignIn {
			return true, nil
		}
	}
	return false, nil
}

// deliver reports whether a code is to be sent to the number rather than written
// as a suppressed challenge. A number registered to an enabled account is
// delivered; so is any number while registration is open, because a first login
// may create an account for it. A number nobody holds, one a disabled or deleted
// account holds, and one whose identifier is not a number read as not delivered.
// A failure to read is not delivered, but is answered as the capability being
// unable to say, never as a suppressed send.
func (m *method) deliver(ctx context.Context, phone string) (bool, error) {
	id, err := m.accounts.Lookup(ctx, authn.IdentifierPhone, phone)
	switch {
	case err == nil:
	case errors.Is(err, authn.ErrAccountNotFound), errors.Is(err, authn.ErrInvalidIdentifier):
		return m.signupOpen(), nil
	default:
		return false, err
	}
	account, err := m.accounts.Get(ctx, id)
	switch {
	case err == nil:
		return account.Status == authn.StatusActive || m.signupOpen(), nil
	case errors.Is(err, authn.ErrAccountNotFound):
		return m.signupOpen(), nil
	default:
		return false, err
	}
}

// signupOpen reports whether the account module's registration switch is open at
// this decision. A store that cannot be read, and an assembly without one, read
// as closed: a method that cannot tell whether registration is open sends a code
// to a known number only.
func (m *method) signupOpen() bool {
	if m.store == nil {
		return false
	}
	var value string
	if err := m.store.Get(settings.Global(), signupPath, &value); err != nil {
		m.reportFault("the setting cannot be read, so it is read as closed", err.Error())
		return false
	}
	if value != signupOpen && value != signupClosed {
		m.reportFault("the setting holds a value outside its domain, so it is read as closed", value)
		return false
	}
	m.clearFault()
	return value == signupOpen
}

// reportFault writes one Error for a switch that read as something other than
// its value, and again only when the fault changes; clearFault drops the record
// once a read comes back clean. An assembly without a settings store is a legal
// one and is not a fault.
func (m *method) reportFault(problem, detail string) {
	fault := problem + ": " + detail
	m.mu.Lock()
	if m.reported == fault {
		m.mu.Unlock()
		return
	}
	m.reported = fault
	m.mu.Unlock()

	logger := m.logger
	if logger == nil {
		logger = log.Default()
	}
	logger.Error(ModuleName+": "+problem,
		slog.String("setting", signupPath), slog.String("cause", detail))
}

func (m *method) clearFault() {
	m.mu.Lock()
	m.reported = ""
	m.mu.Unlock()
}

// unavailable answers that the method cannot say now. The dependency's error
// stays in the chain, so that the engine finds an ErrUnavailable in it, and the
// text names the step and nothing the client sent.
func unavailable(doing string, err error) authn.Step {
	return authn.StepUnavailable(fmt.Errorf("authn.login.sms: %s: %w", doing, err))
}

// descriptorFields is the one field the first step asks for, which the descriptor
// previews.
func descriptorFields() []authn.Field {
	return []authn.Field{
		{Name: fieldPhone, Kind: authn.FieldPhone, Required: true},
	}
}

// promptPhone asks for the phone number.
func promptPhone() authn.Step {
	return authn.StepPrompt(stepPhone, nil, descriptorFields()...)
}

// promptCode asks for the code a sent message carries, with the state that names
// the number the challenge was sent to and the challenge itself.
func promptCode(private []byte) authn.Step {
	return authn.StepPrompt(stepCode, private,
		authn.Field{Name: fieldCode, Kind: authn.FieldCode, Required: true, Secret: true},
	)
}

// phoneThrottleKey is the back-off key of a number: the SHA-256 of its E.164
// form, in hexadecimal, the same key the account self-service phone routes use.
// It is the number, not the account, because a login has no account yet and the
// number is the only stable thing to count against.
func phoneThrottleKey(phone string) string {
	sum := sha256.Sum256([]byte(phone))
	return hex.EncodeToString(sum[:])
}

// state is the flow state between the step that sent a code and the step that
// answers it: the number the code was sent to and the challenge that carries it.
// It holds no code and no account.
type state struct {
	Version   int    `json:"v"`
	Phone     string `json:"phone"`
	Challenge string `json:"challenge"`
}

// writeState writes the state the answer step reads. The phone and the challenge
// are the method's own, not the client's, so the engine keeps them in a record
// the client cannot write.
func writeState(phone, challenge string) []byte {
	// A struct of an int and two strings always encodes.
	b, _ := json.Marshal(state{Version: stateVersion, Phone: phone, Challenge: challenge})
	return b
}

// readState reads the state writeState wrote. Anything else, empty state
// included, names no challenge.
func readState(private []byte) (state, bool) {
	var s state
	dec := json.NewDecoder(bytes.NewReader(private))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil || s.Version != stateVersion || s.Phone == "" || s.Challenge == "" || dec.More() {
		return state{}, false
	}
	return s, true
}
