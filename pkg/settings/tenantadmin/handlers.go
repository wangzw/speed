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

// handleList answers GET /{tenant}/settings: the items whose declarations expose
// them to a tenant, each with its platform value, tenant value and effective
// value, in the envelope that also says whether the tenant may write at all.
// The route's authorization step has already decided the read.
func (p *product) handleList(ctx context.Context, in listInput) (SettingsTenantAdminSettings, error) {
	tenant, ok := p.pathTenant(in.Tenant)
	if !ok {
		return SettingsTenantAdminSettings{}, p.badRequest()
	}
	return p.list(ctx, tenant)
}

// list answers the all-items read.
//
// The items are the declaration listing filtered by declaration alone: an item
// is visible when its declaration gives it a tenant access, and no namespace is
// excluded or opened by its name. Each item's platform value and row existence
// come from the global layer's rows, its tenant value and row existence from the
// path tenant's rows, and its effective value is the boolean OR of the two. A
// failure to read either layer is a 503, because a listing that could not read a
// row must not answer as though the row were not there.
//
// The envelope also carries whether the host's settings write seam would refuse
// a write to this tenant. A refusal is reported as read_only_reason `protected`.
// A seam that cannot answer at all is reported as read_only_reason
// `unavailable` with writable false: the settings were read and are served, and
// the surface declines to claim a write it could not clear. The listing still
// answers 200 in that case; a 503 would hide settings the tenant can read over a
// question about writing them, and the write routes keep answering 503 for the
// same unreadable seam. The seam's cause goes to the log and never to the body.
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

// handleSet answers PUT /{tenant}/settings/:namespace/:key: it writes one row on
// the path tenant's layer. The route's authorization step has already decided
// the write, and the entry point has already bound the body.
//
// The order is fixed: the tenant and the declaration are checked, a read-only
// item is refused, a masked value is refused, the platform policy is checked for
// a disable it forbids, the host's settings write seam is asked, the mandatory
// audit row lands before the action, and the settings module is called last. A
// key no declaration exposes to a tenant at all is a bare 404, the same answer
// an undeclared key gets; an item declared read-only is 403 read-only, because
// it is visible and the caller is told why the write is refused. A masked value
// is 422 masked-value without the seam, the audit or the store; a disable of a
// platform-enforced policy is 409 tenant-admission-enforced, also without them;
// a seam refusal is 403 protected and an unanswered seam is 503; a value the
// settings module refuses is a 422.
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
	tenantAlias, aliasErr := p.aliasOf(ctx, tenant.tenant)
	if aliasErr != nil {
		return nil, p.unavailable("reading the tenant's alias", aliasErr)
	}
	caller, _ := authn.FromContext(ctx)
	actionID := newActionID()
	if err = p.auditRequired(ctx, mandatory(actionSet, key, detail(tenantAlias, key, actionID))); err != nil {
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
// The full key is required to be declared and exposed to a tenant before the
// action: a key with no tenant access is a bare 404, and an item declared
// read-only is 403 read-only. Dropping the tenant row while a platform policy is
// on is allowed: the effective value stays on, because the platform layer is a
// floor, so there is no disable to refuse. The host's settings write seam is
// asked next, a refusal being 403 protected; the mandatory audit row lands
// before the settings module is called; and any failure the module returns is a
// 503, because a delete of a declared row has no other refusal to name.
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
	tenantAlias, aliasErr := p.aliasOf(ctx, tenant.tenant)
	if aliasErr != nil {
		return nil, p.unavailable("reading the tenant's alias", aliasErr)
	}
	caller, _ := authn.FromContext(ctx)
	actionID := newActionID()
	if err = p.auditRequired(ctx, mandatory(actionDelete, key, detail(tenantAlias, key, actionID))); err != nil {
		return nil, err
	}
	if err = p.store.Delete(ctx, tenant.layer, key, caller.Key()); err != nil {
		p.auditBestEffort(ctx, failedAudit(actionDelete, key, actionID))
		return nil, p.unavailable("deleting a setting", err)
	}
	return speedhttp.EmptyReply(http.StatusNoContent), nil
}

// visibleToTenant reports whether an item's declaration exposes it to a tenant.
// The schema-level TenantFacing switch is judged when the key table is built: an
// item carrying a tenant access under a schema that does not declare itself
// tenant-facing fails the startup, so the item itself is the complete statement
// by the time a request sees it.
func visibleToTenant(item settings.DeclaredItem) bool {
	return item.TenantAccess == settings.TenantRead || item.TenantAccess == settings.TenantReadWrite
}

// writeAccess settles what the tenant write routes may do with a full key. It
// keeps the two refusals apart: an undeclared key and an item with no tenant
// access are invisible and answer the same bare 404, while an item the
// declarations expose for reading alone is visible and answers 403 read-only.
// Only a read-write item proceeds, carrying its declaration.
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

// refuseDisabledPolicy refuses a write that would turn off a policy the platform
// layer requires. The platform value is the global row, or the declared default
// when there is none; when it is true the item is enforced, and a request that
// sets the item to false is a 409 rather than a write the effective-value rule
// would silently overrule. Writing the enforced value and dropping the tenant
// row stay legal.
//
// The check runs after the declaration is established and before the host seam,
// the audit and the store: a refused write asks nothing and changes nothing. A
// platform value that cannot be read or decoded is a 503, never read as an
// absent enforcement.
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

// globalText reads the global layer's text for one key. The second result says
// whether the layer holds a row at all, which a write of the declared default
// needs to tell apart from an explicit default row.
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

// disables reports whether a write's value turns a boolean item off, and whether
// the value is a boolean at all. A value of another shape is left to the
// settings module, which rejects it under the declared type.
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
