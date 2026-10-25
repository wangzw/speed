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
// provider behind the seam. Only the off mode is complete in this build: the pow
// and turnstile providers are constructed and refuse a missing proof, and answer
// a presented proof with ErrUnavailable, because their proof checks are not part
// of this build. A deployment that enables one of them therefore sends nothing
// rather than accepting a token nobody checked.
package humanverify

import (
	"context"
	"fmt"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

// ModuleName is this module's name in the registry, and the name its
// configuration namespace takes.
const ModuleName = "authn.humanverify"

// ConfigNamespace is where this module's input items are mounted.
const ConfigNamespace = ModuleName

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
// Prepare states StateEnabled in every mode, including the default off: a mode
// the configuration refused stops the startup there, and a command that takes
// this module in gets a verifier that states whether it requires a proof. The
// mode alone decides the answer, so the module has no stand-down of its own.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Provides: []core.Provision{
			{Token: (*authn.HumanVerifier)(nil), Exclusive: true},
		},
		Resources: []any{schema()},

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
			return newProvider(cfg)
		},
	}
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
