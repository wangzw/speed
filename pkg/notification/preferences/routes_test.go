package preferences

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/notification"
)

// wantRoutes is the closed route set.
var wantRoutes = map[mountedRoute]bool{
	{http.MethodGet, "/api/v1/notifications/types"}:                         true,
	{http.MethodGet, "/api/v1/notifications/preferences"}:                   true,
	{http.MethodPut, "/api/v1/notifications/preferences/:typeKey/:channel"}: true,
}

// TestTheModuleMountsItsWholeRouteSet holds that every route in the closed set is
// mounted on an endpoint the module is told to carry them, and that the entry
// point's other endpoints are left alone.
func TestTheModuleMountsItsWholeRouteSet(t *testing.T) {
	router := newStubRouter("public", "admin")
	h := engines[0].on(t, harness{withRouter: true, router: router, endpoints: []string{"public"}})
	h.drive(t, func(_ context.Context, _ *core.Registry) error {
		got := router.endpoint("public").registered()
		if len(got) != len(wantRoutes) {
			t.Fatalf("mounted %d routes (%v), want %d", len(got), got, len(wantRoutes))
		}
		for _, r := range got {
			if !wantRoutes[r] {
				t.Errorf("unexpected route %s %s", r.method, r.path)
			}
		}
		if elsewhere := router.endpoint("admin").registered(); len(elsewhere) != 0 {
			t.Errorf("the unnamed endpoint carries %v, want none", elsewhere)
		}
		return nil
	})
}

// TestACommandWithoutAnEntryPointStillDeliversPreferences holds that the entry
// point is optional: a run that assembles none still delivers the seam.
func TestACommandWithoutAnEntryPointStillDeliversPreferences(t *testing.T) {
	h := engines[0].on(t, harness{declare: []core.Module{declaringModule("billing", aSpec("billing.invoice_paid", true))}})
	h.drive(t, func(_ context.Context, reg *core.Registry) error {
		if prefs, err := core.Resolve[notification.Preferences](reg); err != nil || prefs == nil {
			t.Fatalf("resolving the preferences with no entry point: %v", err)
		}
		return nil
	})
}

// TestGetTypesReportsTheTaxonomy holds the GET /types body: one entry per type,
// built field by field.
func TestGetTypesReportsTheTaxonomy(t *testing.T) {
	spec := notification.TypeSpec{
		Key:                    "billing.invoice_paid",
		Group:                  "billing",
		DefaultChannels:        []notification.Channel{notification.ChannelInApp, notification.ChannelEmail},
		Unsubscribable:         true,
		RecipientVisibleParams: []string{"invoice_id", "amount"},
	}
	h := engines[0].on(t, harness{declare: []core.Module{declaringModule("billing", spec)}})
	h.drive(t, func(_ context.Context, reg *core.Registry) error {
		p := productOfAssembly(t, reg)
		rec := serveHandler(t, http.MethodGet, pathTypes, "/api/v1/notifications/types",
			context.Background(), nil, listTypesOperation.Handle(p.listTypes))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /types answered %d, want 200: %s", rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Vary"); !strings.Contains(got, "Accept-Language") {
			t.Errorf("GET /types Vary = %q, want it to name Accept-Language", got)
		}
		var body struct {
			Types []NotificationType `json:"types"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decoding the body: %v", err)
		}
		if len(body.Types) != 1 {
			t.Fatalf("the body carries %d types, want 1", len(body.Types))
		}
		got := body.Types[0]
		if got.Key != "billing.invoice_paid" || got.Group != "billing" || !got.Unsubscribable {
			t.Errorf("the type view is %+v", got)
		}
		if len(got.DefaultChannels) != 2 || got.DefaultChannels[0] != "in_app" {
			t.Errorf("default_channels = %v, want [in_app email]", got.DefaultChannels)
		}
		if len(got.RecipientVisibleParams) != 2 {
			t.Errorf("recipient_visible_params = %v, want two entries", got.RecipientVisibleParams)
		}
		return nil
	})
}

// TestGetPreferencesReportsTheEffectiveMatrix holds the GET /preferences body:
// one row per type, the effective set, and explicit telling a stored row from
// the default channels. It reads the table, so it runs on every engine.
func TestGetPreferencesReportsTheEffectiveMatrix(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		spec := aSpec("billing.invoice_paid", true, notification.ChannelInApp, notification.ChannelEmail)
		h := e.on(t, harness{declare: []core.Module{declaringModule("billing", spec)}})
		h.drive(t, func(_ context.Context, reg *core.Registry) error {
			ctx := tenantCtx(t, reg, "acme")
			p := productOfAssembly(t, reg)

			// No stored row yet: the default channels, not explicit.
			row := getPreferences(t, ctx, p)
			if len(row.Preferences) != 1 {
				t.Fatalf("the matrix has %d rows, want 1", len(row.Preferences))
			}
			first := row.Preferences[0]
			if first.Explicit {
				t.Error("a type with no stored row reports explicit=true")
			}
			if len(first.Channels) != 2 || first.Channels[0] != "in_app" {
				t.Errorf("channels = %v, want the default [in_app email]", first.Channels)
			}

			// After a write the row is explicit and carries the written set.
			if _, err := preferencesOf(t, reg).Set(ctx, "user-1", "billing.invoice_paid", []notification.Channel{notification.ChannelSMS}); err != nil {
				t.Fatalf("Set: %v", err)
			}
			row = getPreferences(t, ctx, p)
			first = row.Preferences[0]
			if !first.Explicit {
				t.Error("a type with a stored row reports explicit=false")
			}
			if len(first.Channels) != 1 || first.Channels[0] != "sms" {
				t.Errorf("channels = %v, want [sms]", first.Channels)
			}
			return nil
		})
	})
}

// preferenceMatrix is the GET /preferences body.
type preferenceMatrix struct {
	Preferences []NotificationPreference `json:"preferences"`
}

// getPreferences drives GET /preferences with the recipient on the context and
// decodes the body.
func getPreferences(t *testing.T, ctx context.Context, p *product) preferenceMatrix {
	t.Helper()
	rec := serveHandler(t, http.MethodGet, pathPreferences, "/api/v1/notifications/preferences",
		WithRecipient(ctx, "user-1"), nil, listPreferencesOperation.Handle(p.listPreferences))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /preferences answered %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Vary"); !strings.Contains(got, "Accept-Language") {
		t.Errorf("GET /preferences Vary = %q, want it to name Accept-Language", got)
	}
	var body preferenceMatrix
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding the body: %v", err)
	}
	return body
}

// putPreference drives one PUT and returns the recorder, so a case reads the
// status and the body. The request carries the declared application/json media
// type, which the entry point requires to bind the body.
func putPreference(t *testing.T, ctx context.Context, p *product, typeKey, channel, body string) *httptest.ResponseRecorder {
	t.Helper()
	return putPreferenceAs(t, ctx, p, typeKey, channel, body, "application/json")
}

// putPreferenceAs drives one PUT with the given Content-Type. An empty value
// sends none, so a case can hold the entry point's 415 refusal.
func putPreferenceAs(t *testing.T, ctx context.Context, p *product, typeKey, channel, body, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Handle(http.MethodPut, pathPreferenceChannel, updatePreferenceOperation.Handle(p.setPreference))
	req, err := http.NewRequestWithContext(WithRecipient(ctx, "user-1"), http.MethodPut,
		"/api/v1/notifications/preferences/"+typeKey+"/"+channel, strings.NewReader(body))
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

// TestPutFollowsTheFixedDecisionOrder holds the order the design fixes: a body
// naming no enabled is refused first, the channel is checked before the type and
// before any no-op branch, the type before unsubscribability, and only then is
// the set written.
func TestPutFollowsTheFixedDecisionOrder(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		unsubscribable := aSpec("billing.invoice_paid", true, notification.ChannelInApp, notification.ChannelEmail)
		locked := aSpec("authn.code.sent", false, notification.ChannelInApp)
		h := e.on(t, harness{declare: []core.Module{
			declaringModule("billing", unsubscribable),
			declaringModule("authn", locked),
		}})
		h.drive(t, func(_ context.Context, reg *core.Registry) error {
			ctx := tenantCtx(t, reg, "acme")
			p := productOfAssembly(t, reg)

			type expect struct {
				status int
				code   string
			}
			cases := []struct {
				name    string
				typeKey string
				channel string
				body    string
				want    expect
			}{
				{"enabled is missing", "billing.invoice_paid", "email", `{}`, expect{status: http.StatusBadRequest, code: badRequestCode}},
				// A literal null body binds as the empty object at the entry point,
				// which reaches the handler as a body naming no enabled.
				{"body is null", "billing.invoice_paid", "email", `null`, expect{status: http.StatusBadRequest, code: badRequestCode}},
				// A literal null enabled decodes into the pointer as nil, the same
				// state as a body naming no enabled, so the handler refuses it.
				{"enabled is null", "billing.invoice_paid", "email", `{"enabled": null}`, expect{status: http.StatusBadRequest, code: badRequestCode}},
				// The channel is checked before the type: an unknown channel on an
				// unknown type is refused as a bad request on the channel.
				{"unknown channel before unknown type", "nope.nope", "pigeon", `{"enabled": true}`, expect{status: http.StatusBadRequest, code: badRequestCode}},
				{"unknown channel", "billing.invoice_paid", "pigeon", `{"enabled": true}`, expect{status: http.StatusBadRequest, code: badRequestCode}},
				{"unknown type", "nope.nope", "email", `{"enabled": true}`, expect{status: http.StatusBadRequest, code: badRequestCode}},
				{"not unsubscribable", "authn.code.sent", "in_app", `{"enabled": false}`, expect{status: http.StatusConflict, code: notUnsubscribableCode}},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					before := rowCount(t, reg)
					rec := putPreference(t, ctx, p, tc.typeKey, tc.channel, tc.body)
					if rec.Code != tc.want.status {
						t.Fatalf("PUT answered %d, want %d: %s", rec.Code, tc.want.status, rec.Body.String())
					}
					problem := problemOf(t, rec.Body.Bytes())
					if want := typePrefix + tc.want.code; problem.Type != want {
						t.Errorf("problem type = %q, want %q", problem.Type, want)
					}
					if problem.Title == "" {
						t.Error("the module's refusal carries no title")
					}
					if problem.Detail == "" {
						t.Error("the module's refusal carries no detail")
					}
					if after := rowCount(t, reg); after != before {
						t.Errorf("a refused PUT changed the row count %d -> %d", before, after)
					}
				})
			}
			return nil
		})
	})
}

// TestPutRefusesABodyOutsideTheDeclaration holds the entry point's binding
// refusals: a Content-Type that is not application/json is a 415, and an empty
// body (the declaration marks it required), a body that is not a JSON object, a
// body whose enabled is not a boolean, or one that carries a member the
// declaration does not describe is a 400. A literal null is not among them: the
// entry point binds it as the empty object, so it is the module's refusal and is
// held by TestPutFollowsTheFixedDecisionOrder. These answers are the entry
// point's, so they carry the default problem document — about:blank, the
// standard English status text and no detail — and none of them reaches the
// handler or writes a row.
func TestPutRefusesABodyOutsideTheDeclaration(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		spec := aSpec("billing.invoice_paid", true, notification.ChannelInApp, notification.ChannelEmail)
		h := e.on(t, harness{declare: []core.Module{declaringModule("billing", spec)}})
		h.drive(t, func(_ context.Context, reg *core.Registry) error {
			ctx := tenantCtx(t, reg, "acme")
			p := productOfAssembly(t, reg)

			cases := []struct {
				name        string
				contentType string
				body        string
				status      int
			}{
				{"body is empty", "application/json", "", http.StatusBadRequest},
				{"body is not an object", "application/json", `"nope"`, http.StatusBadRequest},
				{"enabled is not a bool", "application/json", `{"enabled": "yes"}`, http.StatusBadRequest},
				{"unknown member", "application/json", `{"enabled": true, "extra": 1}`, http.StatusBadRequest},
				{"media type is not JSON", "text/plain", `{"enabled": true}`, http.StatusUnsupportedMediaType},
				{"media type is missing", "", `{"enabled": true}`, http.StatusUnsupportedMediaType},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					before := rowCount(t, reg)
					rec := putPreferenceAs(t, ctx, p, "billing.invoice_paid", "email", tc.body, tc.contentType)
					if rec.Code != tc.status {
						t.Fatalf("PUT answered %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
					}
					problem := problemOf(t, rec.Body.Bytes())
					if problem.Type != "about:blank" {
						t.Errorf("problem type = %q, want the entry point's default about:blank", problem.Type)
					}
					if want := http.StatusText(tc.status); problem.Title != want {
						t.Errorf("problem title = %q, want the standard %q", problem.Title, want)
					}
					if problem.Detail != "" {
						t.Errorf("problem detail = %q, want none from the entry point", problem.Detail)
					}
					if after := rowCount(t, reg); after != before {
						t.Errorf("a refused PUT changed the row count %d -> %d", before, after)
					}
				})
			}
			return nil
		})
	})
}

// TestPutIsANoOpWhenTheTargetEqualsTheCurrentSet holds the no-op branch: adding a
// channel already present, and removing one already absent, answer 200 and write
// nothing. In particular, PUTing a type that has no stored row to its own
// defaults writes no row, so explicit stays false.
func TestPutIsANoOpWhenTheTargetEqualsTheCurrentSet(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		spec := aSpec("billing.invoice_paid", true, notification.ChannelInApp, notification.ChannelEmail)
		h := e.on(t, harness{declare: []core.Module{declaringModule("billing", spec)}})
		h.drive(t, func(_ context.Context, reg *core.Registry) error {
			ctx := tenantCtx(t, reg, "acme")
			p := productOfAssembly(t, reg)

			// No stored row; adding a channel that is already in the default set
			// is a no-op and lands no row.
			rec := putPreference(t, ctx, p, "billing.invoice_paid", "email", `{"enabled": true}`)
			if rec.Code != http.StatusOK {
				t.Fatalf("PUT answered %d, want 200: %s", rec.Code, rec.Body.String())
			}
			if n := rowCount(t, reg); n != 0 {
				t.Errorf("a no-op PUT landed %d rows, want 0", n)
			}
			if got := putChannels(t, rec.Body.Bytes()); len(got) != 2 || got[0] != "in_app" {
				t.Errorf("the no-op PUT answered channels %v, want the current effective set", got)
			}

			// A stored row now; removing a channel that is not in it is a no-op too.
			if _, err := preferencesOf(t, reg).Set(ctx, "user-1", "billing.invoice_paid", []notification.Channel{notification.ChannelInApp}); err != nil {
				t.Fatalf("Set: %v", err)
			}
			rec = putPreference(t, ctx, p, "billing.invoice_paid", "sms", `{"enabled": false}`)
			if rec.Code != http.StatusOK {
				t.Fatalf("PUT answered %d, want 200: %s", rec.Code, rec.Body.String())
			}
			if n := rowCount(t, reg); n != 1 {
				t.Errorf("a no-op PUT changed the row count to %d, want 1", n)
			}
			return nil
		})
	})
}

// TestPutWritesTheTargetSetAndAnswersTheEffectiveSet holds the write branch:
// adding and removing a channel both land a row and the answer carries the
// effective set.
func TestPutWritesTheTargetSetAndAnswersTheEffectiveSet(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		spec := aSpec("billing.invoice_paid", true, notification.ChannelInApp, notification.ChannelEmail)
		h := e.on(t, harness{declare: []core.Module{declaringModule("billing", spec)}})
		h.drive(t, func(_ context.Context, reg *core.Registry) error {
			ctx := tenantCtx(t, reg, "acme")
			p := productOfAssembly(t, reg)

			// Remove email from the default set: a write lands a row.
			rec := putPreference(t, ctx, p, "billing.invoice_paid", "email", `{"enabled": false}`)
			if rec.Code != http.StatusOK {
				t.Fatalf("PUT answered %d, want 200: %s", rec.Code, rec.Body.String())
			}
			if got := putChannels(t, rec.Body.Bytes()); len(got) != 1 || got[0] != "in_app" {
				t.Errorf("the PUT answered channels %v, want [in_app]", got)
			}
			if n := rowCount(t, reg); n != 1 {
				t.Errorf("the write branch landed %d rows, want 1", n)
			}

			// Add sms back: the answer is the new effective set.
			rec = putPreference(t, ctx, p, "billing.invoice_paid", "sms", `{"enabled": true}`)
			if rec.Code != http.StatusOK {
				t.Fatalf("PUT answered %d, want 200: %s", rec.Code, rec.Body.String())
			}
			if got := putChannels(t, rec.Body.Bytes()); len(got) != 2 || got[0] != "in_app" || got[1] != "sms" {
				t.Errorf("the PUT answered channels %v, want [in_app sms]", got)
			}
			return nil
		})
	})
}

// putChannels decodes a PUT body's channel list.
func putChannels(t *testing.T, body []byte) []string {
	t.Helper()
	var out struct {
		Channels []string `json:"channels"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decoding the PUT body: %v", err)
	}
	return out.Channels
}

// problem is the members of a problem document a case reads.
type problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

// problemOf decodes a problem body's members.
func problemOf(t *testing.T, body []byte) problem {
	t.Helper()
	var out problem
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decoding the problem body: %v", err)
	}
	return out
}
