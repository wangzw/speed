package inbox

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

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

// The page bounds of GET /messages. A limit left off, or written as zero, is
// the default page; a limit outside the range, or a negative offset, is refused
// whole rather than clamped.
const (
	defaultPageLimit = 50
	maxPageLimit     = 200
)

// caller reads the identity the gateway put on the request and answers with the
// account whose inbox is being read.
//
// A route is reached with no identity when the gateway is absent or the request
// carried no credential; either way there is no recipient to serve, so the
// answer is the status alone — the routes hold no realm challenge of their own.
// The recipient is the identity's effective account, so an impersonation reads
// the impersonated account's inbox.
func (s *inboxStore) caller(c *gin.Context) (string, bool) {
	id, ok := authn.FromContext(c.Request.Context())
	if !ok {
		speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusUnauthorized})
		return "", false
	}
	return string(id.Account()), true
}

// handleList answers GET /api/v1/notifications/messages: one page of the
// caller's messages. The only filter is group; limit defaults to 50 and is
// refused outside 0..200, and a negative offset is refused, both whole.
func (s *inboxStore) handleList(c *gin.Context) {
	recipient, ok := s.caller(c)
	if !ok {
		return
	}
	q, ok := pageQuery(c)
	if !ok {
		s.refuse(c, http.StatusBadRequest, badRequestCode)
		return
	}
	page, err := s.List(c.Request.Context(), recipient, q)
	if err != nil {
		s.answer(c, "listing the inbox", err)
		return
	}
	views := make([]messageView, len(page.Items))
	for i, m := range page.Items {
		views[i] = messageViewOf(m)
	}
	c.JSON(http.StatusOK, gin.H{"messages": views, "total": page.Total})
}

// handleUnreadCount answers GET /api/v1/notifications/messages/unread-count:
// the number of the caller's unread, unexpired messages.
func (s *inboxStore) handleUnreadCount(c *gin.Context) {
	recipient, ok := s.caller(c)
	if !ok {
		return
	}
	n, err := s.UnreadCount(c.Request.Context(), recipient)
	if err != nil {
		s.answer(c, "counting the unread messages", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"unread": n})
}

// handleReadAll answers POST /api/v1/notifications/messages/read-all: it marks
// every unread message of the caller read and answers how many were marked. A
// repeat matches nothing and answers zero.
func (s *inboxStore) handleReadAll(c *gin.Context) {
	recipient, ok := s.caller(c)
	if !ok {
		return
	}
	n, err := s.ReadAll(c.Request.Context(), recipient)
	if err != nil {
		s.answer(c, "marking the messages read", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"read_count": n})
}

// handleMessageRead answers POST /api/v1/notifications/messages/:messageId/read:
// it marks one message read and answers 204, or 404 when the message is not one
// this recipient can see.
func (s *inboxStore) handleMessageRead(c *gin.Context) {
	recipient, ok := s.caller(c)
	if !ok {
		return
	}
	if err := s.MarkRead(c.Request.Context(), recipient, c.Param("messageId")); err != nil {
		s.answer(c, "marking a message read", err)
		return
	}
	c.Status(http.StatusNoContent)
	c.Writer.WriteHeaderNow()
}

// pageQuery reads the group, limit and offset of a listing. A limit left off or
// written as zero becomes the default; a limit below zero or above the cap, a
// negative offset, or a limit or offset that is not a number is refused whole
// with a 400, which is what the caller is told rather than being handed a page
// bound it did not ask for.
func pageQuery(c *gin.Context) (notification.ListQuery, bool) {
	q := notification.ListQuery{Group: c.Query("group")}
	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return q, false
		}
		q.Limit = n
	}
	if q.Limit == 0 {
		q.Limit = defaultPageLimit
	}
	if q.Limit < 0 || q.Limit > maxPageLimit {
		return q, false
	}
	if raw := c.Query("offset"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return q, false
		}
		q.Offset = n
	}
	if q.Offset < 0 {
		return q, false
	}
	return q, true
}

// answer maps a failure a method returned to the response it belongs to. The
// mapping is closed and fail-closed: a request whose shape cannot be read is a
// 400, a message this recipient cannot see is a 404 of the message-not-found
// code, and every other failure — a database that could not answer, anything
// unnamed — is a 503 whose cause stays in the log and never reaches the caller.
func (s *inboxStore) answer(c *gin.Context, op string, err error) {
	switch {
	case errors.Is(err, notification.ErrInvalidRequest):
		s.refuse(c, http.StatusBadRequest, badRequestCode)
	case errors.Is(err, notification.ErrMessageNotFound):
		s.refuse(c, http.StatusNotFound, messageNotFoundCode)
	default:
		s.logger.Error(ModuleName+": "+op+" failed", "cause", err.Error())
		s.refuse(c, http.StatusServiceUnavailable, "")
	}
}
