package admission

import (
	"testing"

	"github.com/vislake/speed/pkg/settings"
)

func TestTheTwoTenantSwitchesAreDeclaredForTheTenantSurface(t *testing.T) {
	schema := settingsSchema()
	if !schema.TenantFacing {
		t.Error("the settings schema is not tenant-facing, so neither switch can be exposed")
	}
	for _, key := range []string{requireMFAItem, requireSSOItem} {
		item, declared := schema.Items[key]
		if !declared {
			t.Errorf("the schema declares no item %q", key)
			continue
		}
		if item.TenantAccess != settings.TenantReadWrite {
			t.Errorf("%s carries tenant access %q, want TenantReadWrite", key, item.TenantAccess)
		}
		if item.TitleKey == "" {
			t.Errorf("%s declares no TitleKey", key)
		}
		if item.DescriptionKey == "" {
			t.Errorf("%s declares no DescriptionKey", key)
		}
		if item.Description == "" {
			t.Errorf("%s declares no English description to fall back to", key)
		}
		if item.Control != settings.ControlSwitch {
			t.Errorf("%s renders control %q, want ControlSwitch", key, item.Control)
		}
		if item.Danger != settings.DangerCritical {
			t.Errorf("%s carries danger %q, want DangerCritical", key, item.Danger)
		}
		if item.Effect != settings.EffectNextSignIn {
			t.Errorf("%s carries effect %q, want EffectNextSignIn", key, item.Effect)
		}
	}
}

func TestTheLifetimeStaysPlatformOnly(t *testing.T) {
	item, declared := settingsSchema().Items[checkTTLItem]
	if !declared {
		t.Fatalf("the settings declare no item %q", checkTTLItem)
	}
	if item.TenantAccess != settings.TenantNone {
		t.Errorf("%s carries tenant access %q, want TenantNone", checkTTLItem, item.TenantAccess)
	}
	if item.TitleKey != "" || item.DescriptionKey != "" || item.Control != "" ||
		item.Danger != "" || item.Effect != "" {
		t.Errorf("%s carries presentation metadata %+v, and it is not tenant-facing", checkTTLItem, item)
	}
}
