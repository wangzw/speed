package board

import (
	"net/http"

	speedhttp "github.com/vislake/speed/pkg/http"
)

const (
	fragmentTitle   = "feature.board"
	fragmentVersion = "1.0.0"
)

// FeatureBoardDocument is the document the read route answers: the feature
// names this process serves.
type FeatureBoardDocument struct {
	_ struct{} `description:"The feature document: the feature names this process serves, decided by the assembly at start and frozen before the first request, so it is the same for every caller and every tenant. The object is extensible — a field added beside features is a compatible change — and carries no feature of its own."`

	// Features is the feature names this process serves, ascending and unique.
	Features []string `json:"features" required:"true" description:"The feature names this process serves, ascending and without repetition. Each name answers whether a frontend surface the caller's build carries is served here; the response carries no configuration value, method descriptor or rendering description."`
}

var commonFailures = []speedhttp.Response{
	speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
}

var readOperation = speedhttp.NewOperation[struct{}, FeatureBoardDocument](
	"feature.board.list",
	http.MethodGet,
	pathFeatures,
	"Read the feature document this process serves",
	"The feature names this process serves, ascending and without repetition. The set is decided by the assembly at start and is the same for every caller; it is not filtered by permission or tenant. Its content may differ between callers and tenants (a tenant-level narrowing is a future addition), so a response must not be cached across sessions.",
	nil,
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

var operations = speedhttp.Operations{readOperation}
