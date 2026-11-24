// Package sms is the SMS second factor: it sends a code to the account's proven phone through the SMS code engine and verifies the answer.
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

const (
	stepCode  = "code"
	fieldCode = "code"
)

const challengeVersion = 1

type factor struct {
	codes    authn.SMSCodes
	accounts authn.Accounts
}

var _ authn.Factor = (*factor)(nil)

func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers. It delivers the factor without claiming it and requires the SMS code engine and the account reads.
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

func (f *factor) Kind() authn.FactorKind { return authn.FactorSMS }

// Enroll registers the factor at once; the account must already hold a phone whose ownership it proved, and no such phone is refused with ErrPhoneRequired.
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

// Challenge sends a code to the account's proven phone and asks the client for it. A flow out of sends is answered as throttled, and the challenge identifier is kept in the private bytes.
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
		return authn.StepRefused(fmt.Errorf("authn.mfa.sms: %w", err)), nil
	case errors.Is(err, authn.ErrUnsupportedRegion):
		return authn.StepRefused(fmt.Errorf("authn.mfa.sms: %w", authn.ErrUnsupportedRegion)), nil
	default:
		return unavailable("sending a code", err), nil
	}
	return authn.StepPrompt(stepCode, markChallenge(id), codeField()).WithSends(1), nil
}

// Verify checks the submitted code against the challenge in the private bytes. A pass carries the sms method; a code engine that cannot be reached is an outage.
func (f *factor) Verify(ctx context.Context, fl authn.FlowState, rec authn.FactorRecord, in authn.StepInput) (authn.Step, error) {
	id, ok := readChallenge(fl.Private)
	if !ok {
		return authn.StepRefused(errNoChallenge), nil
	}
	code := in.Fields[fieldCode]
	if code == "" {
		// An answer with no code is not a guess, so it spends no attempt.
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

// Remove does nothing: the factor holds no data of its own.
func (f *factor) Remove(ctx context.Context, account authn.AccountID, rec authn.FactorRecord) error {
	return nil
}

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

func throttledWait(err error) time.Duration {
	var throttled *authn.ThrottledError
	if errors.As(err, &throttled) {
		return throttled.RetryAfter
	}
	return 0
}

func codeField() authn.Field {
	return authn.Field{Name: fieldCode, Kind: authn.FieldCode, Required: true}
}

type challengeMark struct {
	Version int    `json:"v"`
	ID      string `json:"id"`
}

func markChallenge(id string) []byte {
	b, _ := json.Marshal(challengeMark{Version: challengeVersion, ID: id})
	return b
}

func readChallenge(private []byte) (string, bool) {
	var m challengeMark
	dec := json.NewDecoder(bytes.NewReader(private))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil || m.Version != challengeVersion || m.ID == "" || dec.More() {
		return "", false
	}
	return m.ID, true
}

var (
	errNoProvenPhone = fmt.Errorf("%w: the account has no proven phone; verify one first", authn.ErrPhoneRequired)

	errNoChallenge = errors.New("authn.mfa.sms: the flow carries no challenge this factor started")

	errNoCode = errors.New("authn.mfa.sms: the answer carries no code")

	errRefused = fmt.Errorf("authn.mfa.sms: %w", authn.ErrInvalidCredentials)
)

func unavailable(doing string, err error) authn.Step {
	return authn.StepUnavailable(fmt.Errorf("authn.mfa.sms: %s: %w", doing, err))
}
