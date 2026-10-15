package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/tenant"
)

// revenueModuleName is the name the won-revenue projection is registered
// under, and the namespace of its setting.
const revenueModuleName = "revenue"

// revenueTable is the table the won-revenue model maps to.
const revenueTable = "won_revenue"

// revenueClaimTTLSetting is how long the projection's claim on an event lasts,
// under its full path.
const revenueClaimTTLSetting = revenueModuleName + ".claim-ttl"

// The records the projection writes about each deal.won it receives, one per
// event and process, naming how the process was done with it. Each carries the
// event's identifier, and the tenant the event was entered under.
const (
	wonCountedMsg   = "a won deal is counted"
	wonSkippedMsg   = "a won deal already counted is skipped"
	wonNotServedMsg = "a won deal names a tenant this process does not serve; it is not counted"
)

//go:embed migrations/revenue
var revenueMigrationRoot embed.FS

var revenueMigrations = subtree(revenueMigrationRoot, "migrations/revenue")

// revenueSettings is the carrier of the projection's setting item.
type revenueSettings struct {
	ClaimTTL time.Duration `config:"claim-ttl"`
}

// revenueSettingsSchema declares the claim lifetime.
func revenueSettingsSchema() settings.Schema {
	return settings.Schema{
		Namespace: revenueModuleName,
		Mounts:    []settings.Mount{{Value: &revenueSettings{ClaimTTL: defaultClaimTTL}}},
		Items: map[string]settings.Item{
			"claim-ttl": {Description: "how long the won-revenue projection remembers an event it counted, so that a " +
				"second delivery of it is not counted again; it must be positive and outlast the bus's redelivery"},
		},
	}
}

// WonRevenue is one tenant's wins in one currency: how many deal.won events
// were counted, and their amounts added up exactly. Amounts in two currencies
// do not add up, so each currency counts apart. It is a projection of the
// events rather than of the deals table: a deal won again after it left the
// won stage is counted again, and nothing but a deal.won moves it, so a change
// of a won deal's amount and its deletion leave it as it was. It is a scoped
// model, written by the projection inside the tenant the event names and read
// by a request inside the tenant it names.
type WonRevenue struct {
	tenant.Model
	Currency  string    `gorm:"column:currency;primaryKey"`
	Deals     int64     `gorm:"column:deals;not null"`
	Amount    int64     `gorm:"column:amount;not null"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null"`
}

// TableName fixes the table this model maps to.
func (WonRevenue) TableName() string { return revenueTable }

// revenueProduct is the projection's product.
type revenueProduct struct {
	released
	db       db.Database
	store    kv.KV
	settings settings.Store
	roster   tenant.Catalog
}

// countWin is the deal.won subscriber: it adds a won deal to its tenant's line
// for the deal's currency, once however many times and wherever the event is
// delivered.
//
// It acts inside the event's tenant, entered through the roster (see
// enterEventTenant): a tenant the roster does not carry or does not serve is
// refused there, and its event is recorded and not counted. Inside, the
// statement names no tenant; the filter scopes and fills it as it does a
// request's, and the records carry the tenant Enter bound.
//
// Every replica on a transport bus receives every event, and so does a worker
// beside them, so the event is claimed first: of the processes that receive it,
// the one that stores the claim counts it and the others see the claim and
// stop. The claim is keyed by the tenant and the event's identifier and lasts
// as long as the claim-ttl setting says for the tenant.
//
// The claim and the count are two writes to two stores. A count that fails
// gives the claim up, so a later delivery of the event counts it; a process
// that stops between the two leaves the event claimed and not counted, which a
// count one event short says, rather than one counted twice.
func (p *revenueProduct) countWin(ctx context.Context, evt event.Event) error {
	logger := p.log.With("event_id", evt.ID, "event_type", evt.Type)
	if evt.TenantID == "" {
		logger.Error("a won deal names no tenant; it is not counted")
		return nil
	}
	scoped, err := enterEventTenant(ctx, p.roster, p.log, evt)
	if err != nil {
		logger.Log(ctx, notServedLevel(err), wonNotServedMsg, "tenant_id", evt.TenantID, "cause", err.Error())
		return nil
	}
	logger = log.FromContext(scoped).With("event_id", evt.ID, "event_type", evt.Type)
	won, err := readWon(evt)
	if err != nil {
		logger.Error("a won deal's payload cannot be read; it is not counted", "error", err)
		return nil
	}
	id := tenant.ID(evt.TenantID)
	ttl, err := settings.Get[time.Duration](p.settings, settings.ForTenant(evt.TenantID), revenueClaimTTLSetting)
	if err != nil {
		return fmt.Errorf("reading %s: %w", revenueClaimTTLSetting, err)
	}
	// A claim stored without an expiry is kept for ever, so a lifetime that
	// is not positive is refused rather than passed on.
	if ttl <= 0 {
		logger.Error("the claim lifetime is not positive, so the won deal is not counted",
			"setting", revenueClaimTTLSetting, "value", ttl.String())
		return nil
	}
	claim := eventClaimKey(revenueModuleName, id, evt.ID)
	claimed, err := p.store.SetIfAbsent(scoped, claim, claimMarker, ttl)
	if err != nil {
		return fmt.Errorf("claiming won deal %s: %w", evt.ID, err)
	}
	if !claimed {
		logger.Info(wonSkippedMsg, "deal_id", won.DealID)
		return nil
	}
	now := time.Now().UTC()
	row := WonRevenue{Currency: won.Currency, Deals: 1, Amount: won.Amount, UpdatedAt: now}
	err = p.db.DB().WithContext(scoped).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "tenant_id"}, {Name: "currency"}},
		DoUpdates: clause.Assignments(map[string]any{
			"deals":      gorm.Expr(revenueTable+".deals + ?", 1),
			"amount":     gorm.Expr(revenueTable+".amount + ?", won.Amount),
			"updated_at": now,
		}),
	}).Create(&row).Error
	if err != nil {
		if releaseErr := p.store.Delete(context.WithoutCancel(scoped), claim); releaseErr != nil {
			err = errors.Join(err, fmt.Errorf("giving the claim up: %w", releaseErr))
		}
		return fmt.Errorf("counting won deal %s: %w", evt.ID, err)
	}
	logger.Info(wonCountedMsg, "deal_id", won.DealID, "currency", won.Currency, "amount", won.Amount)
	return nil
}

// init registers the projection. It runs on serve and on the worker: on the
// in-process bus the serving process is the only one an event reaches, and on a
// transport every process that runs it receives each event and the claim keeps
// all but one of them from counting it. The route that reads the table is the
// reports module's, on serve, since the worker runs no HTTP entry point.
func init() {
	registerApplication(application{
		module: revenueModule(),
		runsOn: []string{serveCommand, workerCommand},
		models: []any{WonRevenue{}},
	})
}

// revenueModule declares the revenue table and the claim setting, and
// subscribes the projection to deal.won.
//
// It requires the roster twice over: for the filter that scopes its table, and
// for Enter, which is how its subscriber acts inside one tenant. The key-value
// store holds the claims, the settings surface their lifetime, and the bus is
// where the events come from; each is an exclusive provision the command names.
func revenueModule() core.Module {
	return core.Module{
		Name: revenueModuleName,
		Requires: []core.Requirement{
			{Token: (*log.Logger)(nil)},
			{Token: (*db.Database)(nil)},
			{Token: (*tenant.Catalog)(nil)},
			{Token: (*kv.KV)(nil)},
			{Token: (*settings.Store)(nil)},
			{Token: (*event.Bus)(nil)},
		},
		Resources: []any{
			db.Migrations{FS: revenueMigrations},
			revenueSettingsSchema(),
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			logger, err := namedLogger(reg, revenueModuleName)
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
			roster, err := core.Resolve[tenant.Catalog](reg)
			if err != nil {
				return nil, err
			}
			return &revenueProduct{
				released: released{log: logger}, db: database, store: store, settings: surface, roster: roster,
			}, nil
		},

		// The subscription is made in Init: every bus freezes its table when
		// it starts, which the drive reaches after every module's Init.
		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			product, ok := instance.(*revenueProduct)
			if !ok {
				return errForeignInstance(revenueModuleName, instance)
			}
			bus, err := core.Resolve[event.Bus](reg)
			if err != nil {
				return err
			}
			bus.Subscribe(dealWonEvent, revenueModuleName, product.countWin)
			return nil
		},

		Stop:  recordStop,
		Close: recordClose,
	}
}
