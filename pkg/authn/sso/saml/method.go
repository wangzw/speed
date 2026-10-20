package saml

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	saml2 "github.com/russellhaering/gosaml2"

	"github.com/vislake/speed/pkg/authn"
)

// stepAssertion is this method's name for its one step: the flow is at the
// redirect to the identity provider, waiting for the assertion's POST back. The
// engine keeps the name and hands it back in FlowState.Step, so a callback that
// arrives at a step this method did not name is refused.
const stepAssertion = "assertion"

// relayStateBytes is the length of the random RelayState the method draws.
const relayStateBytes = 16

// The causes the method puts on an answer. None carries a value the client sent.
var (
	errNotCallback    = errors.New("authn.sso.saml: the answer is not a redirect back")
	errNoProvider     = errors.New("authn.sso.saml: the start names no provider")
	errProviderUsable = errors.New("authn.sso.saml: the provider does not answer this method")
)

// Name is the method's stable name.
func (p *product) Name() string { return methodName }

// Describe reports the enterprise SAML method: an enterprise entry that
// discovers the provider and leaves for it. It opens no account of its own; a
// provider's trust settings and the account resolution decide that.
func (p *product) Describe() authn.MethodDescriptor {
	return authn.MethodDescriptor{
		Name:             methodName,
		Kind:             authn.MethodKindEnterprise,
		Protocol:         authn.MethodProtocolSAML,
		BindingProviders: []string{providerName + ":"},
	}
}

// Begin starts a flow: it reads the provider row the start names, builds an
// AuthnRequest for it, keeps the request's identifier, the RelayState and the
// provider's identifier in the flow's private state, and answers the redirect
// the user agent leaves for. A provider that does not exist, is disabled or is
// not a SAML provider is refused, before anything is drawn.
func (p *product) Begin(ctx context.Context, s authn.Start) (authn.Step, error) {
	if !servesPurpose(s.Purpose) {
		return refusePurpose(s.Purpose), nil
	}
	id := s.Fields["provider"]
	if id == "" {
		return authn.StepRefused(errNoProvider), nil
	}
	row, err := p.providers.Get(ctx, id)
	switch {
	case err == nil:
	case errors.Is(err, authn.ErrProviderNotFound):
		return authn.StepRefused(errProviderUsable), nil
	default:
		return stepUnavailable("reading the provider", err), nil
	}
	if !row.Enabled || row.Protocol != providerProtocol {
		return authn.StepRefused(errProviderUsable), nil
	}
	b, err := p.serviceProvider(ctx, row)
	if err != nil {
		return stepUnavailable("building the service provider", err), nil
	}
	relay, err := randomRelay()
	if err != nil {
		return stepUnavailable("drawing the RelayState", err), nil
	}
	doc, err := b.sp.BuildAuthRequestDocument()
	if err != nil {
		return stepUnavailable("building the AuthnRequest", err), nil
	}
	requestID := doc.Root().SelectAttrValue(saml2.IdAttr, "")
	if requestID == "" {
		return stepUnavailable("reading the request identifier", errors.New("the AuthnRequest carries no ID")), nil
	}
	url, err := b.sp.BuildAuthURLFromDocument(relay, doc)
	if err != nil {
		return stepUnavailable("building the redirect URL", err), nil
	}
	return authn.StepRedirect(stepAssertion, writeState(requestID, relay, row.ID), url), nil
}

// Answer handles the assertion's POST back. The engine has already checked the
// flow's binding, the method's name and the path, and moved the record to
// processing, so the assertion is consumed at most once by the engine's own
// compare-and-swap; the checks here are the design's ordered set on top of that.
func (p *product) Answer(ctx context.Context, f authn.FlowState, in authn.StepInput) (authn.Step, error) {
	if in.Action != authn.InputCallback {
		return authn.StepRefused(errNotCallback), nil
	}
	if !servesPurpose(f.Purpose) {
		return refusePurpose(f.Purpose), nil
	}
	st, ok := readState(f.Private)
	if !ok {
		// An identity-provider-initiated sign-in: the engine created the flow on
		// the spot and names the provider to the method in the flow state, with no
		// drawn request or RelayState to check. A flow that names no provider at
		// all is refused rather than validated without a provider to check it
		// against.
		if f.Provider == "" {
			return p.reject(ctx, "", outcomeOther, "the flow names no provider"), nil
		}
		st = methodState{Version: stateVersion, Provider: f.Provider}
	}
	row, err := p.providers.Get(ctx, st.Provider)
	switch {
	case err == nil:
	case errors.Is(err, authn.ErrProviderNotFound):
		return p.reject(ctx, st.Provider, outcomeOther, "the provider does not exist"), nil
	default:
		return stepUnavailable("reading the provider", err), nil
	}
	if !row.Enabled || row.Protocol != providerProtocol {
		return p.reject(ctx, st.Provider, outcomeOther, "the provider does not answer this method"), nil
	}
	encoded := in.Form.Get("SAMLResponse")
	if encoded == "" {
		return p.reject(ctx, row.ID, outcomeOther, "the callback carries no assertion"), nil
	}
	b, err := p.serviceProvider(ctx, row)
	if err != nil {
		return p.reject(ctx, row.ID, outcomeOther, "the provider's configuration cannot be read"), nil
	}
	return p.consume(ctx, row, b, st, encoded, in.Form.Get("RelayState")), nil
}

// servePurpose reports whether the method answers a flow of this purpose. A
// SAML identity proves which external identity a caller holds: a login uses it
// to resolve an account, a link binds it to the flow's account, and a step-up
// re-proves an account whose first factor is this one. An enrollment registers
// a factor, which an external identity does not prove.
func servesPurpose(purpose string) bool {
	switch purpose {
	case authn.PurposeLogin, authn.PurposeLink, authn.PurposeStepUp:
		return true
	default:
		return false
	}
}

// refusePurpose refuses a flow of a purpose the method does not serve, before it
// reads or draws anything.
func refusePurpose(purpose string) authn.Step {
	return authn.StepRefused(fmt.Errorf("%s: purpose %q is not served", ModuleName, purpose))
}

// randomRelay draws the RelayState: 16 random bytes, in hexadecimal.
func randomRelay() (string, error) {
	var raw [relayStateBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

// serviceProvider returns the built service provider for a row, from the cache
// or freshly built.
func (p *product) serviceProvider(ctx context.Context, row authn.SSOProvider) (built, error) {
	ttl := p.settings.providerCacheTTL()
	now := p.clock.Now()
	return p.cache.get(row, ttl, now, func() (built, error) {
		return p.buildServiceProvider(row)
	})
}
