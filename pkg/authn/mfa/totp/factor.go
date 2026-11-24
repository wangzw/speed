package totp

import (
	"context"
	"errors"
	"fmt"

	"github.com/pquerna/otp"
	pquernaotp "github.com/pquerna/otp/totp"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/clock"
	"github.com/vislake/speed/pkg/authn/mfa"
)

const (
	stepEnroll    = "totp"
	stepChallenge = "code"
)

var privateConfirm = []byte{1}

var errCodeMismatch = errors.New("the code does not match the seed")

type enrollmentOptions struct {
	OTPAuthURI string `json:"otpauth_uri"`
}

type factor struct {
	register *register
	accounts authn.Accounts
	issuer   string
	clock    clock.Source
}

func (f *factor) Kind() authn.FactorKind { return authn.FactorTOTP }

// Enroll registers a TOTP factor: the first step generates and seals a seed and answers the otpauth address once, and the confirmation step requires a code before the factor is activated.
func (f *factor) Enroll(ctx context.Context, fs authn.FlowState, in authn.StepInput) (authn.Step, error) {
	rec, ok := mfa.PendingFactor(ctx)
	if !ok {
		return authn.Step{}, fmt.Errorf("%w: a registration step reached the TOTP factor without a pending row",
			authn.ErrInvalidArgument)
	}
	if len(fs.Private) == 0 {
		return f.beginEnroll(ctx, rec)
	}
	return f.finishEnroll(ctx, rec, in)
}

func (f *factor) beginEnroll(ctx context.Context, rec authn.FactorRecord) (authn.Step, error) {
	key, err := pquernaotp.Generate(pquernaotp.GenerateOpts{
		Issuer:      f.issuer,
		AccountName: f.accountName(ctx, rec.Account),
		Period:      periodSeconds,
		SecretSize:  secretBytes,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
	if err != nil {
		return authn.Step{}, fmt.Errorf("generating a TOTP seed: %w", err)
	}
	s := seed{
		FactorID:    rec.ID,
		Secret:      key.Secret(),
		Algorithm:   algorithmSHA1,
		Digits:      digitsOfSix,
		Period:      periodSeconds,
		LastCounter: 0,
	}
	if err := f.register.insert(ctx, s); err != nil {
		return authn.Step{}, err
	}
	return authn.StepData(stepEnroll, privateConfirm, enrollmentOptions{OTPAuthURI: key.URL()}), nil
}

func (f *factor) accountName(ctx context.Context, account authn.AccountID) string {
	identifiers, err := f.accounts.Identifiers(ctx, account)
	if err != nil {
		return string(account)
	}
	for _, identifier := range identifiers {
		if identifier.Kind == authn.IdentifierUsername && identifier.Value != "" {
			return identifier.Value
		}
	}
	return string(account)
}

func (f *factor) finishEnroll(ctx context.Context, rec authn.FactorRecord, in authn.StepInput) (authn.Step, error) {
	s, found, err := f.register.load(ctx, rec.ID)
	if err != nil {
		return authn.Step{}, err
	}
	if !found {
		return authn.StepRefused(authn.ErrFactorNotEnrolled), nil
	}
	counter, ok := match(s, f.clock.Now(), in.Fields[codeField])
	if !ok {
		return authn.StepRefused(errCodeMismatch), nil
	}
	if counter <= s.LastCounter {
		return authn.StepRefused(authn.ErrReplay), nil
	}
	won, err := f.register.advance(ctx, rec.ID, counter)
	if err != nil {
		return authn.Step{}, err
	}
	if !won {
		return authn.StepRefused(authn.ErrReplay), nil
	}
	return authn.StepEnrolled(rec), nil
}

func (f *factor) Challenge(_ context.Context, _ authn.FlowState, _ authn.FactorRecord) (authn.Step, error) {
	return authn.StepPrompt(stepChallenge, nil, authn.Field{Name: codeField, Kind: authn.FieldCode, Required: true}), nil
}

// Verify checks a code against the seed, accepting the current step and one either side, and refuses a counter at or below the stored one as a replay.
func (f *factor) Verify(ctx context.Context, _ authn.FlowState, rec authn.FactorRecord, in authn.StepInput) (authn.Step, error) {
	if rec.Status != authn.FactorActive {
		return authn.StepRefused(authn.ErrFactorNotEnrolled), nil
	}
	s, found, err := f.register.load(ctx, rec.ID)
	if err != nil {
		return authn.Step{}, err
	}
	if !found {
		return authn.StepRefused(authn.ErrFactorNotEnrolled), nil
	}
	counter, ok := match(s, f.clock.Now(), in.Fields[codeField])
	if !ok {
		return authn.StepRefused(errCodeMismatch), nil
	}
	if counter <= s.LastCounter {
		return authn.StepRefused(authn.ErrReplay), nil
	}
	won, err := f.register.advance(ctx, rec.ID, counter)
	if err != nil {
		return authn.Step{}, err
	}
	if !won {
		return authn.StepRefused(authn.ErrReplay), nil
	}
	return authn.StepPassed(authn.Pass{Methods: []authn.Method{authn.MethodTOTP}}), nil
}

// Remove deletes the factor's seed; it is idempotent.
func (f *factor) Remove(ctx context.Context, _ authn.AccountID, rec authn.FactorRecord) error {
	return f.register.remove(ctx, rec.ID)
}
