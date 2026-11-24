package main

// The notification fixture, tested where it is compiled: the type is in the
// available set both taxonomy consumers read, a dispatch that the worker submits
// writes an inbox row for the member and renders the email from the module's own
// catalog in the recipient's language — Chinese for a member who stored zh-CN,
// the platform default for a member who stored none — the preference write
// succeeds because the fixture names the recipient, and an anonymous caller is
// still refused.
//
// The dispatch is the worker's: the module declares a jobs.Schedule and the
// handler turns each window into one dispatch per active tenant member. Nothing
// here calls an HTTP endpoint to make a notification happen; the browser tier
// would only observe what this worker produces.

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/core/coretest"
	"github.com/vislake/speed/pkg/jobs"
	"github.com/vislake/speed/pkg/notification"
	"github.com/vislake/speed/pkg/tenant"
)

// The accounts the notification cases register. They are not seed rows, and
// each case's database is a file of its own, so the names collide with nothing.
const (
	e2eNotifyUsername = "notice-member"
	e2eNotifyPassword = "notice-member-password-1"
	e2eNotifyEmail    = "notice-member@example.test"

	e2eNotifyChineseUsername = "notice-member-zh"
	e2eNotifyChinesePassword = "notice-member-zh-password-2"
	e2eNotifyChineseEmail    = "notice-member-zh@example.test"
)

// The languages the notification cases exercise: the one a member stores on the
// account, and the platform default a member with no stored preference resolves
// to.
const (
	e2eChineseLocale = "zh-CN"
	e2eDefaultLocale = "en-US"
	// e2eThirdLocale is the run's default in the cases that hold the
	// fallback itself. It is neither the language a member stores nor the one a
	// request carries, so a hard-coded "en-US" fails those cases.
	e2eThirdLocale = "fr-FR"
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

// TestE2ENotificationTypeReachesBothTaxonomyConsumers holds the declaration
// side: the demo notification module is named for the serve roster, its type is in the
// available set notification.preferences and notification.delivery collect with
// core.Resources, and its schedule names the handler this module registers.
func TestE2ENotificationTypeReachesBothTaxonomyConsumers(t *testing.T) {
	t.Parallel()
	if !slices.Contains(capabilityModules, demoNotificationModuleName) {
		t.Fatalf("the capability ceiling names no %q module, so the binary would run neither "+
			"the type nor the worker", demoNotificationModuleName)
	}

	var declared *notification.TypeSpec
	for _, resource := range core.Resources[notification.TypeSpec](core.ProcessRegistry) {
		if resource.Value.Key != demoNotificationTypeKey {
			continue
		}
		if resource.Module != demoNotificationModuleName {
			t.Fatalf("the type %q is declared by %q, want %q", demoNotificationTypeKey,
				resource.Module, demoNotificationModuleName)
		}
		spec := resource.Value
		declared = &spec
	}
	if declared == nil {
		t.Fatalf("the available set declares no %q type; both taxonomy consumers read "+
			"this query, so the type would be absent from the matrix and refused at dispatch",
			demoNotificationTypeKey)
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
		if resource.Module == demoNotificationModuleName {
			s := resource.Value
			schedule = &s
		}
	}
	if schedule == nil {
		t.Fatalf("the module declares no jobs.Schedule, so no worker window ever dispatches the type")
	}
	if schedule.Type != demoNotificationJobType {
		t.Errorf("the schedule enqueues %q, want the handler's type %q", schedule.Type, demoNotificationJobType)
	}
	if schedule.Interval <= 0 {
		t.Errorf("the schedule's interval is %s, want a positive window", schedule.Interval)
	}
}

// TestE2ENotificationJourneyIsReachable drives the fixture end to end over
// the real binary: a registered member gets a tenant, the worker writes
// the type into the member's inbox, the email is rendered from the type's
// catalog, the member turns the email channel off and the write succeeds, an
// anonymous caller is still refused, and the next windows write no email.
func TestE2ENotificationJourneyIsReachable(t *testing.T) {
	t.Parallel()
	binary := e2eBinary(t)
	serve, ports := startServing(t, func(ports []int) *process {
		config := "file://" + e2eConfig(t,
			fmt.Sprintf("127.0.0.1:%d", ports[backendListener]))
		seedE2E(t, binary, config)
		return startE2E(t, binary, config)
	})

	base := fmt.Sprintf("http://127.0.0.1:%d", ports[backendListener])
	client := &http.Client{Timeout: probeTimeout()}

	// The member registers, signs in, proves an email address and ensures a
	// tenant. The account needs an email identifier for the email channel's
	// destination, and registration is the UI's own path to one.
	token := registerE2EMember(t, client, base, serve,
		e2eNotifyUsername, e2eNotifyEmail, e2eNotifyPassword)
	headers := map[string]string{
		"Authorization": "Bearer " + token,
		"X-Tenant":      personalTenantName(t, client, base, token),
	}

	// The preferences consumer sees the type with its declared defaults.
	var types struct {
		Types []notificationTypeView `json:"types"`
	}
	readJSON(t, client, base+"/api/v1/notifications/types", headers, &types)
	var typeView *notificationTypeView
	for i := range types.Types {
		if types.Types[i].Key == demoNotificationTypeKey {
			typeView = &types.Types[i]
		}
	}
	if typeView == nil {
		t.Fatalf("GET /notifications/types lists no %q:\n%+v", demoNotificationTypeKey, types)
	}
	if want := []string{"in_app", "email", "sms"}; !slices.Equal(typeView.DefaultChannels, want) {
		t.Errorf("the type's listed default channels are %v, want %v", typeView.DefaultChannels, want)
	}

	// The worker dispatches once per window. Wait for the first message; the
	// delivery consumer accepting the type is what proves it collected it.
	page := waitForMessages(t, client, base+"/api/v1/notifications/messages?group="+demoNotificationGroup,
		headers, 1, serve)
	first := page.Messages[0]
	if first.TypeKey != demoNotificationTypeKey {
		t.Fatalf("the first message is %q, want %q:\n%+v", first.TypeKey, demoNotificationTypeKey, first)
	}
	if want := e2eCatalogEntry(t, e2eDefaultLocale, "demo.notice.in_app.title"); first.Title != want {
		t.Errorf("the in-app title is %q, want the catalog's %q", first.Title, want)
	}
	if want := e2eCatalogEntry(t, e2eDefaultLocale, "demo.notice.in_app.body"); first.Body != want {
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
	subject := e2eCatalogEntry(t, e2eDefaultLocale, "demo.notice.email.subject")
	body := e2eCatalogEntry(t, e2eDefaultLocale, "demo.notice.email.body_text")
	waitForLogLine(t, serve, subject)
	if strings.Contains(serve.output(), demoNotificationTypeKey+".email.subject") {
		t.Fatalf("the log mail line carries the raw catalog key instead of the rendered subject:\n%s",
			serve.output())
	}
	if !strings.Contains(serve.output(), body) {
		t.Errorf("the log mail line carries no rendered body %q", body)
	}

	// The preference write succeeds: the assembly names the caller as the
	// recipient on the request context. Without that wiring the capability
	// refuses a write with no recipient as a 400.
	written := send(t, client, http.MethodPut,
		base+"/api/v1/notifications/preferences/"+demoNotificationTypeKey+"/email",
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
	if applied.TypeKey != demoNotificationTypeKey || !slices.Equal(applied.Channels, []string{"in_app"}) {
		t.Errorf("the preference write answered %+v, want %q with in_app alone",
			applied, demoNotificationTypeKey)
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
		base+"/api/v1/notifications/preferences/"+demoNotificationTypeKey+"/email",
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
	// tenant is a real organization that receives notices too. Every window
	// therefore carries one more email, addressed to the operator rather than
	// to the member this case registered. Count only the lines addressed to the
	// member, which is the delivery the preference governs.
	emailLines := func() int {
		count := 0
		for _, line := range strings.Split(serve.output(), "\n") {
			if strings.Contains(line, subject) && strings.Contains(line, e2eNotifyEmail) {
				count++
			}
		}
		return count
	}
	waitForMessages(t, client, base+"/api/v1/notifications/messages?group="+demoNotificationGroup,
		headers, 2, serve)
	afterFirst := emailLines()
	waitForMessages(t, client, base+"/api/v1/notifications/messages?group="+demoNotificationGroup,
		headers, 3, serve)
	if afterSecond := emailLines(); afterSecond != afterFirst {
		t.Errorf("the inbox grew after the email channel was turned off, but the notice's email "+
			"was sent %d more time(s); the stored preference did not reach delivery", afterSecond-afterFirst)
	}
}

// TestE2ENotificationRendersInTheRecipientsLanguage holds the dispatch
// language: a member whose account stores zh-CN receives the notice rendered
// from the zh-CN catalog, in the inbox and in the email alike. The journey case
// above holds the other direction — the same fixture with a member who stores no
// preference, which resolves to the platform default.
func TestE2ENotificationRendersInTheRecipientsLanguage(t *testing.T) {
	t.Parallel()
	binary := e2eBinary(t)
	serve, ports := startServing(t, func(ports []int) *process {
		config := "file://" + e2eConfig(t,
			fmt.Sprintf("127.0.0.1:%d", ports[backendListener]))
		seedE2E(t, binary, config)
		return startE2E(t, binary, config)
	})

	base := fmt.Sprintf("http://127.0.0.1:%d", ports[backendListener])
	client := &http.Client{Timeout: probeTimeout()}

	token := registerE2EMember(t, client, base, serve,
		e2eNotifyChineseUsername, e2eNotifyChineseEmail, e2eNotifyChinesePassword)

	// The preference is stored before the tenant exists, so every dispatch the
	// worker makes for it already resolves the recipient's language. The write
	// goes through the profile route the interface's own personalization uses.
	stored := send(t, client, http.MethodPatch, base+"/api/v1/auth/me",
		map[string]string{"locale": e2eChineseLocale},
		map[string]string{"Authorization": "Bearer " + token})
	if stored.status != http.StatusOK {
		t.Fatalf("storing the member's %s language preference answered %d:\n%s",
			e2eChineseLocale, stored.status, stored.body)
	}

	headers := map[string]string{
		"Authorization": "Bearer " + token,
		"X-Tenant":      personalTenantName(t, client, base, token),
	}

	// The worker's first window for this tenant renders the notice — and the
	// email the same dispatch carries — in the recipient's language.
	page := waitForMessages(t, client, base+"/api/v1/notifications/messages?group="+demoNotificationGroup,
		headers, 1, serve)
	first := page.Messages[0]
	if first.TypeKey != demoNotificationTypeKey {
		t.Fatalf("the first message is %q, want %q:\n%+v", first.TypeKey, demoNotificationTypeKey, first)
	}
	if want := e2eCatalogEntry(t, e2eChineseLocale, "demo.notice.in_app.title"); first.Title != want {
		t.Errorf("the %s recipient's in-app title is %q, want the %s catalog's %q",
			e2eChineseLocale, first.Title, e2eChineseLocale, want)
	}
	if want := e2eCatalogEntry(t, e2eChineseLocale, "demo.notice.in_app.body"); first.Body != want {
		t.Errorf("the %s recipient's in-app body is %q, want the %s catalog's %q",
			e2eChineseLocale, first.Body, e2eChineseLocale, want)
	}

	subject := e2eCatalogEntry(t, e2eChineseLocale, "demo.notice.email.subject")
	emailBody := e2eCatalogEntry(t, e2eChineseLocale, "demo.notice.email.body_text")
	waitForLogLine(t, serve, subject)
	if !strings.Contains(serve.output(), emailBody) {
		t.Errorf("the log mail line carries no %s body %q", e2eChineseLocale, emailBody)
	}
}

// registerE2EMember registers a member over the binary's own routes
// and answers its signed-in token. Registration leaves the address pending
// rather than binding it as an identifier, and the self-service add writes one
// only once the code is presented; the log mail carrier writes that code to
// the process log, so the member proves the address the way the walkthrough
// does.
func registerE2EMember(t *testing.T, client *http.Client, base string, serve *process,
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
	code := e2eMailCode(t, serve)
	verified := send(t, client, http.MethodPost, base+"/api/v1/auth/identifiers/verify",
		map[string]string{"code": code}, auth)
	if verified.status != http.StatusOK {
		t.Fatalf("verifying the member's address answered %d:\n%s", verified.status, verified.body)
	}
	return token
}

// personalTenantName reads the caller's own personal tenant from the account's
// tenant list and answers its name, which the tenant-scoped routes read from
// the X-Tenant header. The account's creation transaction wrote the tenant; the
// host has no backfill route.
func personalTenantName(t *testing.T, client *http.Client, base, token string) string {
	t.Helper()
	read := send(t, client, http.MethodGet, base+"/api/v1/auth/me/tenants", nil,
		map[string]string{"Authorization": "Bearer " + token})
	if read.status != http.StatusOK {
		t.Fatalf("GET the caller's tenants answered %d:\n%s", read.status, read.body)
	}
	var answer struct {
		Tenants []struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
		} `json:"tenants"`
	}
	if err := json.Unmarshal(read.body, &answer); err != nil {
		t.Fatalf("the tenant list is not JSON: %v\n%s", err, read.body)
	}
	for _, entry := range answer.Tenants {
		if entry.Kind == string(tenant.KindPersonal) && entry.Name != "" {
			return entry.Name
		}
	}
	t.Fatalf("the caller's tenant list carries no personal tenant:\n%s", read.body)
	return ""
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

// preferenceRow finds the demo notification type's row in the matrix.
func preferenceRow(t *testing.T, rows []preferenceView) preferenceView {
	t.Helper()
	for _, row := range rows {
		if row.TypeKey == demoNotificationTypeKey {
			return row
		}
	}
	t.Fatalf("the preference matrix has no %q row: %+v", demoNotificationTypeKey, rows)
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
	page, err := waitForInbox(want, coretest.Scale(60*time.Second),
		func() (response, error) {
			return send(t, client, http.MethodGet, url, nil, headers), nil
		}, serve.output)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return page
}

// waitForInbox polls until one answer holds want messages or the bound expires.
// A 429 is waited out with a longer pause because the api rate limiter is
// spending the fixture's budget, but it does not step around the bound: a
// server that keeps answering 429 would otherwise never end the wait. The
// timeout names the last answer's status and body, so a wait that spent its
// bound on 429 is distinguishable from a worker that never dispatched.
func waitForInbox(want int, bound time.Duration, poll func() (response, error),
	output func() string,
) (messagePage, error) {
	deadline := time.Now().Add(bound)
	var last response
	for {
		res, err := poll()
		if err != nil {
			return messagePage{}, fmt.Errorf("GET the inbox: %w", err)
		}
		last = res
		pause := time.Second
		switch res.status {
		case http.StatusOK:
			var page messagePage
			if err := json.Unmarshal(res.body, &page); err != nil {
				return messagePage{}, fmt.Errorf("the inbox answer is not JSON: %w\n%s", err, res.body)
			}
			if page.Total >= want {
				return page, nil
			}
		case http.StatusTooManyRequests:
			pause = 2 * time.Second
		default:
			return messagePage{}, fmt.Errorf("the inbox answered %d:\n%s", res.status, res.body)
		}
		if time.Now().After(deadline) {
			return messagePage{}, fmt.Errorf("the inbox held fewer than %d message(s) within the "+
				"bound; the last answer was %d:\n%s\nthe process output:\n%s",
				want, last.status, last.body, output())
		}
		time.Sleep(pause)
	}
}

// TestWaitForInbox429HoldsTheBound pins the rate-limit branch to the deadline: a
// server that answers 429 to every read must end the wait at the bound instead
// of looping forever. The wait runs in a goroutine because the bug being pinned
// is one that never returns; the outer select fails the case rather than letting
// an unfixed implementation hang the suite.
func TestWaitForInbox429HoldsTheBound(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"code":"rate_limited","title":"slow down"}`))
	}))
	defer server.Close()

	client := server.Client()
	bound := coretest.Scale(200 * time.Millisecond)
	guard := bound + coretest.Scale(3*time.Second)
	poll := func() (response, error) {
		res, err := client.Get(server.URL)
		if err != nil {
			return response{}, err
		}
		defer func() { _ = res.Body.Close() }()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			return response{}, err
		}
		return response{status: res.StatusCode, body: body}, nil
	}
	done := make(chan error, 1)
	go func() {
		_, err := waitForInbox(1, bound, poll, func() string { return "the test server read no worker output" })
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("the wait answered no error although every read was %d", http.StatusTooManyRequests)
		}
		for _, want := range []string{fmt.Sprint(http.StatusTooManyRequests), "slow down"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("the timeout error does not name %q; got:\n%s", want, err)
			}
		}
	case <-time.After(guard):
		t.Fatalf("the wait did not return within %s although its bound was %s; a 429 branch that "+
			"skips the deadline check never ends against a server that keeps answering %d",
			guard, bound, http.StatusTooManyRequests)
	}
}

// waitForLogLine polls the child's output for a line carrying needle.
func waitForLogLine(t *testing.T, serve *process, needle string) {
	t.Helper()
	deadline := time.Now().Add(coretest.Scale(20 * time.Second))
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

// e2eCatalogEntry reads one message from the fixture's catalog for a
// language, so the cases expect the shipped text rather than a hardcoded copy.
func e2eCatalogEntry(t *testing.T, locale, key string) string {
	t.Helper()
	raw, err := fs.ReadFile(demoNotificationLocales, "demo_notification_locales/"+locale+".json")
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
