package guard

import (
	nethttp "net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authz"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
)

// outcome is what the middleware concluded about one request. The set is closed
// and each member is the value of the metric attribute of the same name.
type outcome int

const (
	// allowed lets the request through.
	allowed outcome = iota
	// denied refuses a request the engine matched to no permission: 403.
	denied
	// unguarded refuses a route with no declaration while undeclared is
	// closed: 403.
	unguarded
	// outage refuses a request the module could not decide because the engine
	// or its store failed: 503.
	outage
	// noIdentity refuses a route that requires identity when the request
	// carries none: 401.
	noIdentity

	outcomeCount
)

var names = [outcomeCount]string{
	allowed:    "allowed",
	denied:     "denied",
	unguarded:  "unguarded",
	outage:     "unavailable",
	noIdentity: "no-identity",
}

func (o outcome) String() string { return names[o] }

// passes reports whether the request goes on to the handler.
func (o outcome) passes() bool { return o == allowed }

// status is the status code a refusing outcome is answered with: 403 for a
// policy denial or an undeclared route, 401 for a missing identity, and 503 for
// a decision the module could not make.
func (o outcome) status() int {
	switch o {
	case denied, unguarded:
		return nethttp.StatusForbidden
	case noIdentity:
		return nethttp.StatusUnauthorized
	case outage:
		return nethttp.StatusServiceUnavailable
	}
	return nethttp.StatusOK
}

// decision is the conclusion about one request, with what the write-out and the
// record need beyond the outcome.
type decision struct {
	outcome outcome
	// cause goes to the log and never to the caller.
	cause error
	// permission is kept out of the detail, the body and the log: naming it
	// would make the answer a probe of the policy.
	permission authz.Permission
}

// The shape of a 403 answer. typePrefix is the authorization capability's
// problem-type namespace; forbiddenCode is the code this layer writes in it.
const (
	typePrefix    = "urn:speed:authz:"
	forbiddenCode = "forbidden"
	// forbiddenDetail is a stable, language-neutral token naming only the kind
	// of refusal, never the permission, the subject or the domain.
	forbiddenDetail = forbiddenCode
)

// forbidden writes the 403 of a policy denial or an undeclared route. The body
// is written by the entry module's Fail.
func forbidden(c *gin.Context, o outcome, perm authz.Permission) {
	speedhttp.Fail(c, speedhttp.Problem{
		Status: o.status(),
		Type:   typePrefix + forbiddenCode,
		Detail: forbiddenDetail,
	})
}

// unavailable writes a 503 for a decision the module could not make. The cause
// goes to the log alone, never onto the problem.
func unavailable(c *gin.Context, o outcome, err error) {
	if err != nil {
		log.FromContext(c.Request.Context()).Error(ModuleName+": the request could not be decided",
			outcomeAttribute, o.String(), "error", err)
	}
	speedhttp.Fail(c, speedhttp.Problem{Status: o.status()})
}

// noIdentity401 writes the 401 of a route that requires identity when the
// request carries none. It is the same answer the admission layer writes for the
// same condition.
func noIdentity401(c *gin.Context, o outcome, realm string) {
	realm = quoteRealm(realm)
	c.Header("WWW-Authenticate", `Bearer realm="`+realm+`", Cookie realm="`+realm+`"`)
	speedhttp.Fail(c, speedhttp.Problem{Status: o.status()})
}

// quoteRealm escapes what goes between the quotes of a realm: a quote or a
// backslash gets a backslash, and a line ending is dropped.
//
// The rule is copied byte for byte into every surface that writes a no-identity
// 401; change every copy by hand.
func quoteRealm(name string) string {
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
