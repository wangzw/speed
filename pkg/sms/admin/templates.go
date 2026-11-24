package admin

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/sms"
)

const (
	defaultPageSize = 50
	maxPageSize     = 200
)

const (
	operationList   = "list"
	operationOffer  = "offer"
	operationRemove = "remove"

	outcomeOK = "ok"
)

// The elapsed time is the call's own, so the audit rows around it are not part of it; no body, vendor code or credential is carried.
func templateCallAttrs(operation, name string, elapsed time.Duration, outcome string) []any {
	attrs := []any{slog.String("operation", operation)}
	if name != "" {
		attrs = append(attrs, slog.String("template", name))
	}
	return append(attrs,
		slog.String("outcome", outcome),
		slog.Int64("duration_ms", elapsed.Milliseconds()))
}

func (p *product) handleTemplateList(ctx context.Context, in ListTemplatesInput) (templatePageBody, error) {
	if in.Limit < 0 {
		return templatePageBody{}, speedhttp.Fault{
			Status: http.StatusBadRequest,
			Type:   typePrefix + badRequestCode,
		}
	}
	q := listQuery{after: in.After, limit: in.Limit}
	switch {
	case q.limit == 0:
		q.limit = defaultPageSize
	case q.limit > maxPageSize:
		q.limit = maxPageSize
	}
	started := time.Now()
	records, err := p.registry.List(ctx)
	if err != nil {
		p.logger.Error(ModuleName+": listing the templates failed",
			append(templateCallAttrs(operationList, "", time.Since(started), outcomeFailed), slog.String("cause", err.Error()))...)
		return templatePageBody{}, speedhttp.Fault{Status: http.StatusServiceUnavailable}
	}
	p.logger.Info(ModuleName+": the templates were listed",
		append(templateCallAttrs(operationList, "", time.Since(started), outcomeOK), slog.Int("count", len(records)))...)
	page, next := paginate(records, q)
	views := make([]SmsAdminTemplate, len(page))
	for i, r := range page {
		views[i] = templateViewOf(r)
	}
	return templatePageBody{Templates: views, NextCursor: next}, nil
}

type listQuery struct {
	after string
	limit int
}

func paginate(records []sms.Record, q listQuery) ([]sms.Record, string) {
	ordered := append([]sms.Record(nil), records...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Name < ordered[j].Name })

	start := 0
	if q.after != "" {
		start = sort.Search(len(ordered), func(i int) bool { return ordered[i].Name > q.after })
	}
	page := ordered[start:]
	if len(page) > q.limit {
		page = page[:q.limit]
	}
	next := ""
	if start+len(page) < len(ordered) && len(page) > 0 {
		next = page[len(page)-1].Name
	}
	return page, next
}

func (p *product) handleTemplateRegister(ctx context.Context, in registerBody, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, speedhttp.Fault{Status: http.StatusBadRequest, Type: typePrefix + badRequestCode}
	}
	actionID := newActionID()
	if err := p.auditRequired(ctx, mandatory(actionTemplateRegister, in.Name, map[string]string{
		detailActionID: actionID,
		detailTemplate: in.Name,
	})); err != nil {
		return nil, err
	}
	record, err := p.registry.Register(ctx, in.Name, sms.TemplateSpec{
		Kind:       sms.Kind(in.Kind),
		Params:     in.Params,
		Codes:      in.Codes,
		Bodies:     in.Bodies,
		Attributes: in.Attributes,
	})
	if err != nil {
		p.auditBestEffort(ctx, failedAudit(actionTemplateRegister, in.Name, actionID))
		return p.answer("registering a template", err)
	}
	return speedhttp.JSONReply(http.StatusCreated, registeredTemplateBody{Template: templateViewOf(record)}), nil
}

func (p *product) handleTemplateOffer(ctx context.Context, in offerBody, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, speedhttp.Fault{Status: http.StatusBadRequest, Type: typePrefix + badRequestCode}
	}
	submission := sms.Submission{
		Name:       in.Name,
		Content:    in.Content,
		Kind:       sms.Kind(in.Kind),
		Locale:     in.Locale,
		Params:     in.Params,
		Attributes: in.Attributes,
		Remark:     in.Remark,
	}
	actionID := newActionID()
	if err := p.auditRequired(ctx, mandatory(actionTemplateOffer, in.Name, map[string]string{
		detailActionID: actionID,
		detailTemplate: in.Name,
	})); err != nil {
		return nil, err
	}
	started := time.Now()
	record, err := p.registry.Offer(ctx, submission)
	elapsed := time.Since(started)
	if err != nil {
		p.auditBestEffort(ctx, failedAudit(actionTemplateOffer, in.Name, actionID))
		return p.answer("offering a template", err,
			templateCallAttrs(operationOffer, in.Name, elapsed, outcomeFailed)...)
	}
	p.logger.Info(ModuleName+": a template was offered",
		append(templateCallAttrs(operationOffer, in.Name, elapsed, outcomeOK),
			slog.String("carrier", record.Carrier))...)
	return speedhttp.JSONReply(http.StatusCreated, offeredTemplateBody{Template: templateViewOf(record)}), nil
}

func (p *product) handleTemplateSetEnabled(ctx context.Context, in setEnabledInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	if in.Enabled == nil {
		return nil, speedhttp.Fault{Status: http.StatusBadRequest, Type: typePrefix + badRequestCode}
	}
	actionID := newActionID()
	if err := p.auditRequired(ctx, mandatory(actionTemplateEnable, in.Name, map[string]string{
		detailActionID: actionID,
		detailTemplate: in.Name,
		detailEnabled:  strconv.FormatBool(*in.Enabled),
	})); err != nil {
		return nil, err
	}
	if err := p.registry.SetEnabled(ctx, in.Name, *in.Enabled); err != nil {
		p.auditBestEffort(ctx, failedAudit(actionTemplateEnable, in.Name, actionID))
		return p.answer("setting a template's enabled state", err)
	}
	return speedhttp.EmptyReply(http.StatusNoContent), nil
}

func (p *product) handleTemplateRemove(ctx context.Context, in templateNameInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	actionID := newActionID()
	if err := p.auditRequired(ctx, mandatory(actionTemplateRemove, in.Name, map[string]string{
		detailActionID: actionID,
		detailTemplate: in.Name,
	})); err != nil {
		return nil, err
	}
	started := time.Now()
	err := p.registry.Remove(ctx, in.Name)
	elapsed := time.Since(started)
	if err != nil {
		p.auditBestEffort(ctx, failedAudit(actionTemplateRemove, in.Name, actionID))
		return p.answer("removing a template", err,
			templateCallAttrs(operationRemove, in.Name, elapsed, outcomeFailed)...)
	}
	p.logger.Info(ModuleName+": a template was removed",
		templateCallAttrs(operationRemove, in.Name, elapsed, outcomeOK)...)
	return speedhttp.EmptyReply(http.StatusNoContent), nil
}
