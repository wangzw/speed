package sms

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/step"
)

const (
	testAccount  = authn.AccountID("acct_0001")
	phoneNumber  = "+8613800138000"
	otherPhone   = "+8613900139000"
	goodCode     = "135790"
	challengeID  = "chal-S3nt1nel-0001"
	unknownPhone = "+8613700137000"
)

type fakeSMSCodes struct {
	authn.SMSCodes // the methods this module does not use

	sendChallenge string
	sendErr       error
	sends         []authn.SendCode

	verifyErr error
	verifies  []verifyCall
}

type verifyCall struct{ challenge, code string }

func (f *fakeSMSCodes) Send(_ context.Context, in authn.SendCode) (string, error) {
	f.sends = append(f.sends, in)
	if f.sendErr != nil {
		return "", f.sendErr
	}
	return f.sendChallenge, nil
}

func (f *fakeSMSCodes) Verify(_ context.Context, challenge, code string) error {
	f.verifies = append(f.verifies, verifyCall{challenge: challenge, code: code})
	return f.verifyErr
}

type fakeThrottle struct {
	authn.Throttle

	wait     time.Duration
	checkErr error
	onCheck  func() // runs as Check is entered, so a case can move the call under the method

	checks   []string
	fails    []string
	succeeds []string
}

func (f *fakeThrottle) Check(_ context.Context, scope authn.ThrottleScope, key string) (time.Duration, error) {
	f.checks = append(f.checks, string(scope)+":"+key)
	if f.onCheck != nil {
		f.onCheck()
	}
	return f.wait, f.checkErr
}

func (f *fakeThrottle) Fail(_ context.Context, scope authn.ThrottleScope, key string) (bool, error) {
	f.fails = append(f.fails, string(scope)+":"+key)
	return false, nil
}

func (f *fakeThrottle) Succeed(_ context.Context, scope authn.ThrottleScope, key string) error {
	f.succeeds = append(f.succeeds, string(scope)+":"+key)
	return nil
}

type fakeAccounts struct {
	authn.Accounts

	lookupID  authn.AccountID
	lookupErr error
	getErr    error
	accounts  map[authn.AccountID]authn.Account

	identifiers    map[authn.AccountID][]authn.Identifier
	identifiersErr error

	lookups      []string
	gets         []authn.AccountID
	identifierOf []authn.AccountID
}

func (f *fakeAccounts) Lookup(_ context.Context, kind authn.IdentifierKind, value string) (authn.AccountID, error) {
	f.lookups = append(f.lookups, string(kind)+":"+value)
	return f.lookupID, f.lookupErr
}

func (f *fakeAccounts) Get(_ context.Context, id authn.AccountID) (authn.Account, error) {
	f.gets = append(f.gets, id)
	return f.accounts[id], f.getErr
}

func (f *fakeAccounts) Identifiers(_ context.Context, id authn.AccountID) ([]authn.Identifier, error) {
	f.identifierOf = append(f.identifierOf, id)
	if f.identifiersErr != nil {
		return nil, f.identifiersErr
	}
	if f.identifiers != nil {
		return f.identifiers[id], nil
	}
	if id == f.lookupID && id != "" {
		return []authn.Identifier{{ID: "ident-phone", Kind: authn.IdentifierPhone, Value: phoneNumber, SignIn: true}}, nil
	}
	return nil, nil
}

type rig struct {
	m  *method
	sm *fakeSMSCodes
	th *fakeThrottle
	ac *fakeAccounts
	st *fakeSettings
}

func newRig() *rig {
	sm := &fakeSMSCodes{sendChallenge: challengeID}
	th := &fakeThrottle{}
	ac := &fakeAccounts{}
	st := &fakeSettings{signup: "closed"}
	return &rig{m: &method{codes: sm, throttle: th, accounts: ac, store: st}, sm: sm, th: th, ac: ac, st: st}
}

func (r *rig) registerActive() {
	r.ac.lookupID = testAccount
	r.ac.accounts = map[authn.AccountID]authn.Account{
		testAccount: {ID: testAccount, Kind: authn.KindUser, Status: authn.StatusActive},
	}
}

func (r *rig) registerDisabled() {
	r.ac.lookupID = testAccount
	r.ac.accounts = map[authn.AccountID]authn.Account{
		testAccount: {ID: testAccount, Kind: authn.KindUser, Status: authn.StatusDisabled},
	}
}

func (r *rig) unregistered() {
	r.ac.lookupErr = authn.ErrAccountNotFound
}

func loginStart(fields map[string]string) authn.Start {
	return authn.Start{Purpose: authn.PurposeLogin, Fields: fields, SendsLeft: 5, Locale: "zh-CN"}
}

func submit(fields map[string]string) authn.StepInput {
	return authn.StepInput{Action: authn.InputSubmit, Fields: fields}
}

func flowAt(name string, private []byte) authn.FlowState {
	return authn.FlowState{ID: "flow-1", Purpose: authn.PurposeLogin, Step: name, Private: private, SendsLeft: 5, Locale: "zh-CN"}
}

func phoneFields(v string) map[string]string { return map[string]string{fieldPhone: v} }
func codeFields(v string) map[string]string  { return map[string]string{fieldCode: v} }

func describe(s authn.Step) string {
	var b strings.Builder
	fmt.Fprintf(&b, "kind=%s name=%q private=%q fields=%+v retry=%s proof=%+v", step.KindOf(s), step.NameOf(s),
		step.PrivateOf(s), step.FieldsOf(s), step.RetryAfterOf(s), step.ProofOf(s))
	if c := step.CauseOf(s); c != nil {
		fmt.Fprintf(&b, " cause=%v", c)
	}
	return b.String()
}

// TestThePhoneThenCodeFlowSendsOnceAndProvesThePhone walks the phone and code steps through one send and one proof.
func TestThePhoneThenCodeFlowSendsOnceAndProvesThePhone(t *testing.T) {
	r := newRig()
	r.registerActive()
	ctx := t.Context()

	asked, _ := r.m.Begin(ctx, loginStart(nil))
	if step.KindOf(asked) != step.KindPrompt || step.NameOf(asked) != stepPhone {
		t.Fatalf("Begin = %s, want a prompt for the phone", describe(asked))
	}
	if f := step.FieldsOf(asked); len(f) != 1 || f[0].Name != fieldPhone || f[0].Kind != authn.FieldPhone {
		t.Fatalf("the phone prompt asked for %+v, want one phone field", f)
	}
	if len(r.sm.sends) != 0 {
		t.Fatalf("Begin sent %d codes before the phone was given, want none", len(r.sm.sends))
	}

	answered, _ := r.m.Answer(ctx, flowAt(stepPhone, nil), submit(phoneFields(phoneNumber)))
	if step.KindOf(answered) != step.KindPrompt || step.NameOf(answered) != stepCode {
		t.Fatalf("the phone step = %s, want a prompt for the code", describe(answered))
	}
	if n := step.SendsOf(answered); n != 1 {
		t.Fatalf("the phone step reported %d sends, want 1", n)
	}
	if len(r.sm.sends) != 1 {
		t.Fatalf("the phone step sent %d codes, want one", len(r.sm.sends))
	}
	sent := r.sm.sends[0]
	if sent.Purpose != authn.SMSPurposeLogin || sent.Phone != phoneNumber || sent.Account != "" || !sent.Deliver {
		t.Fatalf("Send saw %+v, want a login send to the phone, delivered", sent)
	}
	if sent.Locale != "zh-CN" {
		t.Errorf("Send saw locale %q, want the flow's zh-CN", sent.Locale)
	}

	proven, _ := r.m.Answer(ctx, flowAt(stepCode, step.PrivateOf(answered)), submit(codeFields(goodCode)))
	if step.KindOf(proven) != step.KindProven {
		t.Fatalf("the code step = %s, want a proof", describe(proven))
	}
	p := step.ProofOf(proven)
	if p.Identifier == nil || p.Identifier.Kind != authn.IdentifierPhone || p.Identifier.Value != phoneNumber {
		t.Fatalf("the proof identified %+v, want the phone %s", p.Identifier, phoneNumber)
	}
	if len(p.Methods) != 1 || p.Methods[0] != authn.MethodSMS {
		t.Fatalf("the proof holds methods %v, want [sms]", p.Methods)
	}
	if len(r.sm.verifies) != 1 || r.sm.verifies[0].challenge != challengeID || r.sm.verifies[0].code != goodCode {
		t.Fatalf("Verify saw %+v, want the challenge and the code", r.sm.verifies)
	}
	if len(r.th.succeeds) != 1 || !strings.HasPrefix(r.th.succeeds[0], string(authn.ScopePhone)+":") {
		t.Fatalf("the phone step settled %v, want one clear under the phone scope", r.th.succeeds)
	}
}

// TestAnUnknownPrefixIsRefusedNotSent checks that a disallowed prefix is refused with unsupported-region and nothing is sent.
func TestAnUnknownPrefixIsRefusedNotSent(t *testing.T) {
	r := newRig()
	r.registerActive()
	r.sm.sendErr = authn.ErrUnsupportedRegion

	got, _ := r.m.Begin(t.Context(), loginStart(phoneFields(phoneNumber)))
	if step.KindOf(got) != step.KindRefused {
		t.Fatalf("a disallowed number = %s, want a refusal", describe(got))
	}
	if c := step.CauseOf(got); !errors.Is(c, authn.ErrUnsupportedRegion) {
		t.Fatalf("the refusal cause = %v, want ErrUnsupportedRegion", c)
	}
	if n := step.SendsOf(got); n != 0 {
		t.Fatalf("a refused send reported %d sends, want none", n)
	}
	if len(r.sm.sends) != 1 {
		t.Fatalf("the method reached Send %d times, want once: the prefix is not checked here", len(r.sm.sends))
	}
}

// TestARegisteredAndAnUnknownNumberAreAnsweredTheSame checks that a registered and an unknown number get one answer shape with registration closed.
func TestARegisteredAndAnUnknownNumberAreAnsweredTheSame(t *testing.T) {
	registered := newRig()
	registered.registerActive()
	known, _ := registered.m.Begin(t.Context(), loginStart(phoneFields(phoneNumber)))

	unknown := newRig()
	unknown.unregistered()
	held, _ := unknown.m.Begin(t.Context(), loginStart(phoneFields(unknownPhone)))

	if step.KindOf(known) != step.KindOf(held) || step.KindOf(known) != step.KindPrompt {
		t.Fatalf("answers %s and %s differ in kind, want one prompt", describe(known), describe(held))
	}
	if step.NameOf(known) != step.NameOf(held) {
		t.Fatalf("answers name steps %q and %q, want the same", step.NameOf(known), step.NameOf(held))
	}
	if len(step.FieldsOf(known)) != len(step.FieldsOf(held)) {
		t.Fatalf("answers ask for %v and %v, want the same fields", step.FieldsOf(known), step.FieldsOf(held))
	}
	if step.SendsOf(known) != 1 || step.SendsOf(held) != 1 {
		t.Fatalf("answers report %d and %d sends, want one each: a suppressed send counts as one",
			step.SendsOf(known), step.SendsOf(held))
	}
	if !registered.sm.sends[0].Deliver {
		t.Errorf("a registered, enabled number was not delivered")
	}
	if unknown.sm.sends[0].Deliver {
		t.Errorf("an unknown number with registration closed was delivered")
	}
}

// TestThePerFlowLimitStopsTheSend checks that at zero nothing is sent and the step throttles, and at one it sends once.
func TestThePerFlowLimitStopsTheSend(t *testing.T) {
	r := newRig()
	r.registerActive()
	ctx := t.Context()

	spent := loginStart(phoneFields(phoneNumber))
	spent.SendsLeft = 0
	got, _ := r.m.Begin(ctx, spent)
	if step.KindOf(got) != step.KindThrottled || step.RetryAfterOf(got) != 0 {
		t.Fatalf("a spent budget = %s, want a throttle with no retry", describe(got))
	}
	if len(r.sm.sends) != 0 {
		t.Fatalf("a spent budget reached Send %d times, want none", len(r.sm.sends))
	}

	last := loginStart(phoneFields(phoneNumber))
	last.SendsLeft = 1
	got, _ = r.m.Begin(ctx, last)
	if step.KindOf(got) != step.KindPrompt || step.SendsOf(got) != 1 {
		t.Fatalf("a budget of one = %s, want a prompt reporting one send", describe(got))
	}
}

// TestAWrongCodeAndAnUnknownNumberReadTheSame checks that both are refused with one cause.
func TestAWrongCodeAndAnUnknownNumberReadTheSame(t *testing.T) {
	r := newRig()
	r.sm.verifyErr = authn.ErrInvalidCredentials
	private := writeState(phoneNumber, challengeID)

	wrong, _ := r.m.Answer(t.Context(), flowAt(stepCode, private), submit(codeFields(badCodeForTest())))
	if step.KindOf(wrong) != step.KindRefused || !errors.Is(step.CauseOf(wrong), authn.ErrInvalidCredentials) {
		t.Fatalf("a wrong code = %s, want a refusal carrying ErrInvalidCredentials", describe(wrong))
	}
	suppressed, _ := r.m.Answer(t.Context(), flowAt(stepCode, writeState(unknownPhone, challengeID)), submit(codeFields(badCodeForTest())))
	if describe(wrong) != describe(suppressed) {
		t.Fatalf("a wrong code and a suppressed challenge read %q and %q, want one answer",
			describe(wrong), describe(suppressed))
	}
	if len(r.th.fails) == 0 || r.th.fails[0] == "" {
		t.Errorf("a refused code did not count against the phone back-off")
	}
}

// TestAPhoneTheAccountDidNotChooseDoesNotCompleteALogin holds that a held but unchosen number's correct code is refused like a wrong one.
func TestAPhoneTheAccountDidNotChooseDoesNotCompleteALogin(t *testing.T) {
	r := newRig()
	r.registerActive()
	r.ac.identifiers = map[authn.AccountID][]authn.Identifier{
		testAccount: {
			{ID: "ident-username", Kind: authn.IdentifierUsername, Value: "ada", SignIn: true},
			{ID: "ident-phone", Kind: authn.IdentifierPhone, Value: phoneNumber, SignIn: false},
		},
	}
	ctx := t.Context()
	private := writeState(phoneNumber, challengeID)

	refused, _ := r.m.Answer(ctx, flowAt(stepCode, private), submit(codeFields(goodCode)))
	if step.KindOf(refused) != step.KindRefused || !errors.Is(step.CauseOf(refused), authn.ErrInvalidCredentials) {
		t.Fatalf("an unchosen number = %s, want a refusal carrying ErrInvalidCredentials", describe(refused))
	}
	if p := step.ProofOf(refused); p.Identifier != nil {
		t.Errorf("an unchosen number reached the engine as %+v, want no proof", p)
	}
	if len(r.th.succeeds) != 0 {
		t.Errorf("an unchosen number cleared the back-off, want it left as it was")
	}

	stepUp := flowAt(stepCode, private)
	stepUp.Purpose = authn.PurposeStepUp
	proven, _ := r.m.Answer(ctx, stepUp, submit(codeFields(goodCode)))
	if step.KindOf(proven) != step.KindProven {
		t.Fatalf("a step-up with the same number = %s, want a proof", describe(proven))
	}
}

// TestTheBackOffStopsTheComparison checks that a number in back-off is answered
// without comparing a code: nothing is offered to a guess.
func TestTheBackOffStopsTheComparison(t *testing.T) {
	r := newRig()
	r.th.wait = 90 * time.Second

	got, _ := r.m.Answer(t.Context(), flowAt(stepCode, writeState(phoneNumber, challengeID)), submit(codeFields(goodCode)))
	if step.KindOf(got) != step.KindThrottled || step.RetryAfterOf(got) != 90*time.Second {
		t.Fatalf("a number in back-off = %s, want a throttle of 90s", describe(got))
	}
	if len(r.sm.verifies) != 0 {
		t.Fatalf("a number in back-off was checked %d times, want none", len(r.sm.verifies))
	}
}

// TestTheMethodServesLoginAndStepUpOnly checks that a purpose a code cannot prove
// is refused before anything is read or sent.
func TestTheMethodServesLoginAndStepUpOnly(t *testing.T) {
	r := newRig()
	r.registerActive()
	ctx := t.Context()

	for _, purpose := range []string{authn.PurposeLink, authn.PurposeEnroll, ""} {
		s := loginStart(phoneFields(phoneNumber))
		s.Purpose = purpose
		got, _ := r.m.Begin(ctx, s)
		if step.KindOf(got) != step.KindRefused {
			t.Errorf("purpose %q = %s, want a refusal", purpose, describe(got))
		}
		if kind := step.KindOf(got); kind == step.KindRefused && errors.Is(step.CauseOf(got), authn.ErrInvalidCredentials) {
			t.Errorf("purpose %q was refused as a failed credential, want a purpose refusal", purpose)
		}
	}
	if len(r.sm.sends) != 0 {
		t.Fatalf("a refused purpose reached Send %d times, want none", len(r.sm.sends))
	}
}

// TestAMalformedPhoneIsRefused checks that a non-E.164 value answers invalid-identifier, nothing sent.
func TestAMalformedPhoneIsRefused(t *testing.T) {
	r := newRig()
	for _, bad := range []string{"123", "+0123456789", "8613800138000", "+1 3800138000"} {
		got, _ := r.m.Begin(t.Context(), loginStart(phoneFields(bad)))
		if step.KindOf(got) != step.KindRefused || !errors.Is(step.CauseOf(got), authn.ErrInvalidIdentifier) {
			t.Errorf("phone %q = %s, want a refusal carrying ErrInvalidIdentifier", bad, describe(got))
		}
	}
	if len(r.sm.sends) != 0 {
		t.Fatalf("a malformed phone reached Send %d times, want none", len(r.sm.sends))
	}
}

// TestADisabledAccountIsNotDelivered checks that a number a disabled account
// holds gets a suppressed challenge, counted and answered as a real one.
func TestADisabledAccountIsNotDelivered(t *testing.T) {
	r := newRig()
	r.registerDisabled()
	got, _ := r.m.Begin(t.Context(), loginStart(phoneFields(phoneNumber)))
	if step.KindOf(got) != step.KindPrompt || step.SendsOf(got) != 1 {
		t.Fatalf("a disabled account's number = %s, want a prompt reporting one send", describe(got))
	}
	if r.sm.sends[0].Deliver {
		t.Errorf("a disabled account's number was delivered, want a suppressed send")
	}
}

// TestRegistrationOpenDeliversEveryNumber checks that an unknown number gets a real code while registration is open.
func TestRegistrationOpenDeliversEveryNumber(t *testing.T) {
	r := newRig()
	r.unregistered()
	r.st.signup = "open"
	got, _ := r.m.Begin(t.Context(), loginStart(phoneFields(unknownPhone)))
	if step.KindOf(got) != step.KindPrompt || step.SendsOf(got) != 1 {
		t.Fatalf("an unknown number with registration open = %s, want a prompt", describe(got))
	}
	if !r.sm.sends[0].Deliver {
		t.Errorf("an unknown number with registration open was suppressed, want a real send")
	}
}

// TestAnUnavailableCapabilityIsNotARefusal checks that a failed Send or read answers 503, never 401.
func TestAnUnavailableCapabilityIsNotARefusal(t *testing.T) {
	ctx := t.Context()

	unavailableSend := newRig()
	unavailableSend.registerActive()
	unavailableSend.sm.sendErr = authn.ErrUnavailable
	if got, _ := unavailableSend.m.Begin(ctx, loginStart(phoneFields(phoneNumber))); step.KindOf(got) != step.KindUnavailable {
		t.Errorf("an unavailable Send = %s, want unavailable", describe(got))
	}

	unreachableStore := newRig()
	unreachableStore.ac.lookupErr = errors.New("the account store is down")
	if got, _ := unreachableStore.m.Begin(ctx, loginStart(phoneFields(phoneNumber))); step.KindOf(got) != step.KindUnavailable {
		t.Errorf("an unreachable store = %s, want unavailable", describe(got))
	}

	brokenBackOff := newRig()
	brokenBackOff.th.checkErr = errors.New("the back-off store is down")
	if got, _ := brokenBackOff.m.Answer(ctx, flowAt(stepCode, writeState(phoneNumber, challengeID)), submit(codeFields(goodCode))); step.KindOf(got) != step.KindUnavailable {
		t.Errorf("an unreadable back-off = %s, want unavailable", describe(got))
	}
}

// TestASendThrottleCarriesItsWait checks that a Send refused as throttled is
// answered with the wait it named.
func TestASendThrottleCarriesItsWait(t *testing.T) {
	r := newRig()
	r.registerActive()
	r.sm.sendErr = &authn.ThrottledError{RetryAfter: 30 * time.Second}
	got, _ := r.m.Begin(t.Context(), loginStart(phoneFields(phoneNumber)))
	if step.KindOf(got) != step.KindThrottled || step.RetryAfterOf(got) != 30*time.Second {
		t.Fatalf("a throttled Send = %s, want a throttle of 30s", describe(got))
	}
}

// TestAnAnswerTheMethodCannotWalkIsRefused checks that a callback, an unknown step and a missing challenge are refused.
func TestAnAnswerTheMethodCannotWalkIsRefused(t *testing.T) {
	r := newRig()
	ctx := t.Context()

	callback, _ := r.m.Answer(ctx, flowAt(stepPhone, nil), authn.StepInput{Action: authn.InputCallback})
	if step.KindOf(callback) != step.KindRefused {
		t.Errorf("a callback = %s, want a refusal", describe(callback))
	}
	unknown, _ := r.m.Answer(ctx, flowAt("something-else", nil), submit(phoneFields(phoneNumber)))
	if step.KindOf(unknown) != step.KindRefused {
		t.Errorf("an unknown step = %s, want a refusal", describe(unknown))
	}
	noState, _ := r.m.Answer(ctx, flowAt(stepCode, nil), submit(codeFields(goodCode)))
	if step.KindOf(noState) != step.KindRefused {
		t.Errorf("a code step with no state = %s, want a refusal", describe(noState))
	}
	if len(r.sm.verifies) != 0 {
		t.Errorf("a refused answer reached Verify %d times, want none", len(r.sm.verifies))
	}
}

// TestACodeStepWithNoCodeAsksAgain checks that an empty submission prompts again, counting no failure and keeping the challenge state.
func TestACodeStepWithNoCodeAsksAgain(t *testing.T) {
	r := newRig()
	got, _ := r.m.Answer(t.Context(), flowAt(stepCode, writeState(phoneNumber, challengeID)), submit(codeFields("")))
	if step.KindOf(got) != step.KindPrompt || step.NameOf(got) != stepCode {
		t.Fatalf("an empty code = %s, want a fresh prompt for the code", describe(got))
	}
	if len(r.sm.verifies) != 0 || len(r.th.fails) != 0 {
		t.Errorf("an empty code was checked or counted, want neither")
	}
	// The re-prompt must carry the same challenge state, or the answer that
	// follows cannot be checked: answering the re-prompted step has to reach a
	// proof.
	private := step.PrivateOf(got)
	if private == nil {
		t.Fatalf("the re-prompt carried no state, want the number and the challenge kept")
	}
	proven, _ := r.m.Answer(t.Context(), flowAt(stepCode, private), submit(codeFields(goodCode)))
	if step.KindOf(proven) != step.KindProven {
		t.Fatalf("answering the re-prompted code step = %s, want a proof", describe(proven))
	}
}

// TestNoSecretReachesACause checks that no cause the method produces puts the
// phone number, the code or the challenge into what reaches a log.
func TestNoSecretReachesACause(t *testing.T) {
	ctx := t.Context()
	r := newRig()
	r.registerActive()

	first, _ := r.m.Begin(ctx, loginStart(nil))
	second, _ := r.m.Begin(ctx, loginStart(phoneFields(phoneNumber)))
	r.sm.sendErr = errors.New("the carrier is down")
	third, _ := r.m.Begin(ctx, loginStart(phoneFields(phoneNumber)))
	r.sm.verifyErr = authn.ErrInvalidCredentials
	fourth, _ := r.m.Answer(ctx, flowAt(stepCode, writeState(phoneNumber, challengeID)), submit(codeFields(goodCode)))

	for _, s := range []authn.Step{first, second, third, fourth} {
		c := step.CauseOf(s)
		if c == nil {
			continue
		}
		for _, secret := range []string{phoneNumber, unknownPhone, goodCode, challengeID} {
			if strings.Contains(c.Error(), secret) {
				t.Errorf("the cause %q carries the secret %q", c.Error(), secret)
			}
		}
	}
}

// TestTheMethodServesStepUpAsWellAsLogin checks that a step-up drives the flow exactly as a login does.
func TestTheMethodServesStepUpAsWellAsLogin(t *testing.T) {
	r := newRig()
	r.registerActive()
	proved := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	r.ac.identifiers = map[authn.AccountID][]authn.Identifier{
		testAccount: {{ID: "ident-phone", Kind: authn.IdentifierPhone, Value: phoneNumber, VerifiedAt: proved, VerifiedVia: authn.VerifiedViaSMSCode}},
	}
	ctx := t.Context()

	start := loginStart(phoneFields(phoneNumber))
	start.Purpose = authn.PurposeStepUp
	start.Account = testAccount
	asked, _ := r.m.Begin(ctx, start)
	if step.KindOf(asked) != step.KindPrompt || step.NameOf(asked) != "code" {
		t.Fatalf("a step-up Begin = %s, want a prompt for the code", describe(asked))
	}
	if n := step.SendsOf(asked); n != 1 {
		t.Fatalf("a step-up Begin reported %d sends, want one", n)
	}
	if len(r.sm.sends) != 1 {
		t.Fatalf("a step-up Begin reached Send %d times, want once", len(r.sm.sends))
	}

	flow := flowAt(stepCode, step.PrivateOf(asked))
	flow.Purpose = authn.PurposeStepUp
	flow.Account = testAccount
	proven, _ := r.m.Answer(ctx, flow, submit(codeFields(goodCode)))
	if step.KindOf(proven) != step.KindProven {
		t.Fatalf("a step-up code step = %s, want a proof", describe(proven))
	}
	p := step.ProofOf(proven)
	if p.Identifier == nil || p.Identifier.Kind != authn.IdentifierPhone || p.Identifier.Value != phoneNumber {
		t.Fatalf("the step-up proof identified %+v, want the phone %s", p.Identifier, phoneNumber)
	}
	if len(p.Methods) != 1 || p.Methods[0] != authn.MethodSMS {
		t.Fatalf("the step-up proof holds methods %v, want [sms]", p.Methods)
	}
}

// TestAStepUpSendsOnlyToTheAccountsProvedPhone holds that an unproved number is refused as a credential failure with no send.
func TestAStepUpSendsOnlyToTheAccountsProvedPhone(t *testing.T) {
	r := newRig()
	r.registerActive()
	proved := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	r.ac.identifiers = map[authn.AccountID][]authn.Identifier{
		testAccount: {
			{ID: "ident-username", Kind: authn.IdentifierUsername, Value: "ada", VerifiedAt: proved},
			{ID: "ident-phone", Kind: authn.IdentifierPhone, Value: phoneNumber, VerifiedAt: proved, VerifiedVia: authn.VerifiedViaSMSCode},
		},
	}
	ctx := t.Context()

	foreignStart := loginStart(phoneFields(otherPhone))
	foreignStart.Purpose = authn.PurposeStepUp
	foreignStart.Account = testAccount
	foreign, _ := r.m.Begin(ctx, foreignStart)
	if step.KindOf(foreign) != step.KindRefused || !errors.Is(step.CauseOf(foreign), authn.ErrInvalidCredentials) {
		t.Fatalf("a foreign number at the start = %s, want a refusal carrying ErrInvalidCredentials", describe(foreign))
	}
	if len(r.sm.sends) != 0 {
		t.Fatalf("a foreign number at the start reached Send %d times, want none", len(r.sm.sends))
	}

	ownStart := loginStart(phoneFields(phoneNumber))
	ownStart.Purpose = authn.PurposeStepUp
	ownStart.Account = testAccount
	asked, _ := r.m.Begin(ctx, ownStart)
	if step.KindOf(asked) != step.KindPrompt || step.NameOf(asked) != stepCode {
		t.Fatalf("the account's own number at the start = %s, want a prompt for the code", describe(asked))
	}
	if len(r.sm.sends) != 1 || r.sm.sends[0].Phone != phoneNumber {
		t.Fatalf("the account's own number reached Send %+v, want one send to %s", r.sm.sends, phoneNumber)
	}

	flow := flowAt(stepPhone, nil)
	flow.Purpose = authn.PurposeStepUp
	flow.Account = testAccount
	foreignAnswer, _ := r.m.Answer(ctx, flow, submit(phoneFields(otherPhone)))
	if step.KindOf(foreignAnswer) != step.KindRefused || !errors.Is(step.CauseOf(foreignAnswer), authn.ErrInvalidCredentials) {
		t.Fatalf("a foreign number in an answer = %s, want a refusal carrying ErrInvalidCredentials", describe(foreignAnswer))
	}
	if len(r.sm.sends) != 1 {
		t.Fatalf("a foreign number in an answer reached Send %d times, want the earlier send only", len(r.sm.sends))
	}
	ownAnswer, _ := r.m.Answer(ctx, flow, submit(phoneFields(phoneNumber)))
	if step.KindOf(ownAnswer) != step.KindPrompt || step.NameOf(ownAnswer) != stepCode {
		t.Fatalf("the account's own number in an answer = %s, want a prompt for the code", describe(ownAnswer))
	}
	if len(r.sm.sends) != 2 {
		t.Fatalf("the account's own number in an answer reached Send %d times, want two sends", len(r.sm.sends))
	}
}

// TestAStepUpAcceptsAnyPhoneTheAccountProved checks that an account with two proved numbers may use either.
func TestAStepUpAcceptsAnyPhoneTheAccountProved(t *testing.T) {
	r := newRig()
	r.registerActive()
	proved := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	r.ac.identifiers = map[authn.AccountID][]authn.Identifier{
		testAccount: {
			{ID: "ident-1", Kind: authn.IdentifierPhone, Value: phoneNumber, VerifiedAt: proved, VerifiedVia: authn.VerifiedViaSMSCode},
			{ID: "ident-2", Kind: authn.IdentifierPhone, Value: otherPhone, VerifiedAt: proved, VerifiedVia: authn.VerifiedViaSMSCode},
		},
	}

	for _, phone := range []string{phoneNumber, otherPhone} {
		start := loginStart(phoneFields(phone))
		start.Purpose = authn.PurposeStepUp
		start.Account = testAccount
		got, _ := r.m.Begin(t.Context(), start)
		if step.KindOf(got) != step.KindPrompt || step.NameOf(got) != stepCode {
			t.Errorf("a proved number %s = %s, want a prompt for the code", phone, describe(got))
		}
	}
	if len(r.sm.sends) != 2 {
		t.Fatalf("the account's proved numbers reached Send %d times, want two", len(r.sm.sends))
	}
}

// TestAStepUpRefusesANumberTheAccountHasNotProved checks that an unproved identifier row is not a target.
func TestAStepUpRefusesANumberTheAccountHasNotProved(t *testing.T) {
	verified := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for name, identifier := range map[string]authn.Identifier{
		"no verification time": {ID: "ident-phone", Kind: authn.IdentifierPhone, Value: phoneNumber},
		"asserted by an administrator": {
			ID: "ident-phone", Kind: authn.IdentifierPhone, Value: phoneNumber,
			VerifiedAt: verified, VerifiedVia: authn.VerifiedViaAdmin,
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig()
			r.registerActive()
			r.ac.identifiers = map[authn.AccountID][]authn.Identifier{
				testAccount: {identifier},
			}
			start := loginStart(phoneFields(phoneNumber))
			start.Purpose = authn.PurposeStepUp
			start.Account = testAccount
			got, _ := r.m.Begin(t.Context(), start)
			if step.KindOf(got) != step.KindRefused || !errors.Is(step.CauseOf(got), authn.ErrInvalidCredentials) {
				t.Fatalf("an unproved number = %s, want a refusal carrying ErrInvalidCredentials", describe(got))
			}
			if len(r.sm.sends) != 0 {
				t.Fatalf("an unproved number reached Send %d times, want none", len(r.sm.sends))
			}
		})
	}
}

// TestAStepUpWithAnUnreadableAccountAnswersUnavailable checks that an unreadable account answers 503 with no send.
func TestAStepUpWithAnUnreadableAccountAnswersUnavailable(t *testing.T) {
	r := newRig()
	r.registerActive()
	r.ac.identifiersErr = errors.New("the account store is down")
	start := loginStart(phoneFields(phoneNumber))
	start.Purpose = authn.PurposeStepUp
	start.Account = testAccount
	got, _ := r.m.Begin(t.Context(), start)
	if step.KindOf(got) != step.KindUnavailable {
		t.Fatalf("an unreadable account = %s, want an unavailable answer", describe(got))
	}
	if len(r.sm.sends) != 0 {
		t.Fatalf("an unreadable account reached Send %d times, want none", len(r.sm.sends))
	}
}

// TestThePromptsPublishTheStepsAndFieldsTheyName pins the step name and field kind, requiredness and secrecy.
func TestThePromptsPublishTheStepsAndFieldsTheyName(t *testing.T) {
	r := newRig()

	asked, _ := r.m.Begin(t.Context(), loginStart(nil))
	if got := step.NameOf(asked); got != "phone" {
		t.Errorf("the first prompt names the step %q, want %q", got, "phone")
	}
	wantPhone := []authn.Field{{Name: "phone", Kind: authn.FieldPhone, Required: true}}
	if got := step.FieldsOf(asked); !reflect.DeepEqual(got, wantPhone) {
		t.Errorf("the phone prompt asks for %+v, want %+v", got, wantPhone)
	}

	r.registerActive()
	prompted, _ := r.m.Answer(t.Context(), flowAt(stepPhone, nil), submit(phoneFields(phoneNumber)))
	if got := step.NameOf(prompted); got != "code" {
		t.Errorf("the second prompt names the step %q, want %q", got, "code")
	}
	wantCode := []authn.Field{{Name: "code", Kind: authn.FieldCode, Required: true, Secret: true}}
	if got := step.FieldsOf(prompted); !reflect.DeepEqual(got, wantCode) {
		t.Errorf("the code prompt asks for %+v, want %+v", got, wantCode)
	}
}

// TestThePhoneShapeIsE164 checks the seven-to-fifteen digit body and its anchors.
func TestThePhoneShapeIsE164(t *testing.T) {
	r := newRig()
	r.registerActive()

	accepted := []string{
		"+1000000",         // the shortest: a country code and six digits, seven in all
		"+100000000000000", // the longest: fifteen digits in all
	}
	for _, v := range accepted {
		got, _ := r.m.Begin(t.Context(), loginStart(phoneFields(v)))
		if step.KindOf(got) != step.KindPrompt {
			t.Errorf("phone %q = %s, want a prompt: it is an E.164 number", v, describe(got))
		}
	}

	refused := []string{
		"+100000",           // six digits, one short of the floor
		"+1000000000000000", // sixteen digits, one past the ceiling
		"+8613800138000x",   // a suffix after the number
		"x+8613800138000",   // a prefix before the number
		"+8613800138000 ",   // a trailing space
		" +8613800138000",   // a leading space
	}
	for _, v := range refused {
		got, _ := r.m.Begin(t.Context(), loginStart(phoneFields(v)))
		if step.KindOf(got) != step.KindRefused || !errors.Is(step.CauseOf(got), authn.ErrInvalidIdentifier) {
			t.Errorf("phone %q = %s, want a refusal carrying ErrInvalidIdentifier", v, describe(got))
		}
	}

	if len(r.sm.sends) != len(accepted) {
		t.Fatalf("the accepted numbers reached Send %d times, want %d", len(r.sm.sends), len(accepted))
	}
}

// TestTheBackOffIsKeyedByTheNumbersHashUnderThePhoneScope pins the scope and the hexadecimal SHA-256 key.
func TestTheBackOffIsKeyedByTheNumbersHashUnderThePhoneScope(t *testing.T) {
	const phoneHash = "ec61f3c620a98bdead8c1f1f0ae747abd1b62a0c2dba4fd4bc22cf0d1d8653e5"
	want := string(authn.ScopePhone) + ":" + phoneHash

	t.Run("a code that holds clears the same key it read", func(t *testing.T) {
		r := newRig()
		got, _ := r.m.Answer(t.Context(), flowAt(stepCode, writeState(phoneNumber, challengeID)), submit(codeFields(goodCode)))
		if step.KindOf(got) != step.KindProven {
			t.Fatalf("a good code = %s, want a proof", describe(got))
		}
		if len(r.th.checks) != 1 || r.th.checks[0] != want {
			t.Errorf("Check saw %v, want [%q]", r.th.checks, want)
		}
		if len(r.th.succeeds) != 1 || r.th.succeeds[0] != want {
			t.Errorf("Succeed saw %v, want [%q]", r.th.succeeds, want)
		}
	})

	t.Run("a wrong code counts the same key it read", func(t *testing.T) {
		r := newRig()
		r.sm.verifyErr = authn.ErrInvalidCredentials
		got, _ := r.m.Answer(t.Context(), flowAt(stepCode, writeState(phoneNumber, challengeID)), submit(codeFields(goodCode)))
		if step.KindOf(got) != step.KindRefused {
			t.Fatalf("a wrong code = %s, want a refusal", describe(got))
		}
		if len(r.th.checks) != 1 || r.th.checks[0] != want {
			t.Errorf("Check saw %v, want [%q]", r.th.checks, want)
		}
		if len(r.th.fails) != 1 || r.th.fails[0] != want {
			t.Errorf("Fail saw %v, want [%q]", r.th.fails, want)
		}
	})
}

// TestAWrappedInvalidCredentialsIsStillARefusal checks that the sentinel is matched through errors.Is and still answers 401.
func TestAWrappedInvalidCredentialsIsStillARefusal(t *testing.T) {
	r := newRig()
	r.sm.verifyErr = fmt.Errorf("checking the code: %w", authn.ErrInvalidCredentials)
	got, _ := r.m.Answer(t.Context(), flowAt(stepCode, writeState(phoneNumber, challengeID)), submit(codeFields(goodCode)))
	if step.KindOf(got) != step.KindRefused || !errors.Is(step.CauseOf(got), authn.ErrInvalidCredentials) {
		t.Fatalf("a wrapped ErrInvalidCredentials = %s, want a refusal carrying it", describe(got))
	}
	if len(r.th.fails) != 1 {
		t.Errorf("a wrapped refusal counted %d failures, want one", len(r.th.fails))
	}
}

// TestABrokenComparisonIsUnavailableNotARefusal checks that any other Verify error answers 503 and counts no failure.
func TestABrokenComparisonIsUnavailableNotARefusal(t *testing.T) {
	r := newRig()
	r.sm.verifyErr = errors.New("the challenge store is down")
	got, _ := r.m.Answer(t.Context(), flowAt(stepCode, writeState(phoneNumber, challengeID)), submit(codeFields(goodCode)))
	if step.KindOf(got) != step.KindUnavailable {
		t.Fatalf("a Verify that cannot be had = %s, want unavailable", describe(got))
	}
	if len(r.th.fails) != 0 || len(r.th.succeeds) != 0 {
		t.Errorf("a broken Verify counted %d failures and %d clears, want neither", len(r.th.fails), len(r.th.succeeds))
	}
}

// TestASendThrottledAsABareSentinelHasNoRetry checks that a bare ErrThrottled is answered as a throttle with no retry.
func TestASendThrottledAsABareSentinelHasNoRetry(t *testing.T) {
	r := newRig()
	r.registerActive()
	r.sm.sendErr = authn.ErrThrottled
	got, _ := r.m.Begin(t.Context(), loginStart(phoneFields(phoneNumber)))
	if step.KindOf(got) != step.KindThrottled || step.RetryAfterOf(got) != 0 {
		t.Fatalf("a Send refused with the bare ErrThrottled = %s, want a throttle of zero", describe(got))
	}
}

// TestAReadFailureOfTheAccountIsNotASuppressedSend checks that an unreadable store answers 503, never a suppressed send.
func TestAReadFailureOfTheAccountIsNotASuppressedSend(t *testing.T) {
	t.Run("a vanished account follows the registration switch", func(t *testing.T) {
		for _, open := range []bool{true, false} {
			r := newRig()
			if open {
				r.st.signup = "open"
			}
			r.ac.lookupID = testAccount
			r.ac.getErr = authn.ErrAccountNotFound
			got, _ := r.m.Begin(t.Context(), loginStart(phoneFields(phoneNumber)))
			if step.KindOf(got) != step.KindPrompt {
				t.Fatalf("open=%v: Begin = %s, want a prompt", open, describe(got))
			}
			if len(r.sm.sends) != 1 {
				t.Fatalf("open=%v: reached Send %d times, want once", open, len(r.sm.sends))
			}
			if got := r.sm.sends[0].Deliver; got != open {
				t.Errorf("open=%v: delivered = %v, want the registration switch", open, got)
			}
		}
	})

	t.Run("a store that cannot be read is unavailable", func(t *testing.T) {
		r := newRig()
		r.ac.lookupID = testAccount
		r.ac.getErr = errors.New("the account store is down")
		got, _ := r.m.Begin(t.Context(), loginStart(phoneFields(phoneNumber)))
		if step.KindOf(got) != step.KindUnavailable {
			t.Fatalf("a Get that cannot be had = %s, want unavailable", describe(got))
		}
		if len(r.sm.sends) != 0 {
			t.Errorf("an unreadable account reached Send %d times, want none", len(r.sm.sends))
		}
	})
}

// TestACancelledCallShortCircuitsBeforeItActs checks that an ended call answers 503 before sending, reading or comparing.
func TestACancelledCallShortCircuitsBeforeItActs(t *testing.T) {
	t.Run("before a code is sent", func(t *testing.T) {
		r := newRig()
		r.registerActive()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		got, _ := r.m.Begin(ctx, loginStart(phoneFields(phoneNumber)))
		if step.KindOf(got) != step.KindUnavailable {
			t.Fatalf("Begin with a cancelled call = %s, want unavailable", describe(got))
		}
		if len(r.ac.lookups) != 0 || len(r.sm.sends) != 0 {
			t.Errorf("a cancelled Begin reached Lookup %d and Send %d times, want none",
				len(r.ac.lookups), len(r.sm.sends))
		}
	})

	t.Run("before the back-off is read", func(t *testing.T) {
		r := newRig()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		got, _ := r.m.Answer(ctx, flowAt(stepCode, writeState(phoneNumber, challengeID)), submit(codeFields(goodCode)))
		if step.KindOf(got) != step.KindUnavailable {
			t.Fatalf("Answer with a cancelled call = %s, want unavailable", describe(got))
		}
		if len(r.th.checks) != 0 || len(r.sm.verifies) != 0 {
			t.Errorf("a cancelled call read the back-off %d and checked %d times, want none",
				len(r.th.checks), len(r.sm.verifies))
		}
	})

	t.Run("before the comparison, after the back-off was read", func(t *testing.T) {
		r := newRig()
		ctx, cancel := context.WithCancel(t.Context())
		r.th.onCheck = cancel
		got, _ := r.m.Answer(ctx, flowAt(stepCode, writeState(phoneNumber, challengeID)), submit(codeFields(goodCode)))
		if step.KindOf(got) != step.KindUnavailable {
			t.Fatalf("a call cancelled while the back-off was read = %s, want unavailable", describe(got))
		}
		if len(r.th.checks) != 1 {
			t.Fatalf("the back-off was read %d times, want once", len(r.th.checks))
		}
		if len(r.sm.verifies) != 0 {
			t.Errorf("a call cancelled before the comparison checked %d times, want none", len(r.sm.verifies))
		}
	})
}

// TestAnAnswerCarryingAnUnknownStateFieldIsRefused checks that an unknown field in the private blob is refused.
func TestAnAnswerCarryingAnUnknownStateFieldIsRefused(t *testing.T) {
	r := newRig()
	private := []byte(`{"v":1,"phone":"+8613800138000","challenge":"chal-S3nt1nel-0001","extra":"x"}`)
	got, _ := r.m.Answer(t.Context(), flowAt(stepCode, private), submit(codeFields(goodCode)))
	if step.KindOf(got) != step.KindRefused || !errors.Is(step.CauseOf(got), errBadState) {
		t.Fatalf("a state with an unknown field = %s, want a refusal naming the state", describe(got))
	}
	if len(r.sm.verifies) != 0 {
		t.Errorf("a state with an unknown field reached Verify %d times, want none", len(r.sm.verifies))
	}
}

// TestAReadFailureOfTheRegistrationSwitchIsLoggedAndReadAsClosed holds that a failed read is logged once at Error and the switch reads closed.
func TestAReadFailureOfTheRegistrationSwitchIsLoggedAndReadAsClosed(t *testing.T) {
	t.Run("a failing read is logged once", func(t *testing.T) {
		r := newRig()
		logs := &logBuffer{}
		r.m.logger = logs.logger()
		r.unregistered()
		r.st.err = errors.New("the settings store is down")

		for range 2 {
			got, _ := r.m.Begin(t.Context(), loginStart(phoneFields(unknownPhone)))
			if step.KindOf(got) != step.KindPrompt {
				t.Fatalf("Begin with an unreadable switch = %s, want a prompt", describe(got))
			}
		}
		if len(r.sm.sends) != 2 {
			t.Fatalf("the method reached Send %d times, want twice", len(r.sm.sends))
		}
		for i, send := range r.sm.sends {
			if send.Deliver {
				t.Errorf("send %d delivered a code while the switch read as closed", i)
			}
		}
		out := logs.String()
		if n := strings.Count(out, "level=ERROR"); n != 1 {
			t.Fatalf("the log holds %d Error lines, want one:\n%s", n, out)
		}
		if !strings.Contains(out, "setting="+signupPath) || !strings.Contains(out, "cause=") {
			t.Errorf("the Error line does not name the setting and the cause:\n%s", out)
		}
	})

	t.Run("a value outside the domain is logged once", func(t *testing.T) {
		r := newRig()
		logs := &logBuffer{}
		r.m.logger = logs.logger()
		r.unregistered()
		r.st.signup = "perhaps"

		for range 2 {
			got, _ := r.m.Begin(t.Context(), loginStart(phoneFields(unknownPhone)))
			if step.KindOf(got) != step.KindPrompt {
				t.Fatalf("Begin with an out-of-domain switch = %s, want a prompt", describe(got))
			}
		}
		for i, send := range r.sm.sends {
			if send.Deliver {
				t.Errorf("send %d delivered a code with an out-of-domain switch", i)
			}
		}
		out := logs.String()
		if n := strings.Count(out, "level=ERROR"); n != 1 {
			t.Fatalf("the log holds %d Error lines, want one:\n%s", n, out)
		}
		if !strings.Contains(out, "outside its domain") || !strings.Contains(out, "setting="+signupPath) {
			t.Errorf("the Error line does not report the domain fault on the setting:\n%s", out)
		}
	})

	t.Run("an absent store is not a fault", func(t *testing.T) {
		r := newRig()
		logs := &logBuffer{}
		r.m.logger = logs.logger()
		r.m.store = nil
		r.unregistered()

		if got, _ := r.m.Begin(t.Context(), loginStart(phoneFields(unknownPhone))); step.KindOf(got) != step.KindPrompt {
			t.Fatalf("Begin without a settings store = %s, want a prompt", describe(got))
		}
		if out := logs.String(); strings.Contains(out, "level=ERROR") {
			t.Errorf("an absent settings store wrote an Error:\n%s", out)
		}
	})
}

type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *logBuffer) logger() *slog.Logger { return slog.New(slog.NewTextHandler(l, nil)) }

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func badCodeForTest() string { return "000000" }

// TestThePhoneStepForwardsTheProofToTheCodeEngine holds that Start's and FlowState's proof reach the code engine unchanged.
func TestThePhoneStepForwardsTheProofToTheCodeEngine(t *testing.T) {
	r := newRig()
	r.registerActive()

	start := loginStart(phoneFields(phoneNumber))
	start.Proof = "proof-1"
	if _, err := r.m.Begin(t.Context(), start); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if len(r.sm.sends) != 1 || r.sm.sends[0].Proof != "proof-1" {
		t.Fatalf("the code engine was handed %+v, want one send carrying proof-1", r.sm.sends)
	}

	fl := flowAt(stepPhone, nil)
	fl.Proof = "proof-1"
	if _, err := r.m.Answer(t.Context(), fl, submit(phoneFields(phoneNumber))); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if len(r.sm.sends) != 2 || r.sm.sends[1].Proof != "proof-1" {
		t.Fatalf("the code engine was handed %+v, want a second send carrying proof-1", r.sm.sends)
	}
}

// TestThePhoneStepRefusesWhenTheCodeEngineRefusesTheProof holds that the gate's refusal becomes a refused step, not a failed login.
func TestThePhoneStepRefusesWhenTheCodeEngineRefusesTheProof(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"a missing or invalid proof", authn.ErrHumanVerificationRequired},
		{"an over-long proof", authn.ErrHumanProofTooLong},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig()
			r.registerActive()
			r.sm.sendErr = tc.err

			got, err := r.m.Begin(t.Context(), loginStart(phoneFields(phoneNumber)))
			if err != nil {
				t.Fatalf("Begin: %v", err)
			}
			if step.KindOf(got) != step.KindRefused {
				t.Fatalf("Begin answered %s, want a refusal", step.KindOf(got))
			}
			if !errors.Is(step.CauseOf(got), tc.err) {
				t.Errorf("the refusal cause = %v, want it to carry %v", step.CauseOf(got), tc.err)
			}
		})
	}
}
