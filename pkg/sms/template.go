package sms

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	placeholderOpen  = "${"
	placeholderClose = "}"
)

// TemplateSpec is one template in a set: what it is for, the ordered parameter
// names it substitutes, the vendor code and rendered body per language, and the
// registration identifiers it carries.
type TemplateSpec struct {
	// Kind is what the template is for. Empty means notification.
	Kind Kind `config:"kind"`
	// Params are the parameter names the template substitutes, in the approved order;
	// it may be empty and no name may repeat.
	Params []string `config:"params"`
	// Codes maps a language to the carrier's approved code; every language it names
	// needs a non-empty code.
	Codes map[string]string `config:"codes"`
	// Bodies maps a language to the body this package renders; every body must be
	// non-empty and use only declared parameters.
	Bodies map[string]string `config:"bodies"`
	// Attributes are the vendor registration identifiers the template carries,
	// such as a DLT template identifier. Their keys are bounded by the mounted
	// carrier's CarrierModel.AttributeKeys.
	Attributes map[string]string `config:"attributes"`
}

// Template is the read-only view of one declared template: its name, kind, ordered
// parameter names and languages. It carries no vendor code and no body.
type Template struct {
	// Name is the logical template name.
	Name string
	// Kind is what the template is for.
	Kind Kind
	// Params are the parameter names in declared order.
	Params []string
	// Locales is the effective language set.
	Locales []string
}

// Declaration is a frozen, self-consistent set of templates.
type Declaration struct {
	byName map[string]declaredTemplate
}

type declaredTemplate struct {
	kind       Kind
	params     []string
	codes      map[string]string
	bodies     map[string]string
	attributes map[string]string
	locales    []string
}

// NewDeclaration checks a template set for self-consistency and freezes it,
// refusing a defect with ErrInvalidConfig naming the template. Whether a template
// fits the mounted carrier is the registry's check.
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

func declare(name string, spec TemplateSpec) (declaredTemplate, error) {
	kind := spec.Kind
	if kind == "" {
		kind = KindNotification
	}
	if !kind.known() {
		return declaredTemplate{}, fmt.Errorf("%w: the template %q is for the kind %q, and the kinds are "+
			"%q, %q and %q", ErrInvalidConfig, name, string(kind), KindVerification, KindNotification,
			KindMarketing)
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

	codes, err := declaredCodes(name, spec.Codes)
	if err != nil {
		return declaredTemplate{}, err
	}
	bodies, err := declaredBodies(name, spec.Bodies, seen)
	if err != nil {
		return declaredTemplate{}, err
	}
	if len(codes) == 0 && len(bodies) == 0 {
		return declaredTemplate{}, fmt.Errorf("%w: the template %q has neither a code nor a body for any "+
			"language, and a template no message could be sent under is not one this set can hold",
			ErrInvalidConfig, name)
	}

	locales := make([]string, 0, len(codes)+len(bodies))
	for locale := range codes {
		locales = append(locales, locale)
	}
	for locale := range bodies {
		if _, ok := codes[locale]; !ok {
			locales = append(locales, locale)
		}
	}
	sort.Strings(locales)

	attributes := make(map[string]string, len(spec.Attributes))
	for key, value := range spec.Attributes {
		attributes[key] = value
	}
	return declaredTemplate{
		kind:       kind,
		params:     params,
		codes:      codes,
		bodies:     bodies,
		attributes: attributes,
		locales:    locales,
	}, nil
}

func declaredCodes(name string, spec map[string]string) (map[string]string, error) {
	codes := make(map[string]string, len(spec))
	for _, locale := range sortedKeys(spec) {
		if strings.TrimSpace(locale) == "" {
			return nil, fmt.Errorf("%w: the template %q declares a code under an empty language",
				ErrInvalidConfig, name)
		}
		if strings.TrimSpace(spec[locale]) == "" {
			return nil, fmt.Errorf("%w: the template %q has no carrier code for the language %q",
				ErrInvalidConfig, name, locale)
		}
		codes[locale] = spec[locale]
	}
	return codes, nil
}

func declaredBodies(name string, spec map[string]string, declared map[string]bool) (map[string]string, error) {
	bodies := make(map[string]string, len(spec))
	for _, locale := range sortedKeys(spec) {
		if strings.TrimSpace(locale) == "" {
			return nil, fmt.Errorf("%w: the template %q declares a body under an empty language",
				ErrInvalidConfig, name)
		}
		body := spec[locale]
		if strings.TrimSpace(body) == "" {
			return nil, fmt.Errorf("%w: the template %q has no body for the language %q",
				ErrInvalidConfig, name, locale)
		}
		names, unclosed, ok := bodyPlaceholders(body)
		if !ok {
			return nil, fmt.Errorf("%w: the body of the template %q for the language %q has an unclosed "+
				"placeholder %q", ErrInvalidConfig, name, locale, unclosed)
		}
		for _, placeholder := range names {
			if !declared[placeholder] {
				return nil, fmt.Errorf("%w: the body of the template %q for the language %q names the "+
					"placeholder %q, which the template does not declare", ErrInvalidConfig, name, locale, placeholder)
			}
		}
		bodies[locale] = body
	}
	return bodies, nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func bodyPlaceholders(body string) (names []string, unclosed string, ok bool) {
	rest := body
	for {
		start := strings.Index(rest, placeholderOpen)
		if start < 0 {
			return names, "", true
		}
		after := rest[start+len(placeholderOpen):]
		end := strings.Index(after, placeholderClose)
		if end < 0 {
			return nil, rest[start:], false
		}
		names = append(names, after[:end])
		rest = after[end+len(placeholderClose):]
	}
}

// RenderBody substitutes the named ${name} placeholders in one pass, so a value
// that itself holds ${...} stays literal. An undeclared placeholder or an unclosed
// ${ is ErrInvalidConfig.
func RenderBody(body string, params map[string]string) (string, error) {
	var out strings.Builder
	out.Grow(len(body))
	rest := body
	for {
		start := strings.Index(rest, placeholderOpen)
		if start < 0 {
			out.WriteString(rest)
			return out.String(), nil
		}
		out.WriteString(rest[:start])
		after := rest[start+len(placeholderOpen):]
		end := strings.Index(after, placeholderClose)
		if end < 0 {
			return "", fmt.Errorf("%w: the body has an unclosed placeholder %q", ErrInvalidConfig, rest[start:])
		}
		name := after[:end]
		value, ok := params[name]
		if !ok {
			return "", fmt.Errorf("%w: the body names the placeholder %q, which the parameters do not carry",
				ErrInvalidConfig, name)
		}
		out.WriteString(value)
		rest = after[end+len(placeholderClose):]
	}
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
			Kind:    t.kind,
			Params:  append([]string(nil), t.params...),
			Locales: append([]string(nil), t.locales...),
		})
	}
	return out
}

// Resolved is one message resolved against a template set: everything a carrier
// needs to build one vendor request. Params is the keyed view and Ordered the
// declared order; a code carrier takes Code, a body carrier Body.
type Resolved struct {
	// To is the destination, in E.164.
	To string
	// Template is the logical template name this message was resolved under, kept
	// for diagnostics.
	Template string
	// Locale is the language it resolved in.
	Locale string
	// Kind is what the template is for, from the declaration.
	Kind Kind
	// Code is the vendor's identifier for the template, empty when the template
	// has no code for the language.
	Code string
	// Body is the body rendered from the declaration, empty when the template has
	// no body for the language.
	Body string
	// Params carries the substitutions, keyed by the declared parameter name.
	Params map[string]string
	// Ordered carries the same values in the order the template declared, which
	// is the order a positional vendor was approved with.
	Ordered []string
	// Originator is the sending identity the message named, empty for the
	// carrier's default. The carrier validates it, not Resolve.
	Originator string
	// Attributes are the template's vendor registration identifiers.
	Attributes map[string]string
	// ValidFor is the lifetime the message asked for, zero for the vendor's
	// default.
	ValidFor time.Duration
	// DedupeID is the stable identifier a carrier sends at most once per.
	DedupeID string
}

// Resolve turns a message into what a carrier request needs. An empty locale, an
// unknown template, a missing language, a missing parameter and a surplus
// parameter are each ErrRejected; the carrier checks Originator in Send.
func (d Declaration) Resolve(m Message) (Resolved, error) {
	if strings.TrimSpace(m.Locale) == "" {
		return Resolved{}, fmt.Errorf("%w: the message for the template %q names no language, and the "+
			"language is the caller's to set", ErrRejected, m.Template)
	}
	t, ok := d.byName[m.Template]
	if !ok {
		return Resolved{}, fmt.Errorf("%w: this set holds no template %q", ErrRejected, m.Template)
	}
	code, hasCode := t.codes[m.Locale]
	body, hasBody := t.bodies[m.Locale]
	if !hasCode && !hasBody {
		return Resolved{}, fmt.Errorf("%w: the template %q has neither a code nor a body for the language %q",
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

	if hasBody {
		rendered, err := RenderBody(body, params)
		if err != nil {
			return Resolved{}, err
		}
		body = rendered
	}
	var attributes map[string]string
	if len(t.attributes) > 0 {
		attributes = make(map[string]string, len(t.attributes))
		for key, value := range t.attributes {
			attributes[key] = value
		}
	}
	return Resolved{
		To:         m.To,
		Template:   m.Template,
		Locale:     m.Locale,
		Kind:       t.kind,
		Code:       code,
		Body:       body,
		Params:     params,
		Ordered:    ordered,
		Originator: m.Originator,
		Attributes: attributes,
		ValidFor:   m.ValidFor,
		DedupeID:   m.DedupeID,
	}, nil
}

func (t declaredTemplate) hasParam(name string) bool {
	for _, p := range t.params {
		if p == name {
			return true
		}
	}
	return false
}
