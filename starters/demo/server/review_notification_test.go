//go:build review

package main

// The review assembly's notification fixture, tested where it is compiled: the
// type is in the available set both taxonomy consumers read, a dispatch that
// the worker submits writes an inbox row for the member and renders the email
// from the module's own catalog in the recipient's language — Chinese for a
// member who stored zh-CN, the platform default for a member who stored none —
// the preference write succeeds because the review build names the recipient,
// and an anonymous caller is still refused.
//
// The dispatch is the worker's: the module declares a jobs.Schedule and the
// handler turns each window into one dispatch per active tenant member. Nothing
// here calls an HTTP endpoint to make a notification happen; the browser tier
// would only observe what this worker produces.

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/jobs"
	"github.com/vislake/speed/pkg/notification"
)

// The accounts the notification cases register. They are not seed rows, and
// each case's database is a file of its own, so the names collide with nothing.
const (
	reviewNotifyUsername = "notice-member"
	reviewNotifyPassword = "review-notify-password-1"
	reviewNotifyEmail    = "notice-member@example.test"

	reviewNotifyChineseUsername = "notice-member-zh"
	reviewNotifyChinesePassword = "review-notify-password-2"
	reviewNotifyChineseEmail    = "notice-member-zh@example.test"
)

// The languages the notification cases exercise: the one a member stores on the
// account, and the platform default a member with no stored preference resolves
// to.
const (
	reviewChineseLocale = "zh-CN"
	reviewDefaultLocale = "en-US"
)

// notificationTypeView is one entry of GET /notifications/types.
type notificationTypeView struct {
	Key                    string   `json:"key"`
	Group                  string   `json:"group"`
	DefaultChannels        []string `json:"default_channels"`
	Unsubscribable         bool     `json:"unsubscribable"`
	RecipientVisibleParams []string `json:"recipient_visible_params"`
}

// preferenceView is one row of GET /notifications/preferences.
type preferenceView struct {
	TypeKey  string   `json:"type_key"`
	Channels []string `json:"channels"`
	Explicit bool     `json:"explicit"`
}

// messageView is one message of GET /notifications/messages.
type messageView struct {
	ID      string `json:"id"`
	TypeKey string `json:"type_key"`
	Group   string `json:"group"`
	Title   string `json:"title"`
	Body    string `json:"body"`
}

// messagePage is the listing's answer.
type messagePage struct {
	Messages []messageView `json:"messages"`
	Total    int           `json:"total"`
}

// TestReviewNotificationTypeReachesBothTaxonomyConsumers holds the declaration
// side: the review module is named for the serve roster, its type is in the
// available set notification.preferences and notification.delivery collect with
// core.Resources, and its schedule names the handler this module registers.
func TestReviewNotificationTypeReachesBothTaxonomyConsumers(t *testing.T) {
	if !slices.Contains(capabilityModules, reviewNotificationModuleName) {
		t.Fatalf("the capability ceiling names no %q module, so the review binary would run neither "+
			"the type nor the worker", reviewNotificationModuleName)
	}

	var declared *notification.TypeSpec
	for _, resource := range core.Resources[notification.TypeSpec](core.ProcessRegistry) {
		if resource.Value.Key != reviewNotificationTypeKey {
			continue
		}
		if resource.Module != reviewNotificationModuleName {
			t.Fatalf("the type %q is declared by %q, want %q", reviewNotificationTypeKey,
				resource.Module, reviewNotificationModuleName)
		}
		spec := resource.Value
		declared = &spec
	}
	if declared == nil {
		t.Fatalf("the available set declares no %q type; both taxonomy consumers read "+
			"this query, so the type would be absent from the matrix and refused at dispatch",
			reviewNotificationTypeKey)
	}
	if !declared.Unsubscribable {
		t.Errorf("the type is not unsubscribable, so the journey's preference write would be refused")
	}
	if want := []notification.Channel{
		notification.ChannelInApp,
		notification.ChannelEmail,
		notification.ChannelSMS,
	}; !slices.Equal(declared.DefaultChannels, want) {
		t.Errorf("the type's default channels are %v, want %v", declared.DefaultChannels, want)
	}
	if !slices.Contains(declared.RecipientVisibleParams, "to") {
		t.Errorf("the type's parameter whitelist is %v; it must admit the destination the "+
			"external channels read, or every external dispatch is refused as a disallowed "+
			"parameter", declared.RecipientVisibleParams)
	}

	var schedule *jobs.Schedule
	for _, resource := range core.Resources[jobs.Schedule](core.ProcessRegistry) {
		if resource.Module == reviewNotificationModuleName {
			s := resource.Value
			schedule = &s
		}
	}
	if schedule == nil {
		t.Fatalf("the module declares no jobs.Schedule, so no worker window ever dispatches the type")
	}
	if schedule.Type != reviewNotificationJobType {
		t.Errorf("the schedule enqueues %q, want the handler's type %q", schedule.Type, reviewNotificationJobType)
	}
	if schedule.Interval <= 0 {
		t.Errorf("the schedule's interval is %s, want a positive window", schedule.Interval)
	}
}

// TestReviewNotificationJourneyIsReachable drives the fixture end to end over
// the real review binary: a registered member gets a tenant, the worker writes
// the type into the member's inbox, the email is rendered from the type's
// catalog, the member turns the email channel off and the write succeeds, an
// anonymous caller is still refused, and the next windows write no email.
func TestReviewNotificationJourneyIsReachable(t *testing.T) {
	binary := reviewBinary(t)
	ports := freePorts(t, listenerCount)
	path := reviewConfig(t,
		fmt.Sprintf("127.0.0.1:%d", ports[backendListener]),
		fmt.Sprintf("127.0.0.1:%d", ports[opsListener]))
	config := "file://" + path
	seedReview(t, binary, config)
	serve := startReview(t, binary, config)
	serve.waitReady(t)

	base := fmt.Sprintf("http://127.0.0.1:%d", ports[backendListener])
	client := &http.Client{Timeout: 10 * time.Second}

	// The member registers, signs in, proves an email address and ensures a
	// tenant. The account needs an email identifier for the email channel's
	// destination, and registration is the UI's own path to one.
	token := registerReviewMember(t, client, base, serve,
		reviewNotifyUsername, reviewNotifyEmail, reviewNotifyPassword)
	headers := map[string]string{
		"Authorization": "Bearer " + token,
		"X-Tenant":      ensureReviewTenant(t, client, base, token),
	}

	// The review-only tenant control is offered by the feature document: the
	// module that mounts its route declares the feature statically, so a review
	// run publishes it and a baseline run, which compiles no such module, does
	// not.
	var features struct {
		Features []string `json:"features"`
	}
	readJSON(t, client, base+"/api/v1/features", headers, &features)
	if !slices.Contains(features.Features, reviewNotificationControlFeature) {
		t.Errorf("the review feature document omits %q, so the tenant's notice control would "+
			"never be offered", reviewNotificationControlFeature)
	}

	// The preferences consumer sees the type with its declared defaults.
	var types struct {
		Types []notificationTypeView `json:"types"`
	}
	readJSON(t, client, base+"/api/v1/notifications/types", headers, &types)
	var typeView *notificationTypeView
	for i := range types.Types {
		if types.Types[i].Key == reviewNotificationTypeKey {
			typeView = &types.Types[i]
		}
	}
	if typeView == nil {
		t.Fatalf("GET /notifications/types lists no %q:\n%+v", reviewNotificationTypeKey, types)
	}
	if want := []string{"in_app", "email", "sms"}; !slices.Equal(typeView.DefaultChannels, want) {
		t.Errorf("the type's listed default channels are %v, want %v", typeView.DefaultChannels, want)
	}

	// The worker dispatches once per window. Wait for the first message; the
	// delivery consumer accepting the type is what proves it collected it.
	page := waitForMessages(t, client, base+"/api/v1/notifications/messages?group="+reviewNotificationGroup,
		headers, 1, serve)
	first := page.Messages[0]
	if first.TypeKey != reviewNotificationTypeKey {
		t.Fatalf("the first message is %q, want %q:\n%+v", first.TypeKey, reviewNotificationTypeKey, first)
	}
	if want := reviewCatalogEntry(t, reviewDefaultLocale, "review.notice.in_app.title"); first.Title != want {
		t.Errorf("the in-app title is %q, want the catalog's %q", first.Title, want)
	}
	if want := reviewCatalogEntry(t, reviewDefaultLocale, "review.notice.in_app.body"); first.Body != want {
		t.Errorf("the in-app body is %q, want the catalog's %q", first.Body, want)
	}

	// This member holds an email address but no phone number, so the fixture
	// addresses them by email: sms is removed from their effective set and is
	// not attempted with the address as its destination. The worker writes that
	// set before it submits the dispatch, so the first message above is the
	// signal that the write has landed.
	var prefs struct {
		Preferences []preferenceView `json:"preferences"`
	}
	readJSON(t, client, base+"/api/v1/notifications/preferences", headers, &prefs)
	row := preferenceRow(t, prefs.Preferences)
	if want := []string{"in_app", "email"}; !slices.Equal(row.Channels, want) {
		t.Errorf("the member's effective channels are %+v, want the email fallback %v with sms "+
			"removed; the recipient has no proved phone number", row, want)
	}

	// The email channel renders the same message from the catalog. The review
	// mail carrier writes the rendered subject and body to the process log; a
	// fallback to the raw key would leave the key in the line instead.
	subject := reviewCatalogEntry(t, reviewDefaultLocale, "review.notice.email.subject")
	body := reviewCatalogEntry(t, reviewDefaultLocale, "review.notice.email.body_text")
	waitForLogLine(t, serve, subject)
	if strings.Contains(serve.output(), reviewNotificationTypeKey+".email.subject") {
		t.Fatalf("the review mail line carries the raw catalog key instead of the rendered subject:\n%s",
			serve.output())
	}
	if !strings.Contains(serve.output(), body) {
		t.Errorf("the review mail line carries no rendered body %q", body)
	}

	// The preference write succeeds: the review build names the caller as the
	// recipient on the request context. Without that wiring the capability
	// refuses a write with no recipient as a 400.
	written := send(t, client, http.MethodPut,
		base+"/api/v1/notifications/preferences/"+reviewNotificationTypeKey+"/email",
		map[string]any{"enabled": false}, headers)
	if written.status != http.StatusOK {
		t.Fatalf("turning the email channel off answered %d:\n%s", written.status, written.body)
	}
	var applied struct {
		TypeKey  string   `json:"type_key"`
		Channels []string `json:"channels"`
	}
	if err := json.Unmarshal(written.body, &applied); err != nil {
		t.Fatalf("the preference write's answer is not JSON: %v\n%s", err, written.body)
	}
	if applied.TypeKey != reviewNotificationTypeKey || !slices.Equal(applied.Channels, []string{"in_app"}) {
		t.Errorf("the preference write answered %+v, want %q with in_app alone",
			applied, reviewNotificationTypeKey)
	}
	readJSON(t, client, base+"/api/v1/notifications/preferences", headers, &prefs)
	row = preferenceRow(t, prefs.Preferences)
	if !slices.Equal(row.Channels, []string{"in_app"}) || !row.Explicit {
		t.Errorf("the stored preference row is %+v, want in_app alone and explicit true", row)
	}

	// A caller with no account is still refused. The notification routes are
	// open on the authorization guard, not anonymous: the gateway answers a
	// request carrying no credential before the preference handler runs.
	anonymous := send(t, client, http.MethodPut,
		base+"/api/v1/notifications/preferences/"+reviewNotificationTypeKey+"/email",
		map[string]any{"enabled": true}, map[string]string{"X-Tenant": headers["X-Tenant"]})
	if anonymous.status != http.StatusUnauthorized {
		t.Fatalf("an anonymous preference write answered %d, want 401:\n%s",
			anonymous.status, anonymous.body)
	}

	// Two windows after the write send no email. The first is waited out so an
	// in-flight window enqueued before the write cannot be mistaken for a
	// post-write one; the count is taken again at the second.
	//
	// The operator owns a personal tenant of its own by now, and a personal
	// tenant is a real workspace that receives notices too. Every window
	// therefore carries one more email, addressed to the operator rather than
	// to the member this case registered. Count only the lines addressed to the
	// member, which is the delivery the preference governs.
	emailLines := func() int {
		count := 0
		for _, line := range strings.Split(serve.output(), "\n") {
			if strings.Contains(line, subject) && strings.Contains(line, reviewNotifyEmail) {
				count++
			}
		}
		return count
	}
	waitForMessages(t, client, base+"/api/v1/notifications/messages?group="+reviewNotificationGroup,
		headers, 2, serve)
	afterFirst := emailLines()
	waitForMessages(t, client, base+"/api/v1/notifications/messages?group="+reviewNotificationGroup,
		headers, 3, serve)
	if afterSecond := emailLines(); afterSecond != afterFirst {
		t.Errorf("the inbox grew after the email channel was turned off, but the notice's email "+
			"was sent %d more time(s); the stored preference did not reach delivery", afterSecond-afterFirst)
	}
}

// TestReviewNotificationRendersInTheRecipientsLanguage holds the dispatch
// language: a member whose account stores zh-CN receives the notice rendered
// from the zh-CN catalog, in the inbox and in the email alike. The journey case
// above holds the other direction — the same fixture with a member who stores no
// preference, which resolves to the platform default.
func TestReviewNotificationRendersInTheRecipientsLanguage(t *testing.T) {
	binary := reviewBinary(t)
	ports := freePorts(t, listenerCount)
	path := reviewConfig(t,
		fmt.Sprintf("127.0.0.1:%d", ports[backendListener]),
		fmt.Sprintf("127.0.0.1:%d", ports[opsListener]))
	config := "file://" + path
	seedReview(t, binary, config)
	serve := startReview(t, binary, config)
	serve.waitReady(t)

	base := fmt.Sprintf("http://127.0.0.1:%d", ports[backendListener])
	client := &http.Client{Timeout: 10 * time.Second}

	token := registerReviewMember(t, client, base, serve,
		reviewNotifyChineseUsername, reviewNotifyChineseEmail, reviewNotifyChinesePassword)

	// The preference is stored before the tenant exists, so every dispatch the
	// worker makes for it already resolves the recipient's language. The write
	// goes through the profile route the interface's own personalization uses.
	stored := send(t, client, http.MethodPatch, base+"/api/v1/auth/me",
		map[string]string{"locale": reviewChineseLocale},
		map[string]string{"Authorization": "Bearer " + token})
	if stored.status != http.StatusOK {
		t.Fatalf("storing the member's %s language preference answered %d:\n%s",
			reviewChineseLocale, stored.status, stored.body)
	}

	headers := map[string]string{
		"Authorization": "Bearer " + token,
		"X-Tenant":      ensureReviewTenant(t, client, base, token),
	}

	// The worker's first window for this tenant renders the notice — and the
	// email the same dispatch carries — in the recipient's language.
	page := waitForMessages(t, client, base+"/api/v1/notifications/messages?group="+reviewNotificationGroup,
		headers, 1, serve)
	first := page.Messages[0]
	if first.TypeKey != reviewNotificationTypeKey {
		t.Fatalf("the first message is %q, want %q:\n%+v", first.TypeKey, reviewNotificationTypeKey, first)
	}
	if want := reviewCatalogEntry(t, reviewChineseLocale, "review.notice.in_app.title"); first.Title != want {
		t.Errorf("the %s recipient's in-app title is %q, want the %s catalog's %q",
			reviewChineseLocale, first.Title, reviewChineseLocale, want)
	}
	if want := reviewCatalogEntry(t, reviewChineseLocale, "review.notice.in_app.body"); first.Body != want {
		t.Errorf("the %s recipient's in-app body is %q, want the %s catalog's %q",
			reviewChineseLocale, first.Body, reviewChineseLocale, want)
	}

	subject := reviewCatalogEntry(t, reviewChineseLocale, "review.notice.email.subject")
	emailBody := reviewCatalogEntry(t, reviewChineseLocale, "review.notice.email.body_text")
	waitForLogLine(t, serve, subject)
	if !strings.Contains(serve.output(), emailBody) {
		t.Errorf("the review mail line carries no %s body %q", reviewChineseLocale, emailBody)
	}
}

// registerReviewMember registers a member over the review binary's own routes
// and answers its signed-in token. Registration leaves the address pending
// rather than binding it as an identifier, and the self-service add writes one
// only once the code is presented; the review mail carrier writes that code to
// the process log, so the member proves the address the way the walkthrough
// does.
func registerReviewMember(t *testing.T, client *http.Client, base string, serve *process,
	username, email, password string,
) string {
	t.Helper()
	registered := send(t, client, http.MethodPost, base+"/api/v1/auth/register", map[string]string{
		"username": username,
		"email":    email,
		"password": password,
	}, nil)
	if registered.status != http.StatusCreated {
		t.Fatalf("registering the member answered %d:\n%s", registered.status, registered.body)
	}
	token := signIn(t, base, username, password)
	auth := map[string]string{"Authorization": "Bearer " + token}
	added := send(t, client, http.MethodPost, base+"/api/v1/auth/identifiers",
		map[string]string{"kind": "email", "value": email}, auth)
	if added.status != http.StatusOK {
		t.Fatalf("adding the member's email identifier answered %d:\n%s", added.status, added.body)
	}
	code := reviewMailCode(t, serve)
	verified := send(t, client, http.MethodPost, base+"/api/v1/auth/identifiers/verify",
		map[string]string{"code": code}, auth)
	if verified.status != http.StatusOK {
		t.Fatalf("verifying the member's address answered %d:\n%s", verified.status, verified.body)
	}
	return token
}

// ensureReviewTenant ensures the caller's personal tenant and answers its
// alias, which the tenant-scoped routes read from the X-Tenant header.
func ensureReviewTenant(t *testing.T, client *http.Client, base, token string) string {
	t.Helper()
	ensured := send(t, client, http.MethodPost, base+personalRoutePath, nil,
		map[string]string{"Authorization": "Bearer " + token})
	if ensured.status != http.StatusOK {
		t.Fatalf("the personal ensure answered %d:\n%s", ensured.status, ensured.body)
	}
	var personal struct {
		Tenant struct {
			Alias string `json:"alias"`
		} `json:"tenant"`
	}
	if err := json.Unmarshal(ensured.body, &personal); err != nil || personal.Tenant.Alias == "" {
		t.Fatalf("the personal ensure named no tenant (%v):\n%s", err, ensured.body)
	}
	return personal.Tenant.Alias
}

// readJSON makes one authorized GET and decodes its answer.
func readJSON(t *testing.T, client *http.Client, url string, headers map[string]string, into any) {
	t.Helper()
	res := send(t, client, http.MethodGet, url, nil, headers)
	if res.status != http.StatusOK {
		t.Fatalf("GET %s answered %d:\n%s", url, res.status, res.body)
	}
	if err := json.Unmarshal(res.body, into); err != nil {
		t.Fatalf("GET %s is not JSON: %v\n%s", url, err, res.body)
	}
}

// preferenceRow finds the review type's row in the matrix.
func preferenceRow(t *testing.T, rows []preferenceView) preferenceView {
	t.Helper()
	for _, row := range rows {
		if row.TypeKey == reviewNotificationTypeKey {
			return row
		}
	}
	t.Fatalf("the preference matrix has no %q row: %+v", reviewNotificationTypeKey, rows)
	return preferenceView{}
}

// waitForMessages polls the inbox until it holds at least want messages. The
// api endpoint carries the rate limiter, so the poll is spaced and a 429 is
// waited out rather than reported: the fixture's own read must not spend the
// budget the journey's windows do not.
func waitForMessages(t *testing.T, client *http.Client, url string, headers map[string]string,
	want int, serve *process,
) messagePage {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		res := send(t, client, http.MethodGet, url, nil, headers)
		switch res.status {
		case http.StatusOK:
			var page messagePage
			if err := json.Unmarshal(res.body, &page); err != nil {
				t.Fatalf("GET %s is not JSON: %v\n%s", url, err, res.body)
			}
			if page.Total >= want {
				return page
			}
		case http.StatusTooManyRequests:
			time.Sleep(2 * time.Second)
			continue
		default:
			t.Fatalf("GET %s answered %d:\n%s", url, res.status, res.body)
		}
		if time.Now().After(deadline) {
			t.Fatalf("the inbox held fewer than %d message(s) within the bound; the worker's "+
				"window never dispatched:\n%s", want, serve.output())
		}
		time.Sleep(time.Second)
	}
}

// waitForLogLine polls the child's output for a line carrying needle.
func waitForLogLine(t *testing.T, serve *process, needle string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		if strings.Contains(serve.output(), needle) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the process log never carried %q:\n%s", needle, serve.output())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// reviewCatalogEntry reads one message from the fixture's catalog for a
// language, so the cases expect the shipped text rather than a hardcoded copy.
func reviewCatalogEntry(t *testing.T, locale, key string) string {
	t.Helper()
	raw, err := fs.ReadFile(reviewNotificationLocales, "review_notification_locales/"+locale+".json")
	if err != nil {
		t.Fatalf("reading the fixture catalog: %v", err)
	}
	var entries map[string]string
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("the fixture catalog is not JSON: %v", err)
	}
	value, ok := entries[key]
	if !ok || value == "" {
		t.Fatalf("the fixture catalog carries no %q", key)
	}
	return value
}
