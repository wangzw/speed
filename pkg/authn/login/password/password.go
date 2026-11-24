// Package password carries the password login method, which proves an account by an identifier and a password.
package password

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
)

// ModuleName is this module's name in the registry.
const ModuleName = "authn.login.password"

const methodName = "password"

const (
	stepCredentials = "credentials"
	stepNewPassword = "new_password"

	fieldIdentifier  = "identifier"
	fieldPassword    = "password"
	fieldNewPassword = "new_password"
)

const markVersion = 1

type method struct {
	passwords authn.Passwords
	throttle  authn.Throttle
}

var _ authn.LoginMethod = (*method)(nil)

func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers. Passwords and Throttle are required from the account module; settings and logging are optional.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*authn.Passwords)(nil)},
			{Token: (*authn.Throttle)(nil)},
		},
		Provides: []core.Provision{{Token: (*authn.LoginMethod)(nil)}},

		Prepare: func(context.Context, *core.Registry) (core.Enablement, error) {
			return core.Enablement{State: core.StateEnabled}, nil
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			passwords, err := core.Resolve[authn.Passwords](reg)
			if err != nil {
				return nil, fmt.Errorf("%s: taking up the password capability: %w", ModuleName, err)
			}
			throttle, err := core.Resolve[authn.Throttle](reg)
			if err != nil {
				return nil, fmt.Errorf("%s: taking up the back-off capability: %w", ModuleName, err)
			}
			return &method{passwords: passwords, throttle: throttle}, nil
		},
	}
}

func (m *method) Name() string { return methodName }

func (m *method) Describe() authn.MethodDescriptor {
	return authn.MethodDescriptor{
		Name:   methodName,
		Kind:   authn.MethodKindForm,
		Fields: descriptorFields(),
	}
}

func (m *method) Begin(ctx context.Context, s authn.Start) (authn.Step, error) {
	if !servesPurpose(s.Purpose) {
		return refusePurpose(s.Purpose), nil
	}
	return m.credentials(ctx, s.Fields), nil
}

func (m *method) Answer(ctx context.Context, f authn.FlowState, in authn.StepInput) (authn.Step, error) {
	if in.Action != authn.InputSubmit {
		return authn.StepRefused(errNotSubmitted), nil
	}
	if !servesPurpose(f.Purpose) {
		return refusePurpose(f.Purpose), nil
	}
	switch f.Step {
	case stepCredentials:
		return m.credentials(ctx, in.Fields), nil
	case stepNewPassword:
		return m.newPassword(ctx, f, in.Fields)
	default:
		return authn.StepRefused(errUnknownStep), nil
	}
}

func servesPurpose(purpose string) bool {
	return purpose == authn.PurposeLogin || purpose == authn.PurposeStepUp
}

var (
	errRefused = fmt.Errorf("authn.login.password: %w", authn.ErrInvalidCredentials)

	errNotSubmitted = errors.New("authn.login.password: the answer is not a submitted form")
	errUnknownStep  = errors.New("authn.login.password: the flow is at a step this method did not name")
	errBadState     = errors.New("authn.login.password: the flow carries no verified credential")
	errBadAccount   = errors.New("authn.login.password: the password capability returned an account that is not a library account")
)

func refusePurpose(purpose string) authn.Step {
	return authn.StepRefused(fmt.Errorf("authn.login.password: purpose %q is not served", purpose))
}

func (m *method) credentials(ctx context.Context, fields map[string]string) authn.Step {
	identifier, secret := fields[fieldIdentifier], fields[fieldPassword]
	if strings.TrimSpace(identifier) == "" || secret == "" {
		return promptCredentials()
	}
	if err := ctx.Err(); err != nil {
		return authn.StepUnavailable(err)
	}

	key := throttleKey(identifier)
	wait, err := m.throttle.Check(ctx, authn.ScopeIdentifier, key)
	if err != nil {
		return unavailable("reading the back-off", err)
	}
	if wait != 0 {
		return authn.StepThrottled(wait)
	}
	if done := ctx.Err(); done != nil {
		return authn.StepUnavailable(done)
	}

	account, result, err := m.passwords.Verify(ctx, identifier, secret)
	switch {
	case err == nil:
	case errors.Is(err, authn.ErrInvalidCredentials), errors.Is(err, authn.ErrAccountNotFound):
		return m.refuse(ctx, key)
	case errors.Is(err, authn.ErrInvalidIdentifier):
		return authn.StepRefused(fmt.Errorf("authn.login.password: %w", authn.ErrInvalidIdentifier))
	default:
		return unavailable("checking the password", err)
	}

	if !libraryAccount(account) {
		return unavailable("checking the password", errBadAccount)
	}
	if err := m.throttle.Succeed(ctx, authn.ScopeIdentifier, key); err != nil {
		return unavailable("clearing the back-off", err)
	}
	if result.MustChange {
		return promptNewPassword(markVerified(account))
	}
	return proven(account)
}

func (m *method) refuse(ctx context.Context, key string) authn.Step {
	if _, err := m.throttle.Fail(ctx, authn.ScopeIdentifier, key); err != nil {
		return unavailable("counting the failure", err)
	}
	return authn.StepRefused(errRefused)
}

func (m *method) newPassword(ctx context.Context, f authn.FlowState, fields map[string]string) (authn.Step, error) {
	account, ok := readMark(f.Private)
	if !ok {
		return authn.StepRefused(errBadState), nil
	}
	next := fields[fieldNewPassword]
	if next == "" {
		return promptNewPassword(f.Private), nil
	}
	if err := ctx.Err(); err != nil {
		return authn.StepUnavailable(err), nil
	}

	err := m.passwords.Set(ctx, account, next, authn.SetPasswordOptions{})
	switch {
	case err == nil:
		return provenChanged(account), nil
	case errors.Is(err, authn.ErrPasswordPolicy):
		return promptNewPassword(f.Private), fmt.Errorf("authn.login.password: %w", authn.ErrPasswordPolicy)
	case errors.Is(err, authn.ErrInvalidCredentials), errors.Is(err, authn.ErrAccountNotFound):
		return authn.StepRefused(errRefused), nil
	default:
		return unavailable("setting the password", err), nil
	}
}

func proven(account authn.AccountID) authn.Step {
	return authn.StepProven(authn.Proof{
		Account: account,
		Methods: []authn.Method{authn.MethodPassword},
	})
}

func provenChanged(account authn.AccountID) authn.Step {
	return authn.StepProven(authn.Proof{
		Account:         account,
		Methods:         []authn.Method{authn.MethodPassword},
		PasswordChanged: true,
	})
}

func unavailable(doing string, err error) authn.Step {
	return authn.StepUnavailable(fmt.Errorf("authn.login.password: %s: %w", doing, err))
}

func descriptorFields() []authn.Field {
	return []authn.Field{
		{Name: fieldIdentifier, Kind: authn.FieldText, Required: true},
		{Name: fieldPassword, Kind: authn.FieldPassword, Required: true, Secret: true},
	}
}

func promptCredentials() authn.Step {
	return authn.StepPrompt(stepCredentials, nil, descriptorFields()...)
}

func promptNewPassword(private []byte) authn.Step {
	return authn.StepPrompt(stepNewPassword, private,
		authn.Field{Name: fieldNewPassword, Kind: authn.FieldPassword, Required: true, Secret: true},
	)
}

func libraryAccount(id authn.AccountID) bool {
	parsed, err := authn.ParseAccountID(id.String())
	return err == nil && !parsed.IsStatic()
}

func throttleKey(identifier string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(identifier))))
	return hex.EncodeToString(sum[:])
}

type mark struct {
	Version int    `json:"v"`
	Account string `json:"account"`
}

func markVerified(account authn.AccountID) []byte {
	b, _ := json.Marshal(mark{Version: markVersion, Account: account.String()})
	return b
}

func readMark(private []byte) (authn.AccountID, bool) {
	var m mark
	dec := json.NewDecoder(bytes.NewReader(private))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil || m.Version != markVersion || dec.More() {
		return "", false
	}
	account, err := authn.ParseAccountID(m.Account)
	if err != nil || !libraryAccount(account) {
		return "", false
	}
	return account, true
}
