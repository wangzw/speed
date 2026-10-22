package sms

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// placeholderOpen and placeholderClose bracket a named placeholder in a body.
// The syntax is ${name}; a body is otherwise literal text.
const (
	placeholderOpen  = "${"
	placeholderClose = "}"
)

// TemplateSpec is one template in a set: what it is for, the ordered parameter
// names it substitutes, the vendor's code for each language, the locally
// rendered body for each language, and the vendor registration identifiers it
// carries.
//
// The shape is nested, not one flat map mixing languages and parameters,
// because it is what the configuration decoder types: the templates item is a
// container leaf, and each template's own section decodes into this struct by
// these tags. A single map of mixed values could only be decoded as an untyped
// map, and a mistyped key in it would be dropped in silence.
type TemplateSpec struct {
	// Kind is what the template is for. Empty means notification.
	Kind Kind `config:"kind"`
	// Params are the parameter names the template substitutes, in the order the
	// approved template expects them: a positional carrier lays them out in this
	// order, and a body names them as ${name}. It may be empty — a body can carry
	// no placeholder — and no name may repeat.
	Params []string `config:"params"`
	// Codes maps a language to the carrier's approved code for that language. It
	// is for a carrier that sends by vendor code. It may be empty, but every
	// language it names needs a non-empty code.
	Codes map[string]string `config:"codes"`
	// Bodies maps a language to the body this package renders for that language.
	// It is for a carrier that sends by body, and for a carrier that creates a
	// vendor content resource when a template is offered. It may be empty, but
	// every language it names needs a non-empty body whose placeholders all name
	// declared parameters.
	Bodies map[string]string `config:"bodies"`
	// Attributes are the vendor registration identifiers the template carries,
	// such as a DLT template identifier. Their keys are bounded by the mounted
	// carrier's CarrierModel.AttributeKeys.
	Attributes map[string]string `config:"attributes"`
}

// Template is the read-only view of one declared template: its name, what it is
// for, the ordered parameter names it substitutes and the languages it can be
// sent in. It carries no vendor code and no body, so a consumer sees what a set
// holds without learning the vendor's numbering, and the order of Params is the
// layout a positional carrier uses.
type Template struct {
	Name    string
	Kind    Kind
	Params  []string
	Locales []string
}

// Declaration is a frozen, self-consistent set of templates.
type Declaration struct {
	byName map[string]declaredTemplate
}

// declaredTemplate is one template as the declaration holds it: its kind, the
// ordered parameter names, the language-to-code and language-to-body tables and
// the registration attributes, private so a caller cannot change any of it after
// startup. Locales is the effective language set, codes and bodies together.
type declaredTemplate struct {
	kind       Kind
	params     []string
	codes      map[string]string
	bodies     map[string]string
	attributes map[string]string
	locales    []string
}

// NewDeclaration checks a template set for self-consistency and freezes it.
//
// Each template may declare no parameter, but no repeated parameter name; must
// name at least one language; must give every language it names a non-empty code
// or a non-empty body; and must have every placeholder in every body name a
// declared parameter. A set that fails is ErrInvalidConfig naming the template
// and, where it applies, the language or the placeholder: a template no message
// could be sent under is a startup failure, not a surprise when a message is
// first built. Whether the templates cover what a consumer needs is the
// consumer's check, made against Declared.
//
// It checks the declaration alone. Whether a template fits the carrier this
// process mounted — a code where the carrier does not send by code, a missing
// code where the vendor reviews per template, an attribute key the carrier does
// not accept, a body over the carrier's bound — is the registry's check, against
// the carrier's CarrierModel.
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

// declaredCodes validates and freezes the language-to-code table of one template.
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

// declaredBodies validates and freezes the language-to-body table of one
// template: every body is non-empty and every placeholder in it names a declared
// parameter.
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

// sortedKeys reports the keys of a string map in a stable order, so a failure
// names the same language on every run.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// bodyPlaceholders reports the placeholder names a body names, in the order they
// appear, and the tail from an opening ${ that is never closed; ok is false for
// that tail. Text that merely holds a dollar sign is not a placeholder.
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

// RenderBody substitutes the named placeholders in a body with the values in
// params, in one pass.
//
// A placeholder is written ${name}: the scan walks the body once, writes each
// placeholder's value in its place, and never scans that value again, so a value
// that itself holds ${...} stays literal. A placeholder naming a key params does
// not carry, and an opening ${ with no closing brace, are both ErrInvalidConfig:
// they say the body and the parameter set disagree, which NewDeclaration refuses
// when it freezes a template rather than anything a message can repair. Resolve
// has already checked that every declared parameter is present, so a body that
// came through NewDeclaration renders without one of these errors.
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
// needs to build one vendor request, and nothing more.
//
// It carries the destination and the dedupe identifier as well as the vendor code
// or the rendered body, because a carrier is handed this and not the Message:
// the decision of which template a message goes out under belongs to the template
// registry, and a carrier that had to look one up would be holding a template set
// of its own. Params is the keyed view, for a carrier that takes a JSON object;
// Ordered is the same values laid out in the declared order, for a positional
// carrier. A carrier that sends by code takes Code and ignores Body; one that
// sends by body takes Body and ignores Code.
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

// Resolve turns a message into what a carrier request needs.
//
// It refuses a message the carrier could not send: an empty locale (the caller's
// to normalize), a template the declaration does not hold, a language the
// template has neither a code nor a body for, a declared parameter the message
// does not carry, and a parameter the message carries that the template never
// declared. Each failure is ErrRejected and names the template, the language or
// the parameter it is about — none reached the carrier, so none is uncertain.
//
// When the template has a body for the language, Resolve renders it through
// RenderBody and answers it in Resolved.Body; when it has a code, the code goes
// into Resolved.Code, so a template that declares both answers both. It does not
// check Originator: a Declaration holds no identities, and the carrier checks the
// name in Send.
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

// hasParam reports whether the template declares the parameter name.
func (t declaredTemplate) hasParam(name string) bool {
	for _, p := range t.params {
		if p == name {
			return true
		}
	}
	return false
}
