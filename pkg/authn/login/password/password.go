// Package password carries the password login method: the first factor that
// proves an account by an identifier and a password.
//
// The method answers with a Step and does nothing else. It checks the
// identifier's back-off before it derives anything, hands the credential to the
// account module's password capability, counts the outcome against the same
// back-off key, and says what the login engine is to do next. It touches no
// HTTP, session, token or flow record, and it writes no log line: the engine
// logs the cause of an answer, and no cause here names a password or an
// identifier.
//
// A credential that carries must_change is not proven by the password alone. The
// method asks for a new password first, and only a new password the policy
// accepted ends in a proof.
//
// A host takes the method in by writing ModuleName into a command alongside the
// login engine and the account module. It delivers the login method capability
// non-exclusively, so it stands beside the other methods.
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

// methodName is the name this method reports. It is a constant of the code: it
// appears in routes, flow records and metric attributes, and a published name
// never changes.
const methodName = "password"

// The method's own names for its steps, which the engine keeps in the flow
// record and hands back as FlowState.Step, and for the fields it asks for.
const (
	stepCredentials = "credentials"
	stepNewPassword = "new_password"

	fieldIdentifier  = "identifier"
	fieldPassword    = "password"
	fieldNewPassword = "new_password"
)

// markVersion is the version of the state kept between the check of a password
// that carries must_change and the answer with the new one.
const markVersion = 1

// method is this module's product.
type method struct {
	passwords authn.Passwords
	throttle  authn.Throttle
}

// method must satisfy the capability it delivers.
var _ authn.LoginMethod = (*method)(nil)

// init registers this module with the process registry.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// Passwords and Throttle are required: each is delivered exclusively by the
// account module, so a command that took this method in without choosing an
// account module fails before anything is built. The login method capability is
// delivered without being claimed, which is what lets several methods stand side
// by side. The module declares no input item, and Prepare states that it runs.
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

// Name is the method's stable name.
func (m *method) Name() string { return methodName }

// Begin starts a flow. The start request's own fields are the credential when
// the client sent both; otherwise the method asks for them.
func (m *method) Begin(ctx context.Context, s authn.Start) (authn.Step, error) {
	if !servesPurpose(s.Purpose) {
		return refusePurpose(s.Purpose), nil
	}
	return m.credentials(ctx, s.Fields), nil
}

// Answer handles the client's answer to the previous step: the credential the
// method asked for, or the new password a must_change credential has to be
// followed by. A callback and a step the method never named are refused, so that
// a flow record this method did not write cannot be walked into a proof.
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

// servesPurpose reports whether the method answers a flow of this purpose. A
// password proves an account, which is what a login and a step-up need; a link
// binds an external identity and an enroll registers a factor, and neither is
// something a password proves. The zero purpose is none.
func servesPurpose(purpose string) bool {
	return purpose == authn.PurposeLogin || purpose == authn.PurposeStepUp
}

// The causes the method puts on an answer. None carries a value the client sent.
var (
	// errRefused is the cause of every refusal of a credential, whatever made it
	// fail: an unknown identifier, a wrong password, a disabled account and an
	// account with no password all read the same here, in the log as on the wire.
	errRefused = fmt.Errorf("authn.login.password: %w", authn.ErrInvalidCredentials)

	errNotSubmitted = errors.New("authn.login.password: the answer is not a submitted form")
	errUnknownStep  = errors.New("authn.login.password: the flow is at a step this method did not name")
	errBadState     = errors.New("authn.login.password: the flow carries no verified credential")
	errBadAccount   = errors.New("authn.login.password: the password capability returned an account that is not a library account")
)

// refusePurpose refuses a flow of a purpose the method does not serve, before it
// reads or counts anything.
func refusePurpose(purpose string) authn.Step {
	return authn.StepRefused(fmt.Errorf("authn.login.password: purpose %q is not served", purpose))
}

// credentials checks the identifier and the password, in the order that keeps a
// back-off from being a free guess: the back-off is read first, and a key in
// back-off is answered without a derivation.
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
	// The derivation is the expensive step and the one a guess is made at, so a
	// call that ended while the back-off was read does not go on to it.
	if done := ctx.Err(); done != nil {
		return authn.StepUnavailable(done)
	}

	account, result, err := m.passwords.Verify(ctx, identifier, secret)
	switch {
	case err == nil:
	case errors.Is(err, authn.ErrInvalidCredentials), errors.Is(err, authn.ErrAccountNotFound):
		return m.refuse(ctx, key)
	case errors.Is(err, authn.ErrInvalidIdentifier):
		// No credential was checked against anything: the identifier cannot name
		// an account, so there is no guess to count, and the answer depends on the
		// form of the string alone.
		return authn.StepRefused(fmt.Errorf("authn.login.password: %w", authn.ErrInvalidIdentifier))
	default:
		// An ErrBusy that refused the derivation, an outage, and a call that ended
		// are not a guess either, and are answered as the capability being unable to
		// say, which is not a refusal.
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

// refuse counts a failed attempt against the key and refuses. A back-off that
// cannot be written is not a refusal: the attempt would go uncounted.
func (m *method) refuse(ctx context.Context, key string) authn.Step {
	if _, err := m.throttle.Fail(ctx, authn.ScopeIdentifier, key); err != nil {
		return unavailable("counting the failure", err)
	}
	return authn.StepRefused(errRefused)
}

// newPassword sets the password a must_change credential is to be followed by.
// Only the flow state that credentials wrote opens it, and only a password the
// policy accepted ends in a proof. A password the policy refuses is a hint the
// client can answer again: the step is asked again and the error carries
// ErrPasswordPolicy, which the engine answers as a 400 without ending the flow.
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

// proven is the answer that the first factor holds.
func proven(account authn.AccountID) authn.Step {
	return authn.StepProven(authn.Proof{
		Account: account,
		Methods: []authn.Method{authn.MethodPassword},
	})
}

// provenChanged is proven for the must_change path, where the method also
// replaced the password. The fact lets the engine revoke the account's other
// sessions once the new one exists.
func provenChanged(account authn.AccountID) authn.Step {
	return authn.StepProven(authn.Proof{
		Account:         account,
		Methods:         []authn.Method{authn.MethodPassword},
		PasswordChanged: true,
	})
}

// unavailable answers that the method cannot say now. The dependency's error
// stays in the chain, so that the engine finds an ErrBusy or an ErrUnavailable
// in it, and the text names the step and nothing the client sent.
func unavailable(doing string, err error) authn.Step {
	return authn.StepUnavailable(fmt.Errorf("authn.login.password: %s: %w", doing, err))
}

// promptCredentials asks for the two fields of a credential.
func promptCredentials() authn.Step {
	return authn.StepPrompt(stepCredentials, nil,
		authn.Field{Name: fieldIdentifier, Kind: authn.FieldText, Required: true},
		authn.Field{Name: fieldPassword, Kind: authn.FieldPassword, Required: true, Secret: true},
	)
}

// promptNewPassword asks for the new password, with the state that says the old
// one has been verified.
func promptNewPassword(private []byte) authn.Step {
	return authn.StepPrompt(stepNewPassword, private,
		authn.Field{Name: fieldNewPassword, Kind: authn.FieldPassword, Required: true, Secret: true},
	)
}

// libraryAccount reports whether an account is one the password capability may
// answer for: well formed and not a subject of the static table, whose accounts
// the account module does not keep.
func libraryAccount(id authn.AccountID) bool {
	parsed, err := authn.ParseAccountID(id.String())
	return err == nil && !parsed.IsStatic()
}

// throttleKey is the back-off key of a submitted identifier: the SHA-256 of it
// with the white space around it removed and its letters folded to lower case,
// in hexadecimal. It is the string the client sent and not the account it names,
// so an identifier that exists and one that does not count along one path, and
// the key holds nothing of the identifier that a read of the table could use.
//
// The account module normalizes an identifier by rules of its own, which are not
// on this surface. Spellings it would treat as one identifier and this folding
// keeps apart count in separate keys here, and the account-level back-off that
// the password capability keeps silently is what holds them together.
func throttleKey(identifier string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(identifier))))
	return hex.EncodeToString(sum[:])
}

// mark is the flow state between a password that carries must_change and the new
// one. It says which account the check proved and nothing else: it holds no
// password, and the engine keeps it as it stands in a record the client cannot
// write.
type mark struct {
	Version int    `json:"v"`
	Account string `json:"account"`
}

// markVerified writes the state that says the account's current password was
// checked.
func markVerified(account authn.AccountID) []byte {
	// A struct of an int and a string always encodes.
	b, _ := json.Marshal(mark{Version: markVersion, Account: account.String()})
	return b
}

// readMark reads the state markVerified wrote. Anything else, empty state
// included, is not a verified credential.
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
