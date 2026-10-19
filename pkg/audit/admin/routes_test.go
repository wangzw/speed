package admin

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/audit/journal"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/tenant"
)

// listRoute and getRoute are the two routes as the cases address them.
const (
	listRoute = "/api/v1/system/audit/records"
	getRoute  = listRoute + "/"
)

// A request with no identity is the 401 the admission and authorization layers
// write: the status alone, no problem type, and the realm challenge naming the
// endpoint the route was mounted on. It is answered before the guard is asked
// and before the table is read.
func TestNoIdentityIs401(t *testing.T) {
	r := newRig(t)
	w := r.doAnonymous(listRoute)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if p := problemOf(t, w); p.Type != blankType {
		t.Errorf("the 401 carries the problem type %q, want the neutral default", p.Type)
	}
	if challenge := w.Header().Get("WWW-Authenticate"); !strings.Contains(challenge, `realm="public"`) {
		t.Errorf("the challenge is %q, want the realm the route was mounted on", challenge)
	}
	if asked := r.guard.askedFor(); len(asked) != 0 {
		t.Errorf("the guard was asked %v, want no question before the caller is read", asked)
	}
	if called := r.reader.recorded(); len(called) != 0 {
		t.Errorf("the table was read %v, want no read before the decision", called)
	}
}

// A shortfall is the guard's own answer: a denial is a 403 and an outage a 503,
// and neither reaches the table.
func TestAShortfallIsTheGuardsOwnAnswer(t *testing.T) {
	t.Run("denial", func(t *testing.T) {
		r := newRig(t)
		r.guard.allow = false
		if w := r.do(listRoute); w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", w.Code)
		}
		if called := r.reader.recorded(); len(called) != 0 {
			t.Errorf("the table was read %v after a denial, want no read", called)
		}
	})
	t.Run("outage", func(t *testing.T) {
		r := newRig(t)
		r.guard.err = errors.New("the policy engine is down")
		if w := r.do(listRoute); w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", w.Code)
		}
	})
}

// The decision is made for audit.read, in the platform domain, whatever tenant
// the request carries. Deciding in the request's own tenant would let a caller
// authorized in one tenant read another's rows by sending a tenant header.
func TestTheDecisionIsMadeInThePlatformDomain(t *testing.T) {
	r := newRig(t)
	if w := r.do(listRoute); w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if asked := r.guard.askedFor(); len(asked) != 1 || asked[0] != authz.PermissionAuditRead {
		t.Fatalf("the guard was asked %v, want just audit.read", asked)
	}
	if domains := r.guard.domainsAsked(); len(domains) != 1 || domains[0] != authz.PlatformDomain {
		t.Fatalf("the guard decided in %v, want the platform domain", domains)
	}
}

// The read is issued under a cross-tenant mark derived from the request, which
// is what makes it reach every tenant's rows and the rows that belong to none
// rather than the request's own tenant. The mark's effect on the table is the
// read surface's, held in its own cases; what is held here is that the handler
// hands the read surface a context of its own, carrying no tenant.
func TestTheReadIsIssuedAcrossTenants(t *testing.T) {
	r := newRig(t)
	if w := r.do(listRoute); w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	called := r.reader.recorded()
	if len(called) != 1 {
		t.Fatalf("the table was read %d time(s), want once", len(called))
	}
	if _, ok := tenant.FromContext(called[0].ctx); ok {
		t.Error("the read context carries a tenant, want a context marked to cross tenants")
	}
	if called[0].ctx == r.reqCtx {
		t.Error("the read was handed the request's own context, want one derived from it")
	}
}

// A shape the route cannot read is a 400 carrying the capability's problem type,
// and it reaches the table for nothing.
func TestAShapeRefusalComesBeforeTheRead(t *testing.T) {
	cases := map[string]string{
		"a time bound that is not RFC 3339":           listRoute + "?since=yesterday",
		"a negative limit":                            listRoute + "?limit=-1",
		"a limit that is not a number":                listRoute + "?limit=many",
		"an action that names nothing":                listRoute + "?action=",
		"an outcome that names nothing":               listRoute + "?outcome=",
		"an actor that names nothing":                 listRoute + "?actor=",
		"a query parameter the route does not accept": listRoute + "?nope=1",
		"a path that is not an identifier":            getRoute + "abc",
		"a query parameter on the get route":          getRoute + "1?foo=bar",
	}
	for name, target := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			w := r.do(target)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", w.Code)
			}
			if p := problemOf(t, w); p.Type != typePrefix+badRequestCode {
				t.Errorf("problem type = %q, want %q", p.Type, typePrefix+badRequestCode)
			}
			if called := r.reader.recorded(); len(called) != 0 {
				t.Errorf("the table was read %v on a shape refusal, want no read", called)
			}
		})
	}
}

// The list route reads one page and answers it as a page of views and the cursor
// of the next, and it hands the read surface every bound the request carried.
func TestTheListAnswersAPageAndPassesTheQueryThrough(t *testing.T) {
	r := newRig(t)
	at := time.Date(2026, 3, 1, 9, 30, 0, 0, time.UTC)
	r.reader.rows = []journal.Row{
		{ID: 7, Tenant: "alpha", OccurredAt: at, Actor: "alice", Source: "seam", Action: "a.update", Object: "orders/1", Outcome: "ok", Detail: map[string]string{"k": "v"}},
		{ID: 6, Tenant: "", OccurredAt: at.Add(-time.Hour), Actor: "bob", Source: "request", Action: "a.create", Object: "orders/2", Outcome: "ok"},
	}
	r.reader.next = "6"

	w := r.do(listRoute + "?since=2026-03-01T00:00:00Z&until=2026-03-02T00:00:00Z&action=a.create&action=a.update&limit=5&cursor=10")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var page AuditAdminRecordPage
	decode(t, w, &page)
	if len(page.Entries) != 2 {
		t.Fatalf("the page holds %d entries, want 2", len(page.Entries))
	}
	if page.Entries[0].ID != "7" || page.Entries[0].Tenant != "alpha" {
		t.Errorf("the first entry is %+v, want id 7 of tenant alpha", page.Entries[0])
	}
	if page.Entries[0].Detail["k"] != "v" {
		t.Errorf("the first entry's detail is %v, want k=v", page.Entries[0].Detail)
	}
	if !page.Entries[0].OccurredAt.Equal(at) {
		t.Errorf("occurred_at = %v, want %v in UTC", page.Entries[0].OccurredAt, at)
	}
	if page.Entries[1].Tenant != "" {
		t.Errorf("a row with no tenant shows %q, want the empty string", page.Entries[1].Tenant)
	}
	if page.Next != "6" {
		t.Errorf("next = %q, want the cursor the read surface returned", page.Next)
	}

	called := r.reader.recorded()
	if len(called) != 1 {
		t.Fatalf("the table was read %d time(s), want once", len(called))
	}
	q := called[0].q
	if !q.Since.Equal(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("since = %v, want the parsed lower bound", q.Since)
	}
	if !q.Until.Equal(time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("until = %v, want the parsed upper bound", q.Until)
	}
	if strings.Join(q.Actions, ",") != "a.create,a.update" {
		t.Errorf("actions = %v, want the two given", q.Actions)
	}
	if q.After != "10" {
		t.Errorf("after = %q, want the cursor given", q.After)
	}
	if q.Limit != 5 {
		t.Errorf("limit = %d, want 5", q.Limit)
	}
}

// The list route hands the read surface the outcome and actor sets the request
// carried, each narrowed to the rows it names, alongside the time window and the
// action set.
func TestTheListPassesOutcomeAndActorThrough(t *testing.T) {
	r := newRig(t)
	w := r.do(listRoute + "?outcome=ok&outcome=failed&actor=alice&actor=bob&action=a.create")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	called := r.reader.recorded()
	if len(called) != 1 {
		t.Fatalf("the table was read %d time(s), want once", len(called))
	}
	q := called[0].q
	if strings.Join(q.Outcomes, ",") != "ok,failed" {
		t.Errorf("outcomes = %v, want the two given", q.Outcomes)
	}
	if strings.Join(q.Actors, ",") != "alice,bob" {
		t.Errorf("actors = %v, want the two given", q.Actors)
	}
	if strings.Join(q.Actions, ",") != "a.create" {
		t.Errorf("actions = %v, want the one given", q.Actions)
	}
}

// The get route reads one row by its identifier and answers it as a record view.
func TestTheGetRouteAnswersOneRecord(t *testing.T) {
	r := newRig(t)
	at := time.Date(2026, 2, 2, 8, 0, 0, 0, time.UTC)
	r.reader.rows = []journal.Row{{ID: 42, OccurredAt: at, Action: "a.remove", Outcome: "ok"}}

	w := r.do(getRoute + "42")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var view AuditAdminRecord
	decode(t, w, &view)
	if view.ID != "42" || view.Action != "a.remove" {
		t.Errorf("the view is %+v, want the row the read surface returned", view)
	}

	called := r.reader.recorded()
	if len(called) != 1 || called[0].q.ID != 42 || called[0].q.Limit != 1 {
		t.Fatalf("the read was issued as %+v, want the row 42 read one at a time", called)
	}
}

// The get route answers a 404, with no problem type, when the table holds no
// such row — including a path naming zero, which names no row at all.
func TestTheGetRouteAnswers404WhenNoRow(t *testing.T) {
	t.Run("no such row", func(t *testing.T) {
		r := newRig(t)
		w := r.do(getRoute + "999")
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", w.Code)
		}
		if w.Body.Len() != 0 {
			t.Errorf("the 404 carries the body %q, want none", w.Body.String())
		}
	})
	t.Run("zero names no row", func(t *testing.T) {
		r := newRig(t)
		if w := r.do(getRoute + "0"); w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", w.Code)
		}
		if called := r.reader.recorded(); len(called) != 0 {
			t.Errorf("the table was read %v for a path naming zero, want no read", called)
		}
	})
}

// A read failure is a 503 with no problem type, and its cause stays in the log.
func TestAReadFailureIs503(t *testing.T) {
	r := newRig(t)
	r.reader.fail(errors.New("the table is gone"))
	w := r.do(listRoute)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	if p := problemOf(t, w); p.Type != blankType {
		t.Errorf("the 503 carries the problem type %q, want the neutral default", p.Type)
	}
	if !strings.Contains(r.logs.String(), "the table is gone") {
		t.Errorf("the cause is not in the log: %q", r.logs.String())
	}
}

// A cursor the read surface refuses is a shape failure the handler could not see
// before the read — the cursor is opaque — so it is a 400, not an outage.
func TestACursorTheReadSurfaceRefusesIs400(t *testing.T) {
	r := newRig(t)
	r.reader.fail(fmt.Errorf("%w: garbage", journal.ErrInvalidQuery))
	w := r.do(listRoute + "?cursor=garbage")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if p := problemOf(t, w); p.Type != typePrefix+badRequestCode {
		t.Errorf("problem type = %q, want %q", p.Type, typePrefix+badRequestCode)
	}
}

// Every read leaves a diagnostic line — the route, the caller and the rows it
// answered — because a read writes no audit row of its own.
func TestEveryReadLeavesADiagnosticLine(t *testing.T) {
	r := newRig(t)
	r.reader.rows = []journal.Row{{ID: 1}}
	if w := r.do(listRoute); w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	line := r.logs.String()
	for _, want := range []string{"/api/v1/system/audit/records", "hits"} {
		if !strings.Contains(line, want) {
			t.Errorf("the diagnostic line %q does not carry %q", line, want)
		}
	}
}
