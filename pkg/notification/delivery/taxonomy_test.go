package delivery

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/notification"
)

type declaration map[string]notification.TypeSpec

func declaredUnder(spec notification.TypeSpec) declaration {
	return declaration{"billing": spec}
}

func legal() notification.TypeSpec {
	return notification.TypeSpec{
		Key:             "billing.invoice_paid",
		DefaultChannels: []notification.Channel{notification.ChannelInApp},
		Unsubscribable:  true,
	}
}

// The two consumption points apply one criterion, so a declaration is refused or
// accepted by both alike.
func TestOneDeclarationIsRefusedTheSameWayByBothConsumptionPoints(t *testing.T) {
	refused := map[string]declaration{
		"empty key": declaredUnder(notification.TypeSpec{
			DefaultChannels: []notification.Channel{notification.ChannelInApp},
		}),
		"empty default channels": declaredUnder(notification.TypeSpec{
			Key: "billing.invoice_paid",
		}),
		"unknown default channel": declaredUnder(notification.TypeSpec{
			Key:             "billing.invoice_paid",
			DefaultChannels: []notification.Channel{notification.Channel("carrier_pigeon")},
		}),
		"repeated default channel": declaredUnder(notification.TypeSpec{
			Key:             "billing.invoice_paid",
			DefaultChannels: []notification.Channel{notification.ChannelInApp, notification.ChannelInApp},
		}),
		"repeated whitelist entry": declaredUnder(notification.TypeSpec{
			Key:                    "billing.invoice_paid",
			DefaultChannels:        []notification.Channel{notification.ChannelInApp},
			RecipientVisibleParams: []string{"amount", "amount"},
		}),
		"one key declared twice": {
			"billing":  legal(),
			"payments": legal(),
		},
	}
	for name, declared := range refused {
		t.Run("refused/"+name, func(t *testing.T) {
			forEachPoint(t, declared, func(t *testing.T, err error) {
				if !errors.Is(err, notification.ErrInvalidConfig) {
					t.Fatalf("the startup ended with %v, want notification.ErrInvalidConfig", err)
				}
				if !strings.Contains(err.Error(), "billing") {
					t.Errorf("the refusal does not name a declaring module: %v", err)
				}
			})
		})
	}

	accepted := map[string]declaration{
		"the legal declaration": declaredUnder(legal()),
		"a parameter whitelist": declaredUnder(notification.TypeSpec{
			Key:                    "billing.invoice_paid",
			DefaultChannels:        []notification.Channel{notification.ChannelInApp},
			Unsubscribable:         true,
			RecipientVisibleParams: []string{"invoice_id", "amount"},
		}),
		"a type that cannot be unsubscribed over an external channel": declaredUnder(notification.TypeSpec{
			Key:             "authn.code.sent",
			DefaultChannels: []notification.Channel{notification.ChannelInApp, notification.ChannelSMS},
			Unsubscribable:  false,
		}),
		// An empty collection is legal too: the criterion has no rule that a
		// process must declare a type, and the duplicate rule is about a key
		// declared twice rather than a key being required.
		"nothing declared at all": {},
	}
	for name, declared := range accepted {
		t.Run("accepted/"+name, func(t *testing.T) {
			forEachPoint(t, declared, func(t *testing.T, err error) {
				if err != nil {
					t.Errorf("the startup ended with %v, want no failure", err)
				}
			})
		})
	}
}

func forEachPoint(t *testing.T, declared declaration, check func(t *testing.T, err error)) {
	t.Helper()
	points := map[string]host{
		"notification.delivery":    {specs: declared},
		"notification.preferences": {specs: declared, withPreferences: true},
	}
	for name, h := range points {
		t.Run(name, func(t *testing.T) {
			check(t, h.drive(t, nil))
		})
	}
}

// A collection the criterion accepts is not merely started: this module
// holds it as the reference a dispatch is validated against.
func TestAnAcceptedCollectionBecomesTheTaxonomy(t *testing.T) {
	err := host{}.drive(t, func(_ context.Context, reg *core.Registry) error {
		dispatcher, resolveErr := core.Resolve[notification.Dispatcher](reg)
		if resolveErr != nil {
			return resolveErr
		}
		p, ok := dispatcher.(*pipeline)
		if !ok {
			t.Fatalf("the delivered dispatcher is a %T, not this module's product", dispatcher)
		}
		if _, known := p.taxonomy.spec(typeName); !known {
			t.Errorf("the taxonomy does not hold the declared key %q", typeName)
		}
		if _, known := p.taxonomy.spec("nobody.declared.this"); known {
			t.Error("the taxonomy holds a key nobody declared")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
}
