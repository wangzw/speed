//go:build review

package main

// This file is the review assembly's notification fixture: one notification
// type the baseline does not declare, a worker that dispatches it to the
// members of every tenant, and the preference-recipient wiring the baseline
// deliberately leaves out.
//
// The type lives here, behind the review tag, for a reason the notification
// capability makes explicit: notification.preferences and notification.delivery
// collect their type taxonomy with core.Resources over the available set, not
// the running set, so a TypeSpec declared in an untagged file would be part of
// the baseline's taxonomy even though no baseline command runs the declaring
// module. A review type in an untagged file would therefore leak into the
// baseline's preference matrix and its dispatch validation. Behind the build
// tag it is absent from a default binary entirely.
//
// The dispatch mechanism is worker-driven, not an HTTP endpoint a test calls.
// The module declares a jobs.Schedule, and the platform's scheduler enqueues
// one dispatch job per tenant per window; the handler registered here turns
// each job into one notification per active member with an email address. A
// member who signs in and reaches a tenant therefore receives notices the way a
// real system action would send them, and the browser only ever clicks and
// types. Nothing in this file exposes a route of its own.
//
// The preference routes read the recipient from the request context, under the
// key preferences.WithRecipient. The notification capability holds no identity,
// so the host's identity side is what names the recipient; the demo baseline
// deliberately does not, which leaves the matrix empty and its write refused.
// This build wires it: a middleware after the authentication gateway copies the
// caller's effective account onto the request context. The layer is registered
// here and nowhere else, so a default build keeps the baseline's semantics.

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/jobs"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/notification"
	"github.com/vislake/speed/pkg/notification/preferences"
	"github.com/vislake/speed/pkg/tenant"
)

// The names this fixture owns. The module name is what reviewModules names in
// the serve roster; the job type is what the schedule enqueues and the handler
// registers; the type key is the vocabulary both taxonomy consumers collect.
const (
	reviewNotificationModuleName = "review.notification"
	reviewNotificationJobType    = "review.notification.dispatch"
	reviewNotificationTypeKey    = "review.notice"
	reviewNotificationGroup      = "review"
)

// reviewNotificationEndpoint is the endpoint carrying the notification surface.
// The name is the platform defaults document's; the preference routes mount
// there and so does the recipient middleware.
const reviewNotificationEndpoint = "api"

// reviewNotificationInterval is the dispatch window. The scheduler enqueues one
// job per tenant per window, so a tenant's members receive a notice at most
// once per interval. It is short enough for a walkthrough to observe a notice
// arrive, and long enough that a person marking the inbox read is not raced by
// the next one.
const reviewNotificationInterval = 10 * time.Second

// reviewRecipientLayerName is the middleware layer's name on the api endpoint.
// It appears in the chain listing, so it says which fixture added it.
const reviewRecipientLayerName = "review.notification.recipient"

//go:embed review_notification_locales/*.json
var reviewNotificationLocales embed.FS

// reviewNotificationCatalog is the message catalog of the type. The keys are
// "<type_key>.<channel>.<part>", which is where the delivery pipeline renders
// an in-app message and an email from; the declaring module owns the text.
var reviewNotificationCatalog = i18n.Catalog{
	FS:      reviewNotificationLocales,
	Pattern: "review_notification_locales/*.json",
}

// reviewNotificationType is the type a business module would declare for a
// system notice a recipient may turn off. Its default channels are the in-app
// inbox and email, it is unsubscribable — the journey's preference write turns
// its email channel off — and the only parameter it exposes is the email
// destination the delivery pipeline needs for that channel, which is the
// recipient's own address.
var reviewNotificationType = notification.TypeSpec{
	Key:                    reviewNotificationTypeKey,
	Group:                  reviewNotificationGroup,
	DefaultChannels:        []notification.Channel{notification.ChannelInApp, notification.ChannelEmail},
	Unsubscribable:         true,
	RecipientVisibleParams: []string{"to"},
}

// reviewNotificationSchedule is the periodic dispatch declaration. The
// scheduler expands it per tenant in the roster, so a tenant created while the
// process runs starts receiving notices on the next window.
var reviewNotificationSchedule = jobs.Schedule{
	Key:      reviewNotificationModuleName + ".periodic",
	Type:     reviewNotificationJobType,
	Interval: reviewNotificationInterval,
}

// init registers the module. The import is the wiring; reviewModules is what
// names it for a run.
func init() { core.ProcessRegistry.Register(reviewNotificationModule()) }

// The product must deliver the job handler and report its type.
var _ jobs.Handler = (*reviewNotifier)(nil)

// reviewNotificationModule is the fixture's descriptor.
//
// The dispatcher, the account store, the membership store, the locale resolver
// and the handler registration face are required: without them the fixture has
// nowhere to submit a dispatch, no member to address, no language to render it
// in and no place to register its job. The logger is optional, as it is for
// every module, and the host's default logger carries the fixture's diagnostics
// when none is delivered.
func reviewNotificationModule() core.Module {
	return core.Module{
		Name: reviewNotificationModuleName,
		Requires: []core.Requirement{
			{Token: (*notification.Dispatcher)(nil)},
			{Token: (*authn.Accounts)(nil)},
			{Token: (*authn.Memberships)(nil)},
			{Token: (*i18n.Resolver)(nil)},
			{Token: (*jobs.Handlers)(nil)},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Resources: []any{
			reviewNotificationType,
			reviewNotificationCatalog,
			reviewNotificationSchedule,
		},
		New: func(_ context.Context, reg *core.Registry) (any, error) {
			return newReviewNotifier(reg)
		},
		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			notifier, ok := instance.(*reviewNotifier)
			if !ok || notifier == nil {
				return nil
			}
			handlers, err := core.Resolve[jobs.Handlers](reg)
			if err != nil {
				return fmt.Errorf("%s: taking up the handler registration face: %w",
					reviewNotificationModuleName, err)
			}
			if err := handlers.Register(notifier); err != nil {
				return fmt.Errorf("%s: registering the dispatch handler: %w",
					reviewNotificationModuleName, err)
			}
			return wireReviewRecipient(reg)
		},
	}
}

// reviewNotifier is the worker's product: the seams one dispatch needs, and the
// logger its skipped members are reported on.
type reviewNotifier struct {
	dispatcher notification.Dispatcher
	accounts   authn.Accounts
	members    authn.Memberships
	resolver   i18n.Resolver
	logger     *slog.Logger
}

// newReviewNotifier takes up what the descriptor requires.
func newReviewNotifier(reg *core.Registry) (*reviewNotifier, error) {
	dispatcher, err := core.Resolve[notification.Dispatcher](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the submission seam: %w",
			reviewNotificationModuleName, err)
	}
	accounts, err := core.Resolve[authn.Accounts](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the account store: %w",
			reviewNotificationModuleName, err)
	}
	members, err := core.Resolve[authn.Memberships](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the membership store: %w",
			reviewNotificationModuleName, err)
	}
	resolver, err := core.Resolve[i18n.Resolver](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the locale resolver: %w",
			reviewNotificationModuleName, err)
	}
	logger := log.Default()
	if delivered, logErr := core.Resolve[log.Logger](reg); logErr == nil {
		logger = delivered.Named(reviewNotificationModuleName)
	}
	return &reviewNotifier{
		dispatcher: dispatcher,
		accounts:   accounts,
		members:    members,
		resolver:   resolver,
		logger:     logger,
	}, nil
}

// Type reports the job type the scheduler enqueues; see jobs.Handler.
func (*reviewNotifier) Type() string { return reviewNotificationJobType }

// Handle turns one periodic job into one dispatch per active member of the
// tenant the job belongs to. The scheduler entered the tenant before handing
// the job over, so the context already carries it.
//
// Each dispatch renders in the recipient's own language: the account's stored
// preference when it has one, and the platform default when it does not. The
// resolver reads the preference off the account, so two members of one tenant
// receive the same notice in the languages each of them chose.
//
// A member this fixture cannot address — one with no email identifier — is
// skipped with a log line rather than failed: the type's default channels
// include email, and the delivery pipeline answers a missing destination as a
// failed channel, which would retry the whole job for one unaddressable member.
// A dispatch that fails is joined into the returned error, so the job retries;
// the per-recipient dedupe key of the dispatch makes the members already
// submitted converge on one delivery job, and a retry writes no second message.
func (n *reviewNotifier) Handle(ctx context.Context, job *jobs.Job, _ jobs.ProgressFn) (jobs.Result, error) {
	id, ok := tenant.FromContext(ctx)
	if !ok {
		return jobs.Result{}, fmt.Errorf("%s: %w", reviewNotificationModuleName, tenant.ErrNoTenantInContext)
	}
	members, err := n.membersOf(ctx, string(id))
	if err != nil {
		return jobs.Result{}, fmt.Errorf("%s: listing the members of tenant %q: %w",
			reviewNotificationModuleName, id, err)
	}
	var failures []error
	for _, member := range members {
		if !member.Active {
			continue
		}
		to, ok, err := n.emailOf(ctx, member.Account)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if !ok {
			n.logger.Info(reviewNotificationModuleName+": a member has no email identifier, so the notice is skipped",
				"tenant", string(id), "account", string(member.Account))
			continue
		}
		_, err = n.dispatcher.Dispatch(ctx, notification.Dispatch{
			TypeKey: reviewNotificationTypeKey,
			Recipient: notification.Recipient{
				Class:  notification.RecipientUser,
				UserID: string(member.Account),
			},
			Locale:       string(n.resolver.LocaleFor(ctx, string(member.Account))),
			Params:       map[string]any{"to": to},
			OccurrenceID: string(job.ID),
		})
		if err != nil {
			failures = append(failures, fmt.Errorf("dispatching to %q: %w", member.Account, err))
		}
	}
	if len(failures) > 0 {
		return jobs.Result{}, fmt.Errorf("%s: %w", reviewNotificationModuleName, errors.Join(failures...))
	}
	return jobs.Result{}, nil
}

// membersOf lists every membership of a tenant, following the paging cursor the
// membership seam exposes.
func (n *reviewNotifier) membersOf(ctx context.Context, tenantID string) ([]authn.Membership, error) {
	const pageSize = 200
	var all []authn.Membership
	after := ""
	for {
		page, err := n.members.Members(ctx, tenantID, authn.Page{After: after, Limit: pageSize})
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if len(page) < pageSize {
			return all, nil
		}
		after = string(page[len(page)-1].Account)
	}
}

// emailOf answers the email address an account is found by, and whether it has
// one. The first email identifier is used; an account may carry at most one.
func (n *reviewNotifier) emailOf(ctx context.Context, id authn.AccountID) (string, bool, error) {
	identifiers, err := n.accounts.Identifiers(ctx, id)
	if err != nil {
		return "", false, fmt.Errorf("listing the identifiers of %q: %w", id, err)
	}
	for _, identifier := range identifiers {
		if identifier.Kind == authn.IdentifierEmail && identifier.Value != "" {
			return identifier.Value, true, nil
		}
	}
	return "", false, nil
}

// wireReviewRecipient adds the layer that names the preference routes'
// recipient: the caller's effective account, which is what an impersonation
// reads as the impersonated one.
//
// It sits in the recording stage, inside the authentication gateway — the
// gateway is the identifying stage, and it puts the identity on the request
// context before calling the next layer — and outside the authorization guard.
// The layer sets nothing when no identity is present, so a run without the
// gateway, or a request that reached the handler before authentication did,
// keeps the capability's own answer: a write with no recipient is refused.
func wireReviewRecipient(reg *core.Registry) error {
	router, err := core.Resolve[speedhttp.Router](reg)
	if errors.Is(err, core.ErrMissingProvider) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%s: taking up the HTTP entry point: %w", reviewNotificationModuleName, err)
	}
	endpoint, err := router.Endpoint(reviewNotificationEndpoint)
	if err != nil {
		return fmt.Errorf("%s: looking up the %q endpoint: %w",
			reviewNotificationModuleName, reviewNotificationEndpoint, err)
	}
	endpoint.Use(speedhttp.Middleware{
		Name:    reviewRecipientLayerName,
		Stage:   speedhttp.StageRecord,
		Handler: reviewRecipientHandler,
	})
	return nil
}

// reviewRecipientHandler copies the authenticated caller's effective account
// onto the request context under preferences.WithRecipient. A request with no
// identity leaves the context untouched.
func reviewRecipientHandler(c *gin.Context) {
	if id, ok := authn.FromContext(c.Request.Context()); ok {
		c.Request = c.Request.WithContext(
			preferences.WithRecipient(c.Request.Context(), string(id.Account())))
	}
	c.Next()
}
