package main

import (
	"context"
	"database/sql"
	"embed"
	"encoding/csv"
	"errors"
	"fmt"
	nethttp "net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
)

// contactsModuleName is the name the contacts module is registered under.
const contactsModuleName = "contacts"

// contactTable is the table the contact model maps to.
const contactTable = "contacts"

// The routes, all on the API endpoint and scoped to the tenant the request
// names.
const (
	createContactRoute    = "POST /contacts"
	listContactsRoute     = "GET /contacts"
	getContactRoute       = "GET /contacts/:id"
	patchContactRoute     = "PATCH /contacts/:id"
	deleteContactRoute    = "DELETE /contacts/:id"
	upsertContactRoute    = "POST /contacts/upsert"
	exportContactsRoute   = "GET /contacts/export"
	autocompleteRoute     = "GET /contacts/autocomplete"
	contactDirectoryRoute = "GET /contacts/directory"
)

// The keys a contact's email and phone are logged under. Both are registered
// with the logging module's redaction when this module is built, so a record
// carrying either reaches no destination with the value in it.
const (
	contactEmailKey = "contact_email"
	contactPhoneKey = "contact_phone"
)

// autocompleteLimit bounds how many names one autocomplete answers with.
const autocompleteLimit = 10

// exportColumns are the columns the export writes, in order. None is sealed:
// the database module refuses a Rows over the model that reads a sealed
// column, because what the column is scanned into is not on the statement.
var exportColumns = []string{"id", "account_id", "name", "title", "created_at"}

// The failures the contacts routes answer with.
var (
	errNoSuchContact   = errors.New("no contact has this identifier in this tenant")
	errNoContactName   = errors.New("the contact's name is empty")
	errBadContactEmail = errors.New("the email is not an address this host accepts")
	errNoAccountRef    = errors.New("the contact names no account")
	errUnknownAccount  = errors.New("no account has this identifier in this tenant")
	errAccountUnread   = errors.New("the contact's account could not be read")
	errNoContactChange = errors.New("the change names no field")
	errNoLookup        = errors.New("a listing names an account_id, an email, or both")
	errContactTaken    = errors.New("another contact of this tenant has this identifier or this email")
	errEmailInUse      = errors.New("another contact of this tenant has this email")
)

//go:embed openapi/contacts.json
var contactsOpenAPI []byte

//go:embed migrations/contacts
var contactsMigrationRoot embed.FS

var contactsMigrations = subtree(contactsMigrationRoot, "migrations/contacts")

// Contact is a person at an account. It is a scoped model, and two of its
// columns are sealed by the database module's encrypted serializer: what the
// table holds for them is ciphertext, and a read into the model opens it.
//
// EmailBidx is the blind index of the normalised email. The serializer acts on
// one field and cannot fill a second column, so every write of Email here
// writes EmailBidx beside it, and every lookup by email flattens the address
// the same way before taking its index.
type Contact struct {
	tenant.Model
	ID        string         `gorm:"column:id;primaryKey"`
	AccountID string         `gorm:"column:account_id;not null"`
	Name      string         `gorm:"column:name;not null"`
	Email     string         `gorm:"column:email;not null;serializer:encrypted"`
	EmailBidx string         `gorm:"column:email_bidx;not null"`
	Phone     *string        `gorm:"column:phone;serializer:encrypted"`
	Title     string         `gorm:"column:title;not null"`
	CreatedAt time.Time      `gorm:"column:created_at;not null"`
	DeletedAt gorm.DeletedAt `gorm:"column:deleted_at"`
	Account   *Account       `gorm:"foreignKey:AccountID;references:ID"`
}

// TableName fixes the table this model maps to.
func (Contact) TableName() string { return contactTable }

// contactsTable is the capability the contacts module stands for, for a module
// whose tables reference the contacts table. A token only, claimed exclusively.
type contactsTable interface{ contactsTable() }

// validContactEmail accepts an address with one @, something on each side of
// it, and no blank or control character.
func validContactEmail(email string) bool {
	local, domain, found := strings.Cut(email, "@")
	if !found || local == "" || domain == "" || strings.Contains(domain, "@") {
		return false
	}
	return !strings.ContainsFunc(email, func(r rune) bool { return r <= ' ' || r == 0x7f })
}

// contactView is one contact as the API shows it, in the clear.
type contactView struct {
	TenantID  string    `json:"tenant_id"`
	ID        string    `json:"id"`
	AccountID string    `json:"account_id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	Phone     *string   `json:"phone"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
}

func contactViewOf(c Contact) contactView {
	return contactView{
		TenantID: string(c.TenantID), ID: c.ID, AccountID: c.AccountID, Name: c.Name,
		Email: c.Email, Phone: c.Phone, Title: c.Title, CreatedAt: c.CreatedAt,
	}
}

// contactList is what a listing answers with.
type contactList struct {
	Contacts []contactView `json:"contacts"`
}

// contactsProduct is the contacts module's product.
type contactsProduct struct {
	released
	db db.Database
}

// contactsTable marks the product as the contacts module's.
func (*contactsProduct) contactsTable() {}

// emailIndex is the blind index an email is stored and looked up under.
func (p *contactsProduct) emailIndex(email string) string { return contactEmailIndex(p.db, email) }

// contactEmailIndex is the blind index of an email under a database's current
// root key. The address is flattened first, so case and surrounding blanks do
// not change it.
func contactEmailIndex(database db.Database, email string) string {
	return database.BlindIndex(normaliseEmail(email))
}

// accountHere reports whether the tenant the handle's context names holds a
// live account under an identifier, and holds that account's row under a share
// lock until the handle's transaction ends. It reads at most one row through
// Row, which goes down GORM's row chain rather than its query chain; the filter
// scopes both. A soft-deleted account is not one: the model's soft delete
// leaves it out of a Row as it does of every read over the model.
//
// The share lock is one half of what keeps a contact from being committed
// under an account a delete running at the same time marks deleted; the other
// half is the lock accountsProduct.removeAccount takes before it touches the
// contacts. On PostgreSQL a check that meets that lock waits for the delete to
// end and then reads the row as the delete left it. SQLite's dialect writes no
// locking clause, and there the transaction alone keeps the two apart: each
// begins holding the database's write lock, which the locator asks for.
func accountHere(tx *gorm.DB, id string) (bool, error) {
	var one int
	err := tx.Model(&Account{}).Clauses(clause.Locking{Strength: clause.LockingStrengthShare}).
		Select("1").Where("id = ?", id).Limit(1).Row().Scan(&one)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, err
	}
	return true, nil
}

// underLiveAccount runs a write in one transaction with the check that the
// named account is a live account of the request's tenant, so the account
// cannot be deleted between the check and the write. It answers
// errUnknownAccount when the tenant holds no live account under the
// identifier, and a failure to read the account wrapped in errAccountUnread.
//
// The reference over the tenant and the account refuses a contact at an
// account the tenant does not hold, and accepts one at a deleted account,
// whose row is still in the table, so the check is what a live account is
// judged by.
func (p *contactsProduct) underLiveAccount(ctx context.Context, accountID string, write func(tx *gorm.DB) error) error {
	return p.db.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		found, err := accountHere(tx, accountID)
		switch {
		case err != nil:
			return fmt.Errorf("%w: %w", errAccountUnread, err)
		case !found:
			return errUnknownAccount
		}
		return write(tx)
	})
}

// answerWriteFailure answers the failure of a write run by underLiveAccount.
//
// A row the primary key or the unique email refuses is a conflict: the
// database module marks either refusal with gorm.ErrDuplicatedKey, and names
// the constraint only in the driver's error, which the host does not read, so
// the answer names both.
func answerWriteFailure(c *gin.Context, err error, doing string) {
	ctx := c.Request.Context()
	switch {
	case errors.Is(err, errUnknownAccount):
		writeError(c, nethttp.StatusUnprocessableEntity, errUnknownAccount.Error())
	case errors.Is(err, gorm.ErrDuplicatedKey):
		writeError(c, nethttp.StatusConflict, errContactTaken.Error())
	case errors.Is(err, errAccountUnread):
		log.FromContext(ctx).Error("reading the contact's account", "error", err)
		writeError(c, nethttp.StatusServiceUnavailable, "the account table cannot be read")
	default:
		log.FromContext(ctx).Error(doing, "error", err)
		writeError(c, nethttp.StatusInternalServerError, "the contact could not be written")
	}
}

// contactRequest is the body of a contact creation and of a find-or-create.
type contactRequest struct {
	ID        string `json:"id"`
	AccountID string `json:"account_id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	Phone     string `json:"phone"`
	Title     string `json:"title"`
}

// Validate refuses a contact with no name, no account or no usable email.
func (c contactRequest) Validate() error {
	switch {
	case c.ID != "" && !validRecordID(c.ID):
		return errBadRecordID
	case strings.TrimSpace(c.Name) == "":
		return errNoContactName
	case !validRecordID(c.AccountID):
		return errNoAccountRef
	case !validContactEmail(strings.TrimSpace(c.Email)):
		return errBadContactEmail
	}
	return nil
}

// optionalText is a trimmed text, or nil for one that is blank.
func optionalText(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

// newContact builds the row a request describes, its blind index filled.
func (p *contactsProduct) newContact(body contactRequest) (Contact, error) {
	id, err := recordID(body.ID, "con_")
	if err != nil {
		return Contact{}, err
	}
	email := strings.TrimSpace(body.Email)
	return Contact{
		ID:        id,
		AccountID: body.AccountID,
		Name:      strings.TrimSpace(body.Name),
		Email:     email,
		EmailBidx: p.emailIndex(email),
		Phone:     optionalText(body.Phone),
		Title:     strings.TrimSpace(body.Title),
		CreatedAt: time.Now().UTC(),
	}, nil
}

// logWritten writes the record of a contact written. The email and the phone
// go under the keys the redaction masks.
func logWritten(ctx context.Context, msg string, c Contact) {
	phone := ""
	if c.Phone != nil {
		phone = *c.Phone
	}
	log.FromContext(ctx).Info(msg, "contact_id", c.ID, "account_id", c.AccountID,
		contactEmailKey, c.Email, contactPhoneKey, phone)
}

// createContact answers a contact creation; a rep or above.
//
// Whether the email is taken in this tenant is the unique index's to decide,
// and nothing looks first: a look-up would answer the ordinary duplicate and
// still leave the concurrent one to the index. The index's refusal is
// answered as a conflict (see answerWriteFailure). The insert runs after the
// check that the account is a live account of the tenant, in the same
// transaction.
func (p *contactsProduct) createContact(c *gin.Context) {
	ctx := c.Request.Context()
	if _, ok := memberWith(c, roleRep); !ok {
		return
	}
	var body contactRequest
	if !bindBody(c, &body) {
		return
	}
	contact, err := p.newContact(body)
	if err != nil {
		log.FromContext(ctx).Error("drawing a contact identifier", "error", err)
		writeError(c, nethttp.StatusInternalServerError, "no identifier could be drawn")
		return
	}
	err = p.underLiveAccount(ctx, body.AccountID, func(tx *gorm.DB) error {
		return tx.Create(&contact).Error
	})
	if err != nil {
		answerWriteFailure(c, err, "writing a contact")
		return
	}
	logWritten(ctx, "contact created", contact)
	c.JSON(nethttp.StatusCreated, contactViewOf(contact))
}

// listContacts answers the tenant's contacts at one account, those with one
// email, or those matching both. The email is matched through its blind index,
// the one column an equality on a sealed value can meet.
func (p *contactsProduct) listContacts(c *gin.Context) {
	ctx := c.Request.Context()
	if _, ok := memberWith(c, roleViewer); !ok {
		return
	}
	query := c.Request.URL.Query()
	account, email := query.Get("account_id"), strings.TrimSpace(query.Get("email"))
	if account == "" && email == "" {
		writeError(c, nethttp.StatusUnprocessableEntity, errNoLookup.Error())
		return
	}
	stmt := p.db.DB().WithContext(ctx).Order("name").Order("id")
	if account != "" {
		stmt = stmt.Where("account_id = ?", account)
	}
	if email != "" {
		stmt = stmt.Where("email_bidx = ?", p.emailIndex(email))
	}
	var found []Contact
	if err := stmt.Find(&found).Error; err != nil {
		log.FromContext(ctx).Error("reading the contacts", "error", err)
		writeError(c, nethttp.StatusServiceUnavailable, "the contact table cannot be read")
		return
	}
	out := contactList{Contacts: []contactView{}}
	for _, c := range found {
		out.Contacts = append(out.Contacts, contactViewOf(c))
	}
	c.JSON(nethttp.StatusOK, out)
}

// getContact answers one contact, in the clear.
func (p *contactsProduct) getContact(c *gin.Context) {
	ctx := c.Request.Context()
	if _, ok := memberWith(c, roleViewer); !ok {
		return
	}
	id := c.Param("id")
	if !validRecordID(id) {
		writeError(c, nethttp.StatusNotFound, errNoSuchContact.Error())
		return
	}
	contact, found, err := p.contactByID(ctx, id)
	switch {
	case err != nil:
		log.FromContext(ctx).Error("reading a contact", "error", err)
		writeError(c, nethttp.StatusServiceUnavailable, "the contact table cannot be read")
	case !found:
		writeError(c, nethttp.StatusNotFound, errNoSuchContact.Error())
	default:
		c.JSON(nethttp.StatusOK, contactViewOf(contact))
	}
}

// contactByID reads one contact of the tenant the context names.
func (p *contactsProduct) contactByID(ctx context.Context, id string) (Contact, bool, error) {
	var contact Contact
	err := p.db.DB().WithContext(ctx).Where("id = ?", id).First(&contact).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return Contact{}, false, nil
	case err != nil:
		return Contact{}, false, err
	}
	return contact, true, nil
}

// contactPatch is the body of a partial update: a member that is present is
// written, one that is absent is left as it is. An empty phone clears it.
type contactPatch struct {
	Name  *string `json:"name"`
	Email *string `json:"email"`
	Phone *string `json:"phone"`
	Title *string `json:"title"`
}

// Validate refuses a change that names nothing, empties the name, or gives an
// email this host does not accept.
func (c contactPatch) Validate() error {
	switch {
	case c.Name == nil && c.Email == nil && c.Phone == nil && c.Title == nil:
		return errNoContactChange
	case c.Name != nil && strings.TrimSpace(*c.Name) == "":
		return errNoContactName
	case c.Email != nil && !validContactEmail(strings.TrimSpace(*c.Email)):
		return errBadContactEmail
	}
	return nil
}

// patchContact changes the named columns of one contact; a rep or above.
//
// The update is written from a map of column names. The database module seals
// the values a map carries into a sealed column before the statement is built,
// so a new email reaches the table as ciphertext, and its blind index goes into
// the same statement. A new email another live contact of the tenant holds is
// refused by the unique index, which the database module marks with
// gorm.ErrDuplicatedKey, and it is answered as a conflict. The statement
// writes no key column, so the index on the email is the one that can refuse
// it.
func (p *contactsProduct) patchContact(c *gin.Context) {
	ctx := c.Request.Context()
	if _, ok := memberWith(c, roleRep); !ok {
		return
	}
	id := c.Param("id")
	var body contactPatch
	if !bindBody(c, &body) {
		return
	}
	if !validRecordID(id) {
		writeError(c, nethttp.StatusNotFound, errNoSuchContact.Error())
		return
	}
	changes := map[string]any{}
	if body.Name != nil {
		changes["name"] = strings.TrimSpace(*body.Name)
	}
	if body.Email != nil {
		email := strings.TrimSpace(*body.Email)
		changes["email"] = email
		changes["email_bidx"] = p.emailIndex(email)
	}
	if body.Phone != nil {
		changes["phone"] = optionalText(*body.Phone)
	}
	if body.Title != nil {
		changes["title"] = strings.TrimSpace(*body.Title)
	}
	res := p.db.DB().WithContext(ctx).Model(&Contact{}).Where("id = ?", id).Updates(changes)
	switch {
	case errors.Is(res.Error, gorm.ErrDuplicatedKey):
		writeError(c, nethttp.StatusConflict, errEmailInUse.Error())
		return
	case res.Error != nil:
		log.FromContext(ctx).Error("updating a contact", "error", res.Error)
		writeError(c, nethttp.StatusInternalServerError, "the contact could not be written")
		return
	case res.RowsAffected == 0:
		writeError(c, nethttp.StatusNotFound, errNoSuchContact.Error())
		return
	}
	contact, found, err := p.contactByID(ctx, id)
	if err != nil || !found {
		log.FromContext(ctx).Error("reading a contact back", "error", err, "found", found)
		writeError(c, nethttp.StatusServiceUnavailable, "the contact table cannot be read")
		return
	}
	logWritten(ctx, "contact updated", contact)
	c.JSON(nethttp.StatusOK, contactViewOf(contact))
}

// deleteContact soft-deletes one contact; an admin or above. The statement
// addresses the row by its model's key, whose tenant half the filter fills from
// the request.
func (p *contactsProduct) deleteContact(c *gin.Context) {
	ctx := c.Request.Context()
	if _, ok := memberWith(c, roleAdmin); !ok {
		return
	}
	id := c.Param("id")
	if !validRecordID(id) {
		writeError(c, nethttp.StatusNotFound, errNoSuchContact.Error())
		return
	}
	res := p.db.DB().WithContext(ctx).Delete(&Contact{ID: id})
	switch {
	case res.Error != nil:
		log.FromContext(ctx).Error("deleting a contact", "error", res.Error)
		writeError(c, nethttp.StatusInternalServerError, "the contact could not be deleted")
	case res.RowsAffected == 0:
		writeError(c, nethttp.StatusNotFound, errNoSuchContact.Error())
	default:
		c.Status(nethttp.StatusNoContent)
	}
}

// upsertContact finds the tenant's contact with an email, or creates it from
// the body when there is none; a rep or above. A contact found is answered as
// it stands, 200; one created is answered 201.
//
// The lookup is the blind index, given as a struct condition so that the
// create that follows a miss starts from it; the rest of the row comes from
// Attrs, which only a create reads. Both run after the check that the account
// the body names is a live account of the tenant, in the same transaction.
func (p *contactsProduct) upsertContact(c *gin.Context) {
	ctx := c.Request.Context()
	if _, ok := memberWith(c, roleRep); !ok {
		return
	}
	var body contactRequest
	if !bindBody(c, &body) {
		return
	}
	fresh, err := p.newContact(body)
	if err != nil {
		log.FromContext(ctx).Error("drawing a contact identifier", "error", err)
		writeError(c, nethttp.StatusInternalServerError, "no identifier could be drawn")
		return
	}
	var contact Contact
	created := false
	err = p.underLiveAccount(ctx, body.AccountID, func(tx *gorm.DB) error {
		res := tx.
			Where(Contact{EmailBidx: fresh.EmailBidx}).
			Attrs(Contact{
				ID: fresh.ID, AccountID: fresh.AccountID, Name: fresh.Name, Email: fresh.Email,
				Phone: fresh.Phone, Title: fresh.Title, CreatedAt: fresh.CreatedAt,
			}).
			FirstOrCreate(&contact)
		// FirstOrCreate reports the one row its insert wrote, and nothing
		// for a row it found.
		created = res.RowsAffected == 1
		return res.Error
	})
	if err != nil {
		answerWriteFailure(c, err, "finding or creating a contact")
		return
	}
	status := nethttp.StatusOK
	if created {
		status = nethttp.StatusCreated
		logWritten(ctx, "contact created", contact)
	}
	c.JSON(status, contactViewOf(contact))
}

// exportedContact is one line of the export.
type exportedContact struct {
	ID        string    `gorm:"column:id"`
	AccountID string    `gorm:"column:account_id"`
	Name      string    `gorm:"column:name"`
	Title     string    `gorm:"column:title"`
	CreatedAt time.Time `gorm:"column:created_at"`
}

// exportContacts streams the tenant's contacts as CSV, one row at a time from
// the result set, with the columns exportColumns names.
//
// Each row is scanned by ScanRows on the handle Rows was called on, which is
// the handle that carries the statement's context. Once the first line is
// written the status is sent, so a failure after it ends the body early and is
// logged rather than answered.
func (p *contactsProduct) exportContacts(c *gin.Context) {
	ctx := c.Request.Context()
	if _, ok := memberWith(c, roleViewer); !ok {
		return
	}
	stmt := p.db.DB().WithContext(ctx).Model(&Contact{}).
		Select(exportColumns).Order("account_id").Order("name").Order("id")
	rows, err := stmt.Rows()
	if err != nil {
		log.FromContext(ctx).Error("reading the contacts to export", "error", err)
		writeError(c, nethttp.StatusServiceUnavailable, "the contact table cannot be read")
		return
	}
	defer func() { _ = rows.Close() }()
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="contacts.csv"`)
	out := csv.NewWriter(c.Writer)
	if err := out.Write(exportColumns); err != nil {
		log.FromContext(ctx).Warn("the export's reader went away", "error", err)
		return
	}
	for rows.Next() {
		var row exportedContact
		if err := stmt.ScanRows(rows, &row); err != nil {
			log.FromContext(ctx).Error("scanning a contact to export", "error", err)
			return
		}
		line := []string{row.ID, row.AccountID, row.Name, row.Title, row.CreatedAt.UTC().Format(time.RFC3339)}
		if err := out.Write(line); err != nil {
			log.FromContext(ctx).Warn("the export's reader went away", "error", err)
			return
		}
	}
	if err := rows.Err(); err != nil {
		log.FromContext(ctx).Error("reading the contacts to export", "error", err)
		return
	}
	out.Flush()
	if err := out.Error(); err != nil {
		log.FromContext(ctx).Warn("the export's reader went away", "error", err)
	}
}

// autocompleteView is what the autocomplete answers with: names and nothing
// else.
type autocompleteView struct {
	Names []string `json:"names"`
}

// autocomplete answers the distinct names of the tenant's contacts that start
// with a prefix, compared without regard to case, in order.
func (p *contactsProduct) autocomplete(c *gin.Context) {
	ctx := c.Request.Context()
	if _, ok := memberWith(c, roleViewer); !ok {
		return
	}
	prefix := strings.ToLower(strings.TrimSpace(c.Query("prefix")))
	out := autocompleteView{Names: []string{}}
	err := p.db.DB().WithContext(ctx).Model(&Contact{}).
		Where("LOWER(name) LIKE ? ESCAPE '"+likeEscape+"'", likePrefixed(prefix)).
		Distinct().Order("name").Limit(autocompleteLimit).
		Pluck("name", &out.Names).Error
	if err != nil {
		log.FromContext(ctx).Error("reading the contact names", "error", err)
		writeError(c, nethttp.StatusServiceUnavailable, "the contact table cannot be read")
		return
	}
	c.JSON(nethttp.StatusOK, out)
}

// directoryEntry is one contact with the name of its account and of the
// account's owner. Each name is read from the column the association join
// selects for it.
type directoryEntry struct {
	ID           string `gorm:"column:id" json:"id"`
	Name         string `gorm:"column:name" json:"name"`
	Title        string `gorm:"column:title" json:"title"`
	AccountID    string `gorm:"column:account_id" json:"account_id"`
	AccountName  string `gorm:"column:Account__name" json:"account_name"`
	AccountOwner string `gorm:"column:Account__OwnerUser__display_name" json:"account_owner"`
}

// directoryView is what the directory answers with.
type directoryView struct {
	Entries []directoryEntry `json:"entries"`
}

// directory answers every contact of the tenant beside the name of its
// account and of the account's owner, in one statement: the association join
// puts the account's columns and its owner's beside the contact's, and the
// rows are scanned into a struct of their own. The selection leaves the sealed
// columns out, which a Scan over the model has to.
//
// The join names the nested path alone, so the account is joined as the
// middle level of the path rather than by a join of its own. It is a scoped
// table all the same, and the filter conditions it; the owner's table, at the
// end of the path, is global.
func (p *contactsProduct) directory(c *gin.Context) {
	ctx := c.Request.Context()
	if _, ok := memberWith(c, roleViewer); !ok {
		return
	}
	out := directoryView{Entries: []directoryEntry{}}
	err := p.db.DB().WithContext(ctx).Model(&Contact{}).Joins("Account.OwnerUser").
		Select("contacts.id, contacts.name, contacts.title, contacts.account_id").
		Order("contacts.name").Order("contacts.id").
		Scan(&out.Entries).Error
	if err != nil {
		log.FromContext(ctx).Error("reading the contact directory", "error", err)
		writeError(c, nethttp.StatusServiceUnavailable, "the contact table cannot be read")
		return
	}
	c.JSON(nethttp.StatusOK, out)
}

// init registers the contacts module. It runs on serve, where its routes sit on
// the API endpoint.
func init() {
	registerApplication(application{
		module: contactsModule(),
		runsOn: []string{serveCommand},
		models: []any{Contact{}, Account{}, User{}},
		journalled: []string{
			createContactRoute, patchContactRoute, deleteContactRoute, upsertContactRoute,
		},
	})
}

// contactsModule declares the contacts table, registers the redaction of the
// keys a contact's email and phone are logged under, and binds the contact
// routes.
//
// It requires the roster, for the filter every contact statement is scoped by,
// and the accounts module's table token, optionally, because the contacts table
// references the accounts table. The redaction is registered in New, before the
// module takes its logger: a rule governs the judgements made after it is
// registered, and taking a Named logger already binds an attribute.
func contactsModule() core.Module {
	return core.Module{
		Name: contactsModuleName,
		Requires: []core.Requirement{
			{Token: (*log.Logger)(nil)},
			{Token: (*db.Database)(nil)},
			{Token: (*tenant.Catalog)(nil)},
			{Token: (*accountsTable)(nil), Optional: true},
		},
		Provides: []core.Provision{
			{Token: (*contactsTable)(nil), Exclusive: true},
		},
		Resources: []any{
			db.Migrations{FS: contactsMigrations},
			speedhttp.Spec{Endpoint: apiEndpoint, Document: contactsOpenAPI},
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			logging, err := core.Resolve[log.Logger](reg)
			if err != nil {
				return nil, err
			}
			logging.Redaction().AddKeys(contactEmailKey, contactPhoneKey)
			database, err := core.Resolve[db.Database](reg)
			if err != nil {
				return nil, err
			}
			return &contactsProduct{released: released{log: logging.Named(contactsModuleName)}, db: database}, nil
		},

		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			product, ok := instance.(*contactsProduct)
			if !ok {
				return errForeignInstance(contactsModuleName, instance)
			}
			router, err := core.Resolve[speedhttp.Router](reg)
			if err != nil {
				return err
			}
			api, err := router.Endpoint(apiEndpoint)
			if err != nil {
				return err
			}
			mount(api, createContactRoute, product.createContact)
			mount(api, listContactsRoute, product.listContacts)
			mount(api, getContactRoute, product.getContact)
			mount(api, patchContactRoute, product.patchContact)
			mount(api, deleteContactRoute, product.deleteContact)
			mount(api, upsertContactRoute, product.upsertContact)
			mount(api, exportContactsRoute, product.exportContacts)
			mount(api, autocompleteRoute, product.autocomplete)
			mount(api, contactDirectoryRoute, product.directory)
			return nil
		},

		Stop:  recordStop,
		Close: recordClose,
	}
}
