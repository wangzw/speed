// Package emailcode is the optional email channel of the authentication
// capability: it adapts the root package's EmailCodeSender to a mail carrier.
//
// It renders the verification code into the authn.email.verify messages, one
// subject and a plain-text and an HTML body, and hands the finished message to
// the top-level mail capability's Sender. It holds no code of its own: the
// login module draws the code, keeps the pending record and checks the answer,
// and this module only turns a SendCode call into one mail.Message. It is
// registered as authn.emailcode and delivers EmailCodeSender exclusively, so a
// command tree names it or leaves the email self-service face unavailable.
//
// The code, the recipient and the message bodies reach no log line, no error
// text and no metric. Each send is logged once with its outcome, the carrier's
// receipt and its duration, so a failed or silently dropped delivery can be
// traced; a carrier failure is passed through as the mail capability classified
// it, with an uncertain one also wrapped in authn.ErrEmailUncertain, so the
// caller answers one 503 whatever happened and can still tell whether the message
// may have left.
package emailcode

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/mail"
)

// ModuleName is this module's name in the registry.
const ModuleName = "authn.emailcode"

// The message keys the module renders. They are namespaced by the capability so
// no other module's catalog can collide with one. The verify set carries a code
// to prove an address; the reset set carries one to prove the holder of an
// address during an anonymous password reset.
const (
	subjectKey i18n.Key = "authn.email.verify.subject"
	textKey    i18n.Key = "authn.email.verify.body_text"
	htmlKey    i18n.Key = "authn.email.verify.body_html"

	resetSubjectKey i18n.Key = "authn.email.reset.subject"
	resetTextKey    i18n.Key = "authn.email.reset.body_text"
	resetHTMLKey    i18n.Key = "authn.email.reset.body_html"
)

// messageKeys is the full set of messages the verifying channel renders, in the
// order the startup check reads them.
var messageKeys = []i18n.Key{subjectKey, textKey, htmlKey}

// resetKeys is the full set of messages the reset channel renders, in the order
// the startup check reads them.
var resetKeys = []i18n.Key{resetSubjectKey, resetTextKey, resetHTMLKey}

// init registers this module with the process registry, so a host that imports
// the package can take it into a command by name.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is this module's descriptor.
//
// It requires the mail capability's Sender: a run that names it hands a mail
// carrier into the same command, and without one there is nothing to deliver
// through. The capability is not exclusive here — the mail carrier's own module
// owns that claim, so a command names exactly one carrier and this module reads
// it. The i18n Localizer is taken up in New rather than declared, because the
// module's messages are its own catalog and its absence has to be reported with
// the key it cannot render. The module declares no configuration item: the mail
// carrier's section carries everything it needs, and the catalog is compiled in
// as a resource.
//
// The reset channel is offered exactly when the compiled-in catalog carries its
// three leaves in every language. The decision is made here, at registration,
// because it is a fact about the catalog this package ships and not about a
// request or an assembly: a build whose catalog lacks the reset message offers
// only the verifying channel and starts normally, and every command tree that
// names this module sees the same descriptor.
func Module() core.Module {
	providesReset := catalogCarriesReset(localesFS, catalogPattern)
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*mail.Sender)(nil)},
		},
		Provides:  provisions(providesReset),
		Resources: []any{messageCatalog()},

		Prepare: func(context.Context, *core.Registry) (core.Enablement, error) {
			return core.Enablement{State: core.StateEnabled}, nil
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			sender, err := core.Resolve[mail.Sender](reg)
			if err != nil {
				return nil, fmt.Errorf("%s: taking up the mail sending capability: %w", ModuleName, err)
			}
			localizer, err := core.Resolve[i18n.Localizer](reg)
			if err != nil {
				return nil, fmt.Errorf("%s: taking up the message localizer: %w", ModuleName, err)
			}
			return newProduct(sender, localizer, providesReset)
		},
	}
}

// provisions is what the module delivers: the verifying channel always, and the
// reset channel when the catalog carries its messages. Each is exclusive, so a
// command tree names one provider per seam and two conflict in resolution.
func provisions(providesReset bool) []core.Provision {
	out := []core.Provision{{Token: (*authn.EmailCodeSender)(nil), Exclusive: true}}
	if providesReset {
		out = append(out, core.Provision{Token: (*authn.EmailResetCodeSender)(nil), Exclusive: true})
	}
	return out
}

// product is the module's instance: the mail seam and the catalog behind it.
type product struct {
	sender    mail.Sender
	localizer i18n.Localizer
}

var (
	_ authn.EmailCodeSender      = (*product)(nil)
	_ authn.EmailResetCodeSender = (*product)(nil)
)

// newProduct checks that every message the module renders is carried by every
// locale its catalog declares, then returns the sender. The reset messages are
// checked only when the module offers the reset channel; a build whose catalog
// lacks them offers only the verifying channel, and its absence is the decision
// the descriptor already carries.
//
// The check runs at startup because a missing message is a defect of this
// package and not a fact about a request: without it the first code sent in that
// language would reach the carrier as the key itself, and the recipient would
// receive no usable code. A localizer that does not carry the catalog is named
// with the locale and key it lacks, never with a value from the message.
func newProduct(sender mail.Sender, localizer i18n.Localizer, providesReset bool) (*product, error) {
	locales, err := catalogLocales()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ModuleName, err)
	}
	keys := messageKeys
	if providesReset {
		keys = append(append([]i18n.Key(nil), messageKeys...), resetKeys...)
	}
	for _, locale := range locales {
		for _, key := range keys {
			if _, ok := localizer.Translate(locale, key, nil); !ok {
				return nil, fmt.Errorf("%s: the catalog carries no %q for %q", ModuleName, key, locale)
			}
		}
	}
	return &product{sender: sender, localizer: localizer}, nil
}

// SendCode renders the code into the module's messages and hands the result to
// the mail capability.
//
// The lifetime is rendered in whole minutes, rounded up and never below one, the
// same unit the SMS templates take. The error a carrier answers is returned as
// it stands: whether the message was throttled, rejected or left in an unknown
// state is the caller's to collapse into one answer, and this module adds
// nothing a client could tell the three apart by.
func (p *product) SendCode(ctx context.Context, to, code string, ttl time.Duration, locale string) error {
	return p.send(ctx, "a verification code", messageKeys, to, code, ttl, locale)
}

// SendResetCode renders the code into the module's reset messages and hands the
// result to the mail capability. Its contract is SendCode's: the same lifetime
// unit, the same fallback through the catalog, the same pass-through of a
// carrier error, and no code, recipient or body in any log line or error text.
func (p *product) SendResetCode(ctx context.Context, to, code string, ttl time.Duration, locale string) error {
	return p.send(ctx, "a password reset code", resetKeys, to, code, ttl, locale)
}

// send renders one code into the messages of keys and hands the result to the
// mail capability. The kind names the send in the one log line without carrying
// anything about its content.
func (p *product) send(ctx context.Context, kind string, keys []i18n.Key, to, code string, ttl time.Duration, locale string) error {
	subject, text, html, err := p.render(code, ttl, locale, keys)
	if err != nil {
		return err
	}
	started := time.Now()
	receipt, err := p.sender.Send(ctx, mail.Message{
		To:      []string{to},
		Subject: subject,
		Text:    text,
		HTML:    html,
	})
	logger := log.FromContext(ctx)
	elapsed := time.Since(started).Milliseconds()
	if err != nil {
		logger.Error(ModuleName+": sending "+kind+" failed",
			"outcome", outcomeOf(err),
			"provider_id", receipt.ProviderID,
			"duration_ms", elapsed,
			"cause", err.Error())
		return publishUncertain(err)
	}
	logger.Info(ModuleName+": "+kind+" was sent",
		"outcome", "sent",
		"provider_id", receipt.ProviderID,
		"duration_ms", elapsed)
	return nil
}

// publishUncertain wraps a mail failure whose delivery is not known in
// authn.ErrEmailUncertain, which is the seam's own word for that class. Every
// other failure is returned as it stands: a throttled call and a refused message
// both certify that nothing was handed to the carrier.
func publishUncertain(err error) error {
	if errors.Is(err, mail.ErrUncertain) {
		return fmt.Errorf("%w: %w", authn.ErrEmailUncertain, err)
	}
	return err
}

// CertifiesNotSent reports whether err certifies that no message was handed to
// the carrier. The mail capability's two definite failures do; an uncertain one,
// and any error it did not classify, does not. The login module reads this
// through its own optional interface, so a sender that does not publish it is
// treated as uncertain and keeps the budget its attempt reserved.
func (*product) CertifiesNotSent(err error) bool {
	return errors.Is(err, mail.ErrThrottled) || errors.Is(err, mail.ErrRejected)
}

// outcomeOf names the carrier answer the mail capability classified, so a
// failure log says whether the relay throttled, rejected or left the send
// unknown. The code, the recipient and the message bodies never reach the log.
func outcomeOf(err error) string {
	switch {
	case errors.Is(err, mail.ErrThrottled):
		return "throttled"
	case errors.Is(err, mail.ErrRejected):
		return "rejected"
	case errors.Is(err, mail.ErrUncertain):
		return "uncertain"
	default:
		return "failed"
	}
}

// render draws the three messages in keys for a code in the language the caller
// asked for. A locale the catalog does not carry falls back by primary subtag
// and then to English, so a request for a language the deployment does not
// translate still reaches the recipient with a code rather than the message key.
// The error it answers names the subject key and the locale, never the code.
func (p *product) render(code string, ttl time.Duration, locale string, keys []i18n.Key) (string, string, string, error) {
	args := i18n.Args{
		"code":        i18n.String(code),
		"ttl_minutes": i18n.Int(ttlMinutes(ttl)),
	}
	for _, candidate := range localesFor(locale) {
		subject, ok := p.localizer.Translate(candidate, keys[0], args)
		if !ok {
			continue
		}
		text, _ := p.localizer.Translate(candidate, keys[1], args)
		html, _ := p.localizer.Translate(candidate, keys[2], args)
		return subject, text, html, nil
	}
	return "", "", "", fmt.Errorf("%s: no catalog carries %q for %q", ModuleName, keys[0], locale)
}

// ttlMinutes renders a lifetime in whole minutes, rounded up and never below
// one, which is the unit the messages interpolate.
func ttlMinutes(ttl time.Duration) int {
	minutes := int((ttl + time.Minute - 1) / time.Minute)
	if minutes < 1 {
		minutes = 1
	}
	return minutes
}

// localesFor lists the locales to try for a request, most specific first: the
// requested tag as written, the catalog's language when only its primary subtag
// matches, and English as the last resort. An empty request names no language
// and falls to English.
func localesFor(requested string) []i18n.Locale {
	var out []i18n.Locale
	seen := map[i18n.Locale]bool{}
	add := func(locale i18n.Locale) {
		if locale == "" || seen[locale] {
			return
		}
		seen[locale] = true
		out = append(out, locale)
	}
	add(i18n.Locale(requested))
	add(catalogLocale(requested))
	add("en-US")
	return out
}

// catalogLocale is the locale the module's catalog carries for a primary subtag,
// or the empty locale when it carries none.
func catalogLocale(tag string) i18n.Locale {
	folded := strings.ReplaceAll(strings.ToLower(tag), "_", "-")
	primary, _, _ := strings.Cut(folded, "-")
	switch primary {
	case "zh":
		return "zh-CN"
	case "en":
		return "en-US"
	default:
		return ""
	}
}
