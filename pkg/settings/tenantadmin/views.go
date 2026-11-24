package tenantadmin

import (
	"context"
	"fmt"
	"reflect"
	"strconv"

	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/settings"
)

const maskedValue = settings.MaskedValue

// The one place an item's view is built; a sensitive item's value fields carry the mask.
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
