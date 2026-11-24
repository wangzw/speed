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

const typePrefix = "urn:speed:authn:"

// The problem codes: the closed set a handler writes in Problem.Type after
// typePrefix. It only grows and a code is never renamed or reused, and each code
// has one status except invitation-target-invalid, which Status reports as 400.
const (
	CodeBadRequest                     = "bad-request"                      // 400
	CodeUsernameReserved               = "username-reserved"                // 400
	CodeUsernameInvalid                = "username-invalid"                 // 400
	CodeUnsupportedRegion              = "unsupported-region"               // 400
	CodeInvitationTargetInvalid        = "invitation-target-invalid"        // 400
	CodeInvitationExpiryTooLong        = "invitation-expiry-too-long"       // 400
	CodeInvitationRoleUnknown          = "invitation-role-unknown"          // 400
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
	CodePhoneRequired                  = "phone-required"                   // 403
	CodeHumanVerificationRequired      = "human-verification-required"      // 403
	CodePasswordResetClosed            = "password-reset-closed"            // 403
	CodePasswordResetUnavailable       = "password-reset-unavailable"       // 403
	CodeInvitationEmailUnproved        = "invitation-email-unproved"        // 403
	CodeInvitationRoleProtected        = "invitation-role-protected"        // 403
	CodeFactorNotFound                 = "factor-not-found"                 // 404
	CodeConflict                       = "conflict"                         // 409
	CodeAccountLastTenantAdmin         = "account-last-tenant-admin"        // 409
	CodeAccountOrganizationOwner       = "account-organization-owner"       // 409
	CodeLoginIdentifierRequired        = "login-identifier-required"        // 409
	CodeLastLoginMethod                = "last-login-method"                // 409
	CodeUsernameImmutable              = "username-immutable"               // 409
	CodeUsernameTaken                  = "username-taken"                   // 409
	CodeEmailTaken                     = "email-taken"                      // 409
	CodeMembershipInactive             = "membership-inactive"              // 409
	CodeInvitationTerminal             = "invitation-terminal"              // 409
	CodeInvitationMember               = "invitation-member"                // 409
	CodeInvitationPending              = "invitation-pending"               // 409
	CodeInvitationTenantInactive       = "invitation-tenant-inactive"       // 409
	CodeUnsupportedMediaType           = "unsupported-media-type"           // 415
	CodeThrottled                      = "throttled"                        // 429
	CodeUnavailable                    = "unavailable"                      // 503
)

var statusOf = map[string]int{
	CodeBadRequest:                     http.StatusBadRequest,
	CodeUsernameReserved:               http.StatusBadRequest,
	CodeUsernameInvalid:                http.StatusBadRequest,
	CodeUnsupportedRegion:              http.StatusBadRequest,
	CodeInvitationTargetInvalid:        http.StatusBadRequest,
	CodeInvitationExpiryTooLong:        http.StatusBadRequest,
	CodeInvitationRoleUnknown:          http.StatusBadRequest,
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
	CodePhoneRequired:                  http.StatusForbidden,
	CodeHumanVerificationRequired:      http.StatusForbidden,
	CodePasswordResetClosed:            http.StatusForbidden,
	CodePasswordResetUnavailable:       http.StatusForbidden,
	CodeInvitationEmailUnproved:        http.StatusForbidden,
	CodeInvitationRoleProtected:        http.StatusForbidden,
	CodeFactorNotFound:                 http.StatusNotFound,
	CodeConflict:                       http.StatusConflict,
	CodeAccountLastTenantAdmin:         http.StatusConflict,
	CodeAccountOrganizationOwner:       http.StatusConflict,
	CodeLoginIdentifierRequired:        http.StatusConflict,
	CodeLastLoginMethod:                http.StatusConflict,
	CodeUsernameImmutable:              http.StatusConflict,
	CodeUsernameTaken:                  http.StatusConflict,
	CodeEmailTaken:                     http.StatusConflict,
	CodeMembershipInactive:             http.StatusConflict,
	CodeInvitationTerminal:             http.StatusConflict,
	CodeInvitationMember:               http.StatusConflict,
	CodeInvitationPending:              http.StatusConflict,
	CodeInvitationTenantInactive:       http.StatusConflict,
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
// or its status is not among allowed: a wrong code is a handler defect and the
// panic ends in the entry point's fallback 500 and log line.
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

// Fail writes a code that carries no challenge or header of its own: the 400s,
// 404, 409 and 415. A 401 goes through Unauthorized or StepUpRequired, a 403
// through Forbidden, a 429 through Throttled, a 503 through Unavailable; anything else panics.
func Fail(c *gin.Context, code string) {
	problem(c, mustCode("Fail", code,
		http.StatusBadRequest, http.StatusNotFound, http.StatusConflict, http.StatusUnsupportedMediaType), code)
}

// challenge is the WWW-Authenticate value of a 401 on realm, offering Bearer and
// Cookie and never Basic, which would make a page's fetch open the browser's
// password prompt; the realm is escaped so a quote cannot end the string early.
func challenge(realm string) string {
	q := QuoteRealm(realm)
	return `Bearer realm="` + q + `", Cookie realm="` + q + `"`
}

// Unauthorized writes a 401 whose challenge offers Bearer and Cookie and never
// Basic. code is login-failed, flow-invalid or link-required, or empty for none;
// insufficient_user_authentication and any other code panic.
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

// StepUpRequired writes the 401 that asks an authenticated caller to prove
// itself again, not a refused credential: type insufficient_user_authentication
// with a challenge naming id.Challenge(), acr_values, and max_age when positive.
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

// Forbidden writes a 403. code is one of the 403 codes this capability's own
// handlers answer; the gateway and the admission layer never write a 403, and
// any other code panics.
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
// otherwise, so a defect cannot put a character into the header that ends the field.
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
