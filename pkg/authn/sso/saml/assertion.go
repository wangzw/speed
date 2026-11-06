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

// assertionLog is the register operation that makes an assertion's identifier
// single-use: it inserts the identifier on the replay line and reports a repeat
// as authn.ErrAssertionReplay. The register holds the table, so the write is the
// register's work and not this method's; the method reaches it through the
// enterprise provider register value it already requires. The register must
// implement this method, or the module refuses to build: without it an assertion
// could be replayed for as long as it is valid.
type assertionLog interface {
	RememberAssertion(ctx context.Context, idpID, assertionID string, notOnOrAfter time.Time) error
}

// idpInitiatedMaxAge is how long before now an identity-provider-initiated
// assertion's IssueInstant may be. It is shorter than the configured
// assertion-max-age because this path has no request to bind the assertion to.
const idpInitiatedMaxAge = 2 * time.Minute

// maxAssertionLifetime is the most an assertion's NotOnOrAfter may follow its
// IssueInstant, which bounds how long it can be replayed.
const maxAssertionLifetime = 10 * time.Minute

// consume runs the design's ordered checks over a decoded response, from the top
// and stopping at the first that fails. The order is the contract: a check that
// depends on an earlier one never runs before it. The provider and its built
// service provider are already resolved; the engine has already checked the flow
// binding, the method's name and the path.
func (p *product) consume(ctx context.Context, row authn.SSOProvider, b built, st methodState, encoded, relayState string) authn.Step {
	spInitiated := st.Request != ""

	// RelayState: an SP-initiated flow drew one and the IdP echoes it, so a
	// mismatch is refused. An IdP-initiated flow drew none.
	if spInitiated && relayState != st.Relay {
		return p.reject(ctx, row.ID, outcomeOther, "the RelayState does not match the flow")
	}

	// SHA-1 pre-check, before the library's signature validation, whose
	// validation context cannot refuse an algorithm on its own.
	if !row.AllowSHA1 && usesSHA1(encoded) {
		return p.reject(ctx, row.ID, outcomeSignature, "the response names a SHA-1 algorithm")
	}

	// A single parse and signature validation. The library decodes from the
	// element it verified, so the verified element and the used element are one.
	resp, err := b.sp.ValidateEncodedResponse(encoded)
	if err != nil {
		return p.reject(ctx, row.ID, outcomeSignature, "the response did not validate")
	}
	if !resp.SignatureValidated && !anyAssertionSigned(resp) {
		return p.reject(ctx, row.ID, outcomeSignature, "neither the response nor an assertion was signed")
	}

	// Destination must be present and be this service provider's ACS. The
	// library compares it only when it is present.
	if resp.Destination == "" || resp.Destination != b.sp.AssertionConsumerServiceURL {
		return p.reject(ctx, row.ID, outcomeDestination, "the destination is not this service provider's ACS")
	}

	if step := p.checkResponseTo(ctx, row, resp, st, spInitiated); step != nil {
		return *step
	}

	// Exactly one assertion, and no encrypted assertion left over: the library's
	// own retrieval uses the first assertion only, which a wrapping attack relies
	// on.
	if len(resp.Assertions) != 1 || len(resp.EncryptedAssertions) != 0 {
		return p.reject(ctx, row.ID, outcomeOther, "the response does not carry exactly one assertion")
	}
	assertion := resp.Assertions[0]

	if step := p.checkConditions(ctx, row, b, &assertion); step != nil {
		return *step
	}
	maxAge := p.settings.assertionMaxAge()
	if !spInitiated {
		// An identity-provider-initiated sign-in has no request to bind the
		// assertion to, so it must be correspondingly fresh.
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

// checkResponseTo checks the response's and every assertion's InResponseTo
// against the drawn request. An identity-provider-initiated response carries
// none, which only a provider that allows that path may answer, and only then.
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

// checkConditions runs the assertion's conditions: the times, the audience and
// the proxy restriction, which the library only reports and does not refuse.
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

// checkConfirmation checks the assertion's issuer, the response's status, the
// bearer confirmation, the recipient address and the two time bounds the design
// adds on top of the library: the assertion may not predate the configured age,
// and its validity window may not exceed the fixed lifetime.
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

// extract reads the subject and the mapped attributes out of a validated
// assertion and builds the proof. The cause is empty on success and is a short,
// value-free description otherwise.
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

// providerName is the namespace prefix an enterprise SAML subject is keyed in.
const providerName = "saml"

// preferredUsernameAttribute is the attribute name a SAML provider sends the
// standard preferred_username value under. The attribute's Name or FriendlyName
// may carry it; a provider that sends neither asserts no preferred username,
// and a just-in-time claim strategy that reads it refuses the login.
const preferredUsernameAttribute = "preferred_username"

// subjectOf takes the subject from the configured subject attribute, or from the
// assertion's NameID when its format can carry a stable identifier. A transient
// NameID and an email-format NameID are not stable subjects.
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

// mappedValue returns the first value of the attributes a target is mapped
// from. The claim names are walked in sorted order, so a mapping with more than
// one name for a target reads the same every time.
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

// attributeSnapshot builds the idp.* attribute set from the claim mapping: the
// target attr.<name> becomes the key idp.<name>, with the value cut at a
// character boundary to the session payload's per-entry bound.
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

// sortedClaims is the claim names in a stable order.
func sortedClaims(claims map[string]string) []string {
	names := make([]string, 0, len(claims))
	for name := range claims {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// acrTrusted reports whether the assertion's AuthnContextClassRef is one the
// provider's trust_idp_mfa settings count as multi-factor.
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

// emailVerified reports whether the address's domain is one this provider has
// verified: the meaning of "verified" a SAML assertion does not carry itself. A
// discovery failure and an address that is not there both read as not verified,
// which leaves the account resolution to reject a mismatched domain on its own.
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

// emailDomain is the part of an address after the last @, lower-cased. An
// address with no @, or with nothing around it, has no domain.
func emailDomain(email string) string {
	at := strings.LastIndex(email, "@")
	if at <= 0 || at == len(email)-1 {
		return ""
	}
	return strings.ToLower(email[at+1:])
}

// attributeValue returns the first value of the attribute with a name, falling
// back to the friendly name.
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

// audienceContains reports whether any audience restriction names the audience.
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

// assertionNotOnOrAfter is the earlier of the assertion's and its bearer
// confirmation's NotOnOrAfter, the instant the assertion can no longer be used.
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

// subjectConfirmationData is an assertion's bearer confirmation data, or nil.
func subjectConfirmationData(assertion *types.Assertion) *types.SubjectConfirmationData {
	if assertion.Subject == nil || assertion.Subject.SubjectConfirmation == nil {
		return nil
	}
	return assertion.Subject.SubjectConfirmation.SubjectConfirmationData
}

// anyAssertionSigned reports whether any assertion carried a validated
// signature, which is what the library sets when the envelope was unsigned but
// an assertion was signed.
func anyAssertionSigned(resp *types.Response) bool {
	for i := range resp.Assertions {
		if resp.Assertions[i].SignatureValidated {
			return true
		}
	}
	return false
}

// stepPtr is a convenience for the check helpers, which report a finished answer
// or nil.
func stepPtr(step authn.Step) *authn.Step { return &step }
