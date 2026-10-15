package preferences

import "github.com/vislake/speed/pkg/notification"

// The views the routes answer with. Each is built field by field from the
// module's own structures — the taxonomy and the effective-set computation — and
// never hands the stored form through as it stands, so a column added to the
// table later does not reach a client by being there.

// typeView is one taxonomy entry as GET /types shows it.
type typeView struct {
	Key                    string   `json:"key"`
	Group                  string   `json:"group"`
	DefaultChannels        []string `json:"default_channels"`
	Unsubscribable         bool     `json:"unsubscribable"`
	RecipientVisibleParams []string `json:"recipient_visible_params"`
}

// typeViewOf builds the view of one type spec.
func typeViewOf(spec notification.TypeSpec) typeView {
	return typeView{
		Key:                    spec.Key,
		Group:                  spec.Group,
		DefaultChannels:        channelStrings(spec.DefaultChannels),
		Unsubscribable:         spec.Unsubscribable,
		RecipientVisibleParams: append([]string{}, spec.RecipientVisibleParams...),
	}
}

// preferenceView is one recipient's row in the GET /preferences matrix: the type,
// its group, the effective channel set and whether the channels came from a
// stored row (true) or the type's default channels (false).
type preferenceView struct {
	TypeKey  string   `json:"type_key"`
	Group    string   `json:"group"`
	Channels []string `json:"channels"`
	Explicit bool     `json:"explicit"`
}

// channelStrings renders a channel set as the strings a response body carries.
func channelStrings(channels []notification.Channel) []string {
	out := make([]string, len(channels))
	for i, channel := range channels {
		out[i] = string(channel)
	}
	return out
}
