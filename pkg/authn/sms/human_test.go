package sms

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/sms/budget"
)

// fakeHuman is the human-verification gate a case scripts. It records every
// proof it was asked about and spends an accepted token, so a second
// presentation of the same token is refused the way a real single-use proof is.
type fakeHuman struct {
	mu       sync.Mutex
	required bool
	// byScope, when set, answers Required per scope instead of the single
	// required flag, so a case can gate one surface and leave the other off.
	byScope        map[authn.HumanScope]bool
	provider       string
	siteKey        string
	accept         map[string]bool
	used           map[string]bool
	calls          []authn.HumanProof
	requiredScopes []authn.HumanScope
	verifyScopes   []authn.HumanScope
	err            error
}

func newRequiredHuman() *fakeHuman {
	return &fakeHuman{
		required: true,
		provider: "pow",
		accept:   map[string]bool{"proof-1": true},
		used:     map[string]bool{},
	}
}

func (h *fakeHuman) Required(scope authn.HumanScope) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.requiredScopes = append(h.requiredScopes, scope)
	if h.byScope != nil {
		return h.byScope[scope]
	}
	return h.required
}
func (h *fakeHuman) Provider(authn.HumanScope) string { return h.provider }
func (h *fakeHuman) SiteKey(authn.HumanScope) string  { return h.siteKey }
func (h *fakeHuman) Action(authn.HumanScope) string   { return "" }

func (h *fakeHuman) Verify(_ context.Context, p authn.HumanProof, scope authn.HumanScope) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, p)
	h.verifyScopes = append(h.verifyScopes, scope)
	if h.err != nil {
		return h.err
	}
	if !h.required && h.byScope == nil {
		return nil
	}
	if p.Token == "" || !h.accept[p.Token] || h.used[p.Token] {
		return fmt.Errorf("%w: the proof is missing, invalid or already used", authn.ErrHumanVerificationRequired)
	}
	h.used[p.Token] = true
	return nil
}

func (h *fakeHuman) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.calls)
}

func (h *fakeHuman) last() (authn.HumanProof, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.calls) == 0 {
		return authn.HumanProof{}, false
	}
	return h.calls[len(h.calls)-1], true
}

// askedScopes is every scope the gate was asked about, through Required and
// Verify alike, in the order the send asked.
func (h *fakeHuman) askedScopes() []authn.HumanScope {
	h.mu.Lock()
	defer h.mu.Unlock()
	scopes := make([]authn.HumanScope, 0, len(h.requiredScopes)+len(h.verifyScopes))
	scopes = append(scopes, h.requiredScopes...)
	scopes = append(scopes, h.verifyScopes...)
	return scopes
}

// assertOnlyScope holds that every question the gate was asked named want: a
// send that asks the wrong surface is asking a different gate, and a send that
// asks both is held to a gate it does not send on.
func assertOnlyScope(t *testing.T, human *fakeHuman, want authn.HumanScope) {
	t.Helper()
	scopes := human.askedScopes()
	if len(scopes) == 0 {
		t.Fatalf("the gate was asked about no scope, want at least %q", want)
	}
	for _, got := range scopes {
		if got != want {
			t.Errorf("the gate was asked about scope %q, want only %q", got, want)
		}
	}
}

// TestSendRefusesAMissingProofBeforeAnyBudget holds the gate's order: a send
// the deployment requires a proof for, presenting none, is refused with the
// gate's own error before the prefix table, the challenge row and every budget.
// The number's row count is what the budgets read, so a zero row count is a
// budget nothing spent. The shared ledger holds no attempt either, so the
// refusal spends neither an attempt budget nor a send budget.
func TestSendRefusesAMissingProofBeforeAnyBudget(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	human := newRequiredHuman()
	r.p.human = human

	_, err := r.sendLogin()
	if !errors.Is(err, authn.ErrHumanVerificationRequired) {
		t.Fatalf("a send with no proof resolved with %v, want ErrHumanVerificationRequired", err)
	}
	if human.count() != 1 {
		t.Errorf("the gate was asked %d times, want once", human.count())
	}
	if n := r.rowCount(); n != 0 {
		t.Errorf("the challenge table holds %d rows, want none: a refused proof spends no budget", n)
	}
	if n := r.sender.count(); n != 0 {
		t.Errorf("the carrier was handed %d messages, want none", n)
	}
	// IncludeRefunded and IncludeSuppressed together read every row the ledger
	// could hold, so a zero here is the absence of any attempt at all.
	all := budget.Query{
		Since:             r.clock.read().Add(-time.Hour),
		IncludeRefunded:   true,
		IncludeSuppressed: true,
	}
	if n := r.ledgerCount(all); n != 0 {
		t.Errorf("the shared ledger holds %d attempts, want none: a refused proof writes no row", n)
	}
}

// TestSendConsumesTheProofOnceAndSendsOnce holds the accepted side and the
// single-use property: a valid proof passes, the send writes its row and reaches
// the carrier, and the same proof presented again is refused before a second row
// is written. The gate's RemoteIP is the send's source.
func TestSendConsumesTheProofOnceAndSendsOnce(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	human := newRequiredHuman()
	r.p.human = human

	_, err := r.p.Send(context.Background(), authn.SendCode{
		Purpose: authn.SMSPurposeLogin,
		Phone:   phone,
		Deliver: true,
		Source:  "203.0.113.7",
		Proof:   "proof-1",
	})
	if err != nil {
		t.Fatalf("a send with a valid proof resolved with %v", err)
	}
	if human.count() != 1 {
		t.Errorf("the gate was asked %d times, want once: a send consumes its proof once", human.count())
	}
	if proof, ok := human.last(); !ok || proof.Token != "proof-1" || proof.RemoteIP != "203.0.113.7" {
		t.Errorf("the gate was handed %+v, want proof-1 from the send's source 203.0.113.7", proof)
	}
	if n := r.rowCount(); n != 1 {
		t.Errorf("the challenge table holds %d rows, want one", n)
	}

	_, err = r.p.Send(context.Background(), authn.SendCode{
		Purpose: authn.SMSPurposeLogin,
		Phone:   phone,
		Deliver: true,
		Proof:   "proof-1",
	})
	if !errors.Is(err, authn.ErrHumanVerificationRequired) {
		t.Fatalf("a reused proof resolved with %v, want ErrHumanVerificationRequired", err)
	}
	if n := r.rowCount(); n != 1 {
		t.Errorf("the challenge table holds %d rows after a reused proof, want still one", n)
	}
}

// TestSendWithAnOffGateSendsWithoutAProof holds the off mechanism: a gate that
// requires nothing sends without asking for a proof. The descriptor makes the
// gate a required dependency, so this is the shape of a deployment that runs it
// off, not an absence of one.
func TestSendWithAnOffGateSendsWithoutAProof(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	r.p.human = &fakeHuman{required: false, provider: "off"}

	if _, err := r.sendLogin(); err != nil {
		t.Fatalf("a send with an off gate resolved with %v", err)
	}
	if n := r.rowCount(); n != 1 {
		t.Errorf("the challenge table holds %d rows, want one", n)
	}
}

// TestSendRefusesAnOverLongProofBeforeTheProviderIsAsked holds the field's own
// bound: a proof over 8 KiB is a bad request before the provider is asked, and
// nothing is written or sent.
func TestSendRefusesAnOverLongProofBeforeTheProviderIsAsked(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	human := newRequiredHuman()
	r.p.human = human

	_, err := r.p.Send(context.Background(), authn.SendCode{
		Purpose: authn.SMSPurposeLogin,
		Phone:   phone,
		Deliver: true,
		Proof:   strings.Repeat("x", mount.HumanProofMaxBytes+1),
	})
	if !errors.Is(err, authn.ErrHumanProofTooLong) {
		t.Fatalf("an over-long proof resolved with %v, want ErrHumanProofTooLong", err)
	}
	if human.count() != 0 {
		t.Errorf("the provider was asked %d times, want none for a proof over the field's bound", human.count())
	}
	if n := r.rowCount(); n != 0 {
		t.Errorf("the challenge table holds %d rows, want none", n)
	}
}

// TestSendCarriesAGateOutageAsTheProviderSaid holds the third failure: a
// provider or token store that cannot answer is ErrUnavailable, and nothing is
// written or sent.
func TestSendCarriesAGateOutageAsTheProviderSaid(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	human := newRequiredHuman()
	human.err = authn.ErrUnavailable
	r.p.human = human

	_, err := r.sendLogin()
	if !errors.Is(err, authn.ErrUnavailable) {
		t.Fatalf("a send under an unavailable gate resolved with %v, want ErrUnavailable", err)
	}
	if n := r.rowCount(); n != 0 {
		t.Errorf("the challenge table holds %d rows, want none", n)
	}
}

// TestSendAsksThePhoneScopeAndOnlyThePhoneScope holds the surface a send is
// gated on. Every SMS purpose reaches a phone number, so each is held to the
// phone surface: a deployment that gates only the phone surface refuses a
// proofless send, and one that gates only the other surface lets it through
// without ever asking the gate about that other surface.
func TestSendAsksThePhoneScopeAndOnlyThePhoneScope(t *testing.T) {
	purposes := []string{
		authn.SMSPurposeLogin,
		authn.SMSPurposeMFA,
		authn.SMSPurposeEnroll,
		authn.SMSPurposeReset,
	}
	for _, purpose := range purposes {
		t.Run(purpose+" is gated on the phone surface", func(t *testing.T) {
			r := newRigSQLite(t)
			r.allow()
			human := &fakeHuman{
				byScope:  map[authn.HumanScope]bool{authn.HumanScopePhone: true},
				provider: "captcha",
				accept:   map[string]bool{},
				used:     map[string]bool{},
			}
			r.p.human = human

			_, err := r.p.Send(context.Background(), sendFor(purpose))
			if !errors.Is(err, authn.ErrHumanVerificationRequired) {
				t.Fatalf("a %s send gated only on the phone surface resolved with %v, want ErrHumanVerificationRequired", purpose, err)
			}
			assertOnlyScope(t, human, authn.HumanScopePhone)
			if n := r.rowCount(); n != 0 {
				t.Errorf("the challenge table holds %d rows, want none", n)
			}
		})

		t.Run(purpose+" is not gated on the other surface", func(t *testing.T) {
			r := newRigSQLite(t)
			r.allow()
			human := &fakeHuman{
				byScope:  map[authn.HumanScope]bool{authn.HumanScopeOther: true},
				provider: "pow",
				accept:   map[string]bool{},
				used:     map[string]bool{},
			}
			r.p.human = human

			if _, err := r.p.Send(context.Background(), sendFor(purpose)); err != nil {
				t.Fatalf("a %s send gated only on the other surface resolved with %v, want a send", purpose, err)
			}
			assertOnlyScope(t, human, authn.HumanScopePhone)
			if n := r.rowCount(); n != 1 {
				t.Errorf("the challenge table holds %d rows, want one", n)
			}
		})
	}
}

// sendFor is a delivering send of one purpose, with the account the purposes
// that count against one require.
func sendFor(purpose string) authn.SendCode {
	in := authn.SendCode{Purpose: purpose, Phone: phone, Deliver: true}
	if purpose == authn.SMSPurposeMFA || purpose == authn.SMSPurposeEnroll {
		in.Account = acct
	}
	return in
}
