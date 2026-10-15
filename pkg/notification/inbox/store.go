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

// message is the row shape of the in_app_messages table.
//
// It embeds the tenant convention, which is both the declaration that a row
// belongs to a tenant and the column that carries it: a statement issued
// through this model is scoped to the tenant of its context, filled with it on
// insert and judged against it on write. The key leads with that column, as
// every scoped table's does.
//
// Every instant is an epoch count in milliseconds, matching the columns the
// migration declares. The group, link and params columns hold the values the
// delivery pipeline rendered; params is the JSON the caller passed, which this
// module stores and reads back without interpreting.
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

// TableName is the table the messages live in.
func (message) TableName() string { return tableName }

// view builds the message a caller sees out of a row.
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

// msTime reads an epoch count in milliseconds as an instant.
func msTime(ms int64) time.Time { return time.UnixMilli(ms).UTC() }

// msTimePtr reads an optional epoch count as an optional instant.
func msTimePtr(ms *int64) *time.Time {
	if ms == nil {
		return nil
	}
	t := msTime(*ms)
	return &t
}

// encodeParams writes a message's parameters as the JSON the column holds. A
// nil map is written as the JSON null, which reads back as a nil map.
func encodeParams(params map[string]any) (string, error) {
	encoded, err := json.Marshal(params)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// decodeParams reads the column back as the parameter map. A value that does
// not decode is a row this build did not write, and the parameters are then
// dropped rather than failing a read: the message is still worth showing.
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

// Write writes one message for the recipient; see notification.Inbox.
//
// The row is inserted under the unique dedupe key: a key already held writes
// nothing and the row already there is returned instead, so a repeat of one
// write lands one row and publishes no second event. An empty recipient or an
// empty dedupe key is a caller defect refused with ErrInvalidRequest, with
// nothing written. The instant and the identifier are the inbox's own — the
// caller renders the title and body, and this module mints the row.
//
// The row commits as the statement returns, and the created event is published
// after it, outside any transaction. A publish that fails does not roll the row
// back and does not reach the caller: the row is in the table, and a consumer
// reads it back.
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
		// The dedupe key is already held: this write is a repeat. The row it
		// belongs to is returned, and no event is published.
		held, err := s.byDedupeKey(ctx, dedupeKey)
		if err != nil {
			return notification.Message{}, err
		}
		return held.view(), nil
	}
	s.publishCreated(ctx, &row)
	return row.view(), nil
}

// byDedupeKey reads the row a dedupe key belongs to, within the tenant of the
// context.
func (s *inboxStore) byDedupeKey(ctx context.Context, dedupeKey string) (*message, error) {
	var row message
	if err := s.handle.WithContext(ctx).Where("dedupe_key = ?", dedupeKey).First(&row).Error; err != nil {
		return nil, fmt.Errorf("%s: reading the message the dedupe key already belongs to: %w", ModuleName, err)
	}
	return &row, nil
}

// publishCreated announces a row that just landed. A bus that is absent means
// no event is published and the row stands; a publish that fails is recorded
// and goes no further, because the row is already in the table and reporting
// the write as failed would tell the caller the opposite of what happened.
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

// listBase is the predicate a listing and the total share: the recipient's rows
// that have not expired, optionally narrowed to one group.
func (s *inboxStore) listBase(ctx context.Context, recipient, group string, now int64) *gorm.DB {
	q := s.handle.WithContext(ctx).Model(&message{}).
		Where("recipient_user_id = ?", recipient).
		Where("expires_at IS NULL OR expires_at > ?", now)
	if group != "" {
		q = q.Where(map[string]any{"group": group})
	}
	return q
}

// List reads one page of the recipient's messages; see notification.Inbox.
//
// Expired rows are not returned, the same judgement the unread count makes.
// The order is fixed as created_at DESC, id DESC, so two rows written in the
// same instant keep a stable order across pages. Total is the number of rows
// the filter matches, without the page bound.
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

// UnreadCount counts the recipient's unread, unexpired messages; see
// notification.Inbox.
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

// MarkRead marks one message read; see notification.Inbox.
//
// The statement is conditional on read_at IS NULL, so it writes only a row that
// is not read yet and never rewrites a first-read timestamp. When it matches no
// row the recipient's own range is read for the identifier: a row that is there
// is one already read — a successful no-op that writes nothing — and one that is
// not there is an unknown identifier, another recipient's or another tenant's,
// all answered with the same ErrMessageNotFound so no existence leaks.
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

// ReadAll marks every unread message of the recipient read and returns the
// number marked; see notification.Inbox.
//
// The predicate is read_at IS NULL, which is what makes a repeat match nothing
// and return zero, and an already-read row is left with its first-read
// timestamp. Whether a row is expired is not part of the predicate: an expired
// row is still marked read.
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
