package selfservice_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/settings"
)

// callLog is the one list every fake writes its calls to, so that a test reads
// the order of calls made on different dependencies.
type callLog struct {
	mu    sync.Mutex
	calls []string
}

func (l *callLog) add(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, fmt.Sprintf(format, args...))
}

func (l *callLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.calls...)
}

// names are the calls without their arguments: the part up to the first space.
func (l *callLog) names() []string {
	var out []string
	for _, c := range l.all() {
		name, _, _ := strings.Cut(c, " ")
		out = append(out, name)
	}
	return out
}

func (l *callLog) count(name string) int {
	n := 0
	for _, c := range l.names() {
		if c == name {
			n++
		}
	}
	return n
}

func (l *callLog) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = nil
}

// The fakes below embed the interface they stand for, so a method nobody
// overrode is a nil call that panics: a handler that reached for something a test
// did not expect ends in the 500 of the entry point's recovery, which no test
// asserts as an answer.

type fakeAccounts struct {
	authn.Accounts
	log         *callLog
	account     authn.Account
	identifiers []authn.Identifier
	externals   []authn.ExternalBinding
	err         error
}

func (f *fakeAccounts) Get(_ context.Context, id authn.AccountID) (authn.Account, error) {
	f.log.add("accounts.Get %s", id)
	return f.account, f.err
}

func (f *fakeAccounts) Identifiers(_ context.Context, id authn.AccountID) ([]authn.Identifier, error) {
	f.log.add("accounts.Identifiers %s", id)
	return f.identifiers, f.err
}

func (f *fakeAccounts) Externals(_ context.Context, id authn.AccountID) ([]authn.ExternalBinding, error) {
	f.log.add("accounts.Externals %s", id)
	return f.externals, f.err
}

func (f *fakeAccounts) Lookup(_ context.Context, _ authn.IdentifierKind, _ string) (authn.AccountID, error) {
	f.log.add("accounts.Lookup")
	return "someone-else", nil
}

type addCall struct {
	account authn.AccountID
	kind    authn.IdentifierKind
	value   string
	via     authn.Verification
}

type fakeAdmin struct {
	authn.AccountAdmin
	log       *callLog
	added     []addCall
	removed   []string
	unlinked  []string
	addErr    error
	removeErr error
	unlinkErr error
}

func (f *fakeAdmin) AddIdentifier(_ context.Context, id authn.AccountID, kind authn.IdentifierKind, value string, via authn.Verification) error {
	f.log.add("admin.AddIdentifier")
	f.added = append(f.added, addCall{id, kind, value, via})
	return f.addErr
}

func (f *fakeAdmin) RemoveIdentifier(_ context.Context, id authn.AccountID, identifierID string) error {
	f.log.add("admin.RemoveIdentifier %s %s", id, identifierID)
	f.removed = append(f.removed, identifierID)
	return f.removeErr
}

func (f *fakeAdmin) UnlinkExternal(_ context.Context, id authn.AccountID, externalID string) error {
	f.log.add("admin.UnlinkExternal %s %s", id, externalID)
	f.unlinked = append(f.unlinked, externalID)
	return f.unlinkErr
}

type setCall struct {
	account  authn.AccountID
	password string
	opt      authn.SetPasswordOptions
}

type fakePasswords struct {
	authn.Passwords
	log  *callLog
	sets []setCall
	err  error
}

func (f *fakePasswords) Set(_ context.Context, id authn.AccountID, password string, opt authn.SetPasswordOptions) error {
	f.log.add("passwords.Set")
	f.sets = append(f.sets, setCall{id, password, opt})
	return f.err
}

type revokeCall struct {
	account authn.AccountID
	except  authn.SessionID
	reason  string
}

type fakeSessions struct {
	authn.Sessions
	log       *callLog
	issued    authn.Issued
	rotated   []authn.SessionID
	revoked   []revokeCall
	rotateErr error
	revokeErr error
}

func (f *fakeSessions) Rotate(_ context.Context, id authn.SessionID) (authn.Issued, error) {
	f.log.add("sessions.Rotate %s", id)
	f.rotated = append(f.rotated, id)
	// The token is handed back even when the error is set, so that a case can
	// exercise a rotation that both failed and returned a value.
	return f.issued, f.rotateErr
}

func (f *fakeSessions) RevokeAccount(_ context.Context, account authn.AccountID, except authn.SessionID, reason string) ([]authn.SessionID, error) {
	f.log.add("sessions.RevokeAccount %s", reason)
	f.revoked = append(f.revoked, revokeCall{account, except, reason})
	return nil, f.revokeErr
}

func (f *fakeSessions) SetCookie(w http.ResponseWriter, in authn.Issued) {
	f.log.add("sessions.SetCookie")
	http.SetCookie(w, &http.Cookie{Name: "sid", Value: in.Token, Path: "/"})
}

type fakeMFA struct {
	authn.MFA
	log     *callLog
	factors []authn.FactorRecord
	err     error
}

func (f *fakeMFA) Factors(_ context.Context, id authn.AccountID) ([]authn.FactorRecord, error) {
	f.log.add("mfa.Factors %s", id)
	return f.factors, f.err
}

type throttleCall struct {
	scope authn.ThrottleScope
	key   string
}

type fakeThrottle struct {
	log        *callLog
	wait       time.Duration
	engaged    bool
	checkErr   error
	failErr    error
	succeedErr error
	checks     []throttleCall
	fails      []throttleCall
	succeeds   []throttleCall
}

func (f *fakeThrottle) Check(_ context.Context, scope authn.ThrottleScope, key string) (time.Duration, error) {
	f.log.add("throttle.Check")
	f.checks = append(f.checks, throttleCall{scope, key})
	return f.wait, f.checkErr
}

func (f *fakeThrottle) Fail(_ context.Context, scope authn.ThrottleScope, key string) (bool, error) {
	f.log.add("throttle.Fail")
	f.fails = append(f.fails, throttleCall{scope, key})
	return f.engaged, f.failErr
}

func (f *fakeThrottle) Succeed(_ context.Context, scope authn.ThrottleScope, key string) error {
	f.log.add("throttle.Succeed")
	f.succeeds = append(f.succeeds, throttleCall{scope, key})
	return f.succeedErr
}

type verifyCall struct {
	challenge, code string
}

type fakeSMS struct {
	log       *callLog
	sent      []authn.SendCode
	verified  []verifyCall
	sendErr   error
	verifyErr error
	next      int
	onSend    func() // runs inside Send, for a test that lets time pass there
}

func (f *fakeSMS) Send(_ context.Context, in authn.SendCode) (string, error) {
	f.log.add("sms.Send")
	f.sent = append(f.sent, in)
	if f.onSend != nil {
		f.onSend()
	}
	if f.sendErr != nil {
		return "", f.sendErr
	}
	f.next++
	return fmt.Sprintf("challenge-%d", f.next), nil
}

func (f *fakeSMS) Verify(_ context.Context, challengeID, code string) error {
	f.log.add("sms.Verify")
	f.verified = append(f.verified, verifyCall{challengeID, code})
	return f.verifyErr
}

type sendEmailCall struct {
	to, code, locale string
	ttl              time.Duration
}

// fakeEmail stands in for the email code channel. It records the messages it was
// handed and fails on demand.
type fakeEmail struct {
	log  *callLog
	sent []sendEmailCall
	err  error
}

func (f *fakeEmail) SendCode(_ context.Context, to, code string, ttl time.Duration, locale string) error {
	f.log.add("email.SendCode")
	f.sent = append(f.sent, sendEmailCall{to: to, code: code, locale: locale, ttl: ttl})
	return f.err
}

// fakeKV is an in-memory kv.KV that keeps the expiry each key was stored with,
// and fails on demand. The operations the routes do not use are refused.
type fakeKV struct {
	log     *callLog
	mu      sync.Mutex
	values  map[string][]byte
	ttls    map[string]time.Duration
	getErr  error
	setErr  error
	delErr  error
	sets    int
	deletes int
}

var _ kv.KV = (*fakeKV)(nil)

func newFakeKV(log *callLog) *fakeKV {
	return &fakeKV{log: log, values: map[string][]byte{}, ttls: map[string]time.Duration{}}
}

func (f *fakeKV) Get(_ context.Context, key string) ([]byte, bool, error) {
	f.log.add("kv.Get")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return nil, false, f.getErr
	}
	v, ok := f.values[key]
	return append([]byte(nil), v...), ok, nil
}

func (f *fakeKV) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	f.log.add("kv.Set")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.setErr != nil {
		return f.setErr
	}
	f.sets++
	f.values[key] = append([]byte(nil), value...)
	f.ttls[key] = ttl
	return nil
}

func (f *fakeKV) Delete(_ context.Context, key string) error {
	f.log.add("kv.Delete")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.delErr != nil {
		return f.delErr
	}
	f.deletes++
	delete(f.values, key)
	delete(f.ttls, key)
	return nil
}

func (f *fakeKV) keys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for k := range f.values {
		out = append(out, k)
	}
	return out
}

var errUnused = errors.New("fakeKV: operation the routes do not use")

func (f *fakeKV) IncrByFloat(context.Context, string, float64) (float64, error) { return 0, errUnused }

func (f *fakeKV) IncrByFloatWithTTL(context.Context, string, float64, time.Duration) (float64, error) {
	return 0, errUnused
}

func (f *fakeKV) CompareAndSwap(context.Context, string, []byte, []byte) (bool, error) {
	return false, errUnused
}

func (f *fakeKV) SetIfAbsent(context.Context, string, []byte, time.Duration) (bool, error) {
	return false, errUnused
}

type fakeBus struct {
	mu     sync.Mutex
	events []event.Event
	err    error
}

func (f *fakeBus) Publish(_ context.Context, e event.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, e)
	return f.err
}

func (f *fakeBus) Subscribe(string, string, event.Handler) {}

func (f *fakeBus) published() []event.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]event.Event(nil), f.events...)
}

type fakeAudit struct {
	mu   sync.Mutex
	rows []audit.Entry
	err  error
}

func (f *fakeAudit) Record(_ context.Context, e audit.Entry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows = append(f.rows, e)
	return f.err
}

func (f *fakeAudit) entries() []audit.Entry {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]audit.Entry(nil), f.rows...)
}

// fakeSettings serves the durations a test set and an error for the keys it
// listed as unreadable.
type fakeSettings struct {
	settings.Store
	values map[string]time.Duration
	errs   map[string]error
}

func (f *fakeSettings) Get(_ settings.Layer, key string, target any) error {
	if err := f.errs[key]; err != nil {
		return err
	}
	v, ok := f.values[key]
	if !ok {
		return settings.ErrUnknownKey
	}
	dst, ok := target.(*time.Duration)
	if !ok {
		return fmt.Errorf("fakeSettings: target is %s", reflect.TypeOf(target))
	}
	*dst = v
	return nil
}
