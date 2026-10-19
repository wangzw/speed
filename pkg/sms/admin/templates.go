package admin

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"

	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/sms"
)

// The page a list answers with when the request names no limit, and the largest
// page it will answer with when the request names one. A limit of zero reads as
// the default: an empty page is never what a caller wants from a listing, and a
// cursor names where to continue from.
const (
	defaultPageSize = 50
	maxPageSize     = 200
)

// handleTemplateList answers GET /sms/admin/templates: one page of the template
// set, configured templates included. It is a read, so the action is
// template.read. The page is ordered by template name and the cursor is the last
// name on it, so a caller continues from where the previous page ended.
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
	records, err := p.registry.List(ctx)
	if err != nil {
		p.logger.Error(ModuleName+": listing the templates failed", "cause", err.Error())
		return templatePageBody{}, speedhttp.Fault{Status: http.StatusServiceUnavailable}
	}
	page, next := paginate(records, q)
	views := make([]SmsAdminTemplate, len(page))
	for i, r := range page {
		views[i] = templateViewOf(r)
	}
	return templatePageBody{Templates: views, NextCursor: next}, nil
}

// listQuery is the page read once its limit has been resolved: the name to
// continue after and the page size.
type listQuery struct {
	after string
	limit int
}

// paginate takes one page of records, ordered by template name, and the cursor
// to continue from or an empty string when the page is the last. The records are
// sorted here rather than trusted to the registry's own order, so the cursor —
// the name to continue after — is well defined whatever order the registry
// answered in.
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

// handleTemplateRegister answers POST /sms/admin/templates: it takes up a
// template whose vendor code the operator already has. It is a write, so the
// action is template.manage. The mandatory audit row lands before the registry
// is called; a name the registry already holds is a 409, and a template set the
// registry cannot run on is a 400.
//
// An empty name is refused on shape before the registry is called: the route
// addresses a template by its name, and the empty name names no row.
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
		Params: in.Params,
		Codes:  in.Codes,
	})
	if err != nil {
		p.auditBestEffort(ctx, failedAudit(actionTemplateRegister, in.Name, actionID))
		return p.answer("registering a template", err)
	}
	return speedhttp.JSONReply(http.StatusCreated, registeredTemplateBody{Template: templateViewOf(record)}), nil
}

// handleTemplateOffer answers POST /sms/admin/templates/submissions: it hands a
// template to the vendor for review and takes up the code the vendor answers
// with. It is a write, so the action is template.manage. The mandatory audit row
// lands before the registry is called; a submission the vendor or the registry
// refuses is mapped to its answer.
//
// An empty name is refused on shape before the registry is called, the same as
// the registration: the submission names the template it creates.
func (p *product) handleTemplateOffer(ctx context.Context, in offerBody, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, speedhttp.Fault{Status: http.StatusBadRequest, Type: typePrefix + badRequestCode}
	}
	submission := sms.Submission{
		Name:    in.Name,
		Content: in.Content,
		Kind:    sms.Kind(in.Kind),
		Locale:  in.Locale,
		Params:  in.Params,
		Remark:  in.Remark,
	}
	actionID := newActionID()
	if err := p.auditRequired(ctx, mandatory(actionTemplateOffer, in.Name, map[string]string{
		detailActionID: actionID,
		detailTemplate: in.Name,
	})); err != nil {
		return nil, err
	}
	record, err := p.registry.Offer(ctx, submission)
	if err != nil {
		p.auditBestEffort(ctx, failedAudit(actionTemplateOffer, in.Name, actionID))
		return p.answer("offering a template", err)
	}
	return speedhttp.JSONReply(http.StatusCreated, offeredTemplateBody{Template: templateViewOf(record)}), nil
}

// handleTemplateSetEnabled answers POST /sms/admin/templates/:name/enabled: it
// turns a message's ability to go out under a runtime template on or off. It is
// a write, so the action is template.manage. The mandatory audit row lands
// before the registry is called; a template that came from the configuration is
// a 409 naming its source, and a name the registry does not hold is a 404.
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

// handleTemplateRemove answers DELETE /sms/admin/templates/:name: it drops a
// runtime template from this process, at the vendor left alone. It is a write,
// so the action is template.manage. The mandatory audit row lands before the
// registry is called; a template that came from the configuration is a 409
// naming its source, and a name the registry does not hold is a 404.
func (p *product) handleTemplateRemove(ctx context.Context, in templateNameInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	actionID := newActionID()
	if err := p.auditRequired(ctx, mandatory(actionTemplateRemove, in.Name, map[string]string{
		detailActionID: actionID,
		detailTemplate: in.Name,
	})); err != nil {
		return nil, err
	}
	if err := p.registry.Remove(ctx, in.Name); err != nil {
		p.auditBestEffort(ctx, failedAudit(actionTemplateRemove, in.Name, actionID))
		return p.answer("removing a template", err)
	}
	return speedhttp.EmptyReply(http.StatusNoContent), nil
}
