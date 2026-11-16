// Package invitemail is the optional email channel of the invitation
// capability: it adapts the root package's InviteNotifier to a mail carrier.
//
// It renders an invitation notice — the tenant the invitation grants, the
// acceptance link and the invitation's deadline — into the
// authn.invitemail messages, one subject and a plain-text and an HTML body, and
// hands the finished message to the top-level mail capability's Sender. It
// holds no invitation state: the invitation module issues the invitation,
// decides the deadline and hands over the token once, and this module only
// turns one Notify call into one mail.Message. It is registered as
// authn.invitemail and delivers InviteNotifier exclusively, so a command tree
// names it or supplies its own notifier.
//
// The link is the configured accept-base-url plus the public acceptance landing
// /invitations/<token>, the path the demo host serves the invitation page on.
// The token is the whole credential of acceptance: it, the recipient and the
// message bodies reach no log line, no error text and no metric. Each send is
// logged once with its outcome, the carrier's receipt and its duration, so a
// failed or silently dropped delivery can be traced without exposing the link.
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

// ModuleName is this module's name in the registry, and the name its
// configuration namespace takes.
const ModuleName = "authn.invitemail"

// ConfigNamespace is where this module's input items are mounted.
const ConfigNamespace = ModuleName

// acceptancePath is the public landing an invitation link points at: the host
// serves the acceptance page at /invitations/:token, and the notice joins that
// path to the configured origin. The token is escaped as one path segment, so a
// token never changes the shape of the route.
const acceptancePath = "/invitations/"

// init registers this module with the process registry, so a host that imports
// the package can take it into a command by name.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is this module's descriptor.
//
// It requires the mail capability's Sender: a run that names it hands a mail
// carrier into the same command, and without one there is nothing to deliver
// through. The capability is not exclusive here — the mail carrier's own module
// owns that claim, so a command names exactly one carrier and this module reads
// it. The i18n Localizer is declared so this module is constructed after the
// one delivering it, and taken up in New, where an absent one is reported with
// the key the module cannot render.
//
// It declares one configuration item, accept-base-url, read in Prepare so an
// origin the code cannot join a link to stops the startup. Before that, Prepare
// reads mail.carrier: a run that selects none switches the mail capability off,
// and this module stands itself down with the same reason rather than failing
// the assembly over a sending seam no module delivers. An empty base URL is a
// legal deployment that carries the module but sends no mail: Prepare leaves it
// enabled, and Notify reports InviteNotConfigured without reaching the carrier.
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

// readerOf takes up the configuration reader the host supplied before the
// assembly began.
func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: the accept base URL is read from configuration, and no reader was "+
			"handed to the registry before the assembly began: %w", ModuleName, err)
	}
	return reader, nil
}

// product is the module's instance: the configured origin, the mail seam and
// the catalog behind it.
type product struct {
	baseURL   string
	sender    mail.Sender
	localizer i18n.Localizer
}

var _ authn.InviteNotifier = (*product)(nil)

// newProduct checks that every message the module renders is carried by every
// locale its catalog declares, then returns the notifier. The check runs at
// startup because a missing message is a defect of this package and not a fact
// about a request: without it the first notice in that language would reach the
// carrier as the key itself. A localizer that does not carry the catalog is
// named with the locale and key it lacks, never with a value from the notice.
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

// Notify renders one invitation notice and hands it to the mail capability.
//
// An unconfigured channel reports InviteNotConfigured with no error and reaches
// no carrier. So does the log carrier: it writes a message to the process log
// and reaches no recipient, so for an invitation it is not a delivery channel.
// The issue answer therefore hands the issuer the acceptance token, as
// InviteNotConfigured asks it to, instead of withholding the link behind a log
// line only the deployment can read. Other mail (a verification code) keeps
// that carrier's own behaviour; this module holds the rule for invitations
// alone.
//
// A configured channel renders in the language the notice names, falling back
// through the catalog, and reports InviteSent or InviteFailed. The carrier's
// error is returned as it stands: whether the message was throttled, rejected
// or left in an unknown state is the caller's to collapse, and this module adds
// nothing a client could tell the three apart by.
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

// acceptURL joins the configured origin to the public acceptance path and one
// token. The origin was normalized without a trailing slash in resolve, so the
// join never writes a doubled slash.
func (p *product) acceptURL(token string) string {
	return p.baseURL + acceptancePath + url.PathEscape(token)
}

// render draws the messages for one notice in the language it names. A locale
// the catalog does not carry falls back by primary subtag and then to English,
// so a request for a language the deployment does not translate still reaches
// the recipient with a usable link rather than the message key. The error it
// answers names the subject key and the locale, never the token or the link.
//
// The body names the inviter when the notice carries a display name and falls
// back to the impersonal form when it does not. The deadline is rendered by the
// message's own time segment: the notice is not a page, and the recipient may
// have no account and no time zone, so the layout says UTC and Translate renders
// with no zone, which reads as UTC. The request's time zone is never consulted,
// and a notice whose caller does not know the deadline omits the sentence.
//
// The HTML body and the HTML deadline line interpolate HTML-escaped values: the
// tenant name and the inviter's display name are free text a person typed, and
// the acceptance link is built from configuration and a token, so none of them
// may reach the markup as markup. The plain-text body and the subject carry the
// same values as written, because neither is parsed as HTML.
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

// catalogLocale is the locale the module's catalog carries for a primary
// subtag, or the empty locale when it carries none.
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

// outcomeOf names the carrier answer the mail capability classified, so a
// failure log says whether the carrier throttled, rejected or left the send
// unknown. The token, the recipient and the message bodies never reach the log.
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
