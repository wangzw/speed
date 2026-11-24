package main

import (
	"context"
	"fmt"
	"slices"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

const seedGuardModuleName = "seed.guard"

const seedGuardHTTPNamespace = "http"

const seedAccountsPath = seedNamespace + "." + accountsItem

func init() { core.ProcessRegistry.Register(seedGuardModule()) }

func seedGuardModule() core.Module {
	return core.Module{
		Name:     seedGuardModuleName,
		Requires: []core.Requirement{{Token: (*config.Reader)(nil)}},
		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			reader, err := core.Resolve[config.Reader](reg)
			if err != nil {
				return core.Enablement{}, fmt.Errorf("%s: taking up the configuration reader: %w",
					seedGuardModuleName, err)
			}
			if err := checkSeedCoverage(reader); err != nil {
				return core.Enablement{}, err
			}
			return core.Enablement{State: core.StateEnabled}, nil
		},
	}
}

type seedGuardHTTPConfig struct {
	Listeners map[string]seedGuardListener `config:"listeners"`
}

type seedGuardListener struct {
	Address string `config:"address"`
}

type seedAccountsView struct {
	Accounts []seedAccount `config:"accounts"`
}

func checkSeedCoverage(reader config.Reader) error {
	fromDefaults, err := seedFromDefaults(reader)
	if err != nil {
		return fmt.Errorf("%s: deciding where the seed comes from: %w", seedGuardModuleName, err)
	}
	if !fromDefaults {
		return nil
	}
	name, address, err := firstExposedListener(reader)
	if err != nil {
		return fmt.Errorf("%s: reading the listener addresses: %w", seedGuardModuleName, err)
	}
	if name == "" {
		return nil
	}
	return fmt.Errorf("%s: the seed comes from the built-in defaults document, so this deployment "+
		"would create the demonstration accounts, while listener %q is bound to %q, which is not a "+
		"loopback address. Override the seed in the deployment configuration, or listen on a loopback "+
		"address", seedGuardModuleName, name, address)
}

// seedFromDefaults reports whether the effective seed accounts are the compiled
// demonstration ones.
//
// config.EffectiveLayer is the primary answer: it reads the layer the value came
// from and never the value, so the account list stays out of the answer even
// though the item is sensitive. The fallback compares the effective list with
// the compiled one, and an empty list is taken as the default because the
// compiled list is not empty and an undeclared path decodes to nothing.
func seedFromDefaults(reader config.Reader) (bool, error) {
	layer, layerErr := config.EffectiveLayer(reader, seedAccountsPath)
	if layerErr == nil {
		return layer == config.LayerDefault, nil
	}
	var view seedAccountsView
	if decodeErr := reader.Decode(seedNamespace, &view); decodeErr != nil {
		return false, fmt.Errorf("the seed's layer is unavailable (%w) and reading seed.%s failed: %w",
			layerErr, accountsItem, decodeErr)
	}
	return len(view.Accounts) == 0 || slices.Equal(view.Accounts, demoSeedAccounts()), nil
}

// firstExposedListener names the first listener, in name order, whose address
// is not loopback. It answers an empty name when every listener is loopback, and
// sorts the names so a run reporting a bad address reports the same one every
// time rather than following map iteration order.
func firstExposedListener(reader config.Reader) (string, string, error) {
	var cfg seedGuardHTTPConfig
	if err := reader.Decode(seedGuardHTTPNamespace, &cfg); err != nil {
		return "", "", err
	}
	names := make([]string, 0, len(cfg.Listeners))
	for name := range cfg.Listeners {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if address := cfg.Listeners[name].Address; !loopbackAddress(address) {
			return name, address, nil
		}
	}
	return "", "", nil
}
