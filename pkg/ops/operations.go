package ops

import (
	"net/http"
	"reflect"

	speedhttp "github.com/vislake/speed/pkg/http"
)

// The fragment's info section: the title this module's contract is published
// under and the version of the fragment's shape.
const (
	fragmentTitle   = "operations"
	fragmentVersion = "1.0.0"
)

// OpsHealth is the liveness response body: a single status field, always ok on
// a process that can answer.
type OpsHealth struct {
	_ struct{} `description:"The liveness response body: a single status field, always ok on a process that can answer."`

	Status string `json:"status" required:"true" enum:"ok" description:"The literal the liveness route writes; a live process always answers ok"`
}

// OpsReadiness is the readiness response body: the verdict and, when it is
// unavailable, one line per part that failed.
type OpsReadiness struct {
	_ struct{} `description:"The readiness response body, carrying the verdict and, when it is unavailable, one line per part that failed."`

	Status   string   `json:"status" required:"true" enum:"ready,unavailable" description:"ready when every part answered, unavailable otherwise"`
	Failures []string `json:"failures,omitempty" description:"One line per part that failed; absent when the verdict is ready"`
}

// OpsDocument is the merged OpenAPI document. The declaration publishes it as a
// free-form object: the document's members are assembled at startup from every
// module's fragment, so no fixed set of properties describes it.
type OpsDocument struct {
	_ struct{} `additionalProperties:"true" description:"The merged OpenAPI document: the version it declares, its info section, every merged path, the components its fragments declared, and x-fragments naming each source"`
}

// healthOperation declares GET /health and binds its types: the handler reads
// nothing and returns the liveness body, so the entry point writes the answer.
var healthOperation = speedhttp.NewOperation[struct{}, OpsHealth](
	"ops.health",
	http.MethodGet,
	healthPath,
	"The process is alive",
	"Reads nothing and always answers 200, so any answer at all means the process is up. "+
		"The route is mounted on the endpoint named by ops.endpoint (default \"ops\"), which "+
		"the host assembles unguarded: a probe needs no credential and no tenant.",
	nil,
	[]speedhttp.Response{{
		Status:      http.StatusOK,
		Description: "Alive",
		Body:        &speedhttp.Body{MediaType: "application/json"},
	}},
)

// readinessOperation declares GET /ready. Its handler builds the answer because
// an unavailable process is a 503 carrying the readiness body, not a problem
// document: both statuses are declared with the same structure.
var readinessOperation = speedhttp.NewReplyOperation[struct{}](
	"ops.readiness",
	http.MethodGet,
	readyPath,
	"Every declared endpoint accepts requests and the database answers",
	"The verdict is read fresh on every request and nothing is cached or polled in the background: "+
		"each endpoint the process declared must be accepting requests, and when a database is "+
		"assembled it is pinged once. Only process-level availability is reported, never tenant or "+
		"business state. 200 means every check passed; 503 names each part that failed in failures.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "Ready",
			Body:        &speedhttp.Body{MediaType: "application/json", Structure: reflect.TypeOf(OpsReadiness{})},
		},
		{
			Status:      http.StatusServiceUnavailable,
			Description: "Not ready, naming each part that failed",
			Body:        &speedhttp.Body{MediaType: "application/json", Structure: reflect.TypeOf(OpsReadiness{})},
		},
	},
)

// documentOperation declares GET /openapi.json. Its handler builds the answer
// from the bytes merged at startup: the body is an opaque document that is
// already encoded, so it is written as bytes rather than re-encoded from a
// value.
var documentOperation = speedhttp.NewReplyOperation[struct{}](
	"ops.document",
	http.MethodGet,
	documentPath,
	"The API document, merged from every module's fragment",
	"Serves the document itself rather than a wrapper, merged once at startup and never recomputed, "+
		"so every request reads the same bytes. Every declared fragment is merged, including those of "+
		"modules that do not run in this command, because the document states which API this binary "+
		"provides. Its title and version come from ops.title and ops.version, and it carries top-level "+
		"openapi, info, paths, components, x-fragments, and a per-operation x-endpoint naming the "+
		"endpoint that serves each route.",
	nil,
	[]speedhttp.Response{{
		Status:      http.StatusOK,
		Description: "The merged document",
		Body:        &speedhttp.Body{MediaType: "application/json", Structure: reflect.TypeOf(OpsDocument{})},
	}},
)

// operations is this module's declaration of the API it serves. The fragment is
// generated from it; the routes are mounted from the same declarations.
var operations = speedhttp.Operations{healthOperation, readinessOperation, documentOperation}
