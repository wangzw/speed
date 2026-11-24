package main

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
	"github.com/vislake/speed/pkg/config"
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

const (
	demoNotificationModuleName  = "demo.notification"
	demoNotificationJobType     = "demo.notification.dispatch"
	demoNotificationTypeKey     = "demo.notice"
	demoNotificationGroup       = "demo"
	demoNotificationSMSTemplate = "demo-notice"
)

const demoNotificationEnabledItem = "enabled"

type demoNotificationConfig struct {
	Enabled bool `config:"enabled"`
}

func demoNotificationSchema() config.Schema {
	return config.Schema{
		Namespace: demoNotificationModuleName,
		Mounts:    []config.Mount{{Value: &demoNotificationConfig{}}},
		Items: map[string]config.Item{
			demoNotificationEnabledItem: {
				Description: "whether this run dispatches the demo notification fixture's notice " +
					"to each tenant's members; off, the worker, its schedule and its recipient wiring " +
					"are absent",
			},
		},
	}
}

func demoNotificationEnabled(reg *core.Registry) (bool, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return false, fmt.Errorf("%s: the enable switch is read from configuration, and no reader was "+
			"handed to the registry before the assembly began: %w", demoNotificationModuleName, err)
	}
	var cfg demoNotificationConfig
	if err := reader.Decode(demoNotificationModuleName, &cfg); err != nil {
		return false, fmt.Errorf("%s: reading %s.%s: %w", demoNotificationModuleName,
			demoNotificationModuleName, demoNotificationEnabledItem, err)
	}
	return cfg.Enabled, nil
}

const demoNotificationEndpoint = "api"

const demoNotificationInterval = 10 * time.Second

const demoRecipientLayerName = "demo.notification.recipient"

//go:embed demo_notification_locales/*.json
var demoNotificationLocales embed.FS

var demoNotificationCatalog = i18n.Catalog{
	FS:      demoNotificationLocales,
	Pattern: "demo_notification_locales/*.json",
}

var demoNotificationType = notification.TypeSpec{
	Key:   demoNotificationTypeKey,
	Group: demoNotificationGroup,
	DefaultChannels: []notification.Channel{
		notification.ChannelInApp,
		notification.ChannelEmail,
		notification.ChannelSMS,
	},
	Unsubscribable:         true,
	RecipientVisibleParams: []string{"to"},
}

var demoNotificationSchedule = jobs.Schedule{
	Key:      demoNotificationModuleName + ".periodic",
	Type:     demoNotificationJobType,
	Interval: demoNotificationInterval,
}

func init() { core.ProcessRegistry.Register(demoNotificationModule()) }

var _ jobs.Handler = (*demoNotifier)(nil)

func demoNotificationModule() core.Module {
	resources := []any{
		demoNotificationSchema(),
		demoNotificationType,
		demoNotificationCatalog,
		demoNotificationSchedule,
	}
	return core.Module{
		Name: demoNotificationModuleName,
		Requires: []core.Requirement{
			{Token: (*notification.Dispatcher)(nil)},
			{Token: (*notification.Preferences)(nil)},
			{Token: (*authn.Accounts)(nil)},
			{Token: (*authn.Memberships)(nil)},
			{Token: (*sms.Registry)(nil)},
			{Token: (*i18n.Profile)(nil)},
			{Token: (*i18n.Resolver)(nil)},
			{Token: (*jobs.Handlers)(nil)},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Resources: resources,
		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			enabled, err := demoNotificationEnabled(reg)
			if err != nil {
				return core.Enablement{}, err
			}
			if !enabled {
				return core.Enablement{
					State: core.StateDisabled,
					Reason: "demo.notification.enabled is off, so this run dispatches no demo " +
						"notice and the fixture takes no part",
				}, nil
			}
			return core.Enablement{State: core.StateEnabled}, nil
		},
		New: func(_ context.Context, reg *core.Registry) (any, error) {
			return newDemoNotifier(reg)
		},
		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			notifier, ok := instance.(*demoNotifier)
			if !ok || notifier == nil {
				return nil
			}
			handlers, err := core.Resolve[jobs.Handlers](reg)
			if err != nil {
				return fmt.Errorf("%s: taking up the handler registration face: %w",
					demoNotificationModuleName, err)
			}
			if err := handlers.Register(notifier); err != nil {
				return fmt.Errorf("%s: registering the dispatch handler: %w",
					demoNotificationModuleName, err)
			}
			if err := wireDemoRecipient(reg); err != nil {
				return err
			}
			return nil
		},
	}
}

type demoSMSTemplates interface {
	Resolve(sms.Message) (sms.Resolved, error)
}

type demoNotifier struct {
	dispatcher notification.Dispatcher
	prefs      notification.Preferences
	accounts   authn.Accounts
	members    authn.Memberships
	templates  demoSMSTemplates
	profiles   []i18n.Profile
	resolver   i18n.Resolver
	logger     *slog.Logger
}

func newDemoNotifier(reg *core.Registry) (*demoNotifier, error) {
	dispatcher, err := core.Resolve[notification.Dispatcher](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the submission seam: %w",
			demoNotificationModuleName, err)
	}
	accounts, err := core.Resolve[authn.Accounts](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the account store: %w",
			demoNotificationModuleName, err)
	}
	prefs, err := core.Resolve[notification.Preferences](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the preference matrix: %w",
			demoNotificationModuleName, err)
	}
	members, err := core.Resolve[authn.Memberships](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the membership store: %w",
			demoNotificationModuleName, err)
	}
	templates, err := core.Resolve[sms.Registry](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the SMS template registry: %w",
			demoNotificationModuleName, err)
	}
	resolver, err := core.Resolve[i18n.Resolver](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the locale resolver: %w",
			demoNotificationModuleName, err)
	}
	logger := log.Default()
	if delivered, logErr := core.Resolve[log.Logger](reg); logErr == nil {
		logger = delivered.Named(demoNotificationModuleName)
	}
	return &demoNotifier{
		dispatcher: dispatcher,
		prefs:      prefs,
		accounts:   accounts,
		members:    members,
		templates:  templates,
		profiles:   core.ResolveAll[i18n.Profile](reg),
		resolver:   resolver,
		logger:     logger,
	}, nil
}

func (*demoNotifier) Type() string { return demoNotificationJobType }

// Handle turns one periodic job into one dispatch per active member of the
// tenant. A member with no address is skipped rather than failed, because the
// pipeline answers a missing destination as a failed channel and retries the job.
func (n *demoNotifier) Handle(ctx context.Context, job *jobs.Job, _ jobs.ProgressFn) (jobs.Result, error) {
	id, ok := tenant.FromContext(ctx)
	if !ok {
		return jobs.Result{}, fmt.Errorf("%s: %w", demoNotificationModuleName, tenant.ErrNoTenantInContext)
	}
	// The system tenant is the platform's policy domain, not an organization, so
	// the notice is not addressed to its administrators; personal tenants do
	// receive it.
	if string(id) == string(authz.PlatformDomain) {
		return jobs.Result{}, nil
	}
	members, err := n.membersOf(ctx, string(id))
	if err != nil {
		return jobs.Result{}, fmt.Errorf("%s: listing the members of tenant %q: %w",
			demoNotificationModuleName, id, err)
	}
	var failures []error
	for _, member := range members {
		if !member.Active {
			continue
		}
		plan, ok, err := n.notify(ctx, id, member.Account, string(job.ID))
		if err != nil {
			failures = append(failures, fmt.Errorf("dispatching to %q: %w", member.Account, err))
			continue
		}
		if !ok {
			n.logger.Info(demoNotificationModuleName+": a member has no proved phone and no email address, "+
				"so the notice is skipped",
				"tenant", string(id), "account", string(member.Account))
			continue
		}
		n.logger.Debug(demoNotificationModuleName+": a notice was dispatched",
			"tenant", string(id), "account", string(member.Account), "channels", plan.Channels)
	}
	if len(failures) > 0 {
		return jobs.Result{}, fmt.Errorf("%s: %w", demoNotificationModuleName, errors.Join(failures...))
	}
	return jobs.Result{}, nil
}

// notify addresses one recipient on the channels their account can be reached
// on, submits one dispatch in their language, and writes the pruned channel set.
// The second result is false when the account has no proved phone or address.
func (n *demoNotifier) notify(ctx context.Context, tenantID tenant.ID, id authn.AccountID, occurrenceID string) (demoNoticePlan, bool, error) {
	identifiers, err := n.accounts.Identifiers(ctx, id)
	if err != nil {
		return demoNoticePlan{}, false, fmt.Errorf("listing the identifiers of %q: %w", id, err)
	}
	locale := string(n.localeFor(ctx, tenantID, id))
	plan, ok := planDemoNotice(identifiers, n.smsAddressable(locale, identifiers))
	if !ok {
		return demoNoticePlan{}, false, nil
	}
	effective, err := n.prefs.Effective(ctx, string(id), demoNotificationTypeKey)
	if err != nil {
		return demoNoticePlan{}, true, fmt.Errorf("reading the effective channels of %q: %w", id, err)
	}
	if wanted := pruneDemoChannels(effective, plan); len(wanted) != len(effective) {
		if _, setErr := n.prefs.Set(ctx, string(id), demoNotificationTypeKey, wanted); setErr != nil {
			return demoNoticePlan{}, true, fmt.Errorf("writing the addressable channels of %q: %w", id, setErr)
		}
	}
	_, err = n.dispatcher.Dispatch(ctx, notification.Dispatch{
		TypeKey: demoNotificationTypeKey,
		Recipient: notification.Recipient{
			Class:  notification.RecipientUser,
			UserID: string(id),
		},
		Locale:       locale,
		Params:       map[string]any{"to": plan.To},
		OccurrenceID: occurrenceID,
	})
	if err != nil {
		return demoNoticePlan{}, true, fmt.Errorf("dispatching the notice of %q: %w", id, err)
	}
	return plan, true, nil
}

// localeFor answers the language a notice to id goes out in: the account's
// stored preference, not the request's language, else the run's default. Its
// warning names the type and tenant, never the recipient.
func (n *demoNotifier) localeFor(ctx context.Context, tenantID tenant.ID, id authn.AccountID) i18n.Locale {
	readFailed := false
	for _, profile := range n.profiles {
		locale, ok, err := profile.LocaleFor(ctx, string(id))
		if err != nil {
			if !readFailed {
				readFailed = true
				n.logger.Warn(demoNotificationModuleName+": the recipient's language preference could not be "+
					"read through one profile; the notice takes the next answer, or the default language when "+
					"none answers",
					"type", demoNotificationTypeKey, "tenant", string(tenantID), "cause", err.Error())
			}
			continue
		}
		if ok && locale != "" {
			return locale
		}
	}
	return n.resolver.Default()
}

// smsAddressable reports whether the type's sms template is in force for the
// account's proved phone and locale. ErrNotSendable counts as addressable: the
// sending gate refuses it and the pipeline must record that refusal.
func (n *demoNotifier) smsAddressable(locale string, identifiers []authn.Identifier) bool {
	phone, _ := demoNoticeDestinations(identifiers)
	if phone == "" {
		return false
	}
	_, err := n.templates.Resolve(sms.Message{
		To:       phone,
		Template: demoNotificationSMSTemplate,
		Params:   map[string]string{"to": phone},
		Locale:   locale,
	})
	return err == nil || errors.Is(err, sms.ErrNotSendable)
}

func (n *demoNotifier) membersOf(ctx context.Context, tenantID string) ([]authn.Membership, error) {
	const pageSize = 200
	var all []authn.Membership
	after := ""
	for {
		page, err := n.members.Members(ctx, tenantID, authn.MemberQuery{Page: authn.Page{After: after, Limit: pageSize}})
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

// wireDemoRecipient adds the layer naming the preference routes' recipient: the
// caller's effective account, which an impersonation reads as the impersonated
// one. It sits in the recording stage, inside the gateway, outside the guard.
func wireDemoRecipient(reg *core.Registry) error {
	router, err := core.Resolve[speedhttp.Router](reg)
	if errors.Is(err, core.ErrMissingProvider) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%s: taking up the HTTP entry point: %w", demoNotificationModuleName, err)
	}
	endpoint, err := router.Endpoint(demoNotificationEndpoint)
	if err != nil {
		return fmt.Errorf("%s: looking up the %q endpoint: %w",
			demoNotificationModuleName, demoNotificationEndpoint, err)
	}
	endpoint.Use(speedhttp.Middleware{
		Name:    demoRecipientLayerName,
		Stage:   speedhttp.StageRecord,
		Handler: demoRecipientHandler,
	})
	return nil
}

func demoRecipientHandler(c *gin.Context) {
	if id, ok := authn.FromContext(c.Request.Context()); ok {
		c.Request = c.Request.WithContext(
			preferences.WithRecipient(c.Request.Context(), string(id.Account())))
	}
	c.Next()
}

type demoNoticePlan struct {
	To       string
	Channels []notification.Channel
}

// demoNoticeDestinations resolves an account's identifiers into the two
// destinations a dispatch can address. An unproved phone is not a destination
// the product would send a notice to, so it is ignored.
func demoNoticeDestinations(identifiers []authn.Identifier) (phone, email string) {
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

func planDemoNotice(identifiers []authn.Identifier, smsAddressable bool) (demoNoticePlan, bool) {
	phone, email := demoNoticeDestinations(identifiers)
	switch {
	case phone != "" && smsAddressable:
		return demoNoticePlan{
			To:       phone,
			Channels: []notification.Channel{notification.ChannelInApp, notification.ChannelSMS},
		}, true
	case email != "":
		return demoNoticePlan{
			To:       email,
			Channels: []notification.Channel{notification.ChannelInApp, notification.ChannelEmail},
		}, true
	case phone != "":
		return demoNoticePlan{
			To:       phone,
			Channels: []notification.Channel{notification.ChannelInApp},
		}, true
	default:
		return demoNoticePlan{}, false
	}
}

// pruneDemoChannels removes the channels a plan cannot address from the
// recipient's effective set. It only drops a channel, never adds one back, so a
// channel the recipient turned off stays off across every dispatch window.
func pruneDemoChannels(effective []notification.Channel, plan demoNoticePlan) []notification.Channel {
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
