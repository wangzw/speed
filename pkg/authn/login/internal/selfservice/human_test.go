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
	// byScope, when set, answers Required per scope instead of the single
	// required flag, so a case can gate one surface and leave the other off.
	byScope        map[authn.HumanScope]bool
	provider       string
	siteKey        string
	accept         map[string]bool
	err            error
	calls          []authn.HumanProof
	requiredScopes []authn.HumanScope
	verifyScopes   []authn.HumanScope
}

func (h *fakeHuman) Required(scope authn.HumanScope) bool {
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
	h.calls = append(h.calls, p)
	h.verifyScopes = append(h.verifyScopes, scope)
	if h.err != nil {
		return h.err
	}
	if !h.required && h.byScope == nil {
		return nil
	}
	if p.Token == "" || !h.accept[p.Token] {
		return fmt.Errorf("%w: the proof is missing or not valid", authn.ErrHumanVerificationRequired)
	}
	return nil
}

// askedScopes is every scope the gate was asked about, through Required and
// Verify alike.
func (h *fakeHuman) askedScopes() []authn.HumanScope {
	scopes := make([]authn.HumanScope, 0, len(h.requiredScopes)+len(h.verifyScopes))
	scopes = append(scopes, h.requiredScopes...)
	scopes = append(scopes, h.verifyScopes...)
	return scopes
}

// assertOnlyScope holds that every question the gate was asked named want.
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
// itself must hold it to the human-verification gate. The gate runs before the
// address's occupancy is read and before the resend window is claimed or a code
// is drawn. A refused proof therefore spends no window, so the same request
// succeeds once a proof is presented.
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

// TestAddEmailChecksTheProofBeforeTheOccupancy holds the order: a request that
// does not pass the gate answers the human-verification refusal even when the
// address belongs to another account, so a signed-in account cannot probe which
// addresses are in use without a proof. The occupancy read is not reached, and a
// caller that does present a proof still learns email-taken.
func TestAddEmailChecksTheProofBeforeTheOccupancy(t *testing.T) {
	human := requiredHuman()
	h := withHuman(t, human, func(c *config) { c.email = true })
	h.accounts.lookup["taken@example.com"] = "someone-else"

	rec := h.post("/identifiers", addBody("email", "taken@example.com"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a taken address with no proof answered %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if got := problemType(t, rec); got != mount.CodeHumanVerificationRequired {
		t.Errorf("the refusal code = %q, want %q", got, mount.CodeHumanVerificationRequired)
	}
	if len(h.accounts.looked) != 0 {
		t.Errorf("the occupancy read was asked for %v before the gate passed, want not read", h.accounts.looked)
	}
	if len(h.email.sent) != 0 {
		t.Errorf("the email channel was handed %d messages, want none", len(h.email.sent))
	}

	taken := h.post("/identifiers", `{"kind":"email","value":"taken@example.com","human_proof":"proof-1"}`)
	if taken.Code != http.StatusConflict {
		t.Fatalf("a taken address with a valid proof answered %d, want 409: %s", taken.Code, taken.Body.String())
	}
	if got := problemType(t, taken); got != "email-taken" {
		t.Errorf("the refusal code = %q, want email-taken", got)
	}
}

// TestAnEmailAddWithAValidProofAsksTheGateOnce holds the accepted path on its
// own: exactly one code is sent and the gate is asked exactly once. The email
// code goes out over the email channel, so the proof never reaches the SMS
// sender: a second check there would consume a single-use proof again.
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
	if len(h.sms.sent) != 0 {
		t.Errorf("the SMS sender was handed %d messages, want none: the email branch has no code engine", len(h.sms.sent))
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

// TestAnEmailAddWithAnOffGateProceeds holds the off gate: a deployment whose
// gate requires nothing sends the email code without asking for a proof.
func TestAnEmailAddWithAnOffGateProceeds(t *testing.T) {
	h := newHarness(t, func(c *config) {
		c.email = true
		c.mutateDeps = func(d *selfservice.Deps) { d.Human = &fakeHuman{provider: "off"} }
	})
	rec := h.post("/identifiers", `{"kind":"email","value":"ada@example.org"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("an email send with an off gate answered %d: %s", rec.Code, rec.Body.String())
	}
	if len(h.email.sent) != 1 {
		t.Errorf("the email channel was handed %d messages, want one", len(h.email.sent))
	}
}

// TestAnEmailAddWithNoGateOnAHandBuiltServiceProceeds holds the defence a
// hand-constructed service relies on. The login engine requires the gate, so a
// service built by the engine always carries one; this case builds the service
// by hand with none, which no assembly produces.
func TestAnEmailAddWithNoGateOnAHandBuiltServiceProceeds(t *testing.T) {
	h := newHarness(t, func(c *config) { c.email = true })
	rec := h.post("/identifiers", `{"kind":"email","value":"ada@example.org"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("an email send with no gate answered %d: %s", rec.Code, rec.Body.String())
	}
	if len(h.email.sent) != 1 {
		t.Errorf("the email channel was handed %d messages, want one", len(h.email.sent))
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
		{"a proof the provider refuses as too long", authn.ErrHumanProofTooLong, http.StatusBadRequest, mount.CodeBadRequest},
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

// TestAnEmailAddAsksTheOtherScopeAndOnlyTheOtherScope holds the surface adding
// an email is gated on. The code goes to an email address, not a phone number,
// so it is held to the other surface: a deployment that gates only the other
// surface refuses a proofless add, and one that gates only the phone surface
// lets it through without ever asking the gate about that phone surface.
func TestAnEmailAddAsksTheOtherScopeAndOnlyTheOtherScope(t *testing.T) {
	t.Run("the other surface gates the email add", func(t *testing.T) {
		human := &fakeHuman{
			byScope:  map[authn.HumanScope]bool{authn.HumanScopeOther: true},
			provider: "pow",
			accept:   map[string]bool{},
		}
		h := withHuman(t, human, func(c *config) { c.email = true })

		rec := h.post("/identifiers", `{"kind":"email","value":"ada@example.org"}`)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("an email add gated only on the other surface answered %d, want 403: %s", rec.Code, rec.Body.String())
		}
		if got := problemType(t, rec); got != mount.CodeHumanVerificationRequired {
			t.Errorf("the refusal code = %q, want %q", got, mount.CodeHumanVerificationRequired)
		}
		assertOnlyScope(t, human, authn.HumanScopeOther)
		if len(h.email.sent) != 0 {
			t.Errorf("the email channel was handed %d messages, want none", len(h.email.sent))
		}
	})

	t.Run("the phone surface does not gate the email add", func(t *testing.T) {
		human := &fakeHuman{
			byScope:  map[authn.HumanScope]bool{authn.HumanScopePhone: true},
			provider: "captcha",
			accept:   map[string]bool{},
		}
		h := withHuman(t, human, func(c *config) { c.email = true })

		rec := h.post("/identifiers", `{"kind":"email","value":"ada@example.org"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("an email add gated only on the phone surface answered %d, want 200: %s", rec.Code, rec.Body.String())
		}
		assertOnlyScope(t, human, authn.HumanScopeOther)
		if len(h.email.sent) != 1 {
			t.Errorf("the email channel was handed %d messages, want one", len(h.email.sent))
		}
	})
}

// TestThePhoneAddLeavesTheProofToTheSender holds that the phone route does not
// ask the gate itself: the proof travels to SMSCodes.Send, where the phone
// surface's check consumes it. Asking the gate here as well would consume a
// single-use proof twice.
func TestThePhoneAddLeavesTheProofToTheSender(t *testing.T) {
	human := &fakeHuman{required: true, provider: "captcha", accept: map[string]bool{"proof-1": true}}
	h := withHuman(t, human)

	rec := h.post("/phone", `{"phone":"+8613800000000","human_proof":"proof-1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("a phone add answered %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if len(h.sms.sent) != 1 {
		t.Fatalf("the SMS channel was handed %d messages, want one", len(h.sms.sent))
	}
	if got := h.sms.sent[0].Proof; got != "proof-1" {
		t.Errorf("the code engine was handed proof %q, want the request's proof-1", got)
	}
	if scopes := human.askedScopes(); len(scopes) != 0 {
		t.Errorf("the route asked the gate about %v, want not asked: the sender checks the proof", scopes)
	}
}
