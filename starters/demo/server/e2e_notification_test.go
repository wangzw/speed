package main

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

const (
	e2eNotifyUsername = "notice-member"
	e2eNotifyPassword = "notice-member-password-1"
	e2eNotifyEmail    = "notice-member@example.test"

	e2eNotifyChineseUsername = "notice-member-zh"
	e2eNotifyChinesePassword = "notice-member-zh-password-2"
	e2eNotifyChineseEmail    = "notice-member-zh@example.test"
)

const (
	e2eChineseLocale = "zh-CN"
	e2eDefaultLocale = "en-US"
	e2eThirdLocale   = "fr-FR"
)

type notificationTypeView struct {
	Key                    string   `json:"key"`
	Group                  string   `json:"group"`
	DefaultChannels        []string `json:"default_channels"`
	Unsubscribable         bool     `json:"unsubscribable"`
	RecipientVisibleParams []string `json:"recipient_visible_params"`
}

type preferenceView struct {
	TypeKey  string   `json:"type_key"`
	Channels []string `json:"channels"`
	Explicit bool     `json:"explicit"`
}

type messageView struct {
	ID      string `json:"id"`
	TypeKey string `json:"type_key"`
	Group   string `json:"group"`
	Title   string `json:"title"`
	Body    string `json:"body"`
}

type messagePage struct {
	Messages []messageView `json:"messages"`
	Total    int           `json:"total"`
}

// TestE2ENotificationTypeReachesBothTaxonomyConsumers holds that the type is in the available set both
// taxonomy consumers collect and its schedule names the registered handler.
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

// TestE2ENotificationJourneyIsReachable drives the fixture end to end: the
// worker writes the notice and turning the channel off stops later windows.
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

	token := registerE2EMember(t, client, base, serve,
		e2eNotifyUsername, e2eNotifyEmail, e2eNotifyPassword)
	headers := map[string]string{
		"Authorization": "Bearer " + token,
		"X-Tenant":      personalTenantName(t, client, base, token),
	}

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

	var prefs struct {
		Preferences []preferenceView `json:"preferences"`
	}
	readJSON(t, client, base+"/api/v1/notifications/preferences", headers, &prefs)
	row := preferenceRow(t, prefs.Preferences)
	if want := []string{"in_app", "email"}; !slices.Equal(row.Channels, want) {
		t.Errorf("the member's effective channels are %+v, want the email fallback %v with sms "+
			"removed; the recipient has no proved phone number", row, want)
	}

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

	anonymous := send(t, client, http.MethodPut,
		base+"/api/v1/notifications/preferences/"+demoNotificationTypeKey+"/email",
		map[string]any{"enabled": true}, map[string]string{"X-Tenant": headers["X-Tenant"]})
	if anonymous.status != http.StatusUnauthorized {
		t.Fatalf("an anonymous preference write answered %d, want 401:\n%s",
			anonymous.status, anonymous.body)
	}

	// The operator's personal tenant also receives notices, so every window carries
	// one more email addressed to the operator; count only the member's lines.
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

// TestE2ENotificationRendersInTheRecipientsLanguage holds that a member whose account stores zh-CN
// receives the notice in the inbox and the email in that language.
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

// A 429 is waited out rather than reported, because the fixture's own poll must
// not spend the budget the journey's windows use.
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

// waitForInbox polls until one answer holds want messages or the bound expires;
// a 429 is waited out with a longer pause but does not step around the bound.
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

// TestWaitForInbox429HoldsTheBound pins that a server answering 429 to every read ends the wait at the
// bound instead of looping forever.
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
