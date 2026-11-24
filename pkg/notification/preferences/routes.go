package preferences

import (
	"context"
	"errors"
	"net/http"

	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/notification"
)

func (p *product) listTypes(_ context.Context, _ struct{}) (typesBody, error) {
	specs := p.taxonomy.Types()
	views := make([]NotificationType, len(specs))
	for i, spec := range specs {
		views[i] = typeViewOf(spec)
	}
	return typesBody{Types: views}, nil
}

func (p *product) listPreferences(ctx context.Context, _ struct{}) (preferencesBody, error) {
	recipient, _ := recipientFrom(ctx)
	stored, err := p.loadStored(ctx, recipient)
	if err != nil {
		return preferencesBody{}, withVary(p.answer(ctx, "reading the preferences", err))
	}
	specs := p.taxonomy.Types()
	views := make([]NotificationPreference, len(specs))
	for i, spec := range specs {
		channels, explicit := effectiveChannels(spec, stored)
		views[i] = NotificationPreference{
			TypeKey:  spec.Key,
			Group:    spec.Group,
			Channels: channelNames(channels),
			Explicit: explicit,
		}
	}
	return preferencesBody{Preferences: views}, nil
}

// effectiveChannels computes the effective set of one type for the recipient: an
// unsubscribable type takes all its default channels, and a subscribable one
// takes its stored row when there is one and its defaults otherwise.
func effectiveChannels(spec notification.TypeSpec, stored map[string][]notification.Channel) ([]notification.Channel, bool) {
	if !spec.Unsubscribable {
		return canonicalChannels(spec.DefaultChannels), false
	}
	if row, ok := stored[spec.Key]; ok {
		return canonicalChannels(row), true
	}
	return canonicalChannels(spec.DefaultChannels), false
}

func (p *product) setPreference(ctx context.Context, in SetPreferenceInput) (preferenceSetBody, error) {
	if in.Enabled == nil {
		return preferenceSetBody{}, p.refusal(ctx, http.StatusBadRequest, badRequestCode)
	}

	channel := notification.Channel(in.Channel)
	if !notification.KnownChannel(channel) {
		return preferenceSetBody{}, p.refusal(ctx, http.StatusBadRequest, badRequestCode)
	}

	spec, ok := p.taxonomy.Type(in.TypeKey)
	if !ok {
		return preferenceSetBody{}, p.refusal(ctx, http.StatusBadRequest, badRequestCode)
	}
	if !spec.Unsubscribable {
		return preferenceSetBody{}, p.refusal(ctx, http.StatusConflict, notUnsubscribableCode)
	}

	recipient, ok := recipientFrom(ctx)
	if !ok || recipient == "" {
		// A write names a recipient; the host's identity and authorization side
		// supplies it on the request context, and a request that reaches this
		// handler without one cannot be acted on.
		return preferenceSetBody{}, p.refusal(ctx, http.StatusBadRequest, badRequestCode)
	}

	current, err := p.Effective(ctx, recipient, in.TypeKey)
	if err != nil {
		return preferenceSetBody{}, p.answer(ctx, "reading the preference", err)
	}
	target := applyToggle(current, channel, *in.Enabled)
	if sameSet(target, current) {
		return preferenceSetBody{TypeKey: in.TypeKey, Channels: channelStrings(current)}, nil
	}

	written, err := p.Set(ctx, recipient, in.TypeKey, target)
	if err != nil {
		return preferenceSetBody{}, p.answer(ctx, "writing the preference", err)
	}
	return preferenceSetBody{TypeKey: in.TypeKey, Channels: channelStrings(written)}, nil
}

// withVary adds the route's declared Vary header to a failure, so a
// refusal carries the representation key the success answer does.
func withVary(err error) error {
	var fault speedhttp.Fault
	if !errors.As(err, &fault) {
		return err
	}
	fault.Headers = append(fault.Headers, speedhttp.HeaderValue{Name: "Vary", Value: "Accept-Language"})
	return fault
}

func (p *product) answer(ctx context.Context, op string, err error) error {
	switch {
	case errors.Is(err, notification.ErrUnknownType),
		errors.Is(err, notification.ErrUnknownChannel):
		return p.refusal(ctx, http.StatusBadRequest, badRequestCode)
	case errors.Is(err, notification.ErrNotUnsubscribable):
		return p.refusal(ctx, http.StatusConflict, notUnsubscribableCode)
	default:
		p.logger.Error(ModuleName+": "+op+" failed", "cause", err.Error())
		return p.refusal(ctx, http.StatusServiceUnavailable, "")
	}
}

func (p *product) refusal(ctx context.Context, status int, code string) error {
	problem := speedhttp.Fault{Status: status}
	if code == "" {
		problem.Title = p.text(ctx, i18n.Key("notification.preferences.unavailable"+titleSuffix), "Service Unavailable")
		return problem
	}
	problem.Type = typePrefix + code
	problem.Title = p.text(ctx, i18n.Key("notification.preferences."+code+titleSuffix), englishTitles[code])
	problem.Detail = p.text(ctx, i18n.Key("notification.preferences."+code+detailSuffix), englishDetails[code])
	return problem
}
