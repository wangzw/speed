package admin

import (
	"net/http"
	"reflect"

	speedhttp "github.com/vislake/speed/pkg/http"
)

const (
	fragmentTitle   = "audit.admin"
	fragmentVersion = "1.0.0"
)

const (
	pathRecords = "/audit/records"
	pathRecord  = "/audit/records/:id"
	pathExport  = "/audit/export"
)

// RecordListInput is the request of the list route: the closed set of
// query parameters the route accepts.
type RecordListInput struct {
	// Since reads rows at or after this instant, RFC 3339.
	Since string `query:"since" format:"date-time" description:"Read rows at or after this instant, RFC 3339"`
	// Until reads rows before this instant, RFC 3339.
	Until string `query:"until" format:"date-time" description:"Read rows before this instant, RFC 3339"`
	// Action narrows to rows carrying one of these actions.
	Action []string `query:"action" description:"Narrow to rows carrying one of these actions"`
	// Outcome narrows to rows whose outcome is one of these.
	Outcome []string `query:"outcome" description:"Narrow to rows whose outcome is one of these"`
	// Actor narrows to rows whose actor is one of these.
	Actor []string `query:"actor" description:"Narrow to rows whose actor is one of these"`
	// Cursor continues after the row it names.
	Cursor string `query:"cursor" description:"Continue after the row this cursor names"`
	// Limit is the page's maximum size; the read surface caps it.
	Limit int `query:"limit" minimum:"0" description:"The page's maximum size; the read surface caps it"`
}

// RecordExportInput is the request of the export route: the same closed
// set of filters as the list route, with no cursor and no page size.
type RecordExportInput struct {
	// Since exports rows at or after this instant, RFC 3339.
	Since string `query:"since" format:"date-time" description:"Export rows at or after this instant, RFC 3339"`
	// Until exports rows before this instant, RFC 3339.
	Until string `query:"until" format:"date-time" description:"Export rows before this instant, RFC 3339"`
	// Action narrows to rows carrying one of these actions.
	Action []string `query:"action" description:"Narrow to rows carrying one of these actions"`
	// Outcome narrows to rows whose outcome is one of these.
	Outcome []string `query:"outcome" description:"Narrow to rows whose outcome is one of these"`
	// Actor narrows to rows whose actor is one of these.
	Actor []string `query:"actor" description:"Narrow to rows whose actor is one of these"`
}

// RecordGetInput is the request of the get route: the row identifier as a path
// parameter. The route takes no query parameter.
type RecordGetInput struct {
	// ID is the row identifier, a non-negative decimal integer.
	ID string `path:"id" description:"The row identifier, a non-negative decimal integer"`
}

var unauthorizedChallenge = speedhttp.Header{
	Name:        "WWW-Authenticate",
	Description: "The identity challenge, carrying the realm of the endpoint the route was mounted on.",
}

func unauthorized(description string) speedhttp.Response {
	response := speedhttp.ProblemResponse(http.StatusUnauthorized, description)
	response.Headers = []speedhttp.Header{unauthorizedChallenge}
	return response
}

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

// getOperation declares GET /audit/records/:id and binds its types. The
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

var exportOperation = speedhttp.NewReplyOperation[RecordExportInput](
	"audit.admin.records.export",
	http.MethodGet,
	pathExport,
	"Export the audit records matching the filters as NDJSON",
	"Reads every audit row the supplied conditions match and writes it as one JSON object per "+
		"line, the same record view the list route answers, with no envelope and no header line. "+
		"The body is streamed page by page as the read surface returns it; the export is not a "+
		"snapshot of one instant, so a row appended while it runs is not in the file and a row "+
		"deleted while it runs is missing from it. The page size is the read surface's maximum and "+
		"is not the caller's to choose; cursor and limit describe a page and are refused. A filter "+
		"that matches nothing answers an empty body.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "Every matching record, one JSON object per line, or an empty body when nothing matches",
			Headers: []speedhttp.Header{
				{
					Name: "Content-Disposition",
					Description: "The download name: audit-records-<the export's UTC instant>.ndjson, " +
						"carried as an attachment.",
				},
				{
					Name:        "Cache-Control",
					Description: "Always no-store: exported audit data is not kept by a cache.",
					Value:       "no-store",
				},
			},
			Body: &speedhttp.Body{MediaType: exportMediaType},
		},
		speedhttp.ProblemResponse(http.StatusBadRequest,
			"A query parameter the route does not accept, a time bound that is not RFC 3339, an "+
				"empty action, outcome or actor, or a cursor or limit, which an export does not take"),
		unauthorized("The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no audit.read in the platform domain"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

var operations = speedhttp.Operations{listOperation, getOperation, exportOperation}
