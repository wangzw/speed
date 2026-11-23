//go:build review

package main

// The review assembly's SMS side of the notification fixture: the type carries
// the closed channel set's sms member and names the template the rollout
// journey registers, and a recipient is addressed on the external channel their
// account can actually be reached on. A recipient with a proved phone is
// addressed by sms; one with only an email address falls back to email, and sms
// is removed from that recipient's effective set rather than attempted with the
// email address as its destination.

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
	"github.com/vislake/speed/pkg/feature"
	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/jobs"
	"github.com/vislake/speed/pkg/notification"
	"github.com/vislake/speed/pkg/sms"
)

const (
	reviewSMSTestEmail = "member@example.test"
	reviewSMSTestPhone = "+15551234567"
)

// TestReviewNotificationDeclaresSMSAndItsTemplateBinding holds the declaration
// side of the rollout journey: the review type's default channels include sms,
// its recipient-visible whitelist admits the destination the sms channel reads,
// and its catalog names the template the journey registers in every language.
func TestReviewNotificationDeclaresSMSAndItsTemplateBinding(t *testing.T) {
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

	for _, locale := range []string{reviewDefaultLocale, reviewChineseLocale} {
		got := reviewCatalogEntry(t, locale, demoNotificationTypeKey+".sms.template")
		if got != demoNotificationSMSTemplate {
			t.Errorf("the %s catalog names the sms template %q, want %q",
				locale, got, demoNotificationSMSTemplate)
		}
	}
}

// TestReviewNotificationControlFeatureIsDeclared holds the review-only gate the
// tenant control reads: the review module declares it as a static feature, so
// it is present exactly when that module is in force and absent from every
// assembly that does not compile it.
func TestReviewNotificationControlFeatureIsDeclared(t *testing.T) {
	t.Parallel()
	declared := false
	for _, resource := range core.Resources[feature.Spec](core.ProcessRegistry) {
		if resource.Module != demoNotificationModuleName ||
			resource.Value.Name != reviewNotificationControlFeature {
			continue
		}
		if resource.Value.Conditional {
			t.Errorf("the control feature %q is conditional, so a review run that does not "+
				"post it offers no control", resource.Value.Name)
		}
		declared = true
	}
	if !declared {
		t.Fatalf("the review module declares no %q feature, so the tenant control can never "+
			"be offered", reviewNotificationControlFeature)
	}
}

// TestReviewNoticePlansTheExternalChannelFromTheAccount holds the per-recipient
// choice: a proved phone whose template the registry holds for the language is
// the destination and sms the external channel; without a held template the
// account falls back to its email address, or to in-app alone when it has none;
// an account with only an email address falls back to email without sms.
func TestReviewNoticePlansTheExternalChannelFromTheAccount(t *testing.T) {
	t.Parallel()
	phone := authn.Identifier{
		Kind:        authn.IdentifierPhone,
		Value:       reviewSMSTestPhone,
		VerifiedVia: authn.VerifiedViaSMSCode,
	}
	email := authn.Identifier{
		Kind:        authn.IdentifierEmail,
		Value:       reviewSMSTestEmail,
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
			wantTo:         reviewSMSTestPhone,
			wantChannels:   []notification.Channel{notification.ChannelInApp, notification.ChannelSMS},
			wantOK:         true,
		},
		{
			name:           "a proved phone without a held template falls back to email",
			identifiers:    []authn.Identifier{email, phone},
			smsAddressable: false,
			wantTo:         reviewSMSTestEmail,
			wantChannels:   []notification.Channel{notification.ChannelInApp, notification.ChannelEmail},
			wantOK:         true,
		},
		{
			name:           "a proved phone without a held template and no email stays in-app",
			identifiers:    []authn.Identifier{phone},
			smsAddressable: false,
			wantTo:         reviewSMSTestPhone,
			wantChannels:   []notification.Channel{notification.ChannelInApp},
			wantOK:         true,
		},
		{
			name:           "an account without a phone falls back to email",
			identifiers:    []authn.Identifier{email},
			smsAddressable: true,
			wantTo:         reviewSMSTestEmail,
			wantChannels:   []notification.Channel{notification.ChannelInApp, notification.ChannelEmail},
			wantOK:         true,
		},
		{
			name: "an unproved phone is not an sms destination",
			identifiers: []authn.Identifier{
				email,
				{Kind: authn.IdentifierPhone, Value: reviewSMSTestPhone, VerifiedVia: authn.VerifiedViaSelf},
			},
			smsAddressable: false,
			wantTo:         reviewSMSTestEmail,
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

// TestReviewNoticeSkipsSMSWithoutASendableTemplate drives the decision through
// notify: a member with a proved phone and an email address whose SMS template
// the registry cannot resolve is dispatched on in-app and email, and sms is
// removed from the effective set before the pipeline reads it. The registry is
// asked first, so a server that never registered the rollout template does not
// attempt sms at all.
func TestReviewNoticeSkipsSMSWithoutASendableTemplate(t *testing.T) {
	t.Parallel()
	templates := &stubReviewSMSTemplates{err: fmt.Errorf("%w: no template", sms.ErrRejected)}
	prefs := &stubReviewPrefs{
		effective: []notification.Channel{
			notification.ChannelInApp, notification.ChannelEmail, notification.ChannelSMS,
		},
	}
	dispatcher := &stubReviewDispatcher{}
	notifier := &demoNotifier{
		dispatcher: dispatcher,
		prefs:      prefs,
		accounts: stubReviewAccounts{identifiers: []authn.Identifier{
			{Kind: authn.IdentifierEmail, Value: reviewSMSTestEmail, VerifiedVia: authn.VerifiedViaEmailCode},
			{Kind: authn.IdentifierPhone, Value: reviewSMSTestPhone, VerifiedVia: authn.VerifiedViaSMSCode},
		}},
		templates: templates,
		resolver:  stubReviewResolver{locale: reviewChineseLocale},
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
	if plan.To != reviewSMSTestEmail {
		t.Errorf("the plan's destination is %q, want the email fallback %q", plan.To, reviewSMSTestEmail)
	}
	if want := []notification.Channel{notification.ChannelInApp, notification.ChannelEmail}; !sameChannels(prefs.written, want) {
		t.Errorf("the effective set written through the preference seam is %v, want %v",
			prefs.written, want)
	}
	if dispatcher.dispatch == nil {
		t.Fatalf("notify submitted no dispatch")
	}
	if got := dispatcher.dispatch.Params["to"]; got != reviewSMSTestEmail {
		t.Errorf("the dispatch's destination is %v, want the email fallback %q", got, reviewSMSTestEmail)
	}
	if templates.message.Template != demoNotificationSMSTemplate {
		t.Errorf("the registry was asked for the template %q, want %q",
			templates.message.Template, demoNotificationSMSTemplate)
	}
	if templates.message.Locale != reviewChineseLocale {
		t.Errorf("the registry was asked for the locale %q, want the recipient's %q",
			templates.message.Locale, reviewChineseLocale)
	}
	if templates.message.To != reviewSMSTestPhone {
		t.Errorf("the registry was asked about the destination %q, want the proved phone %q",
			templates.message.To, reviewSMSTestPhone)
	}
}

// TestReviewNoticeKeepsSMSForATemplateThePlatformRefuses holds the other side
// of the selection rule: a template the registry holds for the language but the
// platform's sending gate refuses (disabled, awaiting review, or the sending
// switch off) is still chosen, so the delivery pipeline records the refusal.
// The fixture must not turn the platform's gate into a silent channel swap.
func TestReviewNoticeKeepsSMSForATemplateThePlatformRefuses(t *testing.T) {
	t.Parallel()
	templates := &stubReviewSMSTemplates{
		err: fmt.Errorf("%w: the template is disabled", sms.ErrNotSendable),
	}
	prefs := &stubReviewPrefs{
		effective: []notification.Channel{
			notification.ChannelInApp, notification.ChannelEmail, notification.ChannelSMS,
		},
	}
	dispatcher := &stubReviewDispatcher{}
	notifier := &demoNotifier{
		dispatcher: dispatcher,
		prefs:      prefs,
		accounts: stubReviewAccounts{identifiers: []authn.Identifier{
			{Kind: authn.IdentifierEmail, Value: reviewSMSTestEmail, VerifiedVia: authn.VerifiedViaEmailCode},
			{Kind: authn.IdentifierPhone, Value: reviewSMSTestPhone, VerifiedVia: authn.VerifiedViaSMSCode},
		}},
		templates: templates,
		resolver:  stubReviewResolver{locale: reviewDefaultLocale},
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
	if plan.To != reviewSMSTestPhone {
		t.Errorf("the plan's destination is %q, want the proved phone %q", plan.To, reviewSMSTestPhone)
	}
	if dispatcher.dispatch == nil {
		t.Fatalf("notify submitted no dispatch")
	}
	if got := dispatcher.dispatch.Params["to"]; got != reviewSMSTestPhone {
		t.Errorf("the dispatch's destination is %v, want the proved phone %q", got, reviewSMSTestPhone)
	}
}

// TestReviewNoticeSendsSMSWithASendableTemplate holds the other direction: when
// the registry resolves the type's template in the recipient's language, the
// phones address is the destination and sms stays in the effective set.
func TestReviewNoticeSendsSMSWithASendableTemplate(t *testing.T) {
	t.Parallel()
	prefs := &stubReviewPrefs{
		effective: []notification.Channel{
			notification.ChannelInApp, notification.ChannelEmail, notification.ChannelSMS,
		},
	}
	dispatcher := &stubReviewDispatcher{}
	notifier := &demoNotifier{
		dispatcher: dispatcher,
		prefs:      prefs,
		accounts: stubReviewAccounts{identifiers: []authn.Identifier{
			{Kind: authn.IdentifierEmail, Value: reviewSMSTestEmail, VerifiedVia: authn.VerifiedViaEmailCode},
			{Kind: authn.IdentifierPhone, Value: reviewSMSTestPhone, VerifiedVia: authn.VerifiedViaSMSCode},
		}},
		templates: &stubReviewSMSTemplates{},
		resolver:  stubReviewResolver{locale: reviewDefaultLocale},
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
	if plan.To != reviewSMSTestPhone {
		t.Errorf("the plan's destination is %q, want the proved phone %q", plan.To, reviewSMSTestPhone)
	}
	if want := []notification.Channel{notification.ChannelInApp, notification.ChannelSMS}; !sameChannels(prefs.written, want) {
		t.Errorf("the effective set written through the preference seam is %v, want %v",
			prefs.written, want)
	}
	if dispatcher.dispatch == nil {
		t.Fatalf("notify submitted no dispatch")
	}
	if got := dispatcher.dispatch.Params["to"]; got != reviewSMSTestPhone {
		t.Errorf("the dispatch's destination is %v, want the proved phone %q", got, reviewSMSTestPhone)
	}
}

// stubReviewSMSTemplates answers Resolve with the error the case set and keeps
// the message it was asked about. A nil error is a sendable template.
type stubReviewSMSTemplates struct {
	err     error
	message sms.Message
}

func (s *stubReviewSMSTemplates) Resolve(m sms.Message) (sms.Resolved, error) {
	s.message = m
	return sms.Resolved{}, s.err
}

// stubReviewAccounts answers Identifiers; the embedded interface makes the
// other reads unreachable without listing them.
type stubReviewAccounts struct {
	authn.Accounts
	identifiers []authn.Identifier
}

func (s stubReviewAccounts) Identifiers(context.Context, authn.AccountID) ([]authn.Identifier, error) {
	return s.identifiers, nil
}

// stubReviewPrefs answers the effective set and records the write.
type stubReviewPrefs struct {
	notification.Preferences
	effective []notification.Channel
	written   []notification.Channel
}

func (s *stubReviewPrefs) Effective(context.Context, string, string) ([]notification.Channel, error) {
	return s.effective, nil
}

func (s *stubReviewPrefs) Set(_ context.Context, _, _ string, channels []notification.Channel) ([]notification.Channel, error) {
	s.written = append([]notification.Channel(nil), channels...)
	return s.written, nil
}

// stubReviewDispatcher records the submitted dispatch.
type stubReviewDispatcher struct {
	notification.Dispatcher
	dispatch *notification.Dispatch
}

func (s *stubReviewDispatcher) Dispatch(_ context.Context, d notification.Dispatch) (jobs.JobID, error) {
	s.dispatch = &d
	return "job-1", nil
}

// stubReviewResolver answers the run's default language.
type stubReviewResolver struct {
	i18n.Resolver
	locale string
}

func (s stubReviewResolver) Default() i18n.Locale { return i18n.Locale(s.locale) }

// stubReviewProfile answers the language an account carries.
type stubReviewProfile struct {
	i18n.Profile
	locale i18n.Locale
}

func (s stubReviewProfile) LocaleFor(context.Context, string) (i18n.Locale, bool, error) {
	return s.locale, s.locale != "", nil
}

func (s stubReviewProfile) TimeZoneFor(context.Context, string) (string, bool, error) {
	return "", false, nil
}

// failingReviewProfile is a profile whose preference read fails.
type failingReviewProfile struct{}

func (failingReviewProfile) LocaleFor(context.Context, string) (i18n.Locale, bool, error) {
	return "", false, errors.New("the account store is unavailable")
}

func (failingReviewProfile) TimeZoneFor(context.Context, string) (string, bool, error) {
	return "", false, nil
}

// TestReviewNoticeGoesOutInTheRecipientsStoredLanguage holds the language
// source: the dispatch's locale is the recipient's stored preference, not the
// language of the request context. The worker runs with no request at all and
// the review control runs under the recipient's own request, so a shared rule
// that read the request language would send the notice in the browser's
// language instead of the account's.
func TestReviewNoticeGoesOutInTheRecipientsStoredLanguage(t *testing.T) {
	t.Parallel()
	dispatcher := &stubReviewDispatcher{}
	notifier := &demoNotifier{
		dispatcher: dispatcher,
		prefs: &stubReviewPrefs{
			effective: []notification.Channel{notification.ChannelInApp, notification.ChannelEmail},
		},
		accounts: stubReviewAccounts{identifiers: []authn.Identifier{
			{Kind: authn.IdentifierEmail, Value: reviewSMSTestEmail, VerifiedVia: authn.VerifiedViaEmailCode},
		}},
		templates: &stubReviewSMSTemplates{},
		profiles:  []i18n.Profile{stubReviewProfile{locale: reviewChineseLocale}},
		resolver:  stubReviewResolver{locale: reviewDefaultLocale},
	}
	ctx := i18n.WithLocale(context.Background(), reviewDefaultLocale)

	if _, ok, err := notifier.notify(ctx, "acme", "acct-locale", "occurrence-locale"); err != nil || !ok {
		t.Fatalf("notify: ok=%t err=%v", ok, err)
	}
	if dispatcher.dispatch == nil {
		t.Fatalf("notify submitted no dispatch")
	}
	if got := dispatcher.dispatch.Locale; got != reviewChineseLocale {
		t.Errorf("the dispatch's locale is %q, want the recipient's stored %q, not the request's %q",
			got, reviewChineseLocale, reviewDefaultLocale)
	}
}

// TestReviewNoticeFallsBackToTheDefaultLanguageWithoutAStoredPreference holds
// the other side: an account that stores no preference receives the notice in
// the run's default, still regardless of the request's language. The default is
// a third language, neither the request's nor "en-US", so an implementation
// that hard-coded English fails this case.
func TestReviewNoticeFallsBackToTheDefaultLanguageWithoutAStoredPreference(t *testing.T) {
	t.Parallel()
	dispatcher := &stubReviewDispatcher{}
	notifier := &demoNotifier{
		dispatcher: dispatcher,
		prefs: &stubReviewPrefs{
			effective: []notification.Channel{notification.ChannelInApp, notification.ChannelEmail},
		},
		accounts: stubReviewAccounts{identifiers: []authn.Identifier{
			{Kind: authn.IdentifierEmail, Value: reviewSMSTestEmail, VerifiedVia: authn.VerifiedViaEmailCode},
		}},
		templates: &stubReviewSMSTemplates{},
		profiles:  []i18n.Profile{stubReviewProfile{}},
		resolver:  stubReviewResolver{locale: reviewThirdLocale},
	}
	ctx := i18n.WithLocale(context.Background(), reviewChineseLocale)

	if _, ok, err := notifier.notify(ctx, "acme", "acct-default", "occurrence-default"); err != nil || !ok {
		t.Fatalf("notify: ok=%t err=%v", ok, err)
	}
	if dispatcher.dispatch == nil {
		t.Fatalf("notify submitted no dispatch")
	}
	if got := dispatcher.dispatch.Locale; got != reviewThirdLocale {
		t.Errorf("the dispatch's locale is %q, want the run's default %q, not the request's %q "+
			"and not a fixed %q", got, reviewThirdLocale, reviewChineseLocale, reviewDefaultLocale)
	}
}

// recordingLogs keeps the records the notifier writes.
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

// TestReviewNoticeFallsBackToTheDefaultWhenThePreferenceReadFails holds the
// state a failed read is: the notice goes out in the run's default, the
// dispatch still happens, and a warning names the notice's declared type and
// tenant without the recipient's identifier anywhere in its fields. The default
// is a third language, so a hard-coded "en-US" fails this case too.
func TestReviewNoticeFallsBackToTheDefaultWhenThePreferenceReadFails(t *testing.T) {
	t.Parallel()
	dispatcher := &stubReviewDispatcher{}
	logs := &recordingLogs{}
	notifier := &demoNotifier{
		dispatcher: dispatcher,
		prefs: &stubReviewPrefs{
			effective: []notification.Channel{notification.ChannelInApp, notification.ChannelEmail},
		},
		accounts: stubReviewAccounts{identifiers: []authn.Identifier{
			{Kind: authn.IdentifierEmail, Value: reviewSMSTestEmail, VerifiedVia: authn.VerifiedViaEmailCode},
		}},
		templates: &stubReviewSMSTemplates{},
		profiles:  []i18n.Profile{failingReviewProfile{}},
		resolver:  stubReviewResolver{locale: reviewThirdLocale},
		logger:    slog.New(logs),
	}
	ctx := i18n.WithLocale(context.Background(), reviewChineseLocale)

	if _, ok, err := notifier.notify(ctx, "acme", "acct-unreadable", "occurrence-unreadable"); err != nil || !ok {
		t.Fatalf("notify: ok=%t err=%v", ok, err)
	}
	if dispatcher.dispatch == nil {
		t.Fatalf("notify submitted no dispatch")
	}
	if got := dispatcher.dispatch.Locale; got != reviewThirdLocale {
		t.Errorf("the dispatch's locale is %q, want the run's default %q after a failed read",
			got, reviewThirdLocale)
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
	// The tenant the call was made for is the one the line names, and it never
	// falls back to the recipient.
	if tenant, found := attrValue(warned[0], "tenant"); !found || tenant != "acme" {
		t.Errorf("the warning names tenant %q found=%t, want the tenant the notice was dispatched for, %q",
			tenant, found, "acme")
	}
	if _, found := attrValue(warned[0], "account"); found {
		t.Errorf("the warning carries the recipient's identifier; the line names the notice, not the person")
	}
	// The identifier stays out of every field, not just the one it used to sit
	// in: cause and recipient carry values too.
	warned[0].Attrs(func(attribute slog.Attr) bool {
		if strings.Contains(attribute.Value.String(), "acct-unreadable") {
			t.Errorf("the warning's %q attribute carries the recipient's identifier", attribute.Key)
		}
		return true
	})
}

// TestReviewNoticeAsksEveryProfileAfterAFailedRead holds that a failed read in
// one profile does not end the search: a later profile's stored preference
// decides the dispatch, and exactly one warning is written. The fallback is a
// third language, so answering with the resolver's default fails this case too.
func TestReviewNoticeAsksEveryProfileAfterAFailedRead(t *testing.T) {
	t.Parallel()
	dispatcher := &stubReviewDispatcher{}
	logs := &recordingLogs{}
	notifier := &demoNotifier{
		dispatcher: dispatcher,
		prefs: &stubReviewPrefs{
			effective: []notification.Channel{notification.ChannelInApp, notification.ChannelEmail},
		},
		accounts: stubReviewAccounts{identifiers: []authn.Identifier{
			{Kind: authn.IdentifierEmail, Value: reviewSMSTestEmail, VerifiedVia: authn.VerifiedViaEmailCode},
		}},
		templates: &stubReviewSMSTemplates{},
		profiles: []i18n.Profile{
			failingReviewProfile{},
			stubReviewProfile{locale: reviewChineseLocale},
		},
		resolver: stubReviewResolver{locale: reviewThirdLocale},
		logger:   slog.New(logs),
	}
	ctx := i18n.WithLocale(context.Background(), reviewThirdLocale)

	if _, ok, err := notifier.notify(ctx, "acme", "acct-two-profiles", "occurrence-two-profiles"); err != nil || !ok {
		t.Fatalf("notify: ok=%t err=%v", ok, err)
	}
	if dispatcher.dispatch == nil {
		t.Fatalf("notify submitted no dispatch")
	}
	if got := dispatcher.dispatch.Locale; got != reviewChineseLocale {
		t.Errorf("the dispatch's locale is %q, want the second profile's stored %q: "+
			"one profile's failed read says nothing about the next", got, reviewChineseLocale)
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

// TestReviewNoticePrunesOnlyUnaddressableChannels holds the write the fixture
// makes before a dispatch: it removes a channel the recipient cannot be
// addressed on and never adds one back, so a recipient's stored choice survives
// every window.
func TestReviewNoticePrunesOnlyUnaddressableChannels(t *testing.T) {
	t.Parallel()
	noPhone := demoNoticePlan{
		To:       reviewSMSTestEmail,
		Channels: []notification.Channel{notification.ChannelInApp, notification.ChannelEmail},
	}
	withPhone := demoNoticePlan{
		To:       reviewSMSTestPhone,
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

// containsString reports whether values holds want.
func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// sameChannels reports whether two channel sets are equal element by element.
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
