package tenantadmin

import (
	"context"
	"reflect"
	"testing"

	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/settings"
)

// TestEffectiveValueIsTheBooleanOr checks the OR and that the platform policy locks the item.
func TestEffectiveValueIsTheBooleanOr(t *testing.T) {
	item := decl(mfaKey, "tenant.admission", reflect.TypeFor[bool](), "false", admissionItem())
	cases := []struct {
		name     string
		platform string
		tenant   string
		want     string
		locked   bool
	}{
		{"platform on", "true", "false", "true", true},
		{"tenant on", "false", "true", "true", false},
		{"both off", "false", "false", "false", false},
		{"both on", "true", "true", "true", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, locked, err := effectiveValue(item, tc.platform, tc.tenant)
			if err != nil {
				t.Fatalf("resolving: %v", err)
			}
			if got != tc.want || locked != tc.locked {
				t.Errorf("effective/locked = %q/%t, want %q/%t", got, locked, tc.want, tc.locked)
			}
		})
	}
}

// TestEffectiveValueRefusesANonBooleanItem checks that another declared type is reported, not rendered as a boolean.
func TestEffectiveValueRefusesANonBooleanItem(t *testing.T) {
	item := decl("app.mode", "app", reflect.TypeFor[string](), "auto", settings.Item{})
	if _, _, err := effectiveValue(item, "auto", "manual"); err == nil {
		t.Error("a non-boolean item resolved, want a type mismatch")
	}
}

// TestEffectiveValueRefusesAnUndecodableRow checks that a non-boolean stored text is reported, not read as false.
func TestEffectiveValueRefusesAnUndecodableRow(t *testing.T) {
	item := decl(mfaKey, "tenant.admission", reflect.TypeFor[bool](), "false", admissionItem())
	if _, _, err := effectiveValue(item, "maybe", "false"); err == nil {
		t.Error("an undecodable global text resolved, want a type mismatch")
	}
	if _, _, err := effectiveValue(item, "false", "maybe"); err == nil {
		t.Error("an undecodable tenant text resolved, want a type mismatch")
	}
}

// TestViewOfBuildsAnItem checks that the view carries the metadata, the layer values, the effective value, row existence and source.
func TestViewOfBuildsAnItem(t *testing.T) {
	p := &product{}
	item := decl(mfaKey, "tenant.admission", reflect.TypeFor[bool](), "false", admissionItem())
	v, err := p.viewOf(context.Background(), item, "false", true, "true", true)
	if err != nil {
		t.Fatalf("building the view: %v", err)
	}
	if v.Key != mfaKey || v.Type != "bool" {
		t.Errorf("the identity is %+v", v)
	}
	if v.Title != item.Description || v.Description != item.Description {
		t.Errorf("title/description are %q/%q, want the declared English text", v.Title, v.Description)
	}
	if v.Control != "switch" || v.Danger != "critical" || v.Effect != "next-sign-in" || v.TenantAccess != "read-write" {
		t.Errorf("the control metadata is %+v", v)
	}
	if v.EffectiveValue != "true" || v.PlatformValue != "true" || v.TenantValue != "false" {
		t.Errorf("the values are %+v", v)
	}
	if !v.LockedByPlatform || !v.HasRow {
		t.Errorf("locked/has_row are %t/%t, want true/true", v.LockedByPlatform, v.HasRow)
	}
	if v.Source != sourceGlobal {
		t.Errorf("source = %q, want global: the platform policy is on", v.Source)
	}
}

// TestViewOfMasksASensitiveItem checks that a sensitive item's three value
// fields carry the mask while existence and source still answer.
func TestViewOfMasksASensitiveItem(t *testing.T) {
	p := &product{}
	item := decl("app.secret", "app", reflect.TypeFor[string](), "", settings.Item{
		Description:    "a credential",
		TitleKey:       "app.secret.title",
		DescriptionKey: "app.secret.description",
		Control:        settings.ControlSwitch,
		TenantAccess:   settings.TenantReadWrite,
		Sensitive:      true,
	})
	item.Type = reflect.TypeFor[bool]()
	item.Default = "false"
	v, err := p.viewOf(context.Background(), item, "true", true, "false", false)
	if err != nil {
		t.Fatalf("building the view: %v", err)
	}
	if v.EffectiveValue != maskedValue || v.PlatformValue != maskedValue || v.TenantValue != maskedValue {
		t.Errorf("the values are %q/%q/%q, want the mask", v.EffectiveValue, v.PlatformValue, v.TenantValue)
	}
	if !v.HasRow {
		t.Error("has_row is false, want the mask to leave the row existence intact")
	}
	if v.Source != sourceTenant {
		t.Errorf("source = %q, want tenant", v.Source)
	}
}

// TestTextFallsBackToTheDeclaredEnglish checks that without a localizer the declared English text is the answer.
func TestTextFallsBackToTheDeclaredEnglish(t *testing.T) {
	p := &product{}
	if got := p.text(context.Background(), "any.key", "the declared text"); got != "the declared text" {
		t.Errorf("text = %q, want the declared fallback", got)
	}
}

// TestViewOfRendersTheRequestLanguage checks that a localizer renders the title and description in the request's language.
func TestViewOfRendersTheRequestLanguage(t *testing.T) {
	p := &product{localizer: localizerStub{
		"tenant.admission.require-mfa.title":       "要求多因素认证",
		"tenant.admission.require-mfa.description": "成员登录时必须使用第二因素。",
	}}
	item := decl(mfaKey, "tenant.admission", reflect.TypeFor[bool](), "false", admissionItemFor(mfaKey))
	v, err := p.viewOf(context.Background(), item, "", false, "", false)
	if err != nil {
		t.Fatalf("building the view: %v", err)
	}
	if v.Title != "要求多因素认证" {
		t.Errorf("title = %q, want the localized title", v.Title)
	}
	if v.Description != "成员登录时必须使用第二因素。" {
		t.Errorf("description = %q, want the localized description", v.Description)
	}
}

type localizerStub map[string]string

func (l localizerStub) Localize(_ context.Context, key i18n.Key, _ i18n.Args) string {
	if text, ok := l[string(key)]; ok {
		return text
	}
	return string(key)
}

func (l localizerStub) Translate(_ i18n.Locale, key i18n.Key, _ i18n.Args) (string, bool) {
	text, ok := l[string(key)]
	return text, ok
}
