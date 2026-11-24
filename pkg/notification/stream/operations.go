package stream

import (
	"net/http"

	speedhttp "github.com/vislake/speed/pkg/http"
)

const (
	fragmentTitle   = "notification stream"
	fragmentVersion = "1.0.0"
)

const pathStreamFragment = "/notifications/stream"

type streamInput struct{}

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

var operations = speedhttp.Operations{subscribeOperation}
