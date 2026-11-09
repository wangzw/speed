package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"time"

	"github.com/vislake/speed/pkg/audit/journal"
	"github.com/vislake/speed/pkg/authn"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/tenant"
)

// exportMediaType is the exported file's media type: one record view per line,
// so the `detail` key-value table keeps its own structure in the file.
const exportMediaType = "application/x-ndjson"

// exportTimeLayout is the UTC, compact time layout the download name carries:
// an instant such as 2026-10-24T10:15:00Z is written 20261024T101500Z.
const exportTimeLayout = "20060102T150405Z"

// handleRecordExport answers GET /api/v1/system/audit/export: every audit record
// the filters match, written as NDJSON and streamed page by page.
//
// The caller and the decision are the mounted route's, as on the list route: the
// guard in front of this handler read the identity (401 with no identity) and
// asked for audit.read (a shortfall is the guard's own answer) before any
// parsing ran. The handler reads the filters (a shape this module cannot read is
// a 400) and reads the first page before it returns the reply, so a failure
// there is still a status code. Only then do the status and headers commit: a
// later page's failure cannot change them, and the connection is abandoned
// instead.
func (p *product) handleRecordExport(ctx context.Context, in RecordExportInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	caller, ok := authn.FromContext(ctx)
	if !ok {
		return nil, speedhttp.Fault{Status: http.StatusUnauthorized}
	}
	q, ok := exportQueryOf(in)
	if !ok {
		return nil, badRequestFault()
	}
	at := time.Now().UTC()
	readerCtx := tenant.CrossTenant(ctx)
	rows, next, err := p.reader.Query(readerCtx, q)
	if err != nil {
		return nil, p.answerErr(ctx, "reading the first page of the audit export", err)
	}
	stream := speedhttp.StreamReply(http.StatusOK, exportMediaType,
		func(ctx context.Context, w io.Writer) error {
			return p.writeExport(ctx, w, readerCtx, q, rows, next, caller)
		})
	return speedhttp.WithHeaders(stream, speedhttp.HeaderValue{
		Name:  "Content-Disposition",
		Value: exportDisposition(at),
	}), nil
}

// writeExport writes the first page, then each page the read surface's cursor
// leads to, until the cursor is empty. The export is not a snapshot: a row
// appended while it runs has a larger identifier and falls before the cursor, so
// it does not reach the file, and a row deleted while it runs is missing from
// it.
//
// A client that left ends the stream quietly: the request context is done, and
// the rows already written are on the caller's side as an incomplete file. Any
// other failure is handed back so the entry point logs it and abandons the
// connection.
func (p *product) writeExport(ctx context.Context, w io.Writer, readerCtx context.Context,
	q journal.Query, rows []journal.Row, next string, caller authn.Identity,
) error {
	written := 0
	for {
		n, err := p.writeRecordViews(ctx, w, rows)
		written += n
		if err != nil {
			return p.exportFailed(ctx, q, written, err)
		}
		flushPage(w)
		if next == "" {
			p.logExport(caller, q, written)
			return nil
		}
		pageQuery := q
		pageQuery.After = next
		rows, next, err = p.reader.Query(readerCtx, pageQuery)
		if err != nil {
			return p.exportFailed(ctx, q, written, err)
		}
	}
}

// writeRecordViews writes one record view per line and answers how many lines it
// wrote, so a failure partway through a page still counts the rows that reached
// the caller. Every line is the same view the list route answers; there is no
// envelope, no header line and no total.
func (p *product) writeRecordViews(ctx context.Context, w io.Writer, rows []journal.Row) (int, error) {
	encoder := json.NewEncoder(w)
	for i, row := range rows {
		name, nameErr := p.nameOf(ctx, row.Tenant)
		if nameErr != nil {
			return i, nameErr
		}
		if err := encoder.Encode(recordViewOf(row, name)); err != nil {
			return i, err
		}
	}
	return len(rows), nil
}

// flushPage pushes the page just written to the client, when the writer can
// flush. The reply's writer is the response writer, so each page reaches the
// caller as it is read rather than at the end of the export.
func flushPage(w io.Writer) {
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

// exportFailed ends an export whose body had already started to go out. The
// status is committed and cannot change, so the failure is only recorded: the
// line carries the filters, the rows that reached the caller and the cause. A
// client that left is not a failure — the request context is done — and the
// stream ends without a record.
func (p *product) exportFailed(ctx context.Context, q journal.Query, written int, err error) error {
	if ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return nil
	}
	p.logger.Error(ModuleName+": an audit export failed after its body had started",
		"filters", exportFilters(q),
		"rows", written,
		slog.String("cause", err.Error()))
	return err
}

// logExport writes the diagnostic line one export leaves. Like every read of
// this module, an export writes no audit row; the line carries the caller, the
// filters and the rows written.
func (p *product) logExport(caller authn.Identity, q journal.Query, written int) {
	p.logger.Info(ModuleName+": audit records were exported",
		"route", exportPath,
		"caller", caller.Key(),
		"filters", exportFilters(q),
		"rows", written)
}

// exportFilters renders the filters of an export for its diagnostic line, so a
// reader sees what the file covers without decoding the query struct. An
// absent filter is left out rather than logged as its zero value.
func exportFilters(q journal.Query) map[string]any {
	filters := map[string]any{}
	if !q.Since.IsZero() {
		filters["since"] = q.Since.Format(time.RFC3339)
	}
	if !q.Until.IsZero() {
		filters["until"] = q.Until.Format(time.RFC3339)
	}
	if len(q.Actions) > 0 {
		filters["actions"] = q.Actions
	}
	if len(q.Outcomes) > 0 {
		filters["outcomes"] = q.Outcomes
	}
	if len(q.Actors) > 0 {
		filters["actors"] = q.Actors
	}
	return filters
}

// exportFileName is the download name for an export started at at: the record
// set's name with the instant written compactly in UTC.
func exportFileName(at time.Time) string {
	return "audit-records-" + at.UTC().Format(exportTimeLayout) + ".ndjson"
}

// exportDisposition builds the Content-Disposition value with
// mime.FormatMediaType, so the filename parameter is escaped the way RFC 6266
// and RFC 5987 require rather than assembled by hand.
func exportDisposition(at time.Time) string {
	return mime.FormatMediaType("attachment", map[string]string{"filename": exportFileName(at)})
}
