package gate

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// TestAConfigurationThisGatewayCannotRunOnIsRefused holds the inputs that are
// refused rather than run on.
//
// The repeated endpoint is the one worth naming: it would register the
// authentication layer twice on that endpoint, so every request would be
// authenticated twice and every backend asked twice — doubling the load on a
// credential store and every counter read off it, for no gain at all.
func TestAConfigurationThisGatewayCannotRunOnIsRefused(t *testing.T) {
	sound := moduleConfig{
		Endpoints:  []string{"public"},
		CallBudget: time.Second,
		InFlight:   8,
	}

	for _, c := range []struct {
		name     string
		mutate   func(moduleConfig) moduleConfig
		mentions string
	}{
		{
			name: "an endpoint named twice",
			mutate: func(m moduleConfig) moduleConfig {
				m.Endpoints = []string{"public", "admin", "public"}
				return m
			},
			mentions: "twice",
		},
		{
			name: "an endpoint name that is empty",
			mutate: func(m moduleConfig) moduleConfig {
				m.Endpoints = []string{""}
				return m
			},
			mentions: "empty endpoint name",
		},
		{
			name: "a call budget of zero",
			mutate: func(m moduleConfig) moduleConfig {
				m.CallBudget = 0
				return m
			},
			mentions: "never gives back",
		},
		{
			name: "an in-flight gate that admits nothing",
			mutate: func(m moduleConfig) moduleConfig {
				m.InFlight = 0
				return m
			},
			mentions: "refuses every request",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := c.mutate(sound).resolve()
			if err == nil {
				t.Fatal("the configuration resolved without complaint")
			}
			if !strings.Contains(err.Error(), c.mentions) {
				t.Errorf("the failure reads %q and does not say what is wrong (looked for %q)",
					err, c.mentions)
			}
			// The sentinel table is closed over what this module judges
			// itself, and a caller tells a configuration it has to
			// correct from every other startup failure by it.
			if !errors.Is(err, authn.ErrInvalidConfig) {
				t.Errorf("the failure reads %q and does not wrap %v", err, authn.ErrInvalidConfig)
			}
		})
	}
}

// TestASoundConfigurationResolves keeps the cases above honest: each of them
// has to fail for its own mutation, not for the shape they all start from.
func TestASoundConfigurationResolves(t *testing.T) {
	set, err := moduleConfig{
		Endpoints:  []string{"public", "admin"},
		CallBudget: time.Second,
		InFlight:   8,
	}.resolve()
	if err != nil {
		t.Fatalf("a sound configuration gave %v", err)
	}
	if len(set.endpoints) != 2 {
		t.Errorf("the settings guard %v, want both endpoints", set.endpoints)
	}
}

// TestWhereAHostWritesThisModulesInputs pins the two names that are easy to
// swap for one another, because both are spelled out of the same two words.
//
// The configuration namespace is this module's own name: each backend hangs its
// section under the capability directory too, and a gateway that took the bare
// heading would sit on the path they extend. The startup diagnostics go the
// other way — they are prefixed with the capability directory, because a reader
// scanning them is looking for what authentication did, not for which of its
// modules said so.
func TestWhereAHostWritesThisModulesInputs(t *testing.T) {
	if ConfigNamespace != ModuleName {
		t.Errorf("the configuration namespace is %q while the module is %q, so a host writing "+
			"one would be read by neither", ConfigNamespace, ModuleName)
	}
	if diagnosticPrefix != "authn: " {
		t.Errorf("the startup diagnostics are prefixed %q, want %q", diagnosticPrefix, "authn: ")
	}
}
