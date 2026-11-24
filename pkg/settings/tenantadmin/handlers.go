package tenantadmin

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"reflect"
	"strconv"

	speedhttp "github.com/vislake/speed/pkg/http"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/settings"
)

func (p *product) handleList(ctx context.Context, in listInput) (SettingsTenantAdminSettings, error) {
	tenant, ok := p.pathTenant(in.Tenant)
	if !ok {
		return SettingsTenantAdminSettings{}, p.badRequest()
	}
	return p.list(ctx, tenant)
}

func (p *product) list(ctx context.Context, tenant selection) (SettingsTenantAdminSettings, error) {
	declared := p.store.Declared()
	items := make([]settings.DeclaredItem, 0, len(declared))
	for _, item := range declared {
		if !visibleToTenant(item) {
			continue
		}
		items = append(items, item)
	}

	rows, err := p.store.List(ctx, tenant.layer)
	if err != nil {
		return SettingsTenantAdminSettings{}, p.unavailable("listing the settings rows", err)
	}
	written := make(map[string]string, len(rows))
	present := make(map[string]bool, len(rows))
	for _, row := range rows {
		written[row.Key] = row.Value
		present[row.Key] = true
	}

	globalRows, err := p.store.List(ctx, settings.Global())
	if err != nil {
		return SettingsTenantAdminSettings{}, p.unavailable("listing the global settings rows", err)
	}
	globalWritten := make(map[string]string, len(globalRows))
	globalPresent := make(map[string]bool, len(globalRows))
	for _, row := range globalRows {
		globalWritten[row.Key] = row.Value
		globalPresent[row.Key] = true
	}

	views := make([]SettingsTenantAdminSetting, 0, len(items))
	for _, item := range items {
		view, viewErr := p.viewOf(ctx, item, written[item.Key], present[item.Key], globalWritten[item.Key], globalPresent[item.Key])
		if viewErr != nil {
			return SettingsTenantAdminSettings{}, p.unavailable("resolving a setting", viewErr)
		}
		views = append(views, view)
	}

	denied, err := p.writeSettings.Denied(ctx, tenant.tenant)
	envelope := SettingsTenantAdminSettings{Settings: views}
	switch {
	case err != nil:
		p.logger.Error(ModuleName+": the settings write seam could not answer, so the surface was reported read-only",
			slog.String("cause", err.Error()))
		envelope.ReadOnlyReason = unavailableCode
	case denied:
		envelope.ReadOnlyReason = protectedCode
	default:
		envelope.Writable = true
	}
	return envelope, nil
}

func (p *product) handleSet(ctx context.Context, in setInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	tenant, ok := p.pathTenant(in.Tenant)
	if !ok {
		return nil, p.badRequest()
	}
	key := fullKeyOf(in.Namespace, in.Key)
	item, writable, readOnly := p.writeAccess(key)
	if readOnly {
		return nil, p.readOnly()
	}
	if !writable {
		return speedhttp.EmptyReply(http.StatusNotFound), nil
	}
	if item.Sensitive {
		if text, isText := in.Value.(string); isText && text == maskedValue {
			return nil, p.maskedValue()
		}
	}
	if err := p.refuseDisabledPolicy(ctx, item, in.Value); err != nil {
		return nil, err
	}
	denied, err := p.writeSettings.Denied(ctx, tenant.tenant)
	if err != nil {
		return nil, p.unavailable("asking the settings write seam", err)
	}
	if denied {
		return nil, p.protected()
	}
	tenantName, nameErr := p.nameOf(ctx, tenant.tenant)
	if nameErr != nil {
		return nil, p.unavailable("reading the tenant's name", nameErr)
	}
	caller, _ := authn.FromContext(ctx)
	actionID := newActionID()
	if err = p.auditRequired(ctx, mandatory(actionSet, key, detail(tenantName, key, actionID))); err != nil {
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

func (p *product) handleRemove(ctx context.Context, in removeInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	tenant, ok := p.pathTenant(in.Tenant)
	if !ok {
		return nil, p.badRequest()
	}
	key := fullKeyOf(in.Namespace, in.Key)
	_, writable, readOnly := p.writeAccess(key)
	if readOnly {
		return nil, p.readOnly()
	}
	if !writable {
		return speedhttp.EmptyReply(http.StatusNotFound), nil
	}
	denied, err := p.writeSettings.Denied(ctx, tenant.tenant)
	if err != nil {
		return nil, p.unavailable("asking the settings write seam", err)
	}
	if denied {
		return nil, p.protected()
	}
	tenantName, nameErr := p.nameOf(ctx, tenant.tenant)
	if nameErr != nil {
		return nil, p.unavailable("reading the tenant's name", nameErr)
	}
	caller, _ := authn.FromContext(ctx)
	actionID := newActionID()
	if err = p.auditRequired(ctx, mandatory(actionDelete, key, detail(tenantName, key, actionID))); err != nil {
		return nil, err
	}
	if err = p.store.Delete(ctx, tenant.layer, key, caller.Key()); err != nil {
		p.auditBestEffort(ctx, failedAudit(actionDelete, key, actionID))
		return nil, p.unavailable("deleting a setting", err)
	}
	return speedhttp.EmptyReply(http.StatusNoContent), nil
}

func visibleToTenant(item settings.DeclaredItem) bool {
	return item.TenantAccess == settings.TenantRead || item.TenantAccess == settings.TenantReadWrite
}

func (p *product) writeAccess(key string) (item settings.DeclaredItem, writable, readOnly bool) {
	item, declared := p.declared(key)
	if !declared {
		return settings.DeclaredItem{}, false, false
	}
	switch item.TenantAccess {
	case settings.TenantRead:
		return settings.DeclaredItem{}, false, true
	case settings.TenantReadWrite:
		return item, true, false
	default:
		return settings.DeclaredItem{}, false, false
	}
}

func (p *product) refuseDisabledPolicy(ctx context.Context, item settings.DeclaredItem, value any) error {
	if item.Type.Kind() != reflect.Bool {
		return nil
	}
	disabled, known := disables(value)
	if !known || !disabled {
		return nil
	}
	text, present, err := p.globalText(ctx, item.Key)
	if err != nil {
		return p.unavailable("reading the platform policy", err)
	}
	if !present {
		text = item.Default
	}
	active, err := strconv.ParseBool(text)
	if err != nil {
		return p.unavailable("reading the platform policy", err)
	}
	if active {
		return p.enforced()
	}
	return nil
}

func (p *product) globalText(ctx context.Context, key string) (string, bool, error) {
	rows, err := p.store.List(ctx, settings.Global())
	if err != nil {
		return "", false, err
	}
	for _, row := range rows {
		if row.Key == key {
			return row.Value, true, nil
		}
	}
	return "", false, nil
}

func disables(value any) (bool, bool) {
	switch v := value.(type) {
	case bool:
		return !v, true
	case string:
		enabled, err := strconv.ParseBool(v)
		if err != nil {
			return false, false
		}
		return !enabled, true
	default:
		return false, false
	}
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

func detail(tenant, key, actionID string) map[string]string {
	return map[string]string{
		detailActionID: actionID,
		detailKey:      key,
		detailTenant:   tenant,
	}
}
