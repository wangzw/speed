package preferences

import "github.com/vislake/speed/pkg/notification"

// The views the routes answer with. Each is built field by field from the
// module's own structures — the taxonomy and the effective-set computation — and
// never hands the stored form through as it stands, so a column added to the
// table later does not reach a client by being there.
//
// The types are named for the components the fragment publishes:
// NotificationType and NotificationPreference.

// Channel is one channel as a response body renders it. It is the closed
// platform vocabulary, and it exists as a type of its own so that the fragment
// publishes the set as a constraint: a generator that cannot read an items tag
// on a field of a nested component still reads the value's Enum method, so the
// published schema keeps the enum the hand-written fragment carried.
type Channel string

// The closed channel set, taken from the notification capability so that the
// vocabulary has one source.
const (
	ChannelInApp Channel = Channel(notification.ChannelInApp)
	ChannelEmail Channel = Channel(notification.ChannelEmail)
	ChannelSMS   Channel = Channel(notification.ChannelSMS)
)

// Enum publishes the closed set the channel vocabulary carries.
func (Channel) Enum() []interface{} {
	return []interface{}{string(ChannelInApp), string(ChannelEmail), string(ChannelSMS)}
}

// Description publishes what the channel vocabulary means, so the fragment
// describes the component rather than listing its values alone.
func (Channel) Description() string {
	return "A delivery channel the capability carries: in_app writes a row the inbox reads, email goes " +
		"through the mail capability's sender, and sms goes through the SMS registry's sender. The set is closed."
}

// NotificationType is one taxonomy entry as GET /types shows it.
type NotificationType struct {
	_ struct{} `description:"One notification type as declared by the business module that owns it; the taxonomy is fixed at startup and read-only for the life of the process."`

	Key                    string    `json:"key" required:"true" description:"The globally unique type key; two modules declaring one key fail the process startup."`
	Group                  string    `json:"group" required:"true" description:"The grouping label the declaring module assigns to the type."`
	DefaultChannels        []Channel `json:"default_channels" required:"true" description:"The channels a recipient gets for this type when no preference row is stored. Non-empty, free of duplicates, and drawn from the channel set."`
	Unsubscribable         bool      `json:"unsubscribable" required:"true" description:"Whether a recipient may change this type's channel set. False means writes answer 409 and the effective set is always default_channels."`
	RecipientVisibleParams []string  `json:"recipient_visible_params" required:"true" description:"The parameter names the declaring module allows to be shown to the recipient. This module stores and returns the list but does not interpret it."`
}

// typeViewOf builds the view of one type spec.
func typeViewOf(spec notification.TypeSpec) NotificationType {
	return NotificationType{
		Key:                    spec.Key,
		Group:                  spec.Group,
		DefaultChannels:        channelNames(spec.DefaultChannels),
		Unsubscribable:         spec.Unsubscribable,
		RecipientVisibleParams: append([]string{}, spec.RecipientVisibleParams...),
	}
}

// NotificationPreference is one recipient's row in the GET /preferences matrix:
// the type, its group, the effective channel set and whether the channels came
// from a stored row (true) or the type's default channels (false).
type NotificationPreference struct {
	_ struct{} `description:"One row of the caller's preference matrix: a notification type and the channels that type's dispatches would go out under for the caller."`

	TypeKey  string    `json:"type_key" required:"true" description:"The notification type key this row describes."`
	Group    string    `json:"group" required:"true" description:"The type's grouping label, copied from its declaration."`
	Channels []Channel `json:"channels" required:"true" description:"The caller's effective channel set for the type: the stored channels when a row exists, otherwise the type's default channels; a type that cannot be unsubscribed always carries its default channels."`
	Explicit bool      `json:"explicit" required:"true" description:"True when the channels came from a stored row, false when they are the type's default channels"`
}

// channelNames renders a channel set as the response's channel type.
func channelNames(channels []notification.Channel) []Channel {
	out := make([]Channel, len(channels))
	for i, channel := range channels {
		out[i] = Channel(channel)
	}
	return out
}

// channelStrings renders a channel set as the strings a response body carries.
func channelStrings(channels []notification.Channel) []string {
	out := make([]string, len(channels))
	for i, channel := range channels {
		out[i] = string(channel)
	}
	return out
}
