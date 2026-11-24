package admission

import (
	"context"
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/settings"
)

func TestRequiresMFAAnswersPerActiveMembership(t *testing.T) {
	cases := []struct {
		name        string
		platform    bool
		tenantRows  map[string]bool
		memberships []authn.Membership
		want        bool
	}{
		{
			name:        "no membership requires anything",
			memberships: []authn.Membership{{Account: alice, Tenant: acme}},
			want:        false,
		},
		{
			name:        "an active membership of a requiring tenant",
			tenantRows:  map[string]bool{acme: true},
			memberships: []authn.Membership{{Account: alice, Tenant: acme, Active: true}},
			want:        true,
		},
		{
			name:        "one requiring tenant among several",
			tenantRows:  map[string]bool{acme: true},
			memberships: []authn.Membership{{Account: alice, Tenant: globex, Active: true}, {Account: alice, Tenant: acme, Active: true}},
			want:        true,
		},
		{
			name:        "the platform floor applies to every tenant",
			platform:    true,
			memberships: []authn.Membership{{Account: alice, Tenant: acme, Active: true}},
			want:        true,
		},
		{
			name:        "a tenant row of false does not lower the platform floor",
			platform:    true,
			tenantRows:  map[string]bool{acme: false},
			memberships: []authn.Membership{{Account: alice, Tenant: acme, Active: true}},
			want:        true,
		},
		{
			name:        "a suspended membership is not counted",
			tenantRows:  map[string]bool{acme: true},
			memberships: []authn.Membership{{Account: alice, Tenant: acme}},
			want:        false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			store := r.withSettings()
			if c.platform {
				store.set(settings.Global(), requireMFAKey, true)
			}
			for tenantID, value := range c.tenantRows {
				store.set(settings.ForTenant(tenantID), requireMFAKey, value)
			}
			for _, m := range c.memberships {
				r.members.setRow(m)
			}
			got, err := r.l.RequiresMFA(context.Background(), alice)
			if err != nil {
				t.Fatalf("RequiresMFA: %v", err)
			}
			if got != c.want {
				t.Errorf("RequiresMFA = %v, want %v", got, c.want)
			}
		})
	}
}

func TestRequiresMFAFailsClosed(t *testing.T) {
	t.Run("a settings read that fails is an error", func(t *testing.T) {
		r := newRig(t)
		store := r.withSettings()
		r.members.setRow(authn.Membership{Account: alice, Tenant: acme, Active: true})
		store.failKey(requireMFAKey, errors.New("settings unavailable"))
		got, err := r.l.RequiresMFA(context.Background(), alice)
		if err == nil {
			t.Fatalf("RequiresMFA = %v, nil, want an error", got)
		}
		if got {
			t.Errorf("the answer beside the error is %v, want false", got)
		}
	})

	t.Run("a membership read that fails is an error", func(t *testing.T) {
		r := newRig(t)
		r.withSettings()
		r.members.failTenants(errors.New("database unavailable"))
		got, err := r.l.RequiresMFA(context.Background(), alice)
		if err == nil {
			t.Fatalf("RequiresMFA = %v, nil, want an error", got)
		}
		if got {
			t.Errorf("the answer beside the error is %v, want false", got)
		}
	})

	t.Run("no settings store has no requirement it could turn on", func(t *testing.T) {
		r := newRig(t)
		r.members.setRow(authn.Membership{Account: alice, Tenant: acme, Active: true})
		got, err := r.l.RequiresMFA(context.Background(), alice)
		if err != nil || got {
			t.Errorf("RequiresMFA = %v, %v, want false and no error", got, err)
		}
	})
}
