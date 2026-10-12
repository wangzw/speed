package main

import (
	"context"
	"embed"
	"errors"
	nethttp "net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/tenant"
)

// accountsModuleName is the name the accounts module is registered under, and
// the namespace of the setting it declares.
const accountsModuleName = "accounts"

// accountTable is the table the account model maps to.
const accountTable = "accounts"

// The routes, all on the API endpoint and scoped to the tenant the request
// names.
const (
	createAccountRoute = "POST /accounts"
	listAccountsRoute  = "GET /accounts"
	getAccountRoute    = "GET /accounts/:id"
	patchAccountRoute  = "PATCH /accounts/:id"
	deleteAccountRoute = "DELETE /accounts/:id"
	ownerAccountRoute  = "POST /accounts/:id/owner"
	purgeAccountRoute  = "POST /accounts/:id/purge"
)

// pageSizeSetting is the setting the listing's page size is read from, under
// its full path: the namespace this module declares, a dot, then the item key.
const pageSizeSetting = accountsModuleName + ".page-size"

// defaultPageSize is the page size when no layer holds another value.
const defaultPageSize = 25

// maxPage bounds the page number a listing accepts, so that the offset it turns
// into cannot overflow.
const maxPage = 1_000_000

// The failures the accounts routes answer with.
var (
	errNoSuchAccount   = errors.New("no account has this identifier in this tenant")
	errNoAccountName   = errors.New("the account's name is empty")
	errNoAccountChange = errors.New("the change names no field")
	errNoOwner         = errors.New("the owner is empty")
	errBadRecordID     = errors.New("an identifier is 1 to 64 letters, digits, '-' or '_'")
	errBadPage         = errors.New("the page is not a whole number from 1 to " + strconv.Itoa(maxPage))
	errAccountExists   = errors.New("an account with this identifier already exists in this tenant")
	errAccountNamed    = errors.New("a deal names the account or one of its contacts, so it is not purged")
)

//go:embed openapi/accounts.json
var accountsOpenAPI []byte

//go:embed migrations/accounts
var accountsMigrationRoot embed.FS

var accountsMigrations = subtree(accountsMigrationRoot, "migrations/accounts")

// Account is a company a tenant sells to. It is a scoped model: it embeds the
// convention, and every statement this host issues on it from a request names
// no tenant.
//
// This Account is the CRM's customer company and is not the identity system's
// account (authn.Account): the two share the word and nothing else. What names
// one of these is the row's own identifier; what names a person is the
// identity system's account identifier, which the owner column carries.
//
// Contacts is the association the detail preloads. It names the account column
// alone, and the tenant half of the reference is the filter's: the filter puts
// the tenant condition on the preloaded rows as on every read of a scoped
// model, and on the joined side of every association join. The table's own
// reference carries both columns, which is what the database enforces.
type Account struct {
	tenant.Model
	ID       string `gorm:"column:id;primaryKey"`
	Name     string `gorm:"column:name;not null"`
	Domain   string `gorm:"column:domain;not null"`
	Industry string `gorm:"column:industry;not null"`
	// Owner is the account identifier of the member who owns this record. It
	// carries no foreign key: the accounts of the identity system live in
	// another module's tables, and a cross-module reference would couple the
	// two migrations' order.
	Owner     string         `gorm:"column:owner;not null"`
	CreatedAt time.Time      `gorm:"column:created_at;not null"`
	UpdatedAt time.Time      `gorm:"column:updated_at;not null"`
	DeletedAt gorm.DeletedAt `gorm:"column:deleted_at"`
	Contacts  []Contact      `gorm:"foreignKey:AccountID;references:ID"`
}

// TableName fixes the table this model maps to.
func (Account) TableName() string { return accountTable }

// accountsTable is the capability the accounts module stands for, so that a
// module whose tables reference the accounts table can declare a requirement on
// it and have its migrations applied after this module's. It is a token only,
// claimed exclusively so a requirement on it never brings this module into a
// command.
type accountsTable interface{ accountsTable() }

// validRecordID accepts an identifier a client chose for a record: 1 to 64
// letters, digits, '-' or '_'. A client choosing identifiers is what lets two
// tenants hold records under one identifier, as the tenant-led keys allow.
func validRecordID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// recordID is the identifier a record is created under: the one the client
// chose, or a fresh one.
func recordID(chosen, prefix string) (string, error) {
	if chosen != "" {
		return chosen, nil
	}
	return newID(prefix)
}

// likeEscape is the escape character every LIKE pattern this host builds
// declares. It is not a backslash, whose meaning in a string literal differs
// between the two dialects.
const likeEscape = "!"

// likeContaining is a LIKE pattern matching text that contains s, with every
// character LIKE would read as a wildcard escaped.
func likeContaining(s string) string { return "%" + escapeLike(s) + "%" }

// likePrefixed is a LIKE pattern matching text that starts with s.
func likePrefixed(s string) string { return escapeLike(s) + "%" }

func escapeLike(s string) string {
	return strings.NewReplacer(likeEscape, likeEscape+likeEscape, "%", likeEscape+"%", "_", likeEscape+"_").Replace(s)
}

// accountView is one account as the API shows it. The tenant is the one the
// row holds, which the filter filled from the request.
type accountView struct {
	TenantID  string    `json:"tenant_id"`
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Domain    string    `json:"domain"`
	Industry  string    `json:"industry"`
	Owner     string    `json:"owner"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func accountViewOf(a Account) accountView {
	return accountView{
		TenantID: string(a.TenantID), ID: a.ID, Name: a.Name, Domain: a.Domain, Industry: a.Industry,
		Owner: a.Owner, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt,
	}
}

// accountDetail is an account with its contacts.
type accountDetail struct {
	accountView
	Contacts []contactView `json:"contacts"`
}

// accountPage is one page of the listing.
type accountPage struct {
	Accounts []accountView `json:"accounts"`
	Page     int           `json:"page"`
	PageSize int           `json:"page_size"`
	Total    int64         `json:"total"`
}

// accountsProduct is the accounts module's product.
type accountsProduct struct {
	released
	db          db.Database
	settings    settings.Store
	memberships authn.Memberships
}

// accountsTable marks the product as the accounts module's.
func (*accountsProduct) accountsTable() {}

// accountRequest is the body of an account creation. The owner is the caller
// unless the body names another member.
type accountRequest struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Domain   string `json:"domain"`
	Industry string `json:"industry"`
	Owner    string `json:"owner"`
}

// Validate refuses a creation with no name or with an identifier this host does
// not accept.
func (a accountRequest) Validate() error {
	switch {
	case a.ID != "" && !validRecordID(a.ID):
		return errBadRecordID
	case strings.TrimSpace(a.Name) == "":
		return errNoAccountName
	}
	return nil
}

// createAccount answers an account creation. The route declares the
// `crm.account.write` permission, so who may reach it is the enforcement
// layer's to decide.
//
// The row names no tenant: the filter fills the column from the request before
// the insert is bound, and the saved row comes back with it filled.
//
// Whether the identifier is taken in this tenant is the primary key's to
// decide, and nothing looks first. The database module marks the key's refusal
// with gorm.ErrDuplicatedKey on either engine, and it is answered as the
// conflict it is.
func (p *accountsProduct) createAccount(c *gin.Context) {
	ctx := c.Request.Context()
	self, ok := memberOf(c, createAccountRoute)
	if !ok {
		return
	}
	var body accountRequest
	if !bindBody(c, &body) {
		return
	}
	owner := string(self.Account)
	if body.Owner != "" {
		belongs, err := isMember(ctx, p.memberships, authn.AccountID(body.Owner), self.TenantID)
		if err != nil {
			log.FromContext(ctx).Error("reading the owner's membership", "error", err)
			writeError(c, nethttp.StatusServiceUnavailable, "the membership table cannot be read")
			return
		}
		if !belongs {
			writeError(c, nethttp.StatusUnprocessableEntity, errOwnerNotMember.Error())
			return
		}
		owner = body.Owner
	}
	id, err := recordID(body.ID, "acc_")
	if err != nil {
		log.FromContext(ctx).Error("drawing an account identifier", "error", err)
		writeError(c, nethttp.StatusInternalServerError, "no identifier could be drawn")
		return
	}
	now := time.Now().UTC()
	account := Account{
		ID:        id,
		Name:      strings.TrimSpace(body.Name),
		Domain:    strings.TrimSpace(body.Domain),
		Industry:  strings.TrimSpace(body.Industry),
		Owner:     owner,
		CreatedAt: now,
		UpdatedAt: now,
	}
	err = p.db.DB().WithContext(ctx).Create(&account).Error
	switch {
	case errors.Is(err, gorm.ErrDuplicatedKey):
		writeError(c, nethttp.StatusConflict, errAccountExists.Error())
		return
	case err != nil:
		log.FromContext(ctx).Error("writing an account", "error", err)
		writeError(c, nethttp.StatusInternalServerError, "the account could not be written")
		return
	}
	c.JSON(nethttp.StatusCreated, accountViewOf(account))
}

// listAccounts answers one page of the tenant's accounts, ordered by name and
// then identifier so that a page holds the same rows on every read, with the
// total the pages add up to. A query parameter q narrows both to the names
// containing it, compared without regard to case.
//
// The page size is the accounts.page-size setting as the tenant's layer
// answers it: the tenant's own row, else the global row, else the declared
// default. The count and the page are two statements, each built from the
// request's context, and neither names a tenant.
//
// The route declares the `crm.account.read` permission, so who may reach it is
// the enforcement layer's to decide.
func (p *accountsProduct) listAccounts(c *gin.Context) {
	ctx := c.Request.Context()
	self, ok := memberOf(c, listAccountsRoute)
	if !ok {
		return
	}
	page := 1
	if raw := c.Query("page"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxPage {
			writeError(c, nethttp.StatusUnprocessableEntity, errBadPage.Error())
			return
		}
		page = n
	}
	size, err := settings.Get[int](p.settings, settings.ForTenant(string(self.TenantID)), pageSizeSetting)
	if err != nil {
		log.FromContext(ctx).Error("reading the page size", "error", err)
		writeError(c, nethttp.StatusInternalServerError, "the page size cannot be read")
		return
	}
	// The settings surface holds whatever was written under the declared
	// type, so the range is the consumer's to check.
	if size <= 0 {
		log.FromContext(ctx).Error("the page size is not positive, so no page is answered",
			"setting", pageSizeSetting, "value", size)
		writeError(c, nethttp.StatusInternalServerError, "the page size is misconfigured")
		return
	}
	search := strings.ToLower(strings.TrimSpace(c.Query("q")))
	matching := func() *gorm.DB {
		stmt := p.db.DB().WithContext(ctx).Model(&Account{})
		if search != "" {
			stmt = stmt.Where("LOWER(name) LIKE ? ESCAPE '"+likeEscape+"'", likeContaining(search))
		}
		return stmt
	}
	out := accountPage{Accounts: []accountView{}, Page: page, PageSize: size}
	if err := matching().Count(&out.Total).Error; err != nil {
		log.FromContext(ctx).Error("counting the accounts", "error", err)
		writeError(c, nethttp.StatusServiceUnavailable, "the account table cannot be read")
		return
	}
	var rows []Account
	if err := matching().Order("name").Order("id").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
		log.FromContext(ctx).Error("reading the accounts", "error", err)
		writeError(c, nethttp.StatusServiceUnavailable, "the account table cannot be read")
		return
	}
	for _, a := range rows {
		out.Accounts = append(out.Accounts, accountViewOf(a))
	}
	c.JSON(nethttp.StatusOK, out)
}

// getAccount answers one account with its contacts, which the preload reads in
// a statement of its own on the same context. The route declares the
// `crm.account.read` permission.
func (p *accountsProduct) getAccount(c *gin.Context) {
	ctx := c.Request.Context()
	if _, ok := memberOf(c, getAccountRoute); !ok {
		return
	}
	id := c.Param("id")
	if !validRecordID(id) {
		writeError(c, nethttp.StatusNotFound, errNoSuchAccount.Error())
		return
	}
	var account Account
	err := p.db.DB().WithContext(ctx).
		Preload("Contacts", func(tx *gorm.DB) *gorm.DB { return tx.Order("name").Order("id") }).
		Where("id = ?", id).First(&account).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		writeError(c, nethttp.StatusNotFound, errNoSuchAccount.Error())
		return
	case err != nil:
		log.FromContext(ctx).Error("reading an account", "error", err)
		writeError(c, nethttp.StatusServiceUnavailable, "the account table cannot be read")
		return
	}
	out := accountDetail{accountView: accountViewOf(account), Contacts: []contactView{}}
	for _, c := range account.Contacts {
		out.Contacts = append(out.Contacts, contactViewOf(c))
	}
	c.JSON(nethttp.StatusOK, out)
}

// accountPatch is the body of a partial update: a member that is present is
// written, one that is absent is left as it is.
type accountPatch struct {
	Name     *string `json:"name"`
	Domain   *string `json:"domain"`
	Industry *string `json:"industry"`
}

// Validate refuses a change that names nothing or empties the name.
func (a accountPatch) Validate() error {
	switch {
	case a.Name == nil && a.Domain == nil && a.Industry == nil:
		return errNoAccountChange
	case a.Name != nil && strings.TrimSpace(*a.Name) == "":
		return errNoAccountName
	}
	return nil
}

// patchAccount changes the named columns of one account. The route declares the
// `crm.account.write` permission.
//
// The update is written from the model: Select names the columns the body
// carries, so a member given as empty is written as empty rather than skipped
// the way a zero field of the struct would be.
func (p *accountsProduct) patchAccount(c *gin.Context) {
	ctx := c.Request.Context()
	if _, ok := memberOf(c, patchAccountRoute); !ok {
		return
	}
	id := c.Param("id")
	var body accountPatch
	if !bindBody(c, &body) {
		return
	}
	if !validRecordID(id) {
		writeError(c, nethttp.StatusNotFound, errNoSuchAccount.Error())
		return
	}
	columns := []string{"updated_at"}
	values := Account{UpdatedAt: time.Now().UTC()}
	if body.Name != nil {
		columns, values.Name = append(columns, "name"), strings.TrimSpace(*body.Name)
	}
	if body.Domain != nil {
		columns, values.Domain = append(columns, "domain"), strings.TrimSpace(*body.Domain)
	}
	if body.Industry != nil {
		columns, values.Industry = append(columns, "industry"), strings.TrimSpace(*body.Industry)
	}
	res := p.db.DB().WithContext(ctx).Model(&Account{}).Where("id = ?", id).Select(columns).Updates(values)
	p.answerChanged(c, id, res, "updating an account")
}

// ownerRequest is the body of an ownership change.
type ownerRequest struct {
	Owner string `json:"owner"`
}

// Validate refuses a change that names no owner.
func (o ownerRequest) Validate() error {
	if strings.TrimSpace(o.Owner) == "" {
		return errNoOwner
	}
	return nil
}

// changeOwner hands one account to another member of the tenant. The route
// declares the `crm.account.own` permission. It writes the one column.
func (p *accountsProduct) changeOwner(c *gin.Context) {
	ctx := c.Request.Context()
	self, ok := memberOf(c, ownerAccountRoute)
	if !ok {
		return
	}
	id := c.Param("id")
	var body ownerRequest
	if !bindBody(c, &body) {
		return
	}
	if !validRecordID(id) {
		writeError(c, nethttp.StatusNotFound, errNoSuchAccount.Error())
		return
	}
	belongs, err := isMember(ctx, p.memberships, authn.AccountID(body.Owner), self.TenantID)
	if err != nil {
		log.FromContext(ctx).Error("reading the owner's membership", "error", err)
		writeError(c, nethttp.StatusServiceUnavailable, "the membership table cannot be read")
		return
	}
	if !belongs {
		writeError(c, nethttp.StatusUnprocessableEntity, errOwnerNotMember.Error())
		return
	}
	res := p.db.DB().WithContext(ctx).Model(&Account{}).Where("id = ?", id).Update("owner", body.Owner)
	p.answerChanged(c, id, res, "changing an account's owner")
}

// answerChanged answers an update of one account: 404 when it matched no row,
// otherwise the account as it now stands.
func (p *accountsProduct) answerChanged(c *gin.Context, id string, res *gorm.DB, doing string) {
	ctx := c.Request.Context()
	switch {
	case res.Error != nil:
		log.FromContext(ctx).Error(doing, "error", res.Error)
		writeError(c, nethttp.StatusInternalServerError, "the account could not be written")
		return
	case res.RowsAffected == 0:
		writeError(c, nethttp.StatusNotFound, errNoSuchAccount.Error())
		return
	}
	var account Account
	if err := p.db.DB().WithContext(ctx).Where("id = ?", id).First(&account).Error; err != nil {
		log.FromContext(ctx).Error("reading an account back", "error", err)
		writeError(c, nethttp.StatusServiceUnavailable, "the account table cannot be read")
		return
	}
	c.JSON(nethttp.StatusOK, accountViewOf(account))
}

// deleteAccount soft-deletes one account and its contacts. The route declares
// the `crm.account.delete` permission. Both rows stay in their tables with the
// deletion time set, and every read through the models leaves them out from
// then on. The statements are one transaction, so an account is never deleted
// with its contacts left showing.
func (p *accountsProduct) deleteAccount(c *gin.Context) {
	if _, ok := memberOf(c, deleteAccountRoute); !ok {
		return
	}
	p.removeAccount(c, false, "deleting an account")
}

// purgeAccount removes one account and its contacts from their tables, deleted
// or not. The route declares the `crm.account.purge` permission. The contacts
// go first, because their rows reference the account's.
func (p *accountsProduct) purgeAccount(c *gin.Context) {
	if _, ok := memberOf(c, purgeAccountRoute); !ok {
		return
	}
	p.removeAccount(c, true, "purging an account")
}

// removeAccount is the transaction a delete and a purge share. A purge runs it
// Unscoped, which reaches the rows a soft delete left and removes them; Unscoped
// is the soft delete's scope only, and the tenant condition stays.
//
// The transaction's first statement locks the account's row, deleted or not,
// before the contacts are touched. A contact is written in a transaction that
// holds the same row under a share lock while it checks the account is live
// (see accountHere), so on an engine that locks rows the two are ordered: a
// contact written first is committed before the contacts' statement here
// reads the table, and a contact written after waits for this transaction and
// then finds the account gone. Locking the row only when the account's own
// statement runs, after the contacts', would let a contact committed between
// the two stay live under a deleted account. SQLite takes no row lock; its
// transactions exclude each other over the whole database instead, each
// beginning with the database's write lock, which the locator asks for.
//
// The locking read is a read over the model like any other, so the filter
// confines it to the tenant's own row: another tenant's account under the
// same identifier is neither locked nor waited for. An identifier the tenant
// holds no row under is answered at once; whether the row is an account left
// to delete is the account statement's to say, through the rows it affected.
//
// A deal naming the account keeps it from a purge: the deals table's reference
// refuses the account statement, and the transaction rolls the contacts'
// statement back with it. A deleted deal names it too: a deal's delete is soft
// and its row stays, with its activities, so an account any deal was ever
// written at is not purged. The purge removes this module's rows and no other
// module's; a deal's row is the deals module's history. A deal naming one of
// the account's contacts as a stakeholder keeps it from a purge the same way,
// through the stakeholders table's reference to the contacts. Whether a deal
// names either is the references' to decide, and nothing looks first. The
// database module marks a reference's refusal with gorm.ErrForeignKeyViolated
// on either engine, and it is answered as the conflict it is.
func (p *accountsProduct) removeAccount(c *gin.Context, purge bool, doing string) {
	ctx := c.Request.Context()
	id := c.Param("id")
	if !validRecordID(id) {
		writeError(c, nethttp.StatusNotFound, errNoSuchAccount.Error())
		return
	}
	err := p.db.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Each statement starts from the transaction's handle: a chain
		// method returns an instance that later ones write into, so one
		// held across two statements would carry the first one's condition
		// into the second.
		scope := func() *gorm.DB {
			if purge {
				return tx.Unscoped()
			}
			return tx
		}
		var locked []string
		err := tx.Unscoped().Model(&Account{}).Clauses(clause.Locking{Strength: clause.LockingStrengthUpdate}).
			Where("id = ?", id).Pluck("id", &locked).Error
		switch {
		case err != nil:
			return err
		case len(locked) == 0:
			return errNoSuchAccount
		}
		if err := scope().Where("account_id = ?", id).Delete(&Contact{}).Error; err != nil {
			return err
		}
		res := scope().Where("id = ?", id).Delete(&Account{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errNoSuchAccount
		}
		return nil
	})
	switch {
	case errors.Is(err, errNoSuchAccount):
		writeError(c, nethttp.StatusNotFound, errNoSuchAccount.Error())
		return
	case errors.Is(err, gorm.ErrForeignKeyViolated):
		writeError(c, nethttp.StatusConflict, errAccountNamed.Error())
		return
	case err != nil:
		log.FromContext(ctx).Error(doing, "error", err)
		writeError(c, nethttp.StatusInternalServerError, "the account could not be removed")
		return
	}
	c.Status(nethttp.StatusNoContent)
}

// accountSettings is the carrier of this module's setting item. A field's value
// is the declared default.
type accountSettings struct {
	PageSize int `config:"page-size"`
}

// accountsSettingsSchema declares the listing's page size on the settings
// surface, where a tenant can hold a value of its own.
func accountsSettingsSchema() settings.Schema {
	return settings.Schema{
		Namespace: accountsModuleName,
		Mounts:    []settings.Mount{{Value: &accountSettings{PageSize: defaultPageSize}}},
		Items: map[string]settings.Item{
			"page-size": {Description: "how many accounts one page of the listing holds"},
		},
	}
}

// init registers the accounts module. It runs on serve, where its routes sit on
// the API endpoint.
func init() {
	registerApplication(application{
		module: accountsModule(),
		runsOn: []string{serveCommand},
		models: []any{Account{}, Contact{}},
		journalled: []string{
			createAccountRoute, patchAccountRoute, deleteAccountRoute, ownerAccountRoute, purgeAccountRoute,
		},
	})
}

// declareAccountRoutes records each account route's required permission on the
// route-declaration surface, where the enforcement middleware reads it in the
// authorizing stage.
//
// The surface is optional: a command that runs this module without the
// authorization layer declares nothing here and the routes carry no requirement,
// which is why a missing surface is not a failure. A command that runs the layer
// declares the permission each route needs, and the surface holds every
// declaration to the permission catalog and to the process's translation once
// the Init window closes, so a route naming a permission the host does not
// declare fails the startup rather than deciding nothing at request time. The
// permissions are the host's own business ones (authzbusiness), which the host
// declares so the catalog carries them.
func declareAccountRoutes(reg *core.Registry) error {
	gate, err := core.Resolve[authz.Gate](reg)
	switch {
	case errors.Is(err, core.ErrMissingProvider):
		return nil
	case err != nil:
		return err
	}
	for _, decl := range []struct {
		route string
		perm  authz.Permission
	}{
		{createAccountRoute, permAccountWrite},
		{listAccountsRoute, permAccountRead},
		{getAccountRoute, permAccountRead},
		{patchAccountRoute, permAccountWrite},
		{deleteAccountRoute, permAccountDel},
		{ownerAccountRoute, permAccountOwn},
		{purgeAccountRoute, permAccountPurge},
	} {
		method, path := splitRoute(decl.route)
		gate.Require(method, path, decl.perm)
	}
	return nil
}

// accountsModule declares the accounts table and the page-size setting and
// binds the account routes.
//
// It requires the roster, which installs the filter the accounts and contacts
// statements are scoped by, so a command running this module without it fails
// at resolution rather than running unscoped. It requires the settings surface
// for the page size, and the membership store for the owner check. It takes the
// route-declaration surface up optionally, in Init, and declares each route's
// permission there; a command without the authorization layer (migrate, for
// one) declares nothing and leaves the routes unguarded, which is why the
// surface is optional rather than required. The accounts table's owner column
// names an account of the identity system's store and carries no foreign key to
// it, so no table token orders this module's migration after another's.
//
// It is built after the roster, and so after the HTTP entry module; it releases
// nothing (see recordClose).
func accountsModule() core.Module {
	return core.Module{
		Name: accountsModuleName,
		Requires: []core.Requirement{
			{Token: (*log.Logger)(nil)},
			{Token: (*db.Database)(nil)},
			{Token: (*tenant.Catalog)(nil)},
			{Token: (*settings.Store)(nil)},
			{Token: (*authn.Memberships)(nil)},
			{Token: (*authz.Gate)(nil), Optional: true},
		},
		Provides: []core.Provision{
			{Token: (*accountsTable)(nil), Exclusive: true},
		},
		Resources: []any{
			db.Migrations{FS: accountsMigrations},
			accountsSettingsSchema(),
			speedhttp.Spec{Endpoint: apiEndpoint, Document: accountsOpenAPI},
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			logger, err := namedLogger(reg, accountsModuleName)
			if err != nil {
				return nil, err
			}
			database, err := core.Resolve[db.Database](reg)
			if err != nil {
				return nil, err
			}
			surface, err := core.Resolve[settings.Store](reg)
			if err != nil {
				return nil, err
			}
			memberships, err := core.Resolve[authn.Memberships](reg)
			if err != nil {
				return nil, err
			}
			return &accountsProduct{
				released: released{log: logger}, db: database, settings: surface, memberships: memberships,
			}, nil
		},

		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			product, ok := instance.(*accountsProduct)
			if !ok {
				return errForeignInstance(accountsModuleName, instance)
			}
			router, err := core.Resolve[speedhttp.Router](reg)
			if err != nil {
				return err
			}
			api, err := router.Endpoint(apiEndpoint)
			if err != nil {
				return err
			}
			mount(api, createAccountRoute, product.createAccount)
			mount(api, listAccountsRoute, product.listAccounts)
			mount(api, getAccountRoute, product.getAccount)
			mount(api, patchAccountRoute, product.patchAccount)
			mount(api, deleteAccountRoute, product.deleteAccount)
			mount(api, ownerAccountRoute, product.changeOwner)
			mount(api, purgeAccountRoute, product.purgeAccount)
			return declareAccountRoutes(reg)
		},

		Stop:  recordStop,
		Close: recordClose,
	}
}
