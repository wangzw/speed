package sms

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func codeSpec() TemplateSpec {
	return TemplateSpec{
		Params: []string{"code", "ttl_minutes"},
		Codes:  map[string]string{"zh-CN": "SMS_ZH", "en-US": "SMS_EN"},
	}
}

func declaration(t *testing.T, specs map[string]TemplateSpec) Declaration {
	t.Helper()
	decl, err := NewDeclaration(specs)
	if err != nil {
		t.Fatalf("building the declaration: %v", err)
	}
	return decl
}

// TestNewDeclarationRejectsAnInconsistentSet checks that an inconsistent template is
// refused with ErrInvalidConfig naming it, while an empty parameter list is not a defect.
func TestNewDeclarationRejectsAnInconsistentSet(t *testing.T) {
	const tpl = "code.tpl"
	cases := []struct {
		name     string
		specs    map[string]TemplateSpec
		mentions string
	}{
		{"no templates", map[string]TemplateSpec{}, "no template"},
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
		{"a blank body", map[string]TemplateSpec{tpl: {
			Bodies: map[string]string{"zh-CN": "  "},
		}}, "zh-CN"},
		{"an undeclared placeholder", map[string]TemplateSpec{tpl: {
			Params: []string{"code"},
			Bodies: map[string]string{"zh-CN": "your code is ${code} for ${ttl_minutes}"},
		}}, "ttl_minutes"},
		{"an unclosed placeholder", map[string]TemplateSpec{tpl: {
			Params: []string{"code"},
			Bodies: map[string]string{"zh-CN": "your code is ${code"},
		}}, "unclosed"},
		{"a kind outside the closed set", map[string]TemplateSpec{tpl: {
			Kind:   Kind("promo"),
			Params: []string{"code"},
			Codes:  map[string]string{"zh-CN": "SMS_ZH"},
		}}, "promo"},
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

// TestNewDeclarationAcceptsASelfConsistentSet checks that code-only, body-only
// and mixed templates freeze and Declared reports them in a stable order.
func TestNewDeclarationAcceptsASelfConsistentSet(t *testing.T) {
	decl := declaration(t, map[string]TemplateSpec{
		"b.template": {Params: []string{"v"}, Bodies: map[string]string{"en-US": "value ${v}"}, Kind: KindMarketing},
		"a.template": codeSpec(),
		"c.template": {Codes: map[string]string{"zh-CN": "Z"}, Bodies: map[string]string{"en-US": "hi"}},
	})
	got := decl.Declared()
	want := []Template{
		{
			Name: "a.template", Kind: KindNotification, Params: []string{"code", "ttl_minutes"},
			Locales: []string{"en-US", "zh-CN"},
		},
		{Name: "b.template", Kind: KindMarketing, Params: []string{"v"}, Locales: []string{"en-US"}},
		{Name: "c.template", Kind: KindNotification, Locales: []string{"en-US", "zh-CN"}},
	}
	if !slices.EqualFunc(got, want, templatesEqual) {
		t.Errorf("Declared() = %+v, want %+v", got, want)
	}
}

// TestNewDeclarationDefaultsKindToNotification checks that a template naming no kind is a notification.
func TestNewDeclarationDefaultsKindToNotification(t *testing.T) {
	decl := declaration(t, map[string]TemplateSpec{
		"default": {Codes: map[string]string{"en-US": "E"}},
		"marked":  {Codes: map[string]string{"en-US": "V"}, Kind: KindVerification},
	})
	for _, tmpl := range decl.Declared() {
		want := KindNotification
		if tmpl.Name == "marked" {
			want = KindVerification
		}
		if tmpl.Kind != want {
			t.Errorf("the kind of %q = %q, want %q", tmpl.Name, tmpl.Kind, want)
		}
	}
}

// TestDeclaredReturnsCopies checks that a caller cannot change what the declaration holds through the view it is handed.
func TestDeclaredReturnsCopies(t *testing.T) {
	decl := declaration(t, map[string]TemplateSpec{"t": codeSpec()})
	first := decl.Declared()
	first[0].Params[0] = "tampered"
	first[0].Locales[0] = "tampered"
	second := decl.Declared()
	if second[0].Params[0] != "code" || second[0].Locales[0] != "en-US" {
		t.Errorf("Declared() followed the caller's copy: %+v", second[0])
	}
}

// TestRenderBodySubstitutesInOnePass checks that placeholders take their values,
// inserted values are not rescanned, and a bare dollar sign is literal.
func TestRenderBodySubstitutesInOnePass(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		params map[string]string
		want   string
	}{
		{
			"one placeholder", "your code is ${code}",
			map[string]string{"code": "123456"},
			"your code is 123456",
		},
		{"several placeholders", "${a}-${b}", map[string]string{"a": "1", "b": "2"}, "1-2"},
		{"no placeholder", "nothing to fill", nil, "nothing to fill"},
		{"a bare dollar", "costs $5, code ${code}", map[string]string{"code": "1"}, "costs $5, code 1"},
		{
			"an inserted value is not rescanned", "${greeting}",
			map[string]string{"greeting": "${code}", "code": "1"},
			"${code}",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := RenderBody(c.body, c.params)
			if err != nil {
				t.Fatalf("RenderBody(%q): %v", c.body, err)
			}
			if got != c.want {
				t.Errorf("RenderBody(%q) = %q, want %q", c.body, got, c.want)
			}
		})
	}
}

// TestRenderBodyRefusesADeclarationDefect checks that an undeclared placeholder and an unclosed ${ are both ErrInvalidConfig.
func TestRenderBodyRefusesADeclarationDefect(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		params   map[string]string
		mentions string
	}{
		{"an undeclared placeholder", "hello ${who}", map[string]string{"code": "1"}, "who"},
		{"an unclosed placeholder", "hello ${who", map[string]string{"who": "x"}, "unclosed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := RenderBody(c.body, c.params)
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("RenderBody(%q) = %v, want one wrapping ErrInvalidConfig", c.body, err)
			}
			if !strings.Contains(err.Error(), c.mentions) {
				t.Errorf("the failure %q does not name %q", err.Error(), c.mentions)
			}
		})
	}
}

// TestResolveNamesTheCodeAndOrdersTheParameters checks that a message resolves
// to the carrier code, the parameter map and the ordered values.
func TestResolveNamesTheCodeAndOrdersTheParameters(t *testing.T) {
	decl := declaration(t, map[string]TemplateSpec{"t": codeSpec()})
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
	if got.Body != "" {
		t.Errorf("Body = %q, want empty for a template with no body", got.Body)
	}
	if !slices.Equal(got.Ordered, []string{"123456", "5"}) {
		t.Errorf("Ordered = %v, want the code then the minutes, in the declared order", got.Ordered)
	}
	if got.Params["code"] != "123456" || got.Params["ttl_minutes"] != "5" {
		t.Errorf("Params = %v, want the message parameters", got.Params)
	}
	if got.Kind != KindNotification {
		t.Errorf("Kind = %q, want the default notification", got.Kind)
	}
}

// TestResolveRendersTheBody checks the body path: a declared body answers the
// rendered body and a code-only language answers an empty body.
func TestResolveRendersTheBody(t *testing.T) {
	decl := declaration(t, map[string]TemplateSpec{
		"both": {
			Params: []string{"code"},
			Codes:  map[string]string{"zh-CN": "ZH"},
			Bodies: map[string]string{"zh-CN": "code ${code}", "en-US": "code ${code}"},
		},
		"plain": {Bodies: map[string]string{"en-US": "fixed text"}},
	})

	zh, err := decl.Resolve(Message{Template: "both", Locale: "zh-CN", Params: map[string]string{"code": "42"}})
	if err != nil {
		t.Fatalf("Resolve(zh-CN): %v", err)
	}
	if zh.Code != "ZH" || zh.Body != "code 42" {
		t.Errorf("Resolve(zh-CN) = code %q, body %q, want the code and the rendered body", zh.Code, zh.Body)
	}

	en, err := decl.Resolve(Message{Template: "both", Locale: "en-US", Params: map[string]string{"code": "42"}})
	if err != nil {
		t.Fatalf("Resolve(en-US): %v", err)
	}
	if en.Code != "" || en.Body != "code 42" {
		t.Errorf("Resolve(en-US) = code %q, body %q, want only the rendered body", en.Code, en.Body)
	}

	plain, err := decl.Resolve(Message{Template: "plain", Locale: "en-US"})
	if err != nil {
		t.Fatalf("Resolve(plain): %v", err)
	}
	if plain.Body != "fixed text" || len(plain.Params) != 0 || len(plain.Ordered) != 0 {
		t.Errorf("Resolve(plain) = %+v, want a fixed body and no parameters", plain)
	}
}

// TestResolveCarriesTheSendFields checks that Resolve hands the carrier the
// message's originator, lifetime and dedupe identifier and the template's kind.
func TestResolveCarriesTheSendFields(t *testing.T) {
	decl := declaration(t, map[string]TemplateSpec{"t": {
		Kind:       KindVerification,
		Codes:      map[string]string{"en-US": "E"},
		Attributes: map[string]string{"IN_TEMPLATE_ID": "t1"},
	}})
	got, err := decl.Resolve(Message{
		To:         "+15551234567",
		Template:   "t",
		Locale:     "en-US",
		Originator: "primary",
		ValidFor:   5 * time.Minute,
		DedupeID:   "d1",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.To != "+15551234567" || got.Originator != "primary" || got.ValidFor != 5*time.Minute ||
		got.DedupeID != "d1" || got.Kind != KindVerification {
		t.Errorf("Resolve = %+v, want the message's send fields and the template's kind", got)
	}
	if got.Attributes["IN_TEMPLATE_ID"] != "t1" {
		t.Errorf("Attributes = %v, want the template's registration attributes", got.Attributes)
	}
	got.Attributes["IN_TEMPLATE_ID"] = "tampered"
	again, err := decl.Resolve(Message{Template: "t", Locale: "en-US"})
	if err != nil {
		t.Fatalf("Resolve again: %v", err)
	}
	if again.Attributes["IN_TEMPLATE_ID"] != "t1" {
		t.Errorf("the declaration followed the resolved copy: %v", again.Attributes)
	}
}

// TestResolveRejectsACallerDefect checks that each caller defect is ErrRejected naming what is wrong.
func TestResolveRejectsACallerDefect(t *testing.T) {
	decl := declaration(t, map[string]TemplateSpec{"t": codeSpec()})
	cases := []struct {
		name     string
		m        Message
		mentions string
	}{
		{"an empty locale", Message{Template: "t", Params: map[string]string{"code": "1", "ttl_minutes": "5"}}, "t"},
		{"an undeclared template", Message{Template: "nope", Locale: "zh-CN"}, "nope"},
		{"a language with no code or body", Message{Template: "t", Locale: "fr-FR"}, "fr-FR"},
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

func templatesEqual(a, b Template) bool {
	return a.Name == b.Name && a.Kind == b.Kind && slices.Equal(a.Params, b.Params) &&
		slices.Equal(a.Locales, b.Locales)
}
