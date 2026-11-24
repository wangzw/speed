// Package delivery is the notification capability's outbound pipeline: a
// delivery intent is validated and enqueued as one job, then delivered per
// channel when the job is consumed, one ledger row per attempt.
package delivery

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/feature"
	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/jobs"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/mail"
	"github.com/vislake/speed/pkg/notification"
	"github.com/vislake/speed/pkg/observability"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/sms"
	"github.com/vislake/speed/pkg/sms/budget"
	"github.com/vislake/speed/pkg/tenant"
)

// ModuleName is this module's name in the registry. A host writes it into the
// command that carries the outbound delivery pipeline, and it is the name the
// pipeline's job type is registered under.
const ModuleName = "notification.delivery"

// JobType is the type of the job a dispatch is enqueued as, and the type
// this module's handler is registered under.
const JobType = ModuleName

const (
	featureEmail = "notification.channel.email"
	featureSMS   = "notification.channel.sms"
)

const (
	jobTimeout = 30 * time.Second
	// jobMaxRetries is how many times the job may fail before it is sent to
	// dead_letter.
	jobMaxRetries = 3
)

//go:embed migrations
var migrationRoot embed.FS

var migrationsFS = func() fs.FS {
	sub, err := fs.Sub(migrationRoot, "migrations")
	if err != nil {
		// The embed directive above is what makes this unreachable: the
		// directory is part of the source tree or the build fails first.
		panic(ModuleName + ": the embedded migration tree: " + err.Error())
	}
	return sub
}()

func init() { core.ProcessRegistry.Register(Module()) }

// The product must deliver the declared seam and be registrable as a handler.
var (
	_ notification.Dispatcher = (*pipeline)(nil)
	_ jobs.Handler            = (*pipeline)(nil)
)

// Module is the descriptor a host registers.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*jobs.Queue)(nil)},
			{Token: (*jobs.Handlers)(nil)},
			{Token: (*db.Database)(nil)},
			{Token: (*tenant.Catalog)(nil)},
			{Token: (*notification.Preferences)(nil)},
			{Token: (*notification.Inbox)(nil)},
			{Token: (*i18n.Localizer)(nil)},
			{Token: (*i18n.Resolver)(nil)},
			{Token: (*authn.Accounts)(nil)},
			{Token: (*kv.KV)(nil)},
			{Token: (*budget.Store)(nil)},
			{Token: (*settings.Store)(nil), Optional: true},
			{Token: (*observability.MeterProvider)(nil), Optional: true},
			{Token: (*log.Logger)(nil), Optional: true},
			{Token: (*mail.Sender)(nil), Optional: true},
			{Token: (*sms.Registry)(nil), Optional: true},
			{Token: (*sms.Sender)(nil), Optional: true},
			{Token: (*authn.TenantNameResolver)(nil), Optional: true},
		},
		Provides: []core.Provision{{Token: (*notification.Dispatcher)(nil), Exclusive: true}},
		Resources: []any{
			db.Migrations{FS: migrationsFS},
			messageCatalog,
			settingsSchema(),
			feature.Spec{Name: featureEmail, Conditional: true},
			feature.Spec{Name: featureSMS, Conditional: true},
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			return newPipeline(reg)
		},

		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			p, ok := productOf(instance)
			if !ok {
				return nil
			}
			if err := p.register(); err != nil {
				return err
			}
			p.postFeatures(reg)
			return nil
		},

		Start: func(_ context.Context, _ *core.Registry, instance any) error {
			p, ok := productOf(instance)
			if !ok {
				return nil
			}
			return p.checkEncryptionKey()
		},
		Stop:  func(context.Context, *core.Registry, any) error { return nil },
		Close: func(context.Context, *core.Registry, any) error { return nil },
	}
}

func productOf(instance any) (*pipeline, bool) {
	p, ok := instance.(*pipeline)
	return p, ok && p != nil
}

type channelSeams struct {
	mailer   mail.Sender
	registry sms.Registry
	sender   sms.Sender
}

func (s channelSeams) emailReady() bool { return s.mailer != nil }

func (s channelSeams) smsReady() bool {
	return s.registry != nil && s.sender != nil
}

type pipeline struct {
	queue     jobs.Queue
	handlers  jobs.Handlers
	prefs     notification.Preferences
	inbox     notification.Inbox
	localizer i18n.Localizer
	resolver  i18n.Resolver
	ledger    *ledger
	taxonomy  taxonomy
	seams     channelSeams
	// accounts reads the recipient's identifiers and asks which account a
	// destination belongs to.
	accounts authn.Accounts
	counters kv.KV
	attempts budget.Store
	// newLedger builds the ledger over a transaction, so the counts a
	// dispatch is compared against and the attempt row it writes are one
	// commitment.
	newLedger func(*gorm.DB) budget.Store
	dialect   db.Dialect
	// blindIndex is the database's keyed digest, the same blind index the
	// authentication channel hashes its destinations through.
	blindIndex func(string) string
	settings   *settingReader
	// meters reports each budget's utilization. Its instruments are nil when the
	// assembly delivered no metric capability, and a report is then a no-op.
	meters *meters
	logger *slog.Logger
	// tenantNames answers the name a tenant identifier is addressed by, nil
	// when the assembly supplies no resolver.
	tenantNames authn.TenantNameResolver
}

func newPipeline(reg *core.Registry) (*pipeline, error) {
	queue, err := core.Resolve[jobs.Queue](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the submission face: %w", ModuleName, err)
	}
	handlers, err := core.Resolve[jobs.Handlers](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the registration face: %w", ModuleName, err)
	}
	database, err := core.Resolve[db.Database](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the database: %w", ModuleName, err)
	}
	if _, rosterErr := core.Resolve[tenant.Catalog](reg); rosterErr != nil {
		return nil, fmt.Errorf("%s: taking up the tenant roster: %w", ModuleName, rosterErr)
	}
	prefs, err := core.Resolve[notification.Preferences](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the preferences seam: %w", ModuleName, err)
	}
	inbox, err := core.Resolve[notification.Inbox](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the inbox seam: %w", ModuleName, err)
	}
	localizer, err := core.Resolve[i18n.Localizer](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the message localizer: %w", ModuleName, err)
	}
	resolver, err := core.Resolve[i18n.Resolver](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the preference resolver: %w", ModuleName, err)
	}
	accounts, err := core.Resolve[authn.Accounts](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the account store the destination rule reads: %w", ModuleName, err)
	}
	counters, err := core.Resolve[kv.KV](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the key-value counters the email budget is counted on: %w", ModuleName, err)
	}
	types, err := collectTaxonomy(reg)
	if err != nil {
		return nil, err
	}
	mailer, err := optional[mail.Sender](reg, "the mail sender seam")
	if err != nil {
		return nil, err
	}
	registry, err := optional[sms.Registry](reg, "the sms registry seam")
	if err != nil {
		return nil, err
	}
	sender, err := optional[sms.Sender](reg, "the sms sender seam")
	if err != nil {
		return nil, err
	}
	logger, err := moduleLogger(reg)
	if err != nil {
		return nil, err
	}
	attempts, err := core.Resolve[budget.Store](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the shared sms ledger: %w", ModuleName, err)
	}
	store, err := optional[settings.Store](reg, "the settings store")
	if err != nil {
		return nil, err
	}
	provider, err := optional[observability.MeterProvider](reg, "the metric capability")
	if err != nil {
		return nil, err
	}
	meters, err := newMeters(provider)
	if err != nil {
		return nil, fmt.Errorf("%s: creating the metric instruments: %w", ModuleName, err)
	}
	names, err := optional[authn.TenantNameResolver](reg, "the tenant name resolver")
	if err != nil {
		return nil, err
	}
	return &pipeline{
		queue:       queue,
		handlers:    handlers,
		prefs:       prefs,
		inbox:       inbox,
		localizer:   localizer,
		resolver:    resolver,
		accounts:    accounts,
		counters:    counters,
		ledger:      &ledger{handle: database.DB()},
		taxonomy:    types,
		seams:       channelSeams{mailer: mailer, registry: registry, sender: sender},
		attempts:    attempts,
		newLedger:   budget.New,
		dialect:     database.Dialect(),
		blindIndex:  database.BlindIndex,
		settings:    newSettingReader(store, logger),
		meters:      meters,
		logger:      logger,
		tenantNames: names,
	}, nil
}

func moduleLogger(reg *core.Registry) (*slog.Logger, error) {
	delivered, err := core.Resolve[log.Logger](reg)
	switch {
	case err == nil:
		return delivered.Named(ModuleName), nil
	case errors.Is(err, core.ErrMissingProvider):
		return log.Default(), nil
	default:
		return nil, fmt.Errorf("%s: taking up the logging capability: %w", ModuleName, err)
	}
}

// optional takes up a capability whose absence is a legal assembly: the zero
// value says it is absent, and any other failure to take it up ends the start,
// because a capability that is there and cannot be taken up is not an absence.
func optional[T any](reg *core.Registry, what string) (T, error) {
	var zero T
	v, err := core.Resolve[T](reg)
	switch {
	case err == nil:
		return v, nil
	case errors.Is(err, core.ErrMissingProvider):
		return zero, nil
	default:
		return zero, fmt.Errorf("%s: taking up %s: %w", ModuleName, what, err)
	}
}

func (p *pipeline) register() error {
	if err := p.handlers.Register(p); err != nil {
		return fmt.Errorf("%s: registering the delivery handler: %w", ModuleName, err)
	}
	return nil
}

func (p *pipeline) checkEncryptionKey() error {
	var missing bool
	func() {
		defer func() {
			if recover() != nil {
				missing = true
			}
		}()
		p.blindIndex(ModuleName)
	}()
	if missing {
		return fmt.Errorf("%w: %s hashes a recipient's ledger rows and email counters under the "+
			"database root key, and this database has none. Configure a root key, or leave this "+
			"module out of the command", authn.ErrNoEncryptionKey, ModuleName)
	}
	return nil
}

func (p *pipeline) postFeatures(reg *core.Registry) {
	board, err := core.Resolve[feature.Board](reg)
	if err != nil {
		return
	}
	if p.seams.emailReady() {
		board.Post(ModuleName, featureEmail)
	}
	if p.seams.smsReady() {
		board.Post(ModuleName, featureSMS)
	}
}
