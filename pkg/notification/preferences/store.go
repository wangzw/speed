package preferences

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/vislake/speed/pkg/notification"
	"github.com/vislake/speed/pkg/tenant"
)

type preferenceRow struct {
	tenant.Model
	ID              string    `gorm:"column:id;primaryKey"`
	RecipientUserID string    `gorm:"column:recipient_user_id;not null"`
	TypeKey         string    `gorm:"column:type_key;not null"`
	Channels        string    `gorm:"column:channels;not null"`
	CreatedAt       time.Time `gorm:"column:created_at;not null"`
	UpdatedAt       time.Time `gorm:"column:updated_at;not null"`
}

func (preferenceRow) TableName() string { return tableName }

func (p *product) Effective(ctx context.Context, recipient, typeKey string) ([]notification.Channel, error) {
	if _, ok := tenant.FromContext(ctx); !ok {
		return nil, fmt.Errorf("%w: the request carries no tenant", tenant.ErrNoTenantInContext)
	}
	spec, ok := p.taxonomy.Type(typeKey)
	if !ok {
		return nil, fmt.Errorf("%w: the type %q is not in the taxonomy", notification.ErrUnknownType, typeKey)
	}
	if !spec.Unsubscribable {
		return canonicalChannels(spec.DefaultChannels), nil
	}
	var row preferenceRow
	err := p.handle.WithContext(ctx).
		Where("recipient_user_id = ? AND type_key = ?", recipient, typeKey).
		First(&row).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return canonicalChannels(spec.DefaultChannels), nil
	case err != nil:
		return nil, fmt.Errorf("%s: reading the preference of %q for the type %q: %w", ModuleName, recipient, typeKey, err)
	}
	stored, err := decodeChannels(row.Channels)
	if err != nil {
		return nil, fmt.Errorf("%s: decoding the preference of %q for the type %q: %w", ModuleName, recipient, typeKey, err)
	}
	return canonicalChannels(stored), nil
}

func (p *product) Set(ctx context.Context, recipient, typeKey string, channels []notification.Channel) ([]notification.Channel, error) {
	if _, ok := tenant.FromContext(ctx); !ok {
		return nil, fmt.Errorf("%w: the request carries no tenant", tenant.ErrNoTenantInContext)
	}
	spec, ok := p.taxonomy.Type(typeKey)
	if !ok {
		return nil, fmt.Errorf("%w: the type %q is not in the taxonomy", notification.ErrUnknownType, typeKey)
	}
	if !spec.Unsubscribable {
		return nil, fmt.Errorf("%w: the type %q cannot be unsubscribed", notification.ErrNotUnsubscribable, typeKey)
	}
	for _, channel := range channels {
		if !notification.KnownChannel(channel) {
			return nil, fmt.Errorf("%w: the channel %q is not in the closed set", notification.ErrUnknownChannel, channel)
		}
	}
	canonical := canonicalChannels(channels)
	encoded, err := encodeChannels(canonical)
	if err != nil {
		return nil, fmt.Errorf("%s: encoding the channels of %q for the type %q: %w", ModuleName, recipient, typeKey, err)
	}
	now := time.Now().UTC()
	row := preferenceRow{
		ID:              uuid.NewString(),
		RecipientUserID: recipient,
		TypeKey:         typeKey,
		Channels:        encoded,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	err = p.handle.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "tenant_id"},
			{Name: "recipient_user_id"},
			{Name: "type_key"},
		},
		DoUpdates: clause.Assignments(map[string]any{
			"channels":   encoded,
			"updated_at": now,
		}),
	}).Create(&row).Error
	if err != nil {
		return nil, fmt.Errorf("%s: writing the preference of %q for the type %q: %w", ModuleName, recipient, typeKey, err)
	}
	return canonical, nil
}

func (p *product) loadStored(ctx context.Context, recipient string) (map[string][]notification.Channel, error) {
	var rows []preferenceRow
	err := p.handle.WithContext(ctx).
		Where("recipient_user_id = ?", recipient).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("%s: reading the preferences of %q: %w", ModuleName, recipient, err)
	}
	out := make(map[string][]notification.Channel, len(rows))
	for _, row := range rows {
		stored, err := decodeChannels(row.Channels)
		if err != nil {
			return nil, fmt.Errorf("%s: decoding the preference of %q for the type %q: %w",
				ModuleName, recipient, row.TypeKey, err)
		}
		out[row.TypeKey] = stored
	}
	return out, nil
}

func encodeChannels(channels []notification.Channel) (string, error) {
	encoded, err := json.Marshal(channels)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func decodeChannels(encoded string) ([]notification.Channel, error) {
	var channels []notification.Channel
	if err := json.Unmarshal([]byte(encoded), &channels); err != nil {
		return nil, err
	}
	return channels, nil
}

// channelOrder is the position of a channel in the closed set, the order an
// effective set is written in. A value outside the closed set sorts last, so an
// ordering is always defined.
func channelOrder(channel notification.Channel) int {
	switch channel {
	case notification.ChannelInApp:
		return 0
	case notification.ChannelEmail:
		return 1
	case notification.ChannelSMS:
		return 2
	default:
		return 3
	}
}

// canonicalChannels deduplicates a channel set and orders it by the closed set,
// which is the one form an effective set takes wherever it is computed.
func canonicalChannels(channels []notification.Channel) []notification.Channel {
	seen := make(map[notification.Channel]struct{}, len(channels))
	out := make([]notification.Channel, 0, len(channels))
	for _, channel := range channels {
		if _, twice := seen[channel]; twice {
			continue
		}
		seen[channel] = struct{}{}
		out = append(out, channel)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := channelOrder(out[i]), channelOrder(out[j])
		if ri != rj {
			return ri < rj
		}
		return out[i] < out[j]
	})
	return out
}

func sameSet(a, b []notification.Channel) bool {
	ca, cb := canonicalChannels(a), canonicalChannels(b)
	if len(ca) != len(cb) {
		return false
	}
	for i := range ca {
		if ca[i] != cb[i] {
			return false
		}
	}
	return true
}

func applyToggle(current []notification.Channel, channel notification.Channel, enabled bool) []notification.Channel {
	if enabled {
		return canonicalChannels(append(append([]notification.Channel(nil), current...), channel))
	}
	out := make([]notification.Channel, 0, len(current))
	for _, present := range current {
		if present != channel {
			out = append(out, present)
		}
	}
	return canonicalChannels(out)
}
