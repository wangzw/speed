package sms

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
)

// fakeHuman is the human-verification gate a case scripts. It records every
// proof it was asked about and spends an accepted token, so a second
// presentation of the same token is refused the way a real single-use proof is.
type fakeHuman struct {
	mu       sync.Mutex
	required bool
	provider string
	siteKey  string
	accept   map[string]bool
	used     map[string]bool
	calls    []authn.HumanProof
	err      error
}

func newRequiredHuman() *fakeHuman {
	return &fakeHuman{
		required: true,
		provider: "pow",
		accept:   map[string]bool{"proof-1": true},
		used:     map[string]bool{},
	}
}

func (h *fakeHuman) Required() bool   { return h.required }
func (h *fakeHuman) Provider() string { return h.provider }
func (h *fakeHuman) SiteKey() string  { return h.siteKey }

func (h *fakeHuman) Verify(_ context.Context, p authn.HumanProof) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, p)
	if h.err != nil {
		return h.err
	}
	if !h.required {
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

// TestSendRefusesAMissingProofBeforeAnyBudget holds the gate's order: a send
// the deployment requires a proof for, presenting none, is refused with the
// gate's own error before the prefix table, the challenge row and every budget.
// The number's row count is what the budgets read, so a zero row count is a
// budget nothing spent.
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

// TestSendWithoutAGateSendsWithoutAProof holds the two legal absences: an
// assembly with no verifier and a gate that requires nothing both send without
// asking for a proof.
func TestSendWithoutAGateSendsWithoutAProof(t *testing.T) {
	for _, human := range []authn.HumanVerifier{nil, &fakeHuman{required: false, provider: "off"}} {
		r := newRigSQLite(t)
		r.allow()
		r.p.human = human

		if _, err := r.sendLogin(); err != nil {
			t.Fatalf("a send with no gate resolved with %v", err)
		}
		if n := r.rowCount(); n != 1 {
			t.Errorf("the challenge table holds %d rows, want one", n)
		}
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
	if !errors.Is(err, authn.ErrInvalidArgument) {
		t.Fatalf("an over-long proof resolved with %v, want ErrInvalidArgument", err)
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
