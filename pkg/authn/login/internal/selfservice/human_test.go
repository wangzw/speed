package selfservice_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/login/internal/selfservice"
)

// fakeHuman is a human-verification gate a case scripts. It records what each
// request presented so a case can see that the gate was asked, and with which
// address.
type fakeHuman struct {
	required bool
	provider string
	siteKey  string
	accept   map[string]bool
	err      error
	calls    []authn.HumanProof
}

func (h *fakeHuman) Required() bool   { return h.required }
func (h *fakeHuman) Provider() string { return h.provider }
func (h *fakeHuman) SiteKey() string  { return h.siteKey }

func (h *fakeHuman) Verify(_ context.Context, p authn.HumanProof) error {
	h.calls = append(h.calls, p)
	if h.err != nil {
		return h.err
	}
	if !h.required {
		return nil
	}
	if p.Token == "" || !h.accept[p.Token] {
		return fmt.Errorf("%w: the proof is missing or not valid", authn.ErrHumanVerificationRequired)
	}
	return nil
}

// requiredHuman is a gate that requires a proof and accepts proof-1.
func requiredHuman() *fakeHuman {
	return &fakeHuman{required: true, provider: "pow", accept: map[string]bool{"proof-1": true}}
}

// withHuman builds a harness whose account self-service routes run behind h.
func withHuman(t *testing.T, h *fakeHuman, cfgs ...func(*config)) *harness {
	t.Helper()
	cfg := func(c *config) {
		c.mutateDeps = func(d *selfservice.Deps) { d.Human = h }
	}
	return newHarness(t, append([]func(*config){cfg}, cfgs...)...)
}

// TestStartPhoneForwardsTheProofToTheCodeEngine holds the phone send's side of
// the gate: the route passes the request's proof to the code engine and does not
// check it itself. The proof is single-use, so a route-side check would consume
// it before the engine's own check.
func TestStartPhoneForwardsTheProofToTheCodeEngine(t *testing.T) {
	h := newHarness(t)

	rec := h.post("/phone", `{"phone":"+8613800000000","human_proof":"proof-1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("a phone send answered %d: %s", rec.Code, rec.Body.String())
	}
	if len(h.sms.sent) != 1 {
		t.Fatalf("the SMS channel was handed %d messages, want one", len(h.sms.sent))
	}
	if got := h.sms.sent[0].Proof; got != "proof-1" {
		t.Errorf("the code engine was handed proof %q, want the request's proof-1", got)
	}
}

// TestAStartPhoneRefusalByTheCodeEngineIsAForbidden holds the answer the route
// gives when the code engine refuses the proof: the gate's own 403, no pending
// record, and nothing sent. The engine is the only place the proof is checked.
func TestAStartPhoneRefusalByTheCodeEngineIsAForbidden(t *testing.T) {
	h := newHarness(t)
	h.sms.sendErr = fmt.Errorf("%w: the proof is missing or not valid", authn.ErrHumanVerificationRequired)

	rec := h.post("/phone", `{"phone":"+8613800000000"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a send the gate refused answered %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if got := problemType(t, rec); got != mount.CodeHumanVerificationRequired {
		t.Errorf("the refusal code = %q, want %q", got, mount.CodeHumanVerificationRequired)
	}
	if keys := h.kv.keys(); len(keys) != 0 {
		t.Errorf("the store holds %v, want no pending record", keys)
	}
}

// TestAddEmailWithoutAProofIsRefusedAndSendsNothing holds the email branch: the
// code goes out over the email channel, not the SMS code engine, so the route
// itself must hold it to the human-verification gate. The address is checked for
// being free first, then the gate refuses the request before the resend window is
// claimed or a code is drawn. A refused proof therefore spends no window, so the
// same request succeeds once a proof is presented.
func TestAddEmailWithoutAProofIsRefusedAndSendsNothing(t *testing.T) {
	human := requiredHuman()
	h := withHuman(t, human, func(c *config) { c.email = true })

	rec := h.post("/identifiers", `{"kind":"email","value":"ada@example.org"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("an email send with no proof answered %d: %s", rec.Code, rec.Body.String())
	}
	if got := problemType(t, rec); got != mount.CodeHumanVerificationRequired {
		t.Errorf("the refusal code = %q, want %q", got, mount.CodeHumanVerificationRequired)
	}
	if len(h.email.sent) != 0 {
		t.Errorf("the email channel was handed %d messages, want none", len(h.email.sent))
	}
	if keys := h.kv.keys(); len(keys) != 0 {
		t.Errorf("the store holds %v, want no resend window, no budget and no pending record", keys)
	}
	if len(human.calls) != 1 {
		t.Fatalf("the gate was asked %d times, want once", len(human.calls))
	}
	if human.calls[0].RemoteIP != "192.0.2.1" {
		t.Errorf("the gate was handed RemoteIP %q, want the request's 192.0.2.1", human.calls[0].RemoteIP)
	}

	ok := h.post("/identifiers", `{"kind":"email","value":"ada@example.org","human_proof":"proof-1"}`)
	if ok.Code != http.StatusOK {
		t.Fatalf("the same send with a valid proof answered %d: %s", ok.Code, ok.Body.String())
	}
	if len(h.email.sent) != 1 {
		t.Errorf("the email channel was handed %d messages, want one", len(h.email.sent))
	}
	if len(human.calls) != 2 {
		t.Errorf("the gate was asked %d times, want twice: the refusal and the accepted send", len(human.calls))
	}
}

// TestAnEmailAddWithAValidProofAsksTheGateOnce holds the accepted path on its
// own: exactly one code is sent and the gate is asked exactly once.
func TestAnEmailAddWithAValidProofAsksTheGateOnce(t *testing.T) {
	human := requiredHuman()
	h := withHuman(t, human, func(c *config) { c.email = true })

	rec := h.post("/identifiers", `{"kind":"email","value":"ada@example.org","human_proof":"proof-1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("an email add with a valid proof answered %d: %s", rec.Code, rec.Body.String())
	}
	if got := problemType(t, rec); got != "" {
		t.Errorf("the accepted send carries the problem code %q", got)
	}
	if len(h.email.sent) != 1 {
		t.Fatalf("the email channel was handed %d messages, want one", len(h.email.sent))
	}
	if len(human.calls) != 1 {
		t.Fatalf("the gate was asked %d times, want once", len(human.calls))
	}
	if got := human.calls[0].Token; got != "proof-1" {
		t.Errorf("the gate was handed proof %q, want the request's proof-1", got)
	}
}

// TestTheUsernameBranchNeedsNoProof holds that the gate follows the send: adding
// a username sends nothing, so it asks for no proof even when the deployment
// requires one.
func TestTheUsernameBranchNeedsNoProof(t *testing.T) {
	human := requiredHuman()
	h := withHuman(t, human)

	rec := h.post("/identifiers", `{"kind":"username","value":"ada2"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("a username add answered %d: %s", rec.Code, rec.Body.String())
	}
	if len(human.calls) != 0 {
		t.Errorf("the gate was asked %v, want not asked for a branch that sends nothing", human.calls)
	}
	if len(h.admin.added) != 1 {
		t.Errorf("the identifier was written %d times, want once", len(h.admin.added))
	}
}

// TestAnEmailAddWithNoGateProceeds holds the default: a deployment with no gate,
// or one that requires nothing, sends the email code without asking for a proof.
func TestAnEmailAddWithNoGateProceeds(t *testing.T) {
	for _, human := range []authn.HumanVerifier{nil, &fakeHuman{required: false, provider: "off"}} {
		h := newHarness(t, func(c *config) {
			c.email = true
			c.mutateDeps = func(d *selfservice.Deps) { d.Human = human }
		})
		rec := h.post("/identifiers", `{"kind":"email","value":"ada@example.org"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("an email send with no gate answered %d: %s", rec.Code, rec.Body.String())
		}
		if len(h.email.sent) != 1 {
			t.Errorf("the email channel was handed %d messages, want one", len(h.email.sent))
		}
	}
}

// TestAnEmailAddHumanFailureMapsToItsAnswer holds the seam's failures on the
// email branch: an unavailable provider is a 503 and a proof the provider refuses
// as too long is a 400, and neither sends anything.
func TestAnEmailAddHumanFailureMapsToItsAnswer(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code int
		typ  string
	}{
		{"a provider that cannot answer", authn.ErrUnavailable, http.StatusServiceUnavailable, mount.CodeUnavailable},
		{"a proof the provider refuses as too long", authn.ErrInvalidArgument, http.StatusBadRequest, mount.CodeBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := withHuman(t, &fakeHuman{required: true, provider: "turnstile", err: fmt.Errorf("provider: %w", tc.err)},
				func(c *config) { c.email = true })
			rec := h.post("/identifiers", `{"kind":"email","value":"ada@example.org","human_proof":"proof-1"}`)
			if rec.Code != tc.code {
				t.Fatalf("a provider failing with %v answered %d, want %d: %s", tc.err, rec.Code, tc.code, rec.Body.String())
			}
			if got := problemType(t, rec); got != tc.typ {
				t.Errorf("the refusal code = %q, want %q", got, tc.typ)
			}
			if len(h.email.sent) != 0 {
				t.Errorf("the email channel was handed %d messages, want none", len(h.email.sent))
			}
			if keys := h.kv.keys(); len(keys) != 0 {
				t.Errorf("the store holds %v, want nothing", keys)
			}
		})
	}
}
