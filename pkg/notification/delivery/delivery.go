// Package delivery is the notification capability's outbound pipeline: one
// delivery intent goes in, is validated, and is enqueued as one job; when the
// job is consumed the intent is validated again, the recipient's effective
// channels are resolved, and each channel is rendered, sent through its seam and
// recorded as one ledger row.
//
// The submission seam is notification.Dispatcher, delivered exclusively by this
// module. Dispatcher.Dispatch only validates and enqueues — it does not render,
// does not send and writes no storage. The other half of the pipeline is a
// jobs.Handler this module registers in its Init; jobs.db claims the job, enters
// its tenant and hands it over, and the handler does the per-channel work.
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
// The module declares no configuration namespace and reads no input item: the
// parameters of a delivery job are fixed by this module at enqueue time, not
// taken from configuration.
package delivery

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/jobs"
	"github.com/vislake/speed/pkg/mail"
	"github.com/vislake/speed/pkg/notification"
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
// preferences seam, the inbox seam and the message localizer are all required
// and hard. A pipeline with no queue has nowhere to put a dispatch, one with no
// registration face cannot be run, one with no table has nowhere to record a
// send, one with no roster carries no filter to scope its ledger writes by
// tenant, one with no preferences cannot resolve a recipient's channels, one
// with no inbox cannot write an in-app message, and one with no localizer cannot
// render one.
//
// The mail sender, the sms registry and the sms sender are optional, and none of
// their absences fails the startup: a channel is available exactly when the seams
// it sends through are present — email on the mail sender, sms on both the
// registry and the sender.
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
			{Token: (*mail.Sender)(nil), Optional: true},
			{Token: (*sms.Registry)(nil), Optional: true},
			{Token: (*sms.Sender)(nil), Optional: true},
		},
		Provides: []core.Provision{{Token: (*notification.Dispatcher)(nil), Exclusive: true}},
		Resources: []any{
			db.Migrations{FS: migrationsFS},
			messageCatalog,
		},

		// New takes up what the descriptor requires and collects the type
		// taxonomy. It reads no table of its own: this stage runs before the
		// migrations are applied, so the ledger table is not there yet. The table
		// is first touched when a job is consumed.
		New: func(_ context.Context, reg *core.Registry) (any, error) {
			return newPipeline(reg)
		},

		// Init registers the handler with the queue's registration face. The
		// handler set is fixed before the queue starts, so this runs early enough
		// that every delivery job has a handler to run it.
		Init: func(_ context.Context, _ *core.Registry, instance any) error {
			p, ok := productOf(instance)
			if !ok {
				return nil
			}
			return p.register()
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
	return &pipeline{
		queue:     queue,
		handlers:  handlers,
		prefs:     prefs,
		inbox:     inbox,
		localizer: localizer,
		ledger:    &ledger{handle: database.DB()},
		taxonomy:  types,
		seams:     channelSeams{mailer: mailer, registry: registry, sender: sender},
	}, nil
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
