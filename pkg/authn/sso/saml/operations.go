package saml

import (
	"net/http"

	speedhttp "github.com/vislake/speed/pkg/http"
)

// The fragment's info section: the title this module's fragment carries and the
// version of the fragment's shape.
const (
	fragmentTitle   = "authn sso saml"
	fragmentVersion = "1.0.0"
)

// metadataPath is the metadata route as the fragment writes it, relative to the
// module's API root. The provider segment is an enterprise provider's
// identifier. The mounter registration resolves the same declaration against its
// base, so the route is written once.
const metadataPath = "/auth/sso/saml/:provider/metadata"

// metadataInput is the declaration's input for the metadata route: the provider
// identifier is a path parameter and the operation takes no body.
type metadataInput struct {
	Provider string `path:"provider" description:"The identifier of the enterprise provider whose service provider metadata is requested; the provider must be enabled and registered with the saml protocol."`
}

// metadataOperation declares the route that publishes one provider's service
// provider metadata. The handler builds the answer because the success body is
// an XML document rather than a JSON value.
var metadataOperation = speedhttp.NewReplyOperation[metadataInput](
	"authn.sso.saml.metadata",
	http.MethodGet,
	metadataPath,
	"The service provider metadata for one SAML provider",
	"Returns the SAML 2.0 service provider entity descriptor that the identity provider side is "+
		"registered against, as XML. The entityID is this metadata address itself, so each provider sees "+
		"a distinct service provider; the assertion consumer service points at the engine's callback "+
		"route for the same provider with the HTTP-POST binding, WantAssertionsSigned is true, and "+
		"AuthnRequestsSigned follows the provider's sign_requests setting. Every URL is derived from the "+
		"configured public address, never from the Host or X-Forwarded-* headers. When the deployment is "+
		"configured with an SP certificate, the descriptor carries the current SP signing certificate "+
		"and, while a rotation is in progress, the next certificate beside it; a deployment that "+
		"configures neither an SP key nor an SP certificate publishes no key descriptor at all. The "+
		"descriptor is declared valid for seven days from generation. The route is public. It answers "+
		"404 when the identifier names no provider, a disabled provider, or one whose protocol is not "+
		"saml; 503 when the provider register cannot be reached.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "The entity descriptor, as application/samlmetadata+xml",
			Body:        &speedhttp.Body{MediaType: contentTypeMetadata},
		},
		{
			Status:      http.StatusNotFound,
			Description: "No enabled SAML provider has this identifier",
		},
		speedhttp.ProblemResponse(http.StatusServiceUnavailable,
			"The provider register could not be reached"),
	},
)

// operations is this module's declaration of the API it serves. The fragment is
// generated from it; the routes are mounted from the same declarations.
var operations = speedhttp.Operations{
	metadataOperation,
}
