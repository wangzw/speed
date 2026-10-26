//go:build review

package main

// This file is the review assembly's notification fixture: one notification
// type the baseline does not declare, a worker that dispatches it to the
// members of every tenant, the preference-recipient wiring the baseline
// deliberately leaves out, and the review-only tenant control the SMS rollout
// journey clicks.
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
// The type's default channels are the in-app inbox, email and sms. The sms
// channel is what the rollout journey drives: the catalog names the runtime
// template the journey registers, and the template declares "to" among its
// parameters because the delivery pipeline uses the dispatch's single
// destination both as the recipient's address and as a template parameter.
//
// A dispatch addresses one recipient on the external channel their account can
// actually be reached on. A proved phone number is the sms destination; with no
// proved phone the account falls back to its email address, and sms is removed
// from that recipient's effective set rather than attempted with the email as
// its destination. The fixture writes that pruned set through the preference
// seam because the delivery pipeline reads the effective channels when it
// consumes the job.
//
// The periodic schedule is the member-workspace journey's dispatch: the
// platform's scheduler enqueues one job per tenant per window and the handler
// turns each job into one notification per addressable active member. The
// rollout journey's proof is not that window: it is the review-only control
// route below, which a tenant member drives from the console by clicking. The
// route and the feature that offers its control are compiled only under the
// review build tag, so neither exists in a default binary.
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
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/feature"
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
// The SMS template name is the catalog entry's value: the rollout journey
// registers a runtime template under exactly this name, with "to" as its
// declared parameter (the dispatch carries no other). The control feature is
// the review-only gate the tenant control reads; it exists only while this
// module is in force, and this module is compiled only under the review build
// tag.
const (
	reviewNotificationModuleName     = "review.notification"
	reviewNotificationJobType        = "review.notification.dispatch"
	reviewNotificationTypeKey        = "review.notice"
	reviewNotificationGroup          = "review"
	reviewNotificationSMSTemplate    = "review-notice"
	reviewNotificationControlFeature = "review.notification.control"
)

// reviewNotificationControlPath is the review-only control's route on the api
// endpoint. The tenant control posts to it to dispatch one notice for the
// calling member; no other assembly mounts anything under it, because the
// module that mounts it is compiled only under the review build tag.
const reviewNotificationControlPath = "/api/v1/notifications/review/notice"

// reviewNoticePlan is how one recipient is addressed by one dispatch: the
// single external destination the dispatch carries and the channels the
// recipient can receive on. The capability carries one destination parameter
// for every external channel, so a recipient is addressed on at most one of
// them per dispatch.
type reviewNoticePlan struct {
	// To is the destination the dispatch carries under "to". For sms it is the
	// recipient's proved phone number and for email the address the account
	// holds.
	To string
	// Channels is the subset of the type's channels the recipient can be
	// reached on: in_app always, plus the one external channel To addresses.
	Channels []notification.Channel
}

// planReviewNotice resolves one account's identifiers into a delivery plan.
//
// A proved phone number is the sms destination: the journey's recipient has
// verified their number, and an unproved one is not a destination the product
// would send a notice to. With no proved phone the account falls back to its
// email address, which keeps the in-app and email channels reachable. An
// account holding neither is not addressable and is skipped rather than failed.
func planReviewNotice(identifiers []authn.Identifier) (reviewNoticePlan, bool) {
	var phone, email string
	for _, identifier := range identifiers {
		if identifier.Value == "" {
			continue
		}
		switch identifier.Kind {
		case authn.IdentifierPhone:
			if phone == "" && (authn.Verification{Via: identifier.VerifiedVia}).Proves() {
				phone = identifier.Value
			}
		case authn.IdentifierEmail:
			if email == "" {
				email = identifier.Value
			}
		}
	}
	switch {
	case phone != "":
		return reviewNoticePlan{
			To:       phone,
			Channels: []notification.Channel{notification.ChannelInApp, notification.ChannelSMS},
		}, true
	case email != "":
		return reviewNoticePlan{
			To:       email,
			Channels: []notification.Channel{notification.ChannelInApp, notification.ChannelEmail},
		}, true
	default:
		return reviewNoticePlan{}, false
	}
}

// pruneReviewChannels removes the channels a plan cannot address from the
// recipient's effective set. It is deliberately monotonic: it only drops a
// channel, never adds one back, so a channel the recipient turned off stays off
// across every dispatch window.
//
// The write it feeds is needed because the delivery pipeline reads the
// recipient's effective channels from the preference seam when it consumes the
// job. A plan that addresses sms carries a phone number as the single
// destination, so leaving email in the set would send the email to a phone
// number; a plan that falls back to email must drop sms, or the sms channel
// would be attempted with the email address as its destination.
func pruneReviewChannels(effective []notification.Channel, plan reviewNoticePlan) []notification.Channel {
	addressable := make(map[notification.Channel]struct{}, len(plan.Channels))
	for _, channel := range plan.Channels {
		addressable[channel] = struct{}{}
	}
	out := make([]notification.Channel, 0, len(effective))
	for _, channel := range effective {
		if _, ok := addressable[channel]; ok {
			out = append(out, channel)
		}
	}
	return out
}

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
// inbox, email and sms: the rollout journey needs sms among them, while the
// member-workspace journey reaches the other two. It is unsubscribable — the
// journey's preference write turns a channel off — and the only parameter it
// exposes is the destination the delivery pipeline reads, which is the
// recipient's own phone number or address. The catalog names the sms template
// the journey registers; the template declares "to" as its only parameter
// because the destination is also a template parameter.
var reviewNotificationType = notification.TypeSpec{
	Key:   reviewNotificationTypeKey,
	Group: reviewNotificationGroup,
	DefaultChannels: []notification.Channel{
		notification.ChannelInApp,
		notification.ChannelEmail,
		notification.ChannelSMS,
	},
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
// The dispatcher, the account store, the membership store, the preference
// matrix, the locale resolver and the handler registration face are required:
// without them the fixture has nowhere to submit a dispatch, no member to
// address, no recipient-aware channel set to write, no language to render it in
// and no place to register its job. The logger is optional, as it is for every
// module, and the host's default logger carries the fixture's diagnostics when
// none is delivered.
//
// The control feature is a static declaration: it is present exactly while this
// module is in force, which is only in a review run, because the module itself
// is compiled only under the review build tag.
func reviewNotificationModule() core.Module {
	return core.Module{
		Name: reviewNotificationModuleName,
		Requires: []core.Requirement{
			{Token: (*notification.Dispatcher)(nil)},
			{Token: (*notification.Preferences)(nil)},
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
			feature.Spec{Name: reviewNotificationControlFeature},
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
			if err := wireReviewRecipient(reg); err != nil {
				return err
			}
			return wireReviewControl(reg, notifier)
		},
	}
}

// reviewNotifier is the worker's product: the seams one dispatch needs, and the
// logger its skipped members are reported on.
type reviewNotifier struct {
	dispatcher notification.Dispatcher
	prefs      notification.Preferences
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
	prefs, err := core.Resolve[notification.Preferences](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the preference matrix: %w",
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
		prefs:      prefs,
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
// A member this fixture cannot address — one with no proved phone and no email
// address — is skipped with a log line rather than failed: the type's default
// channels include email and sms, and the delivery pipeline answers a missing
// destination as a failed channel, which would retry the whole job for one
// unaddressable member. A dispatch that fails is joined into the returned
// error, so the job retries; the per-recipient dedupe key of the dispatch makes
// the members already submitted converge on one delivery job, and a retry
// writes no second message.
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
		plan, ok, err := n.notify(ctx, member.Account, string(job.ID))
		if err != nil {
			failures = append(failures, fmt.Errorf("dispatching to %q: %w", member.Account, err))
			continue
		}
		if !ok {
			n.logger.Info(reviewNotificationModuleName+": a member has no proved phone and no email address, "+
				"so the notice is skipped",
				"tenant", string(id), "account", string(member.Account))
			continue
		}
		n.logger.Debug(reviewNotificationModuleName+": a notice was dispatched",
			"tenant", string(id), "account", string(member.Account), "channels", plan.Channels)
	}
	if len(failures) > 0 {
		return jobs.Result{}, fmt.Errorf("%s: %w", reviewNotificationModuleName, errors.Join(failures...))
	}
	return jobs.Result{}, nil
}

// notify addresses one recipient on the channels their account can be reached
// on and submits one dispatch. It resolves the account's identifiers into a
// plan, writes the recipient-aware channel set through the preference seam so
// the delivery pipeline reads the same set when it consumes the job, and
// submits the dispatch under occurrenceID.
//
// The second result is false when the account has neither a proved phone nor an
// email address; the caller reports that as a skip, not a failure.
func (n *reviewNotifier) notify(ctx context.Context, id authn.AccountID, occurrenceID string) (reviewNoticePlan, bool, error) {
	identifiers, err := n.accounts.Identifiers(ctx, id)
	if err != nil {
		return reviewNoticePlan{}, false, fmt.Errorf("listing the identifiers of %q: %w", id, err)
	}
	plan, ok := planReviewNotice(identifiers)
	if !ok {
		return reviewNoticePlan{}, false, nil
	}
	effective, err := n.prefs.Effective(ctx, string(id), reviewNotificationTypeKey)
	if err != nil {
		return reviewNoticePlan{}, true, fmt.Errorf("reading the effective channels of %q: %w", id, err)
	}
	if wanted := pruneReviewChannels(effective, plan); len(wanted) != len(effective) {
		if _, setErr := n.prefs.Set(ctx, string(id), reviewNotificationTypeKey, wanted); setErr != nil {
			return reviewNoticePlan{}, true, fmt.Errorf("writing the addressable channels of %q: %w", id, setErr)
		}
	}
	_, err = n.dispatcher.Dispatch(ctx, notification.Dispatch{
		TypeKey: reviewNotificationTypeKey,
		Recipient: notification.Recipient{
			Class:  notification.RecipientUser,
			UserID: string(id),
		},
		Locale:       string(n.resolver.LocaleFor(ctx, string(id))),
		Params:       map[string]any{"to": plan.To},
		OccurrenceID: occurrenceID,
	})
	if err != nil {
		return reviewNoticePlan{}, true, fmt.Errorf("dispatching the notice of %q: %w", id, err)
	}
	return plan, true, nil
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

// wireReviewControl mounts the review-only control route and opens it on the
// authorization guard's declaration surface.
//
// The route is a review-assembly artefact and nothing more: the module that
// mounts it is compiled only under the review build tag, so a baseline binary
// carries neither the route nor this function. It is registered open because
// the tenant control is an ordinary member's action, not a permission; the
// handler answers for the signed-in caller. Both surfaces are optional in the
// same way wireReviewRecipient's are: a run without the HTTP entry point has no
// route to mount, and one without the guard has no declaration table to write.
func wireReviewControl(reg *core.Registry, notifier *reviewNotifier) error {
	router, err := core.Resolve[speedhttp.Router](reg)
	if errors.Is(err, core.ErrMissingProvider) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%s: taking up the HTTP entry point for the control: %w",
			reviewNotificationModuleName, err)
	}
	endpoint, err := router.Endpoint(reviewNotificationEndpoint)
	if err != nil {
		return fmt.Errorf("%s: looking up the %q endpoint for the control: %w",
			reviewNotificationModuleName, reviewNotificationEndpoint, err)
	}
	gate, err := core.Resolve[authz.Gate](reg)
	switch {
	case err == nil:
		gate.Open(http.MethodPost, reviewNotificationControlPath)
	case !errors.Is(err, core.ErrMissingProvider):
		return fmt.Errorf("%s: taking up the route-declaration surface: %w",
			reviewNotificationModuleName, err)
	}
	endpoint.Route(http.MethodPost, reviewNotificationControlPath, notifier.handleControl)
	return nil
}

// handleControl answers the tenant control: one signed-in member asks the
// review assembly to dispatch one notice to themselves.
//
// The caller is the recipient, so the target is their own account and their own
// identifiers; the tenant comes from the request context the resolution layer
// set. Membership is checked here rather than assumed: the route is open on the
// permission guard, so nothing else would stop an account that does not belong
// to the tenant from asking for a tenant-scoped dispatch. The response is 204:
// the dispatch is enqueued, and the delivery is the pipeline's to settle.
func (n *reviewNotifier) handleControl(c *gin.Context) {
	ctx := c.Request.Context()
	identity, ok := authn.FromContext(ctx)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "the review notice control requires a signed-in account"})
		return
	}
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "the review notice control requires a tenant"})
		return
	}
	member, err := n.members.IsMember(ctx, identity.Account(), string(tenantID))
	if err != nil {
		n.logger.Error(reviewNotificationModuleName+": the control could not read the membership",
			"tenant", string(tenantID), "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "the membership could not be read"})
		return
	}
	if !member {
		c.JSON(http.StatusForbidden, gin.H{"error": "the review notice control is for the tenant's members"})
		return
	}
	plan, ok, err := n.notify(ctx, identity.Account(), uuid.NewString())
	if err != nil {
		n.logger.Error(reviewNotificationModuleName+": the control could not dispatch the notice",
			"tenant", string(tenantID), "account", string(identity.Account()), "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "the review notice could not be dispatched"})
		return
	}
	if !ok {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "the account has neither a proved phone number nor an email address to dispatch to",
		})
		return
	}
	n.logger.Info(reviewNotificationModuleName+": the review notice control dispatched a notice",
		"tenant", string(tenantID), "account", string(identity.Account()), "channels", plan.Channels)
	c.Status(http.StatusNoContent)
}
