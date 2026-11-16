package preferences

import (
	"context"
	"errors"
	"net/http"

	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/notification"
)

// listTypes answers GET /types: the taxonomy itself, one list every recipient
// shares. It is a read with no tenant of its own and it refuses nothing of this
// module's own; the body does not vary by language, but the endpoint keys its
// representation by the negotiated language — this module's error responses are
// written in it — so the declared Vary header keeps a shared cache from serving
// one language's representation for another.
func (p *product) listTypes(_ context.Context, _ struct{}) (typesBody, error) {
	specs := p.taxonomy.Types()
	views := make([]NotificationType, len(specs))
	for i, spec := range specs {
		views[i] = typeViewOf(spec)
	}
	return typesBody{Types: views}, nil
}

// listPreferences answers GET /preferences: the caller's preference matrix, one
// row per type in the taxonomy. Each row's channels are the effective set — the
// stored channels when the recipient has a row for the type, the type's default
// channels otherwise. Like GET /types it refuses nothing of this module's own
// and keys its representation by language.
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

// effectiveChannels computes the effective set of one type for the recipient
// whose stored rows are given: a type that cannot be unsubscribed takes all of
// its default channels and is never explicit, a type that can takes its stored
// row when there is one and its default channels otherwise. It is the same
// computation Effective makes for one type, driven over the whole taxonomy.
func effectiveChannels(spec notification.TypeSpec, stored map[string][]notification.Channel) ([]notification.Channel, bool) {
	if !spec.Unsubscribable {
		return canonicalChannels(spec.DefaultChannels), false
	}
	if row, ok := stored[spec.Key]; ok {
		return canonicalChannels(row), true
	}
	return canonicalChannels(spec.DefaultChannels), false
}

// setPreference answers PUT /preferences/:type_key/:channel: it turns one
// channel on or off for one type and returns that type's effective set.
//
// The decision order is fixed. The entry point binds the body before the
// handler runs, so a body it could not decode — not a JSON object, a
// non-boolean enabled other than null, an unknown member — is refused there
// with the entry point's own problem document. The handler first refuses a body
// naming no enabled — a literal null binds as if absent — then checks the
// channel before any no-op branch, so an unknown
// channel is never taken for a no-op; the type is checked after the channel;
// and the type's unsubscribability is checked after that. Only then is the
// effective set computed and the target set derived from it, and a target that
// equals the current effective set is a 200 that writes nothing. A write that
// does proceed goes through Set.
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

// withVary adds the route's declared Vary header to a failure, so a refusal
// carries the representation key the success answer does. The hand-written
// handler set the header before it read the table; a simple operation writes the
// success headers itself, so the failure carries it as a fault header, which the
// 503 declaration allows.
func withVary(err error) error {
	var fault speedhttp.Fault
	if !errors.As(err, &fault) {
		return err
	}
	fault.Headers = append(fault.Headers, speedhttp.HeaderValue{Name: "Vary", Value: "Accept-Language"})
	return fault
}

// answer maps a failure a seam method returned to the answer it belongs to. The
// mapping is closed and fail-closed: an unknown type or channel is a 400; a
// type that cannot be unsubscribed is a 409; every other failure — a missing
// tenant, a table that cannot be read — is a 503 whose cause stays in the log
// and never reaches the caller.
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

// refusal builds the answer a handler returns for one refusal: its status, its
// type and its rendered title and detail. An empty code writes the 503 — no
// problem type of any kind — whose cause stays in the log and whose body
// carries only a generic title.
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
