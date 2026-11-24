package inbox

import (
	"context"
	"errors"
	"net/http"

	"github.com/vislake/speed/pkg/authn"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/notification"
)

// The problem types this module writes. typePrefix is the capability's
// namespace; the two codes are this surface's kinds within it. A 503 carries no
// type of its own, only the generic title its catalog holds.
const (
	typePrefix          = "urn:speed:notification:"
	badRequestCode      = "bad-request"
	messageNotFoundCode = "message-not-found"
)

const (
	defaultPageLimit = 50
	maxPageLimit     = 200
)

func (s *inboxStore) caller(ctx context.Context) (string, error) {
	id, ok := authn.FromContext(ctx)
	if !ok {
		return "", speedhttp.Fault{Status: http.StatusUnauthorized}
	}
	return string(id.Account()), nil
}

// listMessages answers GET /api/v1/notifications/messages: one page of the
// caller's messages. The only filter is group; limit defaults to 50 and is
// refused outside 0..200, and a negative offset is refused, both whole.
func (s *inboxStore) listMessages(ctx context.Context, in ListMessagesInput) (messagePageBody, error) {
	recipient, err := s.caller(ctx)
	if err != nil {
		return messagePageBody{}, err
	}
	q := notification.ListQuery{Group: in.Group, Limit: in.Limit, Offset: in.Offset}
	if q.Limit == 0 {
		q.Limit = defaultPageLimit
	}
	if q.Limit < 0 || q.Limit > maxPageLimit || q.Offset < 0 {
		return messagePageBody{}, s.refusal(ctx, http.StatusBadRequest, badRequestCode)
	}
	page, err := s.List(ctx, recipient, q)
	if err != nil {
		return messagePageBody{}, s.answer(ctx, "listing the inbox", err)
	}
	views := make([]NotificationMessage, len(page.Items))
	for i, m := range page.Items {
		views[i] = messageViewOf(m)
	}
	return messagePageBody{Messages: views, Total: page.Total}, nil
}

func (s *inboxStore) unreadCount(ctx context.Context, _ struct{}) (unreadCountBody, error) {
	recipient, err := s.caller(ctx)
	if err != nil {
		return unreadCountBody{}, err
	}
	n, err := s.UnreadCount(ctx, recipient)
	if err != nil {
		return unreadCountBody{}, s.answer(ctx, "counting the unread messages", err)
	}
	return unreadCountBody{Unread: n}, nil
}

func (s *inboxStore) markAllRead(ctx context.Context, _ struct{}) (readAllBody, error) {
	recipient, err := s.caller(ctx)
	if err != nil {
		return readAllBody{}, err
	}
	n, err := s.ReadAll(ctx, recipient)
	if err != nil {
		return readAllBody{}, s.answer(ctx, "marking the messages read", err)
	}
	return readAllBody{ReadCount: n}, nil
}

func (s *inboxStore) markMessageRead(ctx context.Context, in MarkMessageReadInput) (struct{}, error) {
	recipient, err := s.caller(ctx)
	if err != nil {
		return struct{}{}, err
	}
	if err := s.MarkRead(ctx, recipient, in.MessageID); err != nil {
		return struct{}{}, s.answer(ctx, "marking a message read", err)
	}
	return struct{}{}, nil
}

func (s *inboxStore) answer(ctx context.Context, op string, err error) error {
	switch {
	case errors.Is(err, notification.ErrInvalidRequest):
		return s.refusal(ctx, http.StatusBadRequest, badRequestCode)
	case errors.Is(err, notification.ErrMessageNotFound):
		return s.refusal(ctx, http.StatusNotFound, messageNotFoundCode)
	default:
		s.logger.Error(ModuleName+": "+op+" failed", "cause", err.Error())
		return s.refusal(ctx, http.StatusServiceUnavailable, "")
	}
}
