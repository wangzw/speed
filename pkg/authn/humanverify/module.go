// Package humanverify is the human-verification gate of the authentication
// capability: the object a send asks, before it spends a budget, whether the
// request came from a person.
//
// A deployment chooses one mechanism per surface with this module's mode and
// phone-mode: off, a self-hosted proof of work, a self-hosted captcha, or
// Cloudflare Turnstile. The other surface runs the proof of work and the phone
// surface runs the captcha by default, and the seam is delivered exclusively, so
// a command names this module or supplies its own authn.HumanVerifier; two
// exclusive providers in one run fail the startup.
//
// The module holds no send state and sends nothing. A send checks the seam
// before it spends anything, and the token's single-use property belongs to the
// provider behind the seam. The off mechanism accepts every proof. The
// proof-of-work mechanism issues a challenge on POST
// /api/v1/auth/human/challenge and spends it when the proof is accepted. The
// captcha mechanism issues a challenge on POST
// /api/v1/auth/human/captcha/challenge and serves its image, background, piece
// and audio on the routes under that path. The turnstile mechanism posts the
// proof to Cloudflare's siteverify endpoint and accepts it once the answer names
// the deployment's hostname and, when configured, its action.
package humanverify

import (
	"context"
	_ "embed"
	"errors"
	"fmt"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/humanverify/pow"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/internal/literal"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
)

// ModuleName is this module's name in the registry, and the name its
// configuration namespace takes.
const ModuleName = "authn.humanverify"

// ConfigNamespace is where this module's input items are mounted.
const ConfigNamespace = ModuleName

// defaultEndpoint is the endpoint this module's own OpenAPI fragment declares
// its route for. The endpoint the route is actually mounted on is the one the
// mounter resolves; the fragment is a static declaration written before that is
// known, so it carries the conventional name.
const defaultEndpoint = "account"

// humanverifyFragment is this module's OpenAPI fragment, describing the
// challenge route.
//
//go:embed openapi/humanverify.json
var humanverifyFragment []byte

// humanverifySpec is the API root this module's fragment and route hang off.
// Its prefix and version are the common ones; the fragment writes the
// capability's own segment, so the route's declared path begins with the part of
// the mounter's base that lies below this root.
var humanverifySpec = speedhttp.Spec{
	Endpoint: defaultEndpoint,
	Prefix:   "api",
	Version:  "v1",
	Document: humanverifyFragment,
}

// init registers this module with the process registry, so a host that imports
// the package has the gate available to name on a command tree.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// The human-verification capability is delivered exclusively: two gates in one
// run could require different proofs, and a send would be answered differently
// depending on which object it reached. A deployment that wants a mechanism this
// module does not bundle supplies its own authn.HumanVerifier and leaves this
// module out.
//
// The key-value store, the settings store, the route mounter and logging are
// required as optional only: the off gate needs none of them, so an assembly
// that leaves the gate off starts without them. The proof-of-work mode needs the
// store its challenges live in and a mounter to serve the challenge route, and
// New or Init names the missing one when the mode asks for it. The turnstile
// mode registers its secret as a literal redaction rule when a logging
// capability is present.
//
// Prepare states StateEnabled in every mode, including the default proof of
// work: a mode
// the configuration refused stops the startup there, and a command that takes
// this module in gets a verifier that states whether it requires a proof. The
// mode alone decides the answer, so the module has no stand-down of its own.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*kv.KV)(nil), Optional: true},
			{Token: (*settings.Store)(nil), Optional: true},
			{Token: (*mount.Mounter)(nil), Optional: true},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Provides: []core.Provision{
			{Token: (*authn.HumanVerifier)(nil), Exclusive: true},
		},
		Resources: []any{schema(), pow.Schema(), humanverifySpec},

		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			reader, err := readerOf(reg)
			if err != nil {
				return core.Enablement{}, err
			}
			if _, err := readConfig(reader); err != nil {
				return core.Enablement{}, err
			}
			return core.Enablement{State: core.StateEnabled}, nil
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			reader, err := readerOf(reg)
			if err != nil {
				return nil, err
			}
			cfg, err := readConfig(reader)
			if err != nil {
				return nil, err
			}
			if cfg.usesTurnstile() {
				secret := cfg.other.secretKey
				if cfg.phone.mode == ModeTurnstile {
					secret = cfg.phone.secretKey
				}
				if registerErr := registerSecret(reg, secret); registerErr != nil {
					return nil, registerErr
				}
			}
			taken, err := dependencies(cfg, reg)
			if err != nil {
				return nil, err
			}
			return newProvider(cfg, taken)
		},

		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			gate, ok := instance.(*gate)
			if !ok || (gate.pow == nil && gate.captcha == nil) {
				return nil
			}
			mounter, err := core.Resolve[mount.Mounter](reg)
			if err != nil {
				return fmt.Errorf("%w: the mechanisms named by %s.%s and %s.%s serve a challenge route "+
					"through the authentication capability's route mounter, which this run did not "+
					"deliver. Name the route mounter on the command, or run the gate off: %w",
					authn.ErrInvalidConfig, ConfigNamespace, modeItem, ConfigNamespace, phoneModeItem, err)
			}
			if gate.pow != nil {
				mountChallenge(mounter, gate.pow)
			}
			if gate.captcha != nil {
				mountCaptcha(mounter, gate.captcha)
			}
			return nil
		},
	}
}

// dependencies takes up what the resolved modes need. The off and turnstile
// gates need nothing, so a run that carries neither the store nor the mounter
// still starts with both surfaces off. The settings store is taken up only when
// a surface runs the proof of work, which is the one mechanism that reads it.
func dependencies(cfg resolved, reg *core.Registry) (deps, error) {
	var taken deps
	if cfg.needsStore() {
		store, err := core.Resolve[kv.KV](reg)
		if err != nil {
			return deps{}, fmt.Errorf("%w: the mechanisms named by %s.%s and %s.%s keep their "+
				"challenges in the key-value store, which this run did not deliver. Name a key-value "+
				"backend on the command, or run those surfaces without a self-hosted mechanism: %w",
				authn.ErrInvalidConfig, ConfigNamespace, modeItem, ConfigNamespace, phoneModeItem, err)
		}
		taken.store = store
	}
	if !cfg.usesPoW() {
		return taken, nil
	}
	settingsStore, err := core.Resolve[settings.Store](reg)
	switch {
	case err == nil:
		taken.settings = settingsStore
	case errors.Is(err, core.ErrMissingProvider):
		// The provider falls back to the declared defaults.
	default:
		return deps{}, fmt.Errorf("%s: taking up the settings store: %w", ModuleName, err)
	}
	return taken, nil
}

// registerSecret registers the Turnstile secret as a literal redaction rule, so
// it is masked wherever it turns up in a log line. A secret has no shape to
// match, so the only thing known about it is the value. The registration runs in
// New, before any logger binds an attribute, which is the timing a redaction
// rule needs. With no logging capability there is no log to protect, and the
// absence is told apart from a failed lookup by the sentinel.
func registerSecret(reg *core.Registry, secret string) error {
	delivered, err := core.Resolve[log.Logger](reg)
	switch {
	case errors.Is(err, core.ErrMissingProvider):
		return nil
	case err != nil:
		return fmt.Errorf("%s: taking up the logging capability to register the Turnstile secret "+
			"with: %w", ModuleName, err)
	}
	literal.Register(delivered.Redaction(), ModuleName+"."+secretKeyItem, secret)
	return nil
}

// readerOf takes up the configuration reader. Its absence is the host's omission
// and is carried out as it stands: the mode has a default, but a run with no
// reader never had a chance to state one.
func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: the mode is read from configuration, and no reader was handed "+
			"to the registry before the assembly began: %w", ModuleName, err)
	}
	return reader, nil
}
