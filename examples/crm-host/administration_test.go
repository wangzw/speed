package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	nethttp "net/http"
	"net/url"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
	"gorm.io/gorm/utils/tests"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/tenant"
)

// The cases of the settings and audit administration slice: a tenant's own
// settings, its audit trail, an operator's writes across tenants, the rate
// limit an operator sets through a tenant's layer, and the tenant lifecycle
// across replicas. Each claim under "Settings and audit administration" in
// main.go's package comment has its case here.

// trailEventsEnv has a serving run capture the settings change event in the
// journal. It is given to the serving runs of these cases alone, through the
// environment: a configuration naming an event type stops every command-line
// write at startup, since the command groups run no bus.
const trailEventsEnv = "CRM_AUDIT__JOURNAL__EVENTS=settings.changed"

// propagationLimit bounds the wait for a change made through one replica to
// reach another. No window is promised by the settings module or the roster;
// this is how long the cases give one before calling it lost.
var propagationLimit = scaledForRace(15 * time.Second)

// duplicateWindow is how long a case watches for a second copy of an audit row
// after the first has arrived. A copy written by another replica for the same
// event arrives within milliseconds of the first.
const duplicateWindow = 2 * time.Second

// changeRounds is how many times the replica case writes and deletes one
// setting with the same value.
const changeRounds = 2

// The tenant-editable settings as a client knows them: key, declared type, the
// value a tenant reads while no layer holds one, and the default's text.
// Written out here rather than read from the host's table or the declarations,
// so a key the host dropped or retyped fails the case instead of moving it
// along.
var tenantSettingDefaults = []struct {
	key   string
	kind  string
	value any
	// text is the default's text as the listing reports it; the secret's is
	// withheld.
	text string
}{
	{"accounts.page-size", "integer", float64(25), "25"},
	{"deals.default-currency", "string", "USD", "USD"},
	{"deals.idempotency-ttl", "duration", "24h0m0s", "24h0m0s"},
	{"webhooks.url", "string", "", ""},
	{"webhooks.secret", "string", "", ""},
	{"webhooks.timeout", "duration", "5s", "5s"},
	{"webhooks.max-attempts", "integer", float64(3), "3"},
	{"webhooks.retry-delay", "duration", "1s", "1s"},
}

// settingsMutation sends a write or a delete of a setting and returns what came
// back. A nil body sends a delete.
func settingsMutation(t *testing.T, h *crmHost, endpoint, path, alias string, cred credential, value any, del bool) response {
	t.Helper()
	req := request{
		method: nethttp.MethodPut, endpoint: endpoint, path: path, tenant: alias, cred: cred,
		body: map[string]any{"value": value},
	}
	if del {
		req.method, req.body = nethttp.MethodDelete, nil
	}
	return h.send(t, req)
}

// writeTenantSettingAs writes one of the caller's tenant's settings on the API.
func writeTenantSettingAs(t *testing.T, h *crmHost, alias string, cred credential, key string, value any) response {
	t.Helper()
	return settingsMutation(t, h, apiEndpoint, "/settings/"+key, alias, cred, value, false)
}

// settingsListing reads a listing and fails the case unless it answers 200.
func settingsListing(t *testing.T, h *crmHost, endpoint, path, alias string, cred credential) settingsList {
	t.Helper()
	resp := h.get(t, endpoint, path, alias, cred)
	expect(t, "GET "+path+" on "+endpoint, resp, nethttp.StatusOK)
	var got settingsList
	resp.decode(t, &got)
	return got
}

// settingIn finds one key in a listing.
func settingIn(t *testing.T, list settingsList, key string) settingView {
	t.Helper()
	for _, s := range list.Settings {
		if s.Key == key {
			return s
		}
	}
	t.Fatalf("the listing of %q carries no %s: %+v", list.Tenant, key, list.Settings)
	return settingView{}
}

// writtenText is a listing entry's written text, "<none>" when the layer holds
// no row.
func writtenText(v settingView) string {
	if v.Written == nil {
		return "<none>"
	}
	return *v.Written
}

// requireSetting fails the case unless a setting reads as wanted: the layer's
// own text ("<none>" for no row) and the resolved value.
func requireSetting(t *testing.T, what string, v settingView, written string, value any) {
	t.Helper()
	if writtenText(v) != written || !reflect.DeepEqual(v.Value, value) {
		t.Errorf("%s: %s reads written %s, value %#v; want written %s, value %#v",
			what, v.Key, writtenText(v), v.Value, written, value)
	}
}

// settingRow reads the settings table's row for one layer and key.
func (c *crmCase) settingRow(tenantID, key string) (string, bool) {
	c.t.Helper()
	var value string
	err := c.sql().QueryRow(c.rebind("SELECT value FROM settings WHERE tenant_id = ? AND key = ?"), tenantID, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false
	}
	if err != nil {
		c.t.Fatalf("reading the settings row %s/%s: %v", tenantID, key, err)
	}
	return value, true
}

// documentedStatuses reads the merged document a replica serves and answers
// the statuses it lists for one operation, by the path pattern and the method.
func documentedStatuses(t *testing.T, h *crmHost, method, pattern string) []string {
	t.Helper()
	resp := h.get(t, opsEndpoint, "/openapi.json", "", noCredential{})
	expect(t, "GET /openapi.json", resp, nethttp.StatusOK)
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	resp.decode(t, &doc)
	raw, ok := doc.Paths[pattern][strings.ToLower(method)]
	if !ok {
		t.Fatalf("the document has no %s %s", method, pattern)
	}
	var operation struct {
		Responses map[string]json.RawMessage `json:"responses"`
	}
	if err := json.Unmarshal(raw, &operation); err != nil {
		t.Fatalf("reading %s %s in the document: %v", method, pattern, err)
	}
	statuses := make([]string, 0, len(operation.Responses))
	for status := range operation.Responses {
		statuses = append(statuses, status)
	}
	slices.Sort(statuses)
	return statuses
}

// TestATenantAdministersItsSettings is the case for the tenant settings API and
// the operator's writes into the layers.
func TestATenantAdministersItsSettings(t *testing.T) {
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		h := c.serve(trailEventsEnv)
		acme, globex := c.tenantNamed("acme"), c.tenantNamed("globex")
		h.adminCreateTenant(t, acme)
		h.adminCreateTenant(t, globex)
		ann := h.adminCreateUser(t, c.userNamed("ann"))
		gus := h.adminCreateUser(t, c.userNamed("gus"))
		rex := h.adminCreateUser(t, c.userNamed("rex"))
		h.adminGrant(t, acme, ann, roleOwner)
		h.adminGrant(t, globex, gus, roleAdmin)
		h.adminGrant(t, acme, rex, roleRep)

		t.Run("the listing names each editable key with its type, its written value and its resolved one", func(t *testing.T) {
			list := settingsListing(t, h, apiEndpoint, "/settings", acme.Alias, signedIn(ann))
			if list.Tenant != acme.ID {
				t.Errorf("the listing is of %q, want %q", list.Tenant, acme.ID)
			}
			if len(list.Settings) != len(tenantSettingDefaults) {
				t.Errorf("the listing carries %d settings, want the %d a tenant edits: %+v",
					len(list.Settings), len(tenantSettingDefaults), list.Settings)
			}
			for _, want := range tenantSettingDefaults {
				got := settingIn(t, list, want.key)
				if string(got.Type) != want.kind || !got.TenantEditable || got.Description == "" {
					t.Errorf("%s is listed as %+v, want type %s, editable, described", want.key, got, want.kind)
				}
				requireSetting(t, "before any write", got, "<none>", want.value)
			}
		})

		t.Run("a write lands in the tenant's own layer, and two tenants writing one key read their own", func(t *testing.T) {
			sub := c.in(t)
			resp := writeTenantSettingAs(t, h, acme.Alias, signedIn(ann), "deals.default-currency", "EUR")
			expect(t, "acme's owner writing the currency", resp, nethttp.StatusOK)
			var written settingView
			resp.decode(t, &written)
			// The replica that wrote serves the value at once.
			requireSetting(t, "the write's answer", written, "EUR", "EUR")
			expect(t, "globex's admin writing the same key",
				writeTenantSettingAs(t, h, globex.Alias, signedIn(gus), "deals.default-currency", "JPY"), nethttp.StatusOK)

			requireSetting(t, "acme's listing", settingIn(t,
				settingsListing(t, h, apiEndpoint, "/settings", acme.Alias, signedIn(ann)), "deals.default-currency"), "EUR", "EUR")
			requireSetting(t, "globex's listing", settingIn(t,
				settingsListing(t, h, apiEndpoint, "/settings", globex.Alias, signedIn(gus)), "deals.default-currency"), "JPY", "JPY")
			for tenantID, want := range map[string]string{acme.ID: "EUR", globex.ID: "JPY"} {
				if got, ok := sub.settingRow(tenantID, "deals.default-currency"); !ok || got != want {
					t.Errorf("the settings table holds %q (present %v) for %s, want %q", got, ok, tenantID, want)
				}
			}
			if got, ok := sub.settingRow("", "deals.default-currency"); ok {
				t.Errorf("a tenant's write left a global row holding %q", got)
			}
		})

		t.Run("a duration is written as text, and the settings module refuses a bare number", func(t *testing.T) {
			sub := c.in(t)
			resp := writeTenantSettingAs(t, h, acme.Alias, signedIn(ann), "deals.idempotency-ttl", "90m")
			expect(t, "a duration written as text", resp, nethttp.StatusOK)
			var written settingView
			resp.decode(t, &written)
			requireSetting(t, "the duration's answer", written, "1h30m0s", "1h30m0s")

			refused := writeTenantSettingAs(t, h, acme.Alias, signedIn(ann), "deals.idempotency-ttl", 300)
			expect(t, "a bare number for a duration", refused, nethttp.StatusUnprocessableEntity)
			var body apiError
			refused.decode(t, &body)
			if !strings.Contains(body.Error, "value does not match the declared type") {
				t.Errorf("the refusal of a bare number reads %q, want the settings module's type mismatch", body.Error)
			}
			if got, _ := sub.settingRow(acme.ID, "deals.idempotency-ttl"); got != "1h30m0s" {
				t.Errorf("the refused number left the row holding %q, want 1h30m0s", got)
			}

			expect(t, "a whole number for an integer",
				writeTenantSettingAs(t, h, acme.Alias, signedIn(ann), "accounts.page-size", 40), nethttp.StatusOK)
			expect(t, "a fraction for an integer",
				writeTenantSettingAs(t, h, acme.Alias, signedIn(ann), "accounts.page-size", 12.5), nethttp.StatusUnprocessableEntity)
			if got, _ := sub.settingRow(acme.ID, "accounts.page-size"); got != "40" {
				t.Errorf("the refused fraction left the row holding %q, want 40", got)
			}
			expect(t, "a body whose value is null",
				writeTenantSettingAs(t, h, acme.Alias, signedIn(ann), "accounts.page-size", nil), nethttp.StatusUnprocessableEntity)
			// A member spelled in another case is read as the one it
			// resembles, as the HTTP module's binder reads a body. The page
			// size is put back for the steps that follow.
			expect(t, "a body naming the member in another case",
				h.send(t, request{
					method: nethttp.MethodPut, endpoint: apiEndpoint, path: "/settings/accounts.page-size",
					tenant: acme.Alias, cred: signedIn(ann), body: map[string]any{"Value": 41},
				}),
				nethttp.StatusOK)
			if got, _ := sub.settingRow(acme.ID, "accounts.page-size"); got != "41" {
				t.Errorf("a body naming the value member in another case left the page size row holding %q, want 41", got)
			}
			expect(t, "the page size written back",
				writeTenantSettingAs(t, h, acme.Alias, signedIn(ann), "accounts.page-size", 40), nethttp.StatusOK)
		})

		t.Run("a delete falls back to the global value and then to the declared default", func(t *testing.T) {
			sub := c.in(t)
			expect(t, "the operator writing the global currency",
				settingsMutation(t, h, adminEndpoint, "/global-settings/deals.default-currency", "", operator(), "GBP", false),
				nethttp.StatusOK)
			requireSetting(t, "the global listing", settingIn(t,
				settingsListing(t, h, adminEndpoint, "/global-settings", "", operator()), "deals.default-currency"), "GBP", "GBP")
			requireSetting(t, "acme's listing over a global value", settingIn(t,
				settingsListing(t, h, apiEndpoint, "/settings", acme.Alias, signedIn(ann)), "deals.default-currency"), "EUR", "EUR")

			resp := settingsMutation(t, h, apiEndpoint, "/settings/deals.default-currency", acme.Alias, signedIn(ann), nil, true)
			expect(t, "acme's owner deleting the currency", resp, nethttp.StatusOK)
			var after settingView
			resp.decode(t, &after)
			requireSetting(t, "the delete's answer", after, "<none>", "GBP")
			requireSetting(t, "globex's listing after acme's delete", settingIn(t,
				settingsListing(t, h, apiEndpoint, "/settings", globex.Alias, signedIn(gus)), "deals.default-currency"), "JPY", "JPY")

			expect(t, "the operator deleting the global currency",
				settingsMutation(t, h, adminEndpoint, "/global-settings/deals.default-currency", "", operator(), nil, true),
				nethttp.StatusOK)
			requireSetting(t, "acme's listing with no row on either layer", settingIn(t,
				settingsListing(t, h, apiEndpoint, "/settings", acme.Alias, signedIn(ann)), "deals.default-currency"), "<none>", "USD")
			expect(t, "deleting a row that is not there",
				settingsMutation(t, h, apiEndpoint, "/settings/deals.default-currency", acme.Alias, signedIn(ann), nil, true),
				nethttp.StatusOK)
			if _, ok := sub.settingRow(acme.ID, "deals.default-currency"); ok {
				t.Error("acme's currency row is still there after its delete")
			}
		})

		t.Run("a key the host does not list for tenants is not found, whichever module declares it", func(t *testing.T) {
			sub := c.in(t)
			expect(t, "a tenant writing its own rate limit",
				writeTenantSettingAs(t, h, acme.Alias, signedIn(ann), "ratelimit.limit", 100000), nethttp.StatusNotFound)
			expect(t, "a tenant writing a key nothing declares",
				writeTenantSettingAs(t, h, acme.Alias, signedIn(ann), "deals.nosuch", "x"), nethttp.StatusNotFound)
			if got, ok := sub.settingRow(acme.ID, "ratelimit.limit"); ok {
				t.Errorf("a tenant's refused write of its rate limit left a row holding %q", got)
			}
		})

		t.Run("a member in a lesser role is refused", func(t *testing.T) {
			sub := c.in(t)
			expect(t, "a rep listing the settings",
				h.get(t, apiEndpoint, "/settings", acme.Alias, signedIn(rex)), nethttp.StatusForbidden)
			expect(t, "a rep writing a setting",
				writeTenantSettingAs(t, h, acme.Alias, signedIn(rex), "deals.idempotency-ttl", "2h"), nethttp.StatusForbidden)
			expect(t, "a rep deleting a setting",
				settingsMutation(t, h, apiEndpoint, "/settings/accounts.page-size", acme.Alias, signedIn(rex), nil, true),
				nethttp.StatusForbidden)
			if got, ok := sub.settingRow(acme.ID, "accounts.page-size"); !ok || got != "40" {
				t.Errorf("a rep's refused delete left acme's page size row holding %q (present %v), want 40", got, ok)
			}
		})

		operatorKey := string(operatorAccount)
		// requireOperatorChange fails the case unless the seam row of an
		// operator's change to globex's layer is the platform's: it names the
		// layer and the tenant in its detail and states its purpose.
		requireOperatorChange := func(t *testing.T, what string, row trailRow) {
			t.Helper()
			detail := rowDetail(t, row)
			if row.purpose == "" || detail["layer"] != "tenant" || detail["tenant"] != globex.ID {
				t.Errorf("%s is recorded as %+v, want a purpose and a detail naming globex's layer", what, row)
			}
		}

		t.Run("an operator writes any tenant's layer and states the purpose through the seam", func(t *testing.T) {
			sub := c.in(t)
			path := "/tenants/" + globex.ID + "/settings/accounts.page-size"
			expect(t, "the operator writing globex's page size",
				settingsMutation(t, h, adminEndpoint, path, "", operator(), 10, false), nethttp.StatusOK)
			requireSetting(t, "globex's listing after the operator's write", settingIn(t,
				settingsListing(t, h, apiEndpoint, "/settings", globex.Alias, signedIn(gus)), "accounts.page-size"), "10", float64(10))
			requireSetting(t, "acme's listing after the operator's write to globex", settingIn(t,
				settingsListing(t, h, apiEndpoint, "/settings", acme.Alias, signedIn(ann)), "accounts.page-size"), "40", float64(40))

			// The change ran in no tenant's scope, so its row is among the
			// platform's rows and globex's own trail does not show it.
			seam := sub.awaitTrailRow("", "the operator's write into globex's layer", func(r trailRow) bool {
				return r.source == "seam" && r.action == settingWriteAction && r.object == "settings/accounts.page-size" &&
					r.actor == operatorKey
			})
			requireOperatorChange(t, "the operator's write into globex's layer", seam)
			if value := rowDetail(t, seam)["value"]; value != "10" {
				t.Errorf("the operator's write into globex's layer records the value %q, want 10", value)
			}
			globexWrites := readTrailPage(t, h, apiEndpoint, "/audit", globex.Alias, signedIn(gus),
				url.Values{"action": {settingWriteAction}})
			if e, ok := entryWith(globexWrites, func(e trailView) bool { return e.Actor == operatorKey }); ok {
				t.Errorf("globex's own trail shows the operator's change to its layer: %+v", e)
			}

			operatorView := settingsListing(t, h, adminEndpoint, "/tenants/"+globex.ID+"/settings", "", operator())
			if operatorView.Tenant != globex.ID {
				t.Errorf("the operator's listing is of %q, want %q", operatorView.Tenant, globex.ID)
			}
			requireSetting(t, "the operator's view of globex's rate limit",
				settingIn(t, operatorView, "ratelimit.limit"), "<none>", float64(100))
			requireSetting(t, "the operator's view of globex's rate window",
				settingIn(t, operatorView, "ratelimit.window"), "<none>", "1m0s")

			expect(t, "the operator writing into a tenant nobody registered",
				settingsMutation(t, h, adminEndpoint, "/tenants/"+sub.tenantNamed("nosuch").ID+"/settings/deals.idempotency-ttl",
					"", operator(), "2h", false), nethttp.StatusNotFound)
			expect(t, "the operator writing a key the host does not list",
				settingsMutation(t, h, adminEndpoint, "/tenants/"+globex.ID+"/settings/sessions.nosuch",
					"", operator(), "x", false), nethttp.StatusNotFound)
		})

		t.Run("an operator's delete from a tenant's layer leaves the global row and every other tenant's", func(t *testing.T) {
			sub := c.in(t)
			expect(t, "the operator writing the global page size",
				settingsMutation(t, h, adminEndpoint, "/global-settings/accounts.page-size", "", operator(), 30, false),
				nethttp.StatusOK)
			resp := settingsMutation(t, h, adminEndpoint, "/tenants/"+globex.ID+"/settings/accounts.page-size",
				"", operator(), nil, true)
			expect(t, "the operator deleting globex's page size", resp, nethttp.StatusOK)
			var after settingView
			resp.decode(t, &after)
			requireSetting(t, "the delete's answer", after, "<none>", float64(30))
			requireSetting(t, "globex's listing after the operator's delete", settingIn(t,
				settingsListing(t, h, apiEndpoint, "/settings", globex.Alias, signedIn(gus)), "accounts.page-size"),
				"<none>", float64(30))
			requireSetting(t, "acme's listing after the operator's delete from globex", settingIn(t,
				settingsListing(t, h, apiEndpoint, "/settings", acme.Alias, signedIn(ann)), "accounts.page-size"),
				"40", float64(40))
			for tenantID, want := range map[string]string{"": "30", acme.ID: "40"} {
				if got, ok := sub.settingRow(tenantID, "accounts.page-size"); !ok || got != want {
					t.Errorf("the settings table holds %q (present %v) for %q after the delete from globex, want %q",
						got, ok, tenantID, want)
				}
			}
			if got, ok := sub.settingRow(globex.ID, "accounts.page-size"); ok {
				t.Errorf("globex's page size row still holds %q after the operator deleted it", got)
			}
			requireOperatorChange(t, "the operator's delete from globex's layer",
				sub.awaitTrailRow("", "the operator's delete from globex's layer", func(r trailRow) bool {
					return r.source == "seam" && r.action == settingDeleteAction &&
						r.object == "settings/accounts.page-size" && r.actor == operatorKey
				}))
		})

		t.Run("the admin routes refuse a malformed identifier and a body they do not read, as the document lists", func(t *testing.T) {
			sub := c.in(t)
			// A blank is outside the identifier's domain; the path carries it
			// escaped, and the route reads it unescaped.
			const malformed = "/tenants/t%20x"
			for _, probe := range []struct {
				what, method, path, pattern string
				body                        any
				status                      int
			}{
				{
					"listing a malformed tenant's layer", nethttp.MethodGet, malformed + "/settings",
					"/tenants/{id}/settings", nil, nethttp.StatusUnprocessableEntity,
				},
				{
					"writing into a malformed tenant's layer", nethttp.MethodPut, malformed + "/settings/deals.idempotency-ttl",
					"/tenants/{id}/settings/{key}",
					map[string]any{"value": "2h"},
					nethttp.StatusUnprocessableEntity,
				},
				{
					"deleting from a malformed tenant's layer", nethttp.MethodDelete, malformed + "/settings/deals.idempotency-ttl",
					"/tenants/{id}/settings/{key}", nil, nethttp.StatusUnprocessableEntity,
				},
				{
					"writing the global layer with a body that is not an object", nethttp.MethodPut,
					"/global-settings/deals.idempotency-ttl", "/global-settings/{key}",
					[]string{"2h"},
					nethttp.StatusBadRequest,
				},
				{
					"writing globex's layer with a body that is not an object", nethttp.MethodPut,
					"/tenants/" + globex.ID + "/settings/deals.idempotency-ttl", "/tenants/{id}/settings/{key}",
					[]string{"2h"},
					nethttp.StatusBadRequest,
				},
			} {
				resp := h.send(t, request{
					method: probe.method, endpoint: adminEndpoint, path: probe.path, cred: operator(), body: probe.body,
				})
				expect(t, "the operator "+probe.what, resp, probe.status)
				listed := documentedStatuses(t, h, probe.method, probe.pattern)
				if !slices.Contains(listed, strconv.Itoa(probe.status)) {
					t.Errorf("the document lists %v for %s %s, and the operator %s was answered %d",
						listed, probe.method, probe.pattern, probe.what, probe.status)
				}
			}
			for _, tenantID := range []string{"", globex.ID} {
				if got, ok := sub.settingRow(tenantID, "deals.idempotency-ttl"); ok {
					t.Errorf("a refused write left an idempotency lifetime row holding %q for %q", got, tenantID)
				}
			}
		})
	})
}

// trailRow is one row of the journal's table as the harness reads it, with the
// identifier the trail answers with.
type trailRow struct {
	id      int64
	tenant  string
	source  string
	actor   string
	action  string
	object  string
	outcome string
	purpose string
	detail  string
}

// requestOn reports whether the row is the record of a request on the route.
func (r trailRow) requestOn(route string) bool { return requestOn(route, r.source, r.action, r.object) }

// requestOn reports whether the entry is the record of a request on the route.
func (e trailView) requestOn(route string) bool {
	return requestOn(route, e.Source, e.Action, e.Object)
}

// rowDetail decodes a row's detail, the JSON object text the journal writes.
func rowDetail(t *testing.T, r trailRow) map[string]string {
	t.Helper()
	detail := map[string]string{}
	if r.detail == "" {
		return detail
	}
	if err := json.Unmarshal([]byte(r.detail), &detail); err != nil {
		t.Fatalf("decoding the detail of audit row #%d, %q: %v", r.id, r.detail, err)
	}
	return detail
}

// trailRowsOf reads every row of the journal's table that carries a tenant,
// oldest first. The empty tenant reads the platform's rows, which carry none.
func (c *crmCase) trailRowsOf(tenantID string) []trailRow {
	c.t.Helper()
	where, args := "tenant_id = ?", []any{tenantID}
	if tenantID == "" {
		where, args = "(tenant_id IS NULL OR tenant_id = '')", nil
	}
	rows, err := c.sql().Query(c.rebind("SELECT id, COALESCE(tenant_id, ''), source, actor, action, object, outcome, "+
		"purpose, detail FROM audit_entries WHERE "+where+" ORDER BY id"), args...)
	if err != nil {
		c.t.Fatalf("reading the journal: %v", err)
	}
	defer rows.Close()
	var out []trailRow
	for rows.Next() {
		var r trailRow
		if err := rows.Scan(&r.id, &r.tenant, &r.source, &r.actor, &r.action, &r.object, &r.outcome, &r.purpose,
			&r.detail); err != nil {
			c.t.Fatalf("scanning a journal row: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		c.t.Fatalf("reading the journal: %v", err)
	}
	return out
}

// awaitTrailRow waits for a row of one tenant's trail matching a predicate.
func (c *crmCase) awaitTrailRow(tenantID, what string, match func(trailRow) bool) trailRow {
	c.t.Helper()
	var found trailRow
	if !eventually(settleLimit, func() bool {
		for _, r := range c.trailRowsOf(tenantID) {
			if match(r) {
				found = r
				return true
			}
		}
		return false
	}) {
		owner := tenantID
		if owner == "" {
			owner = "the platform"
		}
		c.t.Fatalf("the journal holds no row of %s for %s; it holds %+v", owner, what, c.trailRowsOf(tenantID))
	}
	return found
}

// readTrailPage reads a page of a trail and fails the case unless it answers
// 200.
func readTrailPage(t *testing.T, h *crmHost, endpoint, path, alias string, cred credential, query url.Values) trailPage {
	t.Helper()
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	resp := h.get(t, endpoint, path, alias, cred)
	expect(t, "GET "+path+" on "+endpoint, resp, nethttp.StatusOK)
	var got trailPage
	resp.decode(t, &got)
	return got
}

// entryIDs lists a page's row identifiers, sorted.
func entryIDs(page trailPage) []int64 {
	ids := make([]int64, 0, len(page.Entries))
	for _, e := range page.Entries {
		ids = append(ids, e.ID)
	}
	slices.Sort(ids)
	return ids
}

// rowIDs lists the harness's rows' identifiers, sorted.
func rowIDs(rows []trailRow) []int64 {
	ids := make([]int64, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.id)
	}
	slices.Sort(ids)
	return ids
}

// actionCounts counts the harness's rows by action, in the order the trail
// answers with.
func actionCounts(rows []trailRow) []actionCount {
	counts := map[string]int64{}
	for _, r := range rows {
		counts[r.action]++
	}
	out := make([]actionCount, 0, len(counts))
	for action, n := range counts {
		out = append(out, actionCount{Action: action, Entries: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Action < out[j].Action })
	return out
}

// entryWith finds the page's entry matching a predicate.
func entryWith(page trailPage, match func(trailView) bool) (trailView, bool) {
	for _, e := range page.Entries {
		if match(e) {
			return e, true
		}
	}
	return trailView{}, false
}

// TestATenantReadsItsOwnAuditTrail is the case for the trail on the API
// endpoint and across tenants on the admin endpoint.
//
// Two tenants do the same things — the same setting written to the same value,
// the same user granted the same role — so each trail has rows the other's
// could be mistaken for, and a read that lost its tenant condition would answer
// the other's among its own. The admission layer refuses a stranger in one of
// them, and the settings change event is captured, so the trail holds a row of
// every capture path.
func TestATenantReadsItsOwnAuditTrail(t *testing.T) {
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		h := c.serve(trailEventsEnv)
		acme, globex := c.tenantNamed("acme"), c.tenantNamed("globex")
		h.adminCreateTenant(t, acme)
		h.adminCreateTenant(t, globex)
		ann := h.adminCreateUser(t, c.userNamed("ann"))
		gus := h.adminCreateUser(t, c.userNamed("gus"))
		uma := h.adminCreateUser(t, c.userNamed("uma"))
		sid := h.adminCreateUser(t, c.userNamed("sid"))
		h.adminGrant(t, acme, ann, roleOwner)
		h.adminGrant(t, globex, gus, roleOwner)
		h.adminGrant(t, globex, sid, roleRep)

		owners := map[string]userRef{acme.ID: ann, globex.ID: gus}
		for _, ref := range []tenantRef{acme, globex} {
			owner := owners[ref.ID]
			expect(t, "the owner of "+ref.ID+" writing the currency",
				writeTenantSettingAs(t, h, ref.Alias, signedIn(owner), "deals.default-currency", "EUR"), nethttp.StatusOK)
			expect(t, "the owner of "+ref.ID+" granting uma",
				h.post(t, apiEndpoint, "/members", ref.Alias, signedIn(owner), map[string]string{"email": uma.Email, "role": roleRep}),
				nethttp.StatusOK)
		}
		expect(t, "sid naming acme", h.get(t, apiEndpoint, "/members", acme.Alias, signedIn(sid)), nethttp.StatusNotFound)

		userKey := func(u userRef) string { return u.ID }
		for _, ref := range []tenantRef{acme, globex} {
			sub := c.in(t)
			owner := owners[ref.ID]
			sub.awaitTrailRow(ref.ID, "the currency write's request", func(r trailRow) bool {
				return r.requestOn(writeSettingRoute) && r.outcome == "200" && r.actor == userKey(owner)
			})
			sub.awaitTrailRow(ref.ID, "the currency write's seam row", func(r trailRow) bool {
				return r.source == "seam" && r.action == settingWriteAction && r.object == "settings/deals.default-currency"
			})
			sub.awaitTrailRow(ref.ID, "the captured change event", func(r trailRow) bool {
				return r.source == "event" && r.action == "settings.changed"
			})
			sub.awaitTrailRow(ref.ID, "the grant's statement", func(r trailRow) bool {
				return r.source == "statement" && r.object == memberRoleTable
			})
			sub.awaitTrailRow(ref.ID, "the grant's request", func(r trailRow) bool {
				return r.requestOn(grantMemberRoute) && r.outcome == "200"
			})
		}
		c.awaitTrailRow(acme.ID, "the admission layer's refusal of sid", func(r trailRow) bool {
			return r.requestOn(listMembersRoute) && r.outcome == "404" && r.actor == userKey(sid)
		})

		t.Run("each tenant's trail holds its own rows and no other", func(t *testing.T) {
			sub := c.in(t)
			for _, ref := range []tenantRef{acme, globex} {
				rows := sub.trailRowsOf(ref.ID)
				page := readTrailPage(t, h, apiEndpoint, "/audit", ref.Alias, signedIn(owners[ref.ID]), nil)
				if page.Tenant != ref.ID {
					t.Errorf("the trail read in %s answers for %q", ref.ID, page.Tenant)
				}
				if got, want := entryIDs(page), rowIDs(rows); !slices.Equal(got, want) {
					t.Errorf("the trail of %s holds rows %v, want exactly the rows carrying its tenant %v", ref.ID, got, want)
				}
				if page.Total != int64(len(rows)) {
					t.Errorf("the trail of %s counts %d rows, want %d", ref.ID, page.Total, len(rows))
				}
				if want := actionCounts(rows); !reflect.DeepEqual(page.Actions, want) {
					t.Errorf("the trail of %s counts by action %+v, want %+v", ref.ID, page.Actions, want)
				}
				for _, e := range page.Entries {
					if e.Tenant != ref.ID {
						t.Errorf("the trail of %s answers a row of %q: %+v", ref.ID, e.Tenant, e)
					}
				}
			}
		})

		t.Run("the trail shows the writes, the refused request, the seam rows and the captured change", func(t *testing.T) {
			page := readTrailPage(t, h, apiEndpoint, "/audit", acme.Alias, signedIn(ann), nil)
			for _, want := range []struct {
				what  string
				match func(trailView) bool
			}{
				{"the currency write's request", func(e trailView) bool {
					return e.requestOn(writeSettingRoute) &&
						e.Outcome == "200" && e.Actor == userKey(ann) && e.Detail["path"] == "/settings/deals.default-currency"
				}},
				{"the currency write's seam row", func(e trailView) bool {
					return e.Source == "seam" && e.Action == settingWriteAction && e.Actor == userKey(ann) &&
						e.Detail["key"] == "deals.default-currency" && e.Detail["value"] == "EUR"
				}},
				{"the captured change event", func(e trailView) bool {
					return e.Source == "event" && e.Action == "settings.changed" &&
						e.Detail["Key"] == "deals.default-currency" && e.Detail["New"] == "EUR"
				}},
				{"the grant's statement", func(e trailView) bool {
					return e.Source == "statement" && e.Object == memberRoleTable && e.Actor == userKey(ann)
				}},
				{"the admission layer's refusal of a stranger", func(e trailView) bool {
					return e.requestOn(listMembersRoute) && e.Outcome == "404" && e.Actor == userKey(sid)
				}},
			} {
				if _, ok := entryWith(page, want.match); !ok {
					t.Errorf("acme's trail shows no row for %s: %+v", want.what, page.Entries)
				}
			}
		})

		t.Run("the trail is newest first and narrows by action", func(t *testing.T) {
			page := readTrailPage(t, h, apiEndpoint, "/audit", acme.Alias, signedIn(ann), nil)
			for i := 1; i < len(page.Entries); i++ {
				prev, cur := page.Entries[i-1], page.Entries[i]
				if cur.OccurredAt.After(prev.OccurredAt) || (cur.OccurredAt.Equal(prev.OccurredAt) && cur.ID > prev.ID) {
					t.Errorf("entry %d (%v, #%d) follows entry %d (%v, #%d): the trail is not newest first",
						i, cur.OccurredAt, cur.ID, i-1, prev.OccurredAt, prev.ID)
				}
			}
			one := readTrailPage(t, h, apiEndpoint, "/audit", acme.Alias, signedIn(ann),
				url.Values{"action": {settingWriteAction}})
			if one.Total != 1 || len(one.Entries) != 1 || one.Entries[0].Action != settingWriteAction {
				t.Errorf("the trail narrowed to %s answers %d of %d rows: %+v", settingWriteAction, len(one.Entries), one.Total, one.Entries)
			}
			if !reflect.DeepEqual(one.Actions, page.Actions) {
				t.Errorf("narrowing by action changed the counts by action from %+v to %+v", page.Actions, one.Actions)
			}
			two := readTrailPage(t, h, apiEndpoint, "/audit", acme.Alias, signedIn(ann),
				url.Values{"action": {settingWriteAction, "settings.changed"}})
			if two.Total != 2 {
				t.Errorf("the trail narrowed to two actions counts %d rows, want 2: %+v", two.Total, two.Entries)
			}
			short := readTrailPage(t, h, apiEndpoint, "/audit", acme.Alias, signedIn(ann), url.Values{"limit": {"2"}})
			if len(short.Entries) != 2 || short.Total != page.Total || short.Entries[0].ID != page.Entries[0].ID {
				t.Errorf("a page of two answers %d rows of %d, starting at #%d; want the newest two of %d",
					len(short.Entries), short.Total, short.Entries[0].ID, page.Total)
			}
			for _, bad := range []url.Values{{"since": {"yesterday"}}, {"limit": {"0"}}, {"acton": {"x"}}} {
				expect(t, "a trail read with "+bad.Encode(),
					h.get(t, apiEndpoint, "/audit?"+bad.Encode(), acme.Alias, signedIn(ann)), nethttp.StatusUnprocessableEntity)
			}
		})

		t.Run("the trail narrows by time", func(t *testing.T) {
			sub := c.in(t)
			time.Sleep(5 * time.Millisecond)
			mid := time.Now().UTC()
			time.Sleep(5 * time.Millisecond)
			expect(t, "acme's owner writing the idempotency lifetime",
				writeTenantSettingAs(t, h, acme.Alias, signedIn(ann), "deals.idempotency-ttl", "2h"), nethttp.StatusOK)
			sub.awaitTrailRow(acme.ID, "the lifetime write's request", func(r trailRow) bool {
				return r.source == "request" && strings.HasSuffix(r.detail, `"path":"/settings/deals.idempotency-ttl"}`)
			})
			sub.awaitTrailRow(acme.ID, "the lifetime change event", func(r trailRow) bool {
				return r.source == "event" && strings.Contains(r.detail, `"Key":"deals.idempotency-ttl"`)
			})

			all := readTrailPage(t, h, apiEndpoint, "/audit", acme.Alias, signedIn(ann), nil)
			bound := mid.Format(time.RFC3339Nano)
			after := readTrailPage(t, h, apiEndpoint, "/audit", acme.Alias, signedIn(ann), url.Values{"since": {bound}})
			before := readTrailPage(t, h, apiEndpoint, "/audit", acme.Alias, signedIn(ann), url.Values{"until": {bound}})
			isLifetime := func(e trailView) bool { return e.Detail["key"] == "deals.idempotency-ttl" }
			isCurrency := func(e trailView) bool { return e.Detail["key"] == "deals.default-currency" }
			if _, ok := entryWith(after, isLifetime); !ok {
				t.Errorf("the rows since %s miss the lifetime write: %+v", bound, after.Entries)
			}
			if _, ok := entryWith(after, isCurrency); ok {
				t.Errorf("the rows since %s hold the currency write made before it", bound)
			}
			if _, ok := entryWith(before, isCurrency); !ok {
				t.Errorf("the rows until %s miss the currency write: %+v", bound, before.Entries)
			}
			if _, ok := entryWith(before, isLifetime); ok {
				t.Errorf("the rows until %s hold the lifetime write made after it", bound)
			}
			for _, e := range after.Entries {
				if e.OccurredAt.Before(mid.Truncate(time.Microsecond)) {
					t.Errorf("the rows since %s hold one written at %s: %+v", bound, e.OccurredAt.Format(time.RFC3339Nano), e)
				}
			}
			if after.Total+before.Total != all.Total || after.Total == 0 || before.Total == 0 {
				t.Errorf("the rows since %s (%d) and until it (%d) do not add up to the trail's %d",
					bound, after.Total, before.Total, all.Total)
			}
		})

		t.Run("a single row of another tenant is not found", func(t *testing.T) {
			sub := c.in(t)
			theirs := sub.awaitTrailRow(globex.ID, "globex's currency seam row", func(r trailRow) bool {
				return r.source == "seam" && r.action == settingWriteAction
			})
			ours := sub.awaitTrailRow(acme.ID, "acme's currency seam row", func(r trailRow) bool {
				return r.source == "seam" && r.action == settingWriteAction
			})
			expect(t, "acme's owner reading globex's row",
				h.get(t, apiEndpoint, "/audit/"+strconv.FormatInt(theirs.id, 10), acme.Alias, signedIn(ann)), nethttp.StatusNotFound)
			resp := h.get(t, apiEndpoint, "/audit/"+strconv.FormatInt(ours.id, 10), acme.Alias, signedIn(ann))
			expect(t, "acme's owner reading acme's row", resp, nethttp.StatusOK)
			var got trailView
			resp.decode(t, &got)
			if got.ID != ours.id || got.Tenant != acme.ID || got.Action != settingWriteAction {
				t.Errorf("acme's row reads %+v, want #%d of %s", got, ours.id, acme.ID)
			}
		})

		t.Run("an operator reads across tenants, and one tenant's by entering it", func(t *testing.T) {
			sub := c.in(t)
			operatorKey := string(operatorAccount)

			// No read of the seam's own action has been made before this one,
			// so the record it answers with is its own: it was written before
			// the read ran.
			own := readTrailPage(t, h, adminEndpoint, "/audit-entries", "", operator(),
				url.Values{"action": {trailQueryAction}})
			if e, ok := entryWith(own, func(e trailView) bool { return e.Detail["action"] == trailQueryAction }); !ok {
				t.Errorf("the operator's read of %s does not answer its own record: %+v", trailQueryAction, own.Entries)
			} else if e.Tenant != "" || e.Actor != operatorKey || e.Source != "seam" || e.Purpose == "" {
				t.Errorf("the record of a read across tenants reads %+v, want the operator's seam row, with no tenant "+
					"and a purpose", e)
			}

			across := readTrailPage(t, h, adminEndpoint, "/audit-entries", "", operator(),
				url.Values{"action": {settingWriteAction}})
			tenants := map[string]bool{}
			for _, e := range across.Entries {
				tenants[e.Tenant] = true
			}
			if !tenants[acme.ID] || !tenants[globex.ID] {
				t.Errorf("the operator's read across tenants holds rows of %v, want both %s and %s", tenants, acme.ID, globex.ID)
			}

			// globex's trail carries the same action, so a read of acme that
			// lost the tenant condition would answer globex's rows as well.
			var acmeWrites []trailRow
			for _, r := range sub.trailRowsOf(acme.ID) {
				if r.action == settingWriteAction {
					acmeWrites = append(acmeWrites, r)
				}
			}
			narrowed := readTrailPage(t, h, adminEndpoint, "/audit-entries", "", operator(),
				url.Values{"tenant": {acme.ID}, "action": {settingWriteAction}})
			if got, want := entryIDs(narrowed), rowIDs(acmeWrites); narrowed.Tenant != acme.ID || !slices.Equal(got, want) ||
				narrowed.Total != int64(len(want)) {
				t.Errorf("the operator's read of acme answers rows %v (%d counted) for %q, want exactly acme's %v",
					got, narrowed.Total, narrowed.Tenant, want)
			}

			// The read of acme was recorded in acme's context, so acme's own
			// trail shows it, and globex's does not.
			ours := readTrailPage(t, h, apiEndpoint, "/audit", acme.Alias, signedIn(ann), url.Values{"action": {trailQueryAction}})
			if len(ours.Entries) != 1 || ours.Entries[0].Actor != operatorKey || ours.Entries[0].Purpose == "" ||
				ours.Entries[0].Detail["tenant"] != acme.ID {
				t.Errorf("acme's trail shows the operator's reads as %+v, want the one read of acme, with its purpose",
					ours.Entries)
			}
			theirs := readTrailPage(t, h, apiEndpoint, "/audit", globex.Alias, signedIn(gus), url.Values{"action": {trailQueryAction}})
			if theirs.Total != 0 {
				t.Errorf("globex's trail shows an operator's read it was not the subject of: %+v", theirs.Entries)
			}

			expect(t, "the operator reading a tenant nobody registered",
				h.get(t, adminEndpoint, "/audit-entries?tenant="+sub.tenantNamed("nosuch").ID, "", operator()),
				nethttp.StatusNotFound)
		})

		t.Run("a disabled tenant's trail is read by entering it, and across tenants", func(t *testing.T) {
			sub := c.in(t)
			hooli := sub.tenantNamed("hooli")
			h.adminCreateTenant(t, hooli)
			h.adminGrant(t, hooli, ann, roleOwner)
			expect(t, "ann writing hooli's currency",
				writeTenantSettingAs(t, h, hooli.Alias, signedIn(ann), "deals.default-currency", "EUR"), nethttp.StatusOK)
			written := sub.awaitTrailRow(hooli.ID, "hooli's currency seam row", func(r trailRow) bool {
				return r.source == "seam" && r.action == settingWriteAction
			})
			expect(t, "the operator disabling hooli",
				h.post(t, adminEndpoint, "/tenants/"+hooli.ID+"/disable", "", operator(), nil), nethttp.StatusOK)

			// The read of the disabled hooli enters it, so the filter holds
			// it to hooli's rows, which the other tenants' writes of the same
			// action would join if it did not.
			alone := readTrailPage(t, h, adminEndpoint, "/audit-entries", "", operator(),
				url.Values{"tenant": {hooli.ID}, "action": {settingWriteAction}})
			if got := entryIDs(alone); alone.Tenant != hooli.ID || !slices.Equal(got, []int64{written.id}) {
				t.Errorf("the operator's read of the disabled hooli answers rows %v for %q, want exactly its #%d",
					got, alone.Tenant, written.id)
			}
			// It ran in hooli's scope, so its record is in hooli's own trail.
			sub.awaitTrailRow(hooli.ID, "the record of the operator's read of the disabled hooli", func(r trailRow) bool {
				return r.source == "seam" && r.action == trailQueryAction && rowDetail(t, r)["tenant"] == hooli.ID
			})
			across := readTrailPage(t, h, adminEndpoint, "/audit-entries", "", operator(),
				url.Values{"action": {settingWriteAction}})
			if _, ok := entryWith(across, func(e trailView) bool { return e.ID == written.id && e.Tenant == hooli.ID }); !ok {
				t.Errorf("the read across tenants does not answer the disabled hooli's row #%d: %+v", written.id, across.Entries)
			}
		})

		t.Run("a member in a lesser role is refused", func(t *testing.T) {
			ours := c.in(t).awaitTrailRow(acme.ID, "acme's currency seam row", func(r trailRow) bool {
				return r.source == "seam" && r.action == settingWriteAction
			})
			expect(t, "a rep reading the trail", h.get(t, apiEndpoint, "/audit", acme.Alias, signedIn(uma)), nethttp.StatusForbidden)
			expect(t, "a rep reading one row of the trail",
				h.get(t, apiEndpoint, "/audit/"+strconv.FormatInt(ours.id, 10), acme.Alias, signedIn(uma)), nethttp.StatusForbidden)
		})
	})
}

// retryAfterWithin fails the case unless a refusal carries a Retry-After of
// whole seconds from 1 to the window's length.
func retryAfterWithin(t *testing.T, resp response, window time.Duration) {
	t.Helper()
	seconds, err := strconv.Atoi(resp.header.Get("Retry-After"))
	if err != nil || seconds < 1 || time.Duration(seconds)*time.Second > window {
		t.Errorf("the refusal's Retry-After is %q, want whole seconds from 1 to %v", resp.header.Get("Retry-After"), window)
	}
}

// limitTenant has the operator set a tenant's rate limit through its settings
// layer: an hour's window, so a case never straddles two windows the limiter
// weighs together, and a limit.
func limitTenant(t *testing.T, h *crmHost, ref tenantRef, limit int) {
	t.Helper()
	for key, value := range map[string]any{"ratelimit.window": "1h", "ratelimit.limit": limit} {
		expect(t, "the operator setting "+key+" of "+ref.ID,
			settingsMutation(t, h, adminEndpoint, "/tenants/"+ref.ID+"/settings/"+key, "", operator(), value, false),
			nethttp.StatusOK)
	}
}

// awaitLimit waits until a replica reads a tenant's rate limit as wanted. The
// operator's view reads the replica's own snapshot, which is what the limiter
// on that replica reads too, and the admin endpoint is not limited, so polling
// it spends nothing of the tenant's allowance.
func awaitLimit(t *testing.T, h *crmHost, ref tenantRef, limit int) {
	t.Helper()
	var last settingsList
	if !eventually(propagationLimit, func() bool {
		resp := h.get(t, adminEndpoint, "/tenants/"+ref.ID+"/settings", "", operator())
		if resp.status != nethttp.StatusOK {
			return false
		}
		resp.decode(t, &last)
		var window, count any
		for _, s := range last.Settings {
			switch s.Key {
			case "ratelimit.window":
				window = s.Value
			case "ratelimit.limit":
				count = s.Value
			}
		}
		return window == "1h0m0s" && count == float64(limit)
	}) {
		t.Fatalf("%s never read %s's limit as %d an hour: %+v", h.name, ref.ID, limit, last.Settings)
	}
}

// TestAnOperatorLowersATenantsRateLimit is the case for the limit an operator
// sets through a tenant's settings layer.
func TestAnOperatorLowersATenantsRateLimit(t *testing.T) {
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		h := c.serve()
		acme, globex := c.tenantNamed("acme"), c.tenantNamed("globex")
		h.adminCreateTenant(t, acme)
		h.adminCreateTenant(t, globex)
		ann := h.adminCreateUser(t, c.userNamed("ann"))
		bob := h.adminCreateUser(t, c.userNamed("bob"))
		otto := h.adminCreateUser(t, c.userNamed("otto"))
		h.adminGrant(t, acme, ann, roleOwner)
		h.adminGrant(t, acme, otto, roleOwner)
		h.adminGrant(t, globex, ann, roleOwner)
		h.adminGrant(t, globex, bob, roleRep)

		const limit = 3
		limitTenant(t, h, acme, limit)
		awaitLimit(t, h, acme, limit)

		// The limit is the platform's, so acme's own owner cannot remove it
		// from acme's layer. otto's requests fill a bucket of their own and
		// spend nothing of ann's allowance, which the loop below counts.
		for _, key := range []string{"ratelimit.limit", "ratelimit.window"} {
			expect(t, "acme's owner deleting "+key+" from acme's layer",
				settingsMutation(t, h, apiEndpoint, "/settings/"+key, acme.Alias, signedIn(otto), nil, true),
				nethttp.StatusNotFound)
		}
		for key, want := range map[string]string{"ratelimit.limit": strconv.Itoa(limit), "ratelimit.window": "1h0m0s"} {
			if got, ok := c.settingRow(acme.ID, key); !ok || got != want {
				t.Errorf("acme's %s row holds %q (present %v) after its owner's refused delete, want %q", key, got, ok, want)
			}
		}

		for i := 1; i <= limit; i++ {
			expect(t, fmt.Sprintf("ann's request %d in acme", i),
				h.get(t, apiEndpoint, "/members", acme.Alias, signedIn(ann)), nethttp.StatusOK)
		}
		refused := h.get(t, apiEndpoint, "/members", acme.Alias, signedIn(ann))
		expect(t, "ann's request past acme's limit", refused, nethttp.StatusTooManyRequests)
		requireProblem(t, "the limiter's refusal", refused)
		retryAfterWithin(t, refused, time.Hour)

		// The limiter keeps a bucket per caller and tenant, so globex is
		// driven past acme's limit as well: a limit that reached globex too
		// would refuse the last of these.
		for i := 1; i <= limit+1; i++ {
			expect(t, fmt.Sprintf("ann's request %d in globex, whose limit nobody lowered", i),
				h.get(t, apiEndpoint, "/members", globex.Alias, signedIn(ann)), nethttp.StatusOK)
		}
		requireSetting(t, "globex's rate limit after acme's was lowered", settingIn(t,
			settingsListing(t, h, adminEndpoint, "/tenants/"+globex.ID+"/settings", "", operator()), "ratelimit.limit"),
			"<none>", float64(100))

		// bob belongs to globex alone, so the admission layer refuses every
		// request of his naming acme with the same 404 it answers a member the
		// tenant does not hold. The limiter sits outside that layer and counts
		// the refusals, so past the limit the answer is the limiter's.
		for i := 1; i <= limit; i++ {
			expect(t, fmt.Sprintf("bob's request %d naming acme", i),
				h.get(t, apiEndpoint, "/members", acme.Alias, signedIn(bob)), nethttp.StatusNotFound)
		}
		expect(t, "bob's request past acme's limit",
			h.get(t, apiEndpoint, "/members", acme.Alias, signedIn(bob)), nethttp.StatusTooManyRequests)

		// The recording stage sits inside the throttling stage, so the
		// limiter's own refusal leaves no row: the case gives any capture path
		// the time it would need and then holds that absence.
		time.Sleep(200 * time.Millisecond)
		for _, r := range c.trailRowsOf(acme.ID) {
			if r.source == "request" && r.outcome == "429" && r.actor == bob.ID {
				t.Errorf("the journal holds a row for the limiter's refusal, and the recording stage sits inside the throttling one: %+v", r)
			}
		}
	})
}

// TestARateLimitOutsideItsDomainIsRefused is the case for a rate limit the
// limiter cannot use: zero, or below it. The limiter lets every request
// through on a value it cannot use, so an operator's write that took one would
// turn the tenant's limit off with nothing but a line on standard error to say
// so. The write is refused, with 422, leaving the stored limit as it was and
// the tenant limited by it.
//
// The domain is the rate limiter's: its declaration of the two settings states
// it, and the settings module refuses a write outside it with ErrInvalidValue
// before anything is written, which the admin route answers as it answers a
// value of the wrong type. Nothing of the domain is written in the host, so a
// limiter that declared none would have both writes stored and this case red.
func TestARateLimitOutsideItsDomainIsRefused(t *testing.T) {
	c := newCase(t, stackNamed(t, localStack))
	c.migrate()
	h := c.serve()
	acme := c.tenantNamed("acme")
	h.adminCreateTenant(t, acme)
	ann := h.adminCreateUser(t, c.userNamed("ann"))
	h.adminGrant(t, acme, ann, roleOwner)

	const limit = 3
	limitTenant(t, h, acme, limit)
	awaitLimit(t, h, acme, limit)
	for _, value := range []int{0, -5} {
		resp := settingsMutation(t, h, adminEndpoint, "/tenants/"+acme.ID+"/settings/ratelimit.limit", "", operator(),
			value, false)
		if resp.status != nethttp.StatusUnprocessableEntity {
			t.Errorf("the operator setting acme's rate limit to %d answered %d %s, want 422", value, resp.status, resp.body)
		}
	}
	if got, ok := c.settingRow(acme.ID, "ratelimit.limit"); !ok || got != strconv.Itoa(limit) {
		t.Errorf("acme's rate limit row holds %q (present %v) after the writes outside its domain, want %d", got, ok, limit)
	}
	for i := 1; i <= limit; i++ {
		expect(t, fmt.Sprintf("ann's request %d in acme", i),
			h.get(t, apiEndpoint, "/members", acme.Alias, signedIn(ann)), nethttp.StatusOK)
	}
	expect(t, "ann's request past acme's limit",
		h.get(t, apiEndpoint, "/members", acme.Alias, signedIn(ann)), nethttp.StatusTooManyRequests)
}

// TestTwoReplicasShareSettingsAndTheLimit is the case for the settings layers
// and the rate limit across replicas.
//
// The settings module's periodic reload is off, so the only thing that can
// carry a change to the other replica is the change event. The roster is not
// reread either: the tenants are registered before either replica starts. The
// same write and the same delete are made twice, so two changes carry each
// payload, and only an event's identifier tells them apart.
//
// The members call with the tokens a login issued, as a client polling a
// replica does, so that no poll derives a password. The
// wait for the other replica polls its API endpoint, and the global limit is
// raised before the run so those polls are answered by the settings rather
// than by the limiter; the limit the last subtest counts is initech's own.
func TestTwoReplicasShareSettingsAndTheLimit(t *testing.T) {
	forEachSharedStack(t, func(t *testing.T, c *crmCase) {
		c.config.set("settings.refresh-interval", "0s")
		c.migrate()
		c.setGlobal("ratelimit.limit", "1000000")
		acme, initech := c.createTenant("acme"), c.createTenant("initech")
		ann, ivy := c.createUser("ann"), c.createUser("ivy")
		c.grant(acme, ann, roleOwner)
		c.grant(initech, ivy, roleRep)
		replicas := c.serveReplicas(2, firstThenRest, trailEventsEnv)
		a, b := replicas[0], replicas[1]
		annToken, ivyToken := bearer(a.login(t, ann)), bearer(a.login(t, ivy))

		t.Run("a setting written through one replica is read on the other, and so is its delete", func(t *testing.T) {
			// The written text is read from the table and the value from the
			// replica's snapshot, so the value is what the event has to move.
			awaitCurrency := func(written, want string) {
				t.Helper()
				var last settingView
				if !eventually(propagationLimit, func() bool {
					last = settingIn(t, settingsListing(t, b, apiEndpoint, "/settings", acme.Alias, annToken),
						"deals.default-currency")
					return last.Value == want
				}) {
					t.Fatalf("the second replica reads acme's currency as %#v after %v, want %q", last.Value, propagationLimit, want)
				}
				requireSetting(t, "acme's listing on the second replica", last, written, want)
			}
			// The same write and the same delete, twice: the second round's
			// events carry the payloads of the first round's, which is what the
			// trail subtest below needs.
			for round := 1; round <= changeRounds; round++ {
				expect(t, fmt.Sprintf("ann writing the currency on the first replica, round %d", round),
					writeTenantSettingAs(t, a, acme.Alias, annToken, "deals.default-currency", "EUR"), nethttp.StatusOK)
				awaitCurrency("EUR", "EUR")
				expect(t, fmt.Sprintf("ann deleting the currency on the first replica, round %d", round),
					settingsMutation(t, a, apiEndpoint, "/settings/deals.default-currency", acme.Alias, annToken, nil, true),
					nethttp.StatusOK)
				awaitCurrency("<none>", "USD")
			}
		})

		t.Run("each change is in the trail once, however many replicas receive its event", func(t *testing.T) {
			sub := c.in(t)
			const made = 2 * changeRounds
			changes := func() []trailRow {
				var out []trailRow
				for _, r := range sub.trailRowsOf(acme.ID) {
					if r.source == "event" && r.action == "settings.changed" {
						out = append(out, r)
					}
				}
				return out
			}
			if !eventually(settleLimit, func() bool { return len(changes()) >= made }) {
				t.Fatalf("acme's trail holds %d change events, want the %d the writes and the deletes made: %+v",
					len(changes()), made, changes())
			}
			if eventually(duplicateWindow, func() bool { return len(changes()) > made }) {
				t.Errorf("acme's trail holds %d change events for %d changes: every replica that received an event "+
					"wrote its own row", len(changes()), made)
			}
			// Each round's write and delete carry the payloads of the other
			// round's, so a trail that told events apart by their payload
			// rather than by their identifier would hold one row per pair.
			payloads := map[string]int{}
			for _, r := range changes() {
				payloads[r.detail]++
			}
			if len(payloads) != 2 {
				t.Errorf("the change events carry %d payloads, want 2, the write's and the delete's: %v", len(payloads), payloads)
			}
			page := readTrailPage(t, a, apiEndpoint, "/audit", acme.Alias, annToken, url.Values{"action": {"settings.changed"}})
			if page.Total != made {
				t.Errorf("acme's trail answers %d change events, want %d: %+v", page.Total, made, page.Entries)
			}
		})

		t.Run("two replicas count one limit", func(t *testing.T) {
			const limit = 3
			limitTenant(t, a, initech, limit)
			awaitLimit(t, b, initech, limit)
			for i, h := range []*crmHost{a, b, a} {
				expect(t, fmt.Sprintf("ivy's request %d, on %s", i+1, h.name),
					h.get(t, apiEndpoint, "/members", initech.Alias, ivyToken), nethttp.StatusOK)
			}
			refused := b.get(t, apiEndpoint, "/members", initech.Alias, ivyToken)
			expect(t, "ivy's request past the limit, on the second replica", refused, nethttp.StatusTooManyRequests)
			retryAfterWithin(t, refused, time.Hour)
		})
	})
}

// TestADisabledTenantIsRefusedOnEveryReplica is the case for the tenant
// lifecycle across replicas: the replica that wrote the status serves it at
// once, and the other one after its roster refresh.
//
// The case polls the API endpoint, and every request that reaches the limiter
// spends the caller's allowance there, on both replicas at once. The global
// limit is raised before the run, so a poll that takes a few refresh periods
// is answered by the roster rather than by the limiter. The polls carry a
// session token, as a client that polls does: a poll every few milliseconds
// that signed in each time would spend the replica on derivations.
func TestADisabledTenantIsRefusedOnEveryReplica(t *testing.T) {
	forEachSharedStack(t, func(t *testing.T, c *crmCase) {
		c.config.set("tenant.catalog.refresh-period", "1s")
		c.migrate()
		c.setGlobal("ratelimit.limit", "1000000")
		acme := c.createTenant("acme")
		ann := c.createUser("ann")
		c.grant(acme, ann, roleOwner)
		replicas := c.serveReplicas(2, firstThenRest)
		a, b := replicas[0], replicas[1]
		token := bearer(a.login(t, ann))

		unknown := a.get(t, apiEndpoint, "/members", c.tenantNamed("nosuch").Alias, token)
		expect(t, "a request naming a tenant nobody registered", unknown, nethttp.StatusNotFound)
		likeUnknown := func(h *crmHost) (response, bool) {
			resp := h.get(t, apiEndpoint, "/members", acme.Alias, token)
			return resp, resp.status == unknown.status && string(resp.body) == string(unknown.body)
		}
		served := func(h *crmHost) (response, bool) {
			resp := h.get(t, apiEndpoint, "/members", acme.Alias, token)
			return resp, resp.status == nethttp.StatusOK
		}
		for _, h := range replicas {
			if resp, ok := served(h); !ok {
				t.Fatalf("%s answers acme %d %q before it is disabled, want 200", h.name, resp.status, resp.body)
			}
		}

		expect(t, "the operator disabling acme on the first replica",
			a.post(t, adminEndpoint, "/tenants/"+acme.ID+"/disable", "", operator(), nil), nethttp.StatusOK)
		if resp, ok := likeUnknown(a); !ok {
			t.Errorf("the replica that disabled acme answers it %d %q, want at once what an unknown tenant gets: %d %q",
				resp.status, resp.body, unknown.status, unknown.body)
		}
		var last response
		if !eventually(propagationLimit, func() bool {
			var ok bool
			last, ok = likeUnknown(b)
			return ok
		}) {
			t.Errorf("the other replica still answers acme %d %q after %v, want what an unknown tenant gets",
				last.status, last.body, propagationLimit)
		}

		expect(t, "the operator enabling acme on the first replica",
			a.post(t, adminEndpoint, "/tenants/"+acme.ID+"/enable", "", operator(), nil), nethttp.StatusOK)
		if resp, ok := served(a); !ok {
			t.Errorf("the replica that enabled acme answers it %d %q, want 200 at once", resp.status, resp.body)
		}
		if !eventually(propagationLimit, func() bool {
			var ok bool
			last, ok = served(b)
			return ok
		}) {
			t.Errorf("the other replica still answers acme %d %q %v after it was enabled, want 200",
				last.status, last.body, propagationLimit)
		}
	})
}

// declaredItems reads the metadata every settings declaration of this binary
// gives its items, by full path: the namespace, a dot, and the item's path
// relative to it, which is how a declaration keys its Items. It reads the
// descriptors as their modules wrote them and derives no key from a carrier.
func declaredItems() map[string]settings.Item {
	out := map[string]settings.Item{}
	for _, schema := range core.Resources[settings.Schema](core.ProcessRegistry) {
		for relative, item := range schema.Value.Items {
			key := relative
			if schema.Value.Namespace != "" {
				key = schema.Value.Namespace + "." + relative
			}
			out[key] = item
		}
	}
	return out
}

// TestTheSettingsListingIsTheDeclaration is the case for what the settings
// listing says about each key: the host serves the keys it chooses, and
// everything else it shows of a key — its description and whether it is
// sensitive — is the declaration of the module that reads the key, as the
// settings module reports it through Store.Declared, and so is the declared
// default, withheld for a sensitive key. The operator's listing of the global
// layer carries every served key, and each is held to the metadata its
// declaration gives, and to the type and the default text a client expects of
// it, the tenant's keys and the platform's alike.
func TestTheSettingsListingIsTheDeclaration(t *testing.T) {
	c := newCase(t, stackNamed(t, localStack))
	c.migrate()
	h := c.serve()
	declared := declaredItems()
	list := settingsListing(t, h, adminEndpoint, "/global-settings", "", operator())
	if len(list.Settings) != len(servedKeys) {
		t.Errorf("the global listing carries %d settings, want the %d the host serves: %+v",
			len(list.Settings), len(servedKeys), list.Settings)
	}
	for _, k := range servedKeys {
		got := settingIn(t, list, k.key)
		item := declared[k.key]
		switch {
		case got.Description != item.Description:
			t.Errorf("%s is listed with the description %q, and its declaration gives %q",
				k.key, got.Description, item.Description)
		case got.Sensitive != item.Sensitive:
			t.Errorf("%s is listed as sensitive %v, and its declaration says %v", k.key, got.Sensitive, item.Sensitive)
		case got.Sensitive && got.Default != nil:
			t.Errorf("the sensitive %s is listed with the default %q, want it withheld", k.key, *got.Default)
		case !got.Sensitive && got.Default == nil:
			t.Errorf("%s is listed with no default", k.key)
		case got.TenantEditable != k.tenantEditable:
			t.Errorf("%s is listed as tenant-editable %v, and the host serves it as %v",
				k.key, got.TenantEditable, k.tenantEditable)
		}
	}
	type expected struct{ key, kind, text string }
	var wanted []expected
	for _, want := range tenantSettingDefaults {
		wanted = append(wanted, expected{want.key, want.kind, want.text})
	}
	for _, want := range platformSettingDefaults {
		wanted = append(wanted, expected{want.key, want.kind, want.text})
	}
	for _, k := range servedKeys {
		if !slices.ContainsFunc(wanted, func(w expected) bool { return w.key == k.key }) {
			t.Errorf("the host serves %s, and this case expects no type and default of it", k.key)
		}
	}
	for _, want := range wanted {
		got := settingIn(t, list, want.key)
		if string(got.Type) != want.kind {
			t.Errorf("%s is listed as %q, want %q", want.key, got.Type, want.kind)
		}
		if want.key != webhookSecretSetting && (got.Default == nil || *got.Default != want.text) {
			t.Errorf("%s is listed with the default %v, want the declared %q", want.key, got.Default, want.text)
		}
	}
}

// platformSettingDefaults are the keys the host serves for an operator alone,
// each with the type and the default text the listing is to report for it.
// Like tenantSettingDefaults they are written out as a client expects them,
// not read from a declaration, so a declaration that changed what a client
// reads is caught here.
var platformSettingDefaults = []struct{ key, kind, text string }{
	{"ratelimit.limit", "integer", "100"},
	{"ratelimit.window", "duration", "1m0s"},
	{"webhooks.claim-ttl", "duration", "48h0m0s"},
	{"revenue.claim-ttl", "duration", "48h0m0s"},
	{"reports.cache-ttl", "duration", "5m0s"},
}

// discardingLogger is a stand-in for the logging capability whose loggers
// write nowhere.
type discardingLogger struct{}

func (discardingLogger) Named(string) *slog.Logger { return slog.New(slog.DiscardHandler) }

func (discardingLogger) Redaction() log.Redaction { return nil }

// declarationsOnly is a stand-in for the settings surface that answers
// Declared with the items it holds. The preferences module reads nothing else
// of the surface as it is built.
type declarationsOnly struct {
	settings.Store
	declared []settings.DeclaredItem
}

func (s declarationsOnly) Declared() []settings.DeclaredItem { return s.declared }

// buildPreferences drives a registry holding the preferences module and a
// stand-in for each capability it requires as far as construction, with the
// settings surface declaring the items given, and returns what the drive
// returned. The roster and the seam stand-ins answer nothing: the module
// resolves them after its join and calls neither while it is built.
func buildPreferences(t *testing.T, declared []settings.DeclaredItem) error {
	t.Helper()
	reg := core.New()
	names := []string{preferencesModuleName}
	for _, stand := range []struct {
		name    string
		token   core.Token
		product any
	}{
		{"stand-in.log", (*log.Logger)(nil), discardingLogger{}},
		{"stand-in.db", (*db.Database)(nil), struct{ db.Database }{}},
		{"stand-in.settings", (*settings.Store)(nil), declarationsOnly{declared: declared}},
		{"stand-in.roster", (*tenant.Catalog)(nil), struct{ tenant.Catalog }{}},
		{"stand-in.seam", (*audit.Audit)(nil), struct{ audit.Audit }{}},
	} {
		reg.Register(core.Module{
			Name:     stand.name,
			Provides: []core.Provision{{Token: stand.token}},
			New:      func(context.Context, *core.Registry) (any, error) { return stand.product, nil },
		})
		names = append(names, stand.name)
	}
	reg.Register(preferencesModule())
	return reg.Drive(t.Context(), core.Drive{Depth: core.StageNew, Participants: names})
}

// TestAServedKeyTheDeclarationsLackFailsTheStartup is the case for the join the
// preferences module makes when it is built: a key the host serves and no
// module declares, or one declared under a type the API does not show, fails
// the module's construction, and with it the drive, naming the key, instead of
// a listing that answers 500 on every request. The drive is the module's own
// descriptor in a registry of stand-ins, so what fails is what a serving
// command's startup runs; the same declarations with every served key in them
// are built.
func TestAServedKeyTheDeclarationsLackFailsTheStartup(t *testing.T) {
	var declared []settings.DeclaredItem
	for _, k := range servedKeys {
		declared = append(declared, settings.DeclaredItem{Key: k.key, Type: reflect.TypeFor[string]()})
	}
	if err := buildPreferences(t, declared); err != nil {
		t.Fatalf("building the preferences module with every served key declared as text failed: %v", err)
	}
	missing := servedKeys[len(servedKeys)-1].key
	if err := buildPreferences(t, declared[:len(declared)-1]); err == nil || !strings.Contains(err.Error(), missing) {
		t.Errorf("building the preferences module with a served key no module declares answered %v, "+
			"want a failure naming %s", err, missing)
	}
	type level int
	odd := slices.Clone(declared)
	odd[0].Type = reflect.TypeFor[level]()
	if err := buildPreferences(t, odd); err == nil || !strings.Contains(err.Error(), odd[0].Key) {
		t.Errorf("building the preferences module with a served key declared as %s answered %v, "+
			"want a failure naming %s", odd[0].Type, err, odd[0].Key)
	}
}

// trailTableColumns are the columns of the journal's table the trail shows.
var trailTableColumns = []string{
	"tenant_id", "id", "occurred_at", "actor", "source", "action", "object", "outcome", "purpose", "detail",
}

// TestTheTrailModelsMapEveryColumn is the case for the columns the trail's two
// read models read: each maps every column the trail shows, the tenant column
// among them, so a read through either fills every field the trail renders.
// A model that mapped fewer would read those columns as empty and fail only
// where a row is rendered.
func TestTheTrailModelsMapEveryColumn(t *testing.T) {
	for _, model := range []any{&trailEntry{}, &anyTenantsEntry{}} {
		parsed, err := schema.Parse(model, &sync.Map{}, schema.NamingStrategy{})
		if err != nil {
			t.Fatalf("parsing %T: %v", model, err)
		}
		for _, column := range trailTableColumns {
			if parsed.LookUpField(column) == nil {
				t.Errorf("%T maps no field to the column %s; it maps %v", model, column, parsed.DBNames)
			}
		}
	}
}

// errProbed is what the probe below stops a statement with, once it has seen
// the model the statement is on.
var errProbed = errors.New("the probe stopped the statement before it was issued")

// TestAnOperatorsReadGoesThroughAModelThatTakesANullTenant is the case for the
// model an operator's read of the trail goes through, a property no run shows:
// a platform row read through a model whose tenant field holds no NULL reads
// as the empty identifier, and the page renders that exactly as it renders a
// NULL, as a row with no tenant. So the read the admin route issues is run on
// a dry-run handle whose probe stops its first statement, and the model that
// statement is on is held to what the package comment says of it: its tenant
// column is bound to a field that takes a NULL, and it embeds the scoping
// convention, so the filter conditions the same read on the tenant its context
// carries, and refuses it with none, rather than taking it for a global table.
func TestAnOperatorsReadGoesThroughAModelThatTakesANullTenant(t *testing.T) {
	handle, err := gorm.Open(tests.DummyDialector{}, &gorm.Config{DryRun: true, Logger: logger.Discard})
	if err != nil {
		t.Fatalf("opening a dry-run handle: %v", err)
	}
	var seen *schema.Schema
	if err := handle.Callback().Query().Before("gorm:query").Register("crm:probe", func(tx *gorm.DB) {
		seen = tx.Statement.Schema
		_ = tx.AddError(errProbed)
	}); err != nil {
		t.Fatalf("registering the probe: %v", err)
	}
	if _, err := readOperatorTrail(t.Context(), handle, trailQuery{limit: defaultTrailPage}); !errors.Is(err, errProbed) {
		t.Fatalf("an operator's read answered %v, want the probe's stop", err)
	}
	if seen == nil {
		t.Fatal("an operator's read issued its first statement on no model")
	}
	switch field := seen.LookUpField("tenant_id"); {
	case field == nil:
		t.Errorf("an operator's read goes through %s, which maps no field to tenant_id", seen.ModelType)
	case field.FieldType.Kind() != reflect.Pointer:
		t.Errorf("an operator's read goes through %s, whose tenant_id field is a %s and holds no NULL",
			seen.ModelType, field.FieldType)
	}
	if !embedsTenantModel(reflect.New(seen.ModelType).Interface()) {
		t.Errorf("an operator's read goes through %s, which does not embed the scoping convention, so the filter "+
			"takes it for a global table and conditions no read of it", seen.ModelType)
	}
}

// TestNothingIsWrittenThroughTheTrailModels is the case for the guard of the
// trail's two read models, the one a tenant's trail is read through and the one
// the read across tenants is: every write form GORM runs model hooks for is
// refused with the models' own error, and a read is not. The handle is a dry
// run on a dialector that reaches no database, since the refusal comes before a
// statement would.
func TestNothingIsWrittenThroughTheTrailModels(t *testing.T) {
	handle, err := gorm.Open(tests.DummyDialector{}, &gorm.Config{DryRun: true, Logger: logger.Discard})
	if err != nil {
		t.Fatalf("opening a dry-run handle: %v", err)
	}
	for _, model := range []struct {
		name string
		// row is a pointer to one row of the model carrying the columns
		// given; rows is a pointer to a slice of them.
		row  func(TrailColumns) any
		rows func(...TrailColumns) any
	}{
		{
			name: "trailEntry",
			row:  func(c TrailColumns) any { return &trailEntry{TrailColumns: c} },
			rows: func(cs ...TrailColumns) any {
				out := []trailEntry{}
				for _, c := range cs {
					out = append(out, trailEntry{TrailColumns: c})
				}
				return &out
			},
		},
		{
			name: "anyTenantsEntry",
			row:  func(c TrailColumns) any { return &anyTenantsEntry{TrailColumns: c} },
			rows: func(cs ...TrailColumns) any {
				out := []anyTenantsEntry{}
				for _, c := range cs {
					out = append(out, anyTenantsEntry{TrailColumns: c})
				}
				return &out
			},
		},
	} {
		t.Run(model.name, func(t *testing.T) {
			forged := func() any { return model.row(TrailColumns{ID: 1, Action: "forged"}) }
			for _, form := range []struct {
				name string
				run  func(tx *gorm.DB) *gorm.DB
			}{
				{"Create", func(tx *gorm.DB) *gorm.DB { return tx.Create(forged()) }},
				{"Create of several rows", func(tx *gorm.DB) *gorm.DB {
					return tx.Create(model.rows(TrailColumns{ID: 1}, TrailColumns{ID: 2}))
				}},
				{"Save", func(tx *gorm.DB) *gorm.DB { return tx.Save(forged()) }},
				{"FirstOrCreate", func(tx *gorm.DB) *gorm.DB {
					return tx.FirstOrCreate(model.row(TrailColumns{}), model.row(TrailColumns{ID: 1}))
				}},
				{"Updates with a map", func(tx *gorm.DB) *gorm.DB {
					return tx.Model(model.row(TrailColumns{})).Where("id = ?", 1).Updates(map[string]any{"action": "forged"})
				}},
				{"Updates with a struct", func(tx *gorm.DB) *gorm.DB {
					return tx.Model(forged()).Updates(model.row(TrailColumns{Outcome: "forged"}))
				}},
				{"Update of one column", func(tx *gorm.DB) *gorm.DB { return tx.Model(forged()).Update("outcome", "forged") }},
				{"Delete by key", func(tx *gorm.DB) *gorm.DB { return tx.Delete(model.row(TrailColumns{}), 1) }},
				{"Delete by condition", func(tx *gorm.DB) *gorm.DB {
					return tx.Where("action = ?", "forged").Delete(model.row(TrailColumns{}))
				}},
			} {
				if err := form.run(handle.Session(&gorm.Session{})).Error; !errors.Is(err, errTrailReadOnly) {
					t.Errorf("%s through %s answered %v, want its refusal", form.name, model.name, err)
				}
			}
			if err := handle.Where("action = ?", "forged").Find(model.rows()).Error; err != nil {
				t.Errorf("a read through %s answered %v, want no error", model.name, err)
			}
		})
	}
}
