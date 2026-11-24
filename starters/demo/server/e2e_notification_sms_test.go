package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/jobs"
	"github.com/vislake/speed/pkg/notification"
	"github.com/vislake/speed/pkg/sms"
)

const (
	e2eSMSTestEmail = "member@example.test"
	e2eSMSTestPhone = "+15551234567"
)

// TestE2ENotificationDeclaresSMSAndItsTemplateBinding holds that the type's
// default channels include sms and its catalog names the journey's template.
func TestE2ENotificationDeclaresSMSAndItsTemplateBinding(t *testing.T) {
	t.Parallel()
	var declared *notification.TypeSpec
	for _, resource := range core.Resources[notification.TypeSpec](core.ProcessRegistry) {
		if resource.Value.Key != demoNotificationTypeKey {
			continue
		}
		spec := resource.Value
		declared = &spec
	}
	if declared == nil {
		t.Fatalf("the available set declares no %q type", demoNotificationTypeKey)
	}

	var haveSMS bool
	for _, channel := range declared.DefaultChannels {
		if channel == notification.ChannelSMS {
			haveSMS = true
		}
	}
	if !haveSMS {
		t.Errorf("the type's default channels are %v; the rollout journey needs %q among them "+
			"or the delivery pipeline never walks the sms channel",
			declared.DefaultChannels, notification.ChannelSMS)
	}
	if !containsString(declared.RecipientVisibleParams, "to") {
		t.Errorf("the type's parameter whitelist is %v; the sms channel reads its destination "+
			"from %q, which must also be a declared template parameter",
			declared.RecipientVisibleParams, "to")
	}

	for _, locale := range []string{e2eDefaultLocale, e2eChineseLocale} {
		got := e2eCatalogEntry(t, locale, demoNotificationTypeKey+".sms.template")
		if got != demoNotificationSMSTemplate {
			t.Errorf("the %s catalog names the sms template %q, want %q",
				locale, got, demoNotificationSMSTemplate)
		}
	}
}

// TestE2ENoticePlansTheExternalChannelFromTheAccount holds that a proved phone with a held template is
// the sms destination, otherwise the account falls back to email or to in-app alone.
func TestE2ENoticePlansTheExternalChannelFromTheAccount(t *testing.T) {
	t.Parallel()
	phone := authn.Identifier{
		Kind:        authn.IdentifierPhone,
		Value:       e2eSMSTestPhone,
		VerifiedVia: authn.VerifiedViaSMSCode,
	}
	email := authn.Identifier{
		Kind:        authn.IdentifierEmail,
		Value:       e2eSMSTestEmail,
		VerifiedVia: authn.VerifiedViaEmailCode,
	}

	cases := []struct {
		name           string
		identifiers    []authn.Identifier
		smsAddressable bool
		wantTo         string
		wantChannels   []notification.Channel
		wantOK         bool
	}{
		{
			name:           "a proved phone with a held template addresses sms",
			identifiers:    []authn.Identifier{email, phone},
			smsAddressable: true,
			wantTo:         e2eSMSTestPhone,
			wantChannels:   []notification.Channel{notification.ChannelInApp, notification.ChannelSMS},
			wantOK:         true,
		},
		{
			name:           "a proved phone without a held template falls back to email",
			identifiers:    []authn.Identifier{email, phone},
			smsAddressable: false,
			wantTo:         e2eSMSTestEmail,
			wantChannels:   []notification.Channel{notification.ChannelInApp, notification.ChannelEmail},
			wantOK:         true,
		},
		{
			name:           "a proved phone without a held template and no email stays in-app",
			identifiers:    []authn.Identifier{phone},
			smsAddressable: false,
			wantTo:         e2eSMSTestPhone,
			wantChannels:   []notification.Channel{notification.ChannelInApp},
			wantOK:         true,
		},
		{
			name:           "an account without a phone falls back to email",
			identifiers:    []authn.Identifier{email},
			smsAddressable: true,
			wantTo:         e2eSMSTestEmail,
			wantChannels:   []notification.Channel{notification.ChannelInApp, notification.ChannelEmail},
			wantOK:         true,
		},
		{
			name: "an unproved phone is not an sms destination",
			identifiers: []authn.Identifier{
				email,
				{Kind: authn.IdentifierPhone, Value: e2eSMSTestPhone, VerifiedVia: authn.VerifiedViaSelf},
			},
			smsAddressable: false,
			wantTo:         e2eSMSTestEmail,
			wantChannels:   []notification.Channel{notification.ChannelInApp, notification.ChannelEmail},
			wantOK:         true,
		},
		{
			name:        "an account with no address is not addressable",
			identifiers: nil,
			wantOK:      false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			plan, ok := planDemoNotice(c.identifiers, c.smsAddressable)
			if ok != c.wantOK {
				t.Fatalf("planDemoNotice answered ok=%t, want %t", ok, c.wantOK)
			}
			if !ok {
				return
			}
			if plan.To != c.wantTo {
				t.Errorf("the destination is %q, want %q", plan.To, c.wantTo)
			}
			if !sameChannels(plan.Channels, c.wantChannels) {
				t.Errorf("the channels are %v, want %v", plan.Channels, c.wantChannels)
			}
		})
	}
}

// TestE2ENoticeSkipsSMSWithoutASendableTemplate holds that an unresolvable template leaves sms out of
// the effective set before the pipeline reads it.
func TestE2ENoticeSkipsSMSWithoutASendableTemplate(t *testing.T) {
	t.Parallel()
	templates := &stubE2ESMSTemplates{err: fmt.Errorf("%w: no template", sms.ErrRejected)}
	prefs := &stubE2EPrefs{
		effective: []notification.Channel{
			notification.ChannelInApp, notification.ChannelEmail, notification.ChannelSMS,
		},
	}
	dispatcher := &stubE2EDispatcher{}
	notifier := &demoNotifier{
		dispatcher: dispatcher,
		prefs:      prefs,
		accounts: stubE2EAccounts{identifiers: []authn.Identifier{
			{Kind: authn.IdentifierEmail, Value: e2eSMSTestEmail, VerifiedVia: authn.VerifiedViaEmailCode},
			{Kind: authn.IdentifierPhone, Value: e2eSMSTestPhone, VerifiedVia: authn.VerifiedViaSMSCode},
		}},
		templates: templates,
		resolver:  stubE2EResolver{locale: e2eChineseLocale},
	}

	plan, ok, err := notifier.notify(context.Background(), "acme", "acct-1", "occurrence-1")
	if err != nil {
		t.Fatalf("notify: %v", err)
	}
	if !ok {
		t.Fatalf("the account is addressable")
	}
	if want := []notification.Channel{notification.ChannelInApp, notification.ChannelEmail}; !sameChannels(plan.Channels, want) {
		t.Errorf("the plan's channels are %v, want %v: a phone with no sendable template must not "+
			"produce an sms attempt", plan.Channels, want)
	}
	if plan.To != e2eSMSTestEmail {
		t.Errorf("the plan's destination is %q, want the email fallback %q", plan.To, e2eSMSTestEmail)
	}
	if want := []notification.Channel{notification.ChannelInApp, notification.ChannelEmail}; !sameChannels(prefs.written, want) {
		t.Errorf("the effective set written through the preference seam is %v, want %v",
			prefs.written, want)
	}
	if dispatcher.dispatch == nil {
		t.Fatalf("notify submitted no dispatch")
	}
	if got := dispatcher.dispatch.Params["to"]; got != e2eSMSTestEmail {
		t.Errorf("the dispatch's destination is %v, want the email fallback %q", got, e2eSMSTestEmail)
	}
	if templates.message.Template != demoNotificationSMSTemplate {
		t.Errorf("the registry was asked for the template %q, want %q",
			templates.message.Template, demoNotificationSMSTemplate)
	}
	if templates.message.Locale != e2eChineseLocale {
		t.Errorf("the registry was asked for the locale %q, want the recipient's %q",
			templates.message.Locale, e2eChineseLocale)
	}
	if templates.message.To != e2eSMSTestPhone {
		t.Errorf("the registry was asked about the destination %q, want the proved phone %q",
			templates.message.To, e2eSMSTestPhone)
	}
}

// TestE2ENoticeKeepsSMSForATemplateThePlatformRefuses holds that a template the sending gate refuses
// is still chosen, so the pipeline records the refusal.
func TestE2ENoticeKeepsSMSForATemplateThePlatformRefuses(t *testing.T) {
	t.Parallel()
	templates := &stubE2ESMSTemplates{
		err: fmt.Errorf("%w: the template is disabled", sms.ErrNotSendable),
	}
	prefs := &stubE2EPrefs{
		effective: []notification.Channel{
			notification.ChannelInApp, notification.ChannelEmail, notification.ChannelSMS,
		},
	}
	dispatcher := &stubE2EDispatcher{}
	notifier := &demoNotifier{
		dispatcher: dispatcher,
		prefs:      prefs,
		accounts: stubE2EAccounts{identifiers: []authn.Identifier{
			{Kind: authn.IdentifierEmail, Value: e2eSMSTestEmail, VerifiedVia: authn.VerifiedViaEmailCode},
			{Kind: authn.IdentifierPhone, Value: e2eSMSTestPhone, VerifiedVia: authn.VerifiedViaSMSCode},
		}},
		templates: templates,
		resolver:  stubE2EResolver{locale: e2eDefaultLocale},
	}

	plan, ok, err := notifier.notify(context.Background(), "acme", "acct-3", "occurrence-3")
	if err != nil {
		t.Fatalf("notify: %v", err)
	}
	if !ok {
		t.Fatalf("the account is addressable")
	}
	if want := []notification.Channel{notification.ChannelInApp, notification.ChannelSMS}; !sameChannels(plan.Channels, want) {
		t.Errorf("the plan's channels are %v, want %v: a held template the platform refuses must "+
			"still reach the pipeline, whose refusal is recorded", plan.Channels, want)
	}
	if plan.To != e2eSMSTestPhone {
		t.Errorf("the plan's destination is %q, want the proved phone %q", plan.To, e2eSMSTestPhone)
	}
	if dispatcher.dispatch == nil {
		t.Fatalf("notify submitted no dispatch")
	}
	if got := dispatcher.dispatch.Params["to"]; got != e2eSMSTestPhone {
		t.Errorf("the dispatch's destination is %v, want the proved phone %q", got, e2eSMSTestPhone)
	}
}

// TestE2ENoticeSendsSMSWithASendableTemplate holds that a resolvable template makes the proved phone
// the destination and keeps sms in the effective set.
func TestE2ENoticeSendsSMSWithASendableTemplate(t *testing.T) {
	t.Parallel()
	prefs := &stubE2EPrefs{
		effective: []notification.Channel{
			notification.ChannelInApp, notification.ChannelEmail, notification.ChannelSMS,
		},
	}
	dispatcher := &stubE2EDispatcher{}
	notifier := &demoNotifier{
		dispatcher: dispatcher,
		prefs:      prefs,
		accounts: stubE2EAccounts{identifiers: []authn.Identifier{
			{Kind: authn.IdentifierEmail, Value: e2eSMSTestEmail, VerifiedVia: authn.VerifiedViaEmailCode},
			{Kind: authn.IdentifierPhone, Value: e2eSMSTestPhone, VerifiedVia: authn.VerifiedViaSMSCode},
		}},
		templates: &stubE2ESMSTemplates{},
		resolver:  stubE2EResolver{locale: e2eDefaultLocale},
	}

	plan, ok, err := notifier.notify(context.Background(), "acme", "acct-2", "occurrence-2")
	if err != nil {
		t.Fatalf("notify: %v", err)
	}
	if !ok {
		t.Fatalf("the account is addressable")
	}
	if want := []notification.Channel{notification.ChannelInApp, notification.ChannelSMS}; !sameChannels(plan.Channels, want) {
		t.Errorf("the plan's channels are %v, want %v", plan.Channels, want)
	}
	if plan.To != e2eSMSTestPhone {
		t.Errorf("the plan's destination is %q, want the proved phone %q", plan.To, e2eSMSTestPhone)
	}
	if want := []notification.Channel{notification.ChannelInApp, notification.ChannelSMS}; !sameChannels(prefs.written, want) {
		t.Errorf("the effective set written through the preference seam is %v, want %v",
			prefs.written, want)
	}
	if dispatcher.dispatch == nil {
		t.Fatalf("notify submitted no dispatch")
	}
	if got := dispatcher.dispatch.Params["to"]; got != e2eSMSTestPhone {
		t.Errorf("the dispatch's destination is %v, want the proved phone %q", got, e2eSMSTestPhone)
	}
}

type stubE2ESMSTemplates struct {
	err     error
	message sms.Message
}

func (s *stubE2ESMSTemplates) Resolve(m sms.Message) (sms.Resolved, error) {
	s.message = m
	return sms.Resolved{}, s.err
}

type stubE2EAccounts struct {
	authn.Accounts
	identifiers []authn.Identifier
}

func (s stubE2EAccounts) Identifiers(context.Context, authn.AccountID) ([]authn.Identifier, error) {
	return s.identifiers, nil
}

type stubE2EPrefs struct {
	notification.Preferences
	effective []notification.Channel
	written   []notification.Channel
}

func (s *stubE2EPrefs) Effective(context.Context, string, string) ([]notification.Channel, error) {
	return s.effective, nil
}

func (s *stubE2EPrefs) Set(_ context.Context, _, _ string, channels []notification.Channel) ([]notification.Channel, error) {
	s.written = append([]notification.Channel(nil), channels...)
	return s.written, nil
}

type stubE2EDispatcher struct {
	notification.Dispatcher
	dispatch *notification.Dispatch
}

func (s *stubE2EDispatcher) Dispatch(_ context.Context, d notification.Dispatch) (jobs.JobID, error) {
	s.dispatch = &d
	return "job-1", nil
}

type stubE2EResolver struct {
	i18n.Resolver
	locale string
}

func (s stubE2EResolver) Default() i18n.Locale { return i18n.Locale(s.locale) }

type stubE2EProfile struct {
	i18n.Profile
	locale i18n.Locale
}

func (s stubE2EProfile) LocaleFor(context.Context, string) (i18n.Locale, bool, error) {
	return s.locale, s.locale != "", nil
}

func (s stubE2EProfile) TimeZoneFor(context.Context, string) (string, bool, error) {
	return "", false, nil
}

type failingE2EProfile struct{}

func (failingE2EProfile) LocaleFor(context.Context, string) (i18n.Locale, bool, error) {
	return "", false, errors.New("the account store is unavailable")
}

func (failingE2EProfile) TimeZoneFor(context.Context, string) (string, bool, error) {
	return "", false, nil
}

// TestE2ENoticeGoesOutInTheRecipientsStoredLanguage holds that the dispatch locale is the recipient's
// stored preference, not the request's language.
func TestE2ENoticeGoesOutInTheRecipientsStoredLanguage(t *testing.T) {
	t.Parallel()
	dispatcher := &stubE2EDispatcher{}
	notifier := &demoNotifier{
		dispatcher: dispatcher,
		prefs: &stubE2EPrefs{
			effective: []notification.Channel{notification.ChannelInApp, notification.ChannelEmail},
		},
		accounts: stubE2EAccounts{identifiers: []authn.Identifier{
			{Kind: authn.IdentifierEmail, Value: e2eSMSTestEmail, VerifiedVia: authn.VerifiedViaEmailCode},
		}},
		templates: &stubE2ESMSTemplates{},
		profiles:  []i18n.Profile{stubE2EProfile{locale: e2eChineseLocale}},
		resolver:  stubE2EResolver{locale: e2eDefaultLocale},
	}
	ctx := i18n.WithLocale(context.Background(), e2eDefaultLocale)

	if _, ok, err := notifier.notify(ctx, "acme", "acct-locale", "occurrence-locale"); err != nil || !ok {
		t.Fatalf("notify: ok=%t err=%v", ok, err)
	}
	if dispatcher.dispatch == nil {
		t.Fatalf("notify submitted no dispatch")
	}
	if got := dispatcher.dispatch.Locale; got != e2eChineseLocale {
		t.Errorf("the dispatch's locale is %q, want the recipient's stored %q, not the request's %q",
			got, e2eChineseLocale, e2eDefaultLocale)
	}
}

// TestE2ENoticeFallsBackToTheDefaultLanguageWithoutAStoredPreference holds that an account with no
// preference receives the notice in the run's default.
func TestE2ENoticeFallsBackToTheDefaultLanguageWithoutAStoredPreference(t *testing.T) {
	t.Parallel()
	dispatcher := &stubE2EDispatcher{}
	notifier := &demoNotifier{
		dispatcher: dispatcher,
		prefs: &stubE2EPrefs{
			effective: []notification.Channel{notification.ChannelInApp, notification.ChannelEmail},
		},
		accounts: stubE2EAccounts{identifiers: []authn.Identifier{
			{Kind: authn.IdentifierEmail, Value: e2eSMSTestEmail, VerifiedVia: authn.VerifiedViaEmailCode},
		}},
		templates: &stubE2ESMSTemplates{},
		profiles:  []i18n.Profile{stubE2EProfile{}},
		resolver:  stubE2EResolver{locale: e2eThirdLocale},
	}
	ctx := i18n.WithLocale(context.Background(), e2eChineseLocale)

	if _, ok, err := notifier.notify(ctx, "acme", "acct-default", "occurrence-default"); err != nil || !ok {
		t.Fatalf("notify: ok=%t err=%v", ok, err)
	}
	if dispatcher.dispatch == nil {
		t.Fatalf("notify submitted no dispatch")
	}
	if got := dispatcher.dispatch.Locale; got != e2eThirdLocale {
		t.Errorf("the dispatch's locale is %q, want the run's default %q, not the request's %q "+
			"and not a fixed %q", got, e2eThirdLocale, e2eChineseLocale, e2eDefaultLocale)
	}
}

type recordingLogs struct {
	mu      sync.Mutex
	records []slog.Record
}

func (*recordingLogs) Enabled(context.Context, slog.Level) bool { return true }

func (r *recordingLogs) Handle(_ context.Context, rec slog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, rec.Clone())
	return nil
}

func (r *recordingLogs) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *recordingLogs) WithGroup(string) slog.Handler      { return r }

// TestE2ENoticeFallsBackToTheDefaultWhenThePreferenceReadFails holds that a failed read still
// dispatches in the default and warns without the recipient's identifier.
func TestE2ENoticeFallsBackToTheDefaultWhenThePreferenceReadFails(t *testing.T) {
	t.Parallel()
	dispatcher := &stubE2EDispatcher{}
	logs := &recordingLogs{}
	notifier := &demoNotifier{
		dispatcher: dispatcher,
		prefs: &stubE2EPrefs{
			effective: []notification.Channel{notification.ChannelInApp, notification.ChannelEmail},
		},
		accounts: stubE2EAccounts{identifiers: []authn.Identifier{
			{Kind: authn.IdentifierEmail, Value: e2eSMSTestEmail, VerifiedVia: authn.VerifiedViaEmailCode},
		}},
		templates: &stubE2ESMSTemplates{},
		profiles:  []i18n.Profile{failingE2EProfile{}},
		resolver:  stubE2EResolver{locale: e2eThirdLocale},
		logger:    slog.New(logs),
	}
	ctx := i18n.WithLocale(context.Background(), e2eChineseLocale)

	if _, ok, err := notifier.notify(ctx, "acme", "acct-unreadable", "occurrence-unreadable"); err != nil || !ok {
		t.Fatalf("notify: ok=%t err=%v", ok, err)
	}
	if dispatcher.dispatch == nil {
		t.Fatalf("notify submitted no dispatch")
	}
	if got := dispatcher.dispatch.Locale; got != e2eThirdLocale {
		t.Errorf("the dispatch's locale is %q, want the run's default %q after a failed read",
			got, e2eThirdLocale)
	}
	logs.mu.Lock()
	records := append([]slog.Record(nil), logs.records...)
	logs.mu.Unlock()
	var warned []slog.Record
	for _, rec := range records {
		if rec.Level == slog.LevelWarn && strings.Contains(rec.Message, "language preference could not be read") {
			warned = append(warned, rec)
		}
	}
	if len(warned) != 1 {
		t.Fatalf("a failed preference read wrote %d warning(s), want one", len(warned))
	}
	attrValue := func(rec slog.Record, key string) (string, bool) {
		var value string
		var found bool
		rec.Attrs(func(a slog.Attr) bool {
			if a.Key == key {
				value = a.Value.String()
				found = true
				return false
			}
			return true
		})
		return value, found
	}
	if got, _ := attrValue(warned[0], "type"); got != demoNotificationTypeKey {
		t.Errorf("the warning names type %q, want %q", got, demoNotificationTypeKey)
	}
	if tenant, found := attrValue(warned[0], "tenant"); !found || tenant != "acme" {
		t.Errorf("the warning names tenant %q found=%t, want the tenant the notice was dispatched for, %q",
			tenant, found, "acme")
	}
	if _, found := attrValue(warned[0], "account"); found {
		t.Errorf("the warning carries the recipient's identifier; the line names the notice, not the person")
	}
	warned[0].Attrs(func(attribute slog.Attr) bool {
		if strings.Contains(attribute.Value.String(), "acct-unreadable") {
			t.Errorf("the warning's %q attribute carries the recipient's identifier", attribute.Key)
		}
		return true
	})
}

// TestE2ENoticeAsksEveryProfileAfterAFailedRead holds that a later profile's preference decides after
// one read fails, with exactly one warning.
func TestE2ENoticeAsksEveryProfileAfterAFailedRead(t *testing.T) {
	t.Parallel()
	dispatcher := &stubE2EDispatcher{}
	logs := &recordingLogs{}
	notifier := &demoNotifier{
		dispatcher: dispatcher,
		prefs: &stubE2EPrefs{
			effective: []notification.Channel{notification.ChannelInApp, notification.ChannelEmail},
		},
		accounts: stubE2EAccounts{identifiers: []authn.Identifier{
			{Kind: authn.IdentifierEmail, Value: e2eSMSTestEmail, VerifiedVia: authn.VerifiedViaEmailCode},
		}},
		templates: &stubE2ESMSTemplates{},
		profiles: []i18n.Profile{
			failingE2EProfile{},
			stubE2EProfile{locale: e2eChineseLocale},
		},
		resolver: stubE2EResolver{locale: e2eThirdLocale},
		logger:   slog.New(logs),
	}
	ctx := i18n.WithLocale(context.Background(), e2eThirdLocale)

	if _, ok, err := notifier.notify(ctx, "acme", "acct-two-profiles", "occurrence-two-profiles"); err != nil || !ok {
		t.Fatalf("notify: ok=%t err=%v", ok, err)
	}
	if dispatcher.dispatch == nil {
		t.Fatalf("notify submitted no dispatch")
	}
	if got := dispatcher.dispatch.Locale; got != e2eChineseLocale {
		t.Errorf("the dispatch's locale is %q, want the second profile's stored %q: "+
			"one profile's failed read says nothing about the next", got, e2eChineseLocale)
	}

	logs.mu.Lock()
	records := append([]slog.Record(nil), logs.records...)
	logs.mu.Unlock()
	var warned []slog.Record
	for _, rec := range records {
		if rec.Level == slog.LevelWarn && strings.Contains(rec.Message, "language preference could not be read") {
			warned = append(warned, rec)
		}
	}
	if len(warned) != 1 {
		t.Fatalf("the failed read wrote %d warning(s), want exactly one for one failing profile", len(warned))
	}
	warned[0].Attrs(func(attribute slog.Attr) bool {
		if strings.Contains(attribute.Value.String(), "acct-two-profiles") {
			t.Errorf("the warning's %q attribute carries the recipient's identifier", attribute.Key)
		}
		return true
	})
}

// TestE2ENoticePrunesOnlyUnaddressableChannels holds that the pre-dispatch write drops an
// unaddressable channel and never adds one back.
func TestE2ENoticePrunesOnlyUnaddressableChannels(t *testing.T) {
	t.Parallel()
	noPhone := demoNoticePlan{
		To:       e2eSMSTestEmail,
		Channels: []notification.Channel{notification.ChannelInApp, notification.ChannelEmail},
	}
	withPhone := demoNoticePlan{
		To:       e2eSMSTestPhone,
		Channels: []notification.Channel{notification.ChannelInApp, notification.ChannelSMS},
	}
	cases := []struct {
		name      string
		effective []notification.Channel
		plan      demoNoticePlan
		want      []notification.Channel
	}{
		{
			name:      "the declared defaults lose sms without a phone",
			effective: []notification.Channel{notification.ChannelInApp, notification.ChannelEmail, notification.ChannelSMS},
			plan:      noPhone,
			want:      []notification.Channel{notification.ChannelInApp, notification.ChannelEmail},
		},
		{
			name:      "a stored set that already dropped email is left alone",
			effective: []notification.Channel{notification.ChannelInApp},
			plan:      noPhone,
			want:      []notification.Channel{notification.ChannelInApp},
		},
		{
			name:      "the declared defaults lose email when a phone addresses sms",
			effective: []notification.Channel{notification.ChannelInApp, notification.ChannelEmail, notification.ChannelSMS},
			plan:      withPhone,
			want:      []notification.Channel{notification.ChannelInApp, notification.ChannelSMS},
		},
		{
			name:      "a recipient who turned sms off keeps it off",
			effective: []notification.Channel{notification.ChannelInApp, notification.ChannelEmail},
			plan:      withPhone,
			want:      []notification.Channel{notification.ChannelInApp},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := pruneDemoChannels(c.effective, c.plan); !sameChannels(got, c.want) {
				t.Errorf("the pruned set is %v, want %v", got, c.want)
			}
		})
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func sameChannels(a, b []notification.Channel) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
