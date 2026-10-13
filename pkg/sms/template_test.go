package sms

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// spec is a self-consistent template the cases start from.
func spec() TemplateSpec {
	return TemplateSpec{
		Params: []string{"code", "ttl_minutes"},
		Codes:  map[string]string{"zh-CN": "SMS_ZH", "en-US": "SMS_EN"},
	}
}

// declaration builds a declaration the cases resolve messages against, failing
// the case if the templates are not self-consistent.
func declaration(t *testing.T, specs map[string]TemplateSpec) Declaration {
	t.Helper()
	decl, err := NewDeclaration(specs)
	if err != nil {
		t.Fatalf("building the declaration: %v", err)
	}
	return decl
}

// TestNewDeclarationRejectsAnInconsistentSet is the carrier self-consistency
// check: a template with no parameter, no language, an empty code, a blank name
// or a repeated parameter is refused with ErrInvalidConfig naming the offending
// template — or, for an empty code, the language it is missing — so a carrier
// stops at startup rather than at the first message it cannot build.
func TestNewDeclarationRejectsAnInconsistentSet(t *testing.T) {
	// The template name the single-template cases declare, distinctive so the
	// naming assertion reads the combination and not an accidental substring.
	const tpl = "code.tpl"
	cases := []struct {
		name     string
		specs    map[string]TemplateSpec
		mentions string
	}{
		{"no templates", map[string]TemplateSpec{}, "no template"},
		{"no parameter", map[string]TemplateSpec{tpl: {Codes: map[string]string{"zh-CN": "SMS_ZH"}}}, tpl},
		{"a blank parameter", map[string]TemplateSpec{tpl: {
			Params: []string{"code", "  "},
			Codes:  map[string]string{"zh-CN": "SMS_ZH"},
		}}, tpl},
		{"a repeated parameter", map[string]TemplateSpec{tpl: {
			Params: []string{"code", "code"},
			Codes:  map[string]string{"zh-CN": "SMS_ZH"},
		}}, tpl},
		{"no language", map[string]TemplateSpec{tpl: {Params: []string{"code"}}}, tpl},
		{"a blank code", map[string]TemplateSpec{tpl: {
			Params: []string{"code"},
			Codes:  map[string]string{"zh-CN": "  "},
		}}, "zh-CN"},
		{"a blank language", map[string]TemplateSpec{tpl: {
			Params: []string{"code"},
			Codes:  map[string]string{"  ": "SMS_ZH"},
		}}, tpl},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := NewDeclaration(c.specs)
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("NewDeclaration(%v) = %v, want one wrapping ErrInvalidConfig", c.name, err)
			}
			if !strings.Contains(err.Error(), c.mentions) {
				t.Errorf("the failure %q does not name %q", err.Error(), c.mentions)
			}
		})
	}
}

// TestNewDeclarationAcceptsASelfConsistentSet is the control, and it holds that
// Declared reports the templates in a stable order with the parameter order
// preserved and the languages sorted.
func TestNewDeclarationAcceptsASelfConsistentSet(t *testing.T) {
	decl := declaration(t, map[string]TemplateSpec{
		"b.template": {Params: []string{"v"}, Codes: map[string]string{"en-US": "E"}},
		"a.template": spec(),
	})
	got := decl.Declared()
	want := []Template{
		{Name: "a.template", Params: []string{"code", "ttl_minutes"}, Locales: []string{"en-US", "zh-CN"}},
		{Name: "b.template", Params: []string{"v"}, Locales: []string{"en-US"}},
	}
	if !slices.EqualFunc(got, want, templatesEqual) {
		t.Errorf("Declared() = %+v, want %+v", got, want)
	}
}

// TestDeclaredReturnsCopies holds that a caller cannot change what the
// declaration holds through the view it is handed.
func TestDeclaredReturnsCopies(t *testing.T) {
	decl := declaration(t, map[string]TemplateSpec{"t": spec()})
	first := decl.Declared()
	first[0].Params[0] = "tampered"
	first[0].Locales[0] = "tampered"
	second := decl.Declared()
	if second[0].Params[0] != "code" || second[0].Locales[0] != "en-US" {
		t.Errorf("Declared() followed the caller's copy: %+v", second[0])
	}
}

// TestResolveNamesTheCodeAndOrdersTheParameters is the control: a message the
// declaration holds resolves to the carrier code, the parameter map and the
// ordered values.
func TestResolveNamesTheCodeAndOrdersTheParameters(t *testing.T) {
	decl := declaration(t, map[string]TemplateSpec{"t": spec()})
	got, err := decl.Resolve(Message{
		Template: "t",
		Locale:   "zh-CN",
		Params:   map[string]string{"ttl_minutes": "5", "code": "123456"},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Code != "SMS_ZH" {
		t.Errorf("Code = %q, want the zh-CN carrier code SMS_ZH", got.Code)
	}
	if !slices.Equal(got.Ordered, []string{"123456", "5"}) {
		t.Errorf("Ordered = %v, want the code then the minutes, in the declared order", got.Ordered)
	}
	if got.Params["code"] != "123456" || got.Params["ttl_minutes"] != "5" {
		t.Errorf("Params = %v, want the message parameters", got.Params)
	}
}

// TestResolveRejectsACallerDefect holds that an empty locale, an undeclared
// template, a language with no code, a missing parameter and a surplus parameter
// are each refused with ErrRejected naming what is wrong: none reached the
// carrier, so none is uncertain.
func TestResolveRejectsACallerDefect(t *testing.T) {
	decl := declaration(t, map[string]TemplateSpec{"t": spec()})
	cases := []struct {
		name     string
		m        Message
		mentions string
	}{
		{"an empty locale", Message{Template: "t", Params: map[string]string{"code": "1", "ttl_minutes": "5"}}, "t"},
		{"an undeclared template", Message{Template: "nope", Locale: "zh-CN"}, "nope"},
		{"a language with no code", Message{Template: "t", Locale: "fr-FR"}, "fr-FR"},
		{"a missing parameter", Message{Template: "t", Locale: "zh-CN", Params: map[string]string{"code": "1"}}, "ttl_minutes"},
		{"a surplus parameter", Message{Template: "t", Locale: "zh-CN", Params: map[string]string{"code": "1", "ttl_minutes": "5", "extra": "x"}}, "extra"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := decl.Resolve(c.m)
			if !errors.Is(err, ErrRejected) {
				t.Fatalf("Resolve gave %v, want one wrapping ErrRejected", err)
			}
			if !strings.Contains(err.Error(), c.mentions) {
				t.Errorf("the failure %q does not name %q", err.Error(), c.mentions)
			}
		})
	}
}

// templatesEqual compares two declared views field by field, since slices are not
// comparable with ==.
func templatesEqual(a, b Template) bool {
	return a.Name == b.Name && slices.Equal(a.Params, b.Params) && slices.Equal(a.Locales, b.Locales)
}
