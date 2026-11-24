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

const exportMediaType = "application/x-ndjson"

const exportTimeLayout = "20060102T150405Z"

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

func flushPage(w io.Writer) {
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

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

func (p *product) logExport(caller authn.Identity, q journal.Query, written int) {
	p.logger.Info(ModuleName+": audit records were exported",
		"route", exportPath,
		"caller", caller.Key(),
		"filters", exportFilters(q),
		"rows", written)
}

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

func exportFileName(at time.Time) string {
	return "audit-records-" + at.UTC().Format(exportTimeLayout) + ".ndjson"
}

// exportDisposition builds the Content-Disposition value with
// mime.FormatMediaType, so the filename parameter is escaped the way RFC 6266
// and RFC 5987 require rather than assembled by hand.
func exportDisposition(at time.Time) string {
	return mime.FormatMediaType("attachment", map[string]string{"filename": exportFileName(at)})
}
