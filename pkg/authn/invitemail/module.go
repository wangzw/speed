// Package invitemail adapts the invitation notifier to the mail capability and
// renders the invitation notice.
package invitemail

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/url"
	"strings"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/mail"
	maillog "github.com/vislake/speed/pkg/mail/log"
)

// ModuleName is this module's name in the registry and its configuration namespace.
const ModuleName = "authn.invitemail"

// ConfigNamespace is where this module's input items are mounted.
const ConfigNamespace = ModuleName

const acceptancePath = "/invitations/"

func init() { core.ProcessRegistry.Register(Module()) }

// Module is this module's descriptor: it requires the mail sender and the
// localizer and provides the invitation notifier exclusively.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*mail.Sender)(nil)},
			{Token: (*i18n.Localizer)(nil)},
		},
		Provides: []core.Provision{
			{Token: (*authn.InviteNotifier)(nil), Exclusive: true},
		},
		Resources: []any{configSchema(), messageCatalog()},

		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			off, reason, err := mail.OffBySelection(reg)
			if err != nil {
				return core.Enablement{}, fmt.Errorf("%s: reading the mail carrier selection: %w", ModuleName, err)
			}
			if off {
				return core.Enablement{State: core.StateDisabled, Reason: reason}, nil
			}
			reader, err := readerOf(reg)
			if err != nil {
				return core.Enablement{}, err
			}
			if _, err := readConfig(reader); err != nil {
				return core.Enablement{}, err
			}
			return core.Enablement{State: core.StateEnabled}, nil
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			reader, err := readerOf(reg)
			if err != nil {
				return nil, err
			}
			cfg, err := readConfig(reader)
			if err != nil {
				return nil, err
			}
			sender, err := core.Resolve[mail.Sender](reg)
			if err != nil {
				return nil, fmt.Errorf("%s: taking up the mail sending capability: %w", ModuleName, err)
			}
			localizer, err := core.Resolve[i18n.Localizer](reg)
			if err != nil {
				return nil, fmt.Errorf("%s: taking up the message localizer: %w", ModuleName, err)
			}
			return newProduct(cfg, sender, localizer)
		},
	}
}

func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: the accept base URL is read from configuration, and no reader was "+
			"handed to the registry before the assembly began: %w", ModuleName, err)
	}
	return reader, nil
}

type product struct {
	baseURL   string
	sender    mail.Sender
	localizer i18n.Localizer
}

var _ authn.InviteNotifier = (*product)(nil)

func newProduct(cfg resolved, sender mail.Sender, localizer i18n.Localizer) (*product, error) {
	locales, err := catalogLocales()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ModuleName, err)
	}
	for _, locale := range locales {
		for _, key := range messageKeys {
			if _, ok := localizer.Translate(locale, key, nil); !ok {
				return nil, fmt.Errorf("%s: the catalog carries no %q for %q", ModuleName, key, locale)
			}
		}
	}
	return &product{baseURL: cfg.acceptBaseURL, sender: sender, localizer: localizer}, nil
}

func (p *product) Notify(ctx context.Context, notice authn.InviteNotice) (authn.InviteDelivery, error) {
	if p.baseURL == "" || p.sender.Name() == maillog.CarrierName {
		return authn.InviteNotConfigured, nil
	}
	acceptURL := p.acceptURL(notice.Token)
	subject, text, html, err := p.render(notice, acceptURL)
	if err != nil {
		return authn.InviteFailed, err
	}
	started := time.Now()
	receipt, err := p.sender.Send(ctx, mail.Message{
		To:      []string{notice.Email},
		Subject: subject,
		Text:    text,
		HTML:    html,
	})
	logger := log.FromContext(ctx)
	elapsed := time.Since(started).Milliseconds()
	if err != nil {
		logger.Error(ModuleName+": sending an invitation notice failed",
			"outcome", outcomeOf(err),
			"provider_id", receipt.ProviderID,
			"duration_ms", elapsed,
			"cause", err.Error())
		return authn.InviteFailed, err
	}
	logger.Info(ModuleName+": an invitation notice was sent",
		"outcome", "sent",
		"provider_id", receipt.ProviderID,
		"duration_ms", elapsed)
	return authn.InviteSent, nil
}

func (p *product) acceptURL(token string) string {
	return p.baseURL + acceptancePath + url.PathEscape(token)
}

func (p *product) render(notice authn.InviteNotice, acceptURL string) (string, string, string, error) {
	args := i18n.Args{
		"tenant":     i18n.String(notice.Tenant),
		"inviter":    i18n.String(notice.Inviter),
		"accept_url": i18n.String(acceptURL),
	}
	htmlArgs := i18n.Args{
		"tenant":     i18n.String(html.EscapeString(notice.Tenant)),
		"inviter":    i18n.String(html.EscapeString(notice.Inviter)),
		"accept_url": i18n.String(html.EscapeString(acceptURL)),
	}
	bodyKey, bodyHTMLKey := textKey, htmlKey
	if notice.Inviter != "" {
		bodyKey, bodyHTMLKey = textByKey, htmlByKey
	}
	hasDeadline := !notice.ExpiresAt.IsZero()
	if hasDeadline {
		args["expires_at"] = i18n.Time(notice.ExpiresAt.UTC())
		htmlArgs["expires_at"] = i18n.Time(notice.ExpiresAt.UTC())
	}
	for _, candidate := range localesFor(notice.Locale) {
		subject, ok := p.localizer.Translate(candidate, subjectKey, args)
		if !ok {
			continue
		}
		text, _ := p.localizer.Translate(candidate, bodyKey, args)
		bodyHTML, _ := p.localizer.Translate(candidate, bodyHTMLKey, htmlArgs)
		if hasDeadline {
			if line, ok := p.localizer.Translate(candidate, expiryTextKey, args); ok {
				text += "\n\n" + line
			}
			if line, ok := p.localizer.Translate(candidate, expiryHTMLKey, htmlArgs); ok {
				bodyHTML += line
			}
		}
		return subject, text, bodyHTML, nil
	}
	return "", "", "", fmt.Errorf("%s: no catalog carries %q for %q", ModuleName, subjectKey, notice.Locale)
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
