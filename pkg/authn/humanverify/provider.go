package humanverify

import (
	"context"
	"errors"
	"fmt"

	"github.com/vislake/speed/pkg/authn"
)

// errNotImplemented is the cause of a presented proof the build cannot check.
// The seam's failure direction is closed: a verifier that has no check answers
// as an outage instead of accepting a token nobody examined.
var errNotImplemented = errors.New("the proof check is not part of this build")

// newProvider builds the verifier for a resolved mode. The mode was held to the
// closed set by resolve, and the default case repeats that judgement for a
// caller that built resolved by hand.
func newProvider(cfg resolved) (authn.HumanVerifier, error) {
	switch cfg.Mode {
	case ModeOff:
		return offVerifier{}, nil
	case ModePoW:
		return powVerifier{}, nil
	case ModeTurnstile:
		return turnstileVerifier{}, nil
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

// powVerifier is the self-hosted proof-of-work gate. It requires a proof and
// has no site key: the challenge is served by this deployment and no
// third-party widget obtains the token.
//
// The proof check is not part of this build. A missing proof is still refused as
// such, because that half of the contract does not depend on the mechanism; a
// presented one is answered as an outage.
type powVerifier struct{}

var _ authn.HumanVerifier = powVerifier{}

// Required reports true: this gate is a condition of sending.
func (powVerifier) Required() bool { return true }

// Provider reports the mode the deployment chose.
func (powVerifier) Provider() string { return ModePoW }

// SiteKey is empty: a self-hosted proof of work needs no widget key.
func (powVerifier) SiteKey() string { return "" }

// Verify refuses a missing proof and answers a presented one as unavailable,
// because the check that would accept it is not in this build.
func (powVerifier) Verify(_ context.Context, p authn.HumanProof) error {
	return verifyPresented(ModePoW, p)
}

// turnstileVerifier is the Cloudflare Turnstile gate. It requires a proof. This
// build carries no item for the client-side widget key, so SiteKey is empty.
//
// The proof check is not part of this build. A missing proof is refused as such;
// a presented one is answered as an outage.
type turnstileVerifier struct{}

var _ authn.HumanVerifier = turnstileVerifier{}

// Required reports true: this gate is a condition of sending.
func (turnstileVerifier) Required() bool { return true }

// Provider reports the mode the deployment chose.
func (turnstileVerifier) Provider() string { return ModeTurnstile }

// SiteKey is empty in this build: no configuration item carries the widget key.
func (turnstileVerifier) SiteKey() string { return "" }

// Verify refuses a missing proof and answers a presented one as unavailable,
// because the check that would accept it is not in this build.
func (turnstileVerifier) Verify(_ context.Context, p authn.HumanProof) error {
	return verifyPresented(ModeTurnstile, p)
}

// verifyPresented holds the half of the contract that does not depend on a
// mechanism: an empty token is a caller that presented nothing, which is the
// 403 refusal. Everything after that needs the provider's check, which this
// build does not have, so it fails closed with the outage answer.
func verifyPresented(mode string, p authn.HumanProof) error {
	if p.Token == "" {
		return fmt.Errorf("%w: the %s verifier was given no proof", authn.ErrHumanVerificationRequired, mode)
	}
	return fmt.Errorf("%w: %s: %w", authn.ErrUnavailable, mode, errNotImplemented)
}
