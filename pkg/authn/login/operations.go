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
	pathMethods               = "/auth/methods"
	pathRegister              = "/auth/register"
	pathFlows                 = "/auth/flows"
	pathFlowsAuth             = "/auth/flows/authenticated"
	pathFlowsAnswer           = "/auth/flows/answer"
	pathFlowsCurrent          = "/auth/flows/current"
	pathPasswordReset         = "/auth/password-reset"
	pathPasswordResetCurrent  = "/auth/password-reset/current"
	pathPasswordResetVerify   = "/auth/password-reset/verify"
	pathPasswordResetComplete = "/auth/password-reset/complete"
	pathCallback              = "/auth/callback/:provider"
	callbackGetID             = "authn.login.callbackGet"
	callbackPostID            = "authn.login.callbackPost"
)

// AuthnLoginMethods is the discovery document: one descriptor per login method
// taking part and not turned off, whether registration is open, the kinds of
// second factor the deployment offers, and the human-verification gate a send
// must satisfy.
type AuthnLoginMethods struct {
	_ struct{} `description:"The discovery document of the login surface: one descriptor per login method that takes part and is not turned off, whether registration is open, the second-factor kinds the deployment takes in, and the human-verification gate this deployment requires before a verification message is sent. Enterprise identity providers are not listed here; they are discovered through the SSO module."`

	Methods []AuthnMethodDescriptor `json:"methods" required:"true" description:"The methods taking part that are not turned off, one descriptor each, sorted by name"`
	Signup  AuthnLoginSignup        "json:\"signup\" required:\"true\" description:\"Whether registration is open on this deployment: true when either `authn.account.signup` or `authn.account.password-signup` is `open`.\""
	MFA     AuthnLoginMFA           `json:"mfa" required:"true" description:"The second-factor kinds the deployment takes in."`
	Human   AuthnLoginHuman         `json:"human" required:"true" description:"The human-verification gate a send must satisfy."`

	PasswordReset AuthnLoginPasswordReset `json:"password_reset" required:"true" description:"Whether this deployment offers the anonymous password reset, and the channels it may use."`
}

// AuthnLoginPasswordReset is the deployment-level password reset capability: it
// says nothing about any account.
type AuthnLoginPasswordReset struct {
	_ struct{} `description:"Whether this deployment offers the anonymous password reset and through which channels. It is a deployment fact, the same for every anonymous caller, and it says nothing about whether any account can be reset."`

	Enabled bool     `json:"enabled" required:"true" description:"Whether the anonymous password reset is open: the switch is open, the audit record is present, and at least one channel is available"`
	Methods []string `json:"methods" required:"true" description:"The channels the reset may be sent through: email and sms, sorted, leaving out a channel that is not assembled, whose template is missing or that a disabled method turned off"`
}

// AuthnLoginHuman is the human-verification gate the deployment runs.
type AuthnLoginHuman struct {
	_ struct{} `description:"The human-verification gate a deployment puts in front of every send of a verification message: whether a proof is required, the mechanism that checks it, and the public identifier a client-side widget needs. A deployment that takes no gate in reports the off mechanism and requires nothing."`

	Required bool   `json:"required" required:"true" description:"Whether every send of a verification message must first present a valid, single-use proof"`
	Provider string "json:\"provider\" required:\"true\" description:\"The mechanism that checks a proof: `off` when no gate is configured, `pow` for the self-hosted proof of work, or `turnstile`\""
	SiteKey  string "json:\"site_key,omitempty\" description:\"The public identifier a client-side widget is rendered with, for a mechanism that needs one; absent for `off` and `pow`\""
}

// AuthnMethodDescriptor is one login method as the discovery document reports
// it.
type AuthnMethodDescriptor struct {
	_ struct{} `description:"One login method as the discovery document reports it: its stable name, how a client renders it, the protocol a redirect or enterprise method speaks, its brand label, whether it can open an account, the provider values its bindings appear under, and the inputs its first step asks for."`

	Name             string            `json:"name" required:"true" description:"The method's stable name"`
	Kind             string            `json:"kind" required:"true" description:"How a client renders the method: form, redirect, enterprise or webauthn"`
	Protocol         string            `json:"protocol,omitempty" description:"The wire protocol a redirect or enterprise method speaks: oauth2, oidc or saml; empty otherwise"`
	Label            string            `json:"label,omitempty" description:"The method's brand name where it has one, otherwise empty for a name-keyed translation"`
	OpensAccount     bool              "json:\"opens_account,omitempty\" description:\"Whether the method may open an account for an identifier it does not yet know; false for the phone and social methods when `authn.account.signup` is closed\""
	BindingProviders []string          `json:"binding_providers,omitempty" description:"The stored provider values, or namespace prefixes, the method's bindings appear under; absent means the method's name"`
	Fields           []AuthnLoginField `json:"fields,omitempty" description:"The inputs the method's first step asks for, empty for a method that does not open with a form"`
}

// AuthnLoginSignup says whether anonymous self-registration is available: either
// a first login may open an account, or the name-and-password register route is
// open.
type AuthnLoginSignup struct {
	_ struct{} `description:"Whether anonymous self-registration is available on this deployment: either a first login may open an account, or the name-and-password register route is open. The two switches are independent; each member below reports its own."`

	Enabled  bool                     `json:"enabled" required:"true" description:"Whether anonymous self-registration is available: true when either a first login may open an account or the name-and-password register route is open"`
	Password AuthnLoginSignupPassword `json:"password" required:"true" description:"Whether the name-and-password register route is open."`
}

// AuthnLoginSignupPassword is the name-and-password register route's own switch.
type AuthnLoginSignupPassword struct {
	_ struct{} `description:"Whether the anonymous name-and-password register route is open, reported on its own because it is independent of a first login opening an account."`

	Enabled bool `json:"enabled" required:"true" description:"Whether POST /auth/register is open"`
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
	State     string         `json:"state" required:"true" description:"The flow's state: first, signup, second, enroll, ready, processing, done or failed"`
	FlowToken string         `json:"flow_token,omitempty" description:"The flow token, set only when the flow is bound by the Authn-Flow header"`
	Next      AuthnLoginStep `json:"next" required:"true" description:"What the client does at the flow's current step."`
}

// AuthnLoginStep is what the client does next.
type AuthnLoginStep struct {
	_ struct{} "description:\"What the client does at the flow's current step. Only the members that belong to `kind` are present: `challenge` carries `fields`; a pending signup of a social first login also carries `provided_email`; `redirect` carries `url` and `method`; `factor` carries `factors`; `enroll` carries `kinds`; `webauthn` carries `options`; `complete` carries nothing.\""

	Kind          string             `json:"kind" required:"true" description:"What the client does next: challenge, redirect, factor, enroll, complete or webauthn"`
	Fields        []AuthnLoginField  `json:"fields,omitempty" description:"The inputs a challenge step asks for"`
	ProvidedEmail string             `json:"provided_email,omitempty" description:"The address the provider declared verified, carried by the pending signup of a social first login for the client to show read-only; absent when the provider declared no verified address and for every other step"`
	ExpiresIn     int                `json:"expires_in,omitempty" description:"How long a challenge lives, in seconds. No handler of this fragment sets it, so the member is absent from every answer."`
	ResendAfter   int                `json:"resend_after,omitempty" description:"How soon a challenge may be asked for again, in seconds. No handler of this fragment sets it, so the member is absent from every answer."`
	URL           string             `json:"url,omitempty" description:"Where a redirect step sends the user agent"`
	Method        string             `json:"method,omitempty" description:"The HTTP method the redirect uses"`
	Factors       []AuthnLoginFactor `json:"factors,omitempty" description:"The factors a second-factor step offers"`
	Kinds         []string           `json:"kinds,omitempty" description:"The factor kinds an enroll step takes in"`
	Options       any                "json:\"options,omitempty\" description:\"The data a webauthn step carries: the public-key creation or request options of a WebAuthn ceremony, or, for a TOTP enrollment, an object with an `otpauth_uri` the client renders as a QR code.\""
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

// AuthnLoginRegistration is the answer of an anonymous self-registration: the
// account that was opened and the email address awaiting verification.
type AuthnLoginRegistration struct {
	_ struct{} "description:\"The answer of an anonymous self-registration: the state name `registered`, the account that was opened, and the email address awaiting verification. No session is made and no email is sent.\""

	State        string                        "json:\"state\" required:\"true\" enum:\"registered\" description:\"The literal `registered`, marking that an account was opened\""
	AccountID    string                        `json:"account_id" required:"true" description:"The identifier of the account that was opened"`
	Verification AuthnLoginRegistrationContact `json:"verification" required:"true" description:"The contact awaiting verification."`
}

// AuthnLoginRegistrationContact is the contact a new account has to prove.
type AuthnLoginRegistrationContact struct {
	_ struct{} "description:\"The contact a new registration has to prove: the pending state of the email address the caller gave, which is not yet a login name.\""

	State string "json:\"state\" required:\"true\" enum:\"pending\" description:\"The verification state of the email address: `pending` until the address is proved after signing in\""
	Email string `json:"email" required:"true" description:"The email address the caller gave, awaiting verification"`
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
	passwordSignupView   = AuthnLoginSignupPassword
	mfaView              = AuthnLoginMFA
	humanView            = AuthnLoginHuman
	registrationReply    = AuthnLoginRegistration
	verificationView     = AuthnLoginRegistrationContact
	passwordResetView    = AuthnLoginPasswordReset
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
	// HumanProof is the proof the deployment's human-verification gate checks
	// before a verification message is sent. It is flat rather than a member of
	// input because an input value is capped at 512 bytes and a Turnstile token
	// can exceed that. It is read only when this request would send a message and
	// the deployment requires a proof; otherwise it is ignored.
	HumanProof string `json:"human_proof" description:"The human-verification proof the deployment may require before it sends a verification message: a Turnstile token or a proof-of-work token. It is ignored when the deployment requires none. A value longer than 8 KiB is a 400 bad-request before any provider is asked; a missing or invalid proof is a 403 human-verification-required and no message is sent."`
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
	// HumanProof is the proof the deployment's human-verification gate checks
	// before a verification message is sent; see startInput.HumanProof.
	HumanProof string `json:"human_proof" description:"The human-verification proof the deployment may require before it sends a verification message: a Turnstile token or a proof-of-work token. It is ignored when the deployment requires none. A value longer than 8 KiB is a 400 bad-request before any provider is asked; a missing or invalid proof is a 403 human-verification-required and no message is sent."`
}

// registerInput is the body of POST register.
type registerInput struct {
	_ struct{} "description:\"The anonymous registration request: a username, an email address and a password are required; a display name, a language and a time zone are optional. The email address is only a contact awaiting verification, not a login name.\""

	Username    string "json:\"username\" description:\"The login name, required. A name already taken is a 409 username-taken; a reserved name is a 400 username-reserved; a name that cannot be normalized is a 400 bad-request.\""
	Email       string `json:"email" description:"The email address, required. It is only a contact awaiting verification, not a login name yet. The registration reserves it in the same transaction, so an address another account has reserved or proved is a 409 email-taken."`
	Password    string `json:"password" description:"The password, required, held to the deployment's password policy; a password that violates it is a 400 bad-request."`
	DisplayName string `json:"display_name" description:"The display name, optional: valid text of at most 100 characters."`
	Locale      string `json:"locale" description:"The account's language preference, optional, written to the i18n.locale attribute; a value outside the accepted language domain is a 400 bad-request."`
	TimeZone    string `json:"time_zone" description:"The account's time-zone preference, optional, written to the i18n.tz attribute; a value outside the accepted time-zone domain is a 400 bad-request."`
}

// passwordResetStartInput is the body of POST password-reset.
type passwordResetStartInput struct {
	_ struct{} `description:"The first step of an anonymous password reset: the channel to prove and the identifier to send a code to."`

	Channel    string `json:"channel" required:"true" enum:"email,sms" description:"The channel to prove the holder of: email proves an address verified by an emailed code, sms proves a phone number verified by a code sent to it."`
	Identifier string `json:"identifier" required:"true" description:"The address or E.164 phone number the code is sent to. An address is trimmed and lowercased first; a malformed value is a 400 bad-request."`
	HumanProof string `json:"human_proof" description:"The human-verification proof the deployment may require before it sends a verification message: a Turnstile token or a proof-of-work token. It is ignored when the deployment requires none. A value longer than 8 KiB is a 400 bad-request before any provider is asked; a missing or invalid proof is a 403 human-verification-required and no message is sent."`
}

// passwordResetVerifyInput is the body of POST password-reset/verify.
type passwordResetVerifyInput struct {
	_ struct{} `description:"The code sent to the identifier the bound reset is proving."`

	Code string `json:"code" required:"true" description:"The code as the channel sent it. Empty or longer than 32 bytes is a 400 bad-request; a wrong code, an expired code and a suppressed record are all a 401 login-failed."`
}

// passwordResetCompleteInput is the body of POST password-reset/complete.
type passwordResetCompleteInput struct {
	_ struct{} `description:"The new password of a reset whose identifier has been proved."`

	New string `json:"new_password" required:"true" description:"The new password, required and non-empty. The password module decides the policy: a value outside it is a 400 bad-request and the reset stays proved, so another password may be tried."`
}

// AuthnLoginPasswordResetState is the answer of the reset send, the current-flow
// read and the verification step.
type AuthnLoginPasswordResetState struct {
	_ struct{} `description:"The state of the bound anonymous password reset: whether a code was sent or the identifier is proved, how long the code or the proved ticket lives, and, while a code is outstanding, how soon another may be asked for. It carries no account, no identifier and no code."`

	State       string `json:"state" required:"true" enum:"sent,verified" description:"sent after a code was sent, verified once the code held"`
	ExpiresIn   int    `json:"expires_in" required:"true" description:"How long the code or the proved ticket lives, in whole seconds"`
	ResendAfter int    `json:"resend_after,omitempty" description:"How soon another code may be asked for, in whole seconds; absent once the identifier is proved"`
}

// passwordResetState is the internal name of that reply.
type passwordResetState = AuthnLoginPasswordResetState

// answerInput is the body of POST flows/answer.
type answerInput struct {
	_ struct{} "description:\"One answer to the bound flow: which step is being answered, the step number to echo, and the fields the step takes.\""

	Seq   int               "json:\"seq\" description:\"The `seq` of the flow as last returned. It must equal the record's current step number; a stale or future value is a 401 flow-invalid.\""
	Step  string            "json:\"step\" description:\"The step being answered: `answer` for a first-factor submission, only while the flow is at the first factor; `factor` for a second-factor step, only at the second factor; `enroll` for an enrollment step, only at the enroll state; `complete` to take the result of a ready flow. A name outside this set is a 400 bad-request, and a step that does not match the flow's state is a 401 flow-invalid.\""
	Input map[string]string "json:\"input\" description:\"The fields the step takes, as name-to-value strings. At most 8 members, each value at most 8 KiB; a second-factor step reads `factor` to choose the factor and then the values that factor asks for, an enrollment step reads `kind`, and `complete` reads nothing.\""
	// HumanProof is the proof the deployment's human-verification gate checks
	// before a verification message is sent; see startInput.HumanProof.
	HumanProof string `json:"human_proof" description:"The human-verification proof the deployment may require before it sends a verification message: a Turnstile token or a proof-of-work token. It is ignored when the deployment requires none. A value longer than 8 KiB is a 400 bad-request before any provider is asked; a missing or invalid proof is a 403 human-verification-required and no message is sent."`
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
	// sendForbiddenAnswer is the 403 of the routes that send a verification
	// message: the refusal shared with the other routes, and the
	// human-verification refusal a deployment may turn on.
	sendForbiddenAnswer = speedhttp.ProblemResponse(http.StatusForbidden,
		"The method or the account is unavailable, or a human-verification proof is missing or invalid "+
			"(human-verification-required)")
	// signedInForbiddenAnswer is the 403 of the flow route that runs on an
	// identity already signed in. It carries the signup gate of `purpose=link`
	// beside the refusal shared with the other routes, and the
	// human-verification refusal a deployment may turn on a step-up.
	signedInForbiddenAnswer = speedhttp.ProblemResponse(http.StatusForbidden,
		"The method or the account is unavailable, or a `purpose=link` flow is refused because the "+
			"caller has an email address that is not proved (contact-required), or a human-verification "+
			"proof is missing or invalid (human-verification-required)")
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
		"method name, together with whether registration is open and the second-factor kinds the "+
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
	speedhttp.Public(),
)

// registerOperation declares the anonymous self-registration route.
var registerOperation = speedhttp.NewReplyOperation[registerInput](
	"authn.login.register",
	http.MethodPost,
	pathRegister,
	"Register an account with a username, an email address and a password",
	"Opens one enabled account in one transaction from a username, an email address and a password, without a "+
		"session and without sending an email. The email address is only a contact awaiting verification: it is "+
		"not a login name until it is proved, which happens after the client signs in through `POST /auth/flows` "+
		"and adds the address through the account self-service routes. The route reads no cookie and keeps no "+
		"flow, so it has no binding and no sequence number. It passes the cross-origin check and refuses a body "+
		"that is not `application/json` with 415 and one over 64 KiB, with an unknown member or with a password "+
		"the policy refuses with 400. Registration that the deployment has closed, the switch read both here as a "+
		"pre-check and in the account module's own registration, is a 403 signup-closed; a username already taken "+
		"is a 409 username-taken; an email address another account has reserved or proved is a 409 email-taken, "+
		"because the reservation row is inserted in the same transaction and its unique constraint is the "+
		"arbiter; and the per-source and deployment-wide budgets answer 429 with `Retry-After` when they are "+
		"exhausted.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The registration request: the username, email address and password the account is opened with, and optionally a display name, a language and a time zone. The body must be `application/json`, is limited to 64 KiB, and refuses unknown members.",
		Required:    true,
	},
	[]speedhttp.Response{
		jsonAnswer(http.StatusCreated,
			"The account was opened; the email address awaits verification",
			AuthnLoginRegistration{}),
		capabilityProblem(http.StatusBadRequest, mount.CodeBadRequest,
			"A required member is missing, the body carries an unknown member or a value outside its domain, or the password does not meet the policy"),
		capabilityProblem(http.StatusBadRequest, mount.CodeUsernameReserved,
			"The username is one the system keeps for itself"),
		capabilityProblem(http.StatusBadRequest, mount.CodeUsernameInvalid,
			"The username does not normalize"),
		bodyOverLimit,
		capabilityProblem(http.StatusForbidden, mount.CodeSignupClosed,
			"Self-registration is closed on this deployment"),
		capabilityProblem(http.StatusConflict, mount.CodeUsernameTaken,
			"The username is already taken, or the email address is reserved by another account (email-taken)"),
		mediaTypeAnswer,
		throttled("The per-source or deployment-wide registration budget is exhausted; retry after the stated delay"),
		dependencyAnswer,
	},
	speedhttp.Public(),
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
		sendForbiddenAnswer,
		conflictAnswer,
		mediaTypeAnswer,
		tooManyAnswer,
		dependencyAnswer,
	},
	speedhttp.Public(),
)

// startAuthenticatedOperation declares the route that starts a link, step-up or
// enrollment flow on the identity the gateway authenticated.
var startAuthenticatedOperation = speedhttp.NewReplyOperation[startAuthenticatedInput](
	"authn.login.startAuthenticated",
	http.MethodPost,
	pathFlowsAuth,
	"Start a flow on the identity already signed in",
	"Starts a `link`, `step-up` or `enroll` flow on the identity the gateway authenticated and advances it to "+
		"its first step. The flow is bound to the effective account and the caller's session: an impersonating "+
		"identity starts it for the impersonated account and proves its own factors. A link and an enrollment "+
		"require the fresh authentication the effective account's factors call for — a fresh local multi-factor "+
		"when the account holds an active factor, a fresh first factor otherwise — and answer 401 "+
		"insufficient_user_authentication with `WWW-Authenticate` when it does not hold. A step-up whose account "+
		"holds at least one factor usable for this session starts at the second factor and answers the offered "+
		"factors without calling the method; an account that holds factors but none usable for this session — "+
		"because each was activated at or after the session authenticated — is refused with 401 "+
		"insufficient_user_authentication, and an account with no factor starts at the first factor with the "+
		"method named in the body. The delivery mode is not read from the body: it follows the kind of the "+
		"request's session.",
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
		signedInForbiddenAnswer,
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
		"call is a 401 flow-invalid. A refused answer is not progress: it keeps the step and its `seq`, counting "+
		"only against the eight-answer limit, so the same `seq` answers again once the value is corrected. "+
		"`step` must be one of `answer`, `factor`, `enroll` or `complete`: a name "+
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
		capabilityProblem(http.StatusBadRequest, mount.CodeUsernameReserved,
			"The username a pending first login would open the account with is one the system keeps for itself"),
		capabilityProblem(http.StatusBadRequest, mount.CodeUsernameInvalid,
			"The username a pending first login would open the account with does not normalize"),
		bodyOverLimit,
		unauthorized("The credential did not hold, or the flow binding is missing or wrong"),
		sendForbiddenAnswer,
		speedhttp.ProblemResponse(http.StatusConflict,
			"The request conflicts with the account's state: a completed link names an external identity another account already holds, or the username a pending first login would open the account with is already taken (username-taken). A step that does not match the flow's state is a 401 flow-invalid, not a 409."),
		mediaTypeAnswer,
		tooManyAnswer,
		dependencyAnswer,
	},
	speedhttp.Public(),
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
	speedhttp.Public(),
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
	speedhttp.Public(),
)

// callbackResponses are the answers both callback methods share.
var callbackResponses = []speedhttp.Response{
	jsonAnswer(http.StatusOK, "The flow, advanced by the callback, as answered to an API callback: a token-delivery login whose callback declares no navigation. A login that declares a navigation is answered 303 instead.", AuthnLoginFlow{}),
	{
		Status: http.StatusSeeOther,
		Description: "The user agent is sent to the flow's stored return target instead of being answered a " +
			"body: a completed cookie login writes its session cookie in this response, a token-delivery " +
			"login whose callback declares a navigation (`Sec-Fetch-Mode: navigate`) keeps the flow cookie " +
			"at ready and makes no session, a flow that still " +
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
		"route answers 303 to the flow's stored return target instead of a body. A token-delivery login whose "+
		"request declares a navigation (`Sec-Fetch-Mode: navigate`) answers 303 the same way, with no session "+
		"made and the flow left at ready; without that declaration it is answered the completion body. A callback of a purpose "+
		"other than login (a link, or a step-up of an account with no factor) that reaches ready answers 303 "+
		"the same way, with no session made: the flow stays in the cookie at ready for the host's callback page to read "+
		"with `GET /auth/flows/current` and complete with `POST /auth/flows/answer`.",
	nil,
	callbackResponses,
	speedhttp.Public(),
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
		"an assertion is handled at most once. A token-delivery login whose request declares a navigation "+
		"(`Sec-Fetch-Mode: navigate`) answers 303 to the flow's stored return target with no session made and "+
		"the flow left at ready; without that declaration it is answered the completion body. A callback of a purpose other than login (a link, or a "+
		"step-up of an account with no factor) that reaches ready answers 303 to the flow's stored return "+
		"target, with no session made and the flow left in the cookie for the host's callback page to read with `GET "+
		"/auth/flows/current` and complete with `POST /auth/flows/answer`. Without a flow cookie the route "+
		"serves an identity-provider-initiated sign-in, but only for an enterprise provider whose row "+
		"enables it.",
	nil,
	callbackResponses,
	speedhttp.Public(),
)

// passwordResetStartOperation declares the first step of an anonymous password
// reset.
var passwordResetStartOperation = speedhttp.NewReplyOperation[passwordResetStartInput](
	"authn.login.passwordResetStart",
	http.MethodPost,
	pathPasswordReset,
	"Start an anonymous password reset and send a code",
	"Starts an anonymous password reset for the identifier in the body and sends a code to it, or follows the "+
		"same path without sending one. A target that is not eligible — unknown, disabled, deleted, without a "+
		"password or whose identifier is not proved by the requested channel — gets the same 202, the same "+
		"headers and the same reset cookie as an eligible one. Every send budget overrun suppresses the message "+
		"and still answers 202; only the originating cookie's own resend interval and the login source-address "+
		"pre-throttle answer 429. The binding is an independent `__Host-authn-reset` cookie; the login flow "+
		"cookie is neither read nor written, and no session is made.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The channel and the identifier to send a reset code to, and the human-verification proof the deployment may require. The body must be `application/json`, is limited to 64 KiB, and refuses unknown members.",
		Required:    true,
	},
	[]speedhttp.Response{
		jsonAnswer(http.StatusAccepted, "A code was sent, or the request followed the suppressed path", AuthnLoginPasswordResetState{}),
		capabilityProblem(http.StatusBadRequest, mount.CodeBadRequest,
			"The body could not be read, the channel is not email or sms, the identifier is malformed, or the phone prefix is not served (unsupported-region)"),
		bodyOverLimit,
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The anonymous password reset is closed (password-reset-closed), or a human-verification proof is missing or invalid (human-verification-required)"),
		mediaTypeAnswer,
		throttled("The originating cookie's resend interval has not elapsed, or the source-address pre-throttle is exhausted; retry after the stated delay"),
		unavailableAnswer,
	},
	speedhttp.Public(),
)

// passwordResetCurrentOperation declares the read of the bound reset.
var passwordResetCurrentOperation = speedhttp.NewReplyOperation[struct{}](
	"authn.login.passwordResetCurrent",
	http.MethodGet,
	pathPasswordResetCurrent,
	"The state of the bound anonymous password reset",
	"Reads the anonymous password reset the request's reset cookie is bound to, so a page refresh can resume it. "+
		"It answers whether a code is outstanding or the identifier is proved and how long either lives, and "+
		"never the account, the identifier or the code. A missing or expired binding, and a record that is being "+
		"completed, are a 401 flow-invalid. A closed reset answers 403 password-reset-closed.",
	nil,
	[]speedhttp.Response{
		jsonAnswer(http.StatusOK, "The bound reset's state", AuthnLoginPasswordResetState{}),
		unauthorized("The reset binding is missing or wrong, or the record is gone or being completed"),
		speedhttp.ProblemResponseOf(http.StatusForbidden,
			"The anonymous password reset is closed", typePrefix+mount.CodePasswordResetClosed),
		unavailableAnswer,
	},
	speedhttp.Public(),
)

// passwordResetDeleteOperation declares the abandonment of the bound reset.
var passwordResetDeleteOperation = speedhttp.NewReplyOperation[struct{}](
	"authn.login.passwordResetDelete",
	http.MethodDelete,
	pathPasswordResetCurrent,
	"Abandon the bound anonymous password reset",
	"Removes the anonymous password reset the request's reset cookie is bound to and clears the cookie. It is "+
		"idempotent: no binding, no record and a closed reset all answer 204. It sends nothing and changes no "+
		"account. A dependency that cannot answer is a 503.",
	nil,
	[]speedhttp.Response{
		{Status: http.StatusNoContent, Description: "The reset was removed, or there was none to remove"},
		unavailableAnswer,
	},
	speedhttp.Public(),
)

// passwordResetVerifyOperation declares the code check of an anonymous password
// reset.
var passwordResetVerifyOperation = speedhttp.NewReplyOperation[passwordResetVerifyInput](
	"authn.login.passwordResetVerify",
	http.MethodPost,
	pathPasswordResetVerify,
	"Verify the code of the bound anonymous password reset",
	"Checks the code sent to the identifier the bound reset is proving. A real SMS code is checked by the SMS "+
		"capability; a real email code is compared against the salted digest in the record; a suppressed record "+
		"compares against a local decoy and never passes. Every failure — a wrong code, an expired code, a "+
		"suppressed record and this record's used-up attempts — is the same 401 login-failed, and the attempts "+
		"are counted on the record itself. On a match the record moves to verified and the reset cookie's "+
		"lifetime is refreshed to the ticket lifetime. A closed reset answers 403 password-reset-closed.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The code the channel sent. The body must be `application/json`, is limited to 64 KiB, and refuses unknown members.",
		Required:    true,
	},
	[]speedhttp.Response{
		jsonAnswer(http.StatusOK, "The identifier is proved", AuthnLoginPasswordResetState{}),
		capabilityProblem(http.StatusBadRequest, mount.CodeBadRequest,
			"The body could not be read, or the code is empty or longer than 32 bytes"),
		bodyOverLimit,
		unauthorized("The code did not hold, or the binding is missing or wrong"),
		speedhttp.ProblemResponseOf(http.StatusForbidden,
			"The anonymous password reset is closed", typePrefix+mount.CodePasswordResetClosed),
		mediaTypeAnswer,
		unavailableAnswer,
	},
	speedhttp.Public(),
)

// passwordResetCompleteOperation declares the write of a proved reset.
var passwordResetCompleteOperation = speedhttp.NewReplyOperation[passwordResetCompleteInput](
	"authn.login.passwordResetComplete",
	http.MethodPost,
	pathPasswordResetComplete,
	"Set the new password of a proved anonymous password reset",
	"Writes the new password of a reset whose identifier has been proved. It re-reads the reset switch and the "+
		"channel, re-checks that the account is still eligible, writes the mandatory `authn.password.reset` "+
		"attempted audit row, then writes the password with the account's own checks inside the write "+
		"transaction: the account must still be enabled, still hold a password credential, and the proved "+
		"identifier must still belong to it. A password outside the policy answers 400 bad-request and the "+
		"reset stays proved, so another password may be tried; an account that is no longer eligible answers 403 "+
		"password-reset-unavailable. On success every browser and token session of the account is revoked, the "+
		"reset record is removed and the cookie cleared, and the answer is 204. No session is created, and no "+
		"factor is touched.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The new password. The body must be `application/json`, is limited to 64 KiB, and refuses unknown members.",
		Required:    true,
	},
	[]speedhttp.Response{
		{Status: http.StatusNoContent, Description: "The password was written and the account's sessions were revoked"},
		capabilityProblem(http.StatusBadRequest, mount.CodeBadRequest,
			"The body could not be read, or the new password does not meet the policy"),
		bodyOverLimit,
		unauthorized("The binding is missing or wrong, or the reset is not proved"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The anonymous password reset is closed (password-reset-closed), or the account is no longer eligible (password-reset-unavailable)"),
		mediaTypeAnswer,
		unavailableAnswer,
	},
	speedhttp.Public(),
)

// loginOperations is the declaration of the engine's own routes. The account
// self-service operations are added to it below, so the fragment is generated
// from one list.
var loginOperations = speedhttp.Operations{
	methodsOperation,
	registerOperation,
	startOperation,
	startAuthenticatedOperation,
	answerOperation,
	currentOperation,
	abandonOperation,
	passwordResetStartOperation,
	passwordResetCurrentOperation,
	passwordResetDeleteOperation,
	passwordResetVerifyOperation,
	passwordResetCompleteOperation,
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
