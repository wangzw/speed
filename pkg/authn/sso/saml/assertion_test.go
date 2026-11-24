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

var _ func(*saml2.SAMLServiceProvider, string) (*types.Response, error) = (*saml2.SAMLServiceProvider).ValidateEncodedResponse

// TestPreferredUsernameIsReadFromItsAttribute checks that preferred_username is read from its attribute.
func TestPreferredUsernameIsReadFromItsAttribute(t *testing.T) {
	for _, tc := range []struct {
		name string
		attr types.Attribute
		want string
	}{
		{"the name", types.Attribute{Name: preferredUsernameAttribute, Values: []types.AttributeValue{{Value: "alice"}}}, "alice"},
		{"the friendly name", types.Attribute{FriendlyName: preferredUsernameAttribute, Values: []types.AttributeValue{{Value: "alice"}}}, "alice"},
		{"absent", types.Attribute{Name: "urn:oid:0.9.2342.19200300.100.1.1", Values: []types.AttributeValue{{Value: "alice"}}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertion := &types.Assertion{AttributeStatement: &types.AttributeStatement{Attributes: []types.Attribute{tc.attr}}}
			if got := attributeValue(assertion, preferredUsernameAttribute); got != tc.want {
				t.Errorf("attributeValue = %q, want %q", got, tc.want)
			}
		})
	}
}

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

// TestAValidAssertionProvesTheExternalIdentity checks that a valid assertion proves the external identity.
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

// TestAnUnsignedResponseIsRefused checks that an unsigned response is refused.
func TestAnUnsignedResponseIsRefused(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	p, sink := newTestProduct(t, newFakeProviders(row))
	signed := signResponse(t, idp, defaultOpts())
	unsigned := stripSignature(t, signed)

	got := consume(t, p, row, unsigned, "relay")
	if out := mustRefuse(t, got, sink); out != outcomeSignature {
		t.Errorf("outcome = %q, want %q", out, outcomeSignature)
	}
}

// TestAnEmptyDestinationIsRefused checks that an empty destination is refused.
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

// TestAMismatchedInResponseToIsRefused checks that a mismatched InResponseTo is refused.
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

// TestAMismatchedRelayStateIsRefused checks that a mismatched RelayState is refused.
func TestAMismatchedRelayStateIsRefused(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	p, sink := newTestProduct(t, newFakeProviders(row))
	st := spState()

	got := consumeWith(t, p, row, st, signResponse(t, idp, defaultOpts()), "another-relay")
	if out := mustRefuse(t, got, sink); out != outcomeOther {
		t.Errorf("outcome = %q, want %q", out, outcomeOther)
	}
}

// TestTwoAssertionsAreRefused checks that two assertions are refused.
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

// TestAnAssertionWithNoAudienceRestrictionIsRefused checks that an assertion with no audience restriction is refused.
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

// TestConditionsWithoutAnAudienceRestrictionAreRefused checks that conditions without an audience restriction are refused.
func TestConditionsWithoutAnAudienceRestrictionAreRefused(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	p, sink := newTestProduct(t, newFakeProviders(row))
	o := defaultOpts()
	o.noAudienceListing = true

	got := consume(t, p, row, signResponse(t, idp, o), "relay")
	if out := mustRefuse(t, got, sink); out != outcomeAudience {
		t.Errorf("outcome = %q, want %q", out, outcomeAudience)
	}
}

// TestAnAudienceOfAnotherServiceProviderIsRefused checks that an audience of another service provider is refused.
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

// TestAnAssertionOutsideItsValidityIsRefused checks that an assertion outside its validity is refused.
func TestAnAssertionOutsideItsValidityIsRefused(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	p, sink := newTestProduct(t, newFakeProviders(row))
	o := defaultOpts()
	o.notBefore = time.Now().UTC().Add(time.Hour)

	got := consume(t, p, row, signResponse(t, idp, o), "relay")
	if out := mustRefuse(t, got, sink); out != outcomeTime {
		t.Errorf("outcome = %q, want %q", out, outcomeTime)
	}
}

// TestAnAssertionOlderThanTheAllowedAgeIsRefused checks that an assertion older than the allowed age is refused.
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

// TestAnAssertionWithTooLongALifetimeIsRefused checks that an assertion with too long a lifetime is refused.
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

// TestASHA1SignatureIsRefusedBeforeTheLibraryRuns checks that a SHA-1 signature is refused before the library runs.
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

// TestAPermittedSHA1AssertionSkipsThePreCheck checks that a permitted SHA-1 assertion skips the pre-check.
func TestAPermittedSHA1AssertionSkipsThePreCheck(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	row.AllowSHA1 = true
	p, _ := newTestProduct(t, newFakeProviders(row))
	encoded := signSHA1Response(t, idp, defaultOpts())

	if usesSHA1(encoded) && row.AllowSHA1 {
		got := consume(t, p, row, encoded, "relay")
		if cause := step.CauseOf(got); cause != nil && strings.Contains(cause.Error(), "SHA-1 algorithm") {
			t.Errorf("the pre-check refused a SHA-1 response the provider permits: %v", cause)
		}
	}
}

// TestTheSHA1PreCheckOnlyReadsAlgorithmAttributes checks that the SHA-1 pre-check reads algorithm attributes only.
func TestTheSHA1PreCheckOnlyReadsAlgorithmAttributes(t *testing.T) {
	body := `<root><Data>c2hhMQ==</Data><SignatureMethod Algorithm="http://www.w3.org/2000/09/xmldsig#rsa-sha256"/></root>`
	if usesSHA1(base64Of(body)) {
		t.Error("a non-algorithm occurrence of sha1 tripped the pre-check")
	}
	sha1Body := `<root><SignatureMethod Algorithm="http://www.w3.org/2000/09/xmldsig#rsa-sha1"/></root>`
	if !usesSHA1(base64Of(sha1Body)) {
		t.Error("a SHA-1 Algorithm attribute was not seen")
	}
}

// TestAReplayOfAnAssertionIsRefused checks that a replay of an assertion is refused.
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

// TestANameIDWithAnXMLCommentIsNotTruncated checks that a NameID containing an XML comment is not truncated.
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
	default:
		t.Fatalf("answer = %s, want proven with the full subject or a refusal", step.KindOf(got))
	}
}

// TestASubjectFromAConfiguredAttributeIsProven checks that a subject from a configured attribute is proven.
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

// TestAnIdPInitiatedAssertionIsAcceptedWhenFresh checks that an IdP-initiated assertion is accepted when fresh.
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

// TestAnIdPInitiatedAssertionIsRefusedWhenStale checks that an IdP-initiated assertion is refused when stale.
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

// TestAnIdPInitiatedAssertionIsRefusedWhenTheProviderForbidsIt checks that an IdP-initiated assertion is refused when the provider forbids it.
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

// TestAnswerReadsTheProviderFromTheFlowState checks that Answer reads the provider from the flow state.
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

// TestAnAssertionFromAnotherIssuerIsRefused checks that an assertion from another issuer is refused.
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

// TestAResidualEncryptedAssertionIsRefused checks that a residual encrypted assertion is refused.
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

// TestTheValidatedResponseComesBackSignatureChecked checks that the validated response comes back signature-checked.
func TestTheValidatedResponseComesBackSignatureChecked(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	p, _ := newTestProduct(t, newFakeProviders(row))
	b, err := p.buildServiceProvider(row)
	if err != nil {
		t.Fatalf("building the service provider: %v", err)
	}

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

// TestAProviderRegisterWithoutTheReplayLineIsNotAnAssertionLog checks that a provider register without the replay line cannot log an assertion.
func TestAProviderRegisterWithoutTheReplayLineIsNotAnAssertionLog(t *testing.T) {
	var withReplay authn.SSOProviders = newFakeProviders(providerRow(nil))
	if _, ok := withReplay.(assertionLog); !ok {
		t.Error("the fake register does not record the replay line")
	}
	var withoutReplay struct{ authn.SSOProviders }
	if _, ok := any(withoutReplay).(assertionLog); ok {
		t.Error("a register without RememberAssertion was taken as one that records the replay line")
	}
}

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
