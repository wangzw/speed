package board

import (
	"net/http"

	speedhttp "github.com/vislake/speed/pkg/http"
)

// The fragment's info section. Every module's fragment carries its own module
// name and the version of the fragment's shape.
const (
	fragmentTitle   = "feature.board"
	fragmentVersion = "1.0.0"
)

// FeatureBoardDocument is the document the read route answers: the feature
// names this process serves. It is the handler's success type, so the entry
// point writes it as the declared 200 response.
type FeatureBoardDocument struct {
	_ struct{} `description:"The feature document: the feature names this process serves, decided by the assembly at start and frozen before the first request, so it is the same for every caller and every tenant. The object is extensible — a field added beside features is a compatible change — and carries no feature of its own."`

	Features []string `json:"features" required:"true" description:"The feature names this process serves, ascending and without repetition. Each name answers whether a frontend surface the caller's build carries is served here; the response carries no configuration value, method descriptor or rendering description."`
}

// commonFailures are the answers every route on this module's endpoint shares:
// the authentication gateway refuses a credential-less request before the
// handler runs. A route with an answer of its own replaces the common entry of
// the same status.
var commonFailures = []speedhttp.Response{
	speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
}

// readOperation declares the read route and binds its types: the handler
// receives no input and returns the feature document, so the entry point — not
// the handler — binds the request and writes the answer.
var readOperation = speedhttp.NewOperation[struct{}, FeatureBoardDocument](
	"feature.board.list",
	http.MethodGet,
	pathFeatures,
	"Read the feature document this process serves",
	"The feature names this process serves, ascending and without repetition. The set is decided by the assembly at start and is the same for every caller; it is not filtered by permission or tenant. Its content may differ between callers and tenants (a tenant-level narrowing is a future addition), so a response must not be cached across sessions.",
	"",
	speedhttp.Failures(commonFailures, speedhttp.Response{
		Status:      200,
		Description: "The feature document. Its content may differ per caller and tenant, and must not be cached across sessions.",
		Headers: []speedhttp.Header{{
			Name:        "Cache-Control",
			Description: "Always no-store: the document must not be cached across sessions.",
			Value:       "no-store",
		}},
		Body: &speedhttp.Body{MediaType: "application/json"},
	}),
)

// operations is this module's declaration of the API it serves. The fragment
// is generated from it; the route is mounted from the same declaration.
var operations = speedhttp.Operations{readOperation}
