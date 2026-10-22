package account

import (
	"context"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
)

// standInDBName is the name a stand-in database module registers under, so that
// the descriptor's requirement is met without a real engine.
const standInDBName = "stand-in.database"

// standInDBModule delivers the database capability over a stub. The module's New
// stage takes the handle and reads no table, which is what makes the delivery
// check below runnable without a migrated database.
func standInDBModule(database db.Database) core.Module {
	return core.Module{
		Name:     standInDBName,
		Provides: []core.Provision{{Token: (*db.Database)(nil), Exclusive: true}},
		New:      func(context.Context, *core.Registry) (any, error) { return database, nil },
	}
}

// driveModule brings the module up in a real registry beside a stand-in database
// and the configuration reader, and runs the action once every instance exists.
func driveModule(t *testing.T, body string, action func(context.Context, *core.Registry) error) {
	t.Helper()
	reader := authntest.ConfigReader(t, Module(), body)
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(standInDBModule(stubDatabase{dialect: db.SQLite}))
	reg.Register(Module())
	reg.Supply(reader, config.ModuleName)
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{ModuleName, standInDBName},
		Action:       action,
	})
	if err != nil {
		t.Fatalf("driving the module: %v", err)
	}
}

// The seven capabilities the descriptor declares are each delivered, and each
// resolves to one and the same instance: construction asserts the instance
// against every capability, so a product missing one fails the drive with
// core.ErrUndeliveredCapability before the action runs.
func TestTheSevenCapabilitiesAreDeliveredByOneInstance(t *testing.T) {
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
		throttle, err := core.Resolve[authn.Throttle](reg)
		if err != nil {
			return err
		}
		p, ok := accounts.(*product)
		if !ok {
			t.Fatalf("the delivered Accounts is a %T, not the module's product", accounts)
		}
		for _, got := range []any{admin, signups, verification, passwords, memberships, throttle} {
			if got != p {
				t.Errorf("a capability resolved to %T, not the one product the descriptor built", got)
			}
		}
		return nil
	})
}

// A command that does not name the module delivers no capability: the drive
// without it in the participants finds no provider, which is what makes a host
// that forgot the module a startup failure.
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
