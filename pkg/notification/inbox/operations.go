package inbox

import (
	"net/http"

	speedhttp "github.com/vislake/speed/pkg/http"
)

// The fragment's info section. The title and the version are the ones the
// hand-written fragment published before the declaration generated it.
const (
	fragmentTitle   = "notification inbox"
	fragmentVersion = "1.0.0"
)

// The paths the operations are declared at, relative to the API root the
// module's Spec carries. The mounting resolves them against that root, so the
// path is written once and the fragment keeps the relative form the platform
// publishes.
const (
	pathMessagesFragment    = "/notifications/messages"
	pathUnreadCountFragment = "/notifications/messages/unread-count"
	pathReadAllFragment     = "/notifications/messages/read-all"
	pathMessageReadFragment = "/notifications/messages/:messageId/read"
)

// ListMessagesInput is the request of the listing: the group filter and the
// page bounds. A limit left off or written as zero is the default page; a limit
// outside the accepted range, a negative offset, or a non-numeric limit or
// offset is refused whole with the module's bad-request problem.
type ListMessagesInput struct {
	Group  string `query:"group" description:"Filter by group; absent or empty means every group"`
	Limit  int    `query:"limit" description:"Page size; absent or zero is the default page, and a value outside the accepted range is refused whole rather than clamped"`
	Offset int    `query:"offset" description:"Number of items to skip; negative is refused"`
}

// MarkMessageReadInput is the request of the per-message marking: the
// identifier the listing returned.
type MarkMessageReadInput struct {
	MessageID string `path:"messageId" description:"The message identifier, as returned in the listing. An identifier this recipient cannot see is answered as not found."`
}

// The success bodies. Each is an alias of an anonymous struct, so the fragment
// publishes the wrapper inline, as the hand-written fragment did, rather than as
// a component of its own.
type (
	// messagePageBody is the 200 answer of a listing: one page and the total the
	// filter matches.
	messagePageBody = struct {
		_        struct{}              `description:"One page of messages and the number of rows the filter matches."`
		Messages []NotificationMessage `json:"messages" required:"true" description:"The page's messages, newest first; the empty array when the page has none."`
		Total    int                   `json:"total" required:"true" description:"The number of the caller's unexpired messages the filter matches, without the page bound."`
	}

	// unreadCountBody is the 200 answer of the unread count.
	unreadCountBody = struct {
		_      struct{} `description:"The caller's unread count."`
		Unread int      `json:"unread" required:"true" description:"The number of unread, unexpired messages in the caller's inbox."`
	}

	// readAllBody is the 200 answer of marking every unread message read.
	readAllBody = struct {
		_         struct{} `description:"How many messages this call marked read."`
		ReadCount int      `json:"read_count" required:"true" description:"The number of rows whose read_at changed; zero when nothing was unread."`
	}
)

// listMessagesOperation declares GET /notifications/messages: one page of the
// caller's messages.
var listMessagesOperation = speedhttp.NewOperation[ListMessagesInput, messagePageBody](
	"notification.inbox.listMessages",
	http.MethodGet,
	pathMessagesFragment,
	"One page of the caller's messages",
	"Lists the messages of the inbox the request's identity resolves to — the impersonated account's inbox while impersonating. Messages are ordered by creation instant descending, then by identifier descending, so pages stay stable when rows share an instant. Expired rows are not listed, and total counts every row the filter matches without the page bound. A limit outside 0..200, a negative offset, or a non-numeric limit or offset is refused whole with a 400 rather than clamped. A request without an identity is answered 401, and a store failure is answered 503.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "A page of messages and the total the query matches",
			Body:        &speedhttp.Body{MediaType: "application/json"},
		},
		speedhttp.ProblemResponse(http.StatusBadRequest,
			"The limit is not a number or is negative or above 200, or the offset is not a number or is negative"),
		speedhttp.ProblemResponse(http.StatusUnauthorized,
			"The request carries no identity, so there is no inbox to read"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable,
			"The inbox could not answer"),
	},
)

// unreadCountOperation declares GET /notifications/messages/unread-count: the
// number of the caller's unread, unexpired messages.
var unreadCountOperation = speedhttp.NewOperation[struct{}, unreadCountBody](
	"notification.inbox.getUnreadCount",
	http.MethodGet,
	pathUnreadCountFragment,
	"The number of the caller's unread, unexpired messages",
	"Counts the messages of the request's identity's inbox that are neither read nor expired; an expired row is excluded even when it was never read. The count covers the whole inbox rather than a page. A request without an identity is answered 401, and a store failure is answered 503.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "The unread count",
			Body:        &speedhttp.Body{MediaType: "application/json"},
		},
		speedhttp.ProblemResponse(http.StatusUnauthorized,
			"The request carries no identity, so there is no inbox to read"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable,
			"The inbox could not answer"),
	},
)

// markAllMessagesReadOperation declares POST /notifications/messages/read-all:
// every unread message of the caller is marked read.
var markAllMessagesReadOperation = speedhttp.NewOperation[struct{}, readAllBody](
	"notification.inbox.markAllMessagesRead",
	http.MethodPost,
	pathReadAllFragment,
	"Mark every unread message of the caller read",
	"Marks every not-yet-read message of the request's identity's inbox read in one conditional statement and answers how many rows it changed. Expiry is not part of the predicate, so an expired unread row is still marked. A repeated call matches nothing and answers zero, and the first-read timestamp of a message already read is never rewritten. A request without an identity is answered 401, and a store failure is answered 503.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "How many messages were marked read; a repeat matches nothing and answers zero",
			Body:        &speedhttp.Body{MediaType: "application/json"},
		},
		speedhttp.ProblemResponse(http.StatusUnauthorized,
			"The request carries no identity, so there is no inbox to act on"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable,
			"The inbox could not answer"),
	},
)

// markMessageReadOperation declares POST
// /notifications/messages/:messageId/read: one message is marked read.
var markMessageReadOperation = speedhttp.NewOperation[MarkMessageReadInput, struct{}](
	"notification.inbox.markMessageRead",
	http.MethodPost,
	pathMessageReadFragment,
	"Mark one message read",
	"Marks one message read and answers 204 with no body. Marking a message that is already read is a successful no-op that leaves its first-read timestamp alone. An identifier that is unknown, belongs to another account, or belongs to another tenant is answered with the same 404, so the route does not reveal which messages exist. A request without an identity is answered 401, and a store failure is answered 503.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusNoContent,
			Description: "The message was marked read",
		},
		speedhttp.ProblemResponse(http.StatusUnauthorized,
			"The request carries no identity, so there is no inbox to act on"),
		speedhttp.ProblemResponse(http.StatusNotFound,
			"The message is not one this recipient can see"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable,
			"The inbox could not answer"),
	},
)

// operations is this module's declaration of the API it serves. The fragment is
// generated from it; the routes are mounted from the same declarations.
var operations = speedhttp.Operations{
	listMessagesOperation,
	unreadCountOperation,
	markAllMessagesReadOperation,
	markMessageReadOperation,
}
