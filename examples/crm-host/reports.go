package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	nethttp "net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/tenant"
)

// reportsModuleName is the name the reports module is registered under, and
// the namespace of its setting.
const reportsModuleName = "reports"

// reportsCacheTTLSetting is how long a pipeline report is served from the
// key-value store, under its full path.
const reportsCacheTTLSetting = reportsModuleName + ".cache-ttl"

// pipelineInvalidatedMsg is the record the module writes for each deal event
// it has invalidated a tenant's report on, naming the event and its tenant.
const pipelineInvalidatedMsg = "a deal event invalidates the pipeline report"

// defaultReportTTL is the cache lifetime when no layer holds another.
const defaultReportTTL = 5 * time.Minute

// The routes, all on the API endpoint and scoped to the tenant the request
// names. Each reads what other modules write: the deals and the stages they
// sit in, the revenue projection's table, the webhooks module's deliveries.
// They sit here rather than beside those tables because the revenue projection
// and the webhooks run on the worker as well, and the worker runs no HTTP entry
// point.
const (
	pipelineReportRoute = "GET /reports/pipeline"
	leaderboardRoute    = "GET /reports/leaderboard"
	revenueRoute        = "GET /revenue"
	deliveriesRoute     = "GET /reports/webhook-deliveries"
)

// reportPermissions is each route's required permission, in the form the
// declaration surface takes. Every route here is a read of what other modules
// wrote, so each needs the report read permission. A route the host mounts on
// the API endpoint must appear here or be declared open, or the authorization
// middleware refuses it as undeclared.
var reportPermissions = []struct {
	route string
	perm  authz.Permission
}{
	{pipelineReportRoute, permReportRead},
	{leaderboardRoute, permReportRead},
	{revenueRoute, permReportRead},
	{deliveriesRoute, permReportRead},
}

// declareReportRoutes declares each report route's required permission on the
// route-declaration surface, so the enforcement middleware decides the route
// rather than the handler reading a role.
//
// The surface is taken up optionally: a command that runs the reports module
// without the enforcement layer has none to declare to, and its routes there
// carry no requirement. A command that runs the layer holds every declaration to
// the permission catalog and to the process's translation once the Init window
// closes, so a route naming a permission the host does not declare fails the
// startup rather than deciding nothing at request time.
func declareReportRoutes(reg *core.Registry) error {
	gate, err := core.Resolve[authz.Gate](reg)
	switch {
	case errors.Is(err, core.ErrMissingProvider):
		return nil
	case err != nil:
		return err
	}
	for _, d := range reportPermissions {
		method, path := splitRoute(d.route)
		gate.Require(method, path, d.perm)
	}
	return nil
}

// deliveriesPage bounds how many deliveries the listing answers with.
const deliveriesPage = 200

//go:embed openapi/reports-api.json
var reportsOpenAPI []byte

// reportsSettings is the carrier of the module's setting item.
type reportsSettings struct {
	CacheTTL time.Duration `config:"cache-ttl"`
}

// reportsSettingsSchema declares the cache lifetime.
func reportsSettingsSchema() settings.Schema {
	return settings.Schema{
		Namespace: reportsModuleName,
		Mounts:    []settings.Mount{{Value: &reportsSettings{CacheTTL: defaultReportTTL}}},
		Items: map[string]settings.Item{
			"cache-ttl": {Description: "how long a tenant's pipeline report is served from the key-value store " +
				"before it is computed again; a value that is not positive turns the cache off"},
		},
	}
}

// pipelineGenerationKey holds a tenant's report generation: a counter every
// invalidation adds one to. A cached report is stored under the generation it
// was computed in, so an invalidation that lands while a report is being
// computed leaves that report under a generation no later read looks up.
//
// A generation the store no longer holds reads as generation 0: the key lapses
// once generationLifetime has passed since it was created, and a store may
// evict it sooner. Each invalidation therefore deletes the report cached under
// the generation it supersedes as well, so a generation that starts again from
// 0 finds no report an invalidation already superseded. What that leaves is a
// report still being computed when the first invalidation lands: stored under
// generation 0 after the delete, it is served again if the generation is lost
// within that report's lifetime.
func pipelineGenerationKey(id tenant.ID) string {
	return storeKey("reports", "pipeline-generation", string(id))
}

// generationLifetime is how long a tenant's generation key is kept, counted
// from the invalidation that created it: an increment does not extend it. It
// bounds how long a tenant whose reports nobody reads again — or an identifier
// a deal event names and no tenant has — leaves a key behind.
const generationLifetime = 24 * time.Hour

// pipelineKey is where one generation of a tenant's pipeline report is cached.
func pipelineKey(id tenant.ID, generation int64) string {
	return storeKey("reports", "pipeline", string(id), strconv.FormatInt(generation, 10))
}

// currentGeneration reads a tenant's report generation. The store keeps the
// counter as the decimal text of a float64, which is parsed rather than taken
// as a key segment as it stands; the increments add one at a time, so the value
// is a whole number.
func currentGeneration(ctx context.Context, store kv.KV, id tenant.ID) (int64, error) {
	value, present, err := store.Get(ctx, pipelineGenerationKey(id))
	if err != nil || !present {
		return 0, err
	}
	n, err := strconv.ParseFloat(string(value), 64)
	if err != nil {
		return 0, fmt.Errorf("the report generation of %s does not hold a number: %w", id, err)
	}
	return int64(n), nil
}

// advanceGeneration invalidates a tenant's pipeline report: it moves the
// generation on and deletes the report cached under the one it superseded.
func advanceGeneration(ctx context.Context, store kv.KV, id tenant.ID) error {
	n, err := store.IncrByFloatWithTTL(ctx, pipelineGenerationKey(id), 1, generationLifetime)
	if err != nil {
		return err
	}
	return store.Delete(ctx, pipelineKey(id, int64(n)-1))
}

// reportsProduct is the reports module's product.
type reportsProduct struct {
	released
	db       db.Database
	store    kv.KV
	settings settings.Store
	accounts authn.Accounts
}

// stageTotal is one row of the pipeline report: how many of the tenant's deals
// sit in one of its stages in one currency, and their amounts added up.
type stageTotal struct {
	Stage    string `json:"stage"`
	Currency string `json:"currency"`
	Deals    int64  `json:"deals"`
	Amount   int64  `json:"amount"`
}

// pipelineReport is the pipeline report: the tenant's deals summed by stage and
// currency, in the order of the tenant's stages, as of the moment it was
// computed.
type pipelineReport struct {
	Tenant string       `json:"tenant"`
	AsOf   time.Time    `json:"as_of"`
	Stages []stageTotal `json:"stages"`
}

// pipeline answers the pipeline report, from the cache when the tenant's
// current generation of it is there.
//
// The cache key carries the tenant and the generation, and the lifetime is the
// tenant's cache-ttl setting. Every deal event moves the tenant's generation
// on (see dropPipeline), so the report a read finds is computed from the deals
// as the last announced write left them. A key-value store that cannot be read
// answers 503: the cache is not read around.
//
// A report is computed by grouping the tenant's deals and scanning the sums
// into a row type of this module's own. The deals are joined to their stages
// through the relation, for the stages' order, and the join selects none of
// the stage's columns: GORM adds every column of a model joined through a
// relation to the statement's select, and in a grouped select those would be
// columns neither grouped nor aggregated. The statement names no tenant: the
// filter puts the tenant condition on the deals and on the joined stages, so a
// deal is not paired with the other tenant's stage of the same key.
func (p *reportsProduct) pipeline(c *gin.Context) {
	ctx := c.Request.Context()
	self, ok := memberOf(c, pipelineReportRoute)
	if !ok {
		return
	}
	ttl, err := settings.Get[time.Duration](p.settings, settings.ForTenant(string(self.TenantID)), reportsCacheTTLSetting)
	if err != nil {
		log.FromContext(ctx).Error("reading the report cache lifetime", "error", err)
		writeError(c, nethttp.StatusInternalServerError, "the report cache lifetime cannot be read")
		return
	}
	// The generation is read before the table, so a report computed from rows
	// an invalidation has already superseded is stored under the generation
	// that invalidation left behind.
	generation, err := currentGeneration(ctx, p.store, self.TenantID)
	if err != nil {
		log.FromContext(ctx).Error("reading the report generation", "error", err)
		writeError(c, nethttp.StatusServiceUnavailable, "the report cache cannot be read")
		return
	}
	key := pipelineKey(self.TenantID, generation)
	if ttl > 0 {
		var (
			cached []byte
			hit    bool
		)
		cached, hit, err = p.store.Get(ctx, key)
		if err != nil {
			log.FromContext(ctx).Error("reading the cached report", "error", err)
			writeError(c, nethttp.StatusServiceUnavailable, "the report cache cannot be read")
			return
		}
		if hit {
			writeRawJSON(c, cached)
			return
		}
	}
	handle := p.db.DB().WithContext(ctx)
	var rows []stageTotal
	err = handle.Model(&Deal{}).
		Select(dealTable+".stage_key AS stage, "+dealTable+".currency AS currency, COUNT(*) AS deals, "+
			"CAST(SUM("+dealTable+".amount) AS BIGINT) AS amount").
		InnerJoins("Stage", handle.Omit("*")).
		Group(dealTable + `.stage_key, "Stage".position, ` + dealTable + ".currency").
		Order(`"Stage".position, ` + dealTable + ".stage_key, " + dealTable + ".currency").
		Scan(&rows).Error
	if err != nil {
		log.FromContext(ctx).Error("computing the pipeline report", "error", err)
		writeError(c, nethttp.StatusServiceUnavailable, "the deal table cannot be read")
		return
	}
	report := pipelineReport{Tenant: string(self.TenantID), AsOf: time.Now().UTC(), Stages: rows}
	if report.Stages == nil {
		report.Stages = []stageTotal{}
	}
	body, err := json.Marshal(report)
	if err != nil {
		log.FromContext(ctx).Error("encoding the pipeline report", "error", err)
		writeError(c, nethttp.StatusInternalServerError, "the report cannot be encoded")
		return
	}
	if ttl > 0 {
		if err := p.store.Set(ctx, key, body, ttl); err != nil {
			log.FromContext(ctx).Error("caching the pipeline report; it is answered and not cached", "error", err)
		}
	}
	writeRawJSON(c, body)
}

// writeRawJSON answers with a JSON body already encoded, so a report from the
// cache and one just computed reach the client as the same bytes.
func writeRawJSON(c *gin.Context, body []byte) {
	c.Data(nethttp.StatusOK, "application/json", body)
}

// currencyOf reads the currency a report is asked for.
func currencyOf(c *gin.Context) (string, bool) {
	currency := c.Query("currency")
	if !currencyPattern.MatchString(currency) {
		writeError(c, nethttp.StatusBadRequest, "the currency query parameter is not a three-letter ISO 4217 code")
		return "", false
	}
	return currency, true
}

// leader is one row of the leaderboard.
type leader struct {
	OwnerID     string `json:"owner_id"`
	DisplayName string `json:"display_name"`
	Won         int64  `json:"won"`
	Amount      int64  `json:"amount"`
}

// leaderboardView is what the leaderboard answers with.
type leaderboardView struct {
	Tenant   string   `json:"tenant"`
	Currency string   `json:"currency"`
	Leaders  []leader `json:"leaders"`
}

// leaderboard answers who won the most, in one currency: the tenant's deals in
// a stage of kind won, grouped by owner, with the owners' names.
//
// The stages are joined through the deal's relation, which is how the filter
// knows to put the tenant condition on them. The owner is an account identifier
// and names no foreign key; its display name is read through the account store,
// one lookup per leader. The statement names no tenant: the deals are the
// scoped side, and the filter puts the condition on them and on their stages.
func (p *reportsProduct) leaderboard(c *gin.Context) {
	ctx := c.Request.Context()
	self, ok := memberOf(c, leaderboardRoute)
	if !ok {
		return
	}
	currency, ok := currencyOf(c)
	if !ok {
		return
	}
	handle := p.db.DB().WithContext(ctx)
	var rows []leader
	err := handle.Model(&Deal{}).
		Select(dealTable+".owner_id AS owner_id, COUNT(*) AS won, "+
			"CAST(SUM("+dealTable+".amount) AS BIGINT) AS amount").
		InnerJoins("Stage", handle.Omit("*")).
		Where(`"Stage".kind = ? AND `+dealTable+".currency = ?", stageWon, currency).
		Group(dealTable + ".owner_id").
		Order("amount DESC, owner_id").
		Scan(&rows).Error
	if err != nil {
		log.FromContext(ctx).Error("computing the leaderboard", "error", err)
		writeError(c, nethttp.StatusServiceUnavailable, "the deal table cannot be read")
		return
	}
	for i := range rows {
		rows[i].DisplayName = p.ownerName(ctx, rows[i].OwnerID)
	}
	if rows == nil {
		rows = []leader{}
	}
	c.JSON(nethttp.StatusOK, leaderboardView{Tenant: string(self.TenantID), Currency: currency, Leaders: rows})
}

// ownerName reads the display name of an account identifier through the account
// store, empty when it is empty or the account is gone.
func (p *reportsProduct) ownerName(ctx context.Context, id string) string {
	if id == "" {
		return ""
	}
	account, err := p.accounts.Get(ctx, authn.AccountID(id))
	if err != nil {
		return ""
	}
	return account.DisplayName
}

// revenueView is one currency's line of a tenant's won revenue.
type revenueView struct {
	Currency string `json:"currency"`
	Deals    int64  `json:"deals"`
	Amount   int64  `json:"amount"`
}

// revenueList is what the revenue route answers with.
type revenueList struct {
	Revenue []revenueView `json:"revenue"`
}

// revenue answers the tenant's won revenue by currency, as the revenue
// projection has counted it. The read names no tenant; the filter scopes it.
func (p *reportsProduct) revenue(c *gin.Context) {
	ctx := c.Request.Context()
	if _, ok := memberOf(c, revenueRoute); !ok {
		return
	}
	var rows []WonRevenue
	if err := p.db.DB().WithContext(ctx).Order("currency").Find(&rows).Error; err != nil {
		log.FromContext(ctx).Error("reading the won revenue", "error", err)
		writeError(c, nethttp.StatusServiceUnavailable, "the revenue table cannot be read")
		return
	}
	out := revenueList{Revenue: make([]revenueView, 0, len(rows))}
	for _, row := range rows {
		out.Revenue = append(out.Revenue, revenueView{Currency: row.Currency, Deals: row.Deals, Amount: row.Amount})
	}
	c.JSON(nethttp.StatusOK, out)
}

// deliveryView is one webhook delivery as the API shows it.
type deliveryView struct {
	TenantID     string    `json:"tenant_id"`
	EventID      string    `json:"event_id"`
	EventType    string    `json:"event_type"`
	Status       string    `json:"status"`
	Attempts     int       `json:"attempts"`
	ResponseCode int       `json:"response_code"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// deliveriesView is what the deliveries listing answers with.
type deliveriesView struct {
	Deliveries []deliveryView `json:"deliveries"`
}

// deliveries answers the tenant's most recent webhook deliveries. The table is
// the webhooks module's; this module reads it on the tenant's behalf, and the
// filter scopes the read.
func (p *reportsProduct) deliveries(c *gin.Context) {
	ctx := c.Request.Context()
	if _, ok := memberOf(c, deliveriesRoute); !ok {
		return
	}
	var rows []WebhookDelivery
	if err := p.db.DB().WithContext(ctx).Order("created_at DESC, event_id").Limit(deliveriesPage).Find(&rows).Error; err != nil {
		log.FromContext(ctx).Error("reading the webhook deliveries", "error", err)
		writeError(c, nethttp.StatusServiceUnavailable, "the deliveries cannot be read")
		return
	}
	out := deliveriesView{Deliveries: []deliveryView{}}
	for _, d := range rows {
		out.Deliveries = append(out.Deliveries, deliveryView{
			TenantID: string(d.TenantID), EventID: d.EventID, EventType: d.EventType, Status: d.Status,
			Attempts: d.Attempts, ResponseCode: d.ResponseCode, UpdatedAt: d.UpdatedAt,
		})
	}
	c.JSON(nethttp.StatusOK, out)
}

// dropPipeline is the module's handler for every deal event: each follows a
// write that changes what the report adds up, so the tenant's report moves on
// to a new generation.
//
// It issues no statement and enters no tenant. What it writes is a key named
// by the event's tenant, and it writes it for a disabled tenant too: a report
// left cached for such a tenant would be served stale the moment the tenant was
// served again. It writes it for an identifier the roster does not carry as
// well, since this replica's roster may not yet carry a tenant another replica
// cached a report for; the generation's lifetime bounds how long such a key
// stays. On a transport bus every replica receives the event and each moves the
// generation on, which invalidates as one move does.
func (p *reportsProduct) dropPipeline(ctx context.Context, evt event.Event) error {
	if !tenant.IsIdentifier(evt.TenantID) {
		p.log.Error("a deal event names no well-formed tenant; no report is invalidated",
			"event_id", evt.ID, "event_type", evt.Type)
		return nil
	}
	if err := advanceGeneration(ctx, p.store, tenant.ID(evt.TenantID)); err != nil {
		return fmt.Errorf("invalidating the pipeline report of %s: %w", evt.TenantID, err)
	}
	p.log.Info(pipelineInvalidatedMsg, "event_id", evt.ID, "event_type", evt.Type, "tenant_id", evt.TenantID)
	return nil
}

// init registers the reports module. It runs on serve: its routes sit on the API
// endpoint, and its subscriber keeps the cache the routes read.
func init() {
	registerApplication(application{
		module: reportsModule(),
		runsOn: []string{serveCommand},
		models: []any{Deal{}, PipelineStage{}, WonRevenue{}, WebhookDelivery{}},
	})
}

// reportsModule binds the report routes and subscribes the cache's
// invalidation to every deal event.
//
// It owns no table: it reads other modules' through the database handle. It
// requires the roster because every table it reads a tenant's rows from is
// scoped, and it takes up the declaration surface optionally: each of its routes
// declares the permission the authorization middleware decides it on from this
// module's own Init.
func reportsModule() core.Module {
	return core.Module{
		Name: reportsModuleName,
		Requires: []core.Requirement{
			{Token: (*log.Logger)(nil)},
			{Token: (*db.Database)(nil)},
			{Token: (*tenant.Catalog)(nil)},
			{Token: (*kv.KV)(nil)},
			{Token: (*settings.Store)(nil)},
			{Token: (*event.Bus)(nil)},
			{Token: (*authn.Accounts)(nil)},
			{Token: (*authz.Gate)(nil), Optional: true},
		},
		Resources: []any{
			reportsSettingsSchema(),
			speedhttp.Spec{Endpoint: apiEndpoint, Document: reportsOpenAPI},
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			logger, err := namedLogger(reg, reportsModuleName)
			if err != nil {
				return nil, err
			}
			database, err := core.Resolve[db.Database](reg)
			if err != nil {
				return nil, err
			}
			store, err := core.Resolve[kv.KV](reg)
			if err != nil {
				return nil, err
			}
			surface, err := core.Resolve[settings.Store](reg)
			if err != nil {
				return nil, err
			}
			accounts, err := core.Resolve[authn.Accounts](reg)
			if err != nil {
				return nil, err
			}
			return &reportsProduct{
				released: released{log: logger}, db: database, store: store, settings: surface, accounts: accounts,
			}, nil
		},

		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			product, ok := instance.(*reportsProduct)
			if !ok {
				return errForeignInstance(reportsModuleName, instance)
			}
			bus, err := core.Resolve[event.Bus](reg)
			if err != nil {
				return err
			}
			router, err := core.Resolve[speedhttp.Router](reg)
			if err != nil {
				return err
			}
			api, err := router.Endpoint(apiEndpoint)
			if err != nil {
				return err
			}
			mount(api, pipelineReportRoute, product.pipeline)
			mount(api, leaderboardRoute, product.leaderboard)
			mount(api, revenueRoute, product.revenue)
			mount(api, deliveriesRoute, product.deliveries)
			for _, eventType := range dealEventTypes {
				bus.Subscribe(eventType, reportsModuleName, product.dropPipeline)
			}
			return declareReportRoutes(reg)
		},

		Stop:  recordStop,
		Close: recordClose,
	}
}
