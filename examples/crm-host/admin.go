package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	nethttp "net/http"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
)

// adminModuleName is the name the admin module is registered under. Its layer
// carries the same name in the chain listing.
const adminModuleName = "admin"

// operatorSubject is the one subject of the operators' table the admin endpoint
// serves, and operatorAccount the account the static backend hands back for it:
// the static prefix and the subject's name. A table holding other subjects
// authenticates them, and every one of them is refused here, which is the
// direction a mismatch between the table and this name has to fail in.
const (
	operatorSubject = "ops"
	operatorAccount = authn.AccountID(authn.StaticPrefix + operatorSubject)
)

// adminRefusal is the record the operator check writes when it turns a caller
// away.
const adminRefusal = "the admin endpoint refused a caller that is not the platform's operator"

// The admin routes. All of them sit on the admin endpoint, which resolves no
// tenant: an operator acts on the roster and across tenants, never inside one.
const (
	listTenantsRoute   = "GET /tenants"
	createTenantRoute  = "POST /tenants"
	disableTenantRoute = "POST /tenants/:id/disable"
	enableTenantRoute  = "POST /tenants/:id/enable"
	createUserRoute    = "POST /users"
	grantTenantRoute   = "POST /tenants/:id/members"
)

//go:embed openapi/admin.json
var adminOpenAPI []byte

var (
	errNoTenantID    = errors.New("the tenant identifier is not a well-formed identifier")
	errNoTenantAlias = errors.New("the tenant alias is not a well-formed name")
)

// operatorLayer is the capability the operator check stands for on the admin
// endpoint's chain. It is a token only.
type operatorLayer interface{ operatorLayer() }

// adminProduct is the admin module's product.
type adminProduct struct {
	released
	db          db.Database
	roster      tenant.Catalog
	recorder    audit.Audit
	accounts    authn.Accounts
	admin       authn.AccountAdmin
	memberships authn.Memberships
}

// layer is the operator check. It sits in the authorizing stage, so it runs
// after the identity is settled, which it reads, and inside the recording
// stage, so a CRM user knocking on the admin endpoint leaves a row. It needs
// no anchor to reach either — the stage order supplies both.
func (p *adminProduct) layer() speedhttp.Middleware {
	return speedhttp.Middleware{
		Name:     adminModuleName,
		Provides: []core.Token{(*operatorLayer)(nil)},
		Stage:    speedhttp.StageAuthorize,
		Handler: func(c *gin.Context) {
			caller, ok := authn.FromContext(c.Request.Context())
			if !ok {
				log.FromContext(c.Request.Context()).Error("the operator check was reached with no identity")
				writeError(c, nethttp.StatusInternalServerError, "the request carries no identity")
				return
			}
			// A platform operator is the subject of the operators' table named
			// operatorSubject. Its account carries the static prefix and equals
			// the full identifier static.<subject>: judging by the prefix alone
			// would let every other subject of that table in, so the whole
			// identifier is compared. A CRM user's account is a library
			// identifier and matches neither.
			account := caller.Account()
			if !account.IsStatic() || account != operatorAccount {
				// The record carries the caller's account and the backend
				// that verified it, which the gateway puts on the context.
				log.FromContext(c.Request.Context()).Info(adminRefusal)
				writeError(c, nethttp.StatusForbidden, "the admin endpoint serves the platform's operators")
				return
			}
			c.Next()
		},
	}
}

// tenantView is a roster entry as the admin endpoint and the command line show
// it.
type tenantView struct {
	ID     string `json:"id"`
	Alias  string `json:"alias"`
	Status string `json:"status"`
}

func viewOf(t tenant.Tenant) tenantView {
	return tenantView{ID: string(t.ID), Alias: t.Alias, Status: t.Status.String()}
}

// listTenants answers the whole roster.
func (p *adminProduct) listTenants(c *gin.Context) {
	all, err := p.roster.List(c.Request.Context())
	if err != nil {
		log.FromContext(c.Request.Context()).Error("reading the roster", "error", err)
		writeError(c, nethttp.StatusServiceUnavailable, "the roster cannot be read")
		return
	}
	out := make([]tenantView, 0, len(all))
	for _, t := range all {
		out = append(out, viewOf(t))
	}
	c.JSON(nethttp.StatusOK, out)
}

// tenantRequest is the body of a tenant registration.
type tenantRequest struct {
	ID    string `json:"id"`
	Alias string `json:"alias"`
}

// Validate refuses what the roster would refuse, so the answer is a 422 naming
// the field rather than whatever the roster's error maps to.
func (t tenantRequest) Validate() error {
	switch {
	case !tenant.IsIdentifier(t.ID):
		return errNoTenantID
	case !tenant.IsIdentifier(t.Alias):
		return errNoTenantAlias
	}
	return nil
}

// registerTenant adds an active tenant to the roster. It refuses an identifier
// the roster already holds, with tenant.ErrTenantExists: a registration is not
// the place a rename or a re-enabling comes from.
//
// The roster's Create is the write that registers only an identifier its table
// does not hold. It asks the table rather than this replica's snapshot, so an
// identifier another replica registered since this one last read the roster
// is refused here too, disabled or not, and of two registrations of one
// identifier at once one registers it and the other is refused. The roster's
// Put would rewrite such a tenant's alias and status instead.
func registerTenant(ctx context.Context, roster tenant.Catalog, id, alias string) (tenant.Tenant, error) {
	t := tenant.Tenant{ID: tenant.ID(id), Alias: alias, Status: tenant.StatusActive}
	if err := roster.Create(ctx, t); err != nil {
		return tenant.Tenant{}, err
	}
	return t, nil
}

// errNoSuchTenant reports a roster operation on an identifier the roster does
// not hold.
var errNoSuchTenant = errors.New("no tenant on the roster has this identifier")

// setTenantStatus disables or enables a tenant and answers with the entry as it
// now stands.
//
// Whether the tenant exists is the roster's to say, and it says so against its
// table: a status change for an identifier no row holds fails with
// tenant.ErrInvalidTenant. Asking the snapshot first would answer for this
// replica's last read instead, and refuse a tenant another replica registered
// since. A malformed identifier is refused before the roster is asked, so that
// failure and a missing tenant are told apart. The roster rereads its table
// after its own write, so the entry read back is the one just written.
func setTenantStatus(ctx context.Context, roster tenant.Catalog, id string, status tenant.Status) (tenant.Tenant, error) {
	if !tenant.IsIdentifier(id) {
		return tenant.Tenant{}, errNoTenantID
	}
	if err := roster.SetStatus(ctx, tenant.ID(id), status); err != nil {
		return tenant.Tenant{}, err
	}
	t, found, err := roster.Get(ctx, tenant.ID(id))
	if err != nil {
		return tenant.Tenant{}, err
	}
	if !found {
		return tenant.Tenant{}, fmt.Errorf("the status of tenant %q was written, and the roster read back without it", id)
	}
	return t, nil
}

// createTenant answers a tenant registration.
//
// A conflict is answered with the roster's own sentinel text and nothing under
// it. The roster wraps the driver's error into the conflict it reports when a
// constraint, rather than its look-up, refused a registration made at the same
// moment as another, and that text names the constraint and the engine's code:
// it differs between the engines and between the two ways one conflict is
// found, and tells the caller nothing the conflict does not.
func (p *adminProduct) createTenant(c *gin.Context) {
	var body tenantRequest
	if !bindBody(c, &body) {
		return
	}
	t, err := registerTenant(c.Request.Context(), p.roster, body.ID, body.Alias)
	switch {
	case errors.Is(err, tenant.ErrTenantExists):
		writeError(c, nethttp.StatusConflict, tenant.ErrTenantExists.Error())
		return
	case errors.Is(err, tenant.ErrAliasConflict):
		writeError(c, nethttp.StatusConflict, tenant.ErrAliasConflict.Error())
		return
	case errors.Is(err, tenant.ErrInvalidTenant):
		writeError(c, nethttp.StatusUnprocessableEntity, err.Error())
		return
	case err != nil:
		log.FromContext(c.Request.Context()).Error("registering a tenant", "error", err)
		writeError(c, nethttp.StatusInternalServerError, "the roster could not be written")
		return
	}
	c.JSON(nethttp.StatusCreated, viewOf(t))
}

// changeStatus answers a disable or an enable.
func (p *adminProduct) changeStatus(status tenant.Status) gin.HandlerFunc {
	return func(c *gin.Context) {
		t, err := setTenantStatus(c.Request.Context(), p.roster, c.Param("id"), status)
		switch {
		case errors.Is(err, errNoTenantID):
			writeError(c, nethttp.StatusUnprocessableEntity, err.Error())
			return
		case errors.Is(err, tenant.ErrInvalidTenant):
			writeError(c, nethttp.StatusNotFound, errNoSuchTenant.Error())
			return
		case err != nil:
			log.FromContext(c.Request.Context()).Error("changing a tenant's status", "error", err)
			writeError(c, nethttp.StatusInternalServerError, "the roster could not be written")
			return
		}
		c.JSON(nethttp.StatusOK, viewOf(t))
	}
}

// userRequest is the body of a user creation.
type userRequest struct {
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
}

// userView is a user as the admin endpoint and the command line show one. It
// never carries a hash.
type userView struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Disabled    bool   `json:"disabled"`
}

// userViewOf renders an account for the admin endpoint and the command line.
// The email is the address the identifier was written under, read through the
// account store.
func userViewOf(a authn.Account, email string) userView {
	return userView{
		ID:          string(a.ID),
		Email:       email,
		DisplayName: a.DisplayName,
		Disabled:    a.Status == authn.StatusDisabled,
	}
}

// createUser creates an account through the account module's write surface. The
// email is written as an administrator's assertion (verified via admin), never a
// proof of ownership, and the password goes through the policy.
func (p *adminProduct) createAccount(ctx context.Context, email, displayName, password string) (authn.Account, error) {
	return p.admin.Create(ctx, authn.CreateAccount{
		Kind:             authn.KindUser,
		DisplayName:      displayName,
		Email:            email,
		EmailVerifiedVia: authn.Verification{Via: authn.VerifiedViaAdmin},
		Password:         password,
		Via:              "admin",
	})
}

// createUserHandler answers a user creation.
func (p *adminProduct) createUserHandler(c *gin.Context) {
	var body userRequest
	if !bindBody(c, &body) {
		return
	}
	account, err := p.createAccount(c.Request.Context(), body.Email, body.DisplayName, body.Password)
	switch {
	case errors.Is(err, authn.ErrAccountExists):
		writeError(c, nethttp.StatusConflict, err.Error())
		return
	case errors.Is(err, authn.ErrInvalidIdentifier), errors.Is(err, authn.ErrInvalidArgument),
		errors.Is(err, authn.ErrPasswordPolicy):
		writeError(c, nethttp.StatusUnprocessableEntity, err.Error())
		return
	case err != nil:
		log.FromContext(c.Request.Context()).Error("creating a user", "error", err)
		writeError(c, nethttp.StatusInternalServerError, "the user could not be written")
		return
	}
	c.JSON(nethttp.StatusCreated, userViewOf(account, body.Email))
}

// grantAcross gives an account a role in the tenant the path names, from an
// endpoint that resolves no tenant.
//
// The write crosses tenants: it happens outside any request scoped to the
// tenant it writes into, so the context says so with tenant.CrossTenant and the
// membership names its tenant itself. That crossing is the one fact no capture
// path of the journal can explain — the statement row sees a write, and the
// request row sees an operator calling a route — so the seam records it with
// the purpose stated.
//
// The tenant it writes into comes from a read of the roster, as a cross-tenant
// write's tenant has to, and the read is this replica's snapshot. A tenant
// another replica registered is refused here with 404 until this replica
// rereads the roster: within the refresh period on a deployment that sets one,
// and not before a restart on one that sets none. That lag is the roster's
// stated window, and a deployment of several replicas sets a refresh period.
func (p *adminProduct) grantAcross(c *gin.Context) {
	ctx := c.Request.Context()
	var body grantRequest
	if !bindBody(c, &body) {
		return
	}
	id := tenant.ID(c.Param("id"))
	if _, found, err := p.roster.Get(ctx, id); err != nil {
		log.FromContext(ctx).Error("reading the roster", "error", err)
		writeError(c, nethttp.StatusServiceUnavailable, "the roster cannot be read")
		return
	} else if !found {
		writeError(c, nethttp.StatusNotFound, errNoSuchTenant.Error())
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
	putErr := p.memberships.Put(tenant.CrossTenant(ctx), authn.Membership{
		Account: account, Tenant: string(id), Active: true, Source: "admin",
	})
	if putErr != nil {
		log.FromContext(ctx).Error("granting a membership", "error", putErr)
		writeError(c, nethttp.StatusInternalServerError, "the membership could not be written")
		return
	}
	granted, err := grantIn(tenant.CrossTenant(ctx), p.db, id, account, body.Role)
	if err != nil {
		log.FromContext(ctx).Error("granting a membership", "error", err)
		writeError(c, nethttp.StatusInternalServerError, "the membership could not be written")
		return
	}
	if err := p.recorder.Record(ctx, audit.Entry{
		Action:  "membership.grant",
		Object:  memberRoleTable + "/" + string(id) + "/" + string(account),
		Outcome: "ok",
		Purpose: "a platform operator granted a membership in a tenant from the admin endpoint, " +
			"outside any request scoped to that tenant",
		Detail: map[string]string{"role": granted.Role},
	}); err != nil {
		// The membership is written and its reason is not: a crossing with
		// no record is what the seam exists to prevent, so the caller is
		// told the operation did not complete as asked.
		log.FromContext(ctx).Error("recording a cross-tenant grant", "error", err)
		writeError(c, nethttp.StatusInternalServerError, "the membership was written and its audit record was not")
		return
	}
	c.JSON(nethttp.StatusOK, grantView{
		TenantID: string(granted.TenantID), AccountID: granted.AccountID, Role: granted.Role,
	})
}

// accountByEmail finds the account an address is bound to, through the account
// module's look-up.
func (p *adminProduct) accountByEmail(ctx context.Context, email string) (authn.AccountID, bool, error) {
	id, err := p.accounts.Lookup(ctx, authn.IdentifierEmail, email)
	if errors.Is(err, authn.ErrAccountNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

// init registers the admin module. It runs on serve: its layer and routes sit on
// the admin endpoint.
func init() {
	registerApplication(application{
		module: adminModule(),
		runsOn: []string{serveCommand},
		models: []any{MemberRole{}},
		journalled: []string{
			listTenantsRoute, createTenantRoute, disableTenantRoute, enableTenantRoute,
			createUserRoute, grantTenantRoute,
		},
	})
}

// adminModule binds the admin endpoint's routes and its operator check.
//
// It requires the roster and the audit seam, which it takes up, and the account
// module's read, write and membership capabilities. The roster requirement is
// the guard the members module states as well: this module writes the scoped
// role table, and a command running it without the roster would write it
// unscoped with nothing said.
func adminModule() core.Module {
	return core.Module{
		Name: adminModuleName,
		Requires: []core.Requirement{
			{Token: (*log.Logger)(nil)},
			{Token: (*db.Database)(nil)},
			{Token: (*tenant.Catalog)(nil)},
			{Token: (*audit.Audit)(nil)},
			{Token: (*authn.Accounts)(nil)},
			{Token: (*authn.AccountAdmin)(nil)},
			{Token: (*authn.Memberships)(nil)},
		},
		Resources: []any{
			speedhttp.Spec{Endpoint: adminEndpoint, Document: adminOpenAPI},
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			logger, err := namedLogger(reg, adminModuleName)
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
			recorder, err := core.Resolve[audit.Audit](reg)
			if err != nil {
				return nil, err
			}
			accounts, err := core.Resolve[authn.Accounts](reg)
			if err != nil {
				return nil, err
			}
			admin, err := core.Resolve[authn.AccountAdmin](reg)
			if err != nil {
				return nil, err
			}
			memberships, err := core.Resolve[authn.Memberships](reg)
			if err != nil {
				return nil, err
			}
			return &adminProduct{
				released: released{log: logger}, db: database, roster: roster, recorder: recorder,
				accounts: accounts, admin: admin, memberships: memberships,
			}, nil
		},

		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			product, ok := instance.(*adminProduct)
			if !ok {
				return errForeignInstance(adminModuleName, instance)
			}
			router, err := core.Resolve[speedhttp.Router](reg)
			if err != nil {
				return err
			}
			admin, err := router.Endpoint(adminEndpoint)
			if err != nil {
				return err
			}
			admin.Use(product.layer())
			mount(admin, listTenantsRoute, product.listTenants)
			mount(admin, createTenantRoute, product.createTenant)
			mount(admin, disableTenantRoute, product.changeStatus(tenant.StatusDisabled))
			mount(admin, enableTenantRoute, product.changeStatus(tenant.StatusActive))
			mount(admin, createUserRoute, product.createUserHandler)
			mount(admin, grantTenantRoute, product.grantAcross)
			return nil
		},

		Stop:  recordStop,
		Close: recordClose,
	}
}
