package sso

import (
	"net/http"

	speedhttp "github.com/vislake/speed/pkg/http"
)

// The fragment's info section: the title this module's fragment carries and the
// version of the fragment's shape.
const (
	fragmentTitle   = "authn sso"
	fragmentVersion = "1.0.0"
)

// discoverPath is the route the domain discovery answers on, relative to the
// module's API root. The fragment writes the same declaration, and the mounting
// resolves it against the API root, so the path is written once.
const discoverPath = "/auth/sso/discover"

// discoverRequest is the declared input of the discovery: the email address
// whose domain is looked up. Only the domain part is read.
type discoverRequest struct {
	_ struct{} `description:"The discovery request. It carries the email address and nothing else; an unknown field is refused as a bad request."`

	Email string `json:"email" description:"The full email address. Only the text after the last '@' is used as the domain; the local part is discarded, and an address with no '@' is a bad request."`
}

// AuthnSsoProvider is one SSO sign-in entry a client can offer for a domain. It
// carries only public identity; no endpoint, configuration or secret is exposed.
type AuthnSsoProvider struct {
	_ struct{} `description:"One SSO sign-in entry a client can offer for a domain. It carries only public identity; no endpoint, configuration or secret is exposed."`

	ID       string `json:"id" required:"true" description:"The provider's opaque identifier, used to start a sign-in flow for this provider."`
	Name     string `json:"name" required:"true" description:"The tenant-chosen display name of the provider, meant to be shown on the sign-in screen."`
	Protocol string `json:"protocol" required:"true" enum:"oidc,saml" description:"The protocol the provider's sign-in entry uses"`
}

// discoverResponse is the answer, always of the same shape: an empty providers
// list when nothing matches. It is an alias of an anonymous struct, so the
// fragment publishes it inline rather than as a component of its own.
type discoverResponse = struct {
	_ struct{} `description:"The discovery answer, always of this shape. The providers array is present whether or not anything matches."`

	Providers []AuthnSsoProvider `json:"providers" required:"true" description:"The providers that have verified the domain and are enabled, in ascending id order; an empty array when none match."`
}

// discoverOperation declares the route that answers the sign-in entries of an
// email's domain. Its handler receives the declared input and returns the
// declared answer, so the entry point binds the request and writes the 200.
var discoverOperation = speedhttp.NewOperation[discoverRequest, discoverResponse](
	"authn.sso.discover",
	http.MethodPost,
	discoverPath,
	"The sign-in entries that exist for an email address's domain",
	"Answers the SSO sign-in entries of an email address's domain. It is public and needs no tenant "+
		"context. Only the text after the last '@' is used; the local part is never looked up, so the "+
		"answer does not reveal whether an account exists. The domain is lowercased and trimmed, then "+
		"must be printable ASCII: an address with no '@', or a domain that is empty, over 253 bytes, "+
		"non-ASCII, has a label that is empty or over 63 bytes, or is not made of labels of letters, "+
		"digits and inner hyphens, is refused. Only providers that have verified the domain, are "+
		"enabled, and whose protocol is enabled in this run are named, in ascending id order; an "+
		"empty list is answered when none match, in the same shape and timing as a match. The body "+
		"must be application/json and may carry no field "+
		"other than email. The response is sent with Cache-Control: no-store.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The email address whose domain is looked up. Required; only the domain part is read.",
		Required:    true,
	},
	[]speedhttp.Response{
		speedhttp.ProblemResponseOf(http.StatusBadRequest,
			"The body could not be read, or it names no domain", typePrefix+"bad-request"),
		speedhttp.ProblemResponse(http.StatusRequestEntityTooLarge,
			"The request body is over the endpoint's limit"),
		speedhttp.ProblemResponseOf(http.StatusUnsupportedMediaType,
			"The request body is not application/json", typePrefix+"unsupported-media-type"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable,
			"The provider register could not be reached, or the run-time login-method switch could not be read"),
		{
			Status: http.StatusOK,
			Description: "The providers that have verified the domain and are enabled, leaving out those whose " +
				"login method the run-time switch has turned off; an empty list when none match",
			Body: &speedhttp.Body{MediaType: "application/json"},
		},
	},
	speedhttp.Public(),
)

// operations is this module's declaration of the API it serves. The fragment is
// generated from it; the route is mounted from the same declaration.
var operations = speedhttp.Operations{
	discoverOperation,
}
