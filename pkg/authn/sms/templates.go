package sms

import (
	"fmt"
	"slices"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/sms"
)

// The template names this module sends its codes under, one per purpose. They
// are the configuration contract a host writes into its carrier's templates:
// the carrier maps each name, with a language, to a template number it holds, so
// the name is what this module needs covered and the number is the carrier's
// own. A carrier that does not declare one of them stops the startup, before any
// code is sent.
const (
	templateLogin  = "authn.code.login"
	templateMFA    = "authn.code.mfa"
	templateEnroll = "authn.code.enroll"
)

// templateParams are the parameter names every one of this module's templates
// substitutes: the code and its lifetime in whole minutes.
var templateParams = []string{"code", "ttl_minutes"}

// templateLocales are the languages this module writes its messages in, taken
// from the root package's two constants.
var templateLocales = []string{authn.LocaleZH, authn.LocaleEN}

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
	default:
		return ""
	}
}

// checkSender verifies that a carrier declares every template and language this
// module sends under and exactly the parameters it sends, and returns the first
// combination it gets wrong, naming it. Every failure wraps
// authn.ErrInvalidConfig.
//
// The parameter names are compared for equality, not containment. Resolve
// requires every parameter a template declares, and this module's messages carry
// only its own two, so a template declaring one the module never sends has no
// message that can satisfy it: it would pass a subset check at startup and then
// reject every send at runtime, where the failure is a metric and a warn line and
// reaches no operator. Equality names that template at startup instead.
//
// The check runs where the need is known: a carrier reports what it holds
// through Sender.Declared and knows nothing of which consumer needs what, so the
// coverage of this module's contract is this module's to check, in New, once the
// sender is taken up. A mismatch is a startup failure, not a message first
// refused when a code is sent under it.
func checkSender(declared []sms.Template) error {
	held := make(map[string]sms.Template, len(declared))
	for _, t := range declared {
		held[t.Name] = t
	}
	for _, name := range []string{templateLogin, templateMFA, templateEnroll} {
		t, ok := held[name]
		if !ok {
			return fmt.Errorf("%w: the carrier declares no template %q", authn.ErrInvalidConfig, name)
		}
		for _, locale := range templateLocales {
			if !slices.Contains(t.Locales, locale) {
				return fmt.Errorf("%w: the carrier's template %q has no code for the language %q",
					authn.ErrInvalidConfig, name, locale)
			}
		}
		for _, param := range templateParams {
			if !slices.Contains(t.Params, param) {
				return fmt.Errorf("%w: the carrier's template %q does not declare the parameter %q",
					authn.ErrInvalidConfig, name, param)
			}
		}
		for _, param := range t.Params {
			if !slices.Contains(templateParams, param) {
				return fmt.Errorf("%w: the carrier's template %q declares the parameter %q, which this "+
					"module never sends", authn.ErrInvalidConfig, name, param)
			}
		}
	}
	return nil
}
