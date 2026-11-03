package sms

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/step"
)

// The values the cases drive through the factor, so that an assertion that one
// is nowhere in an answer or an error has something to find.
const (
	testAccount       = authn.AccountID("acct_0001")
	sentinelPhone     = "+8613800000000"
	sentinelChallenge = "ch_9f3a7c2e"
	sentinelCode      = "482913"
)

// sendCall is one call to the code engine's Send, as the fake recorded it.
type sendCall struct {
	purpose string
	phone   string
	account authn.AccountID
	locale  string
	deliver bool
	source  string
	proof   string
}

// verifyCall is one call to the code engine's Verify.
type verifyCall struct {
	id   string
	code string
}

// fakeCodes is an SMSCodes whose Send and Verify answer with what a case set and
// record what they were asked.
type fakeCodes struct {
	mu        sync.Mutex
	sends     []sendCall
	sendID    string
	sendErr   error
	verifies  []verifyCall
	verifyErr error
}

func (c *fakeCodes) Send(_ context.Context, in authn.SendCode) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sends = append(c.sends, sendCall{in.Purpose, in.Phone, in.Account, in.Locale, in.Deliver, in.Source, in.Proof})
	if c.sendErr != nil {
		return "", c.sendErr
	}
	return c.sendID, nil
}

func (c *fakeCodes) Verify(_ context.Context, id, code string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.verifies = append(c.verifies, verifyCall{id, code})
	return c.verifyErr
}

func (c *fakeCodes) sendCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.sends)
}

func (c *fakeCodes) lastSend() (sendCall, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.sends) == 0 {
		return sendCall{}, false
	}
	return c.sends[len(c.sends)-1], true
}

func (c *fakeCodes) verifyCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.verifies)
}

// fakeAccounts is an Accounts whose Identifiers answer with what a case set. The
// other methods are not this module's and fail the case that reaches them.
type fakeAccounts struct {
	authn.Accounts

	mu          sync.Mutex
	identifiers []authn.Identifier
	err         error
	calls       int
}

func (a *fakeAccounts) Identifiers(_ context.Context, _ authn.AccountID) ([]authn.Identifier, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	if a.err != nil {
		return nil, a.err
	}
	return append([]authn.Identifier(nil), a.identifiers...), nil
}

func (a *fakeAccounts) identifierCalls() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

// rig is one factor with its two fakes.
type rig struct {
	f  *factor
	cs *fakeCodes
	ac *fakeAccounts
}

// newRig builds a factor whose code engine returns sentinelChallenge for a send
// and whose account holds one proven phone, unless a case changes those first.
func newRig() *rig {
	cs := &fakeCodes{sendID: sentinelChallenge}
	ac := &fakeAccounts{identifiers: []authn.Identifier{{
		Kind: authn.IdentifierPhone, Value: sentinelPhone,
		VerifiedAt: time.Unix(1, 0), VerifiedVia: authn.VerifiedViaSMSCode,
	}}}
	return &rig{f: &factor{codes: cs, accounts: ac}, cs: cs, ac: ac}
}

// provenPhoneIdentifier is an identifier of a phone whose ownership a code
// proved.
func provenPhoneIdentifier() authn.Identifier {
	return authn.Identifier{
		Kind: authn.IdentifierPhone, Value: sentinelPhone,
		VerifiedAt: time.Unix(1, 0), VerifiedVia: authn.VerifiedViaSMSCode,
	}
}

func submit(fields map[string]string) authn.StepInput {
	return authn.StepInput{Action: authn.InputSubmit, Fields: fields}
}

func flow(sendsLeft int, locale string, private []byte) authn.FlowState {
	return authn.FlowState{ID: "flow-1", Purpose: authn.PurposeLogin, Account: testAccount, SendsLeft: sendsLeft, Locale: locale, Private: private}
}

func record() authn.FactorRecord {
	return authn.FactorRecord{ID: "fac_1", Account: testAccount, Kind: authn.FactorSMS, Status: authn.FactorActive}
}

func TestTheFactorIsOfTheSMSKind(t *testing.T) {
	if got := newRig().f.Kind(); got != authn.FactorSMS {
		t.Errorf("Kind = %q, want %q", got, authn.FactorSMS)
	}
}

func TestEnrollRegistersTheAccountsProvenPhoneWithoutAStep(t *testing.T) {
	r := newRig()
	got, err := r.f.Enroll(t.Context(), flow(0, "", nil), submit(nil))
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	if step.KindOf(got) != step.KindEnrolled {
		t.Fatalf("Enroll answered %s, want an enrollment: a phone that is already proven registers at once", step.KindOf(got))
	}
}

func TestEnrollRefusesAnAccountWithNoProvenPhone(t *testing.T) {
	cases := []struct {
		name        string
		identifiers []authn.Identifier
	}{
		{"no identifier at all", nil},
		{"an email only", []authn.Identifier{{Kind: authn.IdentifierEmail, Value: "a@b.test", VerifiedAt: time.Unix(1, 0), VerifiedVia: authn.VerifiedViaSMSCode}}},
		{"an administrator-asserted phone", []authn.Identifier{{Kind: authn.IdentifierPhone, Value: sentinelPhone, VerifiedAt: time.Unix(1, 0), VerifiedVia: authn.VerifiedViaAdmin}}},
		{"a phone an import never verified", []authn.Identifier{{Kind: authn.IdentifierPhone, Value: sentinelPhone, VerifiedAt: time.Unix(1, 0), VerifiedVia: authn.VerifiedViaImportUnverified}}},
		{"a phone with no verification time", []authn.Identifier{{Kind: authn.IdentifierPhone, Value: sentinelPhone, VerifiedVia: authn.VerifiedViaSMSCode}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig()
			r.ac.identifiers = tc.identifiers
			got, err := r.f.Enroll(t.Context(), flow(0, "", nil), submit(nil))
			if err != nil {
				t.Fatalf("Enroll: %v", err)
			}
			if step.KindOf(got) != step.KindRefused {
				t.Fatalf("Enroll answered %s, want a refusal: the factor binds a number the account proved it holds", step.KindOf(got))
			}
			cause := step.CauseOf(got)
			if cause == nil || !strings.Contains(cause.Error(), "B/phone") {
				t.Errorf("the refusal cause is %v, want it to name the phone verification routes", cause)
			}
		})
	}
}

func TestEnrollIsAnOutageWhenThePhoneCannotBeRead(t *testing.T) {
	r := newRig()
	r.ac.err = errors.New("boom")
	got, err := r.f.Enroll(t.Context(), flow(0, "", nil), submit(nil))
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	if step.KindOf(got) != step.KindUnavailable {
		t.Fatalf("Enroll answered %s, want an outage: an unreadable account must not read as one with no phone", step.KindOf(got))
	}
}

func TestChallengeSendsOneCodeForTheMFAAndAsksForIt(t *testing.T) {
	r := newRig()
	got, err := r.f.Challenge(t.Context(), flow(3, "en-US", nil), record())
	if err != nil {
		t.Fatalf("Challenge: %v", err)
	}
	if step.KindOf(got) != step.KindPrompt {
		t.Fatalf("Challenge answered %s, want a prompt for the code", step.KindOf(got))
	}
	if name := step.NameOf(got); name != stepCode {
		t.Errorf("the prompt names step %q, want %q", name, stepCode)
	}
	fields := step.FieldsOf(got)
	if len(fields) != 1 || fields[0].Name != fieldCode || fields[0].Kind != authn.FieldCode || !fields[0].Required {
		t.Errorf("the prompt asks for %+v, want one required code field", fields)
	}
	if got := step.SendsOf(got); got != 1 {
		t.Errorf("the answer reports %d sends, want 1: the engine folds it into the flow's budget", got)
	}

	sent, ok := r.cs.lastSend()
	if !ok {
		t.Fatal("Challenge sent no code")
	}
	if sent.purpose != authn.SMSPurposeMFA {
		t.Errorf("the send purpose is %q, want %q", sent.purpose, authn.SMSPurposeMFA)
	}
	if sent.phone != sentinelPhone {
		t.Errorf("the send went to %q, want the account's proven phone %q", sent.phone, sentinelPhone)
	}
	if sent.account != testAccount {
		t.Errorf("the send counts against account %q, want %q", sent.account, testAccount)
	}
	if sent.locale != "en-US" {
		t.Errorf("the send locale is %q, want the flow's en-US", sent.locale)
	}
	if !sent.deliver {
		t.Error("the send is not marked to deliver: the account's phone is proven, so the code is really sent")
	}

	// The challenge identifier is kept in the private bytes, which is how the
	// verification step learns which challenge the code answers.
	id, ok := readChallenge(step.PrivateOf(got))
	if !ok || id != sentinelChallenge {
		t.Errorf("the answer keeps challenge %q (ok %v), want %q", id, ok, sentinelChallenge)
	}
}

func TestChallengeSendsNothingWhenTheFlowHasNoSendBudget(t *testing.T) {
	r := newRig()
	got, err := r.f.Challenge(t.Context(), flow(0, "", nil), record())
	if err != nil {
		t.Fatalf("Challenge: %v", err)
	}
	if step.KindOf(got) != step.KindThrottled {
		t.Fatalf("Challenge answered %s, want a throttle when no send is left", step.KindOf(got))
	}
	if wait := step.RetryAfterOf(got); wait != 0 {
		t.Errorf("the throttle asks to wait %s, want none when the flow simply has no budget", wait)
	}
	if n := r.cs.sendCount(); n != 0 {
		t.Errorf("Challenge sent %d codes, want none: a flow with no budget left must not send", n)
	}
}

func TestChallengeThatIsThrottledByTheEngineAsksToWait(t *testing.T) {
	r := newRig()
	r.cs.sendErr = &authn.ThrottledError{RetryAfter: 7 * time.Second}
	got, err := r.f.Challenge(t.Context(), flow(1, "", nil), record())
	if err != nil {
		t.Fatalf("Challenge: %v", err)
	}
	if step.KindOf(got) != step.KindThrottled || step.RetryAfterOf(got) != 7*time.Second {
		t.Fatalf("Challenge answered %s after %s, want a throttle after 7s", step.KindOf(got), step.RetryAfterOf(got))
	}
	if n := r.cs.sendCount(); n != 1 {
		t.Errorf("the engine was asked %d times, want 1", n)
	}
}

func TestChallengeRefusesAnUnsupportedRegion(t *testing.T) {
	r := newRig()
	r.cs.sendErr = authn.ErrUnsupportedRegion
	got, err := r.f.Challenge(t.Context(), flow(1, "", nil), record())
	if err != nil {
		t.Fatalf("Challenge: %v", err)
	}
	if step.KindOf(got) != step.KindRefused {
		t.Fatalf("Challenge answered %s, want a refusal when the number's region is not allowed", step.KindOf(got))
	}
	if cause := step.CauseOf(got); !errors.Is(cause, authn.ErrUnsupportedRegion) {
		t.Errorf("the refusal cause is %v, want it to read as ErrUnsupportedRegion", cause)
	}
}

func TestChallengeIsAnOutageWhenTheEngineCannotSend(t *testing.T) {
	r := newRig()
	r.cs.sendErr = authn.ErrUnavailable
	got, err := r.f.Challenge(t.Context(), flow(1, "", nil), record())
	if err != nil {
		t.Fatalf("Challenge: %v", err)
	}
	if step.KindOf(got) != step.KindUnavailable {
		t.Fatalf("Challenge answered %s, want an outage: a send that could not happen is no refusal", step.KindOf(got))
	}
}

func TestChallengeSendsNothingForAnAccountWithNoProvenPhone(t *testing.T) {
	r := newRig()
	r.ac.identifiers = nil
	got, err := r.f.Challenge(t.Context(), flow(1, "", nil), record())
	if err != nil {
		t.Fatalf("Challenge: %v", err)
	}
	if step.KindOf(got) != step.KindRefused {
		t.Fatalf("Challenge answered %s, want a refusal when the account has no proven phone", step.KindOf(got))
	}
	if n := r.cs.sendCount(); n != 0 {
		t.Errorf("Challenge sent %d codes, want none without a phone to send to", n)
	}
}

func TestVerifyPassesTheChallengeAndTheCode(t *testing.T) {
	r := newRig()
	private := markChallenge(sentinelChallenge)
	got, err := r.f.Verify(t.Context(), flow(0, "", private), record(), submit(map[string]string{fieldCode: sentinelCode}))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if step.KindOf(got) != step.KindPassed {
		t.Fatalf("Verify answered %s, want a pass", step.KindOf(got))
	}
	pass := step.PassOf(got)
	if len(pass.Methods) != 1 || pass.Methods[0] != authn.MethodSMS {
		t.Errorf("the pass methods are %v, want the sms method alone", pass.Methods)
	}
	if pass.UserVerified {
		t.Error("the pass claims user verification, which an SMS code does not carry")
	}
	if r.cs.verifyCount() != 1 {
		t.Fatalf("the engine was asked %d times to check a code, want 1", r.cs.verifyCount())
	}
	r.cs.mu.Lock()
	call := r.cs.verifies[0]
	r.cs.mu.Unlock()
	if call.id != sentinelChallenge || call.code != sentinelCode {
		t.Errorf("the engine checked (%q, %q), want the flow's challenge %q and the answered code", call.id, call.code, sentinelChallenge)
	}
}

func TestVerifyRefusesACodeThatDoesNotHold(t *testing.T) {
	r := newRig()
	r.cs.verifyErr = authn.ErrInvalidCredentials
	got, err := r.f.Verify(t.Context(), flow(0, "", markChallenge(sentinelChallenge)), record(), submit(map[string]string{fieldCode: sentinelCode}))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if step.KindOf(got) != step.KindRefused {
		t.Fatalf("Verify answered %s, want a refusal", step.KindOf(got))
	}
	if cause := step.CauseOf(got); !errors.Is(cause, authn.ErrInvalidCredentials) {
		t.Errorf("the refusal cause is %v, want it to read as ErrInvalidCredentials", cause)
	}
}

func TestVerifyIsAnOutageWhenTheEngineCannotBeReached(t *testing.T) {
	r := newRig()
	r.cs.verifyErr = authn.ErrUnavailable
	got, err := r.f.Verify(t.Context(), flow(0, "", markChallenge(sentinelChallenge)), record(), submit(map[string]string{fieldCode: sentinelCode}))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if step.KindOf(got) != step.KindUnavailable {
		t.Fatalf("Verify answered %s, want an outage: a code that could not be checked is no refusal", step.KindOf(got))
	}
}

func TestVerifyRefusesAFlowThatCarriesNoChallenge(t *testing.T) {
	for _, private := range [][]byte{nil, []byte("not-ours"), []byte(`{"v":2,"id":"x"}`), []byte(`{"v":1,"id":""}`)} {
		r := newRig()
		got, err := r.f.Verify(t.Context(), flow(0, "", private), record(), submit(map[string]string{fieldCode: sentinelCode}))
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if step.KindOf(got) != step.KindRefused {
			t.Errorf("a flow carrying %q answered %s, want a refusal", private, step.KindOf(got))
		}
		if n := r.cs.verifyCount(); n != 0 {
			t.Errorf("a flow carrying %q asked the engine %d times, want none", private, n)
		}
	}
}

func TestVerifyRefusesAnAnswerWithNoCode(t *testing.T) {
	r := newRig()
	got, err := r.f.Verify(t.Context(), flow(0, "", markChallenge(sentinelChallenge)), record(), submit(map[string]string{}))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if step.KindOf(got) != step.KindRefused {
		t.Fatalf("Verify answered %s, want a refusal for an answer with no code", step.KindOf(got))
	}
	if n := r.cs.verifyCount(); n != 0 {
		t.Errorf("an empty code burned %d challenge attempts, want none", n)
	}
}

func TestRemoveKeepsNoDataOfItsOwn(t *testing.T) {
	if err := newRig().f.Remove(t.Context(), testAccount, record()); err != nil {
		t.Errorf("Remove = %v, want nil: the factor keeps no data of its own to erase", err)
	}
}

func TestTheChallengeMarkRoundTripsAndRejectsAnythingElse(t *testing.T) {
	if id, ok := readChallenge(markChallenge(sentinelChallenge)); !ok || id != sentinelChallenge {
		t.Errorf("the mark does not round-trip: got (%q, %v)", id, ok)
	}
	for _, bad := range [][]byte{nil, {}, []byte("{}"), []byte(`{"v":1}`), []byte(`{"v":1,"id":"x","extra":1}`), []byte(`{"v":1,"id":"x"} trailing`)} {
		if _, ok := readChallenge(bad); ok {
			t.Errorf("readChallenge accepted %q, want it refused", bad)
		}
	}
}

// TestChallengeForwardsTheProofToTheCodeEngine holds the factor's side of the
// gate: the proof the engine put in FlowState reaches the code engine unchanged.
func TestChallengeForwardsTheProofToTheCodeEngine(t *testing.T) {
	r := newRig()
	fl := flow(3, "en-US", nil)
	fl.Proof = "proof-1"

	if _, err := r.f.Challenge(t.Context(), fl, record()); err != nil {
		t.Fatalf("Challenge: %v", err)
	}
	sent, ok := r.cs.lastSend()
	if !ok {
		t.Fatal("the code engine was handed no send")
	}
	if sent.proof != "proof-1" {
		t.Errorf("the code engine was handed proof %q, want the flow's proof-1", sent.proof)
	}
}

// TestChallengeRefusesWhenTheCodeEngineRefusesTheProof holds the factor's answer
// to the gate's two refusals: a refused step, which the engine answers with the
// gate's own 403 or 400.
func TestChallengeRefusesWhenTheCodeEngineRefusesTheProof(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"a missing or invalid proof", authn.ErrHumanVerificationRequired},
		{"an over-long proof", authn.ErrHumanProofTooLong},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig()
			r.cs.sendErr = tc.err

			got, err := r.f.Challenge(t.Context(), flow(1, "", nil), record())
			if err != nil {
				t.Fatalf("Challenge: %v", err)
			}
			if step.KindOf(got) != step.KindRefused {
				t.Fatalf("Challenge answered %s, want a refusal", step.KindOf(got))
			}
			if !errors.Is(step.CauseOf(got), tc.err) {
				t.Errorf("the refusal cause = %v, want it to carry %v", step.CauseOf(got), tc.err)
			}
		})
	}
}
