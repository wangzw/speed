package admin

import (
	"net/http"
	"reflect"

	speedhttp "github.com/vislake/speed/pkg/http"
)

// The fragment's info section. Every module's fragment carries its own module
// name and the version of the fragment's shape.
const (
	fragmentTitle   = "audit.admin"
	fragmentVersion = "1.0.0"
)

// The routes as the fragment writes them: relative to the API root the module's
// Spec declares. The mounting resolves them to the absolute paths the routes are
// registered at.
const (
	pathRecords = "/audit/admin/records"
	pathRecord  = "/audit/admin/records/:id"
)

// RecordListInput is the request of the list route: the closed set of query
// parameters the route accepts. A time bound is read as text and parsed by the
// handler, because a bound that is not RFC 3339 is the capability's own
// bad-request problem rather than the entry point's generic shape refusal.
//
// A parameter the route does not accept never reaches this type: the typed
// binder ignores a query key the declaration does not carry, so the module keeps
// the closed-set check in front of the handler (routes.go).
type RecordListInput struct {
	Since   string   `query:"since" format:"date-time" description:"Read rows at or after this instant, RFC 3339"`
	Until   string   `query:"until" format:"date-time" description:"Read rows before this instant, RFC 3339"`
	Action  []string `query:"action" description:"Narrow to rows carrying one of these actions"`
	Outcome []string `query:"outcome" description:"Narrow to rows whose outcome is one of these"`
	Actor   []string `query:"actor" description:"Narrow to rows whose actor is one of these"`
	Cursor  string   `query:"cursor" description:"Continue after the row this cursor names"`
	Limit   int      `query:"limit" minimum:"0" description:"The page's maximum size; the read surface caps it"`
}

// RecordGetInput is the request of the get route: the row identifier as a path
// parameter. The route takes no query parameter.
type RecordGetInput struct {
	ID string `path:"id" description:"The row identifier, a non-negative decimal integer"`
}

// unauthorizedChallenge declares the header a 401 on this module's routes
// carries. The handler that answers 401 itself writes the realm of the endpoint
// the route was mounted on, so the header is declared and the value is left to
// the handler.
var unauthorizedChallenge = speedhttp.Header{
	Name:        "WWW-Authenticate",
	Description: "The identity challenge, carrying the realm of the endpoint the route was mounted on.",
}

// unauthorized is the declaration of the 401 every route shares: the request
// carried no identity. It is the same answer the admission and authorization
// layers write for the same condition, so the two agree on the shape.
func unauthorized(description string) speedhttp.Response {
	response := speedhttp.ProblemResponse(http.StatusUnauthorized, description)
	response.Headers = []speedhttp.Header{unauthorizedChallenge}
	return response
}

// listOperation declares GET /audit/admin/records and binds its types: the
// handler receives the declared query parameters and returns the page, so the
// entry point binds the request and writes the declared 200.
var listOperation = speedhttp.NewOperation[RecordListInput, AuditAdminRecordPage](
	"audit.admin.records.list",
	http.MethodGet,
	pathRecords,
	"Page through the audit records, newest first",
	"Reads one page of the audit table across every tenant and the rows that belong to none, "+
		"newest first. All supplied conditions apply together: the time window plus each non-empty "+
		"set of actions, outcomes and actors. Paging is cut by row identifier rather than by offset, "+
		"so rows appended while paging do not shift the page that follows. The page size defaults "+
		"to 50 and is capped at 200; the answer's next is empty on the last page.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "One page of records and the cursor of the next, empty on the last page",
			Body:        &speedhttp.Body{MediaType: "application/json"},
		},
		speedhttp.ProblemResponse(http.StatusBadRequest,
			"A query parameter the route does not accept, a time bound that is not RFC 3339, an "+
				"empty action, outcome or actor, a limit that is not a non-negative integer, or a "+
				"cursor the read surface did not hand out"),
		unauthorized("The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no audit.read in the platform domain"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// getOperation declares GET /audit/admin/records/:id and binds its types. The
// handler builds the answer because a row the table does not hold is a bare 404
// with no problem document, which the simple form cannot write.
var getOperation = speedhttp.NewReplyOperation[RecordGetInput](
	"audit.admin.records.get",
	http.MethodGet,
	pathRecord,
	"Read one audit record by its row identifier",
	"Reads one audit row by its identifier. The identifier is unique over the whole table, so the "+
		"row is found whatever tenant it belongs to, or when it belongs to none. The identifier must "+
		"be a non-negative decimal integer and the request must carry no query parameter; anything "+
		"else is a 400. An identifier of zero, or one that no row holds, is a 404.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "The record",
			Body: &speedhttp.Body{
				MediaType: "application/json",
				Structure: reflect.TypeOf(AuditAdminRecord{}),
			},
		},
		speedhttp.ProblemResponse(http.StatusBadRequest,
			"The path parameter is not a row identifier, or a query parameter is present that the "+
				"route does not accept"),
		unauthorized("The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no audit.read in the platform domain"),
		{
			Status:      http.StatusNotFound,
			Description: "The table holds no record with the identifier",
		},
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// operations is this module's declaration of the API it serves. The fragment is
// generated from it; the routes are mounted from the same declarations.
var operations = speedhttp.Operations{listOperation, getOperation}
