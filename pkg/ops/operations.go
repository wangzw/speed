package ops

import (
	"net/http"
	"reflect"

	speedhttp "github.com/vislake/speed/pkg/http"
)

const (
	fragmentTitle   = "operations"
	fragmentVersion = "1.0.0"
)

// OpsHealth is the liveness response body: a single status field, always ok on a
// process that can answer.
type OpsHealth struct {
	_ struct{} `description:"The liveness response body: a single status field, always ok on a process that can answer."`

	// Status is the liveness verdict, always "ok" on a process that can answer.
	Status string `json:"status" required:"true" enum:"ok" description:"The literal the liveness route writes; a live process always answers ok"`
}

// OpsReadiness is the readiness response body: the verdict and, when it is
// unavailable, one line per part that failed.
type OpsReadiness struct {
	_ struct{} `description:"The readiness response body, carrying the verdict and, when it is unavailable, one line per part that failed."`

	// Status is the readiness verdict: "ready" or "unavailable".
	Status string `json:"status" required:"true" enum:"ready,unavailable" description:"ready when every part answered, unavailable otherwise"`
	// Failures lists one line per part that failed, absent when the verdict is
	// ready.
	Failures []string `json:"failures,omitempty" description:"One line per part that failed; absent when the verdict is ready"`
}

// OpsDocument is the merged OpenAPI document, published as a free-form object.
type OpsDocument struct {
	_ struct{} `additionalProperties:"true" description:"The merged OpenAPI document: the version it declares, its info section, every merged path, the components its fragments declared, and x-fragments naming each source"`
}

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
	speedhttp.Public(),
)

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
	speedhttp.Public(),
)

var documentOperation = speedhttp.NewReplyOperation[struct{}](
	"ops.document",
	http.MethodGet,
	documentPath,
	"The API document, merged from every module's fragment",
	"Serves the document itself rather than a wrapper, merged once at startup and never recomputed, "+
		"so every request reads the same bytes. Every declared fragment is merged, including those of "+
		"modules that do not run in this command, because the document states which API this binary "+
		"provides. Its title, version, description and servers come from ops.title, ops.version, "+
		"ops.description and ops.servers; it carries top-level openapi, info, servers, paths, "+
		"components, tags and x-fragments; every operation is tagged with the module that declared "+
		"it, names its security requirement, and carries an x-endpoint naming the endpoint that "+
		"serves the route.",
	nil,
	[]speedhttp.Response{{
		Status:      http.StatusOK,
		Description: "The merged document",
		Body:        &speedhttp.Body{MediaType: "application/json", Structure: reflect.TypeOf(OpsDocument{})},
	}},
	speedhttp.Public(),
)

var operations = speedhttp.Operations{healthOperation, readinessOperation, documentOperation}
