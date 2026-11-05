package tenantadmin

import (
	"context"
	"fmt"
	"reflect"
	"strconv"

	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/settings"
)

// maskedValue is the fixed string a sensitive item's value is replaced with
// before the view reaches a client. It is the settings capability's own mask,
// so a client reads one marker across every management surface.
const maskedValue = settings.MaskedValue

// viewOf builds one item's view from its declaration, the text the tenant layer
// holds for it, whether it holds a row at all, the text the global layer holds,
// and whether the global layer holds a row.
//
// The platform value is the global row's text, or the declared default when
// there is no global row. The tenant value is the tenant row's text, or the
// declared default when the tenant holds none. The effective value is the
// policy's boolean OR: a platform requirement is a floor a tenant cannot lower,
// so an item is effectively on when either layer holds true. The effective
// value is also what locks the item: a platform policy that is on keeps it on
// regardless of the tenant's own row.
//
// source names the layer the effective value came from, in the closed set the
// capability already uses: global when the platform policy is on, tenant when
// the tenant holds a row and the platform is not forcing the item, global when
// only the global layer holds a row, default when neither does. There is no
// separate platform marker.
//
// The title and description are rendered in the request's language from the
// declaration's keys; with no localizer, or a key no catalog carries, both fall
// back to the declared English text.
//
// A sensitive item's three value fields carry the mask; its row existence and
// its source still answer, because those say which layer is configured and not
// what the value is.
func (p *product) viewOf(
	ctx context.Context,
	item settings.DeclaredItem,
	tenantText string,
	hasRow bool,
	platformText string,
	hasPlatformRow bool,
) (SettingsTenantAdminSetting, error) {
	tenantValue := item.Default
	if hasRow {
		tenantValue = tenantText
	}
	platformValue := item.Default
	if hasPlatformRow {
		platformValue = platformText
	}
	effective, locked, err := effectiveValue(item, platformValue, tenantValue)
	if err != nil {
		return SettingsTenantAdminSetting{}, err
	}
	source := sourceDefault
	switch {
	case locked:
		source = sourceGlobal
	case hasRow:
		source = sourceTenant
	case hasPlatformRow:
		source = sourceGlobal
	}
	view := SettingsTenantAdminSetting{
		Key:              item.Key,
		Title:            p.text(ctx, i18n.Key(item.TitleKey), item.Description),
		Description:      p.text(ctx, i18n.Key(item.DescriptionKey), item.Description),
		Type:             item.Type.String(),
		Control:          string(item.Control),
		Danger:           string(item.Danger),
		Effect:           string(item.Effect),
		TenantAccess:     string(item.TenantAccess),
		EffectiveValue:   effective,
		PlatformValue:    platformValue,
		TenantValue:      tenantValue,
		LockedByPlatform: locked,
		HasRow:           hasRow,
		Source:           source,
	}
	if item.Sensitive {
		view.EffectiveValue = maskedValue
		view.PlatformValue = maskedValue
		view.TenantValue = maskedValue
	}
	return view, nil
}

// effectiveValue resolves one switch's effective value and whether the platform
// policy locks it. The tenant surface renders switches, so an item of any other
// declared type is a declaration this surface cannot resolve and is reported as
// a type mismatch rather than rendered as though it were a boolean.
//
// A row whose text does not decode as a boolean is a corrupt stored value; it is
// reported as a mismatch so the listing answers 503 rather than a wrong value.
func effectiveValue(item settings.DeclaredItem, platformValue, tenantValue string) (string, bool, error) {
	if item.Type.Kind() != reflect.Bool {
		return "", false, fmt.Errorf("%w: the tenant surface renders switches, and %q is declared as %s",
			settings.ErrTypeMismatch, item.Key, item.Type)
	}
	platform, err := strconv.ParseBool(platformValue)
	if err != nil {
		return "", false, fmt.Errorf("%w: the global row of %q is not a boolean: %w",
			settings.ErrTypeMismatch, item.Key, err)
	}
	tenant, err := strconv.ParseBool(tenantValue)
	if err != nil {
		return "", false, fmt.Errorf("%w: the tenant row of %q is not a boolean: %w",
			settings.ErrTypeMismatch, item.Key, err)
	}
	return strconv.FormatBool(platform || tenant), platform, nil
}
