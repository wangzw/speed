package saml

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	saml2 "github.com/russellhaering/gosaml2"
	"github.com/russellhaering/gosaml2/types"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/model"
)

type assertionLog interface {
	RememberAssertion(ctx context.Context, idpID, assertionID string, notOnOrAfter time.Time) error
}

const idpInitiatedMaxAge = 2 * time.Minute

const maxAssertionLifetime = 10 * time.Minute

func (p *product) consume(ctx context.Context, row authn.SSOProvider, b built, st methodState, encoded, relayState string) authn.Step {
	spInitiated := st.Request != ""

	// RelayState is checked when the flow drew one; an IdP-initiated flow drew none.
	if spInitiated && relayState != st.Relay {
		return p.reject(ctx, row.ID, outcomeOther, "the RelayState does not match the flow")
	}

	// The SHA-1 pre-check runs before the library's validation, whose context cannot refuse an algorithm.
	if !row.AllowSHA1 && usesSHA1(encoded) {
		return p.reject(ctx, row.ID, outcomeSignature, "the response names a SHA-1 algorithm")
	}

	// One parse verifies and decodes the same element, so the verified and used elements are one.
	resp, err := b.sp.ValidateEncodedResponse(encoded)
	if err != nil {
		return p.reject(ctx, row.ID, outcomeSignature, "the response did not validate")
	}
	if !resp.SignatureValidated && !anyAssertionSigned(resp) {
		return p.reject(ctx, row.ID, outcomeSignature, "neither the response nor an assertion was signed")
	}

	// Destination must be present and be this service provider's ACS; the library compares it only when present.
	if resp.Destination == "" || resp.Destination != b.sp.AssertionConsumerServiceURL {
		return p.reject(ctx, row.ID, outcomeDestination, "the destination is not this service provider's ACS")
	}

	if step := p.checkResponseTo(ctx, row, resp, st, spInitiated); step != nil {
		return *step
	}

	// Exactly one assertion is required: the library's retrieval uses the first one, which a wrapping attack relies on.
	if len(resp.Assertions) != 1 || len(resp.EncryptedAssertions) != 0 {
		return p.reject(ctx, row.ID, outcomeOther, "the response does not carry exactly one assertion")
	}
	assertion := resp.Assertions[0]

	if step := p.checkConditions(ctx, row, b, &assertion); step != nil {
		return *step
	}
	maxAge := p.settings.assertionMaxAge()
	if !spInitiated {
		// An IdP-initiated sign-in has no request to bind to, so its assertion must be correspondingly fresh.
		maxAge = idpInitiatedMaxAge
	}
	if step := p.checkConfirmation(ctx, row, b, &assertion, p.clock.Now(), maxAge); step != nil {
		return *step
	}

	notOnOrAfter, ok := assertionNotOnOrAfter(&assertion)
	if !ok {
		return p.reject(ctx, row.ID, outcomeTime, "the assertion carries no usable NotOnOrAfter")
	}
	if assertion.ID == "" {
		return p.reject(ctx, row.ID, outcomeOther, "the assertion carries no identifier")
	}
	switch err := p.assertions.RememberAssertion(ctx, row.ID, assertion.ID, notOnOrAfter); {
	case err == nil:
	case errors.Is(err, authn.ErrAssertionReplay):
		return p.reject(ctx, row.ID, outcomeReplay, "the assertion was already used")
	default:
		return stepUnavailable("recording the assertion", err)
	}

	proof, cause := p.extract(ctx, row, b.config, &assertion)
	if cause != "" {
		return p.reject(ctx, row.ID, outcomeOther, cause)
	}
	p.accept(ctx)
	return authn.StepProven(proof)
}

func (p *product) checkResponseTo(ctx context.Context, row authn.SSOProvider, resp *types.Response, st methodState, spInitiated bool) *authn.Step {
	if spInitiated {
		if resp.InResponseTo != st.Request {
			return stepPtr(p.reject(ctx, row.ID, outcomeInResponse, "the response answers another request"))
		}
	} else {
		if !row.AllowIDPInitiated {
			return stepPtr(p.reject(ctx, row.ID, outcomeInResponse, "the provider does not allow an identity-provider-initiated sign-in"))
		}
		if resp.InResponseTo != "" {
			return stepPtr(p.reject(ctx, row.ID, outcomeInResponse, "the response answers a request the flow did not draw"))
		}
	}
	for i := range resp.Assertions {
		scd := subjectConfirmationData(&resp.Assertions[i])
		if scd == nil {
			continue
		}
		got := scd.InResponseTo
		if spInitiated && got != st.Request {
			return stepPtr(p.reject(ctx, row.ID, outcomeInResponse, "an assertion answers another request"))
		}
		if !spInitiated && got != "" {
			return stepPtr(p.reject(ctx, row.ID, outcomeInResponse, "an assertion answers a request the flow did not draw"))
		}
	}
	return nil
}

func (p *product) checkConditions(ctx context.Context, row authn.SSOProvider, b built, assertion *types.Assertion) *authn.Step {
	warn, err := b.sp.VerifyAssertionConditions(assertion)
	if err != nil || warn == nil {
		return stepPtr(p.reject(ctx, row.ID, outcomeAudience, "the assertion's conditions cannot be read"))
	}
	if warn.InvalidTime {
		return stepPtr(p.reject(ctx, row.ID, outcomeTime, "the assertion's conditions are outside their validity"))
	}
	if warn.NotInAudience || warn.ProxyRestriction != nil {
		return stepPtr(p.reject(ctx, row.ID, outcomeAudience, "the assertion is not for this audience"))
	}
	if assertion.Conditions == nil || len(assertion.Conditions.AudienceRestrictions) == 0 {
		return stepPtr(p.reject(ctx, row.ID, outcomeAudience, "the assertion carries no audience restriction"))
	}
	if !audienceContains(assertion.Conditions, b.sp.AudienceURI) {
		return stepPtr(p.reject(ctx, row.ID, outcomeAudience, "the assertion's audience is not this service provider"))
	}
	return nil
}

func (p *product) checkConfirmation(ctx context.Context, row authn.SSOProvider, b built, assertion *types.Assertion, now time.Time, maxAge time.Duration) *authn.Step {
	if assertion.Issuer == nil || strings.TrimSpace(assertion.Issuer.Value) != b.config.IDPEntityID {
		return stepPtr(p.reject(ctx, row.ID, outcomeIssuer, "the assertion's issuer is not the identity provider"))
	}
	if b.config.IDPEntityID == "" {
		return stepPtr(p.reject(ctx, row.ID, outcomeIssuer, "the provider names no identity provider issuer"))
	}
	subject := assertion.Subject
	if subject == nil || subject.SubjectConfirmation == nil {
		return stepPtr(p.reject(ctx, row.ID, outcomeOther, "the assertion carries no subject confirmation"))
	}
	if subject.SubjectConfirmation.Method != saml2.SubjMethodBearer {
		return stepPtr(p.reject(ctx, row.ID, outcomeOther, "the subject confirmation is not a bearer one"))
	}
	scd := subject.SubjectConfirmation.SubjectConfirmationData
	if scd == nil || scd.Recipient != b.sp.AssertionConsumerServiceURL {
		return stepPtr(p.reject(ctx, row.ID, outcomeDestination, "the assertion's recipient is not this service provider's ACS"))
	}
	if now.Sub(assertion.IssueInstant) > maxAge {
		return stepPtr(p.reject(ctx, row.ID, outcomeTime, "the assertion predates the allowed age"))
	}
	notOnOrAfter, ok := assertionNotOnOrAfter(assertion)
	if !ok {
		return stepPtr(p.reject(ctx, row.ID, outcomeTime, "the assertion carries no usable NotOnOrAfter"))
	}
	if notOnOrAfter.Sub(assertion.IssueInstant) > maxAssertionLifetime {
		return stepPtr(p.reject(ctx, row.ID, outcomeTime, "the assertion's validity window is too long"))
	}
	return nil
}

func (p *product) extract(ctx context.Context, row authn.SSOProvider, cfg providerConfig, assertion *types.Assertion) (authn.Proof, string) {
	subject, ok := subjectOf(cfg, assertion)
	if !ok {
		return authn.Proof{}, "the assertion carries no usable subject"
	}
	email := mappedValue(cfg, assertion, "email")
	display := mappedValue(cfg, assertion, "display_name")
	preferredUsername := attributeValue(assertion, preferredUsernameAttribute)
	attrs := attributeSnapshot(cfg, assertion)

	proof := authn.Proof{
		External: &authn.External{
			Provider:          providerName + ":" + row.ID,
			Subject:           subject,
			Email:             email,
			EmailVerified:     p.emailVerified(ctx, row, email),
			DisplayName:       display,
			PreferredUsername: preferredUsername,
			Tenant:            row.Tenant,
			Attributes:        attrs,
		},
		Methods:  []authn.Method{authn.MethodSAML},
		Provider: row.ID,
	}
	if row.TrustIDPMFA && acrTrusted(cfg, assertion) {
		proof.IDPAsserted = true
	}
	return proof, ""
}

const providerName = "saml"

const preferredUsernameAttribute = "preferred_username"

func subjectOf(cfg providerConfig, assertion *types.Assertion) (string, bool) {
	if cfg.SubjectAttribute != "" {
		if v := attributeValue(assertion, cfg.SubjectAttribute); v != "" {
			return v, true
		}
		return "", false
	}
	if assertion.Subject == nil || assertion.Subject.NameID == nil {
		return "", false
	}
	switch assertion.Subject.NameID.Format {
	case "", saml2.NameIdFormatPersistent, saml2.NameIdFormatUnspecified:
	default:
		return "", false
	}
	value := strings.TrimSpace(assertion.Subject.NameID.Value)
	return value, value != ""
}

func mappedValue(cfg providerConfig, assertion *types.Assertion, target string) string {
	for _, name := range sortedClaims(cfg.Claims) {
		if cfg.Claims[name] == target {
			if v := attributeValue(assertion, name); v != "" {
				return v
			}
		}
	}
	return ""
}

func attributeSnapshot(cfg providerConfig, assertion *types.Assertion) map[string]string {
	out := make(map[string]string)
	for _, name := range sortedClaims(cfg.Claims) {
		target := cfg.Claims[name]
		if !strings.HasPrefix(target, "attr.") {
			continue
		}
		key := authn.ReservedPrefixIDP + strings.TrimPrefix(target, "attr.")
		if len(key) > authn.SessionIDPKeyMax {
			continue
		}
		value := attributeValue(assertion, name)
		if value == "" {
			continue
		}
		out[key] = model.TruncateUTF8(value, authn.SessionIDPValueMax)
		if len(out) >= authn.SessionIDPAttributesMax {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func sortedClaims(claims map[string]string) []string {
	names := make([]string, 0, len(claims))
	for name := range claims {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func acrTrusted(cfg providerConfig, assertion *types.Assertion) bool {
	if len(cfg.MFAACRValues) == 0 || assertion.AuthnStatement == nil || assertion.AuthnStatement.AuthnContext == nil {
		return false
	}
	ref := assertion.AuthnStatement.AuthnContext.AuthnContextClassRef
	if ref == nil {
		return false
	}
	for _, want := range cfg.MFAACRValues {
		if ref.Value == want {
			return true
		}
	}
	return false
}

func (p *product) emailVerified(ctx context.Context, row authn.SSOProvider, email string) bool {
	domain := emailDomain(email)
	if domain == "" {
		return false
	}
	providers, err := p.providers.Discover(ctx, domain)
	if err != nil {
		return false
	}
	for _, prov := range providers {
		if prov.Tenant == row.Tenant {
			return true
		}
	}
	return false
}

func emailDomain(email string) string {
	at := strings.LastIndex(email, "@")
	if at <= 0 || at == len(email)-1 {
		return ""
	}
	return strings.ToLower(email[at+1:])
}

func attributeValue(assertion *types.Assertion, name string) string {
	if assertion.AttributeStatement == nil {
		return ""
	}
	for _, attr := range assertion.AttributeStatement.Attributes {
		if attr.Name != name && attr.FriendlyName != name {
			continue
		}
		for _, value := range attr.Values {
			if value.Value != "" {
				return value.Value
			}
			if value.NameID != nil && value.NameID.Value != "" {
				return value.NameID.Value
			}
		}
	}
	return ""
}

func audienceContains(conditions *types.Conditions, audience string) bool {
	for _, ar := range conditions.AudienceRestrictions {
		for _, a := range ar.Audiences {
			if a.Value == audience {
				return true
			}
		}
	}
	return false
}

func assertionNotOnOrAfter(assertion *types.Assertion) (time.Time, bool) {
	var out time.Time
	ok := false
	add := func(text string) {
		if text == "" {
			return
		}
		t, err := time.Parse(time.RFC3339, text)
		if err != nil {
			return
		}
		if !ok || t.Before(out) {
			out = t
			ok = true
		}
	}
	if assertion.Conditions != nil {
		add(assertion.Conditions.NotOnOrAfter)
	}
	if scd := subjectConfirmationData(assertion); scd != nil {
		add(scd.NotOnOrAfter)
	}
	return out, ok
}

func subjectConfirmationData(assertion *types.Assertion) *types.SubjectConfirmationData {
	if assertion.Subject == nil || assertion.Subject.SubjectConfirmation == nil {
		return nil
	}
	return assertion.Subject.SubjectConfirmation.SubjectConfirmationData
}

func anyAssertionSigned(resp *types.Response) bool {
	for i := range resp.Assertions {
		if resp.Assertions[i].SignatureValidated {
			return true
		}
	}
	return false
}

func stepPtr(step authn.Step) *authn.Step { return &step }
