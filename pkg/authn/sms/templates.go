package sms

import (
	"fmt"
	"slices"
	"strings"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/sms"
)

// The template names this module sends its codes under, one per purpose. They
// are the configuration contract a host writes into its carrier's templates:
// the carrier maps each name, with a language, to a template number it holds or
// to a body this module's message is rendered into, so the name is what this
// module needs covered and the number or the body is the carrier's own. A
// carrier that does not declare one of the required templates stops the startup,
// before any code is sent. The reset template is optional: its absence withdraws
// the SMS reset channel and leaves every other purpose working.
const (
	templateLogin  = "authn.code.login"
	templateMFA    = "authn.code.mfa"
	templateEnroll = "authn.code.enroll"
	templateReset  = "authn.code.reset"
)

// templateParams are the parameter names every one of this module's templates
// substitutes: the code and its lifetime in whole minutes.
var templateParams = []string{"code", "ttl_minutes"}

// requiredTemplates are the template names every purpose but reset sends its
// codes under. A carrier that does not declare one of them — neither a vendor
// code nor a body for any language — cannot carry this module's messages and
// stops the startup. The reset template is not here: a carrier may carry the
// three login ways and no password reset, which withdraws that channel alone.
var requiredTemplates = []string{templateLogin, templateMFA, templateEnroll}

// purposeTemplate maps a purpose to the template name its message carries. The
// purpose is one of the closed set, checked before a message is built, so the
// empty answer stands for no valid purpose.
func purposeTemplate(purpose string) string {
	switch purpose {
	case purposeLogin:
		return templateLogin
	case purposeMFA:
		return templateMFA
	case purposeEnroll:
		return templateEnroll
	case purposeReset:
		return templateReset
	default:
		return ""
	}
}

// templateLocales returns, for each template name this module sends under, the
// languages that template declares, in declared order. A message goes out in a
// language of the template its purpose maps to, not of the registry's whole set:
// a registry whose templates declare different languages is valid, and the
// languages a message may be sent in are the ones of the template it carries.
// The set is the template's effective one — a language it has a vendor code for
// or a body for, together — so a carrier that sends only rendered bodies is
// covered the same way as one that sends only by code. checkSender runs first
// and refuses a registry that leaves any required template with no languages or
// without the configured default, so every entry read here is a set a message
// can actually go out in.
func templateLocales(declared []sms.Template) map[string][]string {
	held := make(map[string]sms.Template, len(declared))
	for _, t := range declared {
		held[t.Name] = t
	}
	out := make(map[string][]string, len(requiredTemplates)+1)
	for _, name := range requiredTemplates {
		out[name] = append([]string(nil), held[name].Locales...)
	}
	// The reset template is present only when the carrier declares it; its
	// absence is what leaves the SMS reset channel out, so no entry is written
	// for it and a message under it has no languages to resolve into.
	if t, ok := held[templateReset]; ok {
		out[templateReset] = append([]string(nil), t.Locales...)
	}
	return out
}

// resolveLocale picks the language a message goes out in. An empty request names
// none and reads as the run's configured default. A non-empty request is matched
// by primary subtag against the languages the message's template declares: the
// request for "en-GB" goes out under the template's "en-US", and a request for a
// language the template does not declare at all falls back to the configured
// default rather than being refused, because a language is a caller's preference
// and the template's set is what the message can actually be sent in.
func resolveLocale(requested, defaultLocale string, declared []string) string {
	if requested == "" {
		return defaultLocale
	}
	want := primarySubtag(requested)
	for _, locale := range declared {
		if primarySubtag(locale) == want {
			return locale
		}
	}
	return defaultLocale
}

// primarySubtag returns the first subtag of a BCP 47 tag, folded to lower case
// so that a case difference does not keep a match apart, or "" for an empty tag.
func primarySubtag(tag string) string {
	first, _, _ := strings.Cut(tag, "-")
	return strings.ToLower(first)
}

// checkSender verifies that the registry declares every required template this
// module sends under, exactly the parameters it sends, and the configured
// default language, and returns the first combination it gets wrong, naming it.
// The reset template is held to the same shape when the carrier declares it, and
// its absence is not a failure: it withdraws the SMS reset channel alone. Every
// failure wraps authn.ErrInvalidConfig.
//
// The parameter names are compared for equality, not containment. Resolve
// requires every parameter a template declares, and this module's messages carry
// only its own two, so a template declaring one the module never sends has no
// message that can satisfy it: it would pass a subset check at startup and then
// reject every send at runtime, where the failure is a metric and a warn line and
// reaches no operator. Equality names that template at startup instead.
//
// The languages are not a set this module holds: they are the registry's, taken
// from what it declares. What the module does require is that every template
// carry the configured default language in its effective set — the languages it
// has a vendor code for and the languages it has a body for, together — because
// every fallback — an empty request, and a request no declared language matches
// — resolves to it, and a template without it could not send a message the
// module falls back to. A carrier that sends by rendered body declares the
// languages in the body table; the text itself is the host's configuration, so
// this module carries no carrier-specific body of its own.
//
// The check runs where the need is known: the registry reports what it holds
// through Registry.Declared and knows nothing of which consumer needs what, so
// the coverage of this module's contract is this module's to check, in New, once
// the registry is taken up. A mismatch is a startup failure, not a message first
// refused when a code is sent under it.
func checkSender(declared []sms.Template, defaultLocale string) error {
	held := make(map[string]sms.Template, len(declared))
	for _, t := range declared {
		held[t.Name] = t
	}
	for _, name := range requiredTemplates {
		t, ok := held[name]
		if !ok {
			return fmt.Errorf("%w: the carrier declares no template %q", authn.ErrInvalidConfig, name)
		}
		if err := checkTemplate(t, defaultLocale); err != nil {
			return err
		}
	}
	// The reset template is optional in its presence but not in its shape: a
	// carrier that declares it and cannot satisfy the same parameters, languages
	// and default-language rule has promised a reset channel every message under
	// which would be refused at send time, so the startup fails naming it. A
	// carrier that declares none keeps the channel closed and starts normally.
	if t, ok := held[templateReset]; ok {
		if err := checkTemplate(t, defaultLocale); err != nil {
			return err
		}
	}
	return nil
}

// checkTemplate holds one declared template to the module's contract: at least
// one language, the configured default language among them, exactly the two
// parameters every message of this module carries, and no other.
func checkTemplate(t sms.Template, defaultLocale string) error {
	if len(t.Locales) == 0 {
		return fmt.Errorf("%w: the carrier's template %q has neither a code nor a body for any language",
			authn.ErrInvalidConfig, t.Name)
	}
	if !slices.Contains(t.Locales, defaultLocale) {
		return fmt.Errorf("%w: the carrier's template %q has neither a code nor a body for the default "+
			"language %q", authn.ErrInvalidConfig, t.Name, defaultLocale)
	}
	for _, param := range templateParams {
		if !slices.Contains(t.Params, param) {
			return fmt.Errorf("%w: the carrier's template %q does not declare the parameter %q",
				authn.ErrInvalidConfig, t.Name, param)
		}
	}
	for _, param := range t.Params {
		if !slices.Contains(templateParams, param) {
			return fmt.Errorf("%w: the carrier's template %q declares the parameter %q, which this "+
				"module never sends", authn.ErrInvalidConfig, t.Name, param)
		}
	}
	return nil
}
