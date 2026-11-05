package tenantadmin

import (
	"reflect"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/settings"
)

// A resolved value is written in the declared type's own text form, the form the
// settings module stores a row in, so a view's value reads the same way its
// written and default do: a duration reads "5m0s" rather than a count of
// nanoseconds, a float its shortest round-tripping text.
func TestRenderValueWritesTheDeclaredTypesOwnTextForm(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	cases := []struct {
		name  string
		typ   reflect.Type
		value any
		want  string
	}{
		{"string", reflect.TypeFor[string](), "auto", "auto"},
		{"bool", reflect.TypeFor[bool](), true, "true"},
		{"int", reflect.TypeFor[int](), 42, "42"},
		{"uint", reflect.TypeFor[uint](), uint(7), "7"},
		{"float", reflect.TypeFor[float64](), 1.5, "1.5"},
		{"duration", reflect.TypeFor[time.Duration](), 5 * time.Minute, "5m0s"},
		{"a type that carries its own text form", reflect.TypeFor[time.Time](), at, at.Format(time.RFC3339Nano)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := renderValue(tc.typ, tc.value)
			if err != nil {
				t.Fatalf("rendering: %v", err)
			}
			if got != tc.want {
				t.Errorf("rendered %q, want %q", got, tc.want)
			}
		})
	}
}

// A value the declared type cannot hold is a failure, not a silent empty string.
func TestRenderValueRefusesAValueOfTheWrongType(t *testing.T) {
	if _, err := renderValue(reflect.TypeFor[time.Duration](), "5m"); err == nil {
		t.Error("rendering a string as a duration returned nil, want a failure")
	}
	if _, err := renderValue(reflect.TypeFor[int](), "nope"); err == nil {
		t.Error("rendering a string as an int returned nil, want a failure")
	}
}

// The view of an ordinary item carries the declaration's metadata, the two texts
// it was built from and the backend's source marker.
func TestViewOfBuildsAnOrdinaryItem(t *testing.T) {
	item := settings.DeclaredItem{
		Key:     "app.mode",
		Module:  "app",
		Type:    reflect.TypeFor[string](),
		Default: "auto",
		Item: settings.Item{
			Description: "how the application runs",
			Domain:      settings.DomainOf("one of auto or manual", func(v string) bool { return v == "auto" || v == "manual" }),
		},
	}
	v := viewOf(item, "manual", true, "manual", sourceTenant)
	if v.Key != "app.mode" || v.Namespace != "app" || v.Type != "string" {
		t.Errorf("the view is %+v", v)
	}
	if v.Description != "how the application runs" || v.Domain != "one of auto or manual" {
		t.Errorf("the metadata is %+v", v)
	}
	if v.Sensitive || v.Default != "auto" || v.Written != "manual" || !v.HasRow || v.Value != "manual" {
		t.Errorf("the values are %+v", v)
	}
	if v.Source != sourceTenant {
		t.Errorf("the source is %q, want tenant", v.Source)
	}
}

// A sensitive item's view carries no value: the default is empty and both the
// written and the resolved text are the mask. The source is the backend's answer
// and is not masked away, because it says which layer the item is configured on
// and not what its value is.
func TestViewOfMasksASensitiveItem(t *testing.T) {
	item := settings.DeclaredItem{
		Key:     "app.secret",
		Module:  "app",
		Type:    reflect.TypeFor[string](),
		Default: "",
		Item:    settings.Item{Sensitive: true},
	}
	v := viewOf(item, "hunter2", true, "hunter2", sourceTenant)
	if !v.Sensitive {
		t.Error("the view is not marked sensitive")
	}
	if v.Default != "" {
		t.Errorf("the default is %q, want empty", v.Default)
	}
	if v.Written != maskedValue || v.Value != maskedValue {
		t.Errorf("written/value are %q/%q, want the mask", v.Written, v.Value)
	}
	if !v.HasRow {
		t.Error("the mask covers the text but has_row must still report the row")
	}
	if v.Source != sourceTenant {
		t.Errorf("the source is %q, want tenant", v.Source)
	}
}

// A type that carries its own text form is classified as one; the scalar kinds
// are not.
func TestDecodesFromText(t *testing.T) {
	if !decodesFromText(reflect.TypeFor[time.Time]()) {
		t.Error("time.Time is not classified as carrying its own text form")
	}
	if decodesFromText(reflect.TypeFor[string]()) {
		t.Error("a string is classified as carrying its own text form")
	}
	if decodesFromText(reflect.TypeFor[time.Duration]()) {
		t.Error("a duration is classified as carrying its own text form")
	}
}

// The platform-only set matches a namespace root and its subtree, and nothing
// else: a module whose name merely begins with the same letters is not
// platform-only.
func TestPlatformOnlyMatchesRootsAndTheirSubtree(t *testing.T) {
	cases := map[string]bool{
		"ratelimit":             true,
		"ratelimit.burst":       true,
		"tenant.admission":      true,
		"tenant.admission.open": true,
		"authn":                 true,
		"authn.session":         true,
		"authn.session.idle":    true,
		"org.create":            true,
		"org.create.limit":      true,
		"app":                   false,
		"authnfoo":              false,
		"org.creation":          false,
		"tenant":                false,
		"tenant.membership":     false,
		"ratelimits":            false,
	}
	for namespace, want := range cases {
		if got := platformOnly(namespace); got != want {
			t.Errorf("platformOnly(%q) = %t, want %t", namespace, got, want)
		}
	}
}
