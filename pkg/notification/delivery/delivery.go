// Package delivery is the notification capability's outbound pipeline: one
// delivery intent goes in, is validated, and is enqueued as one job; when the
// job is consumed the intent is validated again, the recipient's effective
// channels are resolved, and each channel is rendered, sent through its seam and
// recorded as one ledger row.
//
// The submission seam is notification.Dispatcher, delivered exclusively by this
// module. Dispatcher.Dispatch validates, holds a carried destination against the
// recipient's own identifiers and enqueues — it does not render, does not send
// and writes no storage. The other half of the pipeline is a jobs.Handler this
// module registers in its Init; jobs.db claims the job, enters its tenant and
// hands it over, and the handler does the per-channel work.
//
// A channel's availability is a matter of assembly. in_app is always available —
// the inbox is a required dependency — while email needs a mail sender and sms
// needs both the sms registry and the sms sender. A channel whose seam is absent
// is skipped and recorded as one `skipped` row, which is not a failure and does
// not enter the joined error; it tells "this assembly has no such channel" apart
// from "the vendor refused the send".
//
// The delivery is at-least-once. Every channel attempt derives an idempotency
// key, and the (tenant_id, idempotency_key) uniqueness on notification_sends is
// what makes a repeated consumption skip the channels already settled and make
// up only the ones that are not.
//
// A settled external channel means the carrier accepted the message and returned
// a receipt; it does not mean the recipient received it. A carrier whose receipts
// are asynchronous answers only with a message identifier, and the final delivery
// state is the carrier's to report, not this module's to record.
//
// The module declares no configuration namespace and reads no input item: the
// parameters of a delivery job are fixed by this module at enqueue time, not
// taken from configuration. It does declare three runtime settings — the two
// per-recipient delivery budgets and the deployment-wide one — and charges them
// against the key-value counters before a dispatch is queued.
package delivery

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"time"

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
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/sms"
	"github.com/vislake/speed/pkg/tenant"
)

// ModuleName is this module's name in the registry. A host writes it into the
// command that carries the outbound delivery pipeline, and it is the name the
// pipeline's job type is registered under.
const ModuleName = "notification.delivery"

// JobType is the type of the job a dispatch is enqueued as, and the type this
// module's handler is registered under. It is the module's own name: the job is
// the module's work, and a second registration under it is a defect the handler
// registration refuses.
const JobType = ModuleName

// The conditional features this module declares: one per external channel, and
// each is posted in Init from the same seam judgement the send path reads.
const (
	featureEmail = "notification.channel.email"
	featureSMS   = "notification.channel.sms"
)

// The parameters of a delivery job, fixed by this module. A send is one external
// call that may take seconds, so it is given a generation timeout and the
// queue's ordinary retry budget; a dispatch is ordinary work and takes the
// normal priority.
const (
	// jobTimeout bounds one execution of the delivery job.
	jobTimeout = 30 * time.Second
	// jobMaxRetries is how many times the job may fail before it is sent to
	// dead_letter.
	jobMaxRetries = 3
)

//go:embed migrations
var migrationRoot embed.FS

// migrationsFS is migrationRoot with the wrapping directory removed, so the tree
// the database module sees has one directory per dialect at its root, which is
// the shape it reads.
var migrationsFS = func() fs.FS {
	sub, err := fs.Sub(migrationRoot, "migrations")
	if err != nil {
		// The embed directive above is what makes this unreachable: the
		// directory is part of the source tree or the build fails first.
		panic(ModuleName + ": the embedded migration tree: " + err.Error())
	}
	return sub
}()

// init registers this module with the process registry. Importing the package is
// what puts the outbound delivery pipeline in a binary's available set.
func init() { core.ProcessRegistry.Register(Module()) }

// The product must deliver the declared seam and be registrable as a handler.
var (
	_ notification.Dispatcher = (*pipeline)(nil)
	_ jobs.Handler            = (*pipeline)(nil)
)

// Module is the descriptor a host registers.
//
// The queue, the handler registration face, the database, the tenant roster, the
// preferences seam, the inbox seam, the message localizer, the account store and
// the key-value counters are all required and hard. A pipeline with no queue has
// nowhere to put a dispatch, one with no registration face cannot be run, one
// with no table has nowhere to record a send, one with no roster carries no
// filter to scope its ledger writes by tenant, one with no preferences cannot
// resolve a recipient's channels, one with no inbox cannot write an in-app
// message, one with no localizer cannot render one, one with no account store
// cannot tell whether a destination belongs to the recipient, and one with no
// counters cannot bound a dispatch. The counters and the account store are hard
// requirements rather than absences the module works around: a delivery budget
// that cannot count and an ownership rule that cannot read would each let a
// message through, which is the direction the requirement forbids.
//
// The settings store is optional: a dispatch reads its budget limits through it,
// and an absence is a legal assembly in which every budget reads its declared
// default. Declaring it also orders this module after settings, so the snapshot a
// dispatch reads has been loaded.
//
// The mail sender, the sms registry and the sms sender are optional, and none of
// their absences fails the startup: a channel is available exactly when the seams
// it sends through are present — email on the mail sender, sms on both the
// registry and the sender.
//
// The logging capability is optional too. The pipeline takes it up in New and
// names it after this module, because the delivery job runs on a context the
// queue created: no entry point injects a logger into it, and the external-send
// lines would otherwise fall back to the process default logger and never reach
// the destinations a host configured. An assembly without logging keeps the
// default logger, which is what it had before.
//
// The submission seam is delivered exclusively: one process has exactly one
// outbound pipeline, so a second provider fails the startup rather than one of
// them silently winning.
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
			{Token: (*authn.Accounts)(nil)},
			{Token: (*kv.KV)(nil)},
			{Token: (*settings.Store)(nil), Optional: true},
			{Token: (*log.Logger)(nil), Optional: true},
			{Token: (*mail.Sender)(nil), Optional: true},
			{Token: (*sms.Registry)(nil), Optional: true},
			{Token: (*sms.Sender)(nil), Optional: true},
		},
		Provides: []core.Provision{{Token: (*notification.Dispatcher)(nil), Exclusive: true}},
		Resources: []any{
			db.Migrations{FS: migrationsFS},
			messageCatalog,
			settingsSchema(),
			feature.Spec{Name: featureEmail, Conditional: true},
			feature.Spec{Name: featureSMS, Conditional: true},
		},

		// New takes up what the descriptor requires and collects the type
		// taxonomy. It reads no table of its own: this stage runs before the
		// migrations are applied, so the ledger table is not there yet. The table
		// is first touched when a job is consumed.
		New: func(_ context.Context, reg *core.Registry) (any, error) {
			return newPipeline(reg)
		},

		// Init registers the handler with the queue's registration face and
		// reports the channels this assembly can send on. The handler set is
		// fixed before the queue starts, so this runs early enough that every
		// delivery job has a handler to run it. The report runs here and not in
		// New: New is outside the board's posting window, where a post panics.
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

		Start: func(context.Context, *core.Registry, any) error { return nil },
		Stop:  func(context.Context, *core.Registry, any) error { return nil },
		Close: func(context.Context, *core.Registry, any) error { return nil },
	}
}

// productOf narrows an instance to this module's product. An instance of another
// shape is one the host supplied for this module, and the callbacks leave it
// alone.
func productOf(instance any) (*pipeline, bool) {
	p, ok := instance.(*pipeline)
	return p, ok && p != nil
}

// channelSeams is the set of channel seams this assembly delivered. A nil seam
// is an absent one, and a channel with an absent seam is skipped rather than
// failed.
type channelSeams struct {
	// mailer hands a rendered email to the carrier.
	mailer mail.Sender
	// registry resolves an sms message against the templates in force.
	registry sms.Registry
	// sender hands a resolved sms message to the vendor.
	sender sms.Sender
}

// emailReady reports whether the email channel can send in this assembly.
func (s channelSeams) emailReady() bool { return s.mailer != nil }

// smsReady reports whether the sms channel can send in this assembly: both the
// registry and the sender seam have to be present.
func (s channelSeams) smsReady() bool {
	return s.registry != nil && s.sender != nil
}

// pipeline is the module's product. It implements notification.Dispatcher and is
// registered as a jobs.Handler. The taxonomy, the seams and the dependencies are
// read-only once New has built it, so Dispatch and Handle share no mutable state
// and may be called from more than one goroutine at once.
type pipeline struct {
	queue     jobs.Queue
	handlers  jobs.Handlers
	prefs     notification.Preferences
	inbox     notification.Inbox
	localizer i18n.Localizer
	ledger    *ledger
	taxonomy  taxonomy
	seams     channelSeams
	// accounts reads the recipient's identifiers and asks which account a
	// destination belongs to. It is what tells the recipient's own address from
	// anyone else's, and it is a hard dependency: without it a dispatch carrying
	// a destination cannot be checked and is refused.
	accounts authn.Accounts
	// counters holds the delivery budgets, one fixed window per key. It is the
	// key-value capability, whose increment is atomic across replicas.
	counters kv.KV
	// settings reads the budget limits on every dispatch.
	settings *settingReader
	// logger is where the external-send lines are written. It is the assembly's
	// logging capability named after this module, or the process default logger
	// when the assembly delivered none; it is never taken from the handler's
	// context, which carries no injected logger (see Module).
	logger *slog.Logger
}

// newPipeline takes up what the descriptor requires and assembles the product.
// The tenant roster is taken up so that the roster and its statement filter are
// part of the assembly this module's scoped ledger writes run under; the tenant a
// delivery belongs to comes from the handler's own context, set by jobs.db when
// it claims the job.
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
	accounts, err := core.Resolve[authn.Accounts](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the account store the destination rule reads: %w", ModuleName, err)
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
	counters, err := core.Resolve[kv.KV](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the key-value counters: %w", ModuleName, err)
	}
	store, err := optional[settings.Store](reg, "the settings store")
	if err != nil {
		return nil, err
	}
	return &pipeline{
		queue:     queue,
		handlers:  handlers,
		prefs:     prefs,
		inbox:     inbox,
		localizer: localizer,
		accounts:  accounts,
		ledger:    &ledger{handle: database.DB()},
		taxonomy:  types,
		seams:     channelSeams{mailer: mailer, registry: registry, sender: sender},
		counters:  counters,
		settings:  newSettingReader(store, logger),
		logger:    logger,
	}, nil
}

// moduleLogger takes up the logging capability when the assembly delivers one,
// and answers the process default logger otherwise.
//
// The returned logger carries this module's name, which is what tells its lines
// apart from every other module's once they reach a shared destination. An
// absence is a legal assembly and is told apart from a failed lookup by the
// sentinel: anything other than "no provider" is the lookup itself failing, and
// that ends the startup rather than quietly sending this module's records
// somewhere the host did not configure.
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

// register binds this module's product as the handler for its job type.
func (p *pipeline) register() error {
	if err := p.handlers.Register(p); err != nil {
		return fmt.Errorf("%s: registering the delivery handler: %w", ModuleName, err)
	}
	return nil
}

// postFeatures reports the external channels this assembly can send on. It runs
// in Init, inside the board's posting window, and reuses the seam judgements the
// send path reads: email on the mail sender, sms only when both its seams are
// present. A process with no board skips the report silently.
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
