// Package humanverify is the human-verification gate of the authentication
// capability: the object a send asks, before it spends a budget, whether the
// request came from a person.
//
// A deployment chooses the mechanism with this module's mode: off, a self-hosted
// proof of work, or Cloudflare Turnstile. The gate is off by default, and the
// seam is delivered exclusively, so a command names this module or supplies its
// own authn.HumanVerifier; two exclusive providers in one run fail the startup.
//
// The module holds no send state and sends nothing. A send checks the seam
// before it spends anything, and the token's single-use property belongs to the
// provider behind the seam. The off mode accepts every proof, and the
// proof-of-work mode issues a challenge on POST /api/v1/auth/human/challenge and
// spends it when the proof is accepted. The turnstile provider is constructed and
// refuses a missing proof, and answers a presented proof with ErrUnavailable,
// because its proof check is not part of this build.
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
	"github.com/vislake/speed/pkg/kv"
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
// The key-value store, the settings store and the route mounter are required as
// optional only: the off gate needs none of them, so an assembly that leaves the
// gate off starts without them. The proof-of-work mode needs the store its
// challenges live in and a mounter to serve the challenge route, and New or Init
// names the missing one when the mode asks for it.
//
// Prepare states StateEnabled in every mode, including the default off: a mode
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
			taken, err := dependencies(cfg, reg)
			if err != nil {
				return nil, err
			}
			return newProvider(cfg, taken)
		},

		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			provider, ok := instance.(*pow.Provider)
			if !ok {
				return nil
			}
			mounter, err := core.Resolve[mount.Mounter](reg)
			if err != nil {
				return fmt.Errorf("%w: %s.%s is %q, and the challenge route is mounted through the "+
					"authentication capability's route mounter, which this run did not deliver. Name the "+
					"route mounter on the command, or run the gate off: %w",
					authn.ErrInvalidConfig, ConfigNamespace, modeItem, ModePoW, err)
			}
			mountChallenge(mounter, provider)
			return nil
		},
	}
}

// dependencies takes up what the resolved mode needs. The off and turnstile
// gates need nothing, so a run that carries neither the store nor the mounter
// still starts with the gate off.
func dependencies(cfg resolved, reg *core.Registry) (deps, error) {
	if cfg.Mode != ModePoW {
		return deps{}, nil
	}
	store, err := core.Resolve[kv.KV](reg)
	if err != nil {
		return deps{}, fmt.Errorf("%w: %s.%s is %q, and the proof-of-work mechanism keeps its "+
			"challenges in the key-value store, which this run did not deliver. Name a key-value "+
			"backend on the command, or run the gate off: %w",
			authn.ErrInvalidConfig, ConfigNamespace, modeItem, cfg.Mode, err)
	}
	taken := deps{store: store}
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
