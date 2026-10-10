package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	nethttp "net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
)

// The deal feed publishes a deal.won for any tenant, under an identifier the
// caller may choose, on an operator's request. It is off unless a deployment
// turns it on, and nothing in the product turns it on: it is the narrowest way
// to hand the deal.won consumers an event the deals module never publishes,
// and every deal.won the cases can make through the deals module they make
// there.
//
// What only the feed publishes is an event whose envelope no committed move
// would carry:
//
//   - one identifier twice, when a case asks for it. The deals module stages
//     each event once, and the outbox's relay publishes it again only after
//     the bus refused it or the relay's claim on it ran out, so no flow of it
//     hands a consumer a second delivery of one event at a moment a case
//     chooses — which is what a consumer's claim and a delivery's row exist to
//     recognise, and what a claim's lifetime is measured by;
//   - one identifier in two tenants. Minted identifiers never meet, so no flow
//     shows that a consumer keeps two tenants' events apart by the tenant as
//     well as the identifier;
//   - a tenant the consumers do not serve: a disabled one, one never
//     registered, and one registered after the consumers last read the roster.
//     The deals module publishes for the tenant a request was resolved in, and
//     every process that resolves a tenant also runs the consumers that serve
//     it.
//
// The feed checks the envelope's shape and the payload's, and not the roster:
// the consumers' refusal of a tenant they do not serve is part of what it is
// there to exercise.
const dealFeedModuleName = "dealfeed"

// publishDealEventRoute is the feed's one route, on the admin endpoint.
const publishDealEventRoute = "POST /deal-events"

//go:embed openapi/dealfeed-admin.json
var dealFeedOpenAPI []byte

// dealFeedConfig is the feed's configuration. It is off unless a deployment
// turns it on.
type dealFeedConfig struct {
	Enabled bool `config:"enabled"`
}

// dealFeedSchema declares the feed's switch.
func dealFeedSchema() config.Schema {
	return config.Schema{
		Namespace: dealFeedModuleName,
		Mounts:    []config.Mount{{Value: &dealFeedConfig{}}},
		Items: map[string]config.Item{
			"enabled": {Description: "whether the admin endpoint serves " + publishDealEventRoute +
				", which lets a platform operator publish a deal.won for any tenant under any identifier; " +
				"it exists to hand the event consumers what the deals module never publishes, and is off by default"},
		},
	}
}

// dealFeedRequest is the body of a publish: the envelope's tenant and
// identifier, and the won deal the payload describes.
type dealFeedRequest struct {
	TenantID  string `json:"tenant_id"`
	ID        string `json:"id"`
	DealID    string `json:"deal_id"`
	AccountID string `json:"account_id"`
	Stage     string `json:"stage"`
	Amount    int64  `json:"amount"`
	Currency  string `json:"currency"`
	OwnerID   string `json:"owner_id"`
}

// maxEventIDLength bounds an identifier the operator supplies.
const maxEventIDLength = 128

var (
	errFeedTenant  = errors.New("the tenant identifier is not a well-formed identifier")
	errFeedEventID = errors.New("the event identifier carries blanks or control characters, or is longer than " +
		strconv.Itoa(maxEventIDLength) + " characters")
)

// won is the payload the request describes, won now. No activity records it
// and no member made it, so neither is named.
func (r dealFeedRequest) won(now time.Time) wonPayload {
	return wonPayload{
		Object: "deal/" + r.DealID, Outcome: stageWon, DealID: r.DealID, AccountID: r.AccountID, Stage: r.Stage,
		Amount: r.Amount, Currency: r.Currency, OwnerID: r.OwnerID, WonAt: now.Format(time.RFC3339Nano),
	}
}

// Validate checks the shape of the envelope and the payload.
func (r dealFeedRequest) Validate() error {
	switch {
	case !tenant.IsIdentifier(r.TenantID):
		return errFeedTenant
	case r.ID != "" && (!tenant.IsIdentifier(r.ID) || len(r.ID) > maxEventIDLength):
		return errFeedEventID
	}
	return r.won(time.Time{}).check()
}

// dealFeedView is what a publish answers with: the envelope as it was handed to
// the bus, the identifier included, since a publish cannot hand an assigned one
// back.
type dealFeedView struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	TenantID string `json:"tenant_id"`
}

// dealFeedProduct is the feed's product.
type dealFeedProduct struct {
	released
	bus event.Bus
}

// publish hands one deal.won to the bus. The identifier is the operator's when
// the request carries one; otherwise it is minted here, so the answer can name
// it.
func (p *dealFeedProduct) publish(c *gin.Context) {
	ctx := c.Request.Context()
	var body dealFeedRequest
	if !bindBody(c, &body) {
		return
	}
	id := body.ID
	if id == "" {
		id = event.NewID()
	}
	evt := event.Event{ID: id, Type: dealWonEvent, TenantID: body.TenantID, Payload: body.won(time.Now().UTC())}
	if err := p.bus.Publish(ctx, evt); err != nil {
		log.FromContext(ctx).Error("publishing a deal event", "event_id", id, "error", err)
		writeError(c, nethttp.StatusServiceUnavailable, "the event was not accepted by the bus")
		return
	}
	c.JSON(nethttp.StatusAccepted, dealFeedView{ID: id, Type: evt.Type, TenantID: evt.TenantID})
}

// init registers the deal feed. It runs on serve, where the admin endpoint is.
func init() {
	registerApplication(application{
		module:     dealFeedModule(),
		runsOn:     []string{serveCommand},
		journalled: []string{publishDealEventRoute},
	})
}

// dealFeedModule binds the feed's route when the configuration turns it on.
//
// The route sits on the admin endpoint, so the operator check there is what
// guards it: publishing a deal event for a tenant is a platform operator's act,
// never a tenant member's.
func dealFeedModule() core.Module {
	return core.Module{
		Name: dealFeedModuleName,
		Requires: []core.Requirement{
			{Token: (*log.Logger)(nil)},
			{Token: (*event.Bus)(nil)},
		},
		Resources: []any{
			dealFeedSchema(),
			speedhttp.Spec{Endpoint: adminEndpoint, Document: dealFeedOpenAPI},
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			logger, err := namedLogger(reg, dealFeedModuleName)
			if err != nil {
				return nil, err
			}
			bus, err := core.Resolve[event.Bus](reg)
			if err != nil {
				return nil, err
			}
			return &dealFeedProduct{released: released{log: logger}, bus: bus}, nil
		},

		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			product, ok := instance.(*dealFeedProduct)
			if !ok {
				return errForeignInstance(dealFeedModuleName, instance)
			}
			reader, err := core.Resolve[config.Reader](reg)
			if err != nil {
				return err
			}
			var cfg dealFeedConfig
			if err = reader.Decode(dealFeedModuleName, &cfg); err != nil {
				return fmt.Errorf("reading %s: %w", dealFeedModuleName, err)
			}
			if !cfg.Enabled {
				return nil
			}
			router, err := core.Resolve[speedhttp.Router](reg)
			if err != nil {
				return err
			}
			admin, err := router.Endpoint(adminEndpoint)
			if err != nil {
				return err
			}
			mount(admin, publishDealEventRoute, product.publish)
			return nil
		},

		Stop:  recordStop,
		Close: recordClose,
	}
}
