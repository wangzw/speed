package selfservice_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
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

// TestStartPhoneWithoutAProofIsRefusedAndSendsNothing holds the phone
// self-service send: the number is validated first, then the gate refuses the
// request before the pending record is read or a code is drawn, so nothing is
// sent, nothing is stored and no budget is taken.
func TestStartPhoneWithoutAProofIsRefusedAndSendsNothing(t *testing.T) {
	human := requiredHuman()
	h := withHuman(t, human)

	rec := h.post("/phone", `{"phone":"+8613800000000"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a phone send with no proof answered %d: %s", rec.Code, rec.Body.String())
	}
	if got := problemType(t, rec); got != mount.CodeHumanVerificationRequired {
		t.Errorf("the refusal code = %q, want %q", got, mount.CodeHumanVerificationRequired)
	}
	if len(h.sms.sent) != 0 {
		t.Errorf("the SMS channel was handed %d messages, want none", len(h.sms.sent))
	}
	if keys := h.kv.keys(); len(keys) != 0 {
		t.Errorf("the store holds %v, want no pending record and no budget", keys)
	}
	if len(human.calls) != 1 {
		t.Fatalf("the gate was asked %d times, want once", len(human.calls))
	}
	if human.calls[0].RemoteIP != "192.0.2.1" {
		t.Errorf("the gate was handed RemoteIP %q, want the request's 192.0.2.1", human.calls[0].RemoteIP)
	}

	ok := h.post("/phone", `{"phone":"+8613800000000","human_proof":"proof-1"}`)
	if ok.Code != http.StatusOK {
		t.Fatalf("the same send with a valid proof answered %d: %s", ok.Code, ok.Body.String())
	}
	if len(h.sms.sent) != 1 {
		t.Errorf("the SMS channel was handed %d messages, want one", len(h.sms.sent))
	}
}

// TestAddEmailWithoutAProofIsRefusedAndSendsNothing holds the email branch: the
// address is checked for being free first, then the gate refuses the request
// before the resend window is claimed or a code is drawn. A refused proof
// therefore spends no window, so the same request succeeds once a proof is
// presented.
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

	ok := h.post("/identifiers", `{"kind":"email","value":"ada@example.org","human_proof":"proof-1"}`)
	if ok.Code != http.StatusOK {
		t.Fatalf("the same send with a valid proof answered %d: %s", ok.Code, ok.Body.String())
	}
	if len(h.email.sent) != 1 {
		t.Errorf("the email channel was handed %d messages, want one", len(h.email.sent))
	}
	if got := problemType(t, ok); got != "" {
		t.Errorf("the accepted send carries the problem code %q", got)
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

// TestASelfServiceSendWithNoGateProceeds holds the default: a deployment with no
// gate, or one that requires nothing, sends without asking for a proof.
func TestASelfServiceSendWithNoGateProceeds(t *testing.T) {
	for _, human := range []authn.HumanVerifier{nil, &fakeHuman{required: false, provider: "off"}} {
		h := newHarness(t, func(c *config) {
			c.mutateDeps = func(d *selfservice.Deps) { d.Human = human }
		})
		rec := h.post("/phone", `{"phone":"+8613800000000"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("a send with no gate answered %d: %s", rec.Code, rec.Body.String())
		}
		if len(h.sms.sent) != 1 {
			t.Errorf("the SMS channel was handed %d messages, want one", len(h.sms.sent))
		}
	}
}

// TestASelfServiceHumanFailureMapsToItsAnswer holds the seam's failures on the
// self-service routes: an unavailable provider is a 503 and a proof the provider
// refuses as too long is a 400, and neither sends anything.
func TestASelfServiceHumanFailureMapsToItsAnswer(t *testing.T) {
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
			h := withHuman(t, &fakeHuman{required: true, provider: "turnstile", err: fmt.Errorf("provider: %w", tc.err)})
			rec := h.post("/phone", `{"phone":"+8613800000000","human_proof":"proof-1"}`)
			if rec.Code != tc.code {
				t.Fatalf("a provider failing with %v answered %d, want %d: %s", tc.err, rec.Code, tc.code, rec.Body.String())
			}
			if got := problemType(t, rec); got != tc.typ {
				t.Errorf("the refusal code = %q, want %q", got, tc.typ)
			}
			if len(h.sms.sent) != 0 {
				t.Errorf("the SMS channel was handed %d messages, want none", len(h.sms.sent))
			}
			if keys := h.kv.keys(); len(keys) != 0 {
				t.Errorf("the store holds %v, want nothing", keys)
			}
		})
	}
}

// TestAnOverLongSelfServiceProofIsABadRequest holds the field's own bound on the
// self-service sends: a proof over 8 KiB is a 400 before the provider is asked.
func TestAnOverLongSelfServiceProofIsABadRequest(t *testing.T) {
	human := requiredHuman()
	h := withHuman(t, human, func(c *config) { c.email = true })
	proof := strings.Repeat("x", mount.HumanProofMaxBytes+1)

	for _, route := range []struct {
		name string
		path string
		body string
	}{
		{"phone", "/phone", fmt.Sprintf(`{"phone":"+8613800000000","human_proof":%q}`, proof)},
		{"email", "/identifiers", fmt.Sprintf(`{"kind":"email","value":"ada@example.org","human_proof":%q}`, proof)},
	} {
		t.Run(route.name, func(t *testing.T) {
			rec := h.post(route.path, route.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("an over-long proof answered %d: %s", rec.Code, rec.Body.String())
			}
			if got := problemType(t, rec); got != mount.CodeBadRequest {
				t.Errorf("the refusal code = %q, want %q", got, mount.CodeBadRequest)
			}
		})
	}
	if len(human.calls) != 0 {
		t.Errorf("the provider was asked %d times, want none for a proof over the field's bound", len(human.calls))
	}
	if len(h.sms.sent) != 0 || len(h.email.sent) != 0 {
		t.Errorf("a message was sent for an over-long proof: sms %d, email %d", len(h.sms.sent), len(h.email.sent))
	}
}
