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

var listParams = map[string]bool{
	paramSince:   true,
	paramUntil:   true,
	paramAction:  true,
	paramOutcome: true,
	paramActor:   true,
	paramCursor:  true,
	paramLimit:   true,
}

var exportParams = map[string]bool{
	paramSince:   true,
	paramUntil:   true,
	paramAction:  true,
	paramOutcome: true,
	paramActor:   true,
}

// handleRecordList answers GET /api/v1/system/audit/records: one page of
// audit records, narrowed by a time window and sets of actions, outcomes
// and actors, read newest first.
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
		return AuditAdminRecordPage{}, p.answerErr(ctx, "querying audit records", err)
	}
	entries := make([]AuditAdminRecord, len(rows))
	for i, r := range rows {
		name, nameErr := p.nameOf(ctx, r.Tenant)
		if nameErr != nil {
			return AuditAdminRecordPage{}, p.answerErr(ctx, "reading a tenant's name", nameErr)
		}
		entries[i] = recordViewOf(r, name)
	}
	p.logRead(recordsPath, caller, len(rows))
	return AuditAdminRecordPage{Entries: entries, Next: next}, nil
}

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
		return nil, p.answerErr(ctx, "reading an audit record", err)
	}
	if len(rows) == 0 {
		return speedhttp.EmptyReply(http.StatusNotFound), nil
	}
	p.logRead(recordPath, caller, 1)
	name, nameErr := p.nameOf(ctx, rows[0].Tenant)
	if nameErr != nil {
		return nil, p.answerErr(ctx, "reading a tenant's name", nameErr)
	}
	return speedhttp.JSONReply(http.StatusOK, recordViewOf(rows[0], name)), nil
}

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

func exportQueryOf(in RecordExportInput) (journal.Query, bool) {
	q, ok := filterQueryOf(in.Since, in.Until, in.Action, in.Outcome, in.Actor)
	if !ok {
		return journal.Query{}, false
	}
	q.Limit = journal.MaxPageSize
	return q, true
}

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

func parseRowID(s string) (uint64, bool) {
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

func (p *product) logRead(route string, caller authn.Identity, hits int) {
	p.logger.Info(ModuleName+": a query was read",
		"route", route,
		"caller", caller.Key(),
		"hits", hits)
}

func (p *product) answerErr(ctx context.Context, op string, err error) error {
	if errors.Is(err, journal.ErrInvalidQuery) {
		return badRequestFault()
	}
	if ctx.Err() == nil && !errors.Is(err, context.Canceled) {
		p.logger.Error(ModuleName+": "+op+" failed", slog.String("cause", err.Error()))
	}
	return speedhttp.Fault{Status: http.StatusServiceUnavailable}
}
