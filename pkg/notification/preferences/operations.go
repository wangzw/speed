package preferences

import (
	"net/http"

	speedhttp "github.com/vislake/speed/pkg/http"
)

// The fragment's info section. The title and the version are the ones the
// hand-written fragment published before the declaration generated it.
const (
	fragmentTitle   = "notification preferences"
	fragmentVersion = "1.0.0"
)

// The paths the operations are declared at, relative to the API root the Spec
// carries. The mounting resolves them against that root, so the path is written
// once and the fragment keeps the relative form the platform publishes.
const (
	pathTypesFragment             = "/notifications/types"
	pathPreferencesFragment       = "/notifications/preferences"
	pathPreferenceChannelFragment = "/notifications/preferences/:type_key/:channel"
)

// The success bodies. Each is an alias of an anonymous struct, so the fragment
// publishes the wrapper inline, as the hand-written fragment did, rather than as
// a component of its own.
type (
	// typesBody is the 200 answer of GET /types: the taxonomy itself.
	typesBody = struct {
		_     struct{}           `description:"The taxonomy: one entry per notification type, ordered by type key."`
		Types []NotificationType `json:"types" required:"true" description:"Every type collected for this process, ordered by type key; the set comes from module declarations rather than from a table."`
	}

	// preferencesBody is the 200 answer of GET /preferences: one row per type.
	preferencesBody = struct {
		_           struct{}                 `description:"The caller's preference matrix: one row per notification type in the taxonomy, ordered by type key."`
		Preferences []NotificationPreference `json:"preferences" required:"true" description:"One row per type in the taxonomy, ordered by type key; each row's channels is the caller's effective set for that type."`
	}

	// preferenceSetBody is the 200 answer of the PUT: the type's effective set.
	preferenceSetBody = struct {
		_        struct{} `description:"The type's effective channel set after the update."`
		TypeKey  string   `json:"type_key" required:"true" description:"The type key the update applied to, echoed from the path."`
		Channels []string `json:"channels" required:"true" description:"The type's effective channel set after the update: the stored channels when a row exists, otherwise the type's default channels, deduplicated and stably ordered."`
	}
)

// SetPreferenceInput is the request of the PUT: the two path parameters and the
// body's target state. The state is a pointer so that a body naming none is
// told apart from one naming false.
type SetPreferenceInput struct {
	_       struct{} `description:"The body of a single-channel preference update."`
	TypeKey string   `path:"type_key" description:"The notification type key, as listed by GET /notifications/types; a key not in the taxonomy is answered 400."`
	Channel string   `path:"channel" enum:"in_app,email,sms" description:"The channel to turn on or off: one of in_app, email, sms. Any other value is answered 400 before the no-op check."`
	Enabled *bool    `json:"enabled" required:"true" description:"The target state of the channel; a body naming none is told apart from one naming false"`
}

// problemResponse declares one error answer in the default problem shape, with
// the given response headers.
func problemResponse(status int, description string, headers ...speedhttp.Header) speedhttp.Response {
	response := speedhttp.ProblemResponse(status, description)
	response.Headers = headers
	return response
}

// listTypesOperation declares GET /types: the taxonomy every recipient shares.
var listTypesOperation = speedhttp.NewOperation[struct{}, typesBody](
	"notification.preferences.listTypes",
	http.MethodGet,
	pathTypesFragment,
	"The notification type taxonomy, one list every recipient shares",
	"Returns every notification type collected from the registered modules, ordered by type key. The list is the same for every recipient: it comes from module declarations, not from stored preference rows, so it does not depend on the caller or the tenant. The route declares no permission gate of its own; who may call it is the host's decision. The response keys its representation by the negotiated language (Vary: Accept-Language) even though the body itself is not translated.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "The taxonomy",
			Headers: []speedhttp.Header{{
				Name:        "Vary",
				Description: "Accept-Language: the body does not vary by language, but this module's error responses do.",
				Value:       "Accept-Language",
			}},
			Body: &speedhttp.Body{MediaType: "application/json"},
		},
	},
)

// listPreferencesOperation declares GET /preferences: the caller's matrix.
var listPreferencesOperation = speedhttp.NewOperation[struct{}, preferencesBody](
	"notification.preferences.listPreferences",
	http.MethodGet,
	pathPreferencesFragment,
	"The caller's preference matrix, one row per type in the taxonomy",
	"Returns one row per type in the taxonomy for the caller, ordered by type key. Each row's channels are the effective set: the channels stored for the type when the recipient has a row, otherwise the type's default channels; explicit says which. A type that cannot be unsubscribed always reports its default channels and is never explicit. The response keys its representation by the negotiated language (Vary: Accept-Language). A stored row that cannot be read answers 503.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "The preference matrix; each row's channels are the effective set",
			Headers: []speedhttp.Header{{
				Name:        "Vary",
				Description: "Accept-Language: the body does not vary by language, but this module's error responses do.",
				Value:       "Accept-Language",
			}},
			Body: &speedhttp.Body{MediaType: "application/json"},
		},
		problemResponse(http.StatusServiceUnavailable, "The preference matrix could not be read",
			// The old handler set Vary before it read the table, so a refusal
			// carried it too; a simple operation writes only the success
			// headers itself, so the failure declares it as well.
			speedhttp.Header{
				Name:        "Vary",
				Description: "Accept-Language: the body does not vary by language, but this module's error responses do.",
				Value:       "Accept-Language",
			}),
	},
)

// updatePreferenceOperation declares PUT /preferences/:type_key/:channel: one
// channel is turned on or off and the type's effective set is answered.
var updatePreferenceOperation = speedhttp.NewOperation[SetPreferenceInput, preferenceSetBody](
	"notification.preferences.updatePreference",
	http.MethodPut,
	pathPreferenceChannelFragment,
	"Turn one channel on or off for one type and return that type's effective set",
	"Applies the body's enabled to the channel named in the path and answers with the type's effective channel set after the change. true adds the channel to the current effective set, false removes it; the result is deduplicated and stably ordered. When the target set equals the current effective set the call answers 200 without writing a row, so repeating it is harmless. Only that one channel changes; the type's other channels keep their effective value. Checks run in a fixed order — body, channel, type, unsubscribability — so an unknown channel is never taken for a no-op.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The target state of the channel named in the path; enabled is required and a body without it is answered 400.",
		Required:    true,
		Name:        "NotificationSetPreferenceRequest",
	},
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "The type's effective channel set after the update; a target equal to the current set answers without writing",
			Body:        &speedhttp.Body{MediaType: "application/json"},
		},
		speedhttp.ProblemResponse(http.StatusBadRequest,
			"The body is not readable, the channel is unknown, the type is not in the taxonomy, or no recipient is named"),
		speedhttp.ProblemResponse(http.StatusUnsupportedMediaType,
			"The request body is not application/json"),
		speedhttp.ProblemResponse(http.StatusConflict,
			"The type cannot be unsubscribed"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable,
			"The preference could not be read or written"),
	},
)

// operations is this module's declaration of the API it serves. The fragment is
// generated from it; the routes are mounted from the same declarations.
var operations = speedhttp.Operations{
	listTypesOperation,
	listPreferencesOperation,
	updatePreferenceOperation,
}
