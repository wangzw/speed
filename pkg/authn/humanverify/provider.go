package humanverify

import (
	"context"
	"fmt"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/humanverify/pow"
	"github.com/vislake/speed/pkg/authn/humanverify/turnstile"
	"github.com/vislake/speed/pkg/kv"
)

// deps is what a mechanism constructor needs. The off gate needs nothing; the
// proof-of-work provider needs the store its challenges live in and the settings
// reader its parameters come from. A mode that does not use a part leaves it
// nil.
type deps struct {
	store    kv.KV
	settings pow.SettingReader
}

// newProvider builds the verifier for a resolved mode. The mode was held to the
// closed set by resolve, and the default case repeats that judgement for a
// caller that built resolved by hand.
func newProvider(cfg resolved, d deps) (authn.HumanVerifier, error) {
	switch cfg.Mode {
	case ModeOff:
		return offVerifier{}, nil
	case ModePoW:
		if d.store == nil {
			return nil, fmt.Errorf("%w: %s.%s is %q, and the proof-of-work mechanism keeps its "+
				"challenges in the key-value store, which this run did not deliver. Name a key-value "+
				"backend on the command, or run the gate off",
				authn.ErrInvalidConfig, ConfigNamespace, modeItem, cfg.Mode)
		}
		return pow.New(d.store, d.settings)
	case ModeTurnstile:
		provider, err := turnstile.New(turnstile.Options{
			SiteKey:   cfg.siteKey,
			Secret:    cfg.secretKey,
			VerifyURL: cfg.verifyURL,
			Hostname:  cfg.hostname,
			Action:    cfg.action,
			Timeout:   cfg.timeout,
		})
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %w", authn.ErrInvalidConfig, ModuleName, err)
		}
		return provider, nil
	default:
		return nil, fmt.Errorf("%w: %s.%s is %q, and it has to be %q, %q or %q",
			authn.ErrInvalidConfig, ConfigNamespace, modeItem, cfg.Mode, ModeOff, ModePoW, ModeTurnstile)
	}
}

// offVerifier is the gate a deployment leaves off. It requires nothing and
// accepts every proof, so a caller never has to ask whether it should present
// one: the answer is the same either way.
type offVerifier struct{}

var _ authn.HumanVerifier = offVerifier{}

// Required reports false: an off gate is not a condition of sending.
func (offVerifier) Required() bool { return false }

// Provider reports the mode the deployment chose.
func (offVerifier) Provider() string { return ModeOff }

// SiteKey is empty: an off gate has no widget to key.
func (offVerifier) SiteKey() string { return "" }

// Verify accepts any proof. The check is not skipped as a failure; off means
// there is nothing to check.
func (offVerifier) Verify(context.Context, authn.HumanProof) error { return nil }
