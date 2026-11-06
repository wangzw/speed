package stream

import (
	"net/http"

	speedhttp "github.com/vislake/speed/pkg/http"
)

// The fragment's info section. The title and the version are the ones the
// hand-written fragment published before the declaration generated it.
const (
	fragmentTitle   = "notification stream"
	fragmentVersion = "1.0.0"
)

// pathStreamFragment is the route as the fragment writes it: relative to the
// API root the module's Spec declares. The mounting resolves it to the absolute
// path the route is registered at.
const pathStreamFragment = "/notifications/stream"

// streamInput is the declaration's input type. The route reads no parameter and
// no body: the recipient comes from the request's identity and the tenant comes
// from the resolving layer, both left in the context the handler receives.
type streamInput struct{}

// subscribeOperation declares the stream route and binds its types. The handler
// builds its own answer, so this is a reply operation: the success answer is one
// text/event-stream the handler produces through the entry point's SSE writer.
var subscribeOperation = speedhttp.NewReplyOperation[streamInput](
	"notification.stream.subscribe",
	http.MethodGet,
	pathStreamFragment,
	"Subscribe to the caller's in-app notifications as a server-sent event stream",
	"One long-lived connection, scoped by the request's tenant and the caller's effective account "+
		"(the impersonated account while impersonation is in force). The server writes a frame each "+
		"time the inbox gains a row for that pair, and the connection ends when the client disconnects, "+
		"the entry point drains or the process stops. Every frame is named message and its data is a "+
		"JSON object carrying the published event payload (message_id, recipient_user_id, "+
		"type_key); the tenant the stream is scoped to is not in the frame, and a client reads the inbox by message_id to obtain content. Delivery is best-effort: "+
		"the hub is in memory and per replica, nothing is replayed, arrival order carries no business "+
		"meaning, and a connection with 64 frames already pending drops its own further frames rather "+
		"than delaying the publisher. The route does not decide who may connect; host authorization "+
		"does.",
	nil,
	[]speedhttp.Response{
		{
			Status: http.StatusOK,
			Description: "One text/event-stream for the life of the connection; each event is named " +
				"message and its data is the published event payload as JSON. The raw event-stream body " +
				"is a sequence of SSE frames, each one an event: message line, a data: line holding a " +
				"JSON object, and a blank line, with no terminating frame or sentinel.",
			Body: &speedhttp.Body{MediaType: "text/event-stream"},
		},
		speedhttp.ProblemResponse(http.StatusUnauthorized,
			"The request carries no identity, so there is no recipient to scope the connection to"),
		speedhttp.ProblemResponse(http.StatusInternalServerError,
			"The response writer cannot flush a stream"),
	},
)

// operations is this module's declaration of the API it serves. The fragment is
// generated from it; the route is mounted from the same declaration.
var operations = speedhttp.Operations{subscribeOperation}
