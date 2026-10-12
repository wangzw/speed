package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	nethttp "net/http"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
)

// membersModuleName is the name the members module is registered under.
const membersModuleName = "members"

// memberRoleTable is the table the scoped role model maps to.
const memberRoleTable = "member_roles"

// The routes. The two on the API endpoint are scoped to the tenant the request
// names; the one on the account endpoint answers across tenants, which is what
// it is for.
const (
	listMembersRoute = "GET /members"
	grantMemberRoute = "POST /members"
	meRoute          = "GET /me"
)

// The four roles. A role is a column value rather than a type of its own, and
// the migration's CHECK holds the same four.
const (
	roleOwner  = "owner"
	roleAdmin  = "admin"
	roleRep    = "rep"
	roleViewer = "viewer"
)

// roles lists them for validation and for the error text that names them.
var roles = []string{roleOwner, roleAdmin, roleRep, roleViewer}

// validRole reports whether a value is one of the four roles.
func validRole(role string) bool { return slices.Contains(roles, role) }

var (
	errUnknownRole = errors.New("the role is not one of " + strings.Join(roles, ", "))
	errNoEmail     = errors.New("the email is empty")
)

// errOwnerNotMember is the refusal of an owner field naming an account the
// tenant does not count among its members.
var errOwnerNotMember = errors.New("the owner is not a member of this tenant")

// normaliseEmail is the one form an email is looked up in: the account store
// normalizes an identifier the same way.
func normaliseEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

//go:embed openapi/members-api.json
var membersAPIOpenAPI []byte

//go:embed openapi/members-account.json
var membersAccountOpenAPI []byte

//go:embed migrations/members
var membersMigrationRoot embed.FS

var membersMigrations = subtree(membersMigrationRoot, "migrations/members")

// MemberRole is a scoped model: it embeds the convention, which is both the
// declaration and the column. The statements this module issues on it with a
// request's context name no tenant; the filter puts the condition there.
//
// Which accounts belong to a tenant is the account module's to say, through
// authn.Memberships; this table holds only the role an account has inside a
// tenant. A member without a row here has no role, which reads as below every
// role the routes take.
type MemberRole struct {
	tenant.Model
	AccountID string    `gorm:"column:account_id;primaryKey"`
	Role      string    `gorm:"column:role;not null"`
	GrantedAt time.Time `gorm:"column:granted_at;not null"`
}

// TableName fixes the table this model maps to.
func (MemberRole) TableName() string { return memberRoleTable }

// grantRole gives an account a role in a tenant, or rewrites the role it
// already has there, through the authorization layer's policy manager. The
// membership relation itself was written through authn.Memberships before this
// runs; this writes the role, which is a policy row now rather than a row of any
// table of this host's.
//
// It ensures the tenant's four roles exist first — re-issuing the matrix only
// when a role is missing, so a grant on an already-provisioned tenant writes no
// grant row and does not reload the policy — and then sets the account's role.
// The account holds
// one role in a tenant, so a grant rewrites it: any other role the account is
// bound to in the tenant is removed before the new binding is written. The
// binding is a g policy row whose domain is the tenant the caller names, never a
// constant.
func grantRole(ctx context.Context, mgr authz.Manager, id tenant.ID, account authn.AccountID, role string) (authz.Binding, error) {
	if !validRole(role) {
		return authz.Binding{}, errUnknownRole
	}
	provisioned, err := tenantRolesProvisioned(ctx, mgr, string(id))
	if err != nil {
		return authz.Binding{}, fmt.Errorf("reading the tenant's roles: %w", err)
	}
	if !provisioned {
		if err = provisionTenantRoles(ctx, mgr, string(id)); err != nil {
			return authz.Binding{}, fmt.Errorf("provisioning the tenant's roles: %w", err)
		}
	}
	existing, err := mgr.BindingsOf(ctx, string(id), string(account))
	if err != nil {
		return authz.Binding{}, fmt.Errorf("reading the account's roles: %w", err)
	}
	for _, held := range existing {
		if err := mgr.DeleteBinding(ctx, string(id), string(account), held.Role); err != nil {
			return authz.Binding{}, fmt.Errorf("removing the account's %s role: %w", held.Role, err)
		}
	}
	binding := authz.Binding{Tenant: string(id), Account: string(account), Role: authz.Role(role)}
	if err := mgr.PutBinding(ctx, binding); err != nil {
		return authz.Binding{}, fmt.Errorf("writing the member role: %w", err)
	}
	return binding, nil
}

// grantHere gives an account a role in the tenant a request's context names.
// The tenant comes from the request itself: the caller passes the identifier
// the request resolved to.
func grantHere(ctx context.Context, mgr authz.Manager, id tenant.ID, account authn.AccountID, role string) (authz.Binding, error) {
	return grantRole(ctx, mgr, id, account, role)
}

// grantIn gives an account a role in a named tenant, from outside any request
// scoped to that tenant: the admin endpoint and the command line. The tenant
// comes from a roster read the caller made, never from a constant.
func grantIn(ctx context.Context, mgr authz.Manager, id tenant.ID, account authn.AccountID, role string) (authz.Binding, error) {
	return grantRole(ctx, mgr, id, account, role)
}

// memberBindingObject names a grant for the audit seam: the tenant and the
// account the binding is for, under a prefix that says what was written.
func memberBindingObject(id tenant.ID, account authn.AccountID) string {
	return "bindings/" + string(id) + "/" + string(account)
}

// member is what a handler reads about the caller: which account is asking, in
// which tenant. The account is the CRM principal and is what a handler stores
// wherever a row records who acted. It carries no role: a route's permission is
// decided by the authorization layer's middleware, not by a role a handler
// reads.
type member struct {
	Account  authn.AccountID
	TenantID tenant.ID
}

// memberOf reads the caller the request stands for: its identity and tenant
// from the request's context. It does not read the caller's role: a route's
// permission is decided by the authorization layer's middleware before the
// handler runs, not by a role the handler reads. The membership check itself is
// the admission layer's; this carries only the account a handler records and
// the tenant it acts in.
func memberOf(c *gin.Context, route string) (member, bool) {
	ctx := c.Request.Context()
	caller, ok := authn.FromContext(ctx)
	if !ok {
		log.FromContext(ctx).Error("a member route was reached with no identity", "route", route)
		writeError(c, nethttp.StatusInternalServerError, "the request carries no identity")
		return member{}, false
	}
	id, ok := tenant.FromContext(ctx)
	if !ok {
		log.FromContext(ctx).Error("a member route was reached with no tenant", "route", route)
		writeError(c, nethttp.StatusInternalServerError, "the request carries no tenant")
		return member{}, false
	}
	return member{Account: caller.Account(), TenantID: id}, true
}

// isMember reports whether an account belongs to the named tenant, through the
// account module's membership store. It is the check the owner fields of the
// scoped models go through: an owner has to be a member of the tenant that owns
// the row.
func isMember(ctx context.Context, memberships authn.Memberships, account authn.AccountID, id tenant.ID) (bool, error) {
	return memberships.IsMember(ctx, account, string(id))
}

// roleRank orders the four tenant roles by how much they carry, each able to do
// what the ones below it do, so a member bound to more than one is shown under
// the highest. It is a display ordering only: what a role may do is its grant
// rows in the tenant's policy, decided by the authorization middleware, not this
// rank.
func roleRank(role string) int {
	switch role {
	case roleOwner:
		return 3
	case roleAdmin:
		return 2
	case roleRep:
		return 1
	case roleViewer:
		return 0
	default:
		return -1
	}
}

// memberLine is what the members module stands for: it holds the tenant's role
// table, so a module whose tables reference it can declare a requirement on it.
// It is a token only — nothing resolves it.
type memberLine interface{ memberLine() }

// membersProduct is the members module's product.
type membersProduct struct {
	released
	memberships authn.Memberships
	accounts    authn.Accounts
	roster      tenant.Catalog
	mgr         authz.Manager
}

// memberLine marks the product as the members module's.
func (*membersProduct) memberLine() {}

// memberView is one membership as the API shows it.
type memberView struct {
	AccountID   string `json:"account_id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
}

// memberList is what the listing answers with.
type memberList struct {
	Tenant  string       `json:"tenant"`
	Caller  string       `json:"caller"`
	Role    string       `json:"role"`
	Members []memberView `json:"members"`
}

// displayOf reads what the members listing shows for one account: its display
// name, and the email among its identifiers. An account that is gone is shown by
// its identifier alone.
func (p *membersProduct) displayOf(ctx context.Context, id authn.AccountID) (email, name string) {
	account, err := p.accounts.Get(ctx, id)
	if err != nil {
		return "", ""
	}
	name = account.DisplayName
	identifiers, err := p.accounts.Identifiers(ctx, id)
	if err != nil {
		return "", name
	}
	for _, ident := range identifiers {
		if ident.Kind == authn.IdentifierEmail {
			return ident.Value, name
		}
	}
	return "", name
}

// listMembers answers the tenant's membership list. It reads the members through
// authn.Memberships and each member's role from the tenant's policy; a member
// with no binding is shown with an empty role. The caller's own role is read the
// same way.
func (p *membersProduct) listMembers(c *gin.Context) {
	ctx := c.Request.Context()
	self, ok := memberOf(c, listMembersRoute)
	if !ok {
		return
	}
	rows, err := p.memberships.Members(ctx, string(self.TenantID), authn.Page{})
	if err != nil {
		log.FromContext(ctx).Error("reading the memberships", "error", err)
		writeError(c, nethttp.StatusServiceUnavailable, "the membership table cannot be read")
		return
	}
	roles, err := p.rolesByAccount(ctx, self.TenantID)
	if err != nil {
		log.FromContext(ctx).Error("reading the member roles", "error", err)
		writeError(c, nethttp.StatusServiceUnavailable, "the member role table cannot be read")
		return
	}
	callerRole, err := p.roleOf(ctx, self.TenantID, self.Account)
	if err != nil {
		log.FromContext(ctx).Error("reading the caller's role", "error", err)
		writeError(c, nethttp.StatusServiceUnavailable, "the member role table cannot be read")
		return
	}
	out := memberList{Tenant: string(self.TenantID), Caller: string(self.Account), Role: callerRole, Members: []memberView{}}
	for _, m := range rows {
		email, name := p.displayOf(ctx, m.Account)
		out.Members = append(out.Members, memberView{
			AccountID: string(m.Account), Email: email, DisplayName: name, Role: roles[string(m.Account)],
		})
	}
	c.JSON(nethttp.StatusOK, out)
}

// rolesByAccount reads every binding the tenant's policy holds, keyed by
// account and reduced to the highest role each account carries. An account bound
// to more than one role is shown under the highest.
func (p *membersProduct) rolesByAccount(ctx context.Context, id tenant.ID) (map[string]string, error) {
	bindings, err := p.mgr.Bindings(ctx, string(id))
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(bindings))
	for _, b := range bindings {
		if roleRank(string(b.Role)) > roleRank(out[b.Account]) {
			out[b.Account] = string(b.Role)
		}
	}
	return out, nil
}

// roleOf reads the highest role an account holds in a tenant from the tenant's
// policy. An account bound to no role reads as the empty string, which ranks
// below every role.
func (p *membersProduct) roleOf(ctx context.Context, id tenant.ID, account authn.AccountID) (string, error) {
	bindings, err := p.mgr.BindingsOf(ctx, string(id), string(account))
	if err != nil {
		return "", err
	}
	best := ""
	for _, b := range bindings {
		if roleRank(string(b.Role)) > roleRank(best) {
			best = string(b.Role)
		}
	}
	return best, nil
}

// grantRequest is the body of a membership grant.
type grantRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

// Validate refuses a grant that names no account or no known role.
func (g grantRequest) Validate() error {
	switch {
	case strings.TrimSpace(g.Email) == "":
		return errNoEmail
	case !validRole(g.Role):
		return errUnknownRole
	}
	return nil
}

// grantView is a membership as a grant answers with it.
type grantView struct {
	TenantID  string `json:"tenant_id"`
	AccountID string `json:"account_id"`
	Role      string `json:"role"`
}

// grantMember gives an existing account a role in the caller's tenant. The
// caller's right to do so is the route's declared permission, decided by the
// authorization layer's middleware before this handler runs. It writes the
// membership relation through authn.Memberships and the role into the tenant's
// policy.
func (p *membersProduct) grantMember(c *gin.Context) {
	ctx := c.Request.Context()
	self, ok := memberOf(c, grantMemberRoute)
	if !ok {
		return
	}
	var body grantRequest
	if !bindBody(c, &body) {
		return
	}
	account, found, err := p.accountByEmail(ctx, body.Email)
	if err != nil {
		log.FromContext(ctx).Error("reading the account to grant", "error", err)
		writeError(c, nethttp.StatusServiceUnavailable, "the account store cannot be read")
		return
	}
	if !found {
		writeError(c, nethttp.StatusNotFound, "no account has this email")
		return
	}
	putErr := p.memberships.Put(ctx, authn.Membership{
		Account: account, Tenant: string(self.TenantID), Active: true, Source: "admin",
	})
	if putErr != nil {
		log.FromContext(ctx).Error("granting a membership", "error", putErr)
		writeError(c, nethttp.StatusInternalServerError, "the membership could not be written")
		return
	}
	granted, err := grantHere(ctx, p.mgr, self.TenantID, account, body.Role)
	if err != nil {
		log.FromContext(ctx).Error("granting a membership", "error", err)
		writeError(c, nethttp.StatusInternalServerError, "the membership could not be written")
		return
	}
	c.JSON(nethttp.StatusOK, grantView{
		TenantID: granted.Tenant, AccountID: granted.Account, Role: string(granted.Role),
	})
}

// accountByEmail finds the account an address is bound to, through the account
// module's look-up. The address is normalized there.
func (p *membersProduct) accountByEmail(ctx context.Context, email string) (authn.AccountID, bool, error) {
	id, err := p.accounts.Lookup(ctx, authn.IdentifierEmail, email)
	if errors.Is(err, authn.ErrAccountNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

// meTenant is one tenant the caller belongs to.
type meTenant struct {
	ID     string `json:"id"`
	Alias  string `json:"alias"`
	Status string `json:"status"`
	Role   string `json:"role"`
}

// meAccount is the account behind the caller.
type meAccount struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
}

// meView is what the account endpoint answers GET /me with.
type meView struct {
	Identity string     `json:"identity"`
	Account  *meAccount `json:"account"`
	Tenants  []meTenant `json:"tenants"`
}

// me answers who the caller is and which tenants they belong to.
//
// The membership read is the one statement in this host that crosses tenants
// from inside a request, and it does so through tenant.CrossTenant, which the
// tenant design sanctions for exactly this: a question whose answer spans
// tenants by definition. The account endpoint resolves no tenant, so a scoped
// statement here would be refused for having none. The only condition is the
// caller's own account identifier, taken from the identity the gateway
// established rather than from anything the request says.
func (p *membersProduct) me(c *gin.Context) {
	ctx := c.Request.Context()
	caller, ok := authn.FromContext(ctx)
	if !ok {
		log.FromContext(ctx).Error("a guarded route was reached with no identity", "route", meRoute)
		writeError(c, nethttp.StatusInternalServerError, "the request carries no identity")
		return
	}
	out := meView{Identity: string(caller.Account()), Tenants: []meTenant{}}
	account := caller.Account()
	if account == "" || account.IsStatic() {
		// An operator is a caller with no account in the library and no tenant.
		c.JSON(nethttp.StatusOK, out)
		return
	}
	if record, err := p.accounts.Get(ctx, account); err == nil {
		email := ""
		if identifiers, err := p.accounts.Identifiers(ctx, account); err == nil {
			for _, ident := range identifiers {
				if ident.Kind == authn.IdentifierEmail {
					email = ident.Value
					break
				}
			}
		}
		out.Account = &meAccount{ID: string(account), Email: email, DisplayName: record.DisplayName}
	}
	rows, err := p.memberships.Tenants(ctx, account)
	if err != nil {
		log.FromContext(ctx).Error("reading the caller's memberships", "error", err)
		writeError(c, nethttp.StatusServiceUnavailable, "the membership table cannot be read")
		return
	}
	for _, m := range rows {
		t, known, err := p.roster.Get(ctx, tenant.ID(m.Tenant))
		if err != nil {
			log.FromContext(ctx).Error("reading the roster", "error", err)
			writeError(c, nethttp.StatusServiceUnavailable, "the roster cannot be read")
			return
		}
		if !known {
			continue
		}
		role, err := p.roleOf(tenant.CrossTenant(ctx), t.ID, account)
		if err != nil {
			log.FromContext(ctx).Error("reading the caller's role", "error", err)
			writeError(c, nethttp.StatusServiceUnavailable, "the member role table cannot be read")
			return
		}
		out.Tenants = append(out.Tenants, meTenant{
			ID: string(t.ID), Alias: t.Alias, Status: t.Status.String(), Role: role,
		})
	}
	c.JSON(nethttp.StatusOK, out)
}

// declareMemberRoutes records, on the authorization layer's declaration table,
// the permission each members route needs. The enforcement middleware on the
// authorizing stage reads that table and decides a route before its handler
// runs, so a handler no longer reads the caller's role itself. Reading the list
// needs crm.member.read; granting a membership needs crm.member.manage, the
// permission the four tenant roles above a rep carry.
func declareMemberRoutes(gate authz.Gate) {
	require := func(route string, perm authz.Permission) {
		method, path := splitRoute(route)
		gate.Require(method, path, perm)
	}
	require(listMembersRoute, permMemberRead)
	require(grantMemberRoute, permMemberManage)
}

// init registers the members module. It runs on serve: its routes sit on the API
// and account endpoints. It lists no model: its handlers issue no statement of
// their own, reading the membership relation through authn.Memberships, the
// account store through authn.Accounts and the role through authz.Manager. The
// member_roles table it once wrote no longer appears among the tables its code
// reaches; the role-import command declares that model alone.
func init() {
	registerApplication(application{
		module:     membersModule(),
		runsOn:     []string{serveCommand},
		journalled: []string{listMembersRoute, grantMemberRoute, meRoute},
	})
}

// membersModule declares the member role table, its routes and the permissions
// those routes need.
//
// The membership relation itself is the account module's, read and written
// through authn.Memberships; the role a tenant gives one of its accounts is a
// binding in the tenant's policy, read and written through authz.Manager. The
// admission layer (tenant.admission) holds the membership check that a caller
// named here belongs to the tenant; that layer, and not a layer of this module,
// is what refuses a stranger.
//
// It requires the manager, since every members route reads or writes the
// tenant's policy, and it takes up the declaration gate when a command carries
// one, to record the permission its two API routes need. The roster is still
// required: the account endpoint's GET /me reads it to name the tenants the
// caller belongs to. The legacy member_roles table this module migrated is left
// in place; the role is a policy row now, and the old table is kept until the
// host confirms the migration.
func membersModule() core.Module {
	return core.Module{
		Name: membersModuleName,
		Requires: []core.Requirement{
			{Token: (*log.Logger)(nil)},
			{Token: (*tenant.Catalog)(nil)},
			{Token: (*authn.Memberships)(nil)},
			{Token: (*authn.Accounts)(nil)},
			{Token: (*authz.Manager)(nil)},
			{Token: (*authz.Gate)(nil), Optional: true},
		},
		Provides: []core.Provision{
			{Token: (*memberLine)(nil), Exclusive: true},
		},
		Resources: []any{
			db.Migrations{FS: membersMigrations},
			speedhttp.Spec{Endpoint: apiEndpoint, Document: membersAPIOpenAPI},
			speedhttp.Spec{Endpoint: accountEndpoint, Document: membersAccountOpenAPI},
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			logger, err := namedLogger(reg, membersModuleName)
			if err != nil {
				return nil, err
			}
			roster, err := core.Resolve[tenant.Catalog](reg)
			if err != nil {
				return nil, err
			}
			memberships, err := core.Resolve[authn.Memberships](reg)
			if err != nil {
				return nil, err
			}
			accounts, err := core.Resolve[authn.Accounts](reg)
			if err != nil {
				return nil, err
			}
			mgr, err := core.Resolve[authz.Manager](reg)
			if err != nil {
				return nil, err
			}
			return &membersProduct{
				released: released{log: logger}, memberships: memberships,
				accounts: accounts, roster: roster, mgr: mgr,
			}, nil
		},

		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			product, ok := instance.(*membersProduct)
			if !ok {
				return errForeignInstance(membersModuleName, instance)
			}
			router, err := core.Resolve[speedhttp.Router](reg)
			if err != nil {
				return err
			}
			api, err := router.Endpoint(apiEndpoint)
			if err != nil {
				return err
			}
			account, err := router.Endpoint(accountEndpoint)
			if err != nil {
				return err
			}
			if gate, gateErr := core.Resolve[authz.Gate](reg); gateErr == nil {
				declareMemberRoutes(gate)
			} else if !errors.Is(gateErr, core.ErrMissingProvider) {
				return gateErr
			}
			mount(api, listMembersRoute, product.listMembers)
			mount(api, grantMemberRoute, product.grantMember)
			mount(account, meRoute, product.me)
			return nil
		},

		Stop:  recordStop,
		Close: recordClose,
	}
}
