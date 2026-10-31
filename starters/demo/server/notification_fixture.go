package main

// The notification fixture: one notification type the platform baseline does
// not declare, a worker that dispatches it to the members of every tenant, and
// the preference-recipient wiring the baseline deliberately leaves out.
//
// The type is compiled into every build, beside the capabilities. The
// notification capability collects its type taxonomy with core.Resources over
// the available set, not the running set, so this declaration is part of the
// binary's taxonomy even on a run that stands the fixture down. That is the
// compile-time ceiling, and it is deliberate: whether the type is dispatched is
// a run-time decision the module states in Prepare. A run that enables the
// fixture gets the type, the worker and the recipient wiring; another run
// leaves both absent from its feature set even though the code is present.
//
// The fixture's review-only dispatch control — the route the SMS rollout
// journey clicks — is not product API and is compiled only under the review tag;
// see notification_control_review.go.
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
// its destination. A proved phone is not enough on its own: the sms channel is
// chosen only when the SMS template registry holds the type's template for the
// recipient's language — the template the rollout journey registers. Another
// server that never registers it therefore falls back to email (or in-app alone)
// instead of attempting sms with a template the registry does not hold; the
// channel choice lives here, not in the type's default set, so the type's sms
// membership does not leak an attempt into every server. The platform's sending
// gate stays with the delivery pipeline: a held template that is disabled or
// still awaiting review is still chosen, so its refusal is recorded rather than
// hidden. The fixture writes the pruned set through the preference seam because
// the delivery pipeline reads the effective channels when it consumes the job.
//
// The periodic schedule is the member-workspace journey's dispatch: the
// platform's scheduler enqueues one job per tenant per window and the handler
// turns each job into one notification per addressable active member. The
// rollout journey's proof is not that window: it is the review-only control
// route, which a tenant member drives from the console by clicking.
//
// The preference routes read the recipient from the request context, under the
// key preferences.WithRecipient. The notification capability holds no identity,
// so the host's identity side is what names the recipient; the baseline
// deliberately does not, which leaves the matrix empty and its write refused.
// This fixture wires it: a middleware after the authentication gateway copies
// the caller's effective account onto the request context. The layer is
// registered by this module's Init, so a run that stands the fixture down keeps
// the baseline's semantics.

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/jobs"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/notification"
	"github.com/vislake/speed/pkg/notification/preferences"
	"github.com/vislake/speed/pkg/sms"
	"github.com/vislake/speed/pkg/tenant"
)

// The names this fixture owns. The module name is what capabilityModules names
// in the serve roster; the job type is what the schedule enqueues and the
// handler registers; the type key is the vocabulary both taxonomy consumers
// collect. The SMS template name is the catalog entry's value: the rollout
// journey registers a runtime template under exactly this name, with "to" as
// its declared parameter (the dispatch carries no other).
const (
	reviewNotificationModuleName  = "review.notification"
	reviewNotificationJobType     = "review.notification.dispatch"
	reviewNotificationTypeKey     = "review.notice"
	reviewNotificationGroup       = "review"
	reviewNotificationSMSTemplate = "review-notice"
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

// init registers the module. The import is the wiring; capabilityModules is
// what names it for a run.
func init() { core.ProcessRegistry.Register(reviewNotificationModule()) }

// The product must deliver the job handler and report its type.
var _ jobs.Handler = (*reviewNotifier)(nil)

// reviewNotificationModule is the fixture's descriptor.
//
// The dispatcher, the account store, the membership store, the preference
// matrix, the SMS template registry, the locale resolver and the handler
// registration face are required: without them the fixture has nowhere to
// submit a dispatch, no member to address, no recipient-aware channel set to
// write, no way to tell whether the sms channel can carry the type's template
// in the recipient's language, no language to render it in and no place to
// register its job. The logger is optional, as it is for every module, and the
// host's default logger carries the fixture's diagnostics when none is
// delivered.
//
// The review-only dispatch control's feature is contributed by
// reviewControlResources, which answers nothing in a default build. The module
// itself is compiled into every build; whether it runs is stated in Prepare.
func reviewNotificationModule() core.Module {
	resources := []any{
		reviewNotificationType,
		reviewNotificationCatalog,
		reviewNotificationSchedule,
	}
	resources = append(resources, reviewControlResources()...)
	return core.Module{
		Name: reviewNotificationModuleName,
		Requires: []core.Requirement{
			{Token: (*notification.Dispatcher)(nil)},
			{Token: (*notification.Preferences)(nil)},
			{Token: (*authn.Accounts)(nil)},
			{Token: (*authn.Memberships)(nil)},
			{Token: (*sms.Registry)(nil)},
			{Token: (*i18n.Resolver)(nil)},
			{Token: (*jobs.Handlers)(nil)},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Resources: resources,
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
			return mountReviewControl(reg, notifier)
		},
	}
}

// reviewSMSTemplates is the part of the SMS template registry this fixture
// reads before it chooses the sms channel: one resolution, which answers
// whether the registry holds a template for a language and can carry a message
// in it. The narrow shape keeps the decision testable without the registry's
// management surface.
type reviewSMSTemplates interface {
	Resolve(sms.Message) (sms.Resolved, error)
}

// reviewNotifier is the worker's product: the seams one dispatch needs, and the
// logger its skipped members are reported on.
type reviewNotifier struct {
	dispatcher notification.Dispatcher
	prefs      notification.Preferences
	accounts   authn.Accounts
	members    authn.Memberships
	templates  reviewSMSTemplates
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
	templates, err := core.Resolve[sms.Registry](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the SMS template registry: %w",
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
		templates:  templates,
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
	// The system tenant is the platform's policy domain, not a workspace: the
	// notice this fixture dispatches is not addressed to its administrators.
	// Personal tenants are real workspaces and do receive it.
	if string(id) == string(authz.PlatformDomain) {
		return jobs.Result{}, nil
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
// The sms channel is offered to the plan only after the SMS template registry
// reports that it holds the type's template for the recipient's language, so a
// server that never registered the rollout template skips sms instead of
// failing the channel locally; the other channels still deliver. A template
// the platform's sending gate refuses is still chosen, so the pipeline records
// that refusal instead of the fixture hiding it. The resolution happens before
// the plan is chosen because the destination the registry checks is the
// recipient's proved phone, which the plan itself selects.
//
// The second result is false when the account has neither a proved phone nor an
// email address; the caller reports that as a skip, not a failure.
func (n *reviewNotifier) notify(ctx context.Context, id authn.AccountID, occurrenceID string) (reviewNoticePlan, bool, error) {
	identifiers, err := n.accounts.Identifiers(ctx, id)
	if err != nil {
		return reviewNoticePlan{}, false, fmt.Errorf("listing the identifiers of %q: %w", id, err)
	}
	locale := string(n.resolver.LocaleFor(ctx, string(id)))
	plan, ok := planReviewNotice(identifiers, n.smsAddressable(locale, identifiers))
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
		Locale:       locale,
		Params:       map[string]any{"to": plan.To},
		OccurrenceID: occurrenceID,
	})
	if err != nil {
		return reviewNoticePlan{}, true, fmt.Errorf("dispatching the notice of %q: %w", id, err)
	}
	return plan, true, nil
}

// smsAddressable reports whether the type's sms template is one the registry
// holds for the account's proved phone and locale: the template is in force and
// declares the language, with the destination parameter the dispatch carries.
//
// ErrRejected is the one answer that says the registry cannot carry the message
// at all — no such template, no code or body for the language, a parameter the
// template does not declare — and it is what keeps the type's sms membership
// from leaking an attempt into a server that never registered the template.
// ErrNotSendable is deliberately different: the template is held and the
// language is declared, but the platform's sending gate refuses it (the
// template is disabled, the language is still under review, or the deployment's
// sending switch is off). The delivery pipeline is where that gate belongs, and
// its refusal is the product behaviour the rollout journey proves; the fixture
// must not hide it by choosing another channel.
//
// An account with no proved phone has no sms destination and answers false.
func (n *reviewNotifier) smsAddressable(locale string, identifiers []authn.Identifier) bool {
	phone, _ := reviewNoticeDestinations(identifiers)
	if phone == "" {
		return false
	}
	_, err := n.templates.Resolve(sms.Message{
		To:       phone,
		Template: reviewNotificationSMSTemplate,
		Params:   map[string]string{"to": phone},
		Locale:   locale,
	})
	return err == nil || errors.Is(err, sms.ErrNotSendable)
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

// reviewNoticeDestinations resolves an account's identifiers into the two
// destinations a dispatch can address: the proved phone number, which is the
// sms destination, and the email address, which is the email destination. An
// unproved phone is not a destination the product would send a notice to, so it
// is ignored.
func reviewNoticeDestinations(identifiers []authn.Identifier) (phone, email string) {
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
	return phone, email
}

// planReviewNotice resolves one account's identifiers into a delivery plan.
//
// A proved phone number is the sms destination when the registry holds the
// type's template for the recipient's language; otherwise the account falls
// back to its email address, which keeps the in-app and email channels
// reachable. The platform's own sending gate — a disabled template, a language
// still under review, the deployment's sending switch — is not part of this
// choice: the delivery pipeline refuses such a send, and the fixture must let it
// be observed rather than quietly choosing another channel. An account holding
// neither destination is not addressable and is skipped rather than failed. A
// proved phone with neither a held template nor an email address is still
// addressable in-app; the dispatch carries the phone as its "to" parameter,
// which the in-app channel does not read.
func planReviewNotice(identifiers []authn.Identifier, smsAddressable bool) (reviewNoticePlan, bool) {
	phone, email := reviewNoticeDestinations(identifiers)
	switch {
	case phone != "" && smsAddressable:
		return reviewNoticePlan{
			To:       phone,
			Channels: []notification.Channel{notification.ChannelInApp, notification.ChannelSMS},
		}, true
	case email != "":
		return reviewNoticePlan{
			To:       email,
			Channels: []notification.Channel{notification.ChannelInApp, notification.ChannelEmail},
		}, true
	case phone != "":
		return reviewNoticePlan{
			To:       phone,
			Channels: []notification.Channel{notification.ChannelInApp},
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
