package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	nethttp "net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
)

// trailModuleName is the name the trail module is registered under.
const trailModuleName = "trail"

// trailTable is the journal's table, which the read models below map to.
const trailTable = "audit_entries"

// The routes. The two on the API endpoint read the trail of the tenant the
// request names; the one on the admin endpoint reads across tenants, for the
// platform's operators.
const (
	listTrailRoute  = "GET /audit"
	trailEntryRoute = "GET /audit/{id}"
	crossTrailRoute = "GET /audit-entries"
)

// The query parameters the trail routes take.
const (
	trailActionParam = "action"
	trailSinceParam  = "since"
	trailUntilParam  = "until"
	trailLimitParam  = "limit"
	trailTenantParam = "tenant"
)

// The page sizes a trail read answers with.
const (
	defaultTrailPage = 100
	maxTrailPage     = 500
)

// The seam action of an operator's read, and the purpose each form of it states.
const (
	trailQueryAction  = "audit.query"
	trailQueryPurpose = "a platform operator read audit rows across tenants from the admin endpoint, " +
		"outside any request scoped to a tenant"
	trailTenantQueryPurpose = "a platform operator read this tenant's audit trail from the admin endpoint, " +
		"outside any request of the tenant's own"
)

//go:embed openapi/trail-api.json
var trailAPIOpenAPI []byte

//go:embed openapi/trail-admin.json
var trailAdminOpenAPI []byte

var (
	errUnknownTrailParam = errors.New("the query names a parameter this route does not take")
	errBadTrailTime      = errors.New("a time bound is not an RFC 3339 time")
	errBadTrailLimit     = errors.New("the limit is not a whole number from 1 to " + strconv.Itoa(maxTrailPage))
	errBadTrailWindow    = errors.New("the since bound is not before the until bound")
	errBadTrailEntryID   = errors.New("the entry identifier is not a number")
)

// TrailColumns are the columns of the journal's table the trail shows, beside
// its tenant column. Two read models carry them: trailEntry for a tenant's own
// trail and anyTenantsEntry for an operator's read.
//
// The audit module offers no query surface — who may read whose rows is a
// permission question it leaves to the host — and its stability promise to a
// host reading the table covers the table's name, its tenant column tenant_id,
// holding the tenant identifier's text or NULL and never an empty text, its key
// id, unique over the whole table, and that the journal is its one writer. The
// other columns here are outside that promise, so these models couple the host
// to columns the audit module may change, and a change there is a change here
// as well. That coupling is the price of showing the trail, and the host
// accepts it; it is not a way around any defect of the module.
//
// Neither model writes. The journal is the table's one writer, and a row
// written through a model would be one no capture path records and the trail
// cannot tell from the journal's; the hooks refuse every write GORM runs model
// hooks for — a create, a save, an update and a delete — before a statement is
// built. UpdateColumn and a session that skips hooks run none, and nothing
// issues either on these models.
//
// The type is exported although nothing outside this program names it: an
// embedded field takes its type's name, and GORM reads the columns of an
// embedded struct only through an exported field, so an unexported type here
// would leave both models mapping the tenant column alone.
type TrailColumns struct {
	ID         int64     `gorm:"column:id;primaryKey"`
	OccurredAt time.Time `gorm:"column:occurred_at"`
	Actor      string    `gorm:"column:actor"`
	Source     string    `gorm:"column:source"`
	Action     string    `gorm:"column:action"`
	Object     string    `gorm:"column:object"`
	Outcome    string    `gorm:"column:outcome"`
	Purpose    string    `gorm:"column:purpose"`
	Detail     string    `gorm:"column:detail"`
}

// errTrailReadOnly is the refusal of a write through the trail's read models.
var errTrailReadOnly = errors.New("the audit trail's read models are only read: the journal writes its table")

// BeforeSave refuses a create, a save and an update through a read model.
func (TrailColumns) BeforeSave(*gorm.DB) error { return errTrailReadOnly }

// BeforeDelete refuses a delete through a read model.
func (TrailColumns) BeforeDelete(*gorm.DB) error { return errTrailReadOnly }

// trailEntry reads one tenant's trail. It is the read-only model the tenant
// design sanctions over a table another module creates and writes: it embeds
// the scoping convention, so every read of it issued with a tenant's context —
// a request's, or one the roster entered — is conditioned on that tenant by
// the filter, as a scoped table's reads are, and reaches no row of another
// tenant and no row without one. The table itself is not scoped: it holds
// every tenant's rows and the platform's rows with no tenant, its tenant
// column is nullable and its key is the identifier alone. The key led by the
// tenant and the NOT NULL tenant column the convention asks for bind whoever
// creates a scoped table and writes it, and guard writes this model does not
// make.
//
// The convention marks its tenant column as part of the key, so the order a
// First or a Last adds by the key is not one this table's rows are read in: a
// page here states its own order, and the one read of a single row names it
// by the identifier, which the table holds once, and takes it with Take,
// which adds none.
type trailEntry struct {
	tenant.Model
	TrailColumns
}

// TableName fixes the table this model maps to.
func (trailEntry) TableName() string { return trailTable }

// anyTenantsEntry is the model an operator reads the journal's table through.
// Its tenant field takes a NULL as the absence of a tenant it is: read through
// trailEntry across tenants, a row the platform wrote with no tenant would read
// as the empty identifier, which no text the column holds stands for.
//
// It is the second of the two forms the tenant design gives such a model: it
// embeds the convention and declares a field of its own on the convention's
// column, which GORM reads in place of the convention's, so the tenant filter
// still takes it for a scoped model. A read of it under tenant.CrossTenant is
// conditioned on nothing and reaches the platform's rows; the same read with a
// tenant in its context is conditioned on that tenant, and one with neither is
// refused. A model that did not embed the convention would be a global table
// to the filter, and a read of it that forgot to cross tenants would reach
// every tenant's rows with nothing said.
//
// The field of its own lies outside the convention's key, so the key GORM
// reads here is the journal's identifier alone, which is the table's own.
type anyTenantsEntry struct {
	tenant.Model
	TenantID *string `gorm:"column:tenant_id"`
	TrailColumns
}

// TableName fixes the table this model maps to.
func (anyTenantsEntry) TableName() string { return trailTable }

// trailRecord is either read model: its columns and the tenant it names, empty
// for none.
type trailRecord interface {
	trailEntry | anyTenantsEntry
	record() (TrailColumns, string)
}

func (e trailEntry) record() (TrailColumns, string) { return e.TrailColumns, string(e.TenantID) }

func (e anyTenantsEntry) record() (TrailColumns, string) {
	if e.TenantID == nil {
		return e.TrailColumns, ""
	}
	return e.TrailColumns, *e.TenantID
}

// actionCount is one action and how many rows of a read carry it.
type actionCount struct {
	Action  string `gorm:"column:action" json:"action"`
	Entries int64  `gorm:"column:entries" json:"entries"`
}

// trailView is one audit row as the API shows it. Tenant is empty for a row the
// platform wrote with no tenant.
type trailView struct {
	ID         int64             `json:"id"`
	Tenant     string            `json:"tenant,omitempty"`
	OccurredAt time.Time         `json:"occurred_at"`
	Actor      string            `json:"actor"`
	Source     string            `json:"source"`
	Action     string            `json:"action"`
	Object     string            `json:"object"`
	Outcome    string            `json:"outcome"`
	Purpose    string            `json:"purpose,omitempty"`
	Detail     map[string]string `json:"detail"`
}

// trailPage is what a trail read answers with: the matching rows' count, the
// newest of them, and how many carry each action under the bounds the query
// sets besides its actions. Tenant is empty for a read across tenants.
type trailPage struct {
	Tenant  string        `json:"tenant,omitempty"`
	Total   int64         `json:"total"`
	Entries []trailView   `json:"entries"`
	Actions []actionCount `json:"actions"`
}

// trailViewOf renders one row read through either model. The detail column is
// the JSON object text the journal writes.
func trailViewOf[E trailRecord](row E) (trailView, error) {
	e, tenantID := row.record()
	detail := map[string]string{}
	if err := json.Unmarshal([]byte(e.Detail), &detail); err != nil {
		return trailView{}, fmt.Errorf("reading the detail of audit row %d: %w", e.ID, err)
	}
	return trailView{
		ID: e.ID, Tenant: tenantID, OccurredAt: e.OccurredAt.UTC(), Actor: e.Actor,
		Source: e.Source, Action: e.Action, Object: e.Object, Outcome: e.Outcome, Purpose: e.Purpose,
		Detail: detail,
	}, nil
}

// trailQuery is what a trail read is narrowed by.
type trailQuery struct {
	actions []string
	since   time.Time
	until   time.Time
	limit   int
}

// parseTrailQuery reads a trail route's query parameters. Every parameter other
// than the ones named is refused, so a misspelt bound is an error rather than a
// read that ignores it.
//
// The bounds are held in UTC. The journal writes every time in UTC, and SQLite
// compares the column as the text the driver wrote, which is only in time order
// between texts written in one zone.
func parseTrailQuery(values url.Values, extra ...string) (trailQuery, error) {
	allowed := append([]string{trailActionParam, trailSinceParam, trailUntilParam, trailLimitParam}, extra...)
	for name := range values {
		if !slices.Contains(allowed, name) {
			return trailQuery{}, fmt.Errorf("%w: %q", errUnknownTrailParam, name)
		}
	}
	q := trailQuery{limit: defaultTrailPage}
	for _, a := range values[trailActionParam] {
		if a = strings.TrimSpace(a); a != "" {
			q.actions = append(q.actions, a)
		}
	}
	for name, target := range map[string]*time.Time{trailSinceParam: &q.since, trailUntilParam: &q.until} {
		text := values.Get(name)
		if text == "" {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, text)
		if err != nil {
			return trailQuery{}, fmt.Errorf("%w: %s=%q", errBadTrailTime, name, text)
		}
		*target = at.UTC()
	}
	if !q.since.IsZero() && !q.until.IsZero() && !q.since.Before(q.until) {
		return trailQuery{}, errBadTrailWindow
	}
	if text := values.Get(trailLimitParam); text != "" {
		n, err := strconv.Atoi(text)
		if err != nil || n < 1 || n > maxTrailPage {
			return trailQuery{}, errBadTrailLimit
		}
		q.limit = n
	}
	return q, nil
}

// window narrows a read to the query's time bounds.
func (q trailQuery) window(tx *gorm.DB) *gorm.DB {
	if !q.since.IsZero() {
		tx = tx.Where("occurred_at >= ?", q.since)
	}
	if !q.until.IsZero() {
		tx = tx.Where("occurred_at < ?", q.until)
	}
	return tx
}

// narrow narrows a read to the query's time bounds and actions.
func (q trailQuery) narrow(tx *gorm.DB) *gorm.DB {
	tx = q.window(tx)
	if len(q.actions) > 0 {
		tx = tx.Where("action IN ?", q.actions)
	}
	return tx
}

// readTrail reads one page of the trail through one of the read models: its
// count, its newest rows and its per-action counts, each as a statement of its
// own on the model.
//
// None of the three names a tenant. Issued with a tenant's context, the filter
// puts that tenant on each — the count, the page and the grouped scan alike —
// through either model, since both embed the convention; issued across tenants,
// nothing conditions them.
func readTrail[E trailRecord](ctx context.Context, handle *gorm.DB, q trailQuery) (trailPage, error) {
	var total int64
	if err := handle.WithContext(ctx).Model(new(E)).Scopes(q.narrow).Count(&total).Error; err != nil {
		return trailPage{}, fmt.Errorf("counting the audit rows: %w", err)
	}
	var rows []E
	if err := handle.WithContext(ctx).Scopes(q.narrow).
		Order("occurred_at DESC").Order("id DESC").Limit(q.limit).Find(&rows).Error; err != nil {
		return trailPage{}, fmt.Errorf("reading the audit rows: %w", err)
	}
	actions := []actionCount{}
	if err := handle.WithContext(ctx).Model(new(E)).Scopes(q.window).
		Select("action, COUNT(*) AS entries").Group("action").Order("action").
		Scan(&actions).Error; err != nil {
		return trailPage{}, fmt.Errorf("counting the audit rows by action: %w", err)
	}
	page := trailPage{Total: total, Entries: make([]trailView, 0, len(rows)), Actions: actions}
	for _, row := range rows {
		v, err := trailViewOf(row)
		if err != nil {
			return trailPage{}, err
		}
		page.Entries = append(page.Entries, v)
	}
	return page, nil
}

// readOperatorTrail is an operator's read of the trail, across tenants or of
// the one tenant the context entered: through anyTenantsEntry either way,
// whose tenant field takes the NULL of a platform row, and which the filter
// conditions on the tenant the context carries, if any.
func readOperatorTrail(ctx context.Context, handle *gorm.DB, q trailQuery) (trailPage, error) {
	return readTrail[anyTenantsEntry](ctx, handle, q)
}

// trailProduct is the trail module's product.
type trailProduct struct {
	released
	db       db.Database
	roster   tenant.Catalog
	recorder audit.Audit
}

// listTrail answers the trail of the tenant the request names.
func (p *trailProduct) listTrail(w nethttp.ResponseWriter, r *nethttp.Request) {
	self, ok := memberWith(w, r, roleAdmin)
	if !ok {
		return
	}
	q, err := parseTrailQuery(r.URL.Query())
	if err != nil {
		writeError(w, nethttp.StatusUnprocessableEntity, err.Error())
		return
	}
	ctx := r.Context()
	page, err := readTrail[trailEntry](ctx, p.db.DB(), q)
	if err != nil {
		log.FromContext(ctx).Error("reading the audit trail", "error", err)
		writeError(w, nethttp.StatusServiceUnavailable, "the audit trail cannot be read")
		return
	}
	page.Tenant = string(self.TenantID)
	_ = speedhttp.WriteJSON(w, nethttp.StatusOK, page)
}

// trailEntryByID answers one row of the tenant's trail. The lookup names the
// row's identifier alone, and a row of another tenant is not found, because the
// filter conditions the statement on this one.
func (p *trailProduct) trailEntryByID(w nethttp.ResponseWriter, r *nethttp.Request) {
	if _, ok := memberWith(w, r, roleAdmin); !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, nethttp.StatusUnprocessableEntity, errBadTrailEntryID.Error())
		return
	}
	ctx := r.Context()
	var row trailEntry
	err = p.db.DB().WithContext(ctx).Take(&row, "id = ?", id).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		writeError(w, nethttp.StatusNotFound, "this tenant's trail holds no such entry")
		return
	case err != nil:
		log.FromContext(ctx).Error("reading an audit row", "error", err)
		writeError(w, nethttp.StatusServiceUnavailable, "the audit trail cannot be read")
		return
	}
	v, err := trailViewOf(row)
	if err != nil {
		log.FromContext(ctx).Error("rendering an audit row", "error", err)
		writeError(w, nethttp.StatusInternalServerError, "the audit row cannot be read")
		return
	}
	_ = speedhttp.WriteJSON(w, nethttp.StatusOK, v)
}

// crossTrail answers audit rows for an operator: every tenant's and the
// platform's, or one tenant's when the query names it.
//
// Both read through anyTenantsEntry, which tells the platform's rows, with no
// tenant, from every tenant's. A read of every tenant crosses tenants, and
// says so with tenant.CrossTenant. A read of one tenant is work for that
// tenant alone, so it enters the tenant through the roster, and the filter
// scopes it as it scopes a request of the tenant's own; its statements name no
// tenant either. A disabled tenant's trail is
// still that tenant's, and reading it is what an operator does once the tenant
// is disabled, so the read enters it through EnterRegistered, which enters a
// tenant whatever its status, rather than Enter, which refuses one it does not
// serve.
//
// The read is recorded through the seam before it runs, with its purpose and
// its bounds, and a read whose record could not be written is not answered: an
// operator's read of tenants' rows with no trace is what the seam is there to
// prevent. The record of a read of one tenant is written in that tenant's
// context, so it is in that tenant's own trail.
func (p *trailProduct) crossTrail(w nethttp.ResponseWriter, r *nethttp.Request) {
	ctx := r.Context()
	values := r.URL.Query()
	q, err := parseTrailQuery(values, trailTenantParam)
	if err != nil {
		writeError(w, nethttp.StatusUnprocessableEntity, err.Error())
		return
	}
	scope, purpose := tenant.CrossTenant(ctx), trailQueryPurpose
	named := values.Get(trailTenantParam)
	if named != "" {
		entered, ok := p.enterNamed(w, r, named)
		if !ok {
			return
		}
		scope, purpose = entered, trailTenantQueryPurpose
	}
	if err = p.recorder.Record(scope, audit.Entry{
		Action:  trailQueryAction,
		Object:  trailTable,
		Outcome: "ok",
		Purpose: purpose,
		Detail: map[string]string{
			trailTenantParam: named,
			trailActionParam: strings.Join(q.actions, ","),
			trailSinceParam:  values.Get(trailSinceParam),
			trailUntilParam:  values.Get(trailUntilParam),
		},
	}); err != nil {
		log.FromContext(ctx).Error("recording an operator's read of the trail", "error", err)
		writeError(w, nethttp.StatusInternalServerError, "the read could not be recorded, so it was not made")
		return
	}
	page, err := readOperatorTrail(scope, p.db.DB(), q)
	if err != nil {
		log.FromContext(ctx).Error("reading audit rows for an operator", "error", err)
		writeError(w, nethttp.StatusServiceUnavailable, "the audit trail cannot be read")
		return
	}
	page.Tenant = named
	_ = speedhttp.WriteJSON(w, nethttp.StatusOK, page)
}

// enterNamed enters the tenant an operator's query names, served or disabled,
// and answers the refusal when the roster does not enter it.
//
// The identifier is the query's text, and the roster is what judges it: one no
// row of its table carries is not found. An identifier this replica's snapshot
// does not hold is read from the table, so a tenant another replica registered
// is entered at once.
func (p *trailProduct) enterNamed(w nethttp.ResponseWriter, r *nethttp.Request, named string) (context.Context, bool) {
	if !tenant.IsIdentifier(named) {
		writeError(w, nethttp.StatusUnprocessableEntity, errNoTenantID.Error())
		return nil, false
	}
	entered, err := p.roster.EnterRegistered(r.Context(), tenant.ID(named))
	switch {
	case errors.Is(err, tenant.ErrInvalidTenant):
		writeError(w, nethttp.StatusNotFound, errNoSuchTenant.Error())
		return nil, false
	case err != nil:
		log.FromContext(r.Context()).Error("entering the tenant an operator named", "error", err)
		writeError(w, nethttp.StatusServiceUnavailable, "the roster cannot be read")
		return nil, false
	}
	return entered, true
}

// init registers the trail module. It runs on serve: its routes sit on the API
// and admin endpoints.
func init() {
	registerApplication(application{
		module:     trailModule(),
		runsOn:     []string{serveCommand},
		models:     []any{trailEntry{}, anyTenantsEntry{}},
		journalled: []string{crossTrailRoute},
	})
}

// trailModule binds the routes that read the journal's table.
//
// It requires the roster, as every module issuing statements on a scoped model
// does: both its read models are scoped, and a command running it without the
// filter would read every tenant's rows for any of them. The roster is also what an
// operator's read of one tenant enters that tenant through. It requires the
// audit seam, which an operator's read is recorded through. The table is built
// by the journal's own migrations, which every stack's migrate applies, so this
// module declares none.
func trailModule() core.Module {
	return core.Module{
		Name: trailModuleName,
		Requires: []core.Requirement{
			{Token: (*log.Logger)(nil)},
			{Token: (*db.Database)(nil)},
			{Token: (*tenant.Catalog)(nil)},
			{Token: (*audit.Audit)(nil)},
		},
		Resources: []any{
			speedhttp.Spec{Endpoint: apiEndpoint, Document: trailAPIOpenAPI},
			speedhttp.Spec{Endpoint: adminEndpoint, Document: trailAdminOpenAPI},
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			logger, err := namedLogger(reg, trailModuleName)
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
			return &trailProduct{
				released: released{log: logger},
				db:       database,
				roster:   roster,
				recorder: recorder,
			}, nil
		},

		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			product, ok := instance.(*trailProduct)
			if !ok {
				return errForeignInstance(trailModuleName, instance)
			}
			router, err := core.Resolve[speedhttp.Router](reg)
			if err != nil {
				return err
			}
			api, err := router.Endpoint(apiEndpoint)
			if err != nil {
				return err
			}
			admin, err := router.Endpoint(adminEndpoint)
			if err != nil {
				return err
			}
			api.Route(listTrailRoute, nethttp.HandlerFunc(product.listTrail))
			api.Route(trailEntryRoute, nethttp.HandlerFunc(product.trailEntryByID))
			admin.Route(crossTrailRoute, nethttp.HandlerFunc(product.crossTrail))
			return nil
		},

		Stop:  recordStop,
		Close: recordClose,
	}
}
