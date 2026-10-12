package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	nethttp "net/http"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
)

// flowModuleName is the name the flow module is registered under.
const flowModuleName = "flow"

// flowTable is the table the stage-flow model maps to.
const flowTable = "stage_flow"

// flowRoute answers a tenant's stage flow, on the API endpoint. It declares the
// flow read permission; the authorization middleware decides it and the handler
// runs no role comparison of its own.
const flowRoute = "GET /pipeline/flow"

// declareFlowRoutes declares the flow route's required permission on the
// route-declaration surface, so the enforcement middleware decides the route
// rather than the handler reading a role.
//
// The surface is taken up optionally: a command that runs the flow module without
// the enforcement layer has none to declare to, and its route there carries no
// requirement. A command that runs the layer holds every declaration to the
// permission catalog and to the process's translation once the Init window
// closes, so a route naming a permission the host does not declare fails the
// startup rather than deciding nothing at request time.
func declareFlowRoutes(reg *core.Registry) error {
	gate, err := core.Resolve[authz.Gate](reg)
	switch {
	case errors.Is(err, core.ErrMissingProvider):
		return nil
	case err != nil:
		return err
	}
	method, path := splitRoute(flowRoute)
	gate.Require(method, path, permFlowRead)
	return nil
}

// The records the subscriber writes for each deal.stage-changed it is handed,
// one per event and process: one that counted it, one that found it already
// counted, or one that could not act in its tenant. A case finds them by name,
// with the event's identifier as an attribute.
const (
	moveCountedMsg   = "a stage move is counted"
	moveSkippedMsg   = "a stage move already counted is skipped"
	moveNotServedMsg = "a stage move names a tenant this process does not serve; it is not counted"
)

//go:embed migrations/flow
var flowMigrationRoot embed.FS

var flowMigrations = subtree(flowMigrationRoot, "migrations/flow")

//go:embed openapi/flow.json
var flowOpenAPI []byte

// StageFlow is how many of one tenant's deals moved from one stage to another:
// the pipeline's conversion, read from the moves rather than from where the
// deals are now. It is a scoped model, written by the subscriber inside the
// tenant the event names and read by a request inside the tenant it names.
type StageFlow struct {
	tenant.Model
	FromStage string    `gorm:"column:from_stage;primaryKey"`
	ToStage   string    `gorm:"column:to_stage;primaryKey"`
	Moves     int64     `gorm:"column:moves;not null"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null"`
}

// TableName fixes the table this model maps to.
func (StageFlow) TableName() string { return flowTable }

// flowProduct is the flow module's product.
type flowProduct struct {
	released
	db     db.Database
	store  kv.KV
	roster tenant.Catalog
}

// countMove is the deal.stage-changed subscriber: it adds a move to its
// tenant's line for the two stages, once however many times the event is
// delivered.
//
// It acts as the revenue projection does: inside the event's tenant, entered
// through the roster, with a claim on the event's identifier in the key-value
// store so that of the replicas a transport hands the event to, one counts it.
// The claim lasts as long as the consumers' default lifetime. Its records name
// the event by the identifier the bus handed it.
func (p *flowProduct) countMove(ctx context.Context, evt event.Event) error {
	logger := p.log.With("event_id", evt.ID, "event_type", evt.Type)
	if evt.TenantID == "" {
		logger.Error("a stage move names no tenant; it is not counted")
		return nil
	}
	scoped, err := enterEventTenant(ctx, p.roster, p.log, evt)
	if err != nil {
		logger.Log(ctx, notServedLevel(err), moveNotServedMsg, "tenant_id", evt.TenantID, "cause", err.Error())
		return nil
	}
	logger = log.FromContext(scoped).With("event_id", evt.ID, "event_type", evt.Type)
	moved, err := readStageChanged(evt)
	if err != nil {
		logger.Error("a stage move's payload cannot be read; it is not counted", "error", err)
		return nil
	}
	claim := eventClaimKey(flowModuleName, tenant.ID(evt.TenantID), evt.ID)
	claimed, err := p.store.SetIfAbsent(scoped, claim, claimMarker, defaultClaimTTL)
	if err != nil {
		return fmt.Errorf("claiming stage move %s: %w", evt.ID, err)
	}
	if !claimed {
		logger.Info(moveSkippedMsg, "deal_id", moved.DealID)
		return nil
	}
	now := time.Now().UTC()
	row := StageFlow{FromStage: moved.FromStage, ToStage: moved.ToStage, Moves: 1, UpdatedAt: now}
	err = p.db.DB().WithContext(scoped).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "tenant_id"}, {Name: "from_stage"}, {Name: "to_stage"}},
		DoUpdates: clause.Set{
			{Column: clause.Column{Name: "moves"}, Value: gorm.Expr(flowTable+".moves + ?", 1)},
			{Column: clause.Column{Name: "updated_at"}, Value: now},
		},
	}).Create(&row).Error
	if err != nil {
		// The claim is given up so that the event, delivered again, is
		// counted then; a bus that does not redeliver loses it either way.
		if releaseErr := p.store.Delete(context.WithoutCancel(scoped), claim); releaseErr != nil {
			err = errors.Join(err, fmt.Errorf("giving the claim up: %w", releaseErr))
		}
		return fmt.Errorf("counting stage move %s: %w", evt.ID, err)
	}
	logger.Info(moveCountedMsg, "deal_id", moved.DealID, "activity_id", moved.ActivityID)
	return nil
}

// flowView is one line of a tenant's stage flow.
type flowView struct {
	FromStage string `json:"from_stage"`
	ToStage   string `json:"to_stage"`
	Moves     int64  `json:"moves"`
}

// flowList is what the flow route answers with.
type flowList struct {
	Flow []flowView `json:"flow"`
}

// listFlow answers the tenant's stage flow, one line for each two stages a
// deal moved between. The lines are found through the model into a type of
// their own, which selects the columns that type has; the statement names no
// tenant, and the filter scopes it by the model.
func (p *flowProduct) listFlow(c *gin.Context) {
	ctx := c.Request.Context()
	if _, ok := memberOf(c, flowRoute); !ok {
		return
	}
	lines := []flowView{}
	err := p.db.DB().WithContext(ctx).Model(&StageFlow{}).Order("from_stage, to_stage").Find(&lines).Error
	if err != nil {
		log.FromContext(ctx).Error("reading the stage flow", "error", err)
		writeError(c, nethttp.StatusServiceUnavailable, "the stage flow cannot be read")
		return
	}
	c.JSON(nethttp.StatusOK, flowList{Flow: lines})
}

// init registers the flow module. It runs on serve, where the stage moves that
// publish deal.stage-changed are made.
func init() {
	registerApplication(application{
		module: flowModule(),
		runsOn: []string{serveCommand},
		models: []any{StageFlow{}},
	})
}

// flowModule declares the stage-flow table, subscribes to deal.stage-changed
// and binds the flow route.
//
// Its requirements are the revenue projection's but the settings surface, for
// the same reasons: the roster for the filter and for Enter, the key-value
// store for the claims and the bus for the events. It takes up the declaration
// surface optionally: its route declares the permission the authorization
// middleware decides it on from this module's own Init. The table holds stage
// keys as plain columns rather than a reference to the stages table: it counts
// what the events said, and nothing it holds is read back through the stages.
func flowModule() core.Module {
	return core.Module{
		Name: flowModuleName,
		Requires: []core.Requirement{
			{Token: (*log.Logger)(nil)},
			{Token: (*db.Database)(nil)},
			{Token: (*tenant.Catalog)(nil)},
			{Token: (*kv.KV)(nil)},
			{Token: (*event.Bus)(nil)},
			{Token: (*authz.Gate)(nil), Optional: true},
		},
		Resources: []any{
			db.Migrations{FS: flowMigrations},
			speedhttp.Spec{Endpoint: apiEndpoint, Document: flowOpenAPI},
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			logger, err := namedLogger(reg, flowModuleName)
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
			roster, err := core.Resolve[tenant.Catalog](reg)
			if err != nil {
				return nil, err
			}
			return &flowProduct{released: released{log: logger}, db: database, store: store, roster: roster}, nil
		},

		// The subscription is made in Init: the bus freezes its table at its
		// own Start, which the drive reaches after every module's Init.
		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			product, ok := instance.(*flowProduct)
			if !ok {
				return errForeignInstance(flowModuleName, instance)
			}
			bus, err := core.Resolve[event.Bus](reg)
			if err != nil {
				return err
			}
			bus.Subscribe(dealStageChangedEvent, product.countMove)
			router, err := core.Resolve[speedhttp.Router](reg)
			if err != nil {
				return err
			}
			api, err := router.Endpoint(apiEndpoint)
			if err != nil {
				return fmt.Errorf("binding the flow route: %w", err)
			}
			mount(api, flowRoute, product.listFlow)
			return declareFlowRoutes(reg)
		},

		Stop:  recordStop,
		Close: recordClose,
	}
}
