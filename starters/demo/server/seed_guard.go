package main

// The seed guard: a deployment that listens off loopback must not run on the
// demonstration seed the compiled defaults document carries.
//
// A copy lifted out of this repository starts on the defaults document, whose
// seed names the development operator the walkthrough publishes and a
// walkthrough tenant. That is what makes a bare run usable, and it is also what
// must never reach a deployment: the account is a known credential with the
// platform-administrator role. The seed command writes whatever the effective
// seed section names, and the deployment entrypoint seeds before it serves, so
// the refusal has to come before either command writes or binds anything.
//
// The verdict rests on two facts, each read from the configuration:
//
//   - Whether the account list is the compiled one. config.EffectiveLayer
//     answers which layer supplied seed.accounts without returning the value,
//     which is what lets a sensitive item be asked about. When the layer query
//     cannot answer -- a Reader another provider delivered, or a binary that
//     declares no such item -- the guard falls back to comparing the effective
//     account list with the compiled one.
//   - Whether the deployment listens off loopback. It decodes http.listeners
//     and uses the same address classification as everything else in this host
//     (loopbackAddress in listener_address.go).
//
// The guard is a module, not a branch inside the commands, so its check runs in
// Prepare: after the configuration is loaded and before any module is
// constructed, any migration applied, any account written or any socket bound.

import (
	"context"
	"fmt"
	"slices"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

// seedGuardModuleName is the module name of the startup guard. It mounts
// nothing: its one work is refusing a run that would serve the demonstration
// seed.
const seedGuardModuleName = "seed.guard"

// seedGuardHTTPNamespace is where the HTTP module's input items are mounted:
// its own fixed namespace, which the guard reads the listener addresses from.
const seedGuardHTTPNamespace = "http"

// seedAccountsPath is the input item whose layer decides whether the seed is
// the compiled one.
const seedAccountsPath = seedNamespace + "." + accountsItem

// init registers the guard. Importing this file is what puts it in the available
// set; the serve and seed rosters name it for a run.
func init() { core.ProcessRegistry.Register(seedGuardModule()) }

// seedGuardModule refuses a run whose seed is the compiled one while a listener
// is bound off loopback.
//
// It is part of both the serve and seed rosters: serve is the deployment form
// the listener fact describes, and seed is the command that would write the
// demonstration account. Its Prepare runs before either does its work.
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

// seedGuardHTTPConfig is the part of the HTTP module's configuration the guard
// reads: the address of each configured listener. The listener entries carry
// more, which is the HTTP module's business.
type seedGuardHTTPConfig struct {
	Listeners map[string]seedGuardListener `config:"listeners"`
}

// seedGuardListener is one listener entry's address.
type seedGuardListener struct {
	Address string `config:"address"`
}

// seedAccountsView is the part of the seed section the fallback reads: the
// account list whose equality with the compiled one decides the same question
// EffectiveLayer answers.
type seedAccountsView struct {
	Accounts []seedAccount `config:"accounts"`
}

// checkSeedCoverage refuses a run that would serve the compiled demonstration
// seed on a listener reachable from off the machine.
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
// though the item is sensitive. The fallback covers a Reader the query cannot
// answer about; it compares the effective list with the compiled one, and an
// empty list is taken as the default because the compiled list is not empty and
// an undeclared path decodes to nothing.
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
