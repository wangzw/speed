package delivery

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/mail"
	"github.com/vislake/speed/pkg/notification"
)

// The parts a channel's text is rendered from. A part is keyed under
// "<type_key>.<channel>.<part>", in the catalog the declaring business module
// carries; the engine falls back to the run's default locale and then to the key
// itself, so a render is never empty.
const (
	partTitle    = "title"
	partBody     = "body"
	partSubject  = "subject"
	partBodyText = "body_text"
	partBodyHTML = "body_html"
	partLink     = "link"
	partTemplate = "template"
)

// destinationParam is the parameter a dispatch carries an external channel's
// destination in: an E.164 phone number for sms, an RFC 5322 address for email.
// Where the destination comes from is not the design's to say — a recipient is an
// account identifier, and this module reaches no account store — so it travels in
// the dispatch's parameters under this name; a dispatch that names none fails
// that channel without reaching the vendor.
const destinationParam = "to"

// contentKey builds the catalog key one part of one channel's text is carried
// under.
func contentKey(typeKey string, channel notification.Channel, part string) i18n.Key {
	return i18n.Key(typeKey + "." + string(channel) + "." + part)
}

// message renders one in-app message for a dispatch, in the recipient's
// language. The title and the body are rendered from the declaring module's
// catalog; the link is written only when the catalog carries one, so an absent
// entry does not put a key string into a field a client would open.
func (p *pipeline) message(ctx context.Context, d notification.Dispatch, spec notification.TypeSpec) notification.Message {
	rctx := i18n.WithLocale(ctx, i18n.Locale(d.Locale))
	args := renderArgs(d.Params)
	linkKey := contentKey(spec.Key, notification.ChannelInApp, partLink)
	msg := notification.Message{
		TypeKey: spec.Key,
		Group:   spec.Group,
		Title:   p.localizer.Localize(rctx, contentKey(spec.Key, notification.ChannelInApp, partTitle), args),
		Body:    p.localizer.Localize(rctx, contentKey(spec.Key, notification.ChannelInApp, partBody), args),
		Params:  visibleParams(d.Params),
	}
	if link := p.localizer.Localize(rctx, linkKey, args); link != string(linkKey) {
		msg.Link = link
	}
	return msg
}

// emailMessage renders one email for a dispatch, in the recipient's language.
// The subject and the plain-text body are rendered from the declaring module's
// catalog; the HTML body is written only when the catalog carries one, so an
// absent entry leaves the message with its plain-text body rather than putting a
// key string into a field a client renders as markup.
//
// From is left empty: an empty Message.From takes the carrier's own configured
// default sender, and this module names no sender of its own. Each attempt builds
// its own value — a mail.Message can be sent only once, because its attachments
// are readers — and this module attaches no file, so nothing here is spent.
func (p *pipeline) emailMessage(ctx context.Context, d notification.Dispatch, spec notification.TypeSpec, to, dedupeID string) mail.Message {
	rctx := i18n.WithLocale(ctx, i18n.Locale(d.Locale))
	args := renderArgs(d.Params)
	htmlKey := contentKey(spec.Key, notification.ChannelEmail, partBodyHTML)
	msg := mail.Message{
		To: []string{to},
		Subject: p.localizer.Localize(rctx,
			contentKey(spec.Key, notification.ChannelEmail, partSubject), args),
		Text: p.localizer.Localize(rctx,
			contentKey(spec.Key, notification.ChannelEmail, partBodyText), args),
		DedupeID: dedupeID,
	}
	if html := p.localizer.Localize(rctx, htmlKey, args); html != string(htmlKey) {
		msg.HTML = html
	}
	return msg
}

// smsTemplate reports the template identifier a dispatch's sms message goes out
// under: the catalog entry for "<type_key>.sms.template", which carries a
// template name and not prose. A type whose catalog declares none fails its sms
// channel before the vendor is reached.
func (p *pipeline) smsTemplate(ctx context.Context, d notification.Dispatch, spec notification.TypeSpec) (string, error) {
	key := contentKey(spec.Key, notification.ChannelSMS, partTemplate)
	rctx := i18n.WithLocale(ctx, i18n.Locale(d.Locale))
	rendered := p.localizer.Localize(rctx, key, renderArgs(d.Params))
	if rendered == string(key) {
		return "", fmt.Errorf("%w: the type %q declares no sms template", notification.ErrInvalidRequest, spec.Key)
	}
	return rendered, nil
}

// renderArgs turns a dispatch's parameters into the values the engine
// interpolates. The whitelist has already been enforced, so every parameter here
// is one the type admits.
func renderArgs(params map[string]any) i18n.Args {
	args := make(i18n.Args, len(params))
	for name, value := range params {
		args[name] = renderValue(value)
	}
	return args
}

// renderValue maps one parameter onto the engine's typed value: a string into
// text, an integer into a whole number, a number with a fraction into a float, a
// time into an instant the engine formats for the locale. Anything else is
// written as its default text.
func renderValue(v any) i18n.Value {
	switch t := v.(type) {
	case string:
		return i18n.String(t)
	case bool:
		return i18n.String(strconv.FormatBool(t))
	case int:
		return i18n.Int(t)
	case int64:
		return i18n.Int(int(t))
	case float64:
		return i18n.Float(t)
	case json.Number:
		if n, err := t.Int64(); err == nil {
			return i18n.Int(int(n))
		}
		if f, err := t.Float64(); err == nil {
			return i18n.Float(f)
		}
		return i18n.String(t.String())
	case time.Time:
		return i18n.Time(t)
	case nil:
		return i18n.String("")
	default:
		return i18n.String(fmt.Sprint(v))
	}
}

// visibleParams copies a dispatch's parameters for the message the client may
// reuse. The whitelist has already been enforced, so the copy is the visible
// set.
func visibleParams(params map[string]any) map[string]any {
	if len(params) == 0 {
		return nil
	}
	out := make(map[string]any, len(params))
	for name, value := range params {
		out[name] = value
	}
	return out
}

// stringifyParams turns a dispatch's parameters into the strings an sms template
// substitutes. A parameter that is missing, empty or cannot be stringified fails
// the channel before the vendor is reached: no empty string and no placeholder
// goes onto the wire.
func stringifyParams(params map[string]any) (map[string]string, error) {
	if len(params) == 0 {
		return nil, fmt.Errorf("%w: the dispatch carries no parameter for the sms template", notification.ErrInvalidRequest)
	}
	out := make(map[string]string, len(params))
	for name, value := range params {
		text, err := stringify(value)
		if err != nil {
			return nil, fmt.Errorf("%w: the sms parameter %q: %w", notification.ErrInvalidRequest, name, err)
		}
		out[name] = text
	}
	return out, nil
}

// stringify renders one parameter as a stable, non-empty text: a string as it
// is, an integer or a boolean in its decimal or lower-case text, a time in RFC
// 3339. A missing, empty or non-stringifiable value is an error.
func stringify(v any) (string, error) {
	switch t := v.(type) {
	case string:
		if t == "" {
			return "", fmt.Errorf("the value is empty")
		}
		return t, nil
	case bool:
		return strconv.FormatBool(t), nil
	case int:
		return strconv.Itoa(t), nil
	case int64:
		return strconv.FormatInt(t, 10), nil
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), nil
	case json.Number:
		return t.String(), nil
	case time.Time:
		return t.UTC().Format(time.RFC3339), nil
	case nil:
		return "", fmt.Errorf("the value is empty")
	default:
		return "", fmt.Errorf("a value of type %T cannot be stringified", v)
	}
}

// destination reads an external channel's destination out of a dispatch's
// parameters.
func destination(params map[string]any) (string, error) {
	value, ok := params[destinationParam]
	if !ok {
		return "", fmt.Errorf("%w: the dispatch names no destination under %q",
			notification.ErrInvalidRequest, destinationParam)
	}
	to, err := stringify(value)
	if err != nil {
		return "", fmt.Errorf("%w: the destination: %w", notification.ErrInvalidRequest, err)
	}
	return to, nil
}

// reason renders this module's own failure reason for the ledger, in the run's
// default language. A key no catalog carries falls back to the given English
// text. A vendor's own error text is not put through here: it is recorded as the
// vendor wrote it.
func (p *pipeline) reason(ctx context.Context, key, fallback string, args i18n.Args) string {
	rendered := p.localizer.Localize(ctx, i18n.Key(key), args)
	if rendered == key {
		return fallback
	}
	return rendered
}
