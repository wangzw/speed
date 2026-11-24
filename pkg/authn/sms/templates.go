package sms

import (
	"fmt"
	"slices"
	"strings"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/sms"
)

const (
	templateLogin  = "authn.code.login"
	templateMFA    = "authn.code.mfa"
	templateEnroll = "authn.code.enroll"
	templateReset  = "authn.code.reset"
)

var templateParams = []string{"code", "ttl_minutes"}

var requiredTemplates = []string{templateLogin, templateMFA, templateEnroll}

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

func templateLocales(declared []sms.Template) map[string][]string {
	held := make(map[string]sms.Template, len(declared))
	for _, t := range declared {
		held[t.Name] = t
	}
	out := make(map[string][]string, len(requiredTemplates)+1)
	for _, name := range requiredTemplates {
		out[name] = append([]string(nil), held[name].Locales...)
	}
	if t, ok := held[templateReset]; ok {
		out[templateReset] = append([]string(nil), t.Locales...)
	}
	return out
}

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

func primarySubtag(tag string) string {
	first, _, _ := strings.Cut(tag, "-")
	return strings.ToLower(first)
}

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
	// A declared reset template must satisfy the same parameters, languages and default-language rule, or the startup fails naming it; declaring none keeps the channel closed.
	if t, ok := held[templateReset]; ok {
		if err := checkTemplate(t, defaultLocale); err != nil {
			return err
		}
	}
	return nil
}

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
