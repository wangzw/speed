package password

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/step"
)

const (
	sentinelPassword = "Sentinel-Pa55-4d1c9e"
	sentinelNew      = "Sentinel-New-7b2e03"
	sentinelIdent    = "sentinel.user@example.test"
)

const testAccount = authn.AccountID("acct_0001")

// call is one call a fake received, in the order it arrived.
type call struct {
	op         string
	scope      authn.ThrottleScope
	key        string
	identifier string
	password   string
}

// ledger is the order of every call the two fakes received.
type ledger struct {
	mu    sync.Mutex
	calls []call
}

func (l *ledger) add(c call) {
	l.mu.Lock()
	l.calls = append(l.calls, c)
	l.mu.Unlock()
}

func (l *ledger) all() []call {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]call(nil), l.calls...)
}

func (l *ledger) count(op string) int {
	n := 0
	for _, c := range l.all() {
		if c.op == op {
			n++
		}
	}
	return n
}

// ops is the sequence of operations, for a case about order.
func (l *ledger) ops() []string {
	var out []string
	for _, c := range l.all() {
		out = append(out, c.op)
	}
	return out
}

type fakeThrottle struct {
	led *ledger

	wait       time.Duration
	checkErr   error
	failErr    error
	succeedErr error
	onCheck    func()
}

func (f *fakeThrottle) Check(_ context.Context, scope authn.ThrottleScope, key string) (time.Duration, error) {
	f.led.add(call{op: "check", scope: scope, key: key})
	if f.onCheck != nil {
		f.onCheck()
	}
	return f.wait, f.checkErr
}

func (f *fakeThrottle) Fail(_ context.Context, scope authn.ThrottleScope, key string) (bool, error) {
	f.led.add(call{op: "fail", scope: scope, key: key})
	return false, f.failErr
}

func (f *fakeThrottle) Succeed(_ context.Context, scope authn.ThrottleScope, key string) error {
	f.led.add(call{op: "succeed", scope: scope, key: key})
	return f.succeedErr
}

type fakePasswords struct {
	authn.Passwords
	led *ledger

	verify func(ctx context.Context, identifier, password string) (authn.AccountID, authn.VerifyResult, error)
	set    func(ctx context.Context, id authn.AccountID, password string, opt authn.SetPasswordOptions) error
}

func (f *fakePasswords) Verify(ctx context.Context, identifier, password string) (authn.AccountID, authn.VerifyResult, error) {
	f.led.add(call{op: "verify", identifier: identifier, password: password})
	return f.verify(ctx, identifier, password)
}

func (f *fakePasswords) Set(ctx context.Context, id authn.AccountID, password string, opt authn.SetPasswordOptions) error {
	f.led.add(call{op: "set", key: id.String(), password: password})
	return f.set(ctx, id, password, opt)
}

type rig struct {
	m  *method
	th *fakeThrottle
	pw *fakePasswords
	l  *ledger
}

func newRig() *rig {
	l := &ledger{}
	th := &fakeThrottle{led: l}
	pw := &fakePasswords{led: l}
	pw.verify = func(_ context.Context, _, password string) (authn.AccountID, authn.VerifyResult, error) {
		if password == sentinelPassword {
			return testAccount, authn.VerifyResult{}, nil
		}
		return "", authn.VerifyResult{}, authn.ErrInvalidCredentials
	}
	pw.set = func(context.Context, authn.AccountID, string, authn.SetPasswordOptions) error { return nil }
	return &rig{m: &method{passwords: pw, throttle: th}, th: th, pw: pw, l: l}
}

func credentialFields(identifier, password string) map[string]string {
	return map[string]string{fieldIdentifier: identifier, fieldPassword: password}
}

func loginStart(fields map[string]string) authn.Start {
	return authn.Start{Purpose: authn.PurposeLogin, Fields: fields}
}

func submit(fields map[string]string) authn.StepInput {
	return authn.StepInput{Action: authn.InputSubmit, Fields: fields}
}

func flowAt(stepName string, private []byte) authn.FlowState {
	return authn.FlowState{ID: "flow-1", Purpose: authn.PurposeLogin, Step: stepName, Private: private}
}

func kindOf(s authn.Step) step.Kind { return step.KindOf(s) }

func describe(s authn.Step) string {
	var b strings.Builder
	fmt.Fprintf(&b, "kind=%s name=%q private=%q to=%q data=%v retry=%s", kindOf(s), step.NameOf(s), step.PrivateOf(s),
		step.RedirectOf(s), step.DataOf(s), step.RetryAfterOf(s))
	fmt.Fprintf(&b, " fields=%+v proof=%+v", step.FieldsOf(s), step.ProofOf(s))
	if c := step.CauseOf(s); c != nil {
		fmt.Fprintf(&b, " cause=%v", c)
	}
	return b.String()
}
