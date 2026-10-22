package selfservice

import (
	"net/http"
	"reflect"

	"github.com/vislake/speed/pkg/authn/internal/mount"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// The routes as the fragment writes them: relative to the module's API root, so
// a route is declared once and both the fragment and the mounting read the same
// string.
const (
	pathPassword          = "/auth/password"
	pathPhone             = "/auth/phone"
	pathPhoneVerify       = "/auth/phone/verify"
	pathIdentifiers       = "/auth/identifiers"
	pathIdentifiersVerify = "/auth/identifiers/verify"
	pathIdentifier        = "/auth/identifiers/:id"
	pathLink              = "/auth/links/:id"
)

// AuthnLoginPhoneChallenge is the answer of POST /phone: how long the code lives
// and how soon another may be asked for, in seconds. It carries neither the
// challenge nor anything that tells whether the number is already another
// account's.
type AuthnLoginPhoneChallenge struct {
	_ struct{} `description:"The answer to a phone-verification send: how long the code lives and how soon another code may be asked for, both in whole seconds. It carries neither the challenge identifier nor any fact about whether the number is already held by another account."`

	ExpiresIn   int `json:"expires_in" required:"true" description:"How long the code lives, in seconds"`
	ResendAfter int `json:"resend_after" required:"true" description:"How soon another code may be asked for, in seconds"`
}

// AuthnLoginIdentifierState is the answer of POST /identifiers and, with the
// state added, of POST /identifiers/verify: what the call did, and for a pending
// email how long the code lives and how soon another may be asked for.
type AuthnLoginIdentifierState struct {
	_ struct{} "description:\"The answer of adding an identifier: `added` when the identifier is written, `pending` when a code was sent to an email address and the answer is still to come. The two durations are present only in the pending state.\""

	State       string `json:"state" required:"true" enum:"added,pending" description:"added when the identifier is written; pending when a code was sent to an address and its answer is still to come"`
	ExpiresIn   int    `json:"expires_in,omitempty" description:"How long the code lives, in seconds; absent when the state is added"`
	ResendAfter int    `json:"resend_after,omitempty" description:"How soon another code may be asked for, in seconds; absent when the state is added"`
}

// The internal names the handlers use for the wire shapes.
type (
	startPhoneResponse    = AuthnLoginPhoneChallenge
	addIdentifierResponse = AuthnLoginIdentifierState
)

// changePasswordInput is the body of PUT /password. The current password is
// optional: an account that signed up through a provider has none.
type changePasswordInput struct {
	_ struct{} `description:"The password change: the password the account has now, when it has one, and the new one."`

	Current string `json:"current_password" description:"The password the account has now. Empty or absent is allowed for an account that signed up through an external provider and holds no password; a wrong value is a 401 login-failed."`
	New     string `json:"new_password" description:"The new password, required and non-empty. The password module decides the policy: a value outside it is a 400 bad-request, and a value equal to the current password is refused."`
}

// startPhoneInput is the body of POST /phone.
type startPhoneInput struct {
	_ struct{} `description:"The phone number to send a verification code to."`

	Phone string "json:\"phone\" description:\"The number in international E.164 form: a leading `+`, a non-zero country digit, then 6 to 14 more digits. The value is not parsed, completed or reformatted, so a national-format number is a 400 bad-request.\""
}

// verifyPhoneInput is the body of POST /phone/verify.
type verifyPhoneInput struct {
	_ struct{} `description:"The verification code sent to the pending phone number."`

	Code string `json:"code" required:"true" description:"The code as the SMS channel sent it. Empty or longer than 32 bytes is a 400 bad-request before any comparison; a wrong code is a 401 login-failed and counts against the number."`
}

// addIdentifierInput is the body of POST /identifiers.
type addIdentifierInput struct {
	_ struct{} `description:"The login identifier to add: a username or an email address."`

	Kind  string `json:"kind" required:"true" description:"The identifier kind to add: username or email; a phone is added through /auth/phone"`
	Value string "json:\"value\" required:\"true\" description:\"The login name or address to add. A username is NFKC-normalized and lowercased first, then accepted only when it is 3 to 64 characters of [a-z0-9._-] beginning with a letter or a digit and does not carry the reserved static-credential prefix; the stored value is the normalized one. An email address is trimmed and lowercased first, then accepted only when it is ASCII, contains exactly one `@` with no whitespace, and is at most 254 bytes; anything else is a 400 bad-request.\""
}

// verifyIdentifierInput is the body of POST /identifiers/verify.
type verifyIdentifierInput struct {
	_ struct{} `description:"The code sent to the email address being added."`

	Code string `json:"code" required:"true" description:"The six-digit code the email channel delivered, as a string. Empty or longer than 32 bytes is a 400 bad-request; a wrong code is a 401 login-failed, and five wrong codes for one issued code spend the pending verification."`
}

// identifierPathInput is the path of DELETE /identifiers/:id.
type identifierPathInput struct {
	ID string "path:\"id\" description:\"The identifier's identifier, as returned in the `id` of one of the caller's own identifiers. A value the caller's account does not hold, including one another account holds, is a 400 bad-request.\""
}

// linkPathInput is the path of DELETE /links/:id.
type linkPathInput struct {
	ID string "path:\"id\" description:\"The external binding's identifier, as returned in the `id` of one of the caller's own external identities. A value the caller's account does not hold is a 400 bad-request.\""
}

// The response declarations shared by the routes that change the account.
var (
	challengeHeader = speedhttp.Header{
		Name:        "WWW-Authenticate",
		Description: "The capability's challenge: the Bearer and Cookie schemes, never Basic.",
	}
	retryAfterHeader = speedhttp.Header{
		Name:        "Retry-After",
		Description: "Whole seconds to wait before retrying, when the back-off states a delay; absent otherwise.",
	}
)

// unauthorized declares a 401 that carries the capability's challenge.
func unauthorized(description string) speedhttp.Response {
	response := speedhttp.ProblemResponse(http.StatusUnauthorized, description)
	response.Headers = []speedhttp.Header{challengeHeader}
	return response
}

// throttled declares a 429 that may carry a retry delay.
func throttled(description string) speedhttp.Response {
	response := speedhttp.ProblemResponse(http.StatusTooManyRequests, description)
	response.Headers = []speedhttp.Header{retryAfterHeader}
	return response
}

// unavailable declares a 503 on a route that cannot refuse for capacity, so it
// never carries a retry delay.
func unavailable(description string) speedhttp.Response {
	return speedhttp.ProblemResponse(http.StatusServiceUnavailable, description)
}

// busyFailure declares the 503 of the password derivation refused for capacity,
// the one 503 here that may carry a retry delay.
func busyFailure(description string) speedhttp.Response {
	response := speedhttp.ProblemResponse(http.StatusServiceUnavailable, description)
	response.Headers = []speedhttp.Header{retryAfterHeader}
	return response
}

// impersonation is the refusal every route here writes for an impersonating
// identity.
func impersonation() speedhttp.Response {
	return speedhttp.ProblemResponse(http.StatusForbidden, "The identity is impersonating, which the route refuses")
}

// mediaType is the refusal of a body of another media type.
func mediaType() speedhttp.Response {
	return speedhttp.ProblemResponseOf(http.StatusUnsupportedMediaType,
		"The request body is not application/json", typePrefix+mount.CodeUnsupportedMediaType)
}

// badRequest is an answer the entry point writes itself, before a handler runs:
// a body it could not bind. Its problem type is the capability's code, so a body
// refused before the handler is answered in the same vocabulary as one the
// handler refuses.
func badRequest(description string) speedhttp.Response {
	return speedhttp.ProblemResponseOf(http.StatusBadRequest, description,
		typePrefix+mount.CodeBadRequest)
}

// bodyOverLimit is the 413 the endpoint's body limit answers with before a route
// runs. It is a platform-wide answer and carries no capability code.
var bodyOverLimit = speedhttp.ProblemResponse(http.StatusRequestEntityTooLarge,
	"The request body is over the endpoint's limit")

// tooMany is the throttle answer the task table states.
func tooMany() speedhttp.Response {
	return throttled("Too many attempts; retry after the stated delay")
}

// jsonAnswer declares one JSON answer whose body is structure.
func jsonAnswer(status int, description string, structure any) speedhttp.Response {
	return speedhttp.Response{
		Status:      status,
		Description: description,
		Body:        &speedhttp.Body{MediaType: "application/json", Structure: reflect.TypeOf(structure)},
	}
}

// The operations below are declared here and mounted by Service.Mount; the
// fragment is generated from them together with the engine's own operations.

var passwordOperation = speedhttp.NewReplyOperation[changePasswordInput](
	"authn.login.changePassword",
	http.MethodPut,
	pathPassword,
	"Change the caller's password",
	"Changes the caller's password. The call requires the fresh authentication every change of what proves "+
		"the account asks for — a fresh local multi-factor when the account holds an active factor, a fresh "+
		"first factor otherwise — and refuses an impersonating identity with 403 impersonation-restricted. The "+
		"current password may be empty for an account that signed up through a provider and holds no "+
		"password; a wrong one is a 401 login-failed, and a new password outside the password module's policy "+
		"is a 400 bad-request. On success the current session is rotated to a new token, the account's other "+
		"sessions are revoked, and the answer is 204 with the new session cookie set. An account that has "+
		"neither a username nor an email address is refused with 409 login-identifier-required.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The current and new passwords. `new_password` is required and `current_password` may be empty. The body must be `application/json` and refuses unknown members.",
		Required:    true,
	},
	[]speedhttp.Response{
		{Status: http.StatusNoContent, Description: "The password was changed"},
		badRequest("The new password is outside the policy"),
		bodyOverLimit,
		unauthorized("No usable credential, or the current password did not hold"),
		impersonation(),
		speedhttp.ProblemResponse(http.StatusConflict, "The request conflicts with the account's state"),
		mediaType(),
		tooMany(),
		busyFailure("A dependency could not answer, or the derivation is refused for capacity"),
	},
)

var startPhoneOperation = speedhttp.NewReplyOperation[startPhoneInput](
	"authn.login.startPhone",
	http.MethodPost,
	pathPhone,
	"Send a code to a phone number being added to the account",
	"Sends a verification code to a phone number the caller wants to add to the account, and answers how long "+
		"the code lives and how soon another may be sent, both in whole seconds. The answer carries no "+
		"challenge identifier and no hint whether the number already belongs to another account: a number "+
		"another account holds is sent a code exactly as a free one is, and the conflict surfaces only when the "+
		"verified number is written. The pending verification is held against the caller's session, so one "+
		"session has at most one and a later call replaces it. The route is present only in an assembly that "+
		"takes in the SMS code capability. A key-value store that cannot be read is a 503 before any code is "+
		"sent; a store that cannot be written after the code went out is also a 503, and the code then cannot "+
		"be verified.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The phone number to send a verification code to. The body must be `application/json` and refuses unknown members.",
		Required:    true,
	},
	[]speedhttp.Response{
		jsonAnswer(http.StatusOK, "The challenge, and when the code may be resent", AuthnLoginPhoneChallenge{}),
		badRequest("The body could not be read, or the number is outside its domain"),
		bodyOverLimit,
		unauthorized("No usable credential"),
		impersonation(),
		mediaType(),
		tooMany(),
		unavailable("A dependency could not answer"),
	},
)

var verifyPhoneOperation = speedhttp.NewReplyOperation[verifyPhoneInput](
	"authn.login.verifyPhone",
	http.MethodPost,
	pathPhoneVerify,
	"Verify the code sent to a phone number",
	"Verifies the code sent to the pending number and binds that number to the caller's account as an "+
		"identifier verified by SMS code, replacing a phone the account already has. The number and the "+
		"challenge are taken from the server-side record of the caller's session, not from the request, so a "+
		"client cannot choose which number is verified. The number's back-off is checked before the code is "+
		"compared, so a number in back-off answers 429 without offering the code to a guess; a wrong code is a "+
		"401 login-failed and counts against the number, and a correct one clears the back-off. On success the "+
		"pending record is consumed, the caller's session token is rotated, the account's other sessions are "+
		"revoked, and the answer is 204. A missing pending record, or one that names another account, is a 401 "+
		"flow-invalid.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The verification code the SMS channel delivered. The body must be `application/json` and refuses unknown members.",
		Required:    true,
	},
	[]speedhttp.Response{
		{Status: http.StatusNoContent, Description: "The number was verified and added to the account"},
		badRequest("The body could not be read, or the code is empty or longer than 32 bytes"),
		bodyOverLimit,
		unauthorized("No usable credential"),
		impersonation(),
		speedhttp.ProblemResponse(http.StatusConflict, "The number belongs to another account"),
		mediaType(),
		tooMany(),
		unavailable("A dependency could not answer"),
	},
)

var removeIdentifierOperation = speedhttp.NewReplyOperation[identifierPathInput](
	"authn.login.removeIdentifier",
	http.MethodDelete,
	pathIdentifier,
	"Remove one of the caller's own identifiers",
	"Removes one login identifier the caller's account holds. The identifier must be the caller's own: one "+
		"that does not exist and one that belongs to another account are answered identically as a 400 "+
		"bad-request, so the route discloses nothing about what another account holds. The call requires the "+
		"fresh authentication every change of what proves the account asks for and refuses an impersonating "+
		"identity. An account that holds a password credential cannot lose its last username or email address: "+
		"that removal is a 409 login-identifier-required and changes nothing. An account without a password "+
		"credential may lose its last login name, and removing a phone number is always allowed. Unlike the "+
		"routes that add a way in, this one does not revoke the caller's other sessions, and it publishes no "+
		"event of its own: `authn.identifier.changed` is published by the account module on the removal.",
	nil,
	[]speedhttp.Response{
		{Status: http.StatusNoContent, Description: "The identifier was removed"},
		speedhttp.ProblemResponse(http.StatusBadRequest, "The identifier cannot be removed"),
		unauthorized("No usable credential"),
		impersonation(),
		speedhttp.ProblemResponseOf(http.StatusConflict,
			"The removal would leave a password account with no login name", typePrefix+mount.CodeLoginIdentifierRequired),
		tooMany(),
		unavailable("A dependency could not answer"),
	},
)

var addIdentifierOperation = speedhttp.NewReplyOperation[addIdentifierInput](
	"authn.login.addIdentifier",
	http.MethodPost,
	pathIdentifiers,
	"Add one of the caller's own login identifiers",
	"Adds one login identifier to the caller's account. `kind=username` writes a login name at once, recorded "+
		"as the account's own assertion; `kind=email` sends a code to the address and answers `state: pending` "+
		"with how long the code lives and how soon another may be sent, and the address is written only after "+
		"`POST /auth/identifiers/verify` accepts the code. A second code to the same normalized address within "+
		"the resend interval is a 429 throttled; the window is held against the address and shared across "+
		"sessions and accounts. The call requires the fresh authentication every change of what proves the "+
		"account asks for and refuses an impersonating identity; a write rotates the caller's session token and "+
		"revokes the account's other sessions. A login name or address another account holds is a 409 conflict, "+
		"and a deployment with no email channel answers the email branch with 503 unavailable.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The login identifier to add: its kind and its value. The body must be `application/json` and refuses unknown members.",
		Required:    true,
	},
	[]speedhttp.Response{
		jsonAnswer(http.StatusOK, "A username was added; or a code was sent to an address and its answer is still to come",
			AuthnLoginIdentifierState{}),
		badRequest("The body could not be read, or the kind or the value is outside its domain"),
		bodyOverLimit,
		unauthorized("No usable credential, or the freshness a change of what proves the account asks for"),
		impersonation(),
		speedhttp.ProblemResponse(http.StatusConflict, "The login name or address belongs to another account"),
		mediaType(),
		tooMany(),
		unavailable("A dependency could not answer, or the deployment offers no email channel"),
	},
)

var verifyIdentifierOperation = speedhttp.NewReplyOperation[verifyIdentifierInput](
	"authn.login.verifyIdentifier",
	http.MethodPost,
	pathIdentifiersVerify,
	"Verify the code sent to an email address being added",
	"Accepts the code sent to an email address being added and, on a match, writes the address as an "+
		"identifier verified by that code. The pending address and the code's digest are read from the "+
		"server-side record of the caller's session, so the client submits only the code and cannot choose "+
		"which address is verified. A missing record, one that names another account, or one that does not "+
		"parse is a 401 flow-invalid. A wrong code is a 401 login-failed; five wrong codes for one issued code "+
		"spend the pending verification, and the record also expires with the code's lifetime. On success the "+
		"record is consumed, the caller's session token is rotated, the account's other sessions are revoked, "+
		"and the answer is 200 with `state: added`.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The code the email channel delivered to the address being added. The body must be `application/json` and refuses unknown members.",
		Required:    true,
	},
	[]speedhttp.Response{
		jsonAnswer(http.StatusOK, "The address was verified and added to the account", AuthnLoginIdentifierState{}),
		badRequest("The body could not be read, or the code is empty or longer than 32 bytes"),
		bodyOverLimit,
		unauthorized("No usable credential, or no pending verification for this session"),
		impersonation(),
		speedhttp.ProblemResponse(http.StatusConflict, "The address belongs to another account"),
		mediaType(),
		tooMany(),
		unavailable("A dependency could not answer"),
	},
)

var unlinkOperation = speedhttp.NewReplyOperation[linkPathInput](
	"authn.login.unlink",
	http.MethodDelete,
	pathLink,
	"Unbind one of the caller's own external identities",
	"Unbinds one external identity from the caller's account. The binding must be the caller's own; one that "+
		"is not, whether it does not exist or belongs to another account, is a 400 bad-request. The call "+
		"requires the fresh authentication every change of what proves the account asks for — a multi-factor "+
		"assurance merely declared by an identity provider does not count — and refuses an impersonating "+
		"identity. The account module refuses to remove the last way to sign in (409 conflict); a way only an "+
		"identity provider declared does not count as a way to sign in, which is why this route asks for local "+
		"freshness. On success the call publishes `authn.binding.changed` when the provider is in the event "+
		"payload's closed set, rotates the caller's session token, revokes the account's other sessions, and "+
		"answers 204.",
	nil,
	[]speedhttp.Response{
		{Status: http.StatusNoContent, Description: "The external identity was unbound"},
		speedhttp.ProblemResponse(http.StatusBadRequest, "The binding cannot be removed"),
		unauthorized("No usable credential"),
		impersonation(),
		speedhttp.ProblemResponse(http.StatusConflict, "The binding is the account's last way in"),
		tooMany(),
		unavailable("A dependency could not answer"),
	},
)

// operations is this package's declaration of the routes it mounts. The login
// engine adds it to its own list, so one fragment describes the whole surface.
var operations = speedhttp.Operations{
	passwordOperation,
	startPhoneOperation,
	verifyPhoneOperation,
	addIdentifierOperation,
	verifyIdentifierOperation,
	removeIdentifierOperation,
	unlinkOperation,
}

// Operations returns the declarations of the account self-service routes, for
// the login engine's fragment.
func Operations() speedhttp.Operations { return operations }
