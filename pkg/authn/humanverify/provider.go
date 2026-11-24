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

type deps struct {
	store    kv.KV
	settings pow.SettingReader
}

type gate struct {
	other authn.HumanVerifier
	phone authn.HumanVerifier

	pow     *pow.Provider
	captcha *captcha.Provider
}

var _ authn.HumanVerifier = (*gate)(nil)

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

func (g *gate) Required(scope authn.HumanScope) bool { return g.forScope(scope).Required(scope) }

func (g *gate) Provider(scope authn.HumanScope) string { return g.forScope(scope).Provider(scope) }

func (g *gate) SiteKey(scope authn.HumanScope) string { return g.forScope(scope).SiteKey(scope) }

func (g *gate) Action(scope authn.HumanScope) string { return g.forScope(scope).Action(scope) }

func (g *gate) Verify(ctx context.Context, proof authn.HumanProof, scope authn.HumanScope) error {
	return g.forScope(scope).Verify(ctx, proof, scope)
}

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

type offVerifier struct{}

var _ authn.HumanVerifier = offVerifier{}

func (offVerifier) Required(authn.HumanScope) bool { return false }

func (offVerifier) Provider(authn.HumanScope) string { return ModeOff }

func (offVerifier) SiteKey(authn.HumanScope) string { return "" }

func (offVerifier) Action(authn.HumanScope) string { return "" }

func (offVerifier) Verify(context.Context, authn.HumanProof, authn.HumanScope) error { return nil }
