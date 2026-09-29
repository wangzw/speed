package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
)

// The deal events: the one contract the deals module publishes by and every
// consumer reads by.
//
// The deals module stages each event in the transaction of the write it
// announces, through the outbox, and the outbox's relay publishes it once that
// transaction has committed: no consumer hears of a write that did not happen,
// and the event of a write that stands reaches the bus however often the bus
// refuses it first. The relay publishes at least once, so a consumer hears an
// event again under the identifier it was staged with:
//
//   - deal.created, deal.changed and deal.deleted: a deal was created, changed
//     or removed from the pipeline.
//   - deal.stage-changed: a deal moved from one of its tenant's stages to
//     another.
//   - deal.won: a deal moved into a stage of kind won. It is published beside
//     that move's deal.stage-changed.
//
// The envelope's TenantID is the tenant the write was made in. Its ID is
// minted in the form event.NewID gives, once for each event: by the outbox as
// it stages a creation's, a change's or a deletion's, and by a move for both
// of its events before its transaction, which records them on the move's
// activity — that is what traces an event a consumer received back to the
// write. Every bus hands every replica the same identifier, every publication
// of one staged event carries it, and a consumer whose effect lands outside
// its own process claims the event by it.
//
// Every payload names the deal as object and what became of it as outcome, the
// two members the journal reads, and the one who wrote as actor_id. An amount
// is in the currency's minor units, and travels as a JSON integer whatever its
// size.
const (
	dealCreatedEvent      = "deal.created"
	dealChangedEvent      = "deal.changed"
	dealDeletedEvent      = "deal.deleted"
	dealStageChangedEvent = "deal.stage-changed"
	dealWonEvent          = "deal.won"
)

// dealEventTypes is every deal event. Each follows a write that changes what a
// report over the deals table adds up.
var dealEventTypes = []string{
	dealCreatedEvent, dealChangedEvent, dealDeletedEvent, dealStageChangedEvent, dealWonEvent,
}

// The outcomes the three write events carry. A stage change's outcome is the
// kind of the stage the deal entered, and a win's is won.
const (
	dealCreated = "created"
	dealChanged = "changed"
	dealDeleted = "deleted"
)

// dealWrittenPayload is what deal.created, deal.changed and deal.deleted carry.
// They announce that the deal is not what it was; a consumer that needs the
// deal as it now is reads it.
type dealWrittenPayload struct {
	Object  string `json:"object"`
	Outcome string `json:"outcome"`
	DealID  string `json:"deal_id"`
	ActorID string `json:"actor_id"`
}

// stageChangedPayload is what deal.stage-changed carries: the two stages, the
// deal's amount and version as the move left them, and the activity that
// records the move.
type stageChangedPayload struct {
	Object     string `json:"object"`
	Outcome    string `json:"outcome"`
	DealID     string `json:"deal_id"`
	FromStage  string `json:"from_stage"`
	ToStage    string `json:"to_stage"`
	Amount     int64  `json:"amount"`
	Currency   string `json:"currency"`
	Version    int64  `json:"version"`
	ActivityID string `json:"activity_id"`
	ActorID    string `json:"actor_id"`
}

// wonPayload is what deal.won carries: the deal that was won, whose it is, and
// how much it is worth.
type wonPayload struct {
	Object     string `json:"object"`
	Outcome    string `json:"outcome"`
	DealID     string `json:"deal_id"`
	AccountID  string `json:"account_id"`
	Stage      string `json:"stage"`
	Amount     int64  `json:"amount"`
	Currency   string `json:"currency"`
	OwnerID    string `json:"owner_id"`
	WonAt      string `json:"won_at"`
	ActivityID string `json:"activity_id"`
	ActorID    string `json:"actor_id"`
}

var errNoDealID = errors.New("the deal event names no deal")

// check refuses a won deal a consumer cannot act on.
func (w wonPayload) check() error {
	switch {
	case strings.TrimSpace(w.DealID) == "":
		return errNoDealID
	case !currencyPattern.MatchString(w.Currency):
		return errBadCurrency
	case w.Amount < 0:
		return errNegativeAmount
	}
	return nil
}

// readWon decodes deal.won's payload. It decodes into the contract's type
// rather than probing members, because the amount is an integer that has to
// arrive exactly over a transport as well as in process.
func readWon(evt event.Event) (wonPayload, error) {
	var won wonPayload
	if err := event.DecodePayload(evt.Payload, &won); err != nil {
		return wonPayload{}, err
	}
	if err := won.check(); err != nil {
		return wonPayload{}, err
	}
	return won, nil
}

// readStageChanged decodes deal.stage-changed's payload.
func readStageChanged(evt event.Event) (stageChangedPayload, error) {
	var moved stageChangedPayload
	if err := event.DecodePayload(evt.Payload, &moved); err != nil {
		return stageChangedPayload{}, err
	}
	if strings.TrimSpace(moved.DealID) == "" {
		return stageChangedPayload{}, errNoDealID
	}
	return moved, nil
}

// defaultClaimTTL is how long a consumer's claim on an event lasts when no
// setting says otherwise. It outlasts the longest a backend keeps an event for
// redelivery.
const defaultClaimTTL = 48 * time.Hour

// eventClaimKey is where one consumer's claim on one event of one tenant is
// stored. The consumer is part of the key, so each consumer of an event claims
// it for itself, and so is the tenant, so two tenants' events are two events
// whatever identifiers their publishers gave them.
func eventClaimKey(consumer string, id tenant.ID, eventID string) string {
	return storeKey(consumer, "claim", string(id), keyDigest(eventID))
}

// claimMarker is the value a claim is stored with: the process that made it,
// which is what an operator looking at a claim key wants to know. A claim's
// value is never read back; the key's presence is the claim.
var claimMarker = func() []byte {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown-host"
	}
	return []byte(host + "/" + strconv.Itoa(os.Getpid()))
}()

// enterEventTenant is the context a consumer acts in for one event: its own
// logger, then the event's tenant entered through the roster.
//
// The tenant is entered from the event rather than read off the context the bus
// hands over: the in-process bus calls a subscriber with the publisher's
// context — the outbox relay's for a staged deal event, which carries no
// tenant, and the publishing request's for one published directly — and a
// transport with one of its own carrying no tenant, and entering from the
// event is what makes them all behave alike. The logger is replaced for the
// same reason: neither the relay's context nor a transport's carries this
// run's logger, so a record through it would leave the configured
// destinations, and a request's carries that request's. Enter binds the
// tenant onto the consumer's own.
func enterEventTenant(ctx context.Context, roster tenant.Catalog, logger *slog.Logger, evt event.Event) (context.Context, error) {
	return roster.Enter(log.WithLogger(ctx, logger), tenant.ID(evt.TenantID))
}

// notServedLevel is the level a consumer records a refusal to enter an event's
// tenant at. A disabled tenant is a state the event is dropped for; an
// identifier the roster's table does not carry is a publisher's mistake, and
// is worth an operator's look, as is a roster that could not be read at all.
//
// A tenant registered since this process last read the roster is not refused:
// the roster reads its table for an identifier its snapshot does not hold, so
// the event of a tenant another process registered a moment ago is acted on
// at once, with no reread of the whole roster to wait for.
func notServedLevel(err error) slog.Level {
	if errors.Is(err, tenant.ErrTenantDisabled) {
		return slog.LevelInfo
	}
	return slog.LevelWarn
}
