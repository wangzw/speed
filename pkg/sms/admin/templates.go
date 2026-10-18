package admin

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

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

// handleTemplateList answers GET /api/v1/sms/admin/templates: one page of the template
// set, configured templates included. It is a read, so the action is
// template.read. The page is ordered by template name and the cursor is the last
// name on it, so a caller continues from where the previous page ended.
func (p *product) handleTemplateList(c *gin.Context) {
	if _, ok := p.caller(c); !ok {
		return
	}
	if !p.require(c, PermissionTemplateRead) {
		return
	}
	q, ok := p.listQuery(c)
	if !ok {
		return
	}
	records, err := p.registry.List(c.Request.Context())
	if err != nil {
		p.answer(c, "listing the templates", err)
		return
	}
	page, next := paginate(records, q)
	views := make([]templateView, len(page))
	for i, r := range page {
		views[i] = templateViewOf(r)
	}
	c.JSON(http.StatusOK, gin.H{"templates": views, "next_cursor": next})
}

// listQuery reads the list's query parameters. The cursor is the name to
// continue after; the limit is a count, and a value that is not a non-negative
// integer is a 400 before the registry is read.
type listQuery struct {
	after string
	limit int
}

func (p *product) listQuery(c *gin.Context) (listQuery, bool) {
	q := listQuery{after: c.Query("after"), limit: defaultPageSize}
	if s := c.Query("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			p.bad(c)
			return listQuery{}, false
		}
		switch {
		case n == 0:
			q.limit = defaultPageSize
		case n > maxPageSize:
			q.limit = maxPageSize
		default:
			q.limit = n
		}
	}
	return q, true
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

// registerBody is the request of a registration: the template's logical name,
// the parameter names it substitutes and the carrier code for each language.
type registerBody struct {
	Name   string            `json:"name"`
	Params []string          `json:"params"`
	Codes  map[string]string `json:"codes"`
}

// handleTemplateRegister answers POST /api/v1/sms/admin/templates: it takes up a
// template whose vendor code the operator already has. It is a write, so the
// action is template.manage. The mandatory audit row lands before the registry
// is called; a name the registry already holds is a 409, and a template set the
// registry cannot run on is a 400.
//
// An empty name is refused on shape before the registry is called: the route
// addresses a template by its name, and the empty name names no row.
func (p *product) handleTemplateRegister(c *gin.Context) {
	if _, ok := p.caller(c); !ok {
		return
	}
	if !p.require(c, PermissionTemplateManage) {
		return
	}
	var body registerBody
	if !p.body(c, &body) {
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		p.bad(c)
		return
	}
	actionID := newActionID()
	if !p.auditRequired(c, mandatory(actionTemplateRegister, body.Name, map[string]string{
		detailActionID: actionID,
		detailTemplate: body.Name,
	})) {
		return
	}
	record, err := p.registry.Register(c.Request.Context(), body.Name, sms.TemplateSpec{
		Params: body.Params,
		Codes:  body.Codes,
	})
	if err != nil {
		p.auditBestEffort(c.Request.Context(), failedAudit(actionTemplateRegister, body.Name, actionID))
		p.answer(c, "registering a template", err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"template": templateViewOf(record)})
}

// offerBody is the request of a submission: the fields of a sms.Submission.
type offerBody struct {
	Name    string   `json:"name"`
	Content string   `json:"content"`
	Kind    string   `json:"kind"`
	Locale  string   `json:"locale"`
	Params  []string `json:"params"`
	Remark  string   `json:"remark"`
}

// handleTemplateOffer answers POST /api/v1/sms/admin/templates/submissions: it hands a
// template to the vendor for review and takes up the code the vendor answers
// with. It is a write, so the action is template.manage. The mandatory audit row
// lands before the registry is called; a submission the vendor or the registry
// refuses is mapped to its answer.
//
// An empty name is refused on shape before the registry is called, the same as
// the registration: the submission names the template it creates.
func (p *product) handleTemplateOffer(c *gin.Context) {
	if _, ok := p.caller(c); !ok {
		return
	}
	if !p.require(c, PermissionTemplateManage) {
		return
	}
	var body offerBody
	if !p.body(c, &body) {
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		p.bad(c)
		return
	}
	submission := sms.Submission{
		Name:    body.Name,
		Content: body.Content,
		Kind:    sms.Kind(body.Kind),
		Locale:  body.Locale,
		Params:  body.Params,
		Remark:  body.Remark,
	}
	actionID := newActionID()
	if !p.auditRequired(c, mandatory(actionTemplateOffer, body.Name, map[string]string{
		detailActionID: actionID,
		detailTemplate: body.Name,
	})) {
		return
	}
	record, err := p.registry.Offer(c.Request.Context(), submission)
	if err != nil {
		p.auditBestEffort(c.Request.Context(), failedAudit(actionTemplateOffer, body.Name, actionID))
		p.answer(c, "offering a template", err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"template": templateViewOf(record)})
}

// enabledBody is the request of an enable or a disable. The target state is a
// pointer so that a body naming none is told apart from one naming false: the
// route changes a template's state, and a body that names no state is a request
// this module refuses rather than a disable by omission.
type enabledBody struct {
	Enabled *bool `json:"enabled"`
}

// handleTemplateSetEnabled answers POST /api/v1/sms/admin/templates/:name/enabled: it
// turns a message's ability to go out under a runtime template on or off. It is
// a write, so the action is template.manage. The mandatory audit row lands before
// the registry is called; a template that came from the configuration is a 409
// naming its source, and a name the registry does not hold is a 404.
func (p *product) handleTemplateSetEnabled(c *gin.Context) {
	if _, ok := p.caller(c); !ok {
		return
	}
	if !p.require(c, PermissionTemplateManage) {
		return
	}
	name := c.Param("name")
	var body enabledBody
	if !p.body(c, &body) {
		return
	}
	if body.Enabled == nil {
		p.bad(c)
		return
	}
	actionID := newActionID()
	if !p.auditRequired(c, mandatory(actionTemplateEnable, name, map[string]string{
		detailActionID: actionID,
		detailTemplate: name,
		detailEnabled:  strconv.FormatBool(*body.Enabled),
	})) {
		return
	}
	if err := p.registry.SetEnabled(c.Request.Context(), name, *body.Enabled); err != nil {
		p.auditBestEffort(c.Request.Context(), failedAudit(actionTemplateEnable, name, actionID))
		p.answer(c, "setting a template's enabled state", err)
		return
	}
	p.noContent(c)
}

// handleTemplateRemove answers DELETE /api/v1/sms/admin/templates/:name: it drops a
// runtime template from this process, at the vendor left alone. It is a write, so
// the action is template.manage. The mandatory audit row lands before the
// registry is called; a template that came from the configuration is a 409 naming
// its source, and a name the registry does not hold is a 404.
func (p *product) handleTemplateRemove(c *gin.Context) {
	if _, ok := p.caller(c); !ok {
		return
	}
	if !p.require(c, PermissionTemplateManage) {
		return
	}
	name := c.Param("name")
	actionID := newActionID()
	if !p.auditRequired(c, mandatory(actionTemplateRemove, name, map[string]string{
		detailActionID: actionID,
		detailTemplate: name,
	})) {
		return
	}
	if err := p.registry.Remove(c.Request.Context(), name); err != nil {
		p.auditBestEffort(c.Request.Context(), failedAudit(actionTemplateRemove, name, actionID))
		p.answer(c, "removing a template", err)
		return
	}
	p.noContent(c)
}
