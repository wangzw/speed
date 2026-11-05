//go:build review

package main

// The review build: the default build plus the test controls a local
// walkthrough drives.
//
// A capability is compiled into the default build (see capabilities.go); the
// review tag adds only what is a test control rather than a product API
// (docs/adr/adr-starters-skeleton.md, "The review build's control routes"):
// the SMS template-review verdict route, the notification dispatch control
// route, and the loopback guard. None of those may exist in a deployment or
// baseline binary.
//
// The guard also constrains the review process's own configuration: a run whose
// HTTP listener is not loopback fails at startup, before a socket is bound. The
// guard limits the listening address only — a log line can still leave the
// machine through log collection or export — and what it keeps off the network
// is the seeded administrator password the repository publishes, the open
// signup, and the log carriers that write verification codes to the process
// output.
//
// The build also carries one settings-surface fixture: a business tenant whose
// identifier carries the protected- prefix has every settings write refused
// with 403 protected, while every other management seam still admits it, so
// organization administration opens and its security page is read-only. The
// prefix and the refusal are compiled under the review tag alone; the default
// build refuses only the personal domain (review_off.go), so no deployment
// knows the fixture. The end-to-end harness seeds the tenant and its
// administrator through its own configuration overlay, never through the
// shared review/sqlite.yaml; see review_settings_guard_test.go for the
// program-level case.
//
// The rest of this build's configuration is server/review/sqlite.yaml, a copy
// outside testdata so the six baselines under testdata stay exactly as they
// are.

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

// reviewGuardModuleName is the module name of the startup guard. It mounts
// nothing: its one work is refusing a run whose listening addresses are not
// loopback.
const reviewGuardModuleName = "review.guard"

// reviewHTTPConfigNamespace is where the HTTP module's input items are mounted:
// its own fixed namespace, which the guard reads the listener addresses from.
const reviewHTTPConfigNamespace = "http"

// reviewModules is what the review build adds to the serve roster: the two test
// controls and the guard. The capabilities themselves are named by
// capabilityModules, which every build carries.
var reviewModules = []string{
	// The control that moves one language's vendor verdict on the log SMS
	// carrier. It mounts nothing in a default build; see review_sms_control.go.
	reviewSMSControlModuleName,
	reviewGuardModuleName,
}

// reviewFeatureNames is the feature vocabulary the review controls declare and
// the default build does not. gen and contracts/features.json describe the
// default build, so the vocabulary case compares their exact contents with
// this binary's declarations less these names, and holds the difference to this
// list name for name: a review control that starts declaring a feature the list
// does not name, or a listed name whose declaration is gone, fails the case
// instead of being read as a baseline feature.
var reviewFeatureNames = []string{
	reviewSMSControlFeature,
	reviewNotificationControlFeature,
}

// protectedTestTenantPrefix marks a business tenant the review build's test
// assembly constructs to exercise the settings surface's read-only state. The
// settings write guard refuses it while every other management seam still
// admits it, so organization administration opens and the settings page shows
// the protected notice. The prefix and the refusal are compiled only under the
// review tag: review_off.go answers the same seam with false, so no default
// build knows the fixture.
const protectedTestTenantPrefix = "protected-"

// settingsWriteGuardReviewDenial reports whether the review build refuses a
// tenant beyond the host's personal-domain policy. It is the review-only half
// of settingsWriteGuard.Denied; the default build compiles the false answer in
// review_off.go instead.
func settingsWriteGuardReviewDenial(tenantID string) bool {
	return strings.HasPrefix(tenantID, protectedTestTenantPrefix)
}

// init registers the guard. Importing this file is what puts it in the available
// set, and reviewModules names it for a run.
func init() { core.ProcessRegistry.Register(reviewGuardModule()) }

// reviewGuardModule refuses a run whose listening addresses are not loopback.
//
// The review build seeds the administrator credential the repository publishes
// and opens signup, so a process anyone on the network can reach hands both to
// anyone. The guard reads the HTTP module's listener addresses in Prepare,
// before any socket is bound: a non-loopback address fails the startup, and the
// run never serves. It limits the listener only: a verification code written to
// the process log can still leave the machine through log collection or export.
func reviewGuardModule() core.Module {
	return core.Module{
		Name:     reviewGuardModuleName,
		Requires: []core.Requirement{{Token: (*config.Reader)(nil)}},
		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			reader, err := core.Resolve[config.Reader](reg)
			if err != nil {
				return core.Enablement{}, fmt.Errorf("%s: taking up the configuration reader: %w",
					reviewGuardModuleName, err)
			}
			if err := checkReviewListeners(reader); err != nil {
				return core.Enablement{}, err
			}
			return core.Enablement{State: core.StateEnabled}, nil
		},
	}
}

// reviewHTTPConfig is the part of the HTTP module's configuration the guard
// reads: the address of each configured listener. The listener entries carry
// more, which is the HTTP module's business.
type reviewHTTPConfig struct {
	Listeners map[string]reviewListenerConfig `config:"listeners"`
}

// reviewListenerConfig is one listener entry's address.
type reviewListenerConfig struct {
	Address string `config:"address"`
}

// checkReviewListeners refuses any listener the review build configured on a
// non-loopback address.
func checkReviewListeners(reader config.Reader) error {
	var cfg reviewHTTPConfig
	if err := reader.Decode(reviewHTTPConfigNamespace, &cfg); err != nil {
		return fmt.Errorf("%s: reading the listener addresses: %w", reviewGuardModuleName, err)
	}
	// The names are visited in order so that a run reporting a bad address
	// reports the same one every time, and so the verdict does not depend on
	// map iteration order.
	names := make([]string, 0, len(cfg.Listeners))
	for name := range cfg.Listeners {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		listener := cfg.Listeners[name]
		if loopbackAddress(listener.Address) {
			continue
		}
		return fmt.Errorf("%s: listener %q is bound to %q, which is not a loopback address. "+
			"The review build seeds a published administrator credential and opens signup, "+
			"so it serves loopback addresses only",
			reviewGuardModuleName, name, listener.Address)
	}
	return nil
}
