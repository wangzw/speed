// Package emailcode is the optional email channel of the authentication capability: it renders the authn.email.verify and authn.email.reset messages and hands them to the mail capability's Sender. The code, recipient and bodies reach no log line or error text, and an uncertain delivery is wrapped in authn.ErrEmailUncertain.
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

const (
	subjectKey i18n.Key = "authn.email.verify.subject"
	textKey    i18n.Key = "authn.email.verify.body_text"
	htmlKey    i18n.Key = "authn.email.verify.body_html"

	resetSubjectKey i18n.Key = "authn.email.reset.subject"
	resetTextKey    i18n.Key = "authn.email.reset.body_text"
	resetHTMLKey    i18n.Key = "authn.email.reset.body_html"
)

var messageKeys = []i18n.Key{subjectKey, textKey, htmlKey}

var resetKeys = []i18n.Key{resetSubjectKey, resetTextKey, resetHTMLKey}

func init() { core.ProcessRegistry.Register(Module()) }

// Module is this module's descriptor. It requires the mail capability's Sender and declares the i18n Localizer so it is constructed after it. The reset channel is offered exactly when the compiled-in catalog carries its messages in every language, decided at registration.
func Module() core.Module {
	providesReset := catalogCarriesReset(localesFS, catalogPattern)
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*mail.Sender)(nil)},
			{Token: (*i18n.Localizer)(nil)},
		},
		Provides:  provisions(providesReset),
		Resources: []any{messageCatalog()},

		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			off, reason, err := mail.OffBySelection(reg)
			if err != nil {
				return core.Enablement{}, fmt.Errorf("%s: reading the mail carrier selection: %w", ModuleName, err)
			}
			if off {
				return core.Enablement{State: core.StateDisabled, Reason: reason}, nil
			}
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

func provisions(providesReset bool) []core.Provision {
	out := []core.Provision{{Token: (*authn.EmailCodeSender)(nil), Exclusive: true}}
	if providesReset {
		out = append(out, core.Provision{Token: (*authn.EmailResetCodeSender)(nil), Exclusive: true})
	}
	return out
}

type product struct {
	sender    mail.Sender
	localizer i18n.Localizer
}

var (
	_ authn.EmailCodeSender      = (*product)(nil)
	_ authn.EmailResetCodeSender = (*product)(nil)
)

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

// SendCode renders the code into the verifying messages and hands the message to the mail capability. The lifetime is whole minutes rounded up and never below one, and a carrier error is returned as classified.
func (p *product) SendCode(ctx context.Context, to, code string, ttl time.Duration, locale string) error {
	return p.send(ctx, "a verification code", messageKeys, to, code, ttl, locale)
}

// SendResetCode renders the code into the reset messages and hands the message to the mail capability; its contract is SendCode's.
func (p *product) SendResetCode(ctx context.Context, to, code string, ttl time.Duration, locale string) error {
	return p.send(ctx, "a password reset code", resetKeys, to, code, ttl, locale)
}

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

func publishUncertain(err error) error {
	if errors.Is(err, mail.ErrUncertain) {
		return fmt.Errorf("%w: %w", authn.ErrEmailUncertain, err)
	}
	return err
}

// CertifiesNotSent reports whether err certifies that no message was handed to the carrier. The mail capability's definite failures do; an uncertain one and an unclassified error do not.
func (*product) CertifiesNotSent(err error) bool {
	return errors.Is(err, mail.ErrThrottled) || errors.Is(err, mail.ErrRejected)
}

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

func ttlMinutes(ttl time.Duration) int {
	minutes := int((ttl + time.Minute - 1) / time.Minute)
	if minutes < 1 {
		minutes = 1
	}
	return minutes
}

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
