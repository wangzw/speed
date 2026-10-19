package inbox

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/core"
)

// send drives one request through the engine with the context given.
func send(t *testing.T, engine *gin.Engine, method, target string, ctx context.Context) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	return w
}

// problem is the default problem-details shape a refusal carries.
type problem struct {
	Type   string `json:"type"`
	Status int    `json:"status"`
}

// decode reads a JSON response body into target.
func decode(t *testing.T, w *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), target); err != nil {
		t.Fatalf("decoding the response %q: %v", w.Body.String(), err)
	}
}

// A route reached with no identity is refused with a 401 carrying no body type.
func TestRoutesRefuseWithNoIdentity(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		e.on(t, host{}).drive(t, func(_ context.Context, reg *core.Registry) error {
			engine := engineFor(t, product(t, reg))
			anon := authntest.Anonymous(t, context.Background(), authn.AccessOptional)
			for _, route := range []struct{ method, target string }{
				{http.MethodGet, pathMessages},
				{http.MethodGet, pathUnreadCount},
				{http.MethodPost, pathReadAll},
				{http.MethodPost, basePath + "/messages/anything/read"},
			} {
				w := send(t, engine, route.method, route.target, anon)
				if w.Code != http.StatusUnauthorized {
					t.Errorf("%s %s answered %d, want 401", route.method, route.target, w.Code)
				}
			}
			return nil
		})
	})
}

// The pagination bounds are refused whole with a 400 of the bad-request code.
func TestListRefusesOutOfRangePagination(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		e.on(t, host{}).drive(t, func(_ context.Context, reg *core.Registry) error {
			engine := engineFor(t, product(t, reg))
			ctx := requestContext(t, tenantCtx(t, reg, "acme"), authntest.WithAccount("user-1"))
			for _, query := range []string{"limit=-1", "limit=201", "offset=-1", "limit=x"} {
				w := send(t, engine, http.MethodGet, pathMessages+"?"+query, ctx)
				if w.Code != http.StatusBadRequest {
					t.Errorf("GET %s answered %d, want 400", query, w.Code)
					continue
				}
				var p problem
				decode(t, w, &p)
				if p.Type != typePrefix+badRequestCode {
					t.Errorf("GET %s answered type %q, want %q", query, p.Type, typePrefix+badRequestCode)
				}
			}
			return nil
		})
	})
}

// The four routes answer the shapes the design fixes, and none of them writes a
// message row.
func TestRoutesAnswerTheirShapesAndWriteNoRow(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		e.on(t, host{}).drive(t, func(_ context.Context, reg *core.Registry) error {
			in := inboxOf(t, reg)
			s := product(t, reg)
			engine := engineFor(t, s)
			ctx := requestContext(t, tenantCtx(t, reg, "acme"), authntest.WithAccount("user-1"))

			before := rowCount(t, s)

			// A listing over the messages written through the seam.
			writeOne(t, in, ctx, "user-1", "key-1")
			writeOne(t, in, ctx, "user-1", "key-2")

			w := send(t, engine, http.MethodGet, pathMessages, ctx)
			if w.Code != http.StatusOK {
				t.Errorf("GET %s answered %d, want 200", pathMessages, w.Code)
			}
			var listing struct {
				Messages []NotificationMessage `json:"messages"`
				Total    int                   `json:"total"`
			}
			decode(t, w, &listing)
			if listing.Total != 2 || len(listing.Messages) != 2 {
				t.Errorf("the listing is %+v (total %d), want the two messages", listing.Messages, listing.Total)
			}
			if len(listing.Messages) == 2 {
				m := listing.Messages[0]
				if m.ID == "" || m.TypeKey != "billing.invoice_paid" || m.Group != "billing" || m.ReadAt != nil {
					t.Errorf("the first view is %+v, want the sample's fields and no read time", m)
				}
			}

			// The group filter reaches the store (the group column is quoted).
			grouped := send(t, engine, http.MethodGet, pathMessages+"?group=billing", ctx)
			if grouped.Code != http.StatusOK {
				t.Errorf("GET %s?group=billing answered %d, want 200", pathMessages, grouped.Code)
			}
			var groupedListing struct {
				Total int `json:"total"`
			}
			decode(t, grouped, &groupedListing)
			if groupedListing.Total != 2 {
				t.Errorf("the group-filtered total is %d, want 2", groupedListing.Total)
			}
			empty := send(t, engine, http.MethodGet, pathMessages+"?group=security", ctx)
			var emptyListing struct {
				Total int `json:"total"`
			}
			decode(t, empty, &emptyListing)
			if emptyListing.Total != 0 {
				t.Errorf("the other group's total is %d, want 0", emptyListing.Total)
			}

			// The unread count.
			unread := send(t, engine, http.MethodGet, pathUnreadCount, ctx)
			if unread.Code != http.StatusOK {
				t.Errorf("GET %s answered %d, want 200", pathUnreadCount, unread.Code)
			}
			var count struct {
				Unread int `json:"unread"`
			}
			decode(t, unread, &count)
			if count.Unread != 2 {
				t.Errorf("the unread count is %d, want 2", count.Unread)
			}

			// Marking one message read.
			first := listing.Messages[0].ID
			marked := send(t, engine, http.MethodPost, basePath+"/messages/"+first+"/read", ctx)
			if marked.Code != http.StatusNoContent {
				t.Errorf("POST the message read answered %d, want 204", marked.Code)
			}
			if again := send(t, engine, http.MethodPost, basePath+"/messages/"+first+"/read", ctx); again.Code != http.StatusNoContent {
				t.Errorf("repeating the marking answered %d, want 204", again.Code)
			}

			// An unknown message is a 404 of the message-not-found code.
			missing := send(t, engine, http.MethodPost, basePath+"/messages/ghost/read", ctx)
			if missing.Code != http.StatusNotFound {
				t.Errorf("marking an unknown message answered %d, want 404", missing.Code)
			}
			var p problem
			decode(t, missing, &p)
			if p.Type != typePrefix+messageNotFoundCode {
				t.Errorf("the missing message answered type %q, want %q", p.Type, typePrefix+messageNotFoundCode)
			}

			// read-all marks the rest, and a repeat marks none.
			readAll := send(t, engine, http.MethodPost, pathReadAll, ctx)
			if readAll.Code != http.StatusOK {
				t.Errorf("POST %s answered %d, want 200", pathReadAll, readAll.Code)
			}
			var readCount struct {
				ReadCount int `json:"read_count"`
			}
			decode(t, readAll, &readCount)
			if readCount.ReadCount != 1 {
				t.Errorf("read-all marked %d, want the one still-unread message", readCount.ReadCount)
			}
			repeat := send(t, engine, http.MethodPost, pathReadAll, ctx)
			var repeatCount struct {
				ReadCount int `json:"read_count"`
			}
			decode(t, repeat, &repeatCount)
			if repeatCount.ReadCount != 0 {
				t.Errorf("the repeated read-all marked %d, want 0", repeatCount.ReadCount)
			}

			// The surface wrote no message row of its own: the two rows are the
			// two written through the seam, and no request added a third.
			if after := rowCount(t, s); after != before+2 {
				t.Errorf("the table holds %d rows, want the two written through the seam (%d before)", after, before)
			}
			return nil
		})
	})
}

// A route reached with an identity carries the recipient of that identity, so
// one account never reads another's inbox.
func TestRoutesReadTheIdentitysInbox(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		e.on(t, host{}).drive(t, func(_ context.Context, reg *core.Registry) error {
			in := inboxOf(t, reg)
			s := product(t, reg)
			engine := engineFor(t, s)
			base := tenantCtx(t, reg, "acme")

			one := requestContext(t, base, authntest.WithAccount("user-1"))
			two := requestContext(t, base, authntest.WithAccount("user-2"))
			writeOne(t, in, one, "user-1", "key-1")

			first := send(t, engine, http.MethodGet, pathMessages, one)
			var listing struct {
				Total int `json:"total"`
			}
			decode(t, first, &listing)
			if listing.Total != 1 {
				t.Errorf("user-1 sees %d messages, want 1", listing.Total)
			}

			second := send(t, engine, http.MethodGet, pathMessages, two)
			var other struct {
				Total int `json:"total"`
			}
			decode(t, second, &other)
			if other.Total != 0 {
				t.Errorf("user-2 sees %d messages, want 0", other.Total)
			}
			return nil
		})
	})
}

// The descriptor's route set is the closed four, and every one of them mounts.
func TestTheRouteSetIsClosedAndMounts(t *testing.T) {
	want := map[mountedRoute]bool{
		{http.MethodGet, "/api/v1/notifications/messages"}:                  true,
		{http.MethodGet, "/api/v1/notifications/messages/unread-count"}:     true,
		{http.MethodPost, "/api/v1/notifications/messages/read-all"}:        true,
		{http.MethodPost, "/api/v1/notifications/messages/:messageId/read"}: true,
	}
	got := (&inboxStore{}).routes()
	if len(got) != len(want) {
		t.Fatalf("the route set holds %d routes, want %d", len(got), len(want))
	}
	for _, r := range got {
		if !want[mountedRoute{r.method, r.path}] {
			t.Errorf("unexpected route %s %s", r.method, r.path)
		}
	}
}
