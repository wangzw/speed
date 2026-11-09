package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"

	speedhttp "github.com/vislake/speed/pkg/http"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/settings"
)

// jsonContentType is the media type the entry point's JSON writer sets. A reply
// operation's JSON answer is written through c.Data, which carries the declared
// media type as it stands, so the one declared without the charset parameter
// would reach the wire without it; this is the media type the simple form's
// reader and the reply form's writer share.
const jsonContentType = "application/json; charset=utf-8"

// replyJSON builds a 200 answer carrying body as JSON, with the media type the
// entry point's own JSON writer produces. It is the answer of a reply operation
// that also has to write a bare 404, which the simple form cannot.
func replyJSON(body any) (speedhttp.Reply, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return speedhttp.BytesReply(http.StatusOK, jsonContentType, encoded), nil
}

// handleList answers GET /settings: every declared setting item
// with the value each resolves to on the selected layer. The route's
// authorization step has already settled the layer and decided the read.
func (p *product) handleList(ctx context.Context, _ listInput) ([]SettingsAdminSetting, error) {
	sel, _ := selectionFrom(ctx)
	return p.list(ctx, sel, "")
}

// handleListNamespace answers GET /settings/:namespace: the
// declared items that namespace declared, each as the all-items route shows it.
// A namespace that no declared item belongs to is a bare 404.
func (p *product) handleListNamespace(ctx context.Context, in namespaceInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	sel, _ := selectionFrom(ctx)
	views, err := p.list(ctx, sel, in.Namespace)
	if errors.Is(err, errNoDeclaredItem) {
		return speedhttp.EmptyReply(http.StatusNotFound), nil
	}
	if err != nil {
		return nil, err
	}
	return replyJSON(views)
}

// list answers a read route with the declared items, limited to namespace when
// it is given.
//
// The items are the declaration listing, which takes no IO and needs no context.
// Each item's written text and row existence come from the selected layer's rows,
// and its resolved text from a read at that layer, which walks the three-level
// fallback. A namespace no declared item belongs to is a bare 404, reported as
// errNoDeclaredItem; a failure to read the rows or resolve a value is a 503,
// because a listing that could not read a row must not answer as though the row
// were not there.
func (p *product) list(ctx context.Context, sel selection, namespace string) ([]SettingsAdminSetting, error) {
	items := p.store.Declared()
	if namespace != "" {
		group := make([]settings.DeclaredItem, 0, len(items))
		for _, item := range items {
			if item.Module == namespace {
				group = append(group, item)
			}
		}
		if len(group) == 0 {
			return nil, errNoDeclaredItem
		}
		items = group
	}

	rows, err := p.store.List(ctx, sel.layer)
	if err != nil {
		return nil, p.unavailable("listing the settings rows", err)
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

	views := make([]SettingsAdminSetting, 0, len(items))
	for _, item := range items {
		value, err := p.resolve(item, sel.layer)
		if err != nil {
			return nil, p.unavailable("resolving a setting", err)
		}
		views = append(views, viewOf(item, written[item.Key], present[item.Key], value))
	}
	return views, nil
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

// handleSet answers PUT /settings/:namespace/:key: it writes one
// row on the selected layer. The route's authorization step has already settled
// the layer and decided the write, and the entry point has already bound the
// body.
//
// The order is fixed: the full key is required to be declared, a masked value
// is refused, the mandatory audit row lands before the action, and the settings
// module is called last. A key with no declaration is a bare 404, a masked value
// written back to a sensitive item is a 422 masked-value before the audit and
// the store, and a value the settings module refuses is a 422.
//
// The write's actor is the caller's accountability key, Identity.Key(), which
// under an impersonation is the administrator whose hands are on the request and
// not the account impersonated: the settings change is attributed to whoever
// made it, and the settings module records that key in the event it publishes.
func (p *product) handleSet(ctx context.Context, in setInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	caller, _ := authn.FromContext(ctx)
	sel, _ := selectionFrom(ctx)
	key := fullKeyOf(in.Namespace, in.Key)
	item, declared := p.declared(key)
	if !declared {
		return speedhttp.EmptyReply(http.StatusNotFound), nil
	}
	if item.Sensitive {
		if text, isText := in.Value.(string); isText && text == maskedValue {
			return nil, p.maskedValue()
		}
	}
	actionID := newActionID()
	if err := p.auditRequired(ctx, mandatory(actionSet, key, detail(sel, key, actionID))); err != nil {
		return nil, err
	}
	err := p.store.Set(ctx, settings.Write{
		Layer: sel.layer,
		Key:   key,
		Value: in.Value,
		Actor: caller.Key(),
	})
	if err != nil {
		p.auditBestEffort(ctx, failedAudit(actionSet, key, actionID))
		if errors.Is(err, settings.ErrUnknownKey) {
			return speedhttp.EmptyReply(http.StatusNotFound), nil
		}
		return nil, p.answer("writing a setting", err)
	}
	return speedhttp.EmptyReply(http.StatusNoContent), nil
}

// handleRemove answers DELETE /settings/:namespace/:key: it drops
// the selected layer's explicit row for one key. The route's authorization step
// has already settled the layer and decided the write.
//
// The full key is required to be declared before the action, a key with no
// declaration being a bare 404; the mandatory audit row lands before the
// settings module is called; and any failure the module returns is a 503,
// because a delete of a declared row has no other refusal to name.
//
// As on the write route, the actor is the caller's accountability key,
// Identity.Key() — the hands on the request, which under an impersonation is the
// administrator and not the account impersonated.
func (p *product) handleRemove(ctx context.Context, in removeInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	caller, _ := authn.FromContext(ctx)
	sel, _ := selectionFrom(ctx)
	key := fullKeyOf(in.Namespace, in.Key)
	if _, declared := p.declared(key); !declared {
		return speedhttp.EmptyReply(http.StatusNotFound), nil
	}
	actionID := newActionID()
	if err := p.auditRequired(ctx, mandatory(actionDelete, key, detail(sel, key, actionID))); err != nil {
		return nil, err
	}
	if err := p.store.Delete(ctx, sel.layer, key, caller.Key()); err != nil {
		p.auditBestEffort(ctx, failedAudit(actionDelete, key, actionID))
		return nil, p.unavailable("deleting a setting", err)
	}
	return speedhttp.EmptyReply(http.StatusNoContent), nil
}

// fullKeyOf assembles the full setting key from the route's two path
// parameters: the namespace that declared the item and the rest of the key
// under it.
func fullKeyOf(namespace, key string) string {
	return namespace + "." + key
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
// write's rows, the full key, the layer name and the tenant name. No value
// enters it.
func detail(sel selection, key, actionID string) map[string]string {
	return map[string]string{
		detailActionID: actionID,
		detailKey:      key,
		detailLayer:    sel.name,
		detailTenant:   sel.tenantName,
	}
}
