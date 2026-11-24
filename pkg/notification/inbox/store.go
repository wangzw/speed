package inbox

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/notification"
	"github.com/vislake/speed/pkg/tenant"
)

// tableName is the in-app message table. It is stated here and once in each
// dialect's migration file, and the writers keep the two in step.
const tableName = "in_app_messages"

type message struct {
	tenant.Model
	ID              string `gorm:"column:id;primaryKey"`
	RecipientUserID string `gorm:"column:recipient_user_id;not null"`
	TypeKey         string `gorm:"column:type_key;not null"`
	Group           string `gorm:"column:group;not null"`
	Title           string `gorm:"column:title;not null"`
	Body            string `gorm:"column:body;not null"`
	Params          string `gorm:"column:params;not null"`
	Link            string `gorm:"column:link;not null"`
	DedupeKey       string `gorm:"column:dedupe_key;not null"`
	ExpiresAt       *int64 `gorm:"column:expires_at"`
	ReadAt          *int64 `gorm:"column:read_at"`
	CreatedAt       int64  `gorm:"column:created_at;not null"`
	UpdatedAt       int64  `gorm:"column:updated_at;not null"`
}

func (message) TableName() string { return tableName }

func (r *message) view() notification.Message {
	return notification.Message{
		ID:        r.ID,
		TypeKey:   r.TypeKey,
		Group:     r.Group,
		Title:     r.Title,
		Body:      r.Body,
		Params:    decodeParams(r.Params),
		Link:      r.Link,
		ReadAt:    msTimePtr(r.ReadAt),
		CreatedAt: msTime(r.CreatedAt),
	}
}

func msTime(ms int64) time.Time { return time.UnixMilli(ms).UTC() }

func msTimePtr(ms *int64) *time.Time {
	if ms == nil {
		return nil
	}
	t := msTime(*ms)
	return &t
}

func encodeParams(params map[string]any) (string, error) {
	encoded, err := json.Marshal(params)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func decodeParams(encoded string) map[string]any {
	if encoded == "" || encoded == "null" {
		return nil
	}
	var params map[string]any
	if err := json.Unmarshal([]byte(encoded), &params); err != nil {
		return nil
	}
	return params
}

func (s *inboxStore) Write(ctx context.Context, recipient string, m notification.Message, dedupeKey string) (notification.Message, error) {
	if strings.TrimSpace(recipient) == "" {
		return notification.Message{}, fmt.Errorf("%w: Write was handed no recipient", notification.ErrInvalidRequest)
	}
	if strings.TrimSpace(dedupeKey) == "" {
		return notification.Message{}, fmt.Errorf("%w: Write was handed no dedupe key", notification.ErrInvalidRequest)
	}
	encodedParams, err := encodeParams(m.Params)
	if err != nil {
		return notification.Message{}, fmt.Errorf("%w: the message parameters cannot be encoded: %w",
			notification.ErrInvalidRequest, err)
	}
	now := time.Now().UnixMilli()
	row := message{
		ID:              uuid.NewString(),
		RecipientUserID: recipient,
		TypeKey:         m.TypeKey,
		Group:           m.Group,
		Title:           m.Title,
		Body:            m.Body,
		Params:          encodedParams,
		Link:            m.Link,
		DedupeKey:       dedupeKey,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	written := s.handle.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if written.Error != nil {
		return notification.Message{}, fmt.Errorf("%s: writing an in-app message: %w", ModuleName, written.Error)
	}
	if written.RowsAffected == 0 {
		// The dedupe key is already held: this write is a repeat, so no event is
		// published.
		held, err := s.byDedupeKey(ctx, dedupeKey)
		if err != nil {
			return notification.Message{}, err
		}
		return held.view(), nil
	}
	s.publishCreated(ctx, &row)
	return row.view(), nil
}

func (s *inboxStore) byDedupeKey(ctx context.Context, dedupeKey string) (*message, error) {
	var row message
	if err := s.handle.WithContext(ctx).Where("dedupe_key = ?", dedupeKey).First(&row).Error; err != nil {
		return nil, fmt.Errorf("%s: reading the message the dedupe key already belongs to: %w", ModuleName, err)
	}
	return &row, nil
}

func (s *inboxStore) publishCreated(ctx context.Context, row *message) {
	if s.bus == nil {
		return
	}
	tenantID, _ := tenant.FromContext(ctx)
	payload := Created{
		MessageID:       row.ID,
		TenantID:        string(tenantID),
		RecipientUserID: row.RecipientUserID,
		TypeKey:         row.TypeKey,
	}
	evt := event.Event{Type: EventInboxCreated, TenantID: string(tenantID), Payload: payload}
	if err := s.bus.Publish(ctx, evt); err != nil {
		s.logger.Warn("notification.inbox: publishing a created message failed",
			"message", row.ID, "error", err.Error())
	}
}

func (s *inboxStore) listBase(ctx context.Context, recipient, group string, now int64) *gorm.DB {
	q := s.handle.WithContext(ctx).Model(&message{}).
		Where("recipient_user_id = ?", recipient).
		Where("expires_at IS NULL OR expires_at > ?", now)
	if group != "" {
		q = q.Where(map[string]any{"group": group})
	}
	return q
}

func (s *inboxStore) List(ctx context.Context, recipient string, q notification.ListQuery) (notification.MessagePage, error) {
	now := time.Now().UnixMilli()
	var total int64
	if err := s.listBase(ctx, recipient, q.Group, now).Count(&total).Error; err != nil {
		return notification.MessagePage{}, fmt.Errorf("%s: counting the recipient's messages: %w", ModuleName, err)
	}
	page := s.listBase(ctx, recipient, q.Group, now).Order("created_at DESC, id DESC")
	if q.Limit > 0 {
		page = page.Limit(q.Limit)
	}
	if q.Offset > 0 {
		page = page.Offset(q.Offset)
	}
	var rows []message
	if err := page.Find(&rows).Error; err != nil {
		return notification.MessagePage{}, fmt.Errorf("%s: listing the recipient's messages: %w", ModuleName, err)
	}
	items := make([]notification.Message, len(rows))
	for i := range rows {
		items[i] = rows[i].view()
	}
	return notification.MessagePage{Items: items, Total: int(total)}, nil
}

func (s *inboxStore) UnreadCount(ctx context.Context, recipient string) (int, error) {
	now := time.Now().UnixMilli()
	var n int64
	err := s.handle.WithContext(ctx).Model(&message{}).
		Where("recipient_user_id = ?", recipient).
		Where("read_at IS NULL").
		Where("expires_at IS NULL OR expires_at > ?", now).
		Count(&n).Error
	if err != nil {
		return 0, fmt.Errorf("%s: counting the recipient's unread messages: %w", ModuleName, err)
	}
	return int(n), nil
}

func (s *inboxStore) MarkRead(ctx context.Context, recipient, messageID string) error {
	now := time.Now().UnixMilli()
	marked := s.handle.WithContext(ctx).Model(&message{}).
		Where("recipient_user_id = ?", recipient).
		Where("id = ?", messageID).
		Where("read_at IS NULL").
		Updates(map[string]any{"read_at": now, "updated_at": now})
	if marked.Error != nil {
		return fmt.Errorf("%s: marking the message %s read: %w", ModuleName, messageID, marked.Error)
	}
	if marked.RowsAffected > 0 {
		return nil
	}
	var held int64
	err := s.handle.WithContext(ctx).Model(&message{}).
		Where("recipient_user_id = ?", recipient).
		Where("id = ?", messageID).
		Count(&held).Error
	if err != nil {
		return fmt.Errorf("%s: reading the message %s back: %w", ModuleName, messageID, err)
	}
	if held == 0 {
		return fmt.Errorf("%w: %s", notification.ErrMessageNotFound, messageID)
	}
	return nil
}

func (s *inboxStore) ReadAll(ctx context.Context, recipient string) (int, error) {
	now := time.Now().UnixMilli()
	marked := s.handle.WithContext(ctx).Model(&message{}).
		Where("recipient_user_id = ?", recipient).
		Where("read_at IS NULL").
		Updates(map[string]any{"read_at": now, "updated_at": now})
	if marked.Error != nil {
		return 0, fmt.Errorf("%s: marking the recipient's messages read: %w", ModuleName, marked.Error)
	}
	return int(marked.RowsAffected), nil
}
