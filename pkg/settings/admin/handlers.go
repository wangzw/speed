package admin

import (
	"net/http"
	"reflect"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/settings"
)

// writeBody is the request of a write: the one value to store. The value is
// decoded as the JSON the client wrote — a number, a bool, a string — and handed
// to the settings module, which encodes it under the declared type and holds it
// to the item's declared domain.
type writeBody struct {
	Value any `json:"value"`
}

// handleList answers GET /api/v1/settings/admin/settings: every declared setting item
// with the value each resolves to on the selected layer. It is a read, so it
// asks for settings.read in the layer's domain.
func (p *product) handleList(c *gin.Context) {
	if _, ok := p.caller(c); !ok {
		return
	}
	sel, ok := p.selectLayer(c)
	if !ok {
		return
	}
	if !p.require(c, authz.PermissionSettingsRead, sel.domain) {
		return
	}
	p.list(c, sel, "")
}

// handleListNamespace answers GET /api/v1/settings/admin/settings/:namespace: the
// declared items that namespace declared, each as the all-items route shows it.
// It is a read, so it asks for settings.read in the layer's domain. A namespace
// that no declared item belongs to is a 404.
func (p *product) handleListNamespace(c *gin.Context) {
	if _, ok := p.caller(c); !ok {
		return
	}
	sel, ok := p.selectLayer(c)
	if !ok {
		return
	}
	if !p.require(c, authz.PermissionSettingsRead, sel.domain) {
		return
	}
	p.list(c, sel, c.Param("namespace"))
}

// list answers a read route with the declared items, limited to namespace when
// it is given.
//
// The items are the declaration listing, which takes no IO and needs no context.
// Each item's written text and row existence come from the selected layer's rows,
// and its resolved text from a read at that layer, which walks the three-level
// fallback. A
// namespace no declared item belongs to is a 404; a failure to read the rows or
// resolve a value is a 503, because a listing that could not read a row must not
// answer as though the row were not there.
func (p *product) list(c *gin.Context, sel selection, namespace string) {
	items := p.store.Declared()
	if namespace != "" {
		group := make([]settings.DeclaredItem, 0, len(items))
		for _, item := range items {
			if item.Module == namespace {
				group = append(group, item)
			}
		}
		if len(group) == 0 {
			p.notFound(c)
			return
		}
		items = group
	}

	rows, err := p.store.List(c.Request.Context(), sel.layer)
	if err != nil {
		p.unavailable(c, "listing the settings rows", err)
		return
	}
	// The rows are keyed by their text and, separately, by their existence: an
	// empty text is a row the layer holds as much as a non-empty one, and a
	// client reads the existence to tell a written row from a fallback.
	written := make(map[string]string, len(rows))
	present := make(map[string]bool, len(rows))
	for _, row := range rows {
		written[row.Key] = row.Value
		present[row.Key] = true
	}

	views := make([]settingView, 0, len(items))
	for _, item := range items {
		value, err := p.resolve(item, sel.layer)
		if err != nil {
			p.unavailable(c, "resolving a setting", err)
			return
		}
		views = append(views, viewOf(item, written[item.Key], present[item.Key], value))
	}
	c.JSON(http.StatusOK, views)
}

// resolve reads one item at the layer and writes the resolved value in the
// declared type's text form. The read walks the three-level fallback — tenant
// row, global row, declared default — starting at the layer, and the target is a
// fresh pointer to the declared type, which is the target Get accepts.
func (p *product) resolve(item settings.DeclaredItem, layer settings.Layer) (string, error) {
	target := reflect.New(item.Type)
	if err := p.store.Get(layer, item.Key, target.Interface()); err != nil {
		return "", err
	}
	return renderValue(item.Type, target.Elem().Interface())
}

// handleSet answers PUT /api/v1/settings/admin/settings/:namespace/:key: it writes one
// row on the selected layer. It is a write, so it asks for settings.write in the
// layer's domain.
//
// The order is fixed: the layer and the body are held to their shape, the full
// key is required to be declared, the mandatory audit row lands before the
// action, and the settings module is called last. A key with no declaration is a
// 404 and a value the settings module refuses is a 422.
//
// The write's actor is the caller's accountability key, Identity.Key(), which
// under an impersonation is the administrator whose hands are on the request and
// not the account impersonated: the settings change is attributed to whoever
// made it, and the settings module records that key in the event it publishes.
func (p *product) handleSet(c *gin.Context) {
	caller, ok := p.caller(c)
	if !ok {
		return
	}
	sel, ok := p.selectLayer(c)
	if !ok {
		return
	}
	if !p.require(c, authz.PermissionSettingsWrite, sel.domain) {
		return
	}
	var body writeBody
	if !p.body(c, &body) {
		return
	}
	key := fullKey(c)
	if _, declared := p.declared(key); !declared {
		p.notFound(c)
		return
	}
	actionID := newActionID()
	if !p.auditRequired(c, mandatory(actionSet, key, detail(sel, key, actionID))) {
		return
	}
	err := p.store.Set(c.Request.Context(), settings.Write{
		Layer: sel.layer,
		Key:   key,
		Value: body.Value,
		Actor: caller.Key(),
	})
	if err != nil {
		p.auditBestEffort(c.Request.Context(), failedAudit(actionSet, key, actionID))
		p.answer(c, "writing a setting", err)
		return
	}
	p.noContent(c)
}

// handleRemove answers DELETE /api/v1/settings/admin/settings/:namespace/:key: it drops
// the selected layer's explicit row for one key. It is a write, so it asks for
// settings.write in the layer's domain.
//
// The full key is required to be declared before the action, a key with no
// declaration being a 404; the mandatory audit row lands before the settings
// module is called; and any failure the module returns is a 503, because a
// delete of a declared row has no other refusal to name.
//
// As on the write route, the actor is the caller's accountability key,
// Identity.Key() — the hands on the request, which under an impersonation is the
// administrator and not the account impersonated.
func (p *product) handleRemove(c *gin.Context) {
	caller, ok := p.caller(c)
	if !ok {
		return
	}
	sel, ok := p.selectLayer(c)
	if !ok {
		return
	}
	if !p.require(c, authz.PermissionSettingsWrite, sel.domain) {
		return
	}
	key := fullKey(c)
	if _, declared := p.declared(key); !declared {
		p.notFound(c)
		return
	}
	actionID := newActionID()
	if !p.auditRequired(c, mandatory(actionDelete, key, detail(sel, key, actionID))) {
		return
	}
	if err := p.store.Delete(c.Request.Context(), sel.layer, key, caller.Key()); err != nil {
		p.auditBestEffort(c.Request.Context(), failedAudit(actionDelete, key, actionID))
		p.unavailable(c, "deleting a setting", err)
		return
	}
	p.noContent(c)
}

// fullKey assembles the full setting key from the route's two path parameters:
// the namespace that declared the item and the rest of the key under it.
func fullKey(c *gin.Context) string {
	return c.Param("namespace") + "." + c.Param("key")
}

// declared reports the declaration of a full key, and whether one covers it. The
// listing is the declaration listing, which takes no IO.
func (p *product) declared(key string) (settings.DeclaredItem, bool) {
	for _, item := range p.store.Declared() {
		if item.Key == key {
			return item, true
		}
	}
	return settings.DeclaredItem{}, false
}

// detail builds a write's audit detail: the action identifier that links the
// write's rows, the full key, the layer by name and the tenant it names. No value
// enters it.
func detail(sel selection, key, actionID string) map[string]string {
	return map[string]string{
		detailActionID: actionID,
		detailKey:      key,
		detailLayer:    sel.name,
		detailTenant:   sel.tenant,
	}
}
