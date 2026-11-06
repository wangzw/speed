package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/audit/journal"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/tenant"
)

// exportRoute is the export route as the cases address it.
const exportRoute = "/api/v1/system/audit/export"

// A request with no identity is the 401 the admission and authorization layers
// write, answered before the guard is asked and before the table is read.
func TestNoIdentityOnTheExportIs401(t *testing.T) {
	r := newRig(t)
	w := r.doAnonymous(exportRoute)

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

// The export asks the guard the same question the list route asks, in the same
// platform domain: one permission, audit.read, covers reading a page and
// exporting the whole matching set.
func TestTheExportAsksForAuditReadInThePlatformDomain(t *testing.T) {
	r := newRig(t)
	if w := r.do(exportRoute); w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if asked := r.guard.askedFor(); len(asked) != 1 || asked[0] != authz.PermissionAuditRead {
		t.Fatalf("the guard was asked %v, want just audit.read", asked)
	}
	if domains := r.guard.domainsAsked(); len(domains) != 1 || domains[0] != authz.PlatformDomain {
		t.Fatalf("the guard decided in %v, want the platform domain", domains)
	}
}

// A shape the route cannot read is a 400 carrying the capability's problem type,
// and it reaches the table for nothing. The export takes the list route's
// filters and neither of the parameters that describe a page.
func TestAnExportShapeRefusalComesBeforeTheRead(t *testing.T) {
	cases := map[string]string{
		"a time bound that is not RFC 3339":           exportRoute + "?since=yesterday",
		"an action that names nothing":                exportRoute + "?action=",
		"an outcome that names nothing":               exportRoute + "?outcome=",
		"an actor that names nothing":                 exportRoute + "?actor=",
		"a cursor on the export":                      exportRoute + "?cursor=10",
		"a limit on the export":                       exportRoute + "?limit=5",
		"a query parameter the route does not accept": exportRoute + "?nope=1",
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

// The export writes one record view per line, page by page: it hands the read
// surface the filters the request carried and the read surface's maximum page
// size, continues with the cursor the read surface returned, and stops when the
// cursor is empty. Every line is the record view, not a row and not a page.
func TestTheExportStreamsEveryPageAsNDJSON(t *testing.T) {
	r := newRig(t)
	at := time.Date(2026, 3, 1, 9, 30, 0, 0, time.UTC)
	r.reader.script(
		stubPage{
			rows: []journal.Row{
				{ID: 9, Tenant: "alpha", OccurredAt: at, Actor: "alice", Source: "seam", Action: "a.update", Object: "orders/1", Outcome: "ok", Detail: map[string]string{"k": "v"}},
			},
			next: "7",
		},
		stubPage{
			rows: []journal.Row{
				{ID: 7, OccurredAt: at.Add(-time.Hour), Action: "a.create", Outcome: "ok"},
			},
		},
	)

	w := r.do(exportRoute + "?since=2026-03-01T00:00:00Z&until=2026-03-02T00:00:00Z&action=a.create&action=a.update&outcome=ok&actor=alice")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != exportMediaType {
		t.Errorf("Content-Type = %q, want %q", got, exportMediaType)
	}

	body := w.Body.String()
	if !strings.HasSuffix(body, "\n") {
		t.Errorf("the body does not end each line with a newline: %q", body)
	}
	lines := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("the export holds %d lines, want 2: %q", len(lines), body)
	}
	var first, second AuditAdminRecord
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("decoding the first line %q: %v", lines[0], err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatalf("decoding the second line %q: %v", lines[1], err)
	}
	if first.ID != "9" || first.Tenant != "alpha" || first.Detail["k"] != "v" {
		t.Errorf("the first line is %+v, want the first page's row as a record view", first)
	}
	if second.ID != "7" || second.Tenant != "" {
		t.Errorf("the second line is %+v, want the second page's row and an empty tenant", second)
	}

	called := r.reader.recorded()
	if len(called) != 2 {
		t.Fatalf("the table was read %d time(s), want one read per page", len(called))
	}
	firstQuery, secondQuery := called[0].q, called[1].q
	if firstQuery.After != "" || secondQuery.After != "7" {
		t.Errorf("the reads started after %q and %q, want the start and then the cursor the first page returned",
			firstQuery.After, secondQuery.After)
	}
	if firstQuery.Limit != journal.MaxPageSize || secondQuery.Limit != journal.MaxPageSize {
		t.Errorf("the reads asked for %d and %d rows, want the read surface's maximum %d",
			firstQuery.Limit, secondQuery.Limit, journal.MaxPageSize)
	}
	for _, q := range []journal.Query{firstQuery, secondQuery} {
		if !q.Since.Equal(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)) {
			t.Errorf("since = %v, want the parsed lower bound on every page", q.Since)
		}
		if !q.Until.Equal(time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)) {
			t.Errorf("until = %v, want the parsed upper bound on every page", q.Until)
		}
		if strings.Join(q.Actions, ",") != "a.create,a.update" {
			t.Errorf("actions = %v, want the two given on every page", q.Actions)
		}
		if strings.Join(q.Outcomes, ",") != "ok" || strings.Join(q.Actors, ",") != "alice" {
			t.Errorf("outcomes/actors = %v/%v, want the values given on every page", q.Outcomes, q.Actors)
		}
	}

	// The export reads across tenants like every route of this module, under a
	// context of its own rather than the request's.
	if _, ok := tenant.FromContext(called[0].ctx); ok {
		t.Error("the read context carries a tenant, want a context marked to cross tenants")
	}
	if called[0].ctx == r.reqCtx {
		t.Error("the read was handed the request's own context, want one derived from it")
	}
}

// aliasedName answers one identifier with an alias that differs from it, so a
// case can tell which of the two a surface carries.
type aliasedName struct {
	id    string
	alias string
}

func (a aliasedName) AliasOf(_ context.Context, id tenant.ID) (string, error) {
	if string(id) == a.id {
		return a.alias, nil
	}
	return "", nil
}

// TestTheExportNamesTheTenantByAliasNeverTheIdentifier holds the tenant column
// of an export file to the rule the list route follows: the row stores the
// internal identifier, and the file a recipient downloads names the alias. The
// fixture's values differ, so a file carrying either one is distinguishable.
func TestTheExportNamesTheTenantByAliasNeverTheIdentifier(t *testing.T) {
	const (
		internalID = "ACME-INTERNAL"
		alias      = "acme-public"
	)
	r := newRig(t)
	r.p.aliases = aliasedName{id: internalID, alias: alias}
	r.reader.script(stubPage{rows: []journal.Row{
		{ID: 1, Tenant: internalID, Action: "a.update", Outcome: "ok"},
	}})

	w := r.do(exportRoute)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if strings.Contains(body, internalID) {
		t.Errorf("the export carries the internal identifier: %s", body)
	}
	if !strings.Contains(body, alias) {
		t.Errorf("the export does not carry the tenant alias: %s", body)
	}
}

// The export names the download and forbids caching it: the name is the record
// set with the export's UTC instant written compactly, and the cache directive
// is no-store because exported audit data is not kept by a cache.
func TestTheExportNamesTheFileAndForbidsCaching(t *testing.T) {
	r := newRig(t)
	before := time.Now().UTC().Add(-time.Minute)
	w := r.do(exportRoute)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}

	disposition, params, err := mime.ParseMediaType(w.Header().Get("Content-Disposition"))
	if err != nil {
		t.Fatalf("parsing Content-Disposition %q: %v", w.Header().Get("Content-Disposition"), err)
	}
	if disposition != "attachment" {
		t.Errorf("disposition = %q, want an attachment", disposition)
	}
	name := params["filename"]
	if !strings.HasPrefix(name, "audit-records-") || !strings.HasSuffix(name, ".ndjson") {
		t.Fatalf("filename = %q, want audit-records-<instant>.ndjson", name)
	}
	stamp := strings.TrimSuffix(strings.TrimPrefix(name, "audit-records-"), ".ndjson")
	at, err := time.Parse(exportTimeLayout, stamp)
	if err != nil {
		t.Fatalf("the filename's instant %q is not %q: %v", stamp, exportTimeLayout, err)
	}
	if at.Before(before) {
		t.Errorf("the filename's instant is %v, want the export's own instant, not before %v", at, before)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

// A filter that matches nothing is a successful export with an empty body, not
// an error: a zero-row file is still a file.
func TestAnEmptyExportIsAnEmptySuccessfulBody(t *testing.T) {
	r := newRig(t)
	w := r.do(exportRoute)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if w.Body.Len() != 0 {
		t.Errorf("the body is %q, want none", w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != exportMediaType {
		t.Errorf("Content-Type = %q, want %q", got, exportMediaType)
	}
	if w.Header().Get("Content-Disposition") == "" {
		t.Error("the empty export carries no Content-Disposition, want the download name")
	}
}

// A first-page read failure happens before any byte is written, so it is still
// a status: a 503 with no problem type, and the cause only in the log.
func TestAFirstPageFailureIs503(t *testing.T) {
	r := newRig(t)
	r.reader.fail(errors.New("the table is gone"))
	w := r.do(exportRoute)
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

// A request the caller abandoned before the first page came back is not a
// dependency failure: the read ends with the request's cancellation, so there is
// no fault to report. The failure is still mapped to the 503 the shape answers
// with, but the log carries no error line for it.
func TestAFirstPageReadCancelledByTheCallerIsNotAFailure(t *testing.T) {
	r := newRig(t)
	r.reader.fail(fmt.Errorf("reading: %w", context.Canceled))
	ctx, cancel := context.WithCancel(authntest.Context(t, context.Background()))
	cancel()
	w := r.send(exportRoute, ctx)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want the mapped 503", w.Code)
	}
	if logged := r.logs.String(); strings.Contains(logged, "level=ERROR") {
		t.Errorf("the log carries an error line for a cancelled request: %q", logged)
	}
}

// A failure after the first byte cannot change the status: the connection is
// abandoned, and the caller sees an incomplete file. The case needs a real
// server, because a recorder swallows the abort the reply raises. The filters
// and the rows that reached the caller are in the log, with the cause.
func TestAnExportFailureAfterTheFirstByteAbandonsTheConnection(t *testing.T) {
	r := newRig(t)
	r.reader.script(
		stubPage{
			rows: []journal.Row{{ID: 2, Action: "a.create", Outcome: "ok"}},
			next: "1",
		},
		stubPage{err: errors.New("the table went away mid-export")},
	)
	server := r.server(t)

	response, err := http.Get(server.URL + exportRoute + "?action=a.create")
	if err != nil {
		t.Fatalf("requesting the export: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: the failure came after the body started", response.StatusCode)
	}
	body, readErr := io.ReadAll(response.Body)
	if readErr == nil {
		t.Fatalf("the body ended without an error, want an incomplete download: %q", body)
	}
	if !strings.Contains(string(body), `"id":"2"`) {
		t.Errorf("the partial body %q does not hold the first page's row", body)
	}

	logged := r.logs.String()
	if !strings.Contains(logged, "the table went away mid-export") {
		t.Errorf("the cause is not in the log: %q", logged)
	}
	if !strings.Contains(logged, "rows=1") {
		t.Errorf("the log does not carry the rows that reached the caller: %q", logged)
	}
}

// The download name is written in UTC whatever zone the instant carries, so an
// export started on a non-UTC clock still names the file with its UTC instant.
func TestTheExportFileNameIsUTC(t *testing.T) {
	zone := time.FixedZone("UTC+8", 8*60*60)
	at := time.Date(2026, 10, 24, 18, 15, 0, 0, zone)
	if got, want := exportFileName(at), "audit-records-20261024T101500Z.ndjson"; got != want {
		t.Errorf("exportFileName(%v) = %q, want %q", at, got, want)
	}
}
