package delivery

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/mail"
	"github.com/vislake/speed/pkg/notification"
)

const (
	partTitle    = "title"
	partBody     = "body"
	partSubject  = "subject"
	partBodyText = "body_text"
	partBodyHTML = "body_html"
	partLink     = "link"
	partTemplate = "template"
)

// destinationParam is the parameter a dispatch carries an external
// channel's destination in: an E.164 phone number for sms, an RFC 5322
// address for email.
const destinationParam = "to"

func contentKey(typeKey string, channel notification.Channel, part string) i18n.Key {
	return i18n.Key(typeKey + "." + string(channel) + "." + part)
}

func (p *pipeline) localeContext(ctx context.Context, d notification.Dispatch) context.Context {
	return i18n.WithLocale(ctx, i18n.Locale(d.Locale))
}

func (p *pipeline) message(ctx context.Context, d notification.Dispatch, spec notification.TypeSpec) notification.Message {
	lctx := p.localeContext(ctx, d)
	marked := i18n.WithTimeRender(lctx, i18n.TimeRenderMarked)
	args := renderArgs(d.Params)
	linkKey := contentKey(spec.Key, notification.ChannelInApp, partLink)
	msg := notification.Message{
		TypeKey: spec.Key,
		Group:   spec.Group,
		Title:   p.localizer.Localize(marked, contentKey(spec.Key, notification.ChannelInApp, partTitle), args),
		Body:    p.localizer.Localize(marked, contentKey(spec.Key, notification.ChannelInApp, partBody), args),
		Params:  visibleParams(d.Params),
	}
	if link := p.localizer.Localize(lctx, linkKey, args); link != string(linkKey) {
		msg.Link = link
	}
	p.warnUnresolvableMarkers(msg)
	return msg
}

func (p *pipeline) emailMessage(ctx context.Context, d notification.Dispatch, spec notification.TypeSpec, to, dedupeID, zone string) mail.Message {
	rctx := i18n.WithTimeZone(i18n.WithLocale(ctx, i18n.Locale(d.Locale)), zone)
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

// smsTemplate reports the template identifier a dispatch's sms message
// goes out under: the catalog entry for "<type_key>.sms.template", which
// carries a template name and not prose.
func (p *pipeline) smsTemplate(ctx context.Context, d notification.Dispatch, spec notification.TypeSpec) (string, error) {
	key := contentKey(spec.Key, notification.ChannelSMS, partTemplate)
	// An sms template carries a template identifier, not prose, and the
	// parameters travel stringified; no time segment is rendered here, so this
	// context names only the language.
	rctx := i18n.WithLocale(ctx, i18n.Locale(d.Locale))
	rendered := p.localizer.Localize(rctx, key, renderArgs(d.Params))
	if rendered == string(key) {
		return "", fmt.Errorf("%w: the type %q declares no sms template", notification.ErrInvalidRequest, spec.Key)
	}
	return rendered, nil
}

func renderArgs(params map[string]any) i18n.Args {
	args := make(i18n.Args, len(params))
	for name, value := range params {
		args[name] = renderValue(value)
	}
	return args
}

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

var timeMarker = regexp.MustCompile("\uE000time:([^\uE001]+)\uE001")

func (p *pipeline) warnUnresolvableMarkers(msg notification.Message) {
	for _, text := range []string{msg.Title, msg.Body} {
		for _, match := range timeMarker.FindAllStringSubmatch(text, -1) {
			name := match[1]
			if _, ok := msg.Params[name]; !ok {
				p.logger.Warn(ModuleName+": a rendered time marker names a parameter the message does not carry",
					"type_key", msg.TypeKey, "parameter", name)
			}
		}
	}
}

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

// stringify renders one parameter as a stable, non-empty text: a string
// as it is, an integer or a boolean in its decimal or lower-case text, a
// time in RFC 3339.
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

func (p *pipeline) reason(ctx context.Context, key, fallback string, args i18n.Args) string {
	rendered := p.localizer.Localize(ctx, i18n.Key(key), args)
	if rendered == key {
		return fallback
	}
	return rendered
}
