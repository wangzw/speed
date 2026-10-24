package admin

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/audit/journal"
	"github.com/vislake/speed/pkg/authn"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/tenant"
)

// The list route's query parameters, a closed set. A name not among them is a
// shape the module refuses, so that a misspelled bound is reported rather than
// swallowed by a read that ignores it.
const (
	paramSince   = "since"
	paramUntil   = "until"
	paramAction  = "action"
	paramOutcome = "outcome"
	paramActor   = "actor"
	paramCursor  = "cursor"
	paramLimit   = "limit"
)

// listParams is the set the list route accepts.
var listParams = map[string]bool{
	paramSince:   true,
	paramUntil:   true,
	paramAction:  true,
	paramOutcome: true,
	paramActor:   true,
	paramCursor:  true,
	paramLimit:   true,
}

// exportParams is the set the export route accepts. It is deliberately not
// listParams: a cursor and a page size describe a page, and an export reads the
// whole matching set, so either on an export request is a shape refusal rather
// than a bound the route silently ignores.
var exportParams = map[string]bool{
	paramSince:   true,
	paramUntil:   true,
	paramAction:  true,
	paramOutcome: true,
	paramActor:   true,
}

// handleRecordList answers GET /api/v1/system/audit/records: one page of audit records,
// narrowed by a time window and sets of actions, outcomes and actors, read
// newest first.
//
// The caller and the decision are the mounted route's: the guard in front of
// this handler read the identity (401 with no identity) and asked for
// audit.read (a shortfall is the guard's own answer) before any parsing ran.
// The handler reads the parameters, checks their shape (a shape this module
// cannot read is a 400), reads the table under a cross-tenant mark (a read
// failure is a 503) and answers a page.
func (p *product) handleRecordList(ctx context.Context, in RecordListInput) (AuditAdminRecordPage, error) {
	caller, ok := authn.FromContext(ctx)
	if !ok {
		return AuditAdminRecordPage{}, speedhttp.Fault{Status: http.StatusUnauthorized}
	}
	q, ok := listQueryOf(in)
	if !ok {
		return AuditAdminRecordPage{}, badRequestFault()
	}
	rows, next, err := p.reader.Query(tenant.CrossTenant(ctx), q)
	if err != nil {
		return AuditAdminRecordPage{}, p.answerErr("querying audit records", err)
	}
	entries := make([]AuditAdminRecord, len(rows))
	for i, r := range rows {
		entries[i] = recordViewOf(r)
	}
	p.logRead(recordsPath, caller, len(rows))
	return AuditAdminRecordPage{Entries: entries, Next: next}, nil
}

// handleRecordGet answers GET /api/v1/system/audit/records/:id: one audit record, taken
// by the row identifier, which is unique over the whole table and so needs no
// tenant.
//
// The caller and the decision are the mounted route's, as on the list route.
// The handler reads the path parameter (a path that is not an identifier is a
// 400), reads the table under a cross-tenant mark (a read failure is a 503) and
// answers the record, or a bare 404 when the table holds no such row. The 404
// carries no problem document: it says a row is not there, not which refusal it
// was.
func (p *product) handleRecordGet(ctx context.Context, in RecordGetInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	caller, ok := authn.FromContext(ctx)
	if !ok {
		return nil, speedhttp.Fault{Status: http.StatusUnauthorized}
	}
	rowID, ok := parseRowID(in.ID)
	if !ok {
		return nil, badRequestFault()
	}
	if rowID == 0 {
		// A row identifier is never zero, so a path naming zero names no row.
		return speedhttp.EmptyReply(http.StatusNotFound), nil
	}
	rows, _, err := p.reader.Query(tenant.CrossTenant(ctx), journal.Query{ID: rowID, Limit: 1})
	if err != nil {
		return nil, p.answerErr("reading an audit record", err)
	}
	if len(rows) == 0 {
		return speedhttp.EmptyReply(http.StatusNotFound), nil
	}
	p.logRead(recordPath, caller, 1)
	return speedhttp.JSONReply(http.StatusOK, recordViewOf(rows[0])), nil
}

// listQueryOf turns the list route's bound input into a read query. The filters
// are the ones every query route shares; on top of them the list route takes the
// page's cursor and size. A limit that is not a non-negative integer is a
// refusal; a limit above the read surface's maximum is not refused, because the
// read surface caps it.
func listQueryOf(in RecordListInput) (journal.Query, bool) {
	q, ok := filterQueryOf(in.Since, in.Until, in.Action, in.Outcome, in.Actor)
	if !ok {
		return journal.Query{}, false
	}
	q.After = in.Cursor
	if in.Limit < 0 {
		return journal.Query{}, false
	}
	q.Limit = in.Limit
	return q, true
}

// exportQueryOf turns the export route's bound input into a read query. It
// carries the shared filters and the read surface's maximum page size: the
// export is not a caller-sized page, it reads as much as one read can return and
// continues with the cursor until the cursor is empty.
func exportQueryOf(in RecordExportInput) (journal.Query, bool) {
	q, ok := filterQueryOf(in.Since, in.Until, in.Action, in.Outcome, in.Actor)
	if !ok {
		return journal.Query{}, false
	}
	q.Limit = journal.MaxPageSize
	return q, true
}

// filterQueryOf turns the filters every query route shares into a read query. A
// time bound that is not RFC 3339 and an action, outcome or actor that names
// nothing are each a refusal.
func filterQueryOf(since, until string, actions, outcomes, actors []string) (journal.Query, bool) {
	var q journal.Query
	if since != "" {
		t, ok := parseInstant(since)
		if !ok {
			return journal.Query{}, false
		}
		q.Since = t
	}
	if until != "" {
		t, ok := parseInstant(until)
		if !ok {
			return journal.Query{}, false
		}
		q.Until = t
	}
	for _, action := range actions {
		if action == "" {
			return journal.Query{}, false
		}
		q.Actions = append(q.Actions, action)
	}
	for _, outcome := range outcomes {
		if outcome == "" {
			return journal.Query{}, false
		}
		q.Outcomes = append(q.Outcomes, outcome)
	}
	for _, actor := range actors {
		if actor == "" {
			return journal.Query{}, false
		}
		q.Actors = append(q.Actors, actor)
	}
	return q, true
}

// onlyAccepted reports whether every query parameter on the request is in the
// accepted set. A nil set accepts none, which is the get route's shape.
//
// The typed binder reads only the parameters an operation declares and silently
// ignores the rest, so this check stays in front of the handler to keep the set
// closed: a misspelled bound is reported rather than swallowed by a read that
// ignores it.
func onlyAccepted(c *gin.Context, accepted map[string]bool) bool {
	for key := range c.Request.URL.Query() {
		if !accepted[key] {
			return false
		}
	}
	return true
}

// parseInstant reads an RFC 3339 time bound. The audit module writes every row's
// time in UTC, so a parsed bound is folded to UTC before it reaches the read
// surface.
func parseInstant(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

// parseRowID reads a path parameter as a row identifier: a non-negative decimal
// integer that fits the identifier's type.
func parseRowID(s string) (uint64, bool) {
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// logRead writes the diagnostic line one read leaves. Every route of this module
// is a read and writes no audit row of its own — reading the table is not an
// action, and a row per read would grow the table with its being read — so the
// trace of a read is this line: the route, the caller and the number of rows the
// read answered.
func (p *product) logRead(route string, caller authn.Identity, hits int) {
	p.logger.Info(ModuleName+": a query was read",
		"route", route,
		"caller", caller.Key(),
		"hits", hits)
}

// answerErr maps a failure the read surface returned to the response it belongs
// to.
//
// The mapping is closed and fail-closed. A cursor that is not one the read
// surface handed out is a shape failure the handler could not see before the
// read — the cursor is opaque — so it is a 400; anything else is a 503 whose
// cause stays in the log and never reaches the caller. A failure the mapping
// does not name does not fall through as a success: it is the same 503 an
// unknown failure gets.
func (p *product) answerErr(op string, err error) error {
	if errors.Is(err, journal.ErrInvalidQuery) {
		return badRequestFault()
	}
	p.logger.Error(ModuleName+": "+op+" failed", slog.String("cause", err.Error()))
	return speedhttp.Fault{Status: http.StatusServiceUnavailable}
}
