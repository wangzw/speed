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
	_ struct{} `description:"The phone number to send a verification code to, and the human-verification proof the deployment may require."`

	Phone string "json:\"phone\" description:\"The number in international E.164 form: a leading `+`, a non-zero country digit, then 6 to 14 more digits. The value is not parsed, completed or reformatted, so a national-format number is a 400 bad-request.\""
	// HumanProof is the proof the deployment's human-verification gate checks
	// before the code is sent; see the engine's startInput.
	HumanProof string `json:"human_proof" description:"The human-verification proof the deployment may require before it sends a verification code: a Turnstile token or a proof-of-work token. It is ignored when the deployment requires none. A value longer than 8 KiB is a 400 bad-request before any provider is asked; a missing or invalid proof is a 403 human-verification-required and no message is sent."`
}

// verifyPhoneInput is the body of POST /phone/verify.
type verifyPhoneInput struct {
	_ struct{} `description:"The verification code sent to the pending phone number."`

	Code string `json:"code" required:"true" description:"The code as the SMS channel sent it. Empty or longer than 32 bytes is a 400 bad-request before any comparison; a wrong code is a 401 login-failed and counts against the number."`
}

// addIdentifierInput is the body of POST /identifiers.
type addIdentifierInput struct {
	_ struct{} `description:"The login identifier to add — a username or an email address — and the human-verification proof the email branch may require."`

	Kind  string `json:"kind" required:"true" description:"The identifier kind to add: username or email; a phone is added through /auth/phone"`
	Value string "json:\"value\" required:\"true\" description:\"The login name or address to add. A username is NFKC-normalized and lowercased first, then accepted only when it is 3 to 64 characters of [a-z0-9._-] beginning with a letter or a digit and does not carry the reserved static-credential prefix; the stored value is the normalized one. An email address is trimmed and lowercased first, then accepted only when it is ASCII, contains exactly one `@` with no whitespace, and is at most 254 bytes; anything else is a 400 bad-request.\""
	// HumanProof is the proof the deployment's human-verification gate checks
	// before an email code is sent. The username branch sends nothing and ignores
	// it.
	HumanProof string `json:"human_proof" description:"The human-verification proof the deployment may require before it sends a verification code to an email address: a Turnstile token or a proof-of-work token. It is ignored when the deployment requires none or the kind is username. A value longer than 8 KiB is a 400 bad-request before any provider is asked; a missing or invalid proof is a 403 human-verification-required and no message is sent."`
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

// updateIdentifierInput is the path and body of PUT /identifiers/:id: the two
// axes and the default contact, each left as it is when absent.
type updateIdentifierInput struct {
	_ struct{} `description:"A partial change to one identifier's two axes and default contact: a member left out is left as it is."`

	ID             string "path:\"id\" description:\"The identifier's identifier, as returned in the `id` of one of the caller's own identifiers. A value the caller's account does not hold, including one another account holds, or a username, is a 400 bad-request.\""
	Contact        *bool  `json:"contact" description:"Whether the identifier is a contact of the account. True is accepted and changes nothing, since a registered address is always a contact, and false is a 400 bad-request; absent leaves it as it is."`
	SignIn         *bool  `json:"sign_in" description:"Whether the identifier may be used to sign in. True requires the identifier's ownership to be proved; absent leaves it as it is."`
	DefaultContact *bool  `json:"default_contact" description:"Whether the identifier is the account's default contact. Only a proved email address may take true, and a true clears the account's previous choice; false changes nothing."`
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

// humanRefused is the 403 of a route that sends a verification message: the
// human-verification refusal a deployment may turn on.
func humanRefused() speedhttp.Response {
	return speedhttp.ProblemResponse(http.StatusForbidden,
		"A human-verification proof is missing or invalid (human-verification-required)")
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
		"first factor otherwise. The "+
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
		badRequest("The body could not be read, the number is outside its domain, or the proof is over its limit"),
		bodyOverLimit,
		unauthorized("No usable credential"),
		humanRefused(),
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
		"pending record is consumed and the answer is 204. Proving a number asks for no fresh authentication "+
		"and never rotates the session, but replacing the number an active SMS factor sends its codes to is a "+
		"change of that factor's target and answers the step-up 401 `insufficient_user_authentication` until "+
		"the session is fresh. A missing pending record, or one that names another account, is a 401 "+
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
		speedhttp.ProblemResponse(http.StatusConflict, "The number belongs to another account"),
		mediaType(),
		tooMany(),
		unavailable("A dependency could not answer"),
	},
)

var updateIdentifierOperation = speedhttp.NewReplyOperation[updateIdentifierInput](
	"authn.login.updateIdentifier",
	http.MethodPut,
	pathIdentifier,
	"Set the two axes and the default contact of one of the caller's own identifiers",
	"Sets what one of the caller's own email addresses or phone numbers is for. A registered address is always a "+
		"contact, so `contact` true is accepted and changes nothing while `contact` false is a 400 bad-request. "+
		"`sign_in` is the axis the holder chooses and may be set true only on a proved identifier, since only a "+
		"proved value may be a way in. `default_contact` may be "+
		"set only on a proved email address, and takes the default away from the address that held it in the same "+
		"write. A member left out is left as it is. An identifier that is not the caller's own, a username, "+
		"an unproved `sign_in`, a `contact` of false and a default contact that is not a proved email address "+
		"are all a 400 bad-request. "+
		"Changing `sign_in` or `default_contact` requires the fresh authentication every change of what proves "+
		"the account asks for; changing `contact` alone does not. Turning `sign_in` off that would leave a "+
		"password account with no login name is a 409 login-identifier-required and one that would leave the "+
		"account with no way to sign in at all is a 409 last-login-method; neither takes effect. Putting a "+
		"proved email address or phone number into sign-in rotates the current session's token and revokes the "+
		"account's other sessions.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The axes and the default contact to write; each member left out is left as it is. The body must be `application/json` and refuses unknown members.",
		Required:    true,
	},
	[]speedhttp.Response{
		{Status: http.StatusNoContent, Description: "The identifier's axes were written"},
		badRequest("The body could not be read, the identifier is not the caller's own, or the change is outside what the axes allow"),
		bodyOverLimit,
		unauthorized("No usable credential, or the freshness a change of what proves the account asks for"),
		speedhttp.ProblemResponseOf(http.StatusConflict,
			"The change would leave a password account with no login name (login-identifier-required), or leave the account with no way to sign in at all (last-login-method)", typePrefix+mount.CodeLoginIdentifierRequired),
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
		"fresh authentication every change of what proves the account asks for. A username cannot be removed: "+
		"that removal is a 409 username-immutable and changes nothing, and this refusal comes before the others. "+
		"An account that holds no username cannot lose its last chosen email address: that removal is a 409 "+
		"login-identifier-required and changes nothing. Removing a phone number is always allowed; a removal that "+
		"would leave the account with no password, no proved phone number and no external identity is a 409 "+
		"last-login-method and changes nothing. Unlike the "+
		"routes that add a way in, this one does not revoke the caller's other sessions, and it publishes no "+
		"event of its own: `authn.identifier.changed` is published by the account module on the removal.",
	nil,
	[]speedhttp.Response{
		{Status: http.StatusNoContent, Description: "The identifier was removed"},
		speedhttp.ProblemResponse(http.StatusBadRequest, "The identifier cannot be removed"),
		unauthorized("No usable credential"),
		speedhttp.ProblemResponseOf(http.StatusConflict,
			"A username stays with its account for the account's whole life and cannot be removed", typePrefix+mount.CodeUsernameImmutable),
		speedhttp.ProblemResponseOf(http.StatusConflict,
			"The removal would leave an account without a username with no login name (login-identifier-required), or leave the account with no way to sign in at all (last-login-method)", typePrefix+mount.CodeLoginIdentifierRequired),
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
		"as the account's own assertion; it is a new way into the account, so it requires the fresh authentication "+
		"every change of what proves the account asks for, and the write rotates the caller's session token and "+
		"revokes the account's other sessions. `kind=email` registers a contact address and asks for none of "+
		"that: it sends a code to the address and answers `state: pending` with how long the code lives and how "+
		"soon another may be sent, and the address is written only after `POST /auth/identifiers/verify` accepts "+
		"the code, without rotating the session or revoking the others. A second code to the same normalized "+
		"address within the resend interval is a 429 throttled; the window is held against the address and shared "+
		"across sessions and accounts. A login name or address another account holds is a 409 conflict, "+
		"and a deployment with no email channel answers the email branch with 503 unavailable.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The login identifier to add: its kind and its value. The body must be `application/json` and refuses unknown members.",
		Required:    true,
	},
	[]speedhttp.Response{
		jsonAnswer(http.StatusOK, "A username was added; or a code was sent to an address and its answer is still to come",
			AuthnLoginIdentifierState{}),
		badRequest("The body could not be read, the kind or the value is outside its domain, or the proof is over its limit"),
		speedhttp.ProblemResponseOf(http.StatusBadRequest,
			"A username is one the system keeps for itself", typePrefix+mount.CodeUsernameReserved),
		speedhttp.ProblemResponseOf(http.StatusBadRequest,
			"A username does not normalize", typePrefix+mount.CodeUsernameInvalid),
		bodyOverLimit,
		unauthorized("No usable credential, or the freshness a username add asks for"),
		humanRefused(),
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
		"record is consumed and the answer is 200 with `state: added`. Proving the address asks for no fresh "+
		"authentication and never rotates the session, but a proof that would change what recovers or proves "+
		"the account does: an address that becomes the account's default contact (it has none) or its only "+
		"address, and so its automatic login way, answers the step-up 401 `insufficient_user_authentication` "+
		"until the session is fresh.",
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
		"assurance merely declared by an identity provider does not count. The account module refuses to remove "+
		"the last way to sign in (409 last-login-method); a way only an "+
		"identity provider declared does not count as a way to sign in, which is why this route asks for local "+
		"freshness. On success the call publishes `authn.binding.changed` when the provider is in the event "+
		"payload's closed set, rotates the caller's session token, revokes the account's other sessions, and "+
		"answers 204.",
	nil,
	[]speedhttp.Response{
		{Status: http.StatusNoContent, Description: "The external identity was unbound"},
		speedhttp.ProblemResponse(http.StatusBadRequest, "The binding cannot be removed"),
		unauthorized("No usable credential"),
		speedhttp.ProblemResponse(http.StatusConflict, "The binding is the account's last way in (last-login-method)"),
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
	updateIdentifierOperation,
	removeIdentifierOperation,
	unlinkOperation,
}

// Operations returns the declarations of the account self-service routes, for
// the login engine's fragment.
func Operations() speedhttp.Operations { return operations }
