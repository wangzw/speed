package invite

import (
	"context"
	"errors"
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
	"github.com/vislake/speed/pkg/settings"
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
	withRealAccountProduct(t, func(handle *gorm.DB, _ authn.Signups, admin authn.AccountAdmin, accounts authn.Accounts,
		verification authn.SignupVerification, memberships authn.Memberships,
	) {
		// This module's own migrations go onto the same handle the account module
		// built its tables on.
		applyMigrations(t, handle)

		p := newRealProduct(t, handle, admin, accounts, verification, memberships)

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

// TestAcceptProvesAnAdministratorAssertedAddress holds that an invitation is a
// proof for an address an administrator wrote without proof: accepting the
// token brings the existing identifier up to invite, and the account stops
// reading pending.
func TestAcceptProvesAnAdministratorAssertedAddress(t *testing.T) {
	withRealAccountProduct(t, func(handle *gorm.DB, _ authn.Signups, admin authn.AccountAdmin, accounts authn.Accounts,
		verification authn.SignupVerification, memberships authn.Memberships,
	) {
		applyMigrations(t, handle)
		p := newRealProduct(t, handle, admin, accounts, verification, memberships)

		ctx := context.Background()
		created, err := admin.Create(ctx, authn.CreateAccount{
			Kind:             authn.KindUser,
			Email:            "Preprovisioned@Example.com",
			EmailVerifiedVia: authn.Verification{Via: authn.VerifiedViaAdmin},
			Via:              authn.ReasonAdmin,
		})
		if err != nil {
			t.Fatalf("creating a provisioned account: %v", err)
		}
		status, err := verification.SignupStateOf(ctx, created.ID)
		if err != nil {
			t.Fatalf("reading the provisioned account's state: %v", err)
		}
		if status.State != authn.SignupPending {
			t.Fatalf("the provisioned account reads %v, want pending until its email is proved", status.State)
		}

		issueCtx := authntest.Context(t, ctx, authntest.WithAccount(adminID))
		issued, err := p.Issue(issueCtx, IssueInvite{Tenant: tenant, Email: "preprovisioned@example.com"})
		if err != nil {
			t.Fatalf("issuing an invitation for the provisioned address: %v", err)
		}
		if _, err = p.Accept(ctx, AcceptInvite{Token: issued.Token}); err != nil {
			t.Fatalf("accepting the invitation: %v", err)
		}

		status, err = verification.SignupStateOf(ctx, created.ID)
		if err != nil {
			t.Fatalf("reading the account's state after acceptance: %v", err)
		}
		if status.State != authn.SignupVerified {
			t.Errorf("the account reads %v after the invitation proved its address, want verified", status.State)
		}
		identifiers, err := accounts.Identifiers(ctx, created.ID)
		if err != nil {
			t.Fatalf("reading the account's identifiers: %v", err)
		}
		for _, identifier := range identifiers {
			if identifier.Kind == authn.IdentifierEmail && identifier.Value == "preprovisioned@example.com" {
				if identifier.VerifiedVia != VerifiedViaInvite {
					t.Errorf("the address reads via %q, want %q", identifier.VerifiedVia, VerifiedViaInvite)
				}
			}
		}
	})
}

// newRealProduct builds this module's product over the account module's own
// capabilities and the database both modules' tables stand on.
func newRealProduct(t *testing.T, handle *gorm.DB, admin authn.AccountAdmin, accounts authn.Accounts,
	verification authn.SignupVerification, memberships authn.Memberships,
) *product {
	t.Helper()
	p := &product{
		handle:       handle,
		accounts:     accounts,
		accountAdm:   admin,
		verification: verification,
		memberships:  memberships,
		mounter:      mounttest.New(t),
		audit:        &recordingAudit{},
		decider:      &fakeDecider{allow: true},
		logger:       (&logBuffer{}).logger(),
	}
	p.now = newTestClock().read
	return p
}

// openPasswordSignupKey is the setting the account module's registration reads
// as its authority. The stand-in below turns it on; every other key it answers
// as undeclared, which the module reads as that item's declared default.
const openPasswordSignupKey = "authn.account.password-signup"

// openPasswordSignup is a settings store whose password-signup item is open. It
// is what lets a case start from a real pending self-registration rather than a
// hand-written row.
type openPasswordSignup struct{}

func (openPasswordSignup) Get(_ settings.Layer, key string, target any) error {
	if key != openPasswordSignupKey {
		return settings.ErrUnknownKey
	}
	value, ok := target.(*string)
	if !ok {
		return settings.ErrTypeMismatch
	}
	*value = "open"
	return nil
}

func (openPasswordSignup) Set(context.Context, settings.Write) error {
	return errors.New("invite test: the stand-in settings store is read-only")
}

func (openPasswordSignup) Delete(context.Context, settings.Layer, string, string) error {
	return errors.New("invite test: the stand-in settings store is read-only")
}

func (openPasswordSignup) List(context.Context, settings.Layer) ([]settings.Entry, error) {
	return nil, nil
}

func (openPasswordSignup) Declared() []settings.DeclaredItem { return nil }

// provideSettings declares the stand-in module that hands the settings store
// over.
func provideSettings() core.Module {
	return core.Module{
		Name:     "stand-in.invite-settings",
		Provides: []core.Provision{{Token: (*settings.Store)(nil), Exclusive: true}},
		New:      func(context.Context, *core.Registry) (any, error) { return openPasswordSignup{}, nil },
	}
}

// withRealAccountProduct brings the real account module up over a SQLite file of
// this test's own, so that its Create runs its own checks, and runs use inside
// the drive with the handle and the capabilities the invitation module takes up.
// use runs before the drive closes the pool, which is why it is a callback
// rather than a set of return values.
func withRealAccountProduct(t *testing.T,
	use func(*gorm.DB, authn.Signups, authn.AccountAdmin, authn.Accounts, authn.SignupVerification, authn.Memberships),
) {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "invite-e2e.db")
	body := `{"db":{"sqlite":{"dsn":"` + dsn + `"}}}`
	mods := []core.Module{sqlite.Module(), account.Module(), provideSettings()}

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
			signups, err := core.Resolve[authn.Signups](reg)
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
			verification, err := core.Resolve[authn.SignupVerification](reg)
			if err != nil {
				return err
			}
			memberships, err := core.Resolve[authn.Memberships](reg)
			if err != nil {
				return err
			}
			use(database.DB(), signups, admin, accounts, verification, memberships)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("driving the account module: %v", err)
	}
}

// TestAcceptMergesIntoThePendingAccountThroughTheRealAccountModule holds the
// acceptance's merge branch against the account module's own writes over one
// database: an address that is nobody's identifier but is the pending contact of
// a self-registration is proved by the invitation's token, so the identifier is
// written on that account through invite, the pending contact with the same
// value is consumed, the account's signup state moves from pending to verified,
// the membership hangs on the same account and no second account is opened.
func TestAcceptMergesIntoThePendingAccountThroughTheRealAccountModule(t *testing.T) {
	withRealAccountProduct(t, func(handle *gorm.DB, signups authn.Signups, admin authn.AccountAdmin,
		accounts authn.Accounts, verification authn.SignupVerification, memberships authn.Memberships,
	) {
		applyMigrations(t, handle)
		p := newRealProduct(t, handle, admin, accounts, verification, memberships)
		ctx := context.Background()

		registered, err := signups.Register(ctx, authn.RegisterAccount{
			Username: "newcomer", Email: "Newcomer@Example.com", Password: "password123",
		})
		if err != nil {
			t.Fatalf("registering the pending account: %v", err)
		}
		if status, sErr := verification.SignupStateOf(ctx, registered.ID); sErr != nil || status.State != authn.SignupPending {
			t.Fatalf("the registered account is %+v (%v), want a pending registration", status, sErr)
		}

		issueCtx := authntest.Context(t, ctx, authntest.WithAccount(adminID))
		issued, err := p.Issue(issueCtx, IssueInvite{Tenant: tenant, Email: "newcomer@example.com"})
		if err != nil {
			t.Fatalf("issuing an invitation for the pending address: %v", err)
		}

		mem, err := p.Accept(ctx, AcceptInvite{Token: issued.Token})
		if err != nil {
			t.Fatalf("accepting an invitation for a pending contact: %v", err)
		}

		// The membership hangs on the account that was already there.
		if mem.Account != registered.ID {
			t.Fatalf("the accepted membership names account %q, want the pending account %q", mem.Account, registered.ID)
		}
		got, ok, err := memberships.Membership(ctx, tenant, registered.ID)
		if err != nil {
			t.Fatalf("reading the membership back: %v", err)
		}
		if !ok || !got.Active || got.Source != SourceInvite {
			t.Errorf("membership = %+v (present %v), want an active invite membership on the pending account", got, ok)
		}

		// The invitation's address verified the registration.
		status, err := verification.SignupStateOf(ctx, registered.ID)
		if err != nil {
			t.Fatalf("reading the signup state back: %v", err)
		}
		if status.State != authn.SignupVerified {
			t.Errorf("the account's signup state is %d, want verified", status.State)
		}
		if status.Email != "newcomer@example.com" {
			t.Errorf("the verified address is %q, want newcomer@example.com", status.Email)
		}
		if status.Since.IsZero() {
			t.Error("the instant the account reached verified was not recorded")
		}
		if n := pendingContacts(t, handle, registered.ID); n != 1 {
			t.Errorf("the account lists %d contact(s), want the proved address's reservation kept", n)
		}
		if n := provedContacts(t, handle, registered.ID); n != 1 {
			t.Errorf("the account has %d proved reservation(s), want the invited address stamped", n)
		}
		if vias := identifierVias(t, handle, registered.ID, "newcomer@example.com"); len(vias) != 1 || vias[0] != VerifiedViaInvite {
			t.Errorf("the email identifier was written through %v, want exactly [%q]", vias, VerifiedViaInvite)
		}
		if n := accountCount(t, handle); n != 1 {
			t.Errorf("the database holds %d accounts, want the one pending account alone", n)
		}
	})
}

// TestASecondPendingOwnerCannotExist holds the reservation's arbitration: a
// second registration of one address is ErrContactTaken, so the reverse lookup
// has one owner and the acceptance merges into it rather than creating a third
// account.
func TestASecondPendingOwnerCannotExist(t *testing.T) {
	withRealAccountProduct(t, func(handle *gorm.DB, signups authn.Signups, admin authn.AccountAdmin,
		accounts authn.Accounts, verification authn.SignupVerification, memberships authn.Memberships,
	) {
		applyMigrations(t, handle)
		p := newRealProduct(t, handle, admin, accounts, verification, memberships)
		ctx := context.Background()

		first, err := signups.Register(ctx, authn.RegisterAccount{
			Username: "first", Email: "clash@example.com", Password: "password123",
		})
		if err != nil {
			t.Fatalf("registering the first pending account: %v", err)
		}
		if _, err = signups.Register(ctx, authn.RegisterAccount{
			Username: "second", Email: "Clash@Example.com", Password: "password123",
		}); !errors.Is(err, authn.ErrContactTaken) {
			t.Fatalf("a second registration of the address gave %v, want ErrContactTaken", err)
		}
		owner, ok, err := verification.PendingAccountByContact(ctx, string(authn.IdentifierEmail), "clash@example.com")
		if err != nil || !ok || owner != first.ID.String() {
			t.Fatalf("PendingAccountByContact = (%q, %v, %v), want the one owner", owner, ok, err)
		}

		issueCtx := authntest.Context(t, ctx, authntest.WithAccount(adminID))
		issued, err := p.Issue(issueCtx, IssueInvite{Tenant: tenant, Email: "clash@example.com"})
		if err != nil {
			t.Fatalf("issuing an invitation for the reserved address: %v", err)
		}
		mem, err := p.Accept(ctx, AcceptInvite{Token: issued.Token})
		if err != nil {
			t.Fatalf("accepting an invitation for the reserved address: %v", err)
		}

		if mem.Account != first.ID {
			t.Fatalf("the accepted membership names account %q, want the owner %q", mem.Account, first.ID)
		}
		if n := accountCount(t, handle); n != 1 {
			t.Errorf("the database holds %d accounts, want the one pending owner", n)
		}
		status, err := verification.SignupStateOf(ctx, first.ID)
		if err != nil || status.State != authn.SignupVerified {
			t.Errorf("the owner reads %+v (%v), want verified by the invitation", status, err)
		}
		if n := provedContacts(t, handle, first.ID); n != 1 {
			t.Errorf("the owner has %d proved reservation(s), want its address kept and stamped", n)
		}
	})
}

// pendingContacts counts the contact reservations an account lists.
func pendingContacts(t *testing.T, handle *gorm.DB, id authn.AccountID) int64 {
	t.Helper()
	var n int64
	if err := handle.Raw("SELECT count(*) FROM authn_pending_contacts WHERE account_id = ?", id.String()).Scan(&n).Error; err != nil {
		t.Fatalf("counting the pending contacts of %q: %v", id, err)
	}
	return n
}

// provedContacts counts the contact reservations an account lists with a proof
// instant.
func provedContacts(t *testing.T, handle *gorm.DB, id authn.AccountID) int64 {
	t.Helper()
	var n int64
	if err := handle.Raw("SELECT count(*) FROM authn_pending_contacts WHERE account_id = ? AND verified_at IS NOT NULL",
		id.String()).Scan(&n).Error; err != nil {
		t.Fatalf("counting the proved contacts of %q: %v", id, err)
	}
	return n
}

// identifierVias reads how the account's identifiers with one email value were
// verified, so that a case can tell the channel they were written through.
func identifierVias(t *testing.T, handle *gorm.DB, id authn.AccountID, value string) []string {
	t.Helper()
	var vias []string
	if err := handle.Raw("SELECT verified_via FROM authn_identifiers WHERE account_id = ? AND kind = ? AND value = ?",
		id.String(), string(authn.IdentifierEmail), value).Scan(&vias).Error; err != nil {
		t.Fatalf("reading the identifiers of %q: %v", id, err)
	}
	return vias
}

// accountCount reads how many account rows the database holds.
func accountCount(t *testing.T, handle *gorm.DB) int64 {
	t.Helper()
	var n int64
	if err := handle.Raw("SELECT count(*) FROM authn_accounts").Scan(&n).Error; err != nil {
		t.Fatalf("counting the accounts: %v", err)
	}
	return n
}
