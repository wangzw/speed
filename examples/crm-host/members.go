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
	"gorm.io/gorm/clause"

	"github.com/vislake/speed/pkg/authn"
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

// roleHere reads the caller's role in the tenant the request's context names.
// The statement names the account and nothing else; the filter scopes it. A
// caller with no row here has no role, which is the empty string.
func roleHere(ctx context.Context, database db.Database, account authn.AccountID) (string, error) {
	var found []MemberRole
	err := database.DB().WithContext(ctx).Where("account_id = ?", string(account)).Limit(1).Find(&found).Error
	if err != nil || len(found) == 0 {
		return "", err
	}
	return found[0].Role, nil
}

// roleIn reads an account's role in a named tenant, from outside any request
// scoped to that tenant: the account endpoint's GET /me. The caller hands in a
// cross-tenant context, so the row names its tenant itself.
func roleIn(ctx context.Context, database db.Database, id tenant.ID, account authn.AccountID) (string, error) {
	var found []MemberRole
	err := database.DB().WithContext(ctx).
		Where("tenant_id = ? AND account_id = ?", string(id), string(account)).Limit(1).Find(&found).Error
	if err != nil || len(found) == 0 {
		return "", err
	}
	return found[0].Role, nil
}

// grantHere gives an account a role in the tenant a request's context names, or
// changes the role it already has there. The membership relation was written
// through authn.Memberships before this runs; this writes the role the tenant's
// own layer holds.
//
// The row it writes names no tenant. The tenant half of the key is the filter's
// to fill, from the context, before the statement is bound. "Insert it, or
// rewrite the one there" is an upsert by the primary key.
func grantHere(ctx context.Context, database db.Database, account authn.AccountID, role string) (MemberRole, error) {
	if !validRole(role) {
		return MemberRole{}, errUnknownRole
	}
	m := MemberRole{AccountID: string(account), Role: role, GrantedAt: time.Now().UTC()}
	if err := database.DB().WithContext(ctx).Clauses(clause.OnConflict{UpdateAll: true}).Create(&m).Error; err != nil {
		return MemberRole{}, fmt.Errorf("writing the member role: %w", err)
	}
	return m, nil
}

// grantIn gives an account a role in a named tenant, from outside any request
// scoped to that tenant: the admin endpoint and the command line.
//
// The caller hands in a cross-tenant context, and under it the filter neither
// adds a condition nor fills the column, so the row names its tenant itself. The
// tenant comes from a roster read the caller made, never from a constant.
func grantIn(ctx context.Context, database db.Database, id tenant.ID, account authn.AccountID, role string) (MemberRole, error) {
	if !validRole(role) {
		return MemberRole{}, errUnknownRole
	}
	m := MemberRole{
		Model:     tenant.Model{TenantID: id},
		AccountID: string(account),
		Role:      role,
		GrantedAt: time.Now().UTC(),
	}
	if err := database.DB().WithContext(ctx).Save(&m).Error; err != nil {
		return MemberRole{}, fmt.Errorf("writing the member role: %w", err)
	}
	return m, nil
}

// member is what a handler reads about the caller: which account is asking, in
// which tenant, in which role. The account is the CRM principal and is what a
// handler stores wherever a row records who acted.
type member struct {
	Account  authn.AccountID
	TenantID tenant.ID
	Role     string
}

// memberOf reads the caller the request stands for: its identity and tenant from
// the request's context, and its role from the tenant's own table. The membership
// check itself is the admission layer's; this reads the role admission does not
// know.
func memberOf(c *gin.Context, database db.Database, route string) (member, bool) {
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
	role, err := roleHere(ctx, database, caller.Account())
	if err != nil {
		log.FromContext(ctx).Error("reading the caller's role", "error", err)
		writeError(c, nethttp.StatusServiceUnavailable, "the member role table cannot be read")
		return member{}, false
	}
	return member{Account: caller.Account(), TenantID: id, Role: role}, true
}

// memberWith reads the caller and refuses the request with 403 when the caller's
// role is below the least one the route takes. It writes the refusal itself; a
// false answer is the end of the handler.
func memberWith(c *gin.Context, database db.Database, least string) (member, bool) {
	self, ok := memberOf(c, database, c.FullPath())
	if !ok {
		return member{}, false
	}
	if roleRank(self.Role) < roleRank(least) {
		writeError(c, nethttp.StatusForbidden, "this takes the "+least+" role or above in this tenant")
		return member{}, false
	}
	return self, true
}

// isMember reports whether an account belongs to the named tenant, through the
// account module's membership store. It is the check the owner fields of the
// scoped models go through: an owner has to be a member of the tenant that owns
// the row.
func isMember(ctx context.Context, memberships authn.Memberships, account authn.AccountID, id tenant.ID) (bool, error) {
	return memberships.IsMember(ctx, account, string(id))
}

// roleRank orders the four roles, each able to do what the ones below it do.
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
	db          db.Database
	memberships authn.Memberships
	accounts    authn.Accounts
	roster      tenant.Catalog
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
// authn.Memberships and each member's role from the tenant's own table; a member
// with no role row is shown with an empty role.
func (p *membersProduct) listMembers(c *gin.Context) {
	ctx := c.Request.Context()
	self, ok := memberOf(c, p.db, listMembersRoute)
	if !ok {
		return
	}
	rows, err := p.memberships.Members(ctx, string(self.TenantID), authn.Page{})
	if err != nil {
		log.FromContext(ctx).Error("reading the memberships", "error", err)
		writeError(c, nethttp.StatusServiceUnavailable, "the membership table cannot be read")
		return
	}
	roles, err := p.rolesByAccount(ctx)
	if err != nil {
		log.FromContext(ctx).Error("reading the member roles", "error", err)
		writeError(c, nethttp.StatusServiceUnavailable, "the member role table cannot be read")
		return
	}
	caller, _ := authn.FromContext(ctx)
	out := memberList{Tenant: string(self.TenantID), Caller: string(caller.Account()), Role: self.Role, Members: []memberView{}}
	for _, m := range rows {
		email, name := p.displayOf(ctx, m.Account)
		out.Members = append(out.Members, memberView{
			AccountID: string(m.Account), Email: email, DisplayName: name, Role: roles[string(m.Account)],
		})
	}
	c.JSON(nethttp.StatusOK, out)
}

// rolesByAccount reads every role the tenant's own table holds, keyed by
// account. The statement names no tenant; the filter scopes it.
func (p *membersProduct) rolesByAccount(ctx context.Context) (map[string]string, error) {
	var found []MemberRole
	if err := p.db.DB().WithContext(ctx).Find(&found).Error; err != nil {
		return nil, err
	}
	out := make(map[string]string, len(found))
	for _, m := range found {
		out[m.AccountID] = m.Role
	}
	return out, nil
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

// grantMember gives an existing account a role in the caller's tenant. Only an
// owner or an admin of the tenant may. It writes the membership relation through
// authn.Memberships and the role into the tenant's own table.
func (p *membersProduct) grantMember(c *gin.Context) {
	ctx := c.Request.Context()
	self, ok := memberOf(c, p.db, grantMemberRoute)
	if !ok {
		return
	}
	if self.Role != roleOwner && self.Role != roleAdmin {
		writeError(c, nethttp.StatusForbidden, "only an owner or an admin of this tenant grants a membership")
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
	granted, err := grantHere(ctx, p.db, account, body.Role)
	if err != nil {
		log.FromContext(ctx).Error("granting a membership", "error", err)
		writeError(c, nethttp.StatusInternalServerError, "the membership could not be written")
		return
	}
	c.JSON(nethttp.StatusOK, grantView{
		TenantID: string(granted.TenantID), AccountID: granted.AccountID, Role: granted.Role,
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
		role, err := roleIn(tenant.CrossTenant(ctx), p.db, t.ID, account)
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

// init registers the members module. It runs on serve: its routes sit on the API
// and account endpoints.
func init() {
	registerApplication(application{
		module:     membersModule(),
		runsOn:     []string{serveCommand},
		models:     []any{MemberRole{}},
		journalled: []string{listMembersRoute, grantMemberRoute, meRoute},
	})
}

// membersModule declares the member role table and binds its routes.
//
// The membership relation itself is the account module's, read and written
// through authn.Memberships; this module owns only the role a tenant gives one of
// its accounts. The admission layer (tenant.admission) holds the membership
// check that a caller named here belongs to the tenant; that layer, and not a
// layer of this module, is what refuses a stranger.
//
// It requires the roster because the role table is scoped, and the filter that
// scopes it is the roster module's plugin: a command that ran this module without
// that module would issue every statement unscoped. The roster is delivered
// exclusively, so the requirement brings nothing into a command.
func membersModule() core.Module {
	return core.Module{
		Name: membersModuleName,
		Requires: []core.Requirement{
			{Token: (*log.Logger)(nil)},
			{Token: (*db.Database)(nil)},
			{Token: (*tenant.Catalog)(nil)},
			{Token: (*authn.Memberships)(nil)},
			{Token: (*authn.Accounts)(nil)},
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
			database, err := core.Resolve[db.Database](reg)
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
			return &membersProduct{
				released: released{log: logger}, db: database, memberships: memberships,
				accounts: accounts, roster: roster,
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
			mount(api, listMembersRoute, product.listMembers)
			mount(api, grantMemberRoute, product.grantMember)
			mount(account, meRoute, product.me)
			return nil
		},

		Stop:  recordStop,
		Close: recordClose,
	}
}
