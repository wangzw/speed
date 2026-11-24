package account

import (
	"context"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/provisioning"
	"github.com/vislake/speed/pkg/authn/provisioning/provisioningtest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
)

const standInDBName = "stand-in.database"

func standInDBModule(database db.Database) core.Module {
	return core.Module{
		Name:     standInDBName,
		Provides: []core.Provision{{Token: (*db.Database)(nil), Exclusive: true}},
		New:      func(context.Context, *core.Registry) (any, error) { return database, nil },
	}
}

func driveModule(t *testing.T, body string, action func(context.Context, *core.Registry) error) {
	t.Helper()
	reader := authntest.ConfigReader(t, Module(), body)
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(standInDBModule(stubDatabase{dialect: db.SQLite}))
	reg.Register(provisioningtest.Module())
	reg.Register(Module())
	reg.Supply(reader, config.ModuleName)
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{ModuleName, standInDBName, provisioningtest.ModuleName},
		Action:       action,
	})
	if err != nil {
		t.Fatalf("driving the module: %v", err)
	}
}

// Each of the eight capabilities resolves to one and the same instance, and a missing one fails the drive before the action runs.
func TestTheEightCapabilitiesAreDeliveredByOneInstance(t *testing.T) {
	driveModule(t, `{}`, func(_ context.Context, reg *core.Registry) error {
		accounts, err := core.Resolve[authn.Accounts](reg)
		if err != nil {
			return err
		}
		admin, err := core.Resolve[authn.AccountAdmin](reg)
		if err != nil {
			return err
		}
		signups, err := core.Resolve[authn.Signups](reg)
		if err != nil {
			return err
		}
		verification, err := core.Resolve[authn.SignupVerification](reg)
		if err != nil {
			return err
		}
		passwords, err := core.Resolve[authn.Passwords](reg)
		if err != nil {
			return err
		}
		memberships, err := core.Resolve[authn.Memberships](reg)
		if err != nil {
			return err
		}
		cleanup, err := core.Resolve[authn.MembershipCleanup](reg)
		if err != nil {
			return err
		}
		throttle, err := core.Resolve[authn.Throttle](reg)
		if err != nil {
			return err
		}
		writer, err := core.Resolve[provisioning.MembershipWriter](reg)
		if err != nil {
			return err
		}
		p, ok := accounts.(*product)
		if !ok {
			t.Fatalf("the delivered Accounts is a %T, not the module's product", accounts)
		}
		for _, got := range []any{admin, signups, verification, passwords, memberships, cleanup, throttle, writer} {
			if got != p {
				t.Errorf("a capability resolved to %T, not the one product the descriptor built", got)
			}
		}
		return nil
	})
}

// A drive without the module in its participants finds no provider, so a host that forgot it fails at startup.
func TestACapabilityWithNoModuleIsMissing(t *testing.T) {
	reader := authntest.ConfigReader(t, Module(), `{}`)
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(standInDBModule(stubDatabase{dialect: db.SQLite}))
	reg.Supply(reader, config.ModuleName)
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{standInDBName},
		Action: func(_ context.Context, reg *core.Registry) error {
			_, err := core.Resolve[authn.Accounts](reg)
			return err
		},
	})
	if err == nil {
		t.Fatal("a registry without the module resolved Accounts")
	}
}
