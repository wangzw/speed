// Package sms carries the SMS login method, which proves ownership of a phone number by a verification code it received. It holds no challenge state of its own.
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

const methodName = "sms"

const (
	stepPhone = "phone"
	stepCode  = "code"

	fieldPhone = "phone"
	fieldCode  = "code"
)

var phoneShape = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)

const stateVersion = 1

const (
	signupPath   = "authn.account.signup"
	signupOpen   = "open"
	signupClosed = "closed"
)

type method struct {
	codes    authn.SMSCodes
	throttle authn.Throttle
	accounts authn.Accounts
	store    settings.Store
	// logger writes the one record this method makes: a read of the switch that
	// failed, reported once for as long as the same fault stands.
	logger *slog.Logger

	mu       sync.Mutex
	reported string
}

var _ authn.LoginMethod = (*method)(nil)

func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers. SMSCodes, Throttle and Accounts are required, while settings and logging are optional.
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

func (m *method) Name() string { return methodName }

func (m *method) Describe() authn.MethodDescriptor {
	return authn.MethodDescriptor{
		Name:         methodName,
		Kind:         authn.MethodKindForm,
		Fields:       descriptorFields(),
		OpensAccount: true,
	}
}

func (m *method) Begin(ctx context.Context, s authn.Start) (authn.Step, error) {
	if !servesPurpose(s.Purpose) {
		return refusePurpose(s.Purpose), nil
	}
	return m.phone(ctx, s.Fields, s.SendsLeft, s.Locale, s.Client.IP, s.Proof, s.Purpose, s.Account), nil
}

func (m *method) Answer(ctx context.Context, f authn.FlowState, in authn.StepInput) (authn.Step, error) {
	if in.Action != authn.InputSubmit {
		return authn.StepRefused(errNotSubmitted), nil
	}
	if !servesPurpose(f.Purpose) {
		return refusePurpose(f.Purpose), nil
	}
	switch f.Step {
	case stepPhone:
		return m.phone(ctx, in.Fields, f.SendsLeft, f.Locale, in.Client.IP, f.Proof, f.Purpose, f.Account), nil
	case stepCode:
		return m.code(ctx, f, in.Fields), nil
	default:
		return authn.StepRefused(errUnknownStep), nil
	}
}

func servesPurpose(purpose string) bool {
	return purpose == authn.PurposeLogin || purpose == authn.PurposeStepUp
}

var (
	errRefused = fmt.Errorf("authn.login.sms: %w", authn.ErrInvalidCredentials)

	errNotSubmitted = errors.New("authn.login.sms: the answer is not a submitted form")
	errUnknownStep  = errors.New("authn.login.sms: the flow is at a step this method did not name")
	errBadState     = errors.New("authn.login.sms: the flow carries no challenge")
)

func refusePurpose(purpose string) authn.Step {
	return authn.StepRefused(fmt.Errorf("authn.login.sms: purpose %q is not served", purpose))
}

func (m *method) phone(ctx context.Context, fields map[string]string, sendsLeft int, locale, source, proof, purpose string, account authn.AccountID) authn.Step {
	phone := fields[fieldPhone]
	if phone == "" {
		return promptPhone()
	}
	if !phoneShape.MatchString(phone) {
		return authn.StepRefused(fmt.Errorf("%w: the phone is not an E.164 number", authn.ErrInvalidIdentifier))
	}
	if purpose == authn.PurposeStepUp {
		held, err := m.provedPhone(ctx, account, phone)
		if err != nil {
			return unavailable("reading the account's phone numbers", err)
		}
		if !held {
			return authn.StepRefused(errRefused)
		}
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
	case errors.Is(err, authn.ErrHumanVerificationRequired), errors.Is(err, authn.ErrHumanProofTooLong):
		return authn.StepRefused(fmt.Errorf("authn.login.sms: %w", err))
	case errors.Is(err, authn.ErrUnsupportedRegion):
		return authn.StepRefused(fmt.Errorf("authn.login.sms: %w", authn.ErrUnsupportedRegion))
	case errors.Is(err, authn.ErrThrottled):
		return authn.StepThrottled(0)
	default:
		return unavailable("sending a code", err)
	}
}

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

func (m *method) provedPhone(ctx context.Context, account authn.AccountID, phone string) (bool, error) {
	if account == "" {
		return false, nil
	}
	held, err := m.accounts.Identifiers(ctx, account)
	if err != nil {
		return false, err
	}
	for _, identifier := range held {
		proves := authn.Verification{Via: identifier.VerifiedVia}.Proves()
		if identifier.Kind == authn.IdentifierPhone && identifier.Value == phone &&
			!identifier.VerifiedAt.IsZero() && proves {
			return true, nil
		}
	}
	return false, nil
}

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

func unavailable(doing string, err error) authn.Step {
	return authn.StepUnavailable(fmt.Errorf("authn.login.sms: %s: %w", doing, err))
}

func descriptorFields() []authn.Field {
	return []authn.Field{
		{Name: fieldPhone, Kind: authn.FieldPhone, Required: true},
	}
}

func promptPhone() authn.Step {
	return authn.StepPrompt(stepPhone, nil, descriptorFields()...)
}

func promptCode(private []byte) authn.Step {
	return authn.StepPrompt(stepCode, private,
		authn.Field{Name: fieldCode, Kind: authn.FieldCode, Required: true, Secret: true},
	)
}

func phoneThrottleKey(phone string) string {
	sum := sha256.Sum256([]byte(phone))
	return hex.EncodeToString(sum[:])
}

type state struct {
	Version   int    `json:"v"`
	Phone     string `json:"phone"`
	Challenge string `json:"challenge"`
}

func writeState(phone, challenge string) []byte {
	b, _ := json.Marshal(state{Version: stateVersion, Phone: phone, Challenge: challenge})
	return b
}

func readState(private []byte) (state, bool) {
	var s state
	dec := json.NewDecoder(bytes.NewReader(private))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil || s.Version != stateVersion || s.Phone == "" || s.Challenge == "" || dec.More() {
		return state{}, false
	}
	return s, true
}
