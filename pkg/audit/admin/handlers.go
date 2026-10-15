package admin

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/audit/journal"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/tenant"
)

// The list route's query parameters, a closed set. A name not among them is a
// shape the module refuses, so that a misspelled bound is reported rather than
// swallowed by a read that ignores it.
const (
	paramSince  = "since"
	paramUntil  = "until"
	paramAction = "action"
	paramCursor = "cursor"
	paramLimit  = "limit"
)

// listParams is the set the list route accepts.
var listParams = map[string]bool{
	paramSince:  true,
	paramUntil:  true,
	paramAction: true,
	paramCursor: true,
	paramLimit:  true,
}

// handleRecordList answers GET /audit/admin/records: one page of audit records,
// narrowed by a time window and a set of actions, read newest first.
//
// The order is fixed: read the caller (401 with no identity) → ask the guard for
// audit.read (a shortfall is the guard's own answer) → read the query parameters
// and check their shape (a shape this module cannot read is a 400) → read the
// table under a cross-tenant mark (a read failure is a 503) → answer a page.
func (p *product) handleRecordList(c *gin.Context) {
	caller, ok := p.caller(c)
	if !ok || !p.require(c) {
		return
	}
	q, ok := p.listQuery(c)
	if !ok {
		return
	}
	rows, next, err := p.reader.Query(tenant.CrossTenant(c.Request.Context()), q)
	if err != nil {
		p.answer(c, "querying audit records", err)
		return
	}
	views := make([]recordView, len(rows))
	for i, r := range rows {
		views[i] = recordViewOf(r)
	}
	p.logRead(c, caller, len(rows))
	c.JSON(http.StatusOK, recordPage{Entries: views, Next: next})
}

// handleRecordGet answers GET /audit/admin/records/:id: one audit record, taken
// by the row identifier, which is unique over the whole table and so needs no
// tenant.
//
// The order is the list route's, with the path parameter parsed in place of the
// query parameters: read the caller → ask the guard → read the path parameter
// (a path that is not an identifier is a 400) → read the table under a
// cross-tenant mark (a read failure is a 503) → answer the record, or a 404 when
// the table holds no such row.
func (p *product) handleRecordGet(c *gin.Context) {
	caller, ok := p.caller(c)
	if !ok || !p.require(c) {
		return
	}
	// This route takes no query parameter; any is a shape the module refuses.
	if !onlyAccepted(c, nil) {
		p.bad(c)
		return
	}
	rowID, ok := parseRowID(c.Param("id"))
	if !ok {
		p.bad(c)
		return
	}
	if rowID == 0 {
		// A row identifier is never zero, so a path naming zero names no row.
		p.notFound(c)
		return
	}
	rows, _, err := p.reader.Query(tenant.CrossTenant(c.Request.Context()), journal.Query{ID: rowID, Limit: 1})
	if err != nil {
		p.answer(c, "reading an audit record", err)
		return
	}
	if len(rows) == 0 {
		p.notFound(c)
		return
	}
	p.logRead(c, caller, 1)
	c.JSON(http.StatusOK, recordViewOf(rows[0]))
}

// listQuery reads the list route's query parameters into a read query. A
// parameter the route does not accept, a time bound that is not RFC 3339, an
// action that names nothing, and a limit that is not a non-negative integer are
// each a 400. A limit above the read surface's maximum is not refused: the read
// surface caps it.
func (p *product) listQuery(c *gin.Context) (journal.Query, bool) {
	if !onlyAccepted(c, listParams) {
		p.bad(c)
		return journal.Query{}, false
	}
	var q journal.Query
	if s := c.Query(paramSince); s != "" {
		t, ok := parseInstant(s)
		if !ok {
			p.bad(c)
			return journal.Query{}, false
		}
		q.Since = t
	}
	if s := c.Query(paramUntil); s != "" {
		t, ok := parseInstant(s)
		if !ok {
			p.bad(c)
			return journal.Query{}, false
		}
		q.Until = t
	}
	for _, action := range c.QueryArray(paramAction) {
		if action == "" {
			p.bad(c)
			return journal.Query{}, false
		}
		q.Actions = append(q.Actions, action)
	}
	q.After = c.Query(paramCursor)
	if s := c.Query(paramLimit); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			p.bad(c)
			return journal.Query{}, false
		}
		q.Limit = n
	}
	return q, true
}

// onlyAccepted reports whether every query parameter on the request is in the
// accepted set. A nil set accepts none, which is the get route's shape.
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
func (p *product) logRead(c *gin.Context, caller authn.Identity, hits int) {
	p.logger.Info(ModuleName+": a query was read",
		"route", c.FullPath(),
		"caller", caller.Key(),
		"hits", hits)
}
