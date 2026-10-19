package login

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/login/internal/selfservice"
	"github.com/vislake/speed/pkg/authn/login/internal/stepup"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// The fragment's info section: the title this module's fragment carries and the
// version of the fragment's shape.
const (
	fragmentTitle   = "authn login"
	fragmentVersion = "1.0.0"
)

// The routes as the fragment writes them: relative to the module's API root, so
// a route is declared once and both the fragment and the mounting read the same
// string.
const (
	pathMethods      = "/auth/methods"
	pathFlows        = "/auth/flows"
	pathFlowsAuth    = "/auth/flows/authenticated"
	pathFlowsAnswer  = "/auth/flows/answer"
	pathFlowsCurrent = "/auth/flows/current"
	pathCallback     = "/auth/callback/:provider"
	callbackGetID    = "authn.login.callbackGet"
	callbackPostID   = "authn.login.callbackPost"
)

// AuthnLoginMethods is the discovery document: one descriptor per login method
// taking part and not turned off, whether registration is open, and the kinds of
// second factor the deployment offers.
type AuthnLoginMethods struct {
	_ struct{} `description:"The discovery document of the login surface: one descriptor per login method that takes part and is not turned off, whether a first login may open an account, and the second-factor kinds the deployment takes in. Enterprise identity providers are not listed here; they are discovered through the SSO module."`

	Methods []AuthnMethodDescriptor `json:"methods" required:"true" description:"The methods taking part that are not turned off, one descriptor each, sorted by name"`
	Signup  AuthnLoginSignup        `json:"signup" required:"true" description:"Whether a first login may open an account on this deployment."`
	MFA     AuthnLoginMFA           `json:"mfa" required:"true" description:"The second-factor kinds the deployment takes in."`
}

// AuthnMethodDescriptor is one login method as the discovery document reports
// it.
type AuthnMethodDescriptor struct {
	_ struct{} `description:"One login method as the discovery document reports it: its stable name, how a client renders it, the protocol a redirect or enterprise method speaks, its brand label, whether it can open an account, and the inputs its first step asks for."`

	Name         string            `json:"name" required:"true" description:"The method's stable name"`
	Kind         string            `json:"kind" required:"true" description:"How a client renders the method: form, redirect, enterprise or webauthn"`
	Protocol     string            `json:"protocol,omitempty" description:"The wire protocol a redirect or enterprise method speaks: oauth2, oidc or saml; empty otherwise"`
	Label        string            `json:"label,omitempty" description:"The method's brand name where it has one, otherwise empty for a name-keyed translation"`
	OpensAccount bool              `json:"opens_account,omitempty" description:"Whether the method may open an account for an identifier it does not yet know"`
	Fields       []AuthnLoginField `json:"fields,omitempty" description:"The inputs the method's first step asks for, empty for a method that does not open with a form"`
}

// AuthnLoginSignup says whether a first login may open an account.
type AuthnLoginSignup struct {
	_ struct{} `description:"Whether a first login may open an account on this deployment, derived from the account module's signup switch. A closed switch does not remove a method; it only makes a first login for an unknown identifier fail."`

	Enabled bool `json:"enabled" required:"true" description:"Whether a first login may open an account"`
}

// AuthnLoginMFA is the second-factor kinds the deployment takes in.
type AuthnLoginMFA struct {
	_ struct{} `description:"The second-factor kinds the deployment takes in, as the multi-factor module reports them. This is a deployment-level list, not the caller's own registered factors."`

	Factors []string `json:"factors" required:"true" description:"The kinds of second factor the deployment takes in"`
}

// AuthnLoginFlow is one open login flow: the step number the next answer must
// echo, the flow's state, the flow token when the flow is bound by the header,
// and what the client does next.
type AuthnLoginFlow struct {
	_ struct{} `description:"One open login flow, as returned by a start, by an answer that keeps the flow open, or by the current-flow read: the step number the next answer must echo, the flow's state, the flow token when the flow is bound by the header, and what the client does next."`

	Seq       int            `json:"seq" required:"true" description:"The step number the next answer must echo"`
	State     string         `json:"state" required:"true" description:"The flow's state: first, second, enroll, ready, processing, done or failed"`
	FlowToken string         `json:"flow_token,omitempty" description:"The flow token, set only when the flow is bound by the Authn-Flow header"`
	Next      AuthnLoginStep `json:"next" required:"true" description:"What the client does at the flow's current step."`
}

// AuthnLoginStep is what the client does next.
type AuthnLoginStep struct {
	_ struct{} "description:\"What the client does at the flow's current step. Only the members that belong to `kind` are present: `challenge` carries `fields`; `redirect` carries `url` and `method`; `factor` carries `factors`; `enroll` carries `kinds`; `webauthn` carries `options`; `complete` carries nothing.\""

	Kind        string             `json:"kind" required:"true" description:"What the client does next: challenge, redirect, factor, enroll, complete or webauthn"`
	Fields      []AuthnLoginField  `json:"fields,omitempty" description:"The inputs a challenge step asks for"`
	ExpiresIn   int                `json:"expires_in,omitempty" description:"How long a challenge lives, in seconds. No handler of this fragment sets it, so the member is absent from every answer."`
	ResendAfter int                `json:"resend_after,omitempty" description:"How soon a challenge may be asked for again, in seconds. No handler of this fragment sets it, so the member is absent from every answer."`
	URL         string             `json:"url,omitempty" description:"Where a redirect step sends the user agent"`
	Method      string             `json:"method,omitempty" description:"The HTTP method the redirect uses"`
	Factors     []AuthnLoginFactor `json:"factors,omitempty" description:"The factors a second-factor step offers"`
	Kinds       []string           `json:"kinds,omitempty" description:"The factor kinds an enroll step takes in"`
	Options     any                "json:\"options,omitempty\" description:\"The data a webauthn step carries: the public-key creation or request options of a WebAuthn ceremony, or, for a TOTP enrollment, an object with an `otpauth_uri` the client renders as a QR code.\""
}

// AuthnLoginField is one input a method asks the client for.
type AuthnLoginField struct {
	_ struct{} `description:"One input a method asks the client for, named by the method and rendered according to its kind."`

	Name     string "json:\"name\" required:\"true\" description:\"The input's key: the client submits its value under this name in the `input` map of the start request or of the answer.\""
	Kind     string "json:\"kind\" required:\"true\" description:\"How the client renders the input: `text`, `password`, `code` or `phone`.\""
	Required bool   `json:"required,omitempty" description:"Whether the step needs a value; absent means the field may be left out."`
	Secret   bool   `json:"secret,omitempty" description:"Whether the value must not be displayed or echoed, as for a password or a code; absent means it is not secret."`
}

// AuthnLoginFactor is one second factor the flow offers to verify.
type AuthnLoginFactor struct {
	_ struct{} `description:"One second factor the flow offers to verify: its identifier, its kind and a display label. It carries nothing secret."`

	ID    string "json:\"id\" required:\"true\" description:\"The factor's identifier; send it as `input.factor` when answering the second-factor step.\""
	Kind  string "json:\"kind\" required:\"true\" description:\"The factor kind, such as `totp`, `sms` or `webauthn`; the client renders the challenge according to it.\""
	Label string `json:"label,omitempty" description:"A display label for the factor; it may be empty, in which case the client falls back to the kind's own name."`
}

// AuthnLoginSession is the answer of a login completed as a cookie session.
type AuthnLoginSession struct {
	_ struct{} "description:\"The answer of a login completed with `delivery=session`, and of a step-up completed on a cookie session: the state name `authenticated` and the public facts of the session that was created or elevated. The session cookie is set in the same response.\""

	State   string                "json:\"state\" required:\"true\" enum:\"authenticated\" description:\"The literal `authenticated`, marking a completed login.\""
	Session AuthnLoginSessionView `json:"session" required:"true" description:"The public facts of the session the login created."`
}

// AuthnLoginSessionView is the public facts of a session a login created.
type AuthnLoginSessionView struct {
	_ struct{} `description:"The public facts of the session a login created. No token or secret is included."`

	ID        string `json:"id" required:"true" description:"The session identifier; the browser client holds the session cookie written in the same response."`
	ExpiresAt string `json:"expires_at" required:"true" description:"The session's expiry as an RFC 3339 instant, or empty"`
}

// AuthnLoginToken is the answer of a login delivered as a token pair, and of a
// token-delivered step-up.
type AuthnLoginToken struct {
	_ struct{} "description:\"The answer of a login completed with `delivery=token`, and of a token-delivered step-up: the state name, the token type, the access token, its lifetime and the refresh token.\""

	State        string `json:"state" required:"true" enum:"authenticated,elevated" description:"authenticated for a login; elevated for a step-up, which issues no refresh token and carries it empty"`
	TokenType    string "json:\"token_type\" required:\"true\" enum:\"Bearer\" description:\"The authorization scheme the access token is sent with; it is always `Bearer`.\""
	AccessToken  string "json:\"access_token\" required:\"true\" description:\"The access token, sent in the `Authorization` header as `Bearer <value>`.\""
	ExpiresIn    int    `json:"expires_in" required:"true" description:"The access token's lifetime in seconds"`
	RefreshToken string `json:"refresh_token" required:"true" description:"Empty on a step-up, which issues no refresh token"`
}

// AuthnLoginActionResult is the answer of a flow that finished by binding an
// external identity or registering a factor.
type AuthnLoginActionResult struct {
	_ struct{} "description:\"The answer of a flow that finished by binding an external identity (`linked`) or registering a factor (`enrolled`); neither issues a session or a token.\""

	State string `json:"state" required:"true" enum:"linked,enrolled" description:"linked when the flow bound an external identity; enrolled when it registered a factor"`
}

// AuthnLoginAnswer is the body of the answer route, whose shape is the closed
// union of the four answers a step can produce. It carries a value and writes it
// as it is; the union describes the set the entry point checks the answer
// against, and the value is one member of it.
type AuthnLoginAnswer struct {
	_ struct{} "description:\"One of four shapes, told apart by `state` and the members present: an open flow, a completed login delivered as a session, a completed login delivered as a token pair or an elevated step-up, or the result of a link or enrollment.\""

	value any
}

// MarshalJSON writes the answer's own shape, which is one of the union's
// members.
func (a AuthnLoginAnswer) MarshalJSON() ([]byte, error) { return json.Marshal(a.value) }

// JSONSchemaOneOf is the union's members: the open flow, the cookie-session
// answer, the token answer and the link or enrollment result.
func (AuthnLoginAnswer) JSONSchemaOneOf() []interface{} {
	return []interface{}{AuthnLoginFlow{}, AuthnLoginSession{}, AuthnLoginToken{}, AuthnLoginActionResult{}}
}

// The internal names the engine already uses for the wire shapes.
type (
	flowReply            = AuthnLoginFlow
	stepView             = AuthnLoginStep
	fieldView            = AuthnLoginField
	factorView           = AuthnLoginFactor
	sessionReply         = AuthnLoginSession
	sessionView          = AuthnLoginSessionView
	tokenReply           = AuthnLoginToken
	actionReply          = AuthnLoginActionResult
	methodsReply         = AuthnLoginMethods
	methodDescriptorView = AuthnMethodDescriptor
	signupView           = AuthnLoginSignup
	mfaView              = AuthnLoginMFA
)

// startInput is the body of POST flows.
type startInput struct {
	_ struct{} "description:\"The start request; `method` must name a login method this deployment takes in, or the call is a bad request.\""

	Method   string            "json:\"method\" description:\"The stable name of a login method, as returned in `name` by `GET /auth/methods`. A name the deployment does not take in is a 400 bad-request; a method turned off by the deployment is a 403 method-disabled.\""
	Purpose  string            "json:\"purpose\" description:\"Accepted but not read by this route: a flow started here always has the purpose login. Use `POST /auth/flows/authenticated` for link, step-up or enroll.\""
	Delivery string            "json:\"delivery\" description:\"How a completed login is delivered: `session` (the default) sets the session cookie and answers the session view; `token` answers an access and refresh token pair. `token` must be enabled by the deployment and requires the token capability, otherwise the request is a 400 bad-request.\""
	ReturnTo string            "json:\"return_to\" description:\"Where the callback lands the user agent: either a path starting with a single `/`, not `//`, without backslash, control characters or `@` and at most 2048 bytes, or an absolute URL whose origin the deployment trusts; anything else is a 400 bad-request. The callback lands the user agent there for every purpose that reaches ready: a login, a link and a no-factor step-up. An absent or empty value uses the configured `authn.login.default-return-to`, which is held to the same rule and falls back to `/`. The value is stored in the flow record, and no parameter of the callback request can change it.\""
	Binding  string            "json:\"binding\" description:\"How the flow token is carried: `cookie` (the default) writes the flow cookie (`__Host-authn-flow`, or `authn-flow` when insecure use is allowed) and returns no token in the body; `header` returns the token in `flow_token` and sets no cookie, so the client must send it in the `Authn-Flow` request header on every later call. A callback navigation cannot set a header, so a flow bound this way cannot finish a redirect method.\""
	Input    map[string]string `json:"input" description:"The fields the method's first step takes, as name-to-value strings. At most 8 members, each value at most 512 bytes; anything beyond is a 400 bad-request."`
}

// startAuthenticatedInput is the body of POST flows/authenticated.
type startAuthenticatedInput struct {
	_ struct{} "description:\"The start request for a purpose other than login; `purpose` must be `link`, `step-up` or `enroll`.\""

	Method   string            "json:\"method\" description:\"The method to run for `link`, and for a `step-up` of an account that holds no factor. For an enrollment it is not read, and for a step-up the policy answers with usable factors it is not read either; a step-up whose account holds factors but none usable for this session is refused with 401 insufficient_user_authentication before the method is consulted. A name the deployment does not take in is a 400 bad-request.\""
	Purpose  string            "json:\"purpose\" description:\"Required, and one of `link`, `step-up` or `enroll`: bind an external identity, strengthen the current session, or register a factor. Any other value, including an empty one, is a 400 bad-request.\""
	Delivery string            "json:\"delivery\" description:\"Accepted but not read: the delivery mode follows the kind of the request's session. It is accepted so that a client may send this route the same start body it sends to `POST /auth/flows`.\""
	ReturnTo string            "json:\"return_to\" description:\"Where the callback lands the user agent: either a path starting with a single `/`, not `//`, without backslash, control characters or `@` and at most 2048 bytes, or an absolute URL whose origin the deployment trusts; anything else is a 400 bad-request. The callback lands the user agent there for every purpose that reaches ready: a login, a link and a no-factor step-up. An absent or empty value uses the configured `authn.login.default-return-to`, which is held to the same rule and falls back to `/`. The value is stored in the flow record, and no parameter of the callback request can change it.\""
	Binding  string            "json:\"binding\" description:\"How the flow token is carried: `cookie` (the default) writes the flow cookie (`__Host-authn-flow`, or `authn-flow` when insecure use is allowed) and returns no token in the body; `header` returns the token in `flow_token` and sets no cookie, so the client must send it in the `Authn-Flow` request header on every later call. A callback navigation cannot set a header, so a flow bound this way cannot finish a redirect method.\""
	Input    map[string]string "json:\"input\" description:\"The fields the started step takes, as name-to-value strings: the method's first step for `link`, the enrollment step for `enroll` where `kind` names the factor kind, and nothing for a step-up that starts at the second factor. At most 8 members, each value at most 512 bytes.\""
}

// answerInput is the body of POST flows/answer.
type answerInput struct {
	_ struct{} "description:\"One answer to the bound flow: which step is being answered, the step number to echo, and the fields the step takes.\""

	Seq   int               "json:\"seq\" description:\"The `seq` of the flow as last returned. It must equal the record's current step number; a stale or future value is a 401 flow-invalid.\""
	Step  string            "json:\"step\" description:\"The step being answered: `answer` for a first-factor submission, only while the flow is at the first factor; `factor` for a second-factor step, only at the second factor; `enroll` for an enrollment step, only at the enroll state; `complete` to take the result of a ready flow. A name outside this set is a 400 bad-request, and a step that does not match the flow's state is a 401 flow-invalid.\""
	Input map[string]string "json:\"input\" description:\"The fields the step takes, as name-to-value strings. At most 8 members, each value at most 8 KiB; a second-factor step reads `factor` to choose the factor and then the values that factor asks for, an enrollment step reads `kind`, and `complete` reads nothing.\""
}

// callbackInput is the path of a provider callback: the provider segment the
// flow recorded.
type callbackInput struct {
	Provider string "path:\"provider\" description:\"The provider this callback is for: a social method's stable name, or, for an enterprise SSO provider, the identifier the start named in `input.provider`. It must equal the provider recorded in the flow the callback is bound to; a mismatch is a 401 flow-invalid and the callback never reaches a method.\""
}

// challengeHeader declares the header every 401 on this module's routes carries:
// the gateway writes the endpoint's challenge when it refuses the request first,
// and a handler that answers 401 itself returns it in the fault's headers.
var challengeHeader = speedhttp.Header{
	Name:        "WWW-Authenticate",
	Description: "The capability's challenge: the Bearer and Cookie schemes, never Basic.",
}

// retryAfterHeader declares the header a 429 carries when the back-off states a
// delay, and a 503 carries when the password derivation is refused for capacity.
var retryAfterHeader = speedhttp.Header{
	Name:        "Retry-After",
	Description: "Whole seconds to wait before retrying, when the back-off states a delay; absent otherwise.",
}

// locationHeader declares the header a redirect answer carries.
var locationHeader = speedhttp.Header{
	Name:        "Location",
	Description: "The flow's stored return target, or the configured `authn.login.default-return-to` when the flow recorded none.",
}

// unauthorized is the declaration of a 401 that carries the capability's
// challenge.
func unauthorized(description string) speedhttp.Response {
	response := speedhttp.ProblemResponse(http.StatusUnauthorized, description)
	response.Headers = []speedhttp.Header{challengeHeader}
	return response
}

// throttled is the declaration of a 429 that may carry a retry delay.
func throttled(description string) speedhttp.Response {
	response := speedhttp.ProblemResponse(http.StatusTooManyRequests, description)
	response.Headers = []speedhttp.Header{retryAfterHeader}
	return response
}

// gatewayFailure is the declaration of a 503 that may carry a retry delay: the
// password derivation refused for capacity answers one.
func gatewayFailure(description string) speedhttp.Response {
	response := speedhttp.ProblemResponse(http.StatusServiceUnavailable, description)
	response.Headers = []speedhttp.Header{retryAfterHeader}
	return response
}

// unavailableAnswer is the declaration of a 503 on a route that cannot refuse
// for capacity, so it never carries a retry delay.
var unavailableAnswer = speedhttp.ProblemResponse(http.StatusServiceUnavailable,
	"A dependency could not answer")

// capabilityProblem is an answer the entry point writes itself, before a handler
// runs: a body it could not bind. Its problem type is the capability's code for
// the status, so a body refused before the handler is answered in the same
// vocabulary as one the handler refuses.
func capabilityProblem(status int, code, description string) speedhttp.Response {
	return speedhttp.ProblemResponseOf(status, description, typePrefix+code)
}

// bodyOverLimit is the 413 the endpoint's body limit answers with before a
// route runs. It is a platform-wide answer and carries no capability code.
var bodyOverLimit = speedhttp.ProblemResponse(http.StatusRequestEntityTooLarge,
	"The request body is over the endpoint's limit")

// jsonAnswer declares one JSON answer whose body is structure.
func jsonAnswer(status int, description string, structure any) speedhttp.Response {
	return speedhttp.Response{
		Status:      status,
		Description: description,
		Body:        &speedhttp.Body{MediaType: "application/json", Structure: reflect.TypeOf(structure)},
	}
}

// The common failures every route that runs a login method shares.
var (
	badRequestAnswer = capabilityProblem(http.StatusBadRequest, mount.CodeBadRequest,
		"The body could not be read, or a value is outside its domain")
	forbiddenAnswer = speedhttp.ProblemResponse(http.StatusForbidden,
		"The method or the account is unavailable")
	conflictAnswer = speedhttp.ProblemResponse(http.StatusConflict,
		"The request conflicts with the account's state")
	mediaTypeAnswer = capabilityProblem(http.StatusUnsupportedMediaType, mount.CodeUnsupportedMediaType,
		"The request body is not application/json")
	tooManyAnswer    = throttled("Too many attempts; retry after the stated delay")
	dependencyAnswer = gatewayFailure("A dependency could not answer")
)

// methodsOperation declares the discovery route.
var methodsOperation = speedhttp.NewReplyOperation[struct{}](
	"authn.login.methods",
	http.MethodGet,
	pathMethods,
	"List the login methods this deployment offers",
	"Lists one descriptor per login method that takes part and is not turned off by the deployment, sorted by "+
		"method name, together with whether a first login may open an account and the second-factor kinds the "+
		"deployment takes in. It reads no credential and no request body, so every caller gets the same document. "+
		"A method turned off by the deployment is left out rather than reported with a flag, and the call answers "+
		"503 when the settings that decide which methods are off cannot be read. Enterprise identity providers are "+
		"not listed here; they are discovered through the SSO module.",
	nil,
	[]speedhttp.Response{
		jsonAnswer(http.StatusOK,
			"A descriptor per method taking part, whether registration is open, and the factor kinds the deployment offers",
			AuthnLoginMethods{}),
		unavailableAnswer,
	},
)

// startOperation declares the route that starts a login flow.
var startOperation = speedhttp.NewReplyOperation[startInput](
	"authn.login.start",
	http.MethodPost,
	pathFlows,
	"Start a login flow for one method",
	"Starts a login flow for the method named in the body and advances it through the method's first step. The "+
		"flow token is written to the flow cookie, or, with `binding` set to `header`, returned in `flow_token` "+
		"with no cookie; later calls are bound to the flow by that token. The route reads no cookie, so a stale "+
		"session cookie does not prevent a login. Depending on the method and the multi-factor policy, the "+
		"returned flow may already stand at a second-factor, enrollment or ready step. A body that is not "+
		"`application/json` is a 415, an unknown member or a value outside its domain is a 400, and a credential "+
		"the method did not accept is a 401 login-failed — except two refusals the route answers as a 400: a "+
		"phone prefix the deployment does not allow (`unsupported-region`) and an identifier that cannot name an "+
		"account (`bad-request`). A 429 carries `Retry-After` when the back-off states a delay.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The start request: the method to run, how the flow token is carried, where to return after a completed login, and the fields the method's first step takes. The body must be `application/json`, is limited to 64 KiB, and refuses unknown members.",
		Required:    true,
	},
	[]speedhttp.Response{
		jsonAnswer(http.StatusOK, "The flow, advanced to its first step", AuthnLoginFlow{}),
		capabilityProblem(http.StatusBadRequest, mount.CodeBadRequest,
			"The body could not be read, a value is outside its domain, or the method refused the identifier as a region the deployment does not serve (`unsupported-region`) or as one that cannot name an account (`bad-request`)"),
		bodyOverLimit,
		unauthorized("The credential did not hold"),
		forbiddenAnswer,
		conflictAnswer,
		mediaTypeAnswer,
		tooManyAnswer,
		dependencyAnswer,
	},
)

// startAuthenticatedOperation declares the route that starts a link, step-up or
// enrollment flow on the identity the gateway authenticated.
var startAuthenticatedOperation = speedhttp.NewReplyOperation[startAuthenticatedInput](
	"authn.login.startAuthenticated",
	http.MethodPost,
	pathFlowsAuth,
	"Start a flow on the identity already signed in",
	"Starts a `link`, `step-up` or `enroll` flow on the identity the gateway authenticated and advances it to "+
		"its first step. The flow is bound to that account and session, and an impersonating identity is refused "+
		"with 403 impersonation-restricted. A link and an enrollment require the fresh authentication the "+
		"account's factors call for — a fresh local multi-factor when the account holds an active factor, a "+
		"fresh first factor otherwise — and answer 401 insufficient_user_authentication with `WWW-Authenticate` "+
		"when it does not hold. A step-up whose account holds at least one factor usable for this session starts "+
		"at the second factor and answers the offered factors without calling the method; an account that holds "+
		"factors but none usable for this session — because each was activated at or after the session "+
		"authenticated — is refused with 401 insufficient_user_authentication, and an account with no factor "+
		"starts at the first factor with the method named in the body. The delivery mode is not read from the "+
		"body: it follows the kind of the request's session.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The request to start a link, step-up or enrollment flow: the purpose, the method and fields where the purpose takes them, how the flow token is carried, and the return target. The body must be `application/json`, is limited to 64 KiB, and refuses unknown members.",
		Required:    true,
	},
	[]speedhttp.Response{
		jsonAnswer(http.StatusOK, "The flow, advanced to its first step", AuthnLoginFlow{}),
		badRequestAnswer,
		bodyOverLimit,
		unauthorized("No usable credential"),
		forbiddenAnswer,
		conflictAnswer,
		mediaTypeAnswer,
		tooManyAnswer,
		dependencyAnswer,
	},
)

// answerOperation declares the route that answers the current step of the flow
// the request is bound to.
var answerOperation = speedhttp.NewReplyOperation[answerInput](
	"authn.login.answer",
	http.MethodPost,
	pathFlowsAnswer,
	"Answer the current step of the flow the request is bound to",
	"Advances the flow the request is bound to by one step. The body must echo the flow's current `seq`, or the "+
		"call is a 401 flow-invalid. `step` must be one of `answer`, `factor`, `enroll` or `complete`: a name "+
		"outside that set is a 400 bad-request, and a step the flow's state does not accept is a 401 "+
		"flow-invalid. A flow accepts at most eight client answers, after which it is moved to failed and every "+
		"later answer is a 401 flow-invalid. The answer is the flow at its next step or, once the flow is ready, "+
		"the completed result: the session view, the token pair, or the `linked`/`enrolled` result of a link or "+
		"enrollment. Each step is a compare-and-swap on the flow record, so concurrent answers are decided by "+
		"the store and the loser gets 401 flow-invalid rather than a retry.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "One answer to the bound flow: the step number to echo, the step being answered, and the fields that step takes. The body must be `application/json`, is limited to 64 KiB, and refuses unknown members.",
		Required:    true,
	},
	[]speedhttp.Response{
		jsonAnswer(http.StatusOK, "The flow advanced, or completed with a session or a token pair", AuthnLoginAnswer{}),
		badRequestAnswer,
		bodyOverLimit,
		unauthorized("The credential did not hold, or the flow binding is missing or wrong"),
		forbiddenAnswer,
		speedhttp.ProblemResponse(http.StatusConflict,
			"The request conflicts with the account's state: a completed link names an external identity another account already holds. A step that does not match the flow's state is a 401 flow-invalid, not a 409."),
		mediaTypeAnswer,
		tooManyAnswer,
		dependencyAnswer,
	},
)

// currentOperation declares the read of the flow the request is bound to.
var currentOperation = speedhttp.NewReplyOperation[struct{}](
	"authn.login.current",
	http.MethodGet,
	pathFlowsCurrent,
	"The state of the flow the request is bound to",
	"Reads the flow the request is bound to so a client can resume it, for example after a page refresh or after "+
		"a callback sent the user agent back. The flow token is taken from the flow cookie or the `Authn-Flow` "+
		"header, and a flow bound by the header gets it echoed in `flow_token`. A missing or mismatched binding, "+
		"and a flow that is done, failed or gone, are all answered 401 flow-invalid. The reply is built from the "+
		"stored record alone, so a first-factor step the method named cannot be re-prompted with its fields and "+
		"its `next.kind` is `challenge`.",
	nil,
	[]speedhttp.Response{
		jsonAnswer(http.StatusOK, "The flow's current step, so a page refresh can resume", AuthnLoginFlow{}),
		unauthorized("The flow binding is missing or wrong, or the flow is done or gone"),
		tooManyAnswer,
		unavailableAnswer,
	},
)

// abandonOperation declares the removal of the flow the request is bound to.
var abandonOperation = speedhttp.NewReplyOperation[struct{}](
	"authn.login.abandon",
	http.MethodDelete,
	pathFlowsCurrent,
	"Abandon the flow the request is bound to",
	"Removes the flow the request is bound to and clears the flow cookie. The flow token is read from the "+
		"cookie or the `Authn-Flow` header. A request with no binding, with a wrong binding, or naming a flow "+
		"that is already gone is answered the same 401 flow-invalid, while a request that removes a live flow "+
		"answers 204; no session, token or account state is touched.",
	nil,
	[]speedhttp.Response{
		{Status: http.StatusNoContent, Description: "The flow was removed"},
		unauthorized("The flow binding is missing or wrong, or the flow is gone"),
		tooManyAnswer,
		unavailableAnswer,
	},
)

// callbackResponses are the answers both callback methods share.
var callbackResponses = []speedhttp.Response{
	jsonAnswer(http.StatusOK, "The flow, advanced by the callback", AuthnLoginFlow{}),
	{
		Status: http.StatusSeeOther,
		Description: "The user agent is sent to the flow's stored return target instead of being answered a " +
			"body: a completed cookie login writes its session cookie in this response, a flow that still " +
			"needs a second factor or an enrollment keeps the flow cookie and resumes with `GET " +
			"/auth/flows/current`, and a callback of a purpose other than login that reached ready keeps the " +
			"flow cookie the same way and completes through `POST /auth/flows/answer`. When the flow recorded " +
			"no return target, the configured `authn.login.default-return-to` is used.",
		Headers: []speedhttp.Header{locationHeader},
	},
	speedhttp.ProblemResponse(http.StatusBadRequest, "The callback is malformed"),
	unauthorized("The flow binding is missing or wrong"),
	forbiddenAnswer,
	speedhttp.ProblemResponse(http.StatusConflict,
		"The request conflicts with the account's state: a completed link names an external identity another account already holds"),
	tooManyAnswer,
	unavailableAnswer,
}

// callbackGetOperation declares a callback delivered as a browser navigation.
var callbackGetOperation = speedhttp.NewReplyOperation[callbackInput](
	callbackGetID,
	http.MethodGet,
	pathCallback,
	"Handle a provider's callback as a browser navigation",
	"Handles a provider's callback as a top-level browser navigation. The callback is bound through the flow "+
		"cookie only, because a navigation cannot set a request header; the path's provider must equal the "+
		"provider recorded in the flow and the flow must be at its first factor, otherwise the call is a 401 "+
		"flow-invalid before any method is asked. The engine moves the flow to processing before calling the "+
		"method, so a replayed authorization code or assertion loses the compare-and-swap and gets 401 "+
		"flow-invalid. A GET callback that carries no flow cookie is always 401 flow-invalid: an "+
		"identity-provider-initiated sign-in is served only as a POST. A cookie login that completes on this "+
		"route answers 303 to the flow's stored return target instead of a body. A callback of a purpose "+
		"other than login (a link, or a step-up of an account with no factor) that reaches ready answers 303 "+
		"the same way, with no session made: the flow stays in the cookie at ready for the host's callback page to read "+
		"with `GET /auth/flows/current` and complete with `POST /auth/flows/answer`.",
	nil,
	callbackResponses,
)

// callbackPostOperation declares a callback delivered as a cross-site form post.
var callbackPostOperation = speedhttp.NewReplyOperation[callbackInput](
	callbackPostID,
	http.MethodPost,
	pathCallback,
	"Handle a provider's callback as a cross-site form post",
	"Handles a provider's callback delivered as a cross-site form post, which is how SAML's assertion consumer "+
		"service is reached. The callback is bound through the flow cookie only, because a form post cannot set "+
		"a request header; the path's provider must equal the provider recorded in the flow and the flow must be "+
		"at its first factor, otherwise the call is a 401 flow-invalid before any method is asked. The posted "+
		"form is bounded to 512 KiB before it is parsed, and a body over that bound is answered 401 login-failed "+
		"with the flow left a tombstone. The engine moves the flow to processing before calling the method, so "+
		"an assertion is handled at most once. A callback of a purpose other than login (a link, or a "+
		"step-up of an account with no factor) that reaches ready answers 303 to the flow's stored return "+
		"target, with no session made and the flow left in the cookie for the host's callback page to read with `GET "+
		"/auth/flows/current` and complete with `POST /auth/flows/answer`. Without a flow cookie the route "+
		"serves an identity-provider-initiated sign-in, but only for an enterprise provider whose row "+
		"enables it.",
	nil,
	callbackResponses,
)

// loginOperations is the declaration of the engine's own routes. The account
// self-service operations are added to it below, so the fragment is generated
// from one list.
var loginOperations = speedhttp.Operations{
	methodsOperation,
	startOperation,
	startAuthenticatedOperation,
	answerOperation,
	currentOperation,
	abandonOperation,
	callbackGetOperation,
	callbackPostOperation,
}

// operations is this module's declaration of the API it serves. The fragment is
// generated from it; the routes are mounted from the same declarations.
var operations = append(loginOperations, selfservice.Operations()...)

// mountRel turns a declared route into the relative path the mounter joins to
// its base. The declaration writes the route against the module's API root, and
// the part of the mounter's default base the capability owns is the default base
// less that root. Removing it leaves the relative path the mounter takes,
// whatever base the deployment configured.
func mountRel(declared string) string {
	owned := strings.TrimPrefix(mount.DefaultBasePath, loginSpec.APIRoot())
	rel, found := strings.CutPrefix(declared, owned)
	if !found {
		panic(diagnosticPrefix + ModuleName + ": the declared route " + declared +
			" does not begin with the capability's mounted path " + owned)
	}
	return rel
}

// stepUpChallenge is the WWW-Authenticate value of a step-up refusal.
func stepUpChallenge(id authn.Identity, r authn.Requirement) string {
	return stepup.Challenge(id, r)
}

// stepUpFault is the 401 of an identity that does not meet a requirement: the
// insufficient_user_authentication type and the challenge carrying the identity's
// scheme and the requirement's level and window.
func stepUpFault(id authn.Identity, r authn.Requirement) speedhttp.Fault {
	return speedhttp.Fault{
		Status: http.StatusUnauthorized,
		Type:   "urn:speed:authn:" + mount.CodeInsufficientUserAuthentication,
		Headers: []speedhttp.HeaderValue{{
			Name:  "WWW-Authenticate",
			Value: stepUpChallenge(id, r),
		}},
	}
}
