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

const jsonContentType = "application/json; charset=utf-8"

func replyJSON(body any) (speedhttp.Reply, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return speedhttp.BytesReply(http.StatusOK, jsonContentType, encoded), nil
}

func (p *product) handleList(ctx context.Context, _ listInput) ([]SettingsAdminSetting, error) {
	sel, _ := selectionFrom(ctx)
	return p.list(ctx, sel, "")
}

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

func (p *product) resolve(item settings.DeclaredItem, layer settings.Layer) (string, error) {
	target := reflect.New(item.Type)
	if err := p.store.Get(layer, item.Key, target.Interface()); err != nil {
		return "", err
	}
	return renderValue(item.Type, target.Elem().Interface())
}

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

func fullKeyOf(namespace, key string) string {
	return namespace + "." + key
}

func (p *product) declared(key string) (settings.DeclaredItem, bool) {
	for _, item := range p.store.Declared() {
		if item.Key == key {
			return item, true
		}
	}
	return settings.DeclaredItem{}, false
}

func detail(sel selection, key, actionID string) map[string]string {
	return map[string]string{
		detailActionID: actionID,
		detailKey:      key,
		detailLayer:    sel.name,
		detailTenant:   sel.tenantName,
	}
}
