package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	nethttp "net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/observability"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/tenant"
)

// webhooksModuleName is the name the webhooks module is registered under, and
// the namespace of its settings.
const webhooksModuleName = "webhooks"

// deliveriesTable is the table the delivery model maps to.
const deliveriesTable = "webhook_deliveries"

// The settings, under their full paths. Each is read per tenant, falling back
// to the global row and then to the declared default.
const (
	webhookURLSetting         = webhooksModuleName + ".url"
	webhookSecretSetting      = webhooksModuleName + ".secret"
	webhookTimeoutSetting     = webhooksModuleName + ".timeout"
	webhookMaxAttemptsSetting = webhooksModuleName + ".max-attempts"
	webhookRetryDelaySetting  = webhooksModuleName + ".retry-delay"
	webhookClaimTTLSetting    = webhooksModuleName + ".claim-ttl"
)

// The request headers a delivery carries besides its content type. The
// signature is the hex HMAC-SHA256 of the body under the tenant's secret,
// written sha256=<hex>, so a receiver holding the secret checks that the body
// came from this host and was not altered.
const (
	eventIDHeader   = "X-CRM-Event-Id"
	eventTypeHeader = "X-CRM-Event-Type"
	attemptHeader   = "X-CRM-Delivery-Attempt"
	signatureHeader = "X-CRM-Signature"
	signaturePrefix = "sha256="
)

// The states a delivery row is in. The migration's CHECK holds the same three.
const (
	deliveryPending   = "pending"
	deliveryDelivered = "delivered"
	deliveryFailed    = "failed"
)

// The records the module writes about each event it receives, one per event
// and process, naming how the process was done with it. Each carries the event
// identifier, and the tenant the event was entered under.
const (
	webhookDeliveredMsg        = "a webhook is delivered"
	webhookFailedMsg           = "a webhook could not be delivered"
	webhookAlreadyClaimedMsg   = "a webhook delivery was already claimed"
	webhookAlreadyDeliveredMsg = "a webhook was already delivered; it is not sent again"
	webhookExhaustedMsg        = "a webhook delivery has used every attempt; it is not attempted again"
	webhookInterruptedMsg      = "a webhook delivery was cut short by the process stopping; its row is left pending"
	webhookStoppingMsg         = "a deal event arrived while the process is stopping; no webhook is sent from here"
	webhookNotServedMsg        = "a deal event names a tenant this process does not serve; no webhook is sent"
	webhookUnusableMsg         = "the tenant's webhook settings cannot be delivered on, so nothing is sent"
)

// The bounds a tenant's delivery settings are held to. Each is far above what a
// receiver needs, and together they keep one delivery from holding one of the
// process's delivery slots for longer than an operator would wait on it.
const (
	maxWebhookTimeout    = 30 * time.Second
	maxWebhookAttempts   = 10
	maxWebhookRetryDelay = time.Minute
)

// maxDeliveries is how many deliveries one process runs at once. A deal event
// arriving while every slot is taken waits for one on the bus's goroutine, which
// is the backpressure the bus sees.
const maxDeliveries = 16

// recordTimeout bounds the statement that records an attempt. It runs on a
// context the process stopping does not cancel, so the attempt a stop cut short
// is still written down, and this is what keeps that write from holding the
// stop up.
const recordTimeout = 5 * time.Second

// errInterrupted is what the row of a delivery the process stopping cut short
// says.
var errInterrupted = errors.New("the process stopped before the delivery ended")

//go:embed migrations/webhooks
var webhooksMigrationRoot embed.FS

var webhooksMigrations = subtree(webhooksMigrationRoot, "migrations/webhooks")

// webhookSettings is the carrier of the module's setting items. The zero URL
// is a tenant without a webhook.
type webhookSettings struct {
	URL         string        `config:"url"`
	Secret      string        `config:"secret"`
	Timeout     time.Duration `config:"timeout"`
	MaxAttempts int           `config:"max-attempts"`
	RetryDelay  time.Duration `config:"retry-delay"`
	ClaimTTL    time.Duration `config:"claim-ttl"`
}

// webhooksSettingsSchema declares the settings, with their defaults on the
// prototype. The secret is sensitive, so a change event for it carries neither
// the old value nor the new one.
func webhooksSettingsSchema() settings.Schema {
	defaults := webhookSettings{
		Timeout:     5 * time.Second,
		MaxAttempts: 3,
		RetryDelay:  time.Second,
		ClaimTTL:    defaultClaimTTL,
	}
	return settings.Schema{
		Namespace: webhooksModuleName,
		Mounts:    []settings.Mount{{Value: &defaults}},
		Items: map[string]settings.Item{
			"url":    {Description: "the http or https URL a tenant's deal.won events are posted to; empty sends nothing"},
			"secret": {Description: "the key the body of each delivery is signed with", Sensitive: true},
			"timeout": {Description: "how long one delivery attempt waits for the receiver; positive, and at most " +
				maxWebhookTimeout.String()},
			"max-attempts": {Description: "how many times a delivery is attempted before it is recorded as failed; " +
				"from 1 to " + strconv.Itoa(maxWebhookAttempts)},
			"retry-delay": {Description: "the wait before the second attempt, growing by as much before each further one; " +
				"at most " + maxWebhookRetryDelay.String()},
			"claim-ttl": {Description: "how long a delivery's claim on an event lasts, so that a second delivery of " +
				"the event sends nothing; it must be positive and outlast the bus's redelivery"},
		},
	}
}

// WebhookDelivery is one event's delivery to a tenant's webhook, as the module
// records it: where it went, how many attempts it took and how it ended. It is
// scoped, and keyed by the event identifier within the tenant.
type WebhookDelivery struct {
	tenant.Model
	EventID      string    `gorm:"column:event_id;primaryKey"`
	EventType    string    `gorm:"column:event_type;not null"`
	URL          string    `gorm:"column:url;not null"`
	Status       string    `gorm:"column:status;not null"`
	Attempts     int       `gorm:"column:attempts;not null"`
	ResponseCode int       `gorm:"column:response_code;not null"`
	LastError    string    `gorm:"column:last_error;not null"`
	CreatedAt    time.Time `gorm:"column:created_at;not null"`
	UpdatedAt    time.Time `gorm:"column:updated_at;not null"`
}

// TableName fixes the table this model maps to.
func (WebhookDelivery) TableName() string { return deliveriesTable }

// webhookBody is what a delivery posts: the event's envelope, and its payload
// as the deal event contract has it.
type webhookBody struct {
	ID       string     `json:"id"`
	Type     string     `json:"type"`
	TenantID string     `json:"tenant_id"`
	Data     wonPayload `json:"data"`
}

// sign is the signature header's value for a body under a secret.
func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return signaturePrefix + hex.EncodeToString(mac.Sum(nil))
}

var (
	errWebhookURL = errors.New("the webhook URL is not an absolute http or https URL")
	errNoSecret   = errors.New("the webhook has no signing secret")
)

// webhooksProduct is the module's product.
type webhooksProduct struct {
	released
	db       db.Database
	store    kv.KV
	settings settings.Store
	roster   tenant.Catalog
	tracer   trace.Tracer
	// client carries the trace context of the delivery's span to the
	// receiver. Each attempt is bounded by a context of its own, so the
	// client sets no timeout.
	client *nethttp.Client

	// The deliveries the handler has handed over run on goroutines of this
	// module's own, one per delivery and at most maxDeliveries at once: a slot
	// is taken from slots and given back when the delivery ends. life is
	// cancelled by Stop, which cuts every delivery in flight short, and
	// running is what Stop waits on. stopping, under mu, refuses a delivery
	// the bus hands over after Stop.
	slots    chan struct{}
	life     context.Context
	endLife  context.CancelFunc
	mu       sync.Mutex
	stopping bool
	running  sync.WaitGroup
}

// tenantWebhook reads a tenant's webhook settings.
func (p *webhooksProduct) tenantWebhook(id tenant.ID) (webhookSettings, error) {
	layer := settings.ForTenant(string(id))
	var (
		cfg webhookSettings
		err error
	)
	read := func(key string, target any) {
		if err == nil {
			err = p.settings.Get(layer, key, target)
		}
	}
	read(webhookURLSetting, &cfg.URL)
	read(webhookSecretSetting, &cfg.Secret)
	read(webhookTimeoutSetting, &cfg.Timeout)
	read(webhookMaxAttemptsSetting, &cfg.MaxAttempts)
	read(webhookRetryDelaySetting, &cfg.RetryDelay)
	read(webhookClaimTTLSetting, &cfg.ClaimTTL)
	return cfg, err
}

// check refuses a tenant's settings a delivery cannot be made on. The settings
// surface holds whatever was written under the declared type, so the domain of
// each value is checked here, by the module that reads it.
func (cfg webhookSettings) check() error {
	target, err := url.Parse(cfg.URL)
	switch {
	case err != nil || !target.IsAbs() || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https"):
		return errWebhookURL
	case cfg.Secret == "":
		return errNoSecret
	case cfg.Timeout <= 0 || cfg.Timeout > maxWebhookTimeout:
		return fmt.Errorf("%s is %s and has to be positive and at most %s", webhookTimeoutSetting, cfg.Timeout,
			maxWebhookTimeout)
	case cfg.MaxAttempts <= 0 || cfg.MaxAttempts > maxWebhookAttempts:
		return fmt.Errorf("%s is %d and has to be from 1 to %d", webhookMaxAttemptsSetting, cfg.MaxAttempts,
			maxWebhookAttempts)
	case cfg.RetryDelay < 0 || cfg.RetryDelay > maxWebhookRetryDelay:
		return fmt.Errorf("%s is %s and has to be from 0 to %s", webhookRetryDelaySetting, cfg.RetryDelay,
			maxWebhookRetryDelay)
	case cfg.ClaimTTL <= 0:
		// A claim stored without an expiry is kept for ever.
		return fmt.Errorf("%s is %s and has to be positive", webhookClaimTTLSetting, cfg.ClaimTTL)
	}
	return nil
}

// deliver is the module's handler for deal.won.
//
// It enters the event's tenant through the roster, as the revenue projection
// does and for the same reasons (see enterEventTenant). A tenant the roster does
// not carry or does not serve gets nothing sent, and neither does one with no
// webhook configured.
//
// Every process that runs this module receives each event on a transport bus,
// so the delivery is claimed first, keyed by the tenant and the event
// identifier; the process that stores the claim delivers and the others stop.
// The delivery row is the second record of it: it is found or created by the
// event identifier, and a row already delivered, or one that has used every
// attempt, sends nothing again.
//
// The attempts do not run here. The bus calls its handlers one after another
// on one goroutine per event type — the in-process bus on the publisher's,
// which for a deal event is the outbox relay's — so a receiver that does not
// answer would hold up every later deal.won of every tenant, the revenue
// projection and the report cache included, and a stop would cut it short at
// the bus's grace. The handler takes one of the process's delivery slots,
// claims the event and finds its row, and hands the attempts to a goroutine of
// the module's own that runs on the entered context without its cancellation.
// The slot is taken before the claim, so an event the handler gives up on
// while waiting for one is left unclaimed for another process.
func (p *webhooksProduct) deliver(ctx context.Context, evt event.Event) error {
	logger := p.log.With("event_id", evt.ID, "event_type", evt.Type)
	if evt.TenantID == "" {
		logger.Error("a deal event names no tenant; no webhook is sent")
		return nil
	}
	id := tenant.ID(evt.TenantID)
	entered, err := enterEventTenant(ctx, p.roster, p.log, evt)
	if err != nil {
		logger.Log(ctx, notServedLevel(err), webhookNotServedMsg, "tenant_id", evt.TenantID, "cause", err.Error())
		return nil
	}
	logger = log.FromContext(entered).With("event_id", evt.ID, "event_type", evt.Type)
	cfg, err := p.tenantWebhook(id)
	if err != nil {
		return fmt.Errorf("reading the webhook settings: %w", err)
	}
	if cfg.URL == "" {
		logger.Debug("the tenant has no webhook, so nothing is sent")
		return nil
	}
	if err = cfg.check(); err != nil {
		logger.Error(webhookUnusableMsg, "error", err)
		return nil
	}
	won, err := readWon(evt)
	if err != nil {
		logger.Error("a won deal's payload cannot be read; no webhook is sent", "error", err)
		return nil
	}
	body, err := json.Marshal(webhookBody{ID: evt.ID, Type: evt.Type, TenantID: evt.TenantID, Data: won})
	if err != nil {
		return fmt.Errorf("encoding the webhook body: %w", err)
	}

	release, admitted, err := p.admit(ctx)
	if err != nil {
		return fmt.Errorf("waiting for a delivery slot for %s: %w", evt.ID, err)
	}
	if !admitted {
		logger.Info(webhookStoppingMsg)
		return nil
	}
	handedOver := false
	defer func() {
		if !handedOver {
			release()
		}
	}()

	claimed, err := p.store.SetIfAbsent(entered, eventClaimKey(webhooksModuleName, id, evt.ID), claimMarker, cfg.ClaimTTL)
	if err != nil {
		return fmt.Errorf("claiming the delivery of %s: %w", evt.ID, err)
	}
	if !claimed {
		logger.Info(webhookAlreadyClaimedMsg)
		return nil
	}

	// The row names no tenant: the filter fills it from the context Enter
	// returned, and conditions the look-up on it, so an event identifier
	// another tenant's delivery carries is not this tenant's row.
	var row WebhookDelivery
	err = p.db.DB().WithContext(entered).
		Where(WebhookDelivery{EventID: evt.ID}).
		Attrs(WebhookDelivery{EventType: evt.Type, URL: cfg.URL, Status: deliveryPending}).
		FirstOrCreate(&row).Error
	if err != nil {
		return fmt.Errorf("recording the delivery of %s: %w", evt.ID, err)
	}
	switch {
	case row.Status == deliveryDelivered:
		logger.Info(webhookAlreadyDeliveredMsg, "delivered_at", row.UpdatedAt)
		return nil
	case row.Status == deliveryFailed || row.Attempts >= cfg.MaxAttempts:
		logger.Info(webhookExhaustedMsg, "status", row.Status, "attempts", row.Attempts)
		return nil
	}

	handedOver = true
	done := row.Attempts
	attempts, cancel := context.WithCancel(context.WithoutCancel(entered))
	unwatch := context.AfterFunc(p.life, cancel)
	go func() {
		defer release()
		defer cancel()
		defer unwatch()
		p.attempt(attempts, logger, cfg, evt, body, done)
	}()
	return nil
}

// admit takes one of the process's delivery slots, waiting while every one is
// in use, and reports false once Stop has begun: a delivery the bus hands over
// then is not started, and not claimed, so a process still running may take it.
// The release it returns gives the slot back and tells Stop the delivery is done.
func (p *webhooksProduct) admit(ctx context.Context) (release func(), admitted bool, err error) {
	p.mu.Lock()
	if p.stopping {
		p.mu.Unlock()
		return nil, false, nil
	}
	p.running.Add(1)
	p.mu.Unlock()
	select {
	case p.slots <- struct{}{}:
		return func() {
			<-p.slots
			p.running.Done()
		}, true, nil
	case <-p.life.Done():
		p.running.Done()
		return nil, false, nil
	case <-ctx.Done():
		p.running.Done()
		return nil, false, ctx.Err()
	}
}

// drain is the module's part of Stop: no delivery starts after it, every
// delivery in flight is cut short, and it returns once each has recorded where
// it stopped. A delivery cut short keeps its claim and leaves its row pending.
func (p *webhooksProduct) drain() {
	p.mu.Lock()
	p.stopping = true
	p.mu.Unlock()
	p.endLife()
	p.running.Wait()
}

// attempt posts the body until the receiver takes it, the attempts run out or
// the process stops, recording each attempt on the delivery row as it ends. ctx
// is cancelled when the process stops.
func (p *webhooksProduct) attempt(ctx context.Context, logger *slog.Logger, cfg webhookSettings, evt event.Event,
	body []byte, done int,
) {
	ctx, span := p.tracer.Start(ctx, "webhooks deliver "+evt.Type,
		trace.WithAttributes(attribute.String("crm.event.id", evt.ID), attribute.String("crm.tenant.id", evt.TenantID)))
	defer span.End()
	interrupted := func(attempts int) {
		span.SetStatus(codes.Error, errInterrupted.Error())
		logger.Warn(webhookInterruptedMsg, "attempts", attempts)
	}

	var (
		code    int
		lastErr error
	)
	for n := done + 1; n <= cfg.MaxAttempts; n++ {
		if n > done+1 {
			wait := cfg.RetryDelay * time.Duration(n-done-1)
			select {
			case <-ctx.Done():
				interrupted(n - 1)
				return
			case <-time.After(wait):
			}
		}
		code, lastErr = p.post(ctx, cfg, evt, body, n)
		if ctx.Err() != nil {
			// The request was made, so the attempt counts: the receiver may
			// have taken it before the stop cut the answer off.
			if err := p.record(ctx, evt, n, deliveryPending, 0, errInterrupted.Error()); err != nil {
				logger.Error("recording the attempt a stop cut short", "attempt", n, "error", err)
			}
			interrupted(n)
			return
		}
		status, message := deliveryFailed, ""
		switch {
		case lastErr != nil:
			message = lastErr.Error()
		case code < 200 || code > 299:
			message = "the receiver answered " + strconv.Itoa(code)
		default:
			status = deliveryDelivered
		}
		if status != deliveryDelivered && n < cfg.MaxAttempts {
			status = deliveryPending
		}
		if err := p.record(ctx, evt, n, status, code, message); err != nil {
			span.SetStatus(codes.Error, err.Error())
			logger.Error("recording a delivery attempt; the delivery stops here and keeps its claim",
				"attempt", n, "error", err)
			return
		}
		if status == deliveryDelivered {
			logger.Info(webhookDeliveredMsg, "attempts", n, "response_code", code)
			return
		}
	}
	span.SetStatus(codes.Error, "the delivery failed")
	failure := "the receiver answered " + strconv.Itoa(code)
	if lastErr != nil {
		failure = lastErr.Error()
	}
	logger.Warn(webhookFailedMsg, "attempts", cfg.MaxAttempts, "response_code", code, "error", failure)
}

// record writes one attempt's outcome on the delivery row. The update names the
// event alone, and the filter keeps it in the tenant the context was entered
// under. It runs without the cancellation of ctx, within recordTimeout, so an
// attempt a stop cut short is recorded too.
func (p *webhooksProduct) record(ctx context.Context, evt event.Event, attempts int, status string, code int,
	message string,
) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer cancel()
	return p.db.DB().WithContext(ctx).Model(&WebhookDelivery{}).Where("event_id = ?", evt.ID).
		Updates(map[string]any{"attempts": attempts, "status": status, "response_code": code, "last_error": message}).Error
}

// post makes one attempt. Its span is a client span, and the client's
// transport injects its context into the request, so the receiver can continue
// the trace.
func (p *webhooksProduct) post(ctx context.Context, cfg webhookSettings, evt event.Event, body []byte, attempt int) (int, error) {
	ctx, span := p.tracer.Start(ctx, "webhooks POST", trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.Int("crm.delivery.attempt", attempt)))
	defer span.End()
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, cfg.URL, bytes.NewReader(body))
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", hostVersion)
	req.Header.Set(eventIDHeader, evt.ID)
	req.Header.Set(eventTypeHeader, evt.Type)
	req.Header.Set(attemptHeader, strconv.Itoa(attempt))
	req.Header.Set(signatureHeader, sign(cfg.Secret, body))
	resp, err := p.client.Do(req)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	// The receiver's answer is read to its end, within a bound, so the
	// connection can be reused; what it says is not recorded.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	span.SetAttributes(attribute.Int("http.response.status_code", resp.StatusCode))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		span.SetStatus(codes.Error, "the receiver answered "+strconv.Itoa(resp.StatusCode))
	}
	return resp.StatusCode, nil
}

// init registers the webhooks module. It runs on serve and on the worker, for
// the reason the revenue projection does: the serving process is the only one
// the in-process bus reaches, and on a transport the claim makes one of the
// processes that receive an event deliver it.
func init() {
	registerApplication(application{
		module: webhooksModule(),
		runsOn: []string{serveCommand, workerCommand},
		models: []any{WebhookDelivery{}},
	})
}

// webhooksModule declares the deliveries table and the settings, and subscribes
// the delivery.
//
// It requires the roster twice over: for Enter, and as the guard every module
// issuing statements on a scoped model states, since the filter is the roster's
// plugin. The tracer provider is required because each delivery is a span, and
// a delivery without one would carry no trace context to the receiver.
func webhooksModule() core.Module {
	return core.Module{
		Name: webhooksModuleName,
		Requires: []core.Requirement{
			{Token: (*log.Logger)(nil)},
			{Token: (*db.Database)(nil)},
			{Token: (*kv.KV)(nil)},
			{Token: (*settings.Store)(nil)},
			{Token: (*tenant.Catalog)(nil)},
			{Token: (*event.Bus)(nil)},
			{Token: (*observability.TracerProvider)(nil)},
		},
		Resources: []any{
			db.Migrations{FS: webhooksMigrations},
			webhooksSettingsSchema(),
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			logger, err := namedLogger(reg, webhooksModuleName)
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
			provider, err := core.Resolve[observability.TracerProvider](reg)
			if err != nil {
				return nil, err
			}
			life, endLife := context.WithCancel(context.Background())
			return &webhooksProduct{
				released: released{log: logger},
				db:       database,
				store:    store,
				settings: surface,
				roster:   roster,
				tracer:   provider.Tracer(hostName + "/" + webhooksModuleName),
				client:   &nethttp.Client{Transport: observability.Transport(nil)},
				slots:    make(chan struct{}, maxDeliveries),
				life:     life,
				endLife:  endLife,
			}, nil
		},

		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			product, ok := instance.(*webhooksProduct)
			if !ok {
				return errForeignInstance(webhooksModuleName, instance)
			}
			bus, err := core.Resolve[event.Bus](reg)
			if err != nil {
				return err
			}
			bus.Subscribe(dealWonEvent, webhooksModuleName, product.deliver)
			return nil
		},

		// Stop ends the module's own deliveries and waits for them to record
		// where they stopped. It releases nothing another module's handler
		// uses: the database handle each delivery records through is the
		// database module's, released in its Close after every Stop.
		Stop: func(ctx context.Context, reg *core.Registry, instance any) error {
			if product, ok := instance.(*webhooksProduct); ok && product != nil {
				product.drain()
			}
			return recordStop(ctx, reg, instance)
		},
		Close: recordClose,
	}
}
