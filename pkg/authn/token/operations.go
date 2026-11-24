package token

import (
	"net/http"
	"reflect"

	"github.com/vislake/speed/pkg/authn/internal/mount"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// The fragment's info section: the title this module's fragment carries and the
// version of the fragment's shape.
const (
	fragmentTitle   = "authn token"
	fragmentVersion = "1.0.0"
)

// The routes as the fragment writes them: relative to the API root the module's
// Spec declares. The mounter registration resolves the same declaration against
// the capability's base, so the path is written once.
const (
	pathJWKS     = "/auth/jwks.json"
	pathIssuance = "/auth/token"
	pathRevoke   = "/auth/token/revoke"
)

// tokenSpec is the API root this module's fragment and routes hang off. Its
// prefix and version are the common ones; the fragment writes the capability's
// own segment, so a route's declared path begins with the part of the mounter's
// base that lies below this root.
var tokenSpec = speedhttp.Spec{
	Endpoint: defaultEndpoint,
	Prefix:   "api",
	Version:  "v1",
	Document: tokenFragment,
}

// The routes as the mounter takes them, derived from the declared paths. They
// are package variables, not constants, because they are computed from the
// declaration: a change to a declared path moves the registered route with it.
// The three are a stable closed set: a reader outside the module writes them
// down.
//
// The declared path is resolved against the Spec's API root and reduced by the
// capability's default base. The base path is a configuration item, and the
// design moves the routes with it without moving the contract: what a
// deployment configures is the segment below the API root, so the part this
// capability owns is the default base less that root, not whatever base a
// deployment configured.
var (
	jwksPath   = mountRel(pathJWKS)
	tokenPath  = mountRel(pathIssuance)
	revokePath = mountRel(pathRevoke)
)

// mountRel translates one declared route into the relative path the mounter
// joins to its base. A declared route that does not begin with the capability's
// mounted path is a declaration defect, not something to mount at a guessed
// path.
func mountRel(declared string) string {
	rel, err := mount.RelativePath(mount.DefaultBasePath, tokenSpec.ResolvePath(declared))
	if err != nil {
		panic(diagnosticPrefix + ModuleName + ": " + err.Error())
	}
	return rel
}

// The form media type the two POST endpoints read, and the keys their bodies
// declare. The entry point binds a form body through these tags; routes.go
// reduces the body to the same key set before the binding runs, so the
// endpoint's own lenient reading of a form is preserved.
const formMediaType = "application/x-www-form-urlencoded"

// AuthnTokenResponse is the success body of POST /auth/token, per RFC 6749
// §5.1. A client_credentials response carries no refresh_token.
type AuthnTokenResponse struct {
	_ struct{} "description:\"The success body of `POST /auth/token`, per RFC 6749 §5.1. A `client_credentials` response carries no `refresh_token`.\""

	// AccessToken is the issued access token.
	AccessToken string "json:\"access_token\" required:\"true\" description:\"The signed access token: a compact JWT, presented as a `Bearer` credential.\""
	// TokenType is Bearer.
	TokenType string `json:"token_type" required:"true" description:"Always Bearer"`
	// ExpiresIn is the access token lifetime in seconds.
	ExpiresIn int `json:"expires_in" required:"true" description:"The access token's remaining lifetime in whole seconds"`
	// RefreshToken is the issued refresh token, when one is issued.
	RefreshToken string `json:"refresh_token,omitempty" description:"Present only for the refresh_token grant"`
}

// AuthnTokenJWKS is the public key set published on GET /auth/jwks.json,
// served as application/jwk-set+json. It holds the public half of every key
// loaded at startup and no private field.
type AuthnTokenJWKS struct {
	_ struct{} "description:\"The public key set published on `GET /auth/jwks.json`, served as `application/jwk-set+json`. It holds the public half of every key loaded at startup and no private field.\""

	// Keys are the published public keys.
	Keys []AuthnTokenJWK `json:"keys" required:"true" description:"Every configured key's public JWK; the set is never empty."`
}

// AuthnTokenJWK is one public JSON Web Key. The published keys carry kty, kid,
// alg and use; the remaining members depend on the key type: an EC key carries
// crv, x and y, an RSA key carries n and e.
type AuthnTokenJWK struct {
	_ struct{} "additionalProperties:\"true\" description:\"One public JSON Web Key. The published keys carry `kty`, `kid`, `alg` and `use`; the remaining members depend on the key type: an EC key carries `crv`, `x` and `y`, an RSA key carries `n` and `e`.\""

	// Kty is the key type.
	Kty string `json:"kty" required:"true" description:"The key type, for example EC or RSA"`
	// Use is the key use, sig.
	Use string `json:"use" description:"The key use, signature"`
	// Kid is the key identifier, the RFC 7638 thumbprint.
	Kid string `json:"kid" required:"true" description:"The RFC 7638 thumbprint of the public key"`
	// Alg is the key algorithm.
	Alg string `json:"alg" description:"The signature algorithm the key signs and verifies with"`
	// Crv is the elliptic curve, for an EC key.
	Crv string "json:\"crv\" description:\"The curve of an EC key; only P-256 is supported, so this is `P-256`.\""
	// X is the EC x coordinate.
	X string `json:"x" description:"The x coordinate of an EC public key, base64url-encoded without padding."`
	// Y is the EC y coordinate.
	Y string `json:"y" description:"The y coordinate of an EC public key, base64url-encoded without padding."`
	// N is the RSA modulus.
	N string `json:"n" description:"The modulus of an RSA public key, at least 2048 bits, base64url-encoded without padding."`
	// E is the RSA public exponent.
	E string `json:"e" description:"The public exponent of an RSA public key, base64url-encoded without padding."`
}

// AuthnTokenOAuthError is the failure body of RFC 6749 §5.2, which the token
// and revocation endpoints answer in place of the entry point's problem+json.
// Its only member is error; no error_description accompanies it.
type AuthnTokenOAuthError struct {
	_ struct{} "description:\"The failure body of RFC 6749 §5.2, which the token and revocation endpoints answer in place of the entry point's problem+json. Its only member is `error`; no `error_description` accompanies it.\""

	// Error is the RFC 6749 error code.
	Error string "json:\"error\" required:\"true\" enum:\"invalid_request,invalid_client,invalid_grant,unsupported_grant_type,temporarily_unavailable\" description:\"The machine-readable failure code, one of `invalid_request`, `invalid_client`, `invalid_grant`, `unsupported_grant_type` and `temporarily_unavailable`.\""
}

// tokenIssueInput is the declaration's input for POST /auth/token: the closed
// set of form fields the two grants read. Every field is optional to the binder
// because the endpoint answers the missing ones in its own RFC 6749 §5.2 shape
// rather than with the entry point's 422. The client credential an
// Authorization: Basic header carries is not a declared parameter: the endpoint
// reads the header before the form, and the routing layer folds a usable one
// into client_id and client_secret the way it has always been read.
type tokenIssueInput struct {
	GrantType    string "form:\"grant_type\" enum:\"client_credentials,refresh_token\" description:\"The grant to perform, one of `client_credentials` and `refresh_token`. Required; any other value answers `unsupported_grant_type`.\""
	RefreshToken string "form:\"refresh_token\" description:\"The refresh token to rotate, read only for the `refresh_token` grant, where a missing value answers `invalid_request`. The response carries the rotated replacement.\""
	ClientID     string "form:\"client_id\" description:\"The service account identifier, read together with `client_secret` only when the request has no usable `Authorization: Basic` header.\""
	ClientSecret string "form:\"client_secret\" description:\"The service account secret, read together with `client_id`. A missing identifier or secret answers `invalid_client`.\""
}

// revokeInput is the declaration's input for POST /auth/token/revoke: its only
// read field is the token.
type revokeInput struct {
	Token string `form:"token" description:"The token to revoke: a refresh token, an access token, or any unrecognised string. The response does not reveal which kind it was or whether it existed."`
}

// The request bodies. The 8 KiB cap and the lenient reading of the form are the
// endpoint's own and are applied before binding; the declaration publishes the
// media type and the closed field set a client may send. Neither body is
// required: an absent or unreadable body is normalized to an empty form and
// answered in the endpoint's own RFC 6749 §5.2 / RFC 7009 shape, so the entry
// point must let the handler run rather than refuse the request first.
var (
	tokenRequest = &speedhttp.RequestBody{
		MediaType:   formMediaType,
		Description: "The grant request as a form. `grant_type` is required and selects which of the remaining fields are read.",
	}
	revokeRequest = &speedhttp.RequestBody{
		MediaType:   formMediaType,
		Description: "The revocation request as a form; `token` names the token to revoke.",
	}
)

var challengeHeader = speedhttp.Header{
	Name:        "WWW-Authenticate",
	Description: "The Basic challenge of the client-authentication failure.",
}

// oauthErrorResponse is one RFC 6749 §5.2 answer: a JSON object whose only
// member is error. Its structure is the declaration's, so the entry point
// checks the body the handler builds against the contract.
func oauthErrorResponse(status int, description string) speedhttp.Response {
	return speedhttp.Response{
		Status:      status,
		Description: description,
		Body: &speedhttp.Body{
			MediaType: "application/json",
			Structure: reflect.TypeOf(AuthnTokenOAuthError{}),
		},
	}
}

// invalidClientResponse is the 401 of a client that did not authenticate: the
// RFC 6749 §5.2 body and the Basic challenge the scheme asks for. The
// challenge's value is written by the handler, because the header belongs to
// the answer rather than to the declaration.
var invalidClientResponse = func() speedhttp.Response {
	response := oauthErrorResponse(http.StatusUnauthorized, "invalid_client, in the RFC 6749 §5.2 body")
	response.Headers = []speedhttp.Header{challengeHeader}
	return response
}()

// jwksOperation declares GET /auth/jwks.json. Its handler builds the answer
// because the body's media type is not JSON, which the simple form cannot write.
var jwksOperation = speedhttp.NewReplyOperation[struct{}](
	"authn.token.jwks",
	http.MethodGet,
	pathJWKS,
	"The public keys that verify the issued tokens",
	"The public halves of the keys loaded at startup, each with its RFC 7638 thumbprint as `kid`, its "+
		"algorithm and `use` of `sig`; no private field is included. The body is fixed for the life of "+
		"the process and holds no secret, so this is the one authn response with a public cache: "+
		"`Cache-Control: public, max-age=300`, the window a key rotation waits out before switching the "+
		"signing key.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "The JWK set of the startup key set",
			Headers: []speedhttp.Header{{
				Name:        "Cache-Control",
				Description: "The one public cache of the capability: the body holds no secret.",
				Value:       jwksCacheControl,
			}},
			Body: &speedhttp.Body{
				MediaType: jwksContentType,
				Structure: reflect.TypeOf(AuthnTokenJWKS{}),
			},
		},
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "The key set could not be written"),
	},
	speedhttp.Public(),
)

// tokenOperation declares POST /auth/token: the RFC 6749 token endpoint. Its
// handler builds every answer because the failures are the RFC 6749 §5.2 body,
// never a problem document, and because a 401 carries a challenge and
// sometimes Retry-After.
var tokenOperation = speedhttp.NewReplyOperation[tokenIssueInput](
	"authn.token.issue",
	http.MethodPost,
	pathIssuance,
	"Exchange a client credential or a refresh token for an access token",
	"The RFC 6749 token endpoint. The body is `application/x-www-form-urlencoded` and is capped at 8 "+
		"KiB; a body that is not a form, is over the limit, or omits `grant_type` answers "+
		"`invalid_request`, and a `grant_type` outside the closed set `client_credentials` and "+
		"`refresh_token` answers `unsupported_grant_type`. Client authentication is read from "+
		"`Authorization: Basic` first and from the `client_id` and `client_secret` form fields only "+
		"when no usable Basic header is present. A success carries `Cache-Control: no-store` and "+
		"`Pragma: no-cache`; failures carry the RFC 6749 §5.2 body whose only member is `error`, with "+
		"no `error_description`, and are never problem+json.",
	tokenRequest,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "The access token, and a refresh token for the refresh_token grant",
			Headers: []speedhttp.Header{{
				Name:        "Pragma",
				Description: "RFC 6749 §5.1 asks for no-cache, for a client that only understands the older header.",
				Value:       "no-cache",
			}},
			Body: &speedhttp.Body{
				MediaType: "application/json",
				Structure: reflect.TypeOf(AuthnTokenResponse{}),
			},
		},
		oauthErrorResponse(http.StatusBadRequest,
			"invalid_request / unsupported_grant_type / invalid_grant"),
		invalidClientResponse,
		oauthErrorResponse(http.StatusServiceUnavailable, "temporarily_unavailable"),
	},
	speedhttp.Public(),
)

// revokeOperation declares POST /auth/token/revoke: the RFC 7009 revocation
// endpoint. Its handler builds the answer because the success answer has no
// body while the failure is the RFC 6749 §5.2 shape.
var revokeOperation = speedhttp.NewReplyOperation[revokeInput](
	"authn.token.revoke",
	http.MethodPost,
	pathRevoke,
	"Revoke a refresh token",
	"The RFC 7009 revocation endpoint. The body is `application/x-www-form-urlencoded`, capped at 8 "+
		"KiB, and its only read field is `token`. The answer is 200 with an empty body whatever the "+
		"token's state — valid, invalid, unknown, or a body that cannot be read — so a presenter "+
		"cannot use the endpoint to probe whether a token exists. A refresh token revokes the session "+
		"it belongs to, an ordinary access token whose signature holds revokes its `sid`'s session, an "+
		"impersonation token ends its record and leaves the administrator's session alone, and a "+
		"service access token is a no-op. Only a dependency that cannot answer gives 503 "+
		"`temporarily_unavailable`, in the RFC 6749 §5.2 body, rather than reporting a revocation that "+
		"did not happen.",
	revokeRequest,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "Answered whatever the token's state, with an empty body",
		},
		oauthErrorResponse(http.StatusServiceUnavailable,
			"temporarily_unavailable — the token store could not answer"),
	},
	speedhttp.Public(),
)

var operations = speedhttp.Operations{
	jwksOperation,
	tokenOperation,
	revokeOperation,
}
