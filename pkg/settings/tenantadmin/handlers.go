package tenantadmin

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

// handleList answers GET /{tenant}/settings: every declared setting item, minus
// the platform-only namespaces, with the value each resolves to on the path
// tenant's layer. The route's authorization step has already decided the read.
func (p *product) handleList(ctx context.Context, in listInput) ([]SettingsTenantAdminSetting, error) {
	tenant, ok := p.pathTenant(in.Tenant)
	if !ok {
		return nil, p.badRequest()
	}
	return p.list(ctx, tenant, "")
}

// handleListNamespace answers GET /{tenant}/settings/:namespace: the declared
// items that namespace declared, each as the all-items route shows it. A
// namespace that no declared item belongs to, or whose items are all
// platform-only, is a bare 404.
func (p *product) handleListNamespace(ctx context.Context, in namespaceInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	tenant, ok := p.pathTenant(in.Tenant)
	if !ok {
		return nil, p.badRequest()
	}
	views, err := p.list(ctx, tenant, in.Namespace)
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
// The items are the declaration listing, which takes no IO and needs no context;
// the platform-only namespaces are dropped from it first. Each item's written
// text and row existence come from the path tenant's rows, its resolved text from
// a read at that layer, which walks the three-level fallback, and its source from
// the tenant layer's rows and the global layer's rows: the tenant row when it is
// there, else the global row, else the declared default. A namespace no declared
// item belongs to is a bare 404, reported as errNoDeclaredItem; a failure to read
// the rows or resolve a value is a 503, because a listing that could not read a
// row must not answer as though the row were not there.
func (p *product) list(ctx context.Context, tenant selection, namespace string) ([]SettingsTenantAdminSetting, error) {
	declared := p.store.Declared()
	items := make([]settings.DeclaredItem, 0, len(declared))
	for _, item := range declared {
		if platformOnly(item.Module) {
			continue
		}
		items = append(items, item)
	}
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

	rows, err := p.store.List(ctx, tenant.layer)
	if err != nil {
		return nil, p.unavailable("listing the settings rows", err)
	}
	// The layer's rows are keyed by their text and, separately, by their
	// existence: an empty text is a row the layer holds as much as a non-empty
	// one, and a client reads the existence to tell a written row from a
	// fallback.
	written := make(map[string]string, len(rows))
	present := make(map[string]bool, len(rows))
	for _, row := range rows {
		written[row.Key] = row.Value
		present[row.Key] = true
	}

	// The global layer is read only for the source marker: this surface does not
	// expose the global layer's values and accepts no write to it, but a value
	// that falls through to the global row has to say so.
	globalRows, err := p.store.List(ctx, settings.Global())
	if err != nil {
		return nil, p.unavailable("listing the global settings rows", err)
	}
	globalPresent := make(map[string]bool, len(globalRows))
	for _, row := range globalRows {
		globalPresent[row.Key] = true
	}

	views := make([]SettingsTenantAdminSetting, 0, len(items))
	for _, item := range items {
		value, err := p.resolve(item, tenant.layer)
		if err != nil {
			return nil, p.unavailable("resolving a setting", err)
		}
		source := sourceDefault
		switch {
		case present[item.Key]:
			source = sourceTenant
		case globalPresent[item.Key]:
			source = sourceGlobal
		}
		views = append(views, viewOf(item, written[item.Key], present[item.Key], value, source))
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

// handleSet answers PUT /{tenant}/settings/:namespace/:key: it writes one row on
// the path tenant's layer. The route's authorization step has already decided
// the write, and the entry point has already bound the body.
//
// The order is fixed: the tenant and the full key are checked, the host's
// settings write seam is asked, the mandatory audit row lands before the action,
// and the settings module is called last. A key with no declaration, or one in a
// platform-only namespace, is a bare 404; a seam refusal is 403 protected and an
// unanswered seam is 503; a value the settings module refuses is a 422.
//
// The write's actor is the caller's accountability key, Identity.Key(), which
// under an impersonation is the administrator whose hands are on the request and
// not the account impersonated: the settings change is attributed to whoever
// made it, and the settings module records that key in the event it publishes.
func (p *product) handleSet(ctx context.Context, in setInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	tenant, ok := p.pathTenant(in.Tenant)
	if !ok {
		return nil, p.badRequest()
	}
	key := fullKeyOf(in.Namespace, in.Key)
	if !p.writable(in.Namespace, key) {
		return speedhttp.EmptyReply(http.StatusNotFound), nil
	}
	denied, err := p.writeSettings.Denied(ctx, tenant.tenant)
	if err != nil {
		return nil, p.unavailable("asking the settings write seam", err)
	}
	if denied {
		return nil, p.protected()
	}
	caller, _ := authn.FromContext(ctx)
	actionID := newActionID()
	if err = p.auditRequired(ctx, mandatory(actionSet, key, detail(tenant.tenant, key, actionID))); err != nil {
		return nil, err
	}
	err = p.store.Set(ctx, settings.Write{
		Layer: tenant.layer,
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

// handleRemove answers DELETE /{tenant}/settings/:namespace/:key: it drops the
// path tenant layer's explicit row for one key. The route's authorization step
// has already decided the write.
//
// The full key is required to be declared and outside the platform-only
// namespaces before the action, otherwise it is a bare 404; the host's settings
// write seam is asked next, a refusal being 403 protected; the mandatory audit
// row lands before the settings module is called; and any failure the module
// returns is a 503, because a delete of a declared row has no other refusal to
// name.
//
// As on the write route, the actor is the caller's accountability key,
// Identity.Key() — the hands on the request, which under an impersonation is the
// administrator and not the account impersonated.
func (p *product) handleRemove(ctx context.Context, in removeInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	tenant, ok := p.pathTenant(in.Tenant)
	if !ok {
		return nil, p.badRequest()
	}
	key := fullKeyOf(in.Namespace, in.Key)
	if !p.writable(in.Namespace, key) {
		return speedhttp.EmptyReply(http.StatusNotFound), nil
	}
	denied, err := p.writeSettings.Denied(ctx, tenant.tenant)
	if err != nil {
		return nil, p.unavailable("asking the settings write seam", err)
	}
	if denied {
		return nil, p.protected()
	}
	caller, _ := authn.FromContext(ctx)
	actionID := newActionID()
	if err = p.auditRequired(ctx, mandatory(actionDelete, key, detail(tenant.tenant, key, actionID))); err != nil {
		return nil, err
	}
	if err = p.store.Delete(ctx, tenant.layer, key, caller.Key()); err != nil {
		p.auditBestEffort(ctx, failedAudit(actionDelete, key, actionID))
		return nil, p.unavailable("deleting a setting", err)
	}
	return speedhttp.EmptyReply(http.StatusNoContent), nil
}

// writable reports whether a full key belongs to this surface: its namespace is
// not platform-only and the key is declared. Both refusals are the same bare
// 404, so the two are one answer.
func (p *product) writable(namespace, key string) bool {
	if platformOnly(namespace) {
		return false
	}
	item, declared := p.declared(key)
	return declared && !platformOnly(item.Module)
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
// write's rows, the full key and the tenant the write acted on. No value enters
// it, so a sensitive item's value never reaches an audit row.
func detail(tenant, key, actionID string) map[string]string {
	return map[string]string{
		detailActionID: actionID,
		detailKey:      key,
		detailTenant:   tenant,
	}
}
