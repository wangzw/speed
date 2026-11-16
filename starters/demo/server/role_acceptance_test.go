package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/audit/journal"
	"github.com/vislake/speed/pkg/authn"
	authnaccount "github.com/vislake/speed/pkg/authn/account"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/gate"
	"github.com/vislake/speed/pkg/authn/invite"
	authnmount "github.com/vislake/speed/pkg/authn/mount"
	"github.com/vislake/speed/pkg/authn/provisioning/provisioningtest"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/policy"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/sqlite"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/kv/memory"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
)

// roleAcceptanceAssembly is the group of modules one acceptance case drives:
// the invitation module over the real policy and account modules, with the
// database and the two accounts every case starts from.
type roleAcceptanceAssembly struct {
	invitations invite.Invites
	memberships authn.Memberships
	manager     authz.Manager
	admin       authn.AccountAdmin
	database    db.Database
	inviter     authn.Account
	invitee     authn.Account
}

// withRoleAcceptanceAssembly brings up one assembly and runs use at the start
// stage. The timeout is the deadlock detector: the role review reads the policy
// tables before the acceptance's transaction opens, and a review that read from
// inside the transaction would wait on the single connection for the whole
// bound.
func withRoleAcceptanceAssembly(t *testing.T, use func(context.Context, *roleAcceptanceAssembly)) {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "role-acceptance.db")
	body := `{"db":{"sqlite":{"dsn":` + quoteJSON(dsn) + `}},` +
		`"authn":{"gate":{"mount":{"endpoints":{"default":"account","tenants":"api",` +
		`"system":"system"},"public-url":"http://localhost:8080"}}}}`

	mods := []core.Module{
		sqlite.Module(),
		authnaccount.Module(),
		provisioningtest.Module(),
		policy.Module(),
		invite.Module(),
		journal.Module(),
		memory.Module(),
		authnmount.Module(),
		log.Module(),
		gate.Module(),
	}
	reader := authntest.ConfigReaderAll(t, body, mods...)

	reg := core.New()
	reg.Register(config.Module())
	names := make([]string, 0, len(mods)+4)
	for _, m := range mods {
		reg.Register(m)
		names = append(names, m.Name)
	}
	// The gateway module is in the assembly for its configuration declarations
	// alone: the instance handed to it is a no-op, so its own mount checks do not
	// run. The entry point, the path-tenant guard, the tenant description read
	// and the tenant roster read are stand-ins; the point of the case is the
	// policy and account modules.
	reg.Supply(roleAcceptanceGate{}, gate.ModuleName)
	for _, m := range []core.Module{
		roleAcceptanceStandIn("stand-in.role-router", (*speedhttp.Router)(nil), roleAcceptanceRouter{}),
		roleAcceptanceStandIn("stand-in.role-route-guard", (*speedhttp.RouteGuard)(nil), roleAcceptanceRouteGuard{}),
		roleAcceptanceStandIn("stand-in.role-tenants", (*authn.TenantDescriptionResolver)(nil),
			roleAcceptanceTenantDescriptions{}),
		roleAcceptanceStandIn("stand-in.role-roster", (*tenant.Catalog)(nil), roleAcceptanceRoster{}),
		roleAcceptanceMembershipCheckerModule(),
	} {
		reg.Register(m)
		names = append(names, m.Name)
	}
	reg.Supply(reader, config.ModuleName)

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	err := reg.Drive(ctx, core.Drive{
		Depth:        core.StageStart,
		Participants: names,
		Action: func(ctx context.Context, reg *core.Registry) error {
			database, err := core.Resolve[db.Database](reg)
			if err != nil {
				return err
			}
			admin, err := core.Resolve[authn.AccountAdmin](reg)
			if err != nil {
				return err
			}
			memberships, err := core.Resolve[authn.Memberships](reg)
			if err != nil {
				return err
			}
			manager, err := core.Resolve[authz.Manager](reg)
			if err != nil {
				return err
			}
			invitations, err := core.Resolve[invite.Invites](reg)
			if err != nil {
				return err
			}

			inviter, err := admin.Create(ctx, authn.CreateAccount{
				Kind: authn.KindUser, Username: "inviter", DisplayName: "Inviter",
				Email: "inviter@example.com", EmailVerifiedVia: authn.Verification{Via: authn.VerifiedViaEmailCode},
				Via: authn.ReasonAdmin,
			})
			if err != nil {
				return fmt.Errorf("creating the inviter: %w", err)
			}
			invitee, err := admin.Create(ctx, authn.CreateAccount{
				Kind: authn.KindUser, Username: "newcomer", DisplayName: "Newcomer",
				Email: "newcomer@example.com", EmailVerifiedVia: authn.Verification{Via: authn.VerifiedViaEmailCode},
				Via: authn.ReasonAdmin,
			})
			if err != nil {
				return fmt.Errorf("creating the invitee: %w", err)
			}
			if err := memberships.Put(ctx, authn.Membership{
				Account: inviter.ID, Tenant: "acme", Active: true, Source: "admin",
			}); err != nil {
				return fmt.Errorf("making the inviter a member: %w", err)
			}

			// The inviter holds a role with the tenant's role management, and the
			// invited role carries a permission the inviter holds, so the subset
			// rule passes on both the issue and the acceptance.
			if err := manager.PutRole(ctx, authz.RoleRecord{
				Tenant: "acme", Role: "manager", DescriptionKey: "demo.role.manager",
			}); err != nil {
				return fmt.Errorf("creating the inviter's role: %w", err)
			}
			if err := manager.PutGrant(ctx, authz.GrantRecord{
				Tenant: "acme", Role: "manager", Resource: "tenant_role", Action: "manage",
			}); err != nil {
				return fmt.Errorf("granting the inviter role management: %w", err)
			}
			if err := manager.PutBinding(ctx, authz.Binding{
				Tenant: "acme", Account: string(inviter.ID), Role: "manager",
			}); err != nil {
				return fmt.Errorf("binding the inviter: %w", err)
			}
			if err := manager.PutRole(ctx, authz.RoleRecord{
				Tenant: "acme", Role: "editor", DescriptionKey: "demo.role.editor",
			}); err != nil {
				return fmt.Errorf("creating the invited role: %w", err)
			}
			if err := manager.PutGrant(ctx, authz.GrantRecord{
				Tenant: "acme", Role: "editor", Resource: "tenant_role", Action: "manage",
			}); err != nil {
				return fmt.Errorf("granting the invited role: %w", err)
			}

			use(ctx, &roleAcceptanceAssembly{
				invitations: invitations,
				memberships: memberships,
				manager:     manager,
				admin:       admin,
				database:    database,
				inviter:     inviter,
				invitee:     invitee,
			})
			return nil
		},
	})
	if err != nil {
		t.Fatalf("driving the role acceptance assembly: %v", err)
	}
}

// oneConnection holds the database to a single connection from here on, so an
// acceptance that reads the policy tables from inside its transaction waits on
// itself until the case's timeout.
func oneConnection(t *testing.T, database db.Database) {
	t.Helper()
	sqlDB, err := database.DB().DB()
	if err != nil {
		t.Fatalf("taking the connection pool: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
}

// TestTheRoleAcceptanceAssemblyAcceptsARoleThroughTheRealPolicyAndAccountModules holds
// the success path: a member who holds role management issues an invitation
// that carries a role within their own held set, and the invited account
// accepts it, ending with the membership and the binding written by the real
// account and policy modules in one transaction.
func TestTheRoleAcceptanceAssemblyAcceptsARoleThroughTheRealPolicyAndAccountModules(t *testing.T) {
	t.Parallel()
	withRoleAcceptanceAssembly(t, func(ctx context.Context, a *roleAcceptanceAssembly) {
		issueCtx := authntest.Context(t, ctx, authntest.WithAccount(a.inviter.ID))
		issued, err := a.invitations.Issue(issueCtx, invite.IssueInvite{
			Tenant: "acme", Email: "newcomer@example.com", Role: "editor",
		})
		if err != nil {
			t.Fatalf("issuing the invitation: %v", err)
		}

		oneConnection(t, a.database)
		acceptCtx := authntest.Context(t, ctx, authntest.WithAccount(a.invitee.ID))
		accepted, err := a.invitations.Accept(acceptCtx, invite.AcceptInvite{Token: issued.Token})
		if err != nil {
			t.Fatalf("accepting the invitation: %v", err)
		}
		if accepted.Role != "editor" {
			t.Errorf("the acceptance reported role %q, want editor", accepted.Role)
		}

		membership, ok, err := a.memberships.Membership(ctx, "acme", a.invitee.ID)
		if err != nil {
			t.Fatalf("reading the membership back: %v", err)
		}
		if !ok || !membership.Active || membership.Source != invite.SourceInvite {
			t.Errorf("membership = %+v (%t), want an active invite membership", membership, ok)
		}
		if bindings := bindingsOf(t, a, a.invitee.ID); !containsRole(bindings, "editor") {
			t.Errorf("the invitee's bindings = %+v, want the invited editor role", bindings)
		}
	})
}

// TestTheRoleAcceptanceAssemblyRefusesAnInvitationWhoseInviterLeftTheTenant holds the
// acceptance review over the real policy module: the inviter's membership is
// suspended after the invitation was issued, their binding and held set stay
// as they were, and the acceptance must still refuse to bind the role. The
// invitation is revoked and neither the membership nor the binding is written.
func TestTheRoleAcceptanceAssemblyRefusesAnInvitationWhoseInviterLeftTheTenant(t *testing.T) {
	t.Parallel()
	withRoleAcceptanceAssembly(t, func(ctx context.Context, a *roleAcceptanceAssembly) {
		issueCtx := authntest.Context(t, ctx, authntest.WithAccount(a.inviter.ID))
		issued, err := a.invitations.Issue(issueCtx, invite.IssueInvite{
			Tenant: "acme", Email: "newcomer@example.com", Role: "editor",
		})
		if err != nil {
			t.Fatalf("issuing the invitation: %v", err)
		}
		// The suspension leaves the policy binding in place, which is why the
		// held set alone cannot judge the inviter's standing.
		if putErr := a.memberships.Put(ctx, authn.Membership{
			Account: a.inviter.ID, Tenant: "acme", Active: false, Source: "admin",
		}); putErr != nil {
			t.Fatalf("suspending the inviter's membership: %v", putErr)
		}

		oneConnection(t, a.database)
		acceptCtx := authntest.Context(t, ctx, authntest.WithAccount(a.invitee.ID))
		if _, acceptErr := a.invitations.Accept(acceptCtx, invite.AcceptInvite{Token: issued.Token}); !errors.Is(acceptErr, invite.ErrInvitationRoleWithdrawn) {
			t.Fatalf("accepting after the inviter left gave %v, want ErrInvitationRoleWithdrawn", acceptErr)
		}

		if _, ok, memberErr := a.memberships.Membership(ctx, "acme", a.invitee.ID); memberErr != nil || ok {
			t.Errorf("the refused acceptance wrote a membership: ok=%t, err=%v", ok, memberErr)
		}
		if bindings := bindingsOf(t, a, a.invitee.ID); len(bindings) != 0 {
			t.Errorf("the refused acceptance wrote bindings %+v, want none", bindings)
		}
		list, _, err := a.invitations.List(ctx, "acme", authn.Page{})
		if err != nil {
			t.Fatalf("listing the invitations: %v", err)
		}
		if len(list) != 1 || list[0].Status != invite.InvitationRevoked {
			t.Errorf("the invitation reads %+v, want the one row revoked", list)
		}
	})
}

// TestTheRoleAcceptanceAssemblyRefusesAnInvitationWhoseInviterAccountIsDisabled
// holds the acceptance review against an account-level disable: disabling the
// inviter's account leaves the membership and the policy bindings in place, so
// the member and held-set checks still pass. The acceptance must read the
// inviter's account and refuse the role anyway. The invitation is revoked and
// neither the membership nor the binding is written.
func TestTheRoleAcceptanceAssemblyRefusesAnInvitationWhoseInviterAccountIsDisabled(t *testing.T) {
	t.Parallel()
	withRoleAcceptanceAssembly(t, func(ctx context.Context, a *roleAcceptanceAssembly) {
		issueCtx := authntest.Context(t, ctx, authntest.WithAccount(a.inviter.ID))
		issued, err := a.invitations.Issue(issueCtx, invite.IssueInvite{
			Tenant: "acme", Email: "newcomer@example.com", Role: "editor",
		})
		if err != nil {
			t.Fatalf("issuing the invitation: %v", err)
		}
		// A disabled account keeps its memberships and its policy bindings, so
		// nothing the policy module reads has changed.
		if statusErr := a.admin.SetStatus(ctx, a.inviter.ID, authn.StatusDisabled, authn.ReasonAdmin); statusErr != nil {
			t.Fatalf("disabling the inviter's account: %v", statusErr)
		}

		oneConnection(t, a.database)
		acceptCtx := authntest.Context(t, ctx, authntest.WithAccount(a.invitee.ID))
		if _, acceptErr := a.invitations.Accept(acceptCtx, invite.AcceptInvite{Token: issued.Token}); !errors.Is(acceptErr, invite.ErrInvitationRoleWithdrawn) {
			t.Fatalf("accepting after the inviter's account was disabled gave %v, want ErrInvitationRoleWithdrawn", acceptErr)
		}

		if _, ok, memberErr := a.memberships.Membership(ctx, "acme", a.invitee.ID); memberErr != nil || ok {
			t.Errorf("the refused acceptance wrote a membership: ok=%t, err=%v", ok, memberErr)
		}
		if bindings := bindingsOf(t, a, a.invitee.ID); len(bindings) != 0 {
			t.Errorf("the refused acceptance wrote bindings %+v, want none", bindings)
		}
		list, _, err := a.invitations.List(ctx, "acme", authn.Page{})
		if err != nil {
			t.Fatalf("listing the invitations: %v", err)
		}
		if len(list) != 1 || list[0].Status != invite.InvitationRevoked {
			t.Errorf("the invitation reads %+v, want the one row revoked", list)
		}
	})
}

// bindingsOf reads one account's bindings in the tenant.
func bindingsOf(t *testing.T, a *roleAcceptanceAssembly, id authn.AccountID) []authz.Binding {
	t.Helper()
	bindings, err := a.manager.BindingsOf(t.Context(), "acme", string(id))
	if err != nil {
		t.Fatalf("reading the bindings back: %v", err)
	}
	return bindings
}

// containsRole reports whether the bindings include the role.
func containsRole(bindings []authz.Binding, role authz.Role) bool {
	for _, b := range bindings {
		if b.Role == role {
			return true
		}
	}
	return false
}

// quoteJSON renders a string as a JSON string literal.
func quoteJSON(value string) string { return fmt.Sprintf("%q", value) }

// roleAcceptanceStandIn is a module that declares one capability token for an
// instance the case supplies, so the registry resolves it without the real
// implementation.
func roleAcceptanceStandIn(name string, token core.Token, instance any) core.Module {
	return core.Module{
		Name:     name,
		Provides: []core.Provision{{Token: token, Exclusive: true}},
		New:      func(context.Context, *core.Registry) (any, error) { return instance, nil },
	}
}

// roleAcceptanceMembershipCheckerModule answers the policy module's membership
// question from the real membership store, which is the seam the acceptance
// review asks whether the inviter is still an active member.
func roleAcceptanceMembershipCheckerModule() core.Module {
	return core.Module{
		Name:     "stand-in.role-membership-checker",
		Requires: []core.Requirement{{Token: (*authn.Memberships)(nil)}},
		Provides: []core.Provision{{Token: (*authz.MembershipChecker)(nil), Exclusive: true}},
		New: func(_ context.Context, reg *core.Registry) (any, error) {
			memberships, err := core.Resolve[authn.Memberships](reg)
			if err != nil {
				return nil, err
			}
			return roleAcceptanceMembershipChecker{reader: memberships}, nil
		},
	}
}

// roleAcceptanceMembershipChecker is the membership seam over the real store.
type roleAcceptanceMembershipChecker struct{ reader authn.Memberships }

func (c roleAcceptanceMembershipChecker) IsMember(ctx context.Context, tenant, account string) (bool, error) {
	id, err := authn.ParseAccountID(account)
	if err != nil {
		return false, nil
	}
	return c.reader.IsMember(ctx, id, tenant)
}

// roleAcceptanceRouter declares the endpoints the routes are mounted on and
// binds nothing: this case drives the modules' interfaces, not their handlers.
type roleAcceptanceRouter struct{}

func (roleAcceptanceRouter) Endpoints() []string { return []string{"account", "api", "system"} }

func (roleAcceptanceRouter) Endpoint(name string) (speedhttp.Endpoint, error) {
	switch name {
	case "account", "api", "system":
		return roleAcceptanceEndpoint{}, nil
	}
	return nil, fmt.Errorf("%w: %q", speedhttp.ErrUnknownEndpoint, name)
}

// roleAcceptanceEndpoint accepts registrations without binding a route.
type roleAcceptanceEndpoint struct{}

func (roleAcceptanceEndpoint) Route(string, string, ...gin.HandlerFunc) {}
func (roleAcceptanceEndpoint) Use(speedhttp.Middleware)                 {}
func (roleAcceptanceEndpoint) Accepting() bool                          { return true }

// roleAcceptanceGate is the gateway the mount module registers its access
// levels on. This case makes no request, so every registration is a no-op.
type roleAcceptanceGate struct{}

func (roleAcceptanceGate) Exempt(string, string, string)       {}
func (roleAcceptanceGate) Optional(string, string, string)     {}
func (roleAcceptanceGate) Impersonable(string, string, string) {}

// roleAcceptanceRouteGuard admits every request to the tenant-scoped groups.
type roleAcceptanceRouteGuard struct{}

func (roleAcceptanceRouteGuard) Guard(next gin.HandlerFunc) gin.HandlerFunc { return next }

// roleAcceptanceTenantDescriptions answers every tenant as an ordinary one, so
// the invitation module can name it and does not treat it as the system tenant.
type roleAcceptanceTenantDescriptions struct{}

func (roleAcceptanceTenantDescriptions) TenantDescriptionOf(_ context.Context, id string) (authn.TenantDescription, error) {
	return authn.TenantDescription{Name: id, DisplayName: id}, nil
}

// roleAcceptanceRoster is the tenant roster stand-in the policy module starts
// over. It carries the platform domain, which the startup check requires, and
// the working tenant, whose owner column is empty, so an account is judged to
// own no organization.
type roleAcceptanceRoster struct{ tenant.Catalog }

func (roleAcceptanceRoster) Get(_ context.Context, id tenant.ID) (tenant.Tenant, bool, error) {
	for _, t := range roleAcceptanceTenants {
		if t.ID == id {
			return t, true, nil
		}
	}
	return tenant.Tenant{}, false, nil
}

func (roleAcceptanceRoster) List(context.Context) ([]tenant.Tenant, error) {
	return roleAcceptanceTenants, nil
}

var roleAcceptanceTenants = []tenant.Tenant{
	{ID: tenant.ID(authz.PlatformDomain), Name: string(authz.PlatformDomain), Status: tenant.StatusActive},
	{ID: tenant.ID("acme"), Name: "acme", Status: tenant.StatusActive},
}
