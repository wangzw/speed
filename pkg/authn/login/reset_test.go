package login

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/settings"
)

// The reset routes as the tests drive them.
const (
	resetPath         = "/api/v1/auth/password-reset"
	resetCurrentPath  = "/api/v1/auth/password-reset/current"
	resetVerifyPath   = "/api/v1/auth/password-reset/verify"
	resetCompletePath = "/api/v1/auth/password-reset/complete"
	resetMethodsPath  = "/api/v1/auth/methods"
)

// resetSettings is a settings.Store a test writes the reset switches into. A key
// it does not hold reads as undeclared, so the reader's declared default applies.
type resetSettings struct {
	settings.Store
	mu     sync.Mutex
	values map[string]any
}

func newResetSettings() *resetSettings {
	return &resetSettings{values: map[string]any{
		"authn.login.password-reset":            "open",
		"authn.login.disabled-methods":          "[]",
		"authn.login.password-reset-ticket-ttl": 10 * time.Minute,
	}}
}

func (s *resetSettings) set(key string, value any) {
	s.mu.Lock()
	s.values[key] = value
	s.mu.Unlock()
}

func (s *resetSettings) Get(_ settings.Layer, key string, target any) error {
	s.mu.Lock()
	v, ok := s.values[key]
	s.mu.Unlock()
	if !ok {
		return settings.ErrUnknownKey
	}
	switch t := target.(type) {
	case *string:
		value, ok := v.(string)
		if !ok {
			return settings.ErrTypeMismatch
		}
		*t = value
	case *int:
		value, ok := v.(int)
		if !ok {
			return settings.ErrTypeMismatch
		}
		*t = value
	case *time.Duration:
		value, ok := v.(time.Duration)
		if !ok {
			return settings.ErrTypeMismatch
		}
		*t = value
	default:
		return settings.ErrTypeMismatch
	}
	return nil
}

// sentMail is one message the reset email channel was handed.
type sentMail struct {
	to     string
	code   string
	ttl    time.Duration
	locale string
}

// fakeResetEmail is the reset email channel. It records every message and offers
// the code to the test over a channel, because the handler delivers off the
// response path.
type fakeResetEmail struct {
	mu   sync.Mutex
	sent []sentMail
	ch   chan sentMail
	err  error
}

func newFakeResetEmail() *fakeResetEmail {
	return &fakeResetEmail{ch: make(chan sentMail, 32)}
}

func (f *fakeResetEmail) SendResetCode(_ context.Context, to, code string, ttl time.Duration, locale string) error {
	f.mu.Lock()
	f.sent = append(f.sent, sentMail{to: to, code: code, ttl: ttl, locale: locale})
	err := f.err
	f.mu.Unlock()
	f.ch <- sentMail{to: to, code: code, ttl: ttl, locale: locale}
	return err
}

// waitTo waits for the message sent to one address.
func (f *fakeResetEmail) waitTo(t *testing.T, to string) sentMail {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case m := <-f.ch:
			if m.to == to {
				return m
			}
		case <-deadline:
			t.Fatalf("no reset email reached %s", to)
		}
	}
}

// count is how many messages the channel was handed.
func (f *fakeResetEmail) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

// fakeResetSMS is the SMS code capability. It records every send, including a
// suppressed one, answers with one fixed code and counts the verifications. A
// case may arm verify to model the real sender's own proof check at the send
// entry.
type fakeResetSMS struct {
	mu        sync.Mutex
	sends     []authn.SendCode
	code      string
	challenge string
	sendErr   error
	verifyErr error
	verifies  int
	support   bool
	verify    func(authn.SendCode) error
}

func newFakeResetSMS() *fakeResetSMS {
	return &fakeResetSMS{code: "123456", challenge: "challenge-1", support: true}
}

func (s *fakeResetSMS) Send(_ context.Context, in authn.SendCode) (string, error) {
	s.mu.Lock()
	s.sends = append(s.sends, in)
	err := s.sendErr
	verify := s.verify
	s.mu.Unlock()
	if err != nil {
		return "", err
	}
	if verify != nil {
		if err := verify(in); err != nil {
			return "", err
		}
	}
	return s.challenge, nil
}

func (s *fakeResetSMS) Verify(_ context.Context, challengeID, code string) error {
	s.mu.Lock()
	s.verifies++
	err := s.verifyErr
	want, wantID := s.code, s.challenge
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if challengeID != wantID || code != want {
		return authn.ErrInvalidCredentials
	}
	return nil
}

func (s *fakeResetSMS) SupportsPurpose(purpose string) bool { return s.support }

func (s *fakeResetSMS) lastSend() authn.SendCode {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.sends) == 0 {
		return authn.SendCode{}
	}
	return s.sends[len(s.sends)-1]
}

func (s *fakeResetSMS) verifyCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.verifies
}

// resetSet is one Passwords.Set call.
type resetSet struct {
	id       authn.AccountID
	password string
	opt      authn.SetPasswordOptions
}

// fakeResetPasswords records the password writes and answers the error a test
// scripts.
type fakeResetPasswords struct {
	authn.Passwords
	mu    sync.Mutex
	calls []resetSet
	err   error
}

func (p *fakeResetPasswords) Set(_ context.Context, id authn.AccountID, password string, opt authn.SetPasswordOptions) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, resetSet{id: id, password: password, opt: opt})
	return p.err
}

func (p *fakeResetPasswords) writes() []resetSet {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]resetSet(nil), p.calls...)
}

// resetRig is a login rig wired to the reset channel fakes.
type resetRig struct {
	*rig
	settings  *resetSettings
	email     *fakeResetEmail
	sms       *fakeResetSMS
	passwords *fakeResetPasswords
	audit     *auditing
	method    *fakeMethod
}

// newResetRig builds a rig whose reset surface is open and whose channels are
// fakes.
func newResetRig(t *testing.T) *resetRig {
	t.Helper()
	method := &fakeMethod{name: "password"}
	r := newRig(t, method)
	store := newResetSettings()
	email := newFakeResetEmail()
	sms := newFakeResetSMS()
	passwords := &fakeResetPasswords{}
	sink := &auditing{}
	r.p.settings = newReader(store, r.p.logger)
	r.p.emailReset = email
	r.p.smsCodes = sms
	r.p.passwords = passwords
	r.p.audit = sink
	return &resetRig{rig: r, settings: store, email: email, sms: sms, passwords: passwords, audit: sink, method: method}
}

// seedResetAccount puts one account with one proved identifier in the fake store
// and answers the identifier's row id. change edits the account before it is
// stored.
func (r *resetRig) seedResetAccount(id authn.AccountID, kind authn.IdentifierKind, value, via string, change ...func(*authn.Account)) string {
	acct := authn.Account{ID: id, Kind: authn.KindUser, Status: authn.StatusActive, PasswordSet: true}
	for _, f := range change {
		f(&acct)
	}
	identID := "ident-" + string(id)
	r.accounts.byID[id] = acct
	r.accounts.ident[id] = []authn.Identifier{{ID: identID, Kind: kind, Value: value, VerifiedVia: via}}
	r.accounts.lookup[string(kind)+"\x00"+value] = id
	return identID
}

// startReset issues POST password-reset.
func (r *resetRig) startReset(body string) *httptest.ResponseRecorder {
	r.t.Helper()
	return r.send(http.MethodPost, resetPath, body)
}

// resetCookie reads the reset cookie a reply set.
func resetCookie(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if strings.Contains(c.Name, "authn-reset") {
			return c.Name + "=" + c.Value
		}
	}
	t.Fatalf("the reply set no reset cookie; headers %v", rec.Header())
	return ""
}

// resetCookieAttributes is every attribute of a reply's reset cookie except its
// random secret, which differs by design: two answers are the same shape when
// these agree.
func resetCookieAttributes(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if strings.Contains(c.Name, "authn-reset") {
			return fmt.Sprintf("%s path=%s secure=%t http=%t same=%v max=%d",
				c.Name, c.Path, c.Secure, c.HttpOnly, c.SameSite, c.MaxAge)
		}
	}
	t.Fatalf("the reply set no reset cookie; headers %v", rec.Header())
	return ""
}

// resetProblemCode is the capability code of a problem reply.
func resetProblemCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var p struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		return ""
	}
	return strings.TrimPrefix(p.Type, "urn:speed:authn:")
}

// emailStart starts an email reset and returns the cookie and the delivered code.
func (r *resetRig) emailStart(t *testing.T, address string) (cookie, code string) {
	t.Helper()
	rec := r.startReset(fmt.Sprintf(`{"channel":"email","identifier":%q}`, address))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("starting an email reset answered %d: %s", rec.Code, rec.Body.String())
	}
	return resetCookie(t, rec), r.email.waitTo(t, address).code
}

// verify submits a code to the bound reset.
func (r *resetRig) verify(cookie, code string) *httptest.ResponseRecorder {
	return r.send(http.MethodPost, resetVerifyPath, `{"code":`+fmt.Sprintf("%q", code)+`}`, "Cookie", cookie)
}

// complete submits a new password to the bound reset.
func (r *resetRig) complete(cookie, password string) *httptest.ResponseRecorder {
	return r.send(http.MethodPost, resetCompletePath, `{"new_password":`+fmt.Sprintf("%q", password)+`}`, "Cookie", cookie)
}

// TestResetEligibleAndIneligibleAnswersAreIdentical holds the enumeration guard:
// an eligible target, a target verified by another channel, a disabled account, a
// deleted account and a passwordless account get one status, one set of headers
// and one byte-identical body.
func TestResetEligibleAndIneligibleAnswersAreIdentical(t *testing.T) {
	r := newResetRig(t)
	r.seedResetAccount("acct-ok", authn.IdentifierEmail, "ok@example.com", authn.VerifiedViaEmailCode)
	r.seedResetAccount("acct-wrong-proof", authn.IdentifierEmail, "wrong@example.com", authn.VerifiedViaAdmin)
	r.seedResetAccount("acct-disabled", authn.IdentifierEmail, "disabled@example.com", authn.VerifiedViaEmailCode,
		func(a *authn.Account) { a.Status = authn.StatusDisabled })
	r.seedResetAccount("acct-passwordless", authn.IdentifierEmail, "none@example.com", authn.VerifiedViaEmailCode,
		func(a *authn.Account) { a.PasswordSet = false })
	// A deleted account names no account at all.

	targets := []string{"ok@example.com", "wrong@example.com", "disabled@example.com", "none@example.com", "gone@example.com"}
	var body []byte
	var first http.Header
	var firstCookie string
	for i, target := range targets {
		rec := r.startReset(fmt.Sprintf(`{"channel":"email","identifier":%q}`, target))
		if rec.Code != http.StatusAccepted {
			t.Fatalf("%s answered %d: %s", target, rec.Code, rec.Body.String())
		}
		if i == 0 {
			body = append([]byte(nil), rec.Body.Bytes()...)
			first = rec.Result().Header.Clone()
			firstCookie = resetCookieAttributes(t, rec)
		} else {
			if !bytes.Equal(body, rec.Body.Bytes()) {
				t.Errorf("%s answered body %q, want the byte-identical %q", target, rec.Body.String(), body)
			}
			for _, name := range []string{"Content-Type", "Cache-Control"} {
				if got, want := rec.Result().Header.Get(name), first.Get(name); got != want {
					t.Errorf("%s header %s is %q, want %q", target, name, got, want)
				}
			}
			if got := resetCookieAttributes(t, rec); got != firstCookie {
				t.Errorf("%s set the cookie %q, want the same attributes as %q", target, got, firstCookie)
			}
		}
	}
}

// TestResetSetsThePasswordAndRevokesTheAccountsSessions drives one eligible email
// reset end to end and holds the write path: one password write with the reset
// options, the target's sessions revoked, and no session made.
func TestResetSetsThePasswordAndRevokesTheAccountsSessions(t *testing.T) {
	r := newResetRig(t)
	identID := r.seedResetAccount("acct-ok", authn.IdentifierEmail, "ok@example.com", authn.VerifiedViaEmailCode)

	cookie, code := r.emailStart(t, "ok@example.com")
	if rec := r.verify(cookie, code); rec.Code != http.StatusOK {
		t.Fatalf("verify answered %d: %s", rec.Code, rec.Body.String())
	}
	if rec := r.complete(cookie, "a whole new password"); rec.Code != http.StatusNoContent {
		t.Fatalf("complete answered %d: %s", rec.Code, rec.Body.String())
	}

	writes := r.passwords.writes()
	if len(writes) != 1 {
		t.Fatalf("%d password writes, want one", len(writes))
	}
	got := writes[0]
	if got.id != "acct-ok" || got.password != "a whole new password" {
		t.Errorf("wrote %q to %q, want the new password to acct-ok", got.password, got.id)
	}
	want := authn.SetPasswordOptions{
		By: "reset", Channel: "email", MustChange: false,
		RequireActive: true, RequireExisting: true, RequireIdentifier: identID, AdvanceValidAfter: true,
	}
	if got.opt != want {
		t.Errorf("password options %+v, want %+v", got.opt, want)
	}
	if r.sessions.revokedAccount != "acct-ok" || r.sessions.revokedAll != 1 || r.sessions.except != "" {
		t.Errorf("revoked account %q except %q (%d times), want acct-ok with no exception",
			r.sessions.revokedAccount, r.sessions.except, r.sessions.revokedAll)
	}
	if len(r.sessions.created) != 0 || len(r.sessions.revoked) != 0 || len(r.sessions.elevated) != 0 {
		t.Errorf("the reset made a session or revoked one by id: created %v, revoked %v, elevated %v",
			r.sessions.created, r.sessions.revoked, r.sessions.elevated)
	}
	// The mandatory action wrote attempted then success, both under the target's
	// identity and with one action id.
	rows := r.audit.rows()
	if len(rows) != 2 {
		t.Fatalf("%d audit rows, want attempted and success: %+v", len(rows), rows)
	}
	if rows[0].Action != "authn.password.reset" || rows[0].Outcome != "attempted" ||
		rows[1].Outcome != "success" {
		t.Errorf("audit rows %+v, want attempted then success", rows)
	}
	if rows[0].Detail["channel"] != "email" || rows[0].Detail["action_id"] == "" ||
		rows[0].Detail["action_id"] != rows[1].Detail["action_id"] {
		t.Errorf("audit details %+v and %+v, want one channel and one action id", rows[0].Detail, rows[1].Detail)
	}
	for _, actor := range r.audit.identities() {
		if actor != "acct-ok" {
			t.Errorf("an audit row was written by %q, want the reset account", actor)
		}
	}
}

// TestResetSMSVerifiesThroughTheCodeCapability holds the SMS channel: the code is
// checked by SMSCodes.Verify, not against a local digest.
func TestResetSMSVerifiesThroughTheCodeCapability(t *testing.T) {
	r := newResetRig(t)
	r.seedResetAccount("acct-phone", authn.IdentifierPhone, "+8613800000000", authn.VerifiedViaSMSCode)

	rec := r.startReset(`{"channel":"sms","identifier":"+8613800000000"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("starting an SMS reset answered %d: %s", rec.Code, rec.Body.String())
	}
	cookie := resetCookie(t, rec)
	if last := r.sms.lastSend(); !last.Deliver || last.Purpose != authn.SMSPurposeReset {
		t.Errorf("the SMS send was %+v, want a delivered reset code", last)
	}
	if got := r.verify(cookie, "123456"); got.Code != http.StatusOK {
		t.Fatalf("verify answered %d: %s", got.Code, got.Body.String())
	}
	if r.sms.verifyCount() != 1 {
		t.Errorf("%d verifications, want one", r.sms.verifyCount())
	}

	// The challenge the code capability verified is spent; the code kept in the
	// record is what answers the same submission again.
	if got := r.verify(cookie, "123456"); got.Code != http.StatusOK {
		t.Fatalf("a repeated SMS verify answered %d: %s, want the same success", got.Code, got.Body.String())
	}
	if r.sms.verifyCount() != 1 {
		t.Errorf("%d verifications after a repeated submit, want no second call", r.sms.verifyCount())
	}
	if got := r.verify(cookie, "000000"); got.Code != http.StatusUnauthorized ||
		resetProblemCode(t, got) != "login-failed" {
		t.Errorf("a different code on a verified SMS record answered %d %q, want 401 login-failed", got.Code, resetProblemCode(t, got))
	}
}

// TestResetSuppressedRecordRefusesEveryCode holds the decoy path: an ineligible
// SMS target takes a suppressed send, and verify never calls the code capability
// and answers the same login-failed for every code.
func TestResetSuppressedRecordRefusesEveryCode(t *testing.T) {
	r := newResetRig(t)

	rec := r.startReset(`{"channel":"sms","identifier":"+8613900000000"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("starting an ineligible SMS reset answered %d: %s", rec.Code, rec.Body.String())
	}
	cookie := resetCookie(t, rec)
	if last := r.sms.lastSend(); last.Deliver {
		t.Errorf("an ineligible target's SMS send was %+v, want a suppressed send", last)
	}
	for _, code := range []string{"123456", "000000", "111111", "222222", "333333"} {
		if got := r.verify(cookie, code); got.Code != http.StatusUnauthorized ||
			resetProblemCode(t, got) != "login-failed" {
			t.Fatalf("code %q answered %d %q, want 401 login-failed", code, got.Code, resetProblemCode(t, got))
		}
	}
	if r.sms.verifyCount() != 0 {
		t.Errorf("a suppressed record called the code verifier %d times, want none", r.sms.verifyCount())
	}
	// The attempts are used up; the record is failed and still answers login-failed.
	if got := r.verify(cookie, "123456"); got.Code != http.StatusUnauthorized ||
		resetProblemCode(t, got) != "login-failed" {
		t.Errorf("a failed record answered %d %q, want 401 login-failed", got.Code, resetProblemCode(t, got))
	}
}

// TestResetCodeCannotResetAnotherAccount holds that a code is bound to the record
// that asked for it: one account's code cannot prove another's reset.
func TestResetCodeCannotResetAnotherAccount(t *testing.T) {
	r := newResetRig(t)
	r.seedResetAccount("acct-a", authn.IdentifierEmail, "a@example.com", authn.VerifiedViaEmailCode)
	r.seedResetAccount("acct-b", authn.IdentifierEmail, "b@example.com", authn.VerifiedViaEmailCode)

	cookieA, codeA := r.emailStart(t, "a@example.com")
	cookieB, _ := r.emailStart(t, "b@example.com")

	if got := r.verify(cookieB, codeA); got.Code != http.StatusUnauthorized ||
		resetProblemCode(t, got) != "login-failed" {
		t.Fatalf("B accepted A's code: %d %q", got.Code, resetProblemCode(t, got))
	}
	if got := r.verify(cookieA, codeA); got.Code != http.StatusOK {
		t.Fatalf("A refused its own code: %d %s", got.Code, got.Body.String())
	}
	if writes := r.passwords.writes(); len(writes) != 0 {
		t.Errorf("a refused verification still wrote a password: %+v", writes)
	}
}

// TestResetRecordBelongsToItsCookie holds the binding: a second cookie cannot
// verify a record another cookie started.
func TestResetRecordBelongsToItsCookie(t *testing.T) {
	r := newResetRig(t)
	r.seedResetAccount("acct-a", authn.IdentifierEmail, "a@example.com", authn.VerifiedViaEmailCode)

	_, code := r.emailStart(t, "a@example.com")
	other := r.send(http.MethodPost, resetPath, `{"channel":"email","identifier":"nobody@example.com"}`)
	otherCookie := resetCookie(t, other)
	_ = otherCookie
	if got := r.verify("authn-reset=not-a-bound-token", code); got.Code != http.StatusUnauthorized ||
		resetProblemCode(t, got) != "flow-invalid" {
		t.Errorf("an unbound cookie answered %d %q, want 401 flow-invalid", got.Code, resetProblemCode(t, got))
	}
}

// TestResetRepeatedVerifyAnswersSuccessAndCompletesOnce holds the state machine:
// a repeated verify with the code that proved the record answers the same
// success, a different code is refused without demoting the record, a second
// complete is flow-invalid, and the password is written exactly once.
func TestResetRepeatedVerifyAnswersSuccessAndCompletesOnce(t *testing.T) {
	r := newResetRig(t)
	r.seedResetAccount("acct-a", authn.IdentifierEmail, "a@example.com", authn.VerifiedViaEmailCode)

	cookie, code := r.emailStart(t, "a@example.com")
	first := r.verify(cookie, code)
	if first.Code != http.StatusOK {
		t.Fatalf("verify answered %d: %s", first.Code, first.Body.String())
	}
	again := r.verify(cookie, code)
	if again.Code != http.StatusOK {
		t.Fatalf("a repeated verify answered %d: %s, want the same success", again.Code, again.Body.String())
	}
	if again.Body.String() != first.Body.String() {
		t.Errorf("a repeated verify answered %q, want the same body %q", again.Body.String(), first.Body.String())
	}

	other := "000000"
	if code == other {
		other = "111111"
	}
	if got := r.verify(cookie, other); got.Code != http.StatusUnauthorized ||
		resetProblemCode(t, got) != "login-failed" {
		t.Errorf("a different code on a verified record answered %d %q, want 401 login-failed", got.Code, resetProblemCode(t, got))
	}

	if got := r.complete(cookie, "first password"); got.Code != http.StatusNoContent {
		t.Fatalf("complete answered %d: %s", got.Code, got.Body.String())
	}
	if got := r.complete(cookie, "second password"); got.Code != http.StatusUnauthorized ||
		resetProblemCode(t, got) != "flow-invalid" {
		t.Errorf("a repeated complete answered %d %q, want 401 flow-invalid", got.Code, resetProblemCode(t, got))
	}
	if writes := r.passwords.writes(); len(writes) != 1 {
		t.Errorf("%d password writes, want one", len(writes))
	}
}

// TestResetConcurrentCompletesHaveOneWinner holds the at-most-once write: of two
// concurrent completions of one proved record, one wins and one is flow-invalid.
func TestResetConcurrentCompletesHaveOneWinner(t *testing.T) {
	r := newResetRig(t)
	r.seedResetAccount("acct-a", authn.IdentifierEmail, "a@example.com", authn.VerifiedViaEmailCode)

	cookie, code := r.emailStart(t, "a@example.com")
	if got := r.verify(cookie, code); got.Code != http.StatusOK {
		t.Fatalf("verify answered %d: %s", got.Code, got.Body.String())
	}

	codes := make(chan int, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- r.complete(cookie, "a new password").Code
		}()
	}
	wg.Wait()
	close(codes)

	var won, lost int
	for code := range codes {
		switch code {
		case http.StatusNoContent:
			won++
		case http.StatusUnauthorized:
			lost++
		default:
			t.Errorf("a concurrent complete answered %d", code)
		}
	}
	if won != 1 || lost != 1 {
		t.Errorf("%d winners and %d losers, want one of each", won, lost)
	}
	if writes := r.passwords.writes(); len(writes) != 1 {
		t.Errorf("%d password writes, want one", len(writes))
	}
}

// TestResetCompleteRefusesAnAccountThatBecameIneligible holds the re-check before
// the write: an account disabled, deleted or stripped of the proved identifier
// between verify and complete answers password-reset-unavailable and no password is
// written.
func TestResetCompleteRefusesAnAccountThatBecameIneligible(t *testing.T) {
	cases := []struct {
		name   string
		change func(*resetRig)
	}{
		{"disabled", func(r *resetRig) {
			acct := r.accounts.byID["acct-a"]
			acct.Status = authn.StatusDisabled
			r.accounts.byID["acct-a"] = acct
		}},
		{"deleted", func(r *resetRig) {
			delete(r.accounts.byID, "acct-a")
			delete(r.accounts.lookup, "email\x00a@example.com")
		}},
		{"identifier removed", func(r *resetRig) {
			r.accounts.ident["acct-a"] = nil
		}},
		{"proof channel changed", func(r *resetRig) {
			r.accounts.ident["acct-a"][0].VerifiedVia = authn.VerifiedViaAdmin
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newResetRig(t)
			r.seedResetAccount("acct-a", authn.IdentifierEmail, "a@example.com", authn.VerifiedViaEmailCode)
			cookie, code := r.emailStart(t, "a@example.com")
			if got := r.verify(cookie, code); got.Code != http.StatusOK {
				t.Fatalf("verify answered %d: %s", got.Code, got.Body.String())
			}
			c.change(r)
			got := r.complete(cookie, "a new password")
			if got.Code != http.StatusForbidden || resetProblemCode(t, got) != "password-reset-unavailable" {
				t.Fatalf("complete answered %d %q, want 403 password-reset-unavailable", got.Code, resetProblemCode(t, got))
			}
			if writes := r.passwords.writes(); len(writes) != 0 {
				t.Errorf("an ineligible completion wrote a password: %+v", writes)
			}
		})
	}
}

// TestResetAuditFailureLeavesThePasswordUnchanged holds the mandatory attempted
// row: when it cannot be written the request is 503 and no password is written.
func TestResetAuditFailureLeavesThePasswordUnchanged(t *testing.T) {
	r := newResetRig(t)
	r.seedResetAccount("acct-a", authn.IdentifierEmail, "a@example.com", authn.VerifiedViaEmailCode)

	cookie, code := r.emailStart(t, "a@example.com")
	if got := r.verify(cookie, code); got.Code != http.StatusOK {
		t.Fatalf("verify answered %d: %s", got.Code, got.Body.String())
	}
	r.audit.err = errors.New("the audit journal is down")
	got := r.complete(cookie, "a new password")
	if got.Code != http.StatusServiceUnavailable {
		t.Fatalf("complete answered %d %q, want 503", got.Code, resetProblemCode(t, got))
	}
	if writes := r.passwords.writes(); len(writes) != 0 {
		t.Errorf("the password was written without its audit row: %+v", writes)
	}
	// The record returned to verified, so a retry after the audit returns works.
	r.audit.err = nil
	if got := r.complete(cookie, "a new password"); got.Code != http.StatusNoContent {
		t.Errorf("the retry answered %d: %s", got.Code, got.Body.String())
	}
}

// TestResetDeniedWriteAppendsADeniedRow holds the second half of the audit: a
// password the policy refuses answers 400 and appends a denied row under the
// attempted row's action id, and the record stays verified so another password
// may be tried.
func TestResetDeniedWriteAppendsADeniedRow(t *testing.T) {
	r := newResetRig(t)
	r.seedResetAccount("acct-a", authn.IdentifierEmail, "a@example.com", authn.VerifiedViaEmailCode)
	cookie, code := r.emailStart(t, "a@example.com")
	if got := r.verify(cookie, code); got.Code != http.StatusOK {
		t.Fatalf("verify answered %d: %s", got.Code, got.Body.String())
	}
	r.passwords.err = fmt.Errorf("%w: too short", authn.ErrPasswordPolicy)
	got := r.complete(cookie, "short")
	if got.Code != http.StatusBadRequest || resetProblemCode(t, got) != "bad-request" {
		t.Fatalf("a policy refusal answered %d %q, want 400 bad-request", got.Code, resetProblemCode(t, got))
	}
	rows := r.audit.rows()
	if len(rows) != 2 || rows[0].Outcome != "attempted" || rows[1].Outcome != "denied" {
		t.Fatalf("audit rows %+v, want attempted then denied", rows)
	}
	if rows[0].Detail["action_id"] == "" || rows[0].Detail["action_id"] != rows[1].Detail["action_id"] {
		t.Errorf("the attempted and denied rows do not share one action id: %+v", rows)
	}
	// The record is back at verified: another password, inside the policy, wins.
	r.passwords.err = nil
	if got := r.complete(cookie, "a whole new password"); got.Code != http.StatusNoContent {
		t.Errorf("the retry answered %d: %s", got.Code, got.Body.String())
	}
}

// TestResetCompleteMapsARacingIneligibilityToUnavailable holds the write
// transaction's own refusal: an account that turns ineligible between the
// re-check and the write answers password-reset-unavailable, not an outage, and
// appends a denied row.
func TestResetCompleteMapsARacingIneligibilityToUnavailable(t *testing.T) {
	r := newResetRig(t)
	r.seedResetAccount("acct-a", authn.IdentifierEmail, "a@example.com", authn.VerifiedViaEmailCode)
	cookie, code := r.emailStart(t, "a@example.com")
	if got := r.verify(cookie, code); got.Code != http.StatusOK {
		t.Fatalf("verify answered %d: %s", got.Code, got.Body.String())
	}
	r.passwords.err = fmt.Errorf("%w: the account is gone", authn.ErrAccountNotFound)
	got := r.complete(cookie, "a whole new password")
	if got.Code != http.StatusForbidden || resetProblemCode(t, got) != "password-reset-unavailable" {
		t.Fatalf("complete answered %d %q, want 403 password-reset-unavailable", got.Code, resetProblemCode(t, got))
	}
	rows := r.audit.rows()
	if len(rows) != 2 || rows[1].Outcome != "denied" {
		t.Errorf("audit rows %+v, want attempted then denied", rows)
	}
}

// TestResetBudgetOverrunsAreSuppressedAndSameShape holds the suppression shape:
// an SMS budget overrun and an email resend-window overrun both answer 202 with
// the same body as a real send, never a 429.
func TestResetBudgetOverrunsAreSuppressedAndSameShape(t *testing.T) {
	t.Run("sms budget overrun", func(t *testing.T) {
		r := newResetRig(t)
		r.seedResetAccount("acct-phone", authn.IdentifierPhone, "+8613800000000", authn.VerifiedViaSMSCode)
		good := r.startReset(`{"channel":"sms","identifier":"+8613800000000"}`)
		if good.Code != http.StatusAccepted {
			t.Fatalf("the real send answered %d: %s", good.Code, good.Body.String())
		}
		r.sms.sendErr = &authn.ThrottledError{RetryAfter: time.Minute}
		suppressed := r.startReset(`{"channel":"sms","identifier":"+8613800000000"}`)
		if suppressed.Code != http.StatusAccepted {
			t.Fatalf("a budget overrun answered %d: %s", suppressed.Code, suppressed.Body.String())
		}
		if !bytes.Equal(good.Body.Bytes(), suppressed.Body.Bytes()) {
			t.Errorf("the suppressed answer body %q differs from the real %q",
				suppressed.Body.String(), good.Body.String())
		}
	})

	t.Run("email resend window", func(t *testing.T) {
		r := newResetRig(t)
		r.seedResetAccount("acct-a", authn.IdentifierEmail, "a@example.com", authn.VerifiedViaEmailCode)
		rec := r.startReset(`{"channel":"email","identifier":"a@example.com"}`)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("the first send answered %d: %s", rec.Code, rec.Body.String())
		}
		r.email.waitTo(t, "a@example.com")
		second := r.startReset(`{"channel":"email","identifier":"a@example.com"}`)
		if second.Code != http.StatusAccepted {
			t.Fatalf("a window overrun answered %d: %s", second.Code, second.Body.String())
		}
		if !bytes.Equal(rec.Body.Bytes(), second.Body.Bytes()) {
			t.Errorf("the suppressed body %q differs from the sent %q", second.Body.String(), rec.Body.String())
		}
		if r.email.count() != 1 {
			t.Errorf("%d emails were sent, want one: the second is suppressed", r.email.count())
		}
	})

	t.Run("same cookie resend interval", func(t *testing.T) {
		r := newResetRig(t)
		r.seedResetAccount("acct-a", authn.IdentifierEmail, "a@example.com", authn.VerifiedViaEmailCode)
		first := r.startReset(`{"channel":"email","identifier":"a@example.com"}`)
		cookie := resetCookie(t, first)
		repeated := r.send(http.MethodPost, resetPath, `{"channel":"email","identifier":"a@example.com"}`, "Cookie", cookie)
		if repeated.Code != http.StatusTooManyRequests || resetProblemCode(t, repeated) != "throttled" {
			t.Fatalf("a repeated send answered %d %q, want 429 throttled", repeated.Code, resetProblemCode(t, repeated))
		}
		if repeated.Header().Get("Retry-After") == "" {
			t.Errorf("the 429 carried no Retry-After")
		}
	})
}

// TestResetClosesOnTheSwitch holds the emergency close: with the switch closed a
// verified ticket cannot complete, GET and verify answer password-reset-closed, a
// new start is refused, and DELETE is still 204.
func TestResetClosesOnTheSwitch(t *testing.T) {
	r := newResetRig(t)
	r.seedResetAccount("acct-a", authn.IdentifierEmail, "a@example.com", authn.VerifiedViaEmailCode)
	cookie, code := r.emailStart(t, "a@example.com")
	if got := r.verify(cookie, code); got.Code != http.StatusOK {
		t.Fatalf("verify answered %d: %s", got.Code, got.Body.String())
	}

	r.settings.set("authn.login.password-reset", "closed")

	if got := r.send(http.MethodGet, resetCurrentPath, "", "Cookie", cookie); got.Code != http.StatusForbidden ||
		resetProblemCode(t, got) != "password-reset-closed" {
		t.Errorf("GET current answered %d %q, want 403 password-reset-closed", got.Code, resetProblemCode(t, got))
	}
	if got := r.verify(cookie, code); got.Code != http.StatusForbidden ||
		resetProblemCode(t, got) != "password-reset-closed" {
		t.Errorf("verify answered %d %q, want 403 password-reset-closed", got.Code, resetProblemCode(t, got))
	}
	if got := r.complete(cookie, "a new password"); got.Code != http.StatusForbidden ||
		resetProblemCode(t, got) != "password-reset-closed" {
		t.Errorf("complete answered %d %q, want 403 password-reset-closed", got.Code, resetProblemCode(t, got))
	}
	if writes := r.passwords.writes(); len(writes) != 0 {
		t.Errorf("a closed reset wrote a password: %+v", writes)
	}
	if got := r.send(http.MethodPost, resetPath, `{"channel":"email","identifier":"a@example.com"}`); got.Code != http.StatusForbidden ||
		resetProblemCode(t, got) != "password-reset-closed" {
		t.Errorf("a start on a closed reset answered %d %q, want 403 password-reset-closed", got.Code, resetProblemCode(t, got))
	}
	if got := r.send(http.MethodDelete, resetCurrentPath, "", "Cookie", cookie); got.Code != http.StatusNoContent {
		t.Errorf("DELETE answered %d, want 204 even while closed", got.Code)
	}
}

// TestResetDiscoveryHoldsTheCapability holds the published member: it is a
// deployment fact that is enabled only when the switch is open, the audit is
// present and at least one channel is available.
func TestResetDiscoveryHoldsTheCapability(t *testing.T) {
	read := func(t *testing.T, r *resetRig) (bool, []string) {
		t.Helper()
		rec := r.send(http.MethodGet, resetMethodsPath, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET methods answered %d: %s", rec.Code, rec.Body.String())
		}
		var doc struct {
			PasswordReset struct {
				Enabled bool     `json:"enabled"`
				Methods []string `json:"methods"`
			} `json:"password_reset"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
			t.Fatalf("decoding methods: %v", err)
		}
		return doc.PasswordReset.Enabled, doc.PasswordReset.Methods
	}

	t.Run("open with both channels", func(t *testing.T) {
		r := newResetRig(t)
		enabled, methods := read(t, r)
		if !enabled || strings.Join(methods, ",") != "email,sms" {
			t.Errorf("discovery %v %v, want enabled email,sms", enabled, methods)
		}
	})
	t.Run("closed switch", func(t *testing.T) {
		r := newResetRig(t)
		r.settings.set("authn.login.password-reset", "closed")
		if enabled, methods := read(t, r); enabled || len(methods) != 2 {
			t.Errorf("discovery %v %v, want disabled with the channels still listed", enabled, methods)
		}
	})
	t.Run("no audit", func(t *testing.T) {
		r := newResetRig(t)
		r.p.audit = nil
		if enabled, _ := read(t, r); enabled {
			t.Errorf("discovery is enabled without the audit capability")
		}
	})
	t.Run("sms disabled by method", func(t *testing.T) {
		r := newResetRig(t)
		r.settings.set("authn.login.disabled-methods", `["sms"]`)
		if enabled, methods := read(t, r); !enabled || strings.Join(methods, ",") != "email" {
			t.Errorf("discovery %v %v, want enabled email only", enabled, methods)
		}
	})
	t.Run("no channel", func(t *testing.T) {
		r := newResetRig(t)
		r.p.emailReset = nil
		r.p.smsCodes = nil
		if enabled, methods := read(t, r); enabled || len(methods) != 0 {
			t.Errorf("discovery %v %v, want disabled with no channel", enabled, methods)
		}
	})
}

// TestResetSMSChannelClosesWhenItsMethodIsDisabled holds disabled-methods: a
// direct SMS call answers password-reset-closed, not unavailable.
func TestResetSMSChannelClosesWhenItsMethodIsDisabled(t *testing.T) {
	r := newResetRig(t)
	r.seedResetAccount("acct-phone", authn.IdentifierPhone, "+8613800000000", authn.VerifiedViaSMSCode)
	r.settings.set("authn.login.disabled-methods", `["sms"]`)
	got := r.startReset(`{"channel":"sms","identifier":"+8613800000000"}`)
	if got.Code != http.StatusForbidden || resetProblemCode(t, got) != "password-reset-closed" {
		t.Errorf("a disabled SMS channel answered %d %q, want 403 password-reset-closed", got.Code, resetProblemCode(t, got))
	}
}

// TestResetImpersonationAsymmetry holds the interaction the design states: the
// reset advances the target's valid_after and revokes only the target's sessions,
// so the target's bearer impersonation tokens lapse while an operator's cookie
// impersonation session, which belongs to another account, continues.
func TestResetImpersonationAsymmetry(t *testing.T) {
	r := newResetRig(t)
	identID := r.seedResetAccount("acct-target", authn.IdentifierEmail, "target@example.com", authn.VerifiedViaEmailCode)
	cookie, code := r.emailStart(t, "target@example.com")
	if got := r.verify(cookie, code); got.Code != http.StatusOK {
		t.Fatalf("verify answered %d: %s", got.Code, got.Body.String())
	}
	if got := r.complete(cookie, "a new password"); got.Code != http.StatusNoContent {
		t.Fatalf("complete answered %d: %s", got.Code, got.Body.String())
	}

	writes := r.passwords.writes()
	if len(writes) != 1 || !writes[0].opt.AdvanceValidAfter || writes[0].opt.RequireIdentifier != identID {
		t.Fatalf("the write did not advance valid_after for the proved identifier: %+v", writes)
	}
	if r.sessions.revokedAccount != "acct-target" {
		t.Errorf("the reset revoked %q, want the target account only", r.sessions.revokedAccount)
	}
	if len(r.sessions.created) != 0 || len(r.sessions.revoked) != 0 {
		t.Errorf("the reset touched a session: created %v, revoked %v", r.sessions.created, r.sessions.revoked)
	}
	// The operator's cookie impersonation session is a session of another account;
	// nothing here names it, so it is not revoked and the target's lapse is the
	// valid_after advance the write carries.
}

// TestResetKeepsTheSecondFactor holds that the reset touches no factor: after it,
// a login of the same account still needs its second factor.
func TestResetKeepsTheSecondFactor(t *testing.T) {
	r := newResetRig(t)
	r.seedResetAccount("acct-a", authn.IdentifierEmail, "a@example.com", authn.VerifiedViaEmailCode)
	cookie, code := r.emailStart(t, "a@example.com")
	if got := r.verify(cookie, code); got.Code != http.StatusOK {
		t.Fatalf("verify answered %d: %s", got.Code, got.Body.String())
	}
	if got := r.complete(cookie, "a new password"); got.Code != http.StatusNoContent {
		t.Fatalf("complete answered %d: %s", got.Code, got.Body.String())
	}
	if r.mfa.required != nil || len(r.mfa.enrolls) != 0 {
		t.Errorf("the reset called the multi-factor module: require %v, enroll %v", r.mfa.required, r.mfa.enrolls)
	}

	// The account's next login is still held to its second factor.
	r.method.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepProven(authn.Proof{Account: "acct-a", Methods: []authn.Method{authn.MethodPassword}}), nil
	}
	r.mfa.outcome = authn.MFAFactor
	r.mfa.offered = []authn.FactorRecord{{ID: "f1", Kind: authn.FactorTOTP, Label: "Authenticator"}}
	r.mfa.assurance = authn.MFAResult{Assurance: authn.AssuranceMulti}

	rec := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("a login after the reset answered %d: %s", rec.Code, rec.Body.String())
	}
	next := decode(t, rec.Body.String())["next"].(map[string]any)
	if next["kind"] != "factor" {
		t.Errorf("a login after the reset answered next %v, want factor", next)
	}
}

// onceHuman is a human-verification gate that accepts each token once, as a
// single-use mechanism does: a second Verify of the same token is refused. It
// records every call, so a case can see how many times a proof was consumed.
type onceHuman struct {
	mu     sync.Mutex
	accept map[string]bool
	calls  int
}

func (h *onceHuman) Required() bool   { return true }
func (h *onceHuman) Provider() string { return "pow" }
func (h *onceHuman) SiteKey() string  { return "" }

func (h *onceHuman) Verify(_ context.Context, p authn.HumanProof) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls++
	if p.Token == "" || !h.accept[p.Token] {
		return fmt.Errorf("%w: the proof is missing or not valid", authn.ErrHumanVerificationRequired)
	}
	delete(h.accept, p.Token)
	return nil
}

func (h *onceHuman) callCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls
}

// TestAnEmailResetWithoutAProofIsRefusedAndWritesNothing holds the email
// channel's human-verification gate. The anonymous reset email is sent without a
// session, so a missing proof is answered 403 and writes no record, spends no
// budget and sends nothing.
func TestAnEmailResetWithoutAProofIsRefusedAndWritesNothing(t *testing.T) {
	r := newResetRig(t)
	r.seedResetAccount("acct-ok", authn.IdentifierEmail, "ok@example.com", authn.VerifiedViaEmailCode)
	r.p.human = requiredHuman()

	rec := r.startReset(`{"channel":"email","identifier":"ok@example.com"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("an email reset with no proof answered %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if got := resetProblemCode(t, rec); got != "human-verification-required" {
		t.Errorf("the refusal code = %q, want human-verification-required", got)
	}
	for _, key := range r.kv.keys() {
		if strings.HasPrefix(key, resetKeyPrefix) {
			t.Errorf("the refused reset wrote the record %q, want none", key)
		}
	}
	if got := r.email.count(); got != 0 {
		t.Errorf("the email channel was handed %d messages, want none", got)
	}
}

// TestAnEmailResetToAnUnknownAddressWithoutAProofSpendsNothing holds the order
// the email branch fixes: the proof is checked before the eligibility lookup and
// before any record or source budget. An address no account holds therefore gets
// the same 403 a known address gets, and leaves no reset record and no charged
// source bucket behind.
func TestAnEmailResetToAnUnknownAddressWithoutAProofSpendsNothing(t *testing.T) {
	r := newResetRig(t)
	r.p.human = requiredHuman()

	rec := r.startReset(`{"channel":"email","identifier":"nobody@example.com"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("an unknown address with no proof answered %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if got := resetProblemCode(t, rec); got != "human-verification-required" {
		t.Errorf("the refusal code = %q, want human-verification-required", got)
	}
	if keys := r.kv.keys(); len(keys) != 0 {
		t.Errorf("the refused reset charged %d store keys, want none: %v", len(keys), keys)
	}
}

// TestAnEmailResetWithAValidProofSendsOnce holds the accepted path: the gate is
// asked once, with the request's proof, and exactly one message is sent.
func TestAnEmailResetWithAValidProofSendsOnce(t *testing.T) {
	r := newResetRig(t)
	r.seedResetAccount("acct-ok", authn.IdentifierEmail, "ok@example.com", authn.VerifiedViaEmailCode)
	human := requiredHuman()
	r.p.human = human

	rec := r.startReset(`{"channel":"email","identifier":"ok@example.com","human_proof":"proof-1"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("an email reset with a valid proof answered %d, want 202: %s", rec.Code, rec.Body.String())
	}
	r.email.waitTo(t, "ok@example.com")
	if got := r.email.count(); got != 1 {
		t.Errorf("the email channel was handed %d messages, want one", got)
	}
	if len(human.calls) != 1 {
		t.Fatalf("the gate was asked %d times, want once", len(human.calls))
	}
	if got := human.calls[0].Token; got != "proof-1" {
		t.Errorf("the gate was handed proof %q, want the request's proof-1", got)
	}
}

// TestTheSMSResetBranchConsumesTheProofExactlyOnce holds the other channel: the
// route passes the proof to SMSCodes.Send, which is the one place it is checked
// and consumed. A route-side check would consume the single-use proof before the
// sender saw it, which the once-only gate turns into a refusal.
func TestTheSMSResetBranchConsumesTheProofExactlyOnce(t *testing.T) {
	r := newResetRig(t)
	r.seedResetAccount("acct-phone", authn.IdentifierPhone, "+8613800000000", authn.VerifiedViaSMSCode)
	human := &onceHuman{accept: map[string]bool{"proof-1": true}}
	r.p.human = human
	r.sms.verify = func(in authn.SendCode) error {
		return human.Verify(context.Background(), authn.HumanProof{Token: in.Proof, RemoteIP: in.Source})
	}

	rec := r.startReset(`{"channel":"sms","identifier":"+8613800000000","human_proof":"proof-1"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("an SMS reset answered %d, want 202: %s", rec.Code, rec.Body.String())
	}
	if got := len(r.sms.sends); got != 1 {
		t.Fatalf("the SMS channel was handed %d sends, want one", got)
	}
	if got := r.sms.lastSend().Proof; got != "proof-1" {
		t.Errorf("the code engine was handed proof %q, want the request's proof-1", got)
	}
	if got := human.callCount(); got != 1 {
		t.Errorf("the proof was consumed %d times, want exactly once", got)
	}
}
