package saml

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	saml2 "github.com/russellhaering/gosaml2"
	"github.com/russellhaering/gosaml2/types"
	dsig "github.com/russellhaering/goxmldsig"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/step"
)

// The design names the library version's return type as an assumption the
// single-parse verification rests on. This compile-time assertion breaks if a
// future gosaml2 changes it.
var _ func(*saml2.SAMLServiceProvider, string) (*types.Response, error) = (*saml2.SAMLServiceProvider).ValidateEncodedResponse

// consumeWith drives the assertion checks with a chosen method state, as
// Answer does after resolving the provider and building the service provider.
func consumeWith(t *testing.T, p *product, row authn.SSOProvider, st methodState, encoded, relay string) authn.Step {
	t.Helper()
	b, err := p.buildServiceProvider(row)
	if err != nil {
		t.Fatalf("building the service provider: %v", err)
	}
	return p.consume(context.Background(), row, b, st, encoded, relay)
}

func spState() methodState {
	return methodState{Version: stateVersion, Request: "_request-1", Relay: "relay", Provider: testProviderID}
}

// mustRefuse fails unless the step is a refusal whose cause wraps
// ErrInvalidCredentials, and returns the outcome it was counted under.
func mustRefuse(t *testing.T, got authn.Step, sink *fakeAudit) string {
	t.Helper()
	if step.KindOf(got) != step.KindRefused {
		t.Fatalf("the assertion was answered %s, want a refusal", step.KindOf(got))
	}
	if !errors.Is(step.CauseOf(got), authn.ErrInvalidCredentials) {
		t.Errorf("the cause %v does not wrap ErrInvalidCredentials", step.CauseOf(got))
	}
	return rejectOutcome(sink)
}

func TestAValidAssertionProvesTheExternalIdentity(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	p, sink := newTestProduct(t, newFakeProviders(row))
	encoded := signResponse(t, idp, defaultOpts())

	got := consume(t, p, row, encoded, "relay")
	if step.KindOf(got) != step.KindProven {
		t.Fatalf("answer = %s, want proven (%v)", step.KindOf(got), step.CauseOf(got))
	}
	proof := step.ProofOf(got)
	if proof.External == nil {
		t.Fatal("the proof carries no external identity")
	}
	if proof.External.Provider != "saml:"+testProviderID {
		t.Errorf("provider = %q", proof.External.Provider)
	}
	if proof.External.Subject != "alice" {
		t.Errorf("subject = %q, want alice", proof.External.Subject)
	}
	if proof.External.Tenant != testTenant {
		t.Errorf("tenant = %q", proof.External.Tenant)
	}
	if proof.Provider != testProviderID {
		t.Errorf("proof.Provider = %q", proof.Provider)
	}
	if len(proof.Methods) != 1 || proof.Methods[0] != authn.MethodSAML {
		t.Errorf("methods = %v", proof.Methods)
	}
	if rejectOutcome(sink) != "" {
		t.Errorf("a valid assertion wrote an audit row: %v", sink.entries)
	}
}

func TestAnUnsignedResponseIsRefused(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	p, sink := newTestProduct(t, newFakeProviders(row))
	// The valid response, but with its signature stripped before transport.
	signed := signResponse(t, idp, defaultOpts())
	unsigned := stripSignature(t, signed)

	got := consume(t, p, row, unsigned, "relay")
	if out := mustRefuse(t, got, sink); out != outcomeSignature {
		t.Errorf("outcome = %q, want %q", out, outcomeSignature)
	}
}

func TestAnEmptyDestinationIsRefused(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	p, sink := newTestProduct(t, newFakeProviders(row))
	o := defaultOpts()
	o.destination = ""

	got := consume(t, p, row, signResponse(t, idp, o), "relay")
	if out := mustRefuse(t, got, sink); out != outcomeDestination {
		t.Errorf("outcome = %q, want %q", out, outcomeDestination)
	}
}

func TestAMismatchedInResponseToIsRefused(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	p, sink := newTestProduct(t, newFakeProviders(row))
	o := defaultOpts()
	o.inResponseTo = "_other-request"
	o.scdInResponse = "_other-request"

	got := consume(t, p, row, signResponse(t, idp, o), "relay")
	if out := mustRefuse(t, got, sink); out != outcomeInResponse {
		t.Errorf("outcome = %q, want %q", out, outcomeInResponse)
	}
}

func TestAMismatchedRelayStateIsRefused(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	p, sink := newTestProduct(t, newFakeProviders(row))
	st := spState() // its Relay is "relay"

	got := consumeWith(t, p, row, st, signResponse(t, idp, defaultOpts()), "another-relay")
	if out := mustRefuse(t, got, sink); out != outcomeOther {
		t.Errorf("outcome = %q, want %q", out, outcomeOther)
	}
}

func TestTwoAssertionsAreRefused(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	p, sink := newTestProduct(t, newFakeProviders(row))
	o := defaultOpts()
	o.extraAssertion = true

	got := consume(t, p, row, signResponse(t, idp, o), "relay")
	if out := mustRefuse(t, got, sink); out != outcomeOther {
		t.Errorf("outcome = %q, want %q", out, outcomeOther)
	}
}

func TestAnAssertionWithNoAudienceRestrictionIsRefused(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	p, sink := newTestProduct(t, newFakeProviders(row))
	o := defaultOpts()
	o.omitAudience = true

	got := consume(t, p, row, signResponse(t, idp, o), "relay")
	if out := mustRefuse(t, got, sink); out != outcomeAudience {
		t.Errorf("outcome = %q, want %q", out, outcomeAudience)
	}
}

func TestConditionsWithoutAnAudienceRestrictionAreRefused(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	p, sink := newTestProduct(t, newFakeProviders(row))
	o := defaultOpts()
	o.noAudienceListing = true // Conditions present, but no AudienceRestriction

	got := consume(t, p, row, signResponse(t, idp, o), "relay")
	if out := mustRefuse(t, got, sink); out != outcomeAudience {
		t.Errorf("outcome = %q, want %q", out, outcomeAudience)
	}
}

func TestAnAudienceOfAnotherServiceProviderIsRefused(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	p, sink := newTestProduct(t, newFakeProviders(row))
	o := defaultOpts()
	o.audience = "https://elsewhere.example.com/metadata"

	got := consume(t, p, row, signResponse(t, idp, o), "relay")
	if out := mustRefuse(t, got, sink); out != outcomeAudience {
		t.Errorf("outcome = %q, want %q", out, outcomeAudience)
	}
}

func TestAnAssertionOutsideItsValidityIsRefused(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	p, sink := newTestProduct(t, newFakeProviders(row))
	o := defaultOpts()
	o.notBefore = time.Now().UTC().Add(time.Hour) // invalid time, not yet valid

	got := consume(t, p, row, signResponse(t, idp, o), "relay")
	if out := mustRefuse(t, got, sink); out != outcomeTime {
		t.Errorf("outcome = %q, want %q", out, outcomeTime)
	}
}

func TestAnAssertionOlderThanTheAllowedAgeIsRefused(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	p, sink := newTestProduct(t, newFakeProviders(row))
	o := defaultOpts()
	o.issueInstant = time.Now().UTC().Add(-10 * time.Minute)
	o.notBefore = o.issueInstant.Add(-time.Minute)

	got := consume(t, p, row, signResponse(t, idp, o), "relay")
	if out := mustRefuse(t, got, sink); out != outcomeTime {
		t.Errorf("outcome = %q, want %q", out, outcomeTime)
	}
}

func TestAnAssertionWithTooLongALifetimeIsRefused(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	p, sink := newTestProduct(t, newFakeProviders(row))
	o := defaultOpts()
	o.notOnOrAfter = o.issueInstant.Add(20 * time.Minute)
	o.scdNotOnOrAfter = o.issueInstant.Add(20 * time.Minute)

	got := consume(t, p, row, signResponse(t, idp, o), "relay")
	if out := mustRefuse(t, got, sink); out != outcomeTime {
		t.Errorf("outcome = %q, want %q", out, outcomeTime)
	}
}

func TestASHA1SignatureIsRefusedBeforeTheLibraryRuns(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	p, sink := newTestProduct(t, newFakeProviders(row))
	encoded := signSHA1Response(t, idp, defaultOpts())
	if !usesSHA1(encoded) {
		t.Fatal("the SHA-1 response was not seen by the pre-check")
	}

	got := consume(t, p, row, encoded, "relay")
	if out := mustRefuse(t, got, sink); out != outcomeSignature {
		t.Errorf("outcome = %q, want %q", out, outcomeSignature)
	}
}

func TestAPermittedSHA1AssertionSkipsThePreCheck(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	row.AllowSHA1 = true
	p, _ := newTestProduct(t, newFakeProviders(row))
	encoded := signSHA1Response(t, idp, defaultOpts())

	// With SHA-1 permitted, the pre-check does not refuse; the library's own
	// validation decides. The observation that matters is that the pre-check no
	// longer turns the response away on the algorithm alone.
	if usesSHA1(encoded) && row.AllowSHA1 {
		got := consume(t, p, row, encoded, "relay")
		if cause := step.CauseOf(got); cause != nil && strings.Contains(cause.Error(), "SHA-1 algorithm") {
			t.Errorf("the pre-check refused a SHA-1 response the provider permits: %v", cause)
		}
	}
}

func TestTheSHA1PreCheckOnlyReadsAlgorithmAttributes(t *testing.T) {
	// A digest value that happens to spell "sha1" in its base64 must not trip
	// the pre-check, which reads Algorithm attributes alone.
	body := `<root><Data>c2hhMQ==</Data><SignatureMethod Algorithm="http://www.w3.org/2000/09/xmldsig#rsa-sha256"/></root>`
	if usesSHA1(base64Of(body)) {
		t.Error("a non-algorithm occurrence of sha1 tripped the pre-check")
	}
	sha1Body := `<root><SignatureMethod Algorithm="http://www.w3.org/2000/09/xmldsig#rsa-sha1"/></root>`
	if !usesSHA1(base64Of(sha1Body)) {
		t.Error("a SHA-1 Algorithm attribute was not seen")
	}
}

func TestAReplayOfAnAssertionIsRefused(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	p, sink := newTestProduct(t, newFakeProviders(row))
	encoded := signResponse(t, idp, defaultOpts())

	if got := consume(t, p, row, encoded, "relay"); step.KindOf(got) != step.KindProven {
		t.Fatalf("the first consumption = %s, want proven", step.KindOf(got))
	}
	got := consume(t, p, row, encoded, "relay")
	if out := mustRefuse(t, got, sink); out != outcomeReplay {
		t.Errorf("outcome = %q, want %q", out, outcomeReplay)
	}
}

func TestANameIDWithAnXMLCommentIsNotTruncated(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	p, _ := newTestProduct(t, newFakeProviders(row))
	o := defaultOpts()
	o.username = "alice<!--not-a-name-->@example.com"

	got := consume(t, p, row, signResponse(t, idp, o), "relay")
	switch step.KindOf(got) {
	case step.KindProven:
		subject := step.ProofOf(got).External.Subject
		if subject == "alice" {
			t.Fatalf("the subject was truncated at the comment: %q", subject)
		}
	case step.KindRefused:
		// A refusal is the other safe outcome: nothing was taken from a comment.
	default:
		t.Fatalf("answer = %s, want proven with the full subject or a refusal", step.KindOf(got))
	}
}

func TestASubjectFromAConfiguredAttributeIsProven(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, map[string]any{
		"subject_attribute": "urn:oid:1.2.3",
		"claims":            map[string]string{"urn:oid:1.2.3": "subject", "mail": "email"},
	}))
	p, _ := newTestProduct(t, newFakeProviders(row))
	o := defaultOpts()
	o.attributes = map[string]string{"urn:oid:1.2.3": "bob", "mail": "bob@acme.test"}

	got := consume(t, p, row, signResponse(t, idp, o), "relay")
	if step.KindOf(got) != step.KindProven {
		t.Fatalf("answer = %s, want proven (%v)", step.KindOf(got), step.CauseOf(got))
	}
	ext := step.ProofOf(got).External
	if ext.Subject != "bob" {
		t.Errorf("subject = %q, want bob", ext.Subject)
	}
	if ext.Email != "bob@acme.test" {
		t.Errorf("email = %q", ext.Email)
	}
}

func TestAnIdPInitiatedAssertionIsAcceptedWhenFresh(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	row.AllowIDPInitiated = true
	p, _ := newTestProduct(t, newFakeProviders(row))
	o := defaultOpts()
	o.inResponseTo = ""
	o.scdInResponse = ""

	st := methodState{Version: stateVersion, Provider: testProviderID}
	got := consumeWith(t, p, row, st, signResponse(t, idp, o), "")
	if step.KindOf(got) != step.KindProven {
		t.Fatalf("answer = %s, want proven (%v)", step.KindOf(got), step.CauseOf(got))
	}
}

func TestAnIdPInitiatedAssertionIsRefusedWhenStale(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	row.AllowIDPInitiated = true
	p, sink := newTestProduct(t, newFakeProviders(row))
	o := defaultOpts()
	o.inResponseTo = ""
	o.scdInResponse = ""
	o.issueInstant = time.Now().UTC().Add(-5 * time.Minute)
	o.notBefore = o.issueInstant.Add(-time.Minute)

	st := methodState{Version: stateVersion, Provider: testProviderID}
	got := consumeWith(t, p, row, st, signResponse(t, idp, o), "")
	if out := mustRefuse(t, got, sink); out != outcomeTime {
		t.Errorf("outcome = %q, want %q", out, outcomeTime)
	}
}

func TestAnIdPInitiatedAssertionIsRefusedWhenTheProviderForbidsIt(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	p, sink := newTestProduct(t, newFakeProviders(row))
	o := defaultOpts()
	o.inResponseTo = ""
	o.scdInResponse = ""

	st := methodState{Version: stateVersion, Provider: testProviderID}
	got := consumeWith(t, p, row, st, signResponse(t, idp, o), "")
	if out := mustRefuse(t, got, sink); out != outcomeInResponse {
		t.Errorf("outcome = %q, want %q", out, outcomeInResponse)
	}
}

// TestAnswerReadsTheProviderFromTheFlowState holds the callback path the engine
// runs for an identity-provider-initiated sign-in: the flow was created on the
// spot, so it carries no method state, and the method reads the provider from the
// flow state and validates the assertion against it. A flow that names no
// provider at all is refused rather than validated without one.
func TestAnswerReadsTheProviderFromTheFlowState(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	row.AllowIDPInitiated = true
	p, _ := newTestProduct(t, newFakeProviders(row))
	o := defaultOpts()
	o.inResponseTo = ""
	o.scdInResponse = ""
	response := signResponse(t, idp, o)
	callback := func(provider string) authn.StepInput {
		return authn.StepInput{
			Action: authn.InputCallback,
			Form:   url.Values{"SAMLResponse": {response}, "RelayState": {""}},
		}
	}

	got, err := p.Answer(context.Background(), authn.FlowState{Purpose: authn.PurposeLogin, Provider: row.ID}, callback(row.ID))
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if step.KindOf(got) != step.KindProven {
		t.Fatalf("answer = %s, want proven (%v)", step.KindOf(got), step.CauseOf(got))
	}

	got, err = p.Answer(context.Background(), authn.FlowState{Purpose: authn.PurposeLogin}, callback(row.ID))
	if err != nil {
		t.Fatalf("Answer without a provider: %v", err)
	}
	if step.KindOf(got) != step.KindRefused {
		t.Errorf("answer without a provider = %s, want refused", step.KindOf(got))
	}
}

func TestAnAssertionFromAnotherIssuerIsRefused(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	p, sink := newTestProduct(t, newFakeProviders(row))
	o := defaultOpts()
	o.assertionIssuer = "https://evil.example.com/entity"

	got := consume(t, p, row, signResponse(t, idp, o), "relay")
	if out := mustRefuse(t, got, sink); out != outcomeIssuer && out != outcomeSignature {
		t.Errorf("outcome = %q, want issuer or signature", out)
	}
}

func TestAResidualEncryptedAssertionIsRefused(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	p, sink := newTestProduct(t, newFakeProviders(row))
	o := defaultOpts()
	o.encrypted = true

	got := consume(t, p, row, signResponse(t, idp, o), "relay")
	if step.KindOf(got) != step.KindRefused {
		t.Fatalf("a response with an encrypted assertion was answered %s, want a refusal", step.KindOf(got))
	}
	_ = rejectOutcome(sink)
}

func TestTheValidatedResponseComesBackSignatureChecked(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	p, _ := newTestProduct(t, newFakeProviders(row))
	b, err := p.buildServiceProvider(row)
	if err != nil {
		t.Fatalf("building the service provider: %v", err)
	}

	// The library decodes from the element it verified, so the returned response
	// is the verified one and carries the facts the later checks read.
	encoded := signResponse(t, idp, defaultOpts())
	resp, err := b.sp.ValidateEncodedResponse(encoded)
	if err != nil {
		t.Fatalf("ValidateEncodedResponse: %v", err)
	}
	if !resp.SignatureValidated {
		t.Error("the response came back without its signature validated")
	}
	if len(resp.Assertions) != 1 {
		t.Fatalf("the response carries %d assertions, want one", len(resp.Assertions))
	}
	if resp.Assertions[0].Issuer == nil || resp.Assertions[0].Issuer.Value != testIssuer {
		t.Errorf("the verified assertion does not carry the identity provider's issuer")
	}
}

func TestAProviderRegisterWithoutTheReplayLineIsNotAnAssertionLog(t *testing.T) {
	var withReplay authn.SSOProviders = newFakeProviders(providerRow(nil))
	if _, ok := withReplay.(assertionLog); !ok {
		t.Error("the fake register does not record the replay line")
	}
	// A register that carries only the SSOProviders method set is not one the
	// method can write the replay line through.
	var withoutReplay struct{ authn.SSOProviders }
	if _, ok := any(withoutReplay).(assertionLog); ok {
		t.Error("a register without RememberAssertion was taken as one that records the replay line")
	}
}

// signSHA1Response signs the response with an RSA-SHA-1 signature method, which
// the pre-check must turn away.
func signSHA1Response(t *testing.T, i *idp, o responseOpts) string {
	t.Helper()
	ctx := i.signingContext(t)
	if err := ctx.SetSignatureMethod(dsig.RSASHA1SignatureMethod); err != nil {
		t.Fatalf("setting the SHA-1 signature method: %v", err)
	}
	doc := readDoc(t, buildResponse(o))
	signed, err := ctx.SignEnveloped(doc.Root())
	if err != nil {
		t.Fatalf("signing the response with SHA-1: %v", err)
	}
	return encodeDoc(t, signed)
}
