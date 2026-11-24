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

const stepAssertion = "assertion"

const relayStateBytes = 16

var (
	errNotCallback    = errors.New("authn.sso.saml: the answer is not a redirect back")
	errNoProvider     = errors.New("authn.sso.saml: the start names no provider")
	errProviderUsable = errors.New("authn.sso.saml: the provider does not answer this method")
)

func (p *product) Name() string { return methodName }

// Describe reports the enterprise SAML method as an entry that discovers the provider and leaves for it, opening no account of its own.
func (p *product) Describe() authn.MethodDescriptor {
	return authn.MethodDescriptor{
		Name:             methodName,
		Kind:             authn.MethodKindEnterprise,
		Protocol:         authn.MethodProtocolSAML,
		BindingProviders: []string{providerName + ":"},
	}
}

// Begin reads the named provider, keeps the AuthnRequest identifier, RelayState and provider identifier in the flow's private state, and answers the redirect. A missing, disabled or non-SAML provider is refused.
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

// Answer handles the assertion's POST back and runs the ordered checks on top of the engine's one-time consumption of the flow.
func (p *product) Answer(ctx context.Context, f authn.FlowState, in authn.StepInput) (authn.Step, error) {
	if in.Action != authn.InputCallback {
		return authn.StepRefused(errNotCallback), nil
	}
	if !servesPurpose(f.Purpose) {
		return refusePurpose(f.Purpose), nil
	}
	st, ok := readState(f.Private)
	if !ok {
		// An IdP-initiated flow names its provider in the flow state and has no drawn request or RelayState to check; a flow naming none is refused.
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

func servesPurpose(purpose string) bool {
	switch purpose {
	case authn.PurposeLogin, authn.PurposeLink, authn.PurposeStepUp:
		return true
	default:
		return false
	}
}

func refusePurpose(purpose string) authn.Step {
	return authn.StepRefused(fmt.Errorf("%s: purpose %q is not served", ModuleName, purpose))
}

func randomRelay() (string, error) {
	var raw [relayStateBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func (p *product) serviceProvider(ctx context.Context, row authn.SSOProvider) (built, error) {
	ttl := p.settings.providerCacheTTL()
	now := p.clock.Now()
	return p.cache.get(row, ttl, now, func() (built, error) {
		return p.buildServiceProvider(row)
	})
}
