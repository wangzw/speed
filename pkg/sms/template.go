package sms

import (
	"fmt"
	"sort"
	"strings"
)

// TemplateSpec is one template in a set: the ordered parameter names it
// substitutes and the vendor's code for each language.
//
// The shape is nested, not one flat map mixing languages and parameters,
// because it is what the configuration decoder types: the templates item is a
// container leaf, and each template's own section decodes into this struct by
// these tags. A single map of mixed values could only be decoded as an untyped
// map, and a mistyped key in it would be dropped in silence.
type TemplateSpec struct {
	// Params are the parameter names the template substitutes, in the order the
	// approved template expects them: a positional carrier lays them out in this
	// order. At least one is required, and no name may repeat.
	Params []string `config:"params"`
	// Codes maps a language to the carrier's approved code for that language. At
	// least one language is required, with a non-empty code for each.
	Codes map[string]string `config:"codes"`
}

// Template is the read-only view of one declared template: its name, the ordered
// parameter names it substitutes and the languages it has a code for. It carries
// no vendor code, so a consumer sees what a set holds without learning the
// vendor's numbering, and the order of Params is the layout a positional
// carrier uses.
type Template struct {
	Name    string
	Params  []string
	Locales []string
}

// Declaration is a frozen, self-consistent set of templates.
type Declaration struct {
	byName map[string]declaredTemplate
}

// declaredTemplate is one template as the declaration holds it: the ordered
// parameter names and the language-to-code table, private so a caller cannot
// change the mapping after startup.
type declaredTemplate struct {
	params  []string
	codes   map[string]string
	locales []string
}

// NewDeclaration checks a template set for self-consistency and freezes it.
//
// Each template needs at least one parameter, no repeated parameter name and at
// least one language with a non-empty code. A set that fails is ErrInvalidConfig
// naming the template: a template no message could be sent under is a startup
// failure, not a surprise when a message is first built. Whether the templates
// cover what a consumer needs is the consumer's check, made against Declared.
func NewDeclaration(specs map[string]TemplateSpec) (Declaration, error) {
	if len(specs) == 0 {
		return Declaration{}, fmt.Errorf("%w: the set holds no template", ErrInvalidConfig)
	}
	names := make([]string, 0, len(specs))
	for name := range specs {
		names = append(names, name)
	}
	sort.Strings(names)

	byName := make(map[string]declaredTemplate, len(specs))
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			return Declaration{}, fmt.Errorf("%w: a template is declared with no name", ErrInvalidConfig)
		}
		declared, err := declare(name, specs[name])
		if err != nil {
			return Declaration{}, err
		}
		byName[name] = declared
	}
	return Declaration{byName: byName}, nil
}

// declare checks one template for self-consistency and freezes it.
func declare(name string, spec TemplateSpec) (declaredTemplate, error) {
	if len(spec.Params) == 0 {
		return declaredTemplate{}, fmt.Errorf("%w: the template %q declares no parameter, and a template "+
			"that substitutes nothing is not one this package can resolve", ErrInvalidConfig, name)
	}
	seen := make(map[string]bool, len(spec.Params))
	params := make([]string, 0, len(spec.Params))
	for _, param := range spec.Params {
		if strings.TrimSpace(param) == "" {
			return declaredTemplate{}, fmt.Errorf("%w: the template %q declares an empty parameter name",
				ErrInvalidConfig, name)
		}
		if seen[param] {
			return declaredTemplate{}, fmt.Errorf("%w: the template %q declares the parameter %q twice",
				ErrInvalidConfig, name, param)
		}
		seen[param] = true
		params = append(params, param)
	}

	if len(spec.Codes) == 0 {
		return declaredTemplate{}, fmt.Errorf("%w: the template %q has a code for no language",
			ErrInvalidConfig, name)
	}
	codes := make(map[string]string, len(spec.Codes))
	locales := make([]string, 0, len(spec.Codes))
	for locale, code := range spec.Codes {
		if strings.TrimSpace(locale) == "" {
			return declaredTemplate{}, fmt.Errorf("%w: the template %q declares a code under an empty language",
				ErrInvalidConfig, name)
		}
		if strings.TrimSpace(code) == "" {
			return declaredTemplate{}, fmt.Errorf("%w: the template %q has no carrier code for the language %q",
				ErrInvalidConfig, name, locale)
		}
		codes[locale] = code
		locales = append(locales, locale)
	}
	sort.Strings(locales)
	return declaredTemplate{params: params, codes: codes, locales: locales}, nil
}

// Declared reports the declared templates in a stable order, each with its own
// copy of the parameter and language lists, so a caller cannot change what the
// declaration holds.
func (d Declaration) Declared() []Template {
	names := make([]string, 0, len(d.byName))
	for name := range d.byName {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]Template, 0, len(names))
	for _, name := range names {
		t := d.byName[name]
		out = append(out, Template{
			Name:    name,
			Params:  append([]string(nil), t.params...),
			Locales: append([]string(nil), t.locales...),
		})
	}
	return out
}

// Resolved is one message resolved against a template set: everything a carrier
// needs to build one vendor request, and nothing more.
//
// It carries the destination and the dedupe identifier as well as the vendor
// code, because a carrier is handed this and not the Message: the decision of
// which template a message goes out under belongs to the template registry, and
// a carrier that had to look one up would be holding a template set of its own.
// Params is the keyed view, for a carrier that takes a JSON object; Ordered is
// the same values laid out in the declared order, for a positional carrier.
type Resolved struct {
	// To is the destination, in E.164.
	To string
	// Template is the logical template name this message was resolved under, kept
	// for diagnostics.
	Template string
	// Locale is the language it resolved in.
	Locale string
	// Code is the vendor's identifier for the template.
	Code string
	// Params carries the substitutions, keyed by the declared parameter name.
	Params map[string]string
	// Ordered carries the same values in the order the template declared, which
	// is the order a positional vendor was approved with.
	Ordered []string
	// DedupeID is the stable identifier a carrier sends at most once per.
	DedupeID string
}

// Resolve turns a message into what a carrier request needs.
//
// It refuses a message the carrier could not send: an empty locale (the caller's
// to normalize), a template the declaration does not hold, a language the
// template has no code for, a declared parameter the message does not carry, and
// a parameter the message carries that the template never declared. Each failure
// is ErrRejected and names the template, the language or the parameter it is
// about — none reached the carrier, so none is uncertain.
//
// It answers for one declaration alone. Whether a template is disabled, or
// whether its vendor review has come back, is not a property of a declaration
// and is not decided here; the registry that holds the declaration decides it
// before reaching this.
func (d Declaration) Resolve(m Message) (Resolved, error) {
	if strings.TrimSpace(m.Locale) == "" {
		return Resolved{}, fmt.Errorf("%w: the message for the template %q names no language, and the "+
			"language is the caller's to set", ErrRejected, m.Template)
	}
	t, ok := d.byName[m.Template]
	if !ok {
		return Resolved{}, fmt.Errorf("%w: this set holds no template %q", ErrRejected, m.Template)
	}
	code, ok := t.codes[m.Locale]
	if !ok {
		return Resolved{}, fmt.Errorf("%w: the template %q has no code for the language %q",
			ErrRejected, m.Template, m.Locale)
	}

	ordered := make([]string, len(t.params))
	params := make(map[string]string, len(t.params))
	for i, name := range t.params {
		value, ok := m.Params[name]
		if !ok {
			return Resolved{}, fmt.Errorf("%w: the template %q needs the parameter %q for the language %q "+
				"and the message carries none", ErrRejected, m.Template, name, m.Locale)
		}
		ordered[i] = value
		params[name] = value
	}

	extra := make([]string, 0, len(m.Params))
	for name := range m.Params {
		if !t.hasParam(name) {
			extra = append(extra, name)
		}
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		return Resolved{}, fmt.Errorf("%w: the message carries the parameter %q, which the template %q does "+
			"not declare", ErrRejected, extra[0], m.Template)
	}
	return Resolved{
		To:       m.To,
		Template: m.Template,
		Locale:   m.Locale,
		Code:     code,
		Params:   params,
		Ordered:  ordered,
		DedupeID: m.DedupeID,
	}, nil
}

// hasParam reports whether the template declares the parameter name.
func (t declaredTemplate) hasParam(name string) bool {
	for _, p := range t.params {
		if p == name {
			return true
		}
	}
	return false
}
