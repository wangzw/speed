package mount

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// typePrefix starts the Problem.Type of every failure this capability's handlers
// write. The machine-readable code follows it.
const typePrefix = "urn:speed:authn:"

// The problem codes, the closed set of what a handler of this capability
// answers with in Problem.Type, after typePrefix. The set only grows; a code is
// never renamed or reused. Each code belongs to one status, which Status reads,
// so that a handler names the code and cannot pair it with another status.
const (
	CodeBadRequest                     = "bad-request"                      // 400
	CodeUnsupportedRegion              = "unsupported-region"               // 400
	CodeLoginFailed                    = "login-failed"                     // 401
	CodeFlowInvalid                    = "flow-invalid"                     // 401
	CodeLinkRequired                   = "link-required"                    // 401
	CodeInsufficientUserAuthentication = "insufficient_user_authentication" // 401
	CodeMethodDisabled                 = "method-disabled"                  // 403
	CodeAccountUnavailable             = "account-unavailable"              // 403
	CodeImpersonationRestricted        = "impersonation-restricted"         // 403
	CodeFactorNotSeasoned              = "factor-not-seasoned"              // 403
	CodeForbidden                      = "forbidden"                        // 403
	CodeSignupClosed                   = "signup-closed"                    // 403
	CodeContactRequired                = "contact-required"                 // 403
	CodeHumanVerificationRequired      = "human-verification-required"      // 403
	CodeConflict                       = "conflict"                         // 409
	CodeLoginIdentifierRequired        = "login-identifier-required"        // 409
	CodeUsernameTaken                  = "username-taken"                   // 409
	CodeEmailTaken                     = "email-taken"                      // 409
	CodeUnsupportedMediaType           = "unsupported-media-type"           // 415
	CodeThrottled                      = "throttled"                        // 429
	CodeUnavailable                    = "unavailable"                      // 503
)

var statusOf = map[string]int{
	CodeBadRequest:                     http.StatusBadRequest,
	CodeUnsupportedRegion:              http.StatusBadRequest,
	CodeLoginFailed:                    http.StatusUnauthorized,
	CodeFlowInvalid:                    http.StatusUnauthorized,
	CodeLinkRequired:                   http.StatusUnauthorized,
	CodeInsufficientUserAuthentication: http.StatusUnauthorized,
	CodeMethodDisabled:                 http.StatusForbidden,
	CodeAccountUnavailable:             http.StatusForbidden,
	CodeImpersonationRestricted:        http.StatusForbidden,
	CodeFactorNotSeasoned:              http.StatusForbidden,
	CodeForbidden:                      http.StatusForbidden,
	CodeSignupClosed:                   http.StatusForbidden,
	CodeContactRequired:                http.StatusForbidden,
	CodeHumanVerificationRequired:      http.StatusForbidden,
	CodeConflict:                       http.StatusConflict,
	CodeLoginIdentifierRequired:        http.StatusConflict,
	CodeUsernameTaken:                  http.StatusConflict,
	CodeEmailTaken:                     http.StatusConflict,
	CodeUnsupportedMediaType:           http.StatusUnsupportedMediaType,
	CodeThrottled:                      http.StatusTooManyRequests,
	CodeUnavailable:                    http.StatusServiceUnavailable,
}

// Status is the status code the problem code belongs to. The second result is
// false for a string that is not one of the codes.
func Status(code string) (int, bool) {
	s, ok := statusOf[code]
	return s, ok
}

// ValidCode reports whether code is one of the problem codes.
func ValidCode(code string) bool {
	_, ok := statusOf[code]
	return ok
}

// problem writes the failure of one code. The body is a problem details object
// whose type is the code under typePrefix; it carries no cause, which is the
// entry point's default shape, and every header set before the call stays.
func problem(c *gin.Context, status int, code string) {
	p := speedhttp.Problem{Status: status}
	if code != "" {
		p.Type = typePrefix + code
	}
	speedhttp.Fail(c, p)
}

// mustCode returns the status of code, or panics when it is not one of the codes
// or its status is not among allowed. A wrong code is a defect of the handler
// that wrote it, and the panic ends in the entry point's fallback 500 and its log
// line, where a quiet pass would write an answer the design does not have.
func mustCode(fn, code string, allowed ...int) int {
	status, ok := statusOf[code]
	if !ok {
		panic(fmt.Sprintf("authn/mount: %s: %q is not a problem code", fn, code))
	}
	for _, a := range allowed {
		if status == a {
			return status
		}
	}
	panic(fmt.Sprintf("authn/mount: %s: the code %q is answered with %d, which %s does not write", fn, code, status, fn))
}

// Fail writes the problem code in its status. It is for the codes that carry no
// challenge and no header of their own: the 400s, 409 and 415. A 401 is written
// with Unauthorized or StepUpRequired, a 403 with Forbidden, a 429 with
// Throttled and a 503 with Unavailable or Busy. A string that is not one of the
// codes, or a code of another kind, panics.
func Fail(c *gin.Context, code string) {
	problem(c, mustCode("Fail", code,
		http.StatusBadRequest, http.StatusConflict, http.StatusUnsupportedMediaType), code)
}

// challenge is the WWW-Authenticate value of a 401 on realm. It offers Bearer
// and Cookie and never Basic: a page's fetch that is answered with Basic makes
// the browser open its own password prompt, and these answers do not ask for a
// Basic credential. The realm is escaped, because a quote or a backslash in it
// would end the quoted string early.
func challenge(realm string) string {
	q := QuoteRealm(realm)
	return `Bearer realm="` + q + `", Cookie realm="` + q + `"`
}

// Unauthorized writes a 401 whose challenge offers Bearer and Cookie and never
// Basic. realm is the endpoint's name; see challenge for what is written.
//
// code is login-failed, flow-invalid or link-required, written as the problem's
// type; the empty string writes none, which is the shape of the gateway's own 401
// and of the 401 of a failed cross-origin check. Any other code panics.
func Unauthorized(c *gin.Context, realm, code string) {
	if code != "" {
		mustCode("Unauthorized", code, http.StatusUnauthorized)
		if code == CodeInsufficientUserAuthentication {
			panic("authn/mount: Unauthorized: insufficient_user_authentication is written with StepUpRequired")
		}
	}
	c.Header("WWW-Authenticate", challenge(realm))
	problem(c, http.StatusUnauthorized, code)
}

// stepUpDescription is the error_description of the step-up challenge. It names
// no requirement of the route and nothing of the account.
const stepUpDescription = "A stronger or more recent authentication is required"

// StepUpRequired writes the 401 of an identity that does not meet a requirement:
// the problem type insufficient_user_authentication and, in WWW-Authenticate,
// the scheme the identity authenticated with, which is id.Challenge(), then
// error, error_description, acr_values with the level's number and max_age with
// the requirement's MaxAge in whole seconds, rounded up. A requirement with no
// MaxAge, or a negative one, writes no max_age. The scheme of a zero identity,
// or one that is not a token, is Bearer.
//
// RFC 9470 defines the parameters for Bearer; carrying them on Cookie and Basic
// is this capability's convention.
func StepUpRequired(c *gin.Context, id authn.Identity, r authn.Requirement) {
	var b strings.Builder
	b.WriteString(challengeScheme(id.Challenge()))
	b.WriteString(` error="insufficient_user_authentication", error_description="`)
	b.WriteString(stepUpDescription)
	b.WriteString(`", acr_values="`)
	b.WriteString(strconv.Itoa(int(r.Assurance)))
	b.WriteByte('"')
	if r.MaxAge > 0 {
		b.WriteString(`, max_age="`)
		b.WriteString(strconv.FormatInt(int64((r.MaxAge+time.Second-1)/time.Second), 10))
		b.WriteByte('"')
	}
	c.Header("WWW-Authenticate", b.String())
	problem(c, http.StatusUnauthorized, CodeInsufficientUserAuthentication)
}

// Forbidden writes a 403. code is method-disabled, account-unavailable,
// impersonation-restricted, factor-not-seasoned, forbidden, signup-closed,
// contact-required or human-verification-required, the eight that this
// capability's own handlers may answer: the gateway and the admission layer
// never write a 403. Any other code panics.
func Forbidden(c *gin.Context, code string) {
	problem(c, mustCode("Forbidden", code, http.StatusForbidden), code)
}

// Throttled writes a 429 with Retry-After in whole seconds, rounded up, when
// retryAfter is positive.
func Throttled(c *gin.Context, retryAfter time.Duration) {
	if retryAfter > 0 {
		c.Header("Retry-After", strconv.FormatInt(int64((retryAfter+time.Second-1)/time.Second), 10))
	}
	problem(c, http.StatusTooManyRequests, CodeThrottled)
}

// Unavailable writes a 503 whose type is unavailable. It never says which
// dependency failed: the cause belongs in the log, and an answer that told would
// let a caller probe what is down.
func Unavailable(c *gin.Context) { problem(c, http.StatusServiceUnavailable, CodeUnavailable) }

// Busy writes the 503 of a password derivation refused for capacity, which is
// the one 503 that carries Retry-After: 1.
func Busy(c *gin.Context) {
	c.Header("Retry-After", "1")
	problem(c, http.StatusServiceUnavailable, CodeUnavailable)
}

// QuoteRealm escapes what goes between the quotes of a realm. A quote or a
// backslash gets a backslash; a CR or LF is dropped, because a line ending would
// not be part of a value but the start of another header.
func QuoteRealm(name string) string {
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		switch ch := name[i]; ch {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteByte(ch)
		case '\r', '\n':
		default:
			b.WriteByte(ch)
		}
	}
	return b.String()
}

// challengeScheme returns scheme when it is an RFC 9110 token, and Bearer
// otherwise. The gateway stamps Identity.Challenge, so an odd value is a zero
// identity or a defect, and neither may put a character into a header that
// ends the field.
func challengeScheme(scheme string) string {
	if scheme == "" {
		return "Bearer"
	}
	for i := 0; i < len(scheme); i++ {
		ch := scheme[i]
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9',
			strings.IndexByte("!#$%&'*+-.^_`|~", ch) >= 0:
		default:
			return "Bearer"
		}
	}
	return scheme
}
