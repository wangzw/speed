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

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/ratelimit"
	"github.com/vislake/speed/pkg/tenant"
)

// membersModuleName is the name the members module is registered under. Its
// layer carries the same name in the chain listing.
const membersModuleName = "members"

// membershipTable is the table the scoped model maps to.
const membershipTable = "memberships"

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

//go:embed openapi/members-api.json
var membersAPIOpenAPI []byte

//go:embed openapi/members-account.json
var membersAccountOpenAPI []byte

//go:embed migrations/members
var membersMigrationRoot embed.FS

var membersMigrations = subtree(membersMigrationRoot, "migrations/members")

// Membership is a scoped model: it embeds the convention, which is both the
// declaration and the column. The statements this module issues on it with a
// request's context name no tenant; the filter puts the condition there.
type Membership struct {
	tenant.Model
	UserID    string    `gorm:"column:user_id;primaryKey"`
	Role      string    `gorm:"column:role;not null"`
	GrantedAt time.Time `gorm:"column:granted_at;not null"`
}

// TableName fixes the table this model maps to.
func (Membership) TableName() string { return membershipTable }

// errOwnerNotMember is the refusal of a record whose owner the tenant does not
// count among its members.
var errOwnerNotMember = errors.New("the owner is not a member of this tenant")

// isMember reports whether a user belongs to the tenant the context names. The
// statement names the user and nothing else; the filter scopes it.
func isMember(ctx context.Context, database db.Database, userID string) (bool, error) {
	var found []Membership
	err := database.DB().WithContext(ctx).Where("user_id = ?", userID).Limit(1).Find(&found).Error
	return len(found) > 0, err
}

// grantHere gives a user a role in the tenant a request's context names, or
// changes the role the user already has there.
//
// The row it writes names no tenant. The tenant half of the key is the
// filter's to fill, from the context, before the statement is bound; a write
// that names the tenant column is how a cross-tenant write states where it
// goes, and this one is not cross-tenant. The written row comes back with the
// column filled, so what the caller answers with is what was written.
//
// "Insert it, or rewrite the one there" is an upsert by the primary key, the
// shape the tenant design gives a request path for it. Save is not: GORM takes
// a struct whose key has an empty part for a new row before any callback runs,
// so with the tenant half left to the filter a second grant is an insert the
// key refuses. The upsert's conflict lands on the context's tenant's own row,
// since the filter fills the tenant column before the clause is rendered, and
// the assignments GORM writes for it leave the key's columns out.
func grantHere(ctx context.Context, database db.Database, userID, role string) (Membership, error) {
	if !validRole(role) {
		return Membership{}, errUnknownRole
	}
	m := Membership{UserID: userID, Role: role, GrantedAt: time.Now().UTC()}
	if err := database.DB().WithContext(ctx).Clauses(clause.OnConflict{UpdateAll: true}).Create(&m).Error; err != nil {
		return Membership{}, fmt.Errorf("writing the membership: %w", err)
	}
	return m, nil
}

// grantIn gives a user a role in a named tenant, or changes the role the user
// already has there, from outside any request scoped to that tenant: the admin
// endpoint and the command line.
//
// The caller hands in a cross-tenant context, and under it the filter neither
// adds a condition nor fills the column, so the row names its tenant itself.
// The tenant comes from a roster read the caller made, never from a constant.
func grantIn(ctx context.Context, database db.Database, id tenant.ID, userID, role string) (Membership, error) {
	if !validRole(role) {
		return Membership{}, errUnknownRole
	}
	m := Membership{
		Model:     tenant.Model{TenantID: id},
		UserID:    userID,
		Role:      role,
		GrantedAt: time.Now().UTC(),
	}
	if err := database.DB().WithContext(ctx).Save(&m).Error; err != nil {
		return Membership{}, fmt.Errorf("writing the membership: %w", err)
	}
	return m, nil
}

// membershipsOfUser lists every tenant a user belongs to. The question crosses
// tenants by definition, so the caller hands in a cross-tenant context and the
// user condition is the only one.
func membershipsOfUser(ctx context.Context, database db.Database, userID string) ([]Membership, error) {
	var found []Membership
	err := database.DB().WithContext(ctx).Where("user_id = ?", userID).Order("tenant_id").Find(&found).Error
	return found, err
}

// memberLayer is the capability the membership layer stands for on a chain, so
// that a later layer can place itself inside it with an After of its own. It
// is a token only: nothing delivers or resolves it.
type memberLayer interface{ memberLayer() }

// member is what the membership layer establishes about a request: which CRM
// user is asking, in which tenant, in which role.
//
// UserID is the CRM principal. A CRM user authenticates through either of two
// backends, so the identity's Key differs by credential — users/<id> for Basic,
// sessions/<id> for a bearer token — and UserID is the same under both: it is
// what a handler stores wherever a row records who acted. TenantID is for
// answering with; a scoped row a handler writes names no tenant, because the
// filter fills it from the request's context.
type member struct {
	UserID   string
	TenantID tenant.ID
	Role     string
}

// memberKey is the context key the established member travels under.
type memberKey struct{}

// memberFrom reads the member the layer established. It is false off the API
// endpoint, where the layer does not run, and a handler there has to handle
// that rather than read an anonymous request as someone.
func memberFrom(ctx context.Context) (member, bool) {
	m, ok := ctx.Value(memberKey{}).(member)
	return m, ok
}

// memberOf reads the member the membership layer established for a route on
// the API endpoint, answering 500 when there is none: a route there reached
// without the layer came by a path that skipped it.
func memberOf(c *gin.Context, route string) (member, bool) {
	self, ok := memberFrom(c.Request.Context())
	if !ok {
		log.FromContext(c.Request.Context()).Error("a member route was reached without the membership layer", "route", route)
		writeError(c, nethttp.StatusInternalServerError, "the request carries no membership")
	}
	return self, ok
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

// memberWith answers the member the membership layer established, and refuses
// the request with 403 when the member's role is below the least one the route
// takes. It writes the refusal itself; a false answer is the end of the
// handler.
func memberWith(c *gin.Context, least string) (member, bool) {
	self, ok := memberOf(c, c.FullPath())
	if !ok {
		return member{}, false
	}
	if roleRank(self.Role) < roleRank(least) {
		writeError(c, nethttp.StatusForbidden, "this takes the "+least+" role or above in this tenant")
		return member{}, false
	}
	return self, true
}

// membersTable is the capability the members module stands for, so that a
// module whose tables reference the memberships table can declare a requirement
// on it: the database module applies migrations in the order the requirements
// give, and that order is the only one between two modules' migrations. It is a
// token only — nothing resolves it — and it is claimed exclusively, so a
// requirement on it never brings this module into a command.
type membersTable interface{ membersTable() }

// membersProduct is the members module's product.
type membersProduct struct {
	released
	db     db.Database
	roster tenant.Catalog
}

// membersTable marks the product as the members module's.
func (*membersProduct) membersTable() {}

// layer is the membership layer: it lets a request through only when the
// caller belongs to the tenant the request names.
//
// Authentication and resolution say who is asking and which tenant is named,
// and neither says whether the first belongs to the second — a caller who
// authenticates and names another tenant would otherwise read that tenant's
// rows. This is where that is decided.
//
// Its anchors are the four layers it has to sit inside. The gateway and
// resolution because it reads what they establish; the journal and the rate
// limiter because its refusals are the ones most worth a record and most worth
// counting — a caller probing tenants it does not belong to is refused here, and
// a layer outside the journal refuses without a row. Anchoring on the first two
// alone lands it outside the other two: once resolution is placed, this layer
// and the journal are both free to go next, the smaller order sits further out,
// and the journal's order is larger than the default this layer carries; the
// limiter anchors on the journal itself, so it lands inside both.
func (p *membersProduct) layer() speedhttp.Middleware {
	return speedhttp.Middleware{
		Name:     membersModuleName,
		Provides: []core.Token{(*memberLayer)(nil)},
		After: []core.Token{
			(*authn.Gate)(nil),
			(*tenant.Resolution)(nil),
			(*audit.Audit)(nil),
			(*ratelimit.Limiter)(nil),
		},
		Handler: p.admit,
	}
}

// admit is the layer's decision.
//
// The lookup is issued with the request's context, so the tenant filter scopes
// it: the statement names the user and nothing else, and a membership in
// another tenant is not a row it can see.
func (p *membersProduct) admit(c *gin.Context) {
	ctx := c.Request.Context()
	caller, ok := authn.FromContext(ctx)
	if !ok {
		// Unreachable behind the gateway, and refused loudly rather than
		// read as anonymous: a request that reached this layer without
		// an identity came by a path that skipped authentication.
		log.FromContext(ctx).Error("the membership layer was reached with no identity")
		writeError(c, nethttp.StatusInternalServerError, "the request carries no identity")
		return
	}
	id, ok := tenant.FromContext(ctx)
	if !ok {
		log.FromContext(ctx).Error("the membership layer was reached with no tenant")
		writeError(c, nethttp.StatusInternalServerError, "the request carries no tenant")
		return
	}
	userID, isUser := userIDOf(caller)
	if !isUser {
		// An operator, or any identity no CRM backend issued, belongs to
		// no tenant and is refused like a stranger.
		writeError(c, nethttp.StatusForbidden, "the caller is not a member of this tenant")
		return
	}
	var found []Membership
	if err := p.db.DB().WithContext(ctx).Where("user_id = ?", userID).Limit(1).Find(&found).Error; err != nil {
		log.FromContext(ctx).Error("reading the caller's membership", "error", err)
		writeError(c, nethttp.StatusServiceUnavailable, "the membership table cannot be read")
		return
	}
	if len(found) == 0 {
		writeError(c, nethttp.StatusForbidden, "the caller is not a member of this tenant")
		return
	}
	ctx = context.WithValue(ctx, memberKey{}, member{UserID: userID, TenantID: id, Role: found[0].Role})
	c.Request = c.Request.WithContext(ctx)
	c.Next()
}

// memberView is one membership as the API shows it.
type memberView struct {
	UserID      string `json:"user_id"`
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

// listMembers answers the tenant's membership list. The query carries no tenant
// condition, and the rows that come back are this tenant's because the filter
// put the condition there.
func (p *membersProduct) listMembers(c *gin.Context) {
	ctx := c.Request.Context()
	self, ok := memberFrom(ctx)
	if !ok {
		log.FromContext(ctx).Error("a member route was reached without the membership layer", "route", listMembersRoute)
		writeError(c, nethttp.StatusInternalServerError, "the request carries no membership")
		return
	}
	var rows []Membership
	if err := p.db.DB().WithContext(ctx).Order("user_id").Find(&rows).Error; err != nil {
		log.FromContext(ctx).Error("reading the memberships", "error", err)
		writeError(c, nethttp.StatusServiceUnavailable, "the membership table cannot be read")
		return
	}
	ids := make([]string, 0, len(rows))
	for _, m := range rows {
		ids = append(ids, m.UserID)
	}
	people, err := usersByID(ctx, p.db, ids)
	if err != nil {
		log.FromContext(ctx).Error("reading the members' users", "error", err)
		writeError(c, nethttp.StatusServiceUnavailable, "the user table cannot be read")
		return
	}
	caller, _ := authn.FromContext(ctx)
	out := memberList{Tenant: string(self.TenantID), Caller: caller.Key(), Role: self.Role, Members: []memberView{}}
	for _, m := range rows {
		u := people[m.UserID]
		out.Members = append(out.Members, memberView{
			UserID: m.UserID, Email: u.Email, DisplayName: u.DisplayName, Role: m.Role,
		})
	}
	c.JSON(nethttp.StatusOK, out)
}

// grantRequest is the body of a membership grant.
type grantRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

// Validate refuses a grant that names no user or no known role.
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
	TenantID string `json:"tenant_id"`
	UserID   string `json:"user_id"`
	Role     string `json:"role"`
}

// grantMember gives an existing user a role in the caller's tenant. Only an
// owner or an admin of the tenant may.
func (p *membersProduct) grantMember(c *gin.Context) {
	ctx := c.Request.Context()
	self, ok := memberFrom(ctx)
	if !ok {
		log.FromContext(ctx).Error("a member route was reached without the membership layer", "route", grantMemberRoute)
		writeError(c, nethttp.StatusInternalServerError, "the request carries no membership")
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
	user, found, err := userByEmail(ctx, p.db, body.Email)
	if err != nil {
		log.FromContext(ctx).Error("reading the user to grant", "error", err)
		writeError(c, nethttp.StatusServiceUnavailable, "the user table cannot be read")
		return
	}
	if !found {
		writeError(c, nethttp.StatusNotFound, "no user has this email")
		return
	}
	granted, err := grantHere(ctx, p.db, user.ID, body.Role)
	if err != nil {
		log.FromContext(ctx).Error("granting a membership", "error", err)
		writeError(c, nethttp.StatusInternalServerError, "the membership could not be written")
		return
	}
	c.JSON(nethttp.StatusOK, grantView{
		TenantID: string(granted.TenantID), UserID: granted.UserID, Role: granted.Role,
	})
}

// meTenant is one tenant the caller belongs to.
type meTenant struct {
	ID     string `json:"id"`
	Alias  string `json:"alias"`
	Status string `json:"status"`
	Role   string `json:"role"`
}

// meUser is the CRM user behind the caller.
type meUser struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
}

// meView is what the account endpoint answers GET /me with.
type meView struct {
	Identity string     `json:"identity"`
	User     *meUser    `json:"user"`
	Tenants  []meTenant `json:"tenants"`
}

// me answers who the caller is and which tenants they belong to.
//
// The membership read is the one statement in this host that crosses tenants
// from inside a request, and it does so through tenant.CrossTenant, which the
// tenant design sanctions for exactly this: a question whose answer spans
// tenants by definition. The account endpoint resolves no tenant, so a scoped
// statement here would be refused for having none — the refusal is the filter
// working, and the cross-tenant context is how a caller states that it means
// to go around it. The only condition is the caller's own user identifier,
// taken from the identity the gateway established rather than from anything
// the request says.
func (p *membersProduct) me(c *gin.Context) {
	ctx := c.Request.Context()
	caller, ok := authn.FromContext(ctx)
	if !ok {
		log.FromContext(ctx).Error("a guarded route was reached with no identity", "route", meRoute)
		writeError(c, nethttp.StatusInternalServerError, "the request carries no identity")
		return
	}
	out := meView{Identity: caller.Key(), Tenants: []meTenant{}}
	userID, isUser := userIDOf(caller)
	if !isUser {
		// An operator is a caller with no CRM user and no tenant.
		c.JSON(nethttp.StatusOK, out)
		return
	}
	user, found, err := userByID(ctx, p.db, userID)
	if err != nil {
		log.FromContext(ctx).Error("reading the caller's user", "error", err)
		writeError(c, nethttp.StatusServiceUnavailable, "the user table cannot be read")
		return
	}
	if found {
		out.User = &meUser{ID: user.ID, Email: user.Email, DisplayName: user.DisplayName}
	}
	rows, err := membershipsOfUser(tenant.CrossTenant(ctx), p.db, userID)
	if err != nil {
		log.FromContext(ctx).Error("reading the caller's memberships", "error", err)
		writeError(c, nethttp.StatusServiceUnavailable, "the membership table cannot be read")
		return
	}
	for _, m := range rows {
		t, known, err := p.roster.Get(ctx, m.TenantID)
		if err != nil {
			log.FromContext(ctx).Error("reading the roster", "error", err)
			writeError(c, nethttp.StatusServiceUnavailable, "the roster cannot be read")
			return
		}
		if !known {
			continue
		}
		out.Tenants = append(out.Tenants, meTenant{
			ID: string(t.ID), Alias: t.Alias, Status: t.Status.String(), Role: m.Role,
		})
	}
	c.JSON(nethttp.StatusOK, out)
}

// init registers the members module. It runs on serve: its layer and routes sit
// on the API and account endpoints.
func init() {
	registerApplication(application{
		module:     membersModule(),
		runsOn:     []string{serveCommand},
		models:     []any{Membership{}, User{}},
		journalled: []string{listMembersRoute, grantMemberRoute, meRoute},
	})
}

// membersModule declares the memberships table, binds its routes and registers
// the membership layer on the API endpoint.
//
// It requires the roster. The memberships table is scoped, and the filter that
// scopes it is the roster module's plugin, installed only in a run that names
// that module: a command that ran this module without it would issue every
// statement unscoped — writing an empty tenant, reading every tenant's rows —
// with nothing refusing and nothing reported. The requirement turns that into
// a startup that fails at resolution, naming the capability. The roster is
// delivered exclusively, so the requirement brings nothing into a command; it
// is the host that names the roster in every command that runs this module.
//
// The requirement also decides where this module is built: after the roster,
// which is built after the HTTP entry module, so this module is released while
// that one is still draining requests. It releases nothing (see recordClose).
//
// The memberships table references the users table, so this module declares a
// requirement on the users module's table token, which orders its migration
// after the users one. The requirement is optional: the order holds in every
// command, because the migration order is computed over every registered
// module, and this module reads the users table through the database handle
// rather than through the users module's product, so a command that runs it
// without the users module has nothing missing.
func membersModule() core.Module {
	return core.Module{
		Name: membersModuleName,
		Requires: []core.Requirement{
			{Token: (*log.Logger)(nil)},
			{Token: (*db.Database)(nil)},
			{Token: (*tenant.Catalog)(nil)},
			{Token: (*usersTable)(nil), Optional: true},
		},
		Provides: []core.Provision{
			{Token: (*membersTable)(nil), Exclusive: true},
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
			return &membersProduct{released: released{log: logger}, db: database, roster: roster}, nil
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
			api.Use(product.layer())
			mount(api, listMembersRoute, product.listMembers)
			mount(api, grantMemberRoute, product.grantMember)
			mount(account, meRoute, product.me)
			return nil
		},

		Stop:  recordStop,
		Close: recordClose,
	}
}
