// Package sms carries the SMS second factor: the factor an account proves with
// a code sent to a phone it has already verified.
//
// The factor keeps no data of its own. It binds the account's current verified
// phone identifier, so a phone that changes moves the factor with it and no
// sub-table is needed. The code itself is the SMS code engine's: this factor
// sends one through authn.SMSCodes with the mfa purpose and checks the answer
// against it. The register row, the back-off, the audit row and the event are
// the multi-factor module's, done around every call it makes here; this factor
// only sends a code, starts a check and reads a pass or a refusal.
//
// A host takes the factor in by writing ModuleName into a command alongside the
// multi-factor module, the SMS code engine and the account module. It delivers
// the factor capability without claiming it, one module per kind.
package sms

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
	smssend "github.com/vislake/speed/pkg/sms"
)

// ModuleName is this module's name in the registry. A host writes it into the
// command that takes the SMS factor in.
const ModuleName = "authn.mfa.sms"

// The factor's own name for the step that asks for the code, and the field it
// asks for. The step name is kept in the flow record and handed back as
// FlowState.Step.
const (
	stepCode  = "code"
	fieldCode = "code"
)

// challengeVersion is the version of the state the factor keeps between sending
// a code and answering it: the challenge identifier the code engine returned.
const challengeVersion = 1

// factor is this module's product. It delivers the SMS kind of second factor.
type factor struct {
	codes    authn.SMSCodes
	accounts authn.Accounts
}

// factor must satisfy the capability it delivers.
var _ authn.Factor = (*factor)(nil)

// init registers this module with the process registry.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// The SMS code engine and the account reads are required: each is delivered
// exclusively by another module of the directory, so the closure of a command
// that takes this one in does not pull them in — the host writes them down. The
// factor capability is delivered without being claimed, which is what lets one
// module deliver each kind and several kinds stand side by side. The module
// declares no input item, and Prepare states that it runs.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*authn.SMSCodes)(nil)},
			{Token: (*authn.Accounts)(nil)},
		},
		Provides: []core.Provision{{Token: (*authn.Factor)(nil)}},

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
				return nil, fmt.Errorf("%s: taking up the SMS code engine: %w", ModuleName, err)
			}
			accounts, err := core.Resolve[authn.Accounts](reg)
			if err != nil {
				return nil, fmt.Errorf("%s: taking up the account reads: %w", ModuleName, err)
			}
			return &factor{codes: codes, accounts: accounts}, nil
		},
	}
}

// Kind is the kind of factor this module is.
func (f *factor) Kind() authn.FactorKind { return authn.FactorSMS }

// Enroll registers the factor. It carries no step of its own: the account must
// already hold a phone whose ownership it proved, and such a phone is
// registered at once. An account with no proven phone is refused with a hint to
// verify one first, because the factor binds a number the account has already
// proven and nothing here sends a code to prove a new one.
//
// The register row is the multi-factor module's: it was written before this
// call and is made active from the StepEnrolled answer.
func (f *factor) Enroll(ctx context.Context, fl authn.FlowState, in authn.StepInput) (authn.Step, error) {
	phone, err := f.provenPhone(ctx, fl.Account)
	if err != nil {
		return unavailable("reading the account's phone", err), nil
	}
	if phone == "" {
		return authn.StepRefused(errNoProvenPhone), nil
	}
	return authn.StepEnrolled(authn.FactorRecord{}), nil
}

// Challenge sends a code to the account's proven phone and asks the client for
// it. A flow that may make no more sends is answered as throttled without a
// send; the count of sends it made is reported with WithSends, which the login
// engine folds into the flow's budget.
//
// The challenge identifier the code engine returned is kept in the answer's
// private bytes, which the engine keeps as they stand and hands back as
// FlowState.Private when the code is answered. FlowState.Proof is the
// human-verification proof the request presented; the code engine checks and
// consumes it once before spending any budget.
func (f *factor) Challenge(ctx context.Context, fl authn.FlowState, rec authn.FactorRecord) (authn.Step, error) {
	if fl.SendsLeft <= 0 {
		return authn.StepThrottled(0), nil
	}
	phone, err := f.provenPhone(ctx, rec.Account)
	if err != nil {
		return unavailable("reading the account's phone", err), nil
	}
	if phone == "" {
		return authn.StepRefused(errNoProvenPhone), nil
	}

	id, err := f.codes.Send(ctx, authn.SendCode{
		Purpose: authn.SMSPurposeMFA,
		Phone:   phone,
		Account: rec.Account,
		Locale:  fl.Locale,
		Deliver: true,
		Source:  fl.Source,
		Proof:   fl.Proof,
	})
	switch {
	case err == nil:
	case errors.Is(err, authn.ErrThrottled):
		return authn.StepThrottled(throttledWait(err)), nil
	case errors.Is(err, authn.ErrHumanVerificationRequired), errors.Is(err, authn.ErrHumanProofTooLong):
		// The deployment requires a proof and this send presented none, an
		// invalid one, or one over its limit. The code engine checked it before
		// spending any budget; the engine answers the 403 or 400 rather than a
		// failed login.
		return authn.StepRefused(fmt.Errorf("authn.mfa.sms: %w", err)), nil
	case errors.Is(err, authn.ErrUnsupportedRegion):
		// The number's region is not in the allowed table, which the engine
		// states as a refusal. The factor's answers have no separate shape for
		// it, and the engine merges a factor's outward failure into login-failed.
		return authn.StepRefused(fmt.Errorf("authn.mfa.sms: %w", authn.ErrUnsupportedRegion)), nil
	default:
		return unavailable("sending a code", err), nil
	}
	return authn.StepPrompt(stepCode, markChallenge(id), codeField()).WithSends(1), nil
}

// Verify checks the code the client answered with against the challenge the
// challenge step started. A pass carries the sms method alone; a code that does
// not hold is a refusal, and a code engine that cannot be reached is an outage.
// The code is passed to the engine and kept nowhere here.
func (f *factor) Verify(ctx context.Context, fl authn.FlowState, rec authn.FactorRecord, in authn.StepInput) (authn.Step, error) {
	id, ok := readChallenge(fl.Private)
	if !ok {
		return authn.StepRefused(errNoChallenge), nil
	}
	code := in.Fields[fieldCode]
	if code == "" {
		// An answer with no code is not a guess at one: the engine counts
		// attempts of its own, and burning a challenge attempt on a malformed
		// answer would spend the account's budget on nothing.
		return authn.StepRefused(errNoCode), nil
	}

	switch err := f.codes.Verify(ctx, id, code); {
	case err == nil:
		return authn.StepPassed(authn.Pass{Methods: []authn.Method{authn.MethodSMS}}), nil
	case errors.Is(err, authn.ErrInvalidCredentials):
		return authn.StepRefused(errRefused), nil
	default:
		return unavailable("checking the code", err), nil
	}
}

// Remove is a no-op. The factor keeps no data of its own to erase: it points at
// the account's phone identifier, which the account module owns and the factor
// does not write.
func (f *factor) Remove(ctx context.Context, account authn.AccountID, rec authn.FactorRecord) error {
	return nil
}

// provenPhone is the account's phone whose ownership it proved, or "" when it
// has none. A phone whose value was only asserted — by an administrator or by an
// import — or one whose verification channel does not prove ownership is not one
// the factor can bind, because a second factor has to be a number the account
// proved it can receive a code at.
func (f *factor) provenPhone(ctx context.Context, account authn.AccountID) (string, error) {
	if account == "" {
		return "", nil
	}
	identifiers, err := f.accounts.Identifiers(ctx, account)
	if err != nil {
		return "", err
	}
	for _, id := range identifiers {
		if id.Kind != authn.IdentifierPhone || id.VerifiedAt.IsZero() {
			continue
		}
		if (authn.Verification{Via: id.VerifiedVia}).Proves() {
			return id.Value, nil
		}
	}
	return "", nil
}

// throttledWait is how long a throttled send asks the caller to wait. A send
// error that reads as ErrThrottled carries the time in a *ThrottledError; one
// that does not is read as no stated time, which the engine sends no Retry-After
// for.
func throttledWait(err error) time.Duration {
	var throttled *authn.ThrottledError
	if errors.As(err, &throttled) {
		return throttled.RetryAfter
	}
	return 0
}

// codeField is the field the factor asks the client for.
func codeField() authn.Field {
	return authn.Field{Name: fieldCode, Kind: authn.FieldCode, Required: true}
}

// challengeMark is the state Challenge leaves for Verify: the challenge
// identifier the code engine returned, and nothing else. It carries no code and
// no phone.
type challengeMark struct {
	Version int    `json:"v"`
	ID      string `json:"id"`
}

// markChallenge writes the state that says which challenge the client's code
// answers.
func markChallenge(id string) []byte {
	// A struct of an int and a string always encodes.
	b, _ := json.Marshal(challengeMark{Version: challengeVersion, ID: id})
	return b
}

// readChallenge reads the state markChallenge wrote. Anything else, empty state
// included, is a flow that carries no challenge this factor started.
func readChallenge(private []byte) (string, bool) {
	var m challengeMark
	dec := json.NewDecoder(bytes.NewReader(private))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil || m.Version != challengeVersion || m.ID == "" || dec.More() {
		return "", false
	}
	return m.ID, true
}

// The causes the factor puts on an answer. None carries a code, a phone or a
// challenge identifier.
var (
	// errNoProvenPhone is the refusal of an account that has no phone whose
	// ownership it proved. It wraps authn.ErrPhoneRequired so the engine answers
	// the 403 phone-required that tells the client to verify a phone first,
	// rather than a failed login. The text after the sentinel is a diagnostic for
	// a caller that logs the cause; this package writes no log line.
	errNoProvenPhone = fmt.Errorf("%w: the account has no proven phone; verify one first", authn.ErrPhoneRequired)

	errNoChallenge = errors.New("authn.mfa.sms: the flow carries no challenge this factor started")

	errNoCode = errors.New("authn.mfa.sms: the answer carries no code")

	// errRefused is the cause of every refusal of a code, whatever made it fail;
	// the cause reaches the log and not the client.
	errRefused = fmt.Errorf("authn.mfa.sms: %w", authn.ErrInvalidCredentials)
)

// unavailable answers that the factor cannot say now. The dependency's error
// stays in the chain so that the engine finds an ErrUnavailable in it, and the
// text names the step and nothing the client sent.
func unavailable(doing string, err error) authn.Step {
	return authn.StepUnavailable(fmt.Errorf("authn.mfa.sms: %s: %w", doing, err))
}
