package preferences

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/notification"
)

// TestTheTaxonomyTakesTheAvailableSetNotTheParticipationSet checks that the
// taxonomy takes the available set rather than the participation set.
func TestTheTaxonomyTakesTheAvailableSetNotTheParticipationSet(t *testing.T) {
	ghost := declaringModule("ghost", aSpec("ghost.digest.ready", true, notification.ChannelInApp))
	live := declaringModule("billing", aSpec("billing.invoice_paid", true, notification.ChannelInApp))
	h := engines[0].on(t, harness{declare: []core.Module{live}, registered: []core.Module{ghost}})
	h.drive(t, func(_ context.Context, reg *core.Registry) error {
		prefs := preferencesOf(t, reg)
		if _, ok := prefs.Type("ghost.digest.ready"); !ok {
			t.Error("the type of a module outside the participating set is not in the taxonomy")
		}
		if _, ok := prefs.Type("billing.invoice_paid"); !ok {
			t.Error("the type of a participating module is not in the taxonomy")
		}
		keys := map[string]bool{}
		for _, spec := range prefs.Types() {
			keys[spec.Key] = true
		}
		if !keys["ghost.digest.ready"] || !keys["billing.invoice_paid"] {
			t.Errorf("Types() = %v, want both the ghost module's type and the live module's", keys)
		}
		return nil
	})
}

// TestADeclarationThatIsNotSelfConsistentFailsTheStartup holds every rule
// of the criterion against a declaration.
func TestADeclarationThatIsNotSelfConsistentFailsTheStartup(t *testing.T) {
	cases := map[string]notification.TypeSpec{
		"empty key": {
			Key:             "",
			DefaultChannels: []notification.Channel{notification.ChannelInApp},
			Unsubscribable:  true,
		},
		"unknown default channel": {
			Key:             "billing.invoice_paid",
			DefaultChannels: []notification.Channel{notification.Channel("carrier_pigeon")},
			Unsubscribable:  true,
		},
		"empty default channel set": {
			Key:             "billing.invoice_paid",
			DefaultChannels: nil,
			Unsubscribable:  true,
		},
		"repeated default channel": {
			Key:             "billing.invoice_paid",
			DefaultChannels: []notification.Channel{notification.ChannelInApp, notification.ChannelInApp},
			Unsubscribable:  true,
		},
		"repeated whitelist entry": {
			Key:                    "billing.invoice_paid",
			DefaultChannels:        []notification.Channel{notification.ChannelInApp},
			Unsubscribable:         true,
			RecipientVisibleParams: []string{"amount", "amount"},
		},
	}
	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			h := engines[0].on(t, harness{
				declare: []core.Module{declaringModule("billing", spec)},
				depth:   core.StageNew,
			})
			err := h.run(t, nil)
			if !errors.Is(err, notification.ErrInvalidConfig) {
				t.Fatalf("the drive ended with %v, want ErrInvalidConfig", err)
			}
			if !strings.Contains(err.Error(), "billing") {
				t.Errorf("the failure does not name the declaring module: %v", err)
			}
		})
	}
}

// TestTwoModulesClaimingOneKeyFailTheStartup holds that a type key is globally
// unique: two modules declaring one key is a startup failure naming both.
func TestTwoModulesClaimingOneKeyFailTheStartup(t *testing.T) {
	a := declaringModule("billing", aSpec("billing.invoice_paid", true))
	b := declaringModule("payments", aSpec("billing.invoice_paid", true))
	h := engines[0].on(t, harness{declare: []core.Module{a, b}, depth: core.StageNew})
	err := h.run(t, nil)
	if !errors.Is(err, notification.ErrInvalidConfig) {
		t.Fatalf("the drive ended with %v, want ErrInvalidConfig", err)
	}
	if !strings.Contains(err.Error(), "billing") || !strings.Contains(err.Error(), "payments") {
		t.Errorf("the failure does not name both declaring modules: %v", err)
	}
}

// TestTypesIsSortedAndCopied holds that the taxonomy is ordered by type key and
// that a caller changing what Types handed it does not disturb the module.
func TestTypesIsSortedAndCopied(t *testing.T) {
	beta := declaringModule("beta", aSpec("beta.two", true))
	alpha := declaringModule("alpha", aSpec("alpha.one", true))
	h := engines[0].on(t, harness{declare: []core.Module{beta, alpha}})
	h.drive(t, func(_ context.Context, reg *core.Registry) error {
		prefs := preferencesOf(t, reg)
		got := prefs.Types()
		if len(got) != 2 || got[0].Key != "alpha.one" || got[1].Key != "beta.two" {
			t.Fatalf("Types() keys = %v, want ascending order", keysOf(got))
		}
		got[0].Key = "mutated"
		got[0].DefaultChannels[0] = notification.ChannelSMS
		fresh, _ := prefs.Type("alpha.one")
		if fresh.Key != "alpha.one" {
			t.Errorf("mutating a returned spec changed the taxonomy: key is %q", fresh.Key)
		}
		if len(fresh.DefaultChannels) == 0 || fresh.DefaultChannels[0] != notification.ChannelInApp {
			t.Errorf("mutating a returned spec's channels changed the taxonomy: %v", fresh.DefaultChannels)
		}
		return nil
	})
}

func keysOf(specs []notification.TypeSpec) []string {
	out := make([]string, len(specs))
	for i, spec := range specs {
		out[i] = spec.Key
	}
	return out
}
