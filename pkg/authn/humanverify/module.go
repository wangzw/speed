// Package humanverify is the human-verification gate: the seam a send asks before spending a budget. A deployment selects off, proof of work, captcha or Turnstile per surface, and the capability is delivered exclusively.
package humanverify

import (
	"context"
	_ "embed"
	"errors"
	"fmt"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/humanverify/captcha"
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

const defaultEndpoint = "account"

// humanverifyFragment is this module's OpenAPI fragment.
//
//go:embed openapi/humanverify.json
var humanverifyFragment []byte

var humanverifySpec = speedhttp.Spec{
	Endpoint: defaultEndpoint,
	Prefix:   "api",
	Version:  "v1",
	Document: humanverifyFragment,
}

func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers. It delivers authn.HumanVerifier exclusively; the key-value store, settings store, mounter and logger are optional because the off gate needs none, and a mode that needs one names it when absent. Prepare states enabled in every mode.
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
		Resources: []any{schema(), pow.Schema(), captcha.Schema(), humanverifySpec},

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

func dependencies(cfg resolved, reg *core.Registry) (deps, error) {
	var taken deps
	if cfg.needsStore() {
		store, err := core.Resolve[kv.KV](reg)
		if err != nil {
			return deps{}, fmt.Errorf("%w: a mechanism named by %s.%s and %s.%s needs the "+
				"key-value store, which this run did not deliver: the self-hosted mechanisms keep "+
				"their challenges there and Turnstile records the digest of every token it accepts "+
				"there so a replay is refused. Name a key-value backend on the command, or leave "+
				"those surfaces off: %w",
				authn.ErrInvalidConfig, ConfigNamespace, modeItem, ConfigNamespace, phoneModeItem, err)
		}
		taken.store = store
	}
	if !cfg.usesSettings() {
		return taken, nil
	}
	settingsStore, err := core.Resolve[settings.Store](reg)
	switch {
	case err == nil:
		taken.settings = settingsStore
	case errors.Is(err, core.ErrMissingProvider):
	default:
		return deps{}, fmt.Errorf("%s: taking up the settings store: %w", ModuleName, err)
	}
	return taken, nil
}

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

func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: the mode is read from configuration, and no reader was handed "+
			"to the registry before the assembly began: %w", ModuleName, err)
	}
	return reader, nil
}
