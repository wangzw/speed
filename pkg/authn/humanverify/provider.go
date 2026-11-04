package humanverify

import (
	"context"
	"fmt"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/humanverify/captcha"
	"github.com/vislake/speed/pkg/authn/humanverify/pow"
	"github.com/vislake/speed/pkg/authn/humanverify/turnstile"
	"github.com/vislake/speed/pkg/kv"
)

// deps is what the mechanism constructors need. The off gate needs nothing; the
// self-hosted mechanisms need the store their challenges live in, Turnstile
// needs the store it records spent tokens in, and the self-hosted mechanisms
// read their parameters from settings when a store answers. A mode that does
// not use a part leaves it nil.
type deps struct {
	store    kv.KV
	settings pow.SettingReader
}

// gate is the verifier a deployment's two surfaces add up to. Each surface runs
// its own mechanism, and every answer is the answer of the surface the caller
// names. The self-hosted mechanisms are held here too, because their challenge
// routes are mounted once for the run rather than once per surface.
type gate struct {
	other authn.HumanVerifier
	phone authn.HumanVerifier

	pow     *pow.Provider
	captcha *captcha.Provider
}

var _ authn.HumanVerifier = (*gate)(nil)

// forScope is the verifier of one surface. An unknown scope answers as the
// phone surface when that surface is gated, otherwise as the gated other
// surface; it resolves to off only when both surfaces are off.
func (g *gate) forScope(scope authn.HumanScope) authn.HumanVerifier {
	switch scope {
	case authn.HumanScopeOther:
		if g.other != nil {
			return g.other
		}
		return offVerifier{}
	case authn.HumanScopePhone:
		if g.phone != nil {
			return g.phone
		}
		return offVerifier{}
	default:
		if g.phone != nil {
			return g.phone
		}
		if g.other != nil {
			return g.other
		}
		return offVerifier{}
	}
}

// Required reports the surface's answer.
func (g *gate) Required(scope authn.HumanScope) bool { return g.forScope(scope).Required(scope) }

// Provider names the surface's mechanism.
func (g *gate) Provider(scope authn.HumanScope) string { return g.forScope(scope).Provider(scope) }

// SiteKey reports the surface's public widget key.
func (g *gate) SiteKey(scope authn.HumanScope) string { return g.forScope(scope).SiteKey(scope) }

// Action reports the action the surface's widget must be rendered with.
func (g *gate) Action(scope authn.HumanScope) string { return g.forScope(scope).Action(scope) }

// Verify checks a proof against the surface's mechanism.
func (g *gate) Verify(ctx context.Context, proof authn.HumanProof, scope authn.HumanScope) error {
	return g.forScope(scope).Verify(ctx, proof, scope)
}

// newProvider builds the verifier for the resolved configuration. Each surface's
// mode was held to the closed set by resolve; the default case repeats that
// judgement for a caller that built resolved by hand. The self-hosted
// mechanisms are built once and shared, so a deployment that runs the same one
// on both surfaces serves one challenge route.
func newProvider(cfg resolved, d deps) (*gate, error) {
	var (
		powProvider     *pow.Provider
		captchaProvider *captcha.Provider
	)
	build := func(s surface) (authn.HumanVerifier, error) {
		switch s.mode {
		case ModeOff:
			return nil, nil
		case ModePoW:
			if powProvider == nil {
				provider, err := pow.New(d.store, d.settings)
				if err != nil {
					return nil, fmt.Errorf("%w: %s: %w", authn.ErrInvalidConfig, ModuleName, err)
				}
				powProvider = provider
			}
			return powProvider, nil
		case ModeCaptcha:
			if captchaProvider == nil {
				provider, err := captcha.New(d.store, d.settings)
				if err != nil {
					return nil, fmt.Errorf("%w: %s: %w", authn.ErrInvalidConfig, ModuleName, err)
				}
				captchaProvider = provider
			}
			return captchaProvider, nil
		case ModeTurnstile:
			provider, err := turnstile.New(turnstile.Options{
				SiteKey:   s.siteKey,
				Secret:    s.secretKey,
				VerifyURL: s.verifyURL,
				Hostname:  s.hostname,
				Action:    s.action,
				Timeout:   s.timeout,
				Store:     d.store,
			})
			if err != nil {
				return nil, fmt.Errorf("%w: %s: %w", authn.ErrInvalidConfig, ModuleName, err)
			}
			return provider, nil
		default:
			return nil, fmt.Errorf("%w: %s.%s or %s.%s is %q, and it has to be %q, %q, %q or %q",
				authn.ErrInvalidConfig, ConfigNamespace, modeItem, ConfigNamespace, phoneModeItem,
				s.mode, ModeOff, ModePoW, ModeCaptcha, ModeTurnstile)
		}
	}

	other, err := build(cfg.other)
	if err != nil {
		return nil, err
	}
	phone, err := build(cfg.phone)
	if err != nil {
		return nil, err
	}
	return &gate{other: other, phone: phone, pow: powProvider, captcha: captchaProvider}, nil
}

// offVerifier is the gate a surface leaves off. It requires nothing and accepts
// every proof, so a caller never has to ask whether it should present one: the
// answer is the same either way.
type offVerifier struct{}

var _ authn.HumanVerifier = offVerifier{}

// Required reports false for every scope: an off gate is not a condition of
// sending.
func (offVerifier) Required(authn.HumanScope) bool { return false }

// Provider reports the mode the deployment chose.
func (offVerifier) Provider(authn.HumanScope) string { return ModeOff }

// SiteKey is empty: an off gate has no widget to key.
func (offVerifier) SiteKey(authn.HumanScope) string { return "" }

// Action is empty: an off gate has no widget to render.
func (offVerifier) Action(authn.HumanScope) string { return "" }

// Verify accepts any proof. The check is not skipped as a failure; off means
// there is nothing to check.
func (offVerifier) Verify(context.Context, authn.HumanProof, authn.HumanScope) error { return nil }
