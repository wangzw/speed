package board

import (
	"net/http"
	"reflect"

	speedhttp "github.com/vislake/speed/pkg/http"
)

// The fragment's info section. Every module's fragment carries its own module
// name and the version of the fragment's shape.
const (
	fragmentTitle   = "feature.board"
	fragmentVersion = "1.0.0"
)

// FeatureBoardDocument is the document the read route answers: the feature
// names this process serves.
type FeatureBoardDocument struct {
	_ struct{} `description:"The feature document: the feature names this process serves, decided by the assembly at start and frozen before the first request, so it is the same for every caller and every tenant. The object is extensible — a field added beside features is a compatible change — and carries no feature of its own."`

	Features []string `json:"features" required:"true" description:"The feature names this process serves, ascending and without repetition. Each name answers whether a frontend surface the caller's build carries is served here; the response carries no configuration value, method descriptor or rendering description."`
}

// featureBoardHeaders are the read route's response headers.
type featureBoardHeaders struct {
	CacheControl string `header:"Cache-Control" description:"Always no-store: the document must not be cached across sessions."`
}

// operations declares this module's API. The fragment is generated from it;
// the handler and the route below read the same declaration.
var operations = speedhttp.Operations{{
	ID:          "feature.board.list",
	Method:      http.MethodGet,
	Path:        pathFeatures,
	Summary:     "Read the feature document this process serves",
	Description: "The feature names this process serves, ascending and without repetition. The set is decided by the assembly at start and is the same for every caller; it is not filtered by permission or tenant. Its content may differ between callers and tenants (a tenant-level narrowing is a future addition), so a response must not be cached across sessions.",
	Responses: []speedhttp.Response{
		{
			Status:      200,
			Description: "The feature document. Its content may differ per caller and tenant, and must not be cached across sessions.",
			Headers:     reflect.TypeOf(featureBoardHeaders{}),
			Body: &speedhttp.Body{
				MediaType: "application/json",
				Structure: reflect.TypeOf(FeatureBoardDocument{}),
			},
		},
		{
			Status:      401,
			Description: "The request carries no identity",
			Body: &speedhttp.Body{
				MediaType: "application/problem+json",
				Structure: reflect.TypeOf(speedhttp.ProblemDocument{}),
			},
		},
	},
}}
