package invite

import (
	"context"
	"path/filepath"
	"testing"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/account"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/sqlite"
)

// TestAcceptCreatesAnAccountThroughTheRealAccountModule holds the whole of a
// redemption against the account module's own Create, over one SQLite database
// both migration sets are applied to: an invitation for an address with no
// account is accepted, an enabled account is created with that address as its
// email, and a membership with source invite stands beside it.
//
// The account product is the real one, taken from a registry, because Create is
// where CreateAccount.EmailVerifiedVia is judged against the Verification closed
// set. The invitation module's own cases inject a fake AccountAdmin and so never
// reach that judgement; the address-verified-through-invitation channel is what
// this case exists to hold.
func TestAcceptCreatesAnAccountThroughTheRealAccountModule(t *testing.T) {
	withRealAccountProduct(t, func(handle *gorm.DB, admin authn.AccountAdmin, accounts authn.Accounts, memberships authn.Memberships) {
		// This module's own migrations go onto the same handle the account module
		// built its tables on.
		applyMigrations(t, handle)

		p := &product{
			handle:      handle,
			cfg:         resolved{ttl: DefaultTTL},
			accounts:    accounts,
			accountAdm:  admin,
			memberships: memberships,
			mounter:     mounttest.New(t),
			audit:       &recordingAudit{},
			decider:     &fakeDecider{allow: true},
			logger:      (&logBuffer{}).logger(),
		}
		p.now = newTestClock().read

		ctx := context.Background()
		issueCtx := authntest.Context(t, ctx, authntest.WithAccount(adminID))
		issued, err := p.Issue(issueCtx, IssueInvite{Tenant: tenant, Email: "Newcomer@Example.com"})
		if err != nil {
			t.Fatalf("issuing an invitation: %v", err)
		}

		mem, err := p.Accept(ctx, AcceptInvite{
			Token: issued.Token, DisplayName: "Newcomer", Password: "correct horse battery staple",
		})
		if err != nil {
			t.Fatalf("accepting an invitation for an address with no account: %v", err)
		}

		// The account was created: the address now names one, and it is the
		// account the accepted membership points at.
		id, err := accounts.Lookup(ctx, authn.IdentifierEmail, "newcomer@example.com")
		if err != nil {
			t.Fatalf("looking the new account up by its address: %v", err)
		}
		if id != mem.Account {
			t.Errorf("the accepted membership names account %q, but the address names %q", mem.Account, id)
		}

		// The membership was written, with source invite.
		got, ok, err := memberships.Membership(ctx, tenant, id)
		if err != nil {
			t.Fatalf("reading the membership back: %v", err)
		}
		if !ok {
			t.Fatal("no membership row was written for the account the accepted invitation created")
		}
		if !got.Active || got.Source != SourceInvite {
			t.Errorf("membership = %+v, want active with source %q", got, SourceInvite)
		}
	})
}

// withRealAccountProduct brings the real account module up over a SQLite file of
// this test's own, so that its Create runs its own checks, and runs use inside
// the drive with the handle and the three capabilities the invitation module
// takes up. use runs before the drive closes the pool, which is why it is a
// callback rather than a set of return values.
func withRealAccountProduct(t *testing.T,
	use func(*gorm.DB, authn.AccountAdmin, authn.Accounts, authn.Memberships),
) {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "invite-e2e.db")
	body := `{"db":{"sqlite":{"dsn":"` + dsn + `"}}}`
	mods := []core.Module{sqlite.Module(), account.Module()}

	reader := authntest.ConfigReaderAll(t, body, mods...)
	reg := core.New()
	names := make([]string, 0, len(mods))
	for _, m := range mods {
		reg.Register(m)
		names = append(names, m.Name)
	}
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)

	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageStart,
		Participants: names,
		Action: func(_ context.Context, reg *core.Registry) error {
			database, err := core.Resolve[db.Database](reg)
			if err != nil {
				return err
			}
			admin, err := core.Resolve[authn.AccountAdmin](reg)
			if err != nil {
				return err
			}
			accounts, err := core.Resolve[authn.Accounts](reg)
			if err != nil {
				return err
			}
			memberships, err := core.Resolve[authn.Memberships](reg)
			if err != nil {
				return err
			}
			use(database.DB(), admin, accounts, memberships)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("driving the account module: %v", err)
	}
}
