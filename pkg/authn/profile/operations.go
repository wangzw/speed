package profile

import (
	"net/http"

	"github.com/vislake/speed/pkg/authn/internal/mount"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// The fragment's info section: the title this module's fragment carries and the
// version of the fragment's shape.
const (
	fragmentTitle   = "authn profile"
	fragmentVersion = "1.0.0"
)

// pathMe is the route as the fragment writes it: relative to the module's API
// root, so it carries the capability-owned `/auth` segment the mounter's base
// ends with. The mounter registration derives its relative path from the same
// declaration, so the route is written once.
const pathMe = "/auth/me"

// challengeHeader declares the header every 401 on this module's routes
// carries: the gateway writes the endpoint's challenge when it refuses the
// request first, and a handler that answers 401 itself returns it in the fault's
// headers. Either way the exchange is a challenge, so the response declares the
// header and a handler's fault may write it.
var challengeHeader = speedhttp.Header{
	Name:        "WWW-Authenticate",
	Description: "The capability's challenge: the Bearer and Cookie schemes, never Basic.",
}

// capabilityProblem is an error answer the entry point writes itself, before a
// handler runs: the binding refusals of the write route. Its problem type is
// the capability's code for the status, so a body refused before the handler is
// answered in the same vocabulary as one the handler refuses.
func capabilityProblem(status int, code, description string) speedhttp.Response {
	response := speedhttp.ProblemResponse(status, description)
	response.ProblemType = typePrefix + code
	return response
}

// unauthorized is the declaration of a 401 that carries the capability's
// challenge.
func unauthorized(description string) speedhttp.Response {
	response := speedhttp.ProblemResponse(http.StatusUnauthorized, description)
	response.Headers = []speedhttp.Header{challengeHeader}
	return response
}

// getOperation declares the read route and binds its types: the handler
// receives no input and returns the view, so the entry point binds the request
// and writes the declared 200.
var getOperation = speedhttp.NewOperation[struct{}, AuthnProfileProfile](
	"authn.profile.get",
	http.MethodGet,
	pathMe,
	"The caller's own profile",
	"The caller's own account, read from the identity the gateway left on the request; no path "+
		"parameter or body names an account. An impersonating identity may reach this route and then "+
		"reads the account it is impersonating. The answer also carries the account's email "+
		"verification state, read on this request and never stored in a session or an access token, and each "+
		"identifier's proven flag. Every time that is present is RFC 3339 in UTC, and a "+
		"zero instant in a time field other than last_login_at is the empty string; last_login_at is "+
		"instead absent when the binding has never been used to sign in. The identity's own account "+
		"being gone (a tombstone, or a row that cannot be read) is a 401, the same answer a request "+
		"with no identity gets.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "The account, its identifiers and its external bindings",
			Body:        &speedhttp.Body{MediaType: "application/json"},
		},
		unauthorized("No usable credential, or the identity's own account is gone"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "The account store could not be reached"),
	},
)

// patchOperation declares the write route and binds its types: the handler
// receives the declared body and returns the view, so the entry point binds the
// members and writes the declared 200. The 415 is the entry point's own refusal
// of a body that is not application/json.
var patchOperation = speedhttp.NewOperation[patchInput, AuthnProfilePatch](
	"authn.profile.patch",
	http.MethodPatch,
	pathMe,
	"Write the caller's own display name, attribute set, locale, time zone and color mode",
	"Writes the caller's own display name, its whole attribute set, and the language, time zone and "+
		"color mode named inside that set; every body field is optional and a field left out is left as "+
		"it is. The display name is written before the attribute set, and the two writes are not one "+
		"atomic change: when the second fails, the first is already in force and the answer is that "+
		"failure's. An impersonating identity writes the impersonated account's profile like any other "+
		"caller; a caller with no login session is refused 401 before anything is written. The write asks "+
		"for no freshness beyond the login session itself.",
	&speedhttp.RequestBody{
		MediaType: "application/json",
		Description: "Fields to write; all are optional and an absent field is left unchanged, while " +
			"an unknown field is refused. locale, time_zone and color_mode are trimmed of surrounding " +
			"whitespace before they are checked and stored, and an empty one clears its key.",
		Required: true,
	},
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "The display name and attribute set as written",
			Body:        &speedhttp.Body{MediaType: "application/json"},
		},
		capabilityProblem(http.StatusBadRequest, mount.CodeBadRequest,
			"The body could not be read, or a value is outside its domain"),
		speedhttp.ProblemResponse(http.StatusRequestEntityTooLarge,
			"The request body is over the endpoint's limit"),
		unauthorized("No usable credential, or the caller has no login session"),
		capabilityProblem(http.StatusUnsupportedMediaType, mount.CodeUnsupportedMediaType,
			"The request body is not application/json"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// operations is this module's declaration of the API it serves. The fragment is
// generated from it; the routes are mounted from the same declarations.
var operations = speedhttp.Operations{
	getOperation,
	patchOperation,
}
