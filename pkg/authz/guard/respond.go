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
// and each member is the value of the metric attribute of the same name, so a
// dashboard or an alert written against a value stops matching when one is
// renamed. A value can be added and none is renamed.
type outcome int

const (
	// allowed lets the request through: the route is open, or the engine said
	// the subject may do this.
	allowed outcome = iota
	// denied refuses a request the engine matched to no permission: 403.
	denied
	// unguarded refuses a route with no declaration while undeclared is
	// closed: 403.
	unguarded
	// outage refuses a request the module could not decide because the engine
	// or its store failed: 503. It is named for the failure rather than the
	// metric value because the write-out function unavailable holds that name.
	outage
	// noIdentity refuses a route that requires identity when the request
	// carries none: 401.
	noIdentity

	outcomeCount
)

// names are the values of the outcome attribute, indexed by outcome.
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

// status is the status code a refusing outcome is answered with. It is the
// closed set of this layer: 403 for a policy denial or an undeclared route, 401
// for a request with no identity where one is required, and 503 for a decision
// the module could not make. A failure is never a denial.
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
	// cause is the failure behind an unavailable outcome. It goes to the log
	// and never to the caller.
	cause error
	// permission is what a denied or an unguarded route asked for. It is read
	// to pick the write-out and never reaches the detail, the body or the log:
	// naming the permission would make the answer a probe of the policy.
	permission authz.Permission
}

// The shape of a 403 answer. typePrefix is this module's problem-type namespace
// and forbidden is its one code in it.
const (
	typePrefix    = "urn:speed:authz:"
	forbiddenCode = "forbidden"
	// forbiddenDetail is the class of a 403's detail and no more. It names the
	// kind of refusal and never the permission, the subject or the domain. It is
	// a stable, language-neutral token and not prose: user-facing text belongs
	// to the i18n resources, and an ErrorResponder that renders one reads this
	// class and writes the wording.
	forbiddenDetail = forbiddenCode
)

// forbidden writes the refusal of an outcome the design fixes at 403 — a policy
// denial or an undeclared route. The status is o.status(), so the code the
// behaviour cases observe is the code status() states and the two cannot
// diverge. The permission the route asked for is accepted so a denied route and
// an unguarded one are written the same way, and it is deliberately kept out of
// the answer: the type is this module's and the detail is the class of the
// refusal alone. The body is written by the entry module's Fail.
func forbidden(c *gin.Context, o outcome, perm authz.Permission) {
	speedhttp.Fail(c, speedhttp.Problem{
		Status: o.status(),
		Type:   typePrefix + forbiddenCode,
		Detail: forbiddenDetail,
	})
}

// unavailable writes a 503. It is the answer to a decision the module could not
// make — a broken snapshot, a failed read, a failed start-up self-check — and
// never a denial: reading an outage as a denial would make a recoverable failure
// look like a permanent permission conclusion. The status is o.status(). The
// answer does not say which dependency failed; the cause goes to the log alone
// and is not put on the problem, and the line is written here so the middleware
// path and the handler path both leave the diagnosis.
func unavailable(c *gin.Context, o outcome, err error) {
	if err != nil {
		log.FromContext(c.Request.Context()).Error(ModuleName+": the request could not be decided",
			outcomeAttribute, o.String(), "error", err)
	}
	speedhttp.Fail(c, speedhttp.Problem{Status: o.status()})
}

// noIdentity401 writes the 401 of a route that requires identity when the
// request carries none. It is the same answer the admission layer writes for the
// same condition: a 401 with no problem type, and the realm challenge naming
// both carriers. It lives here because the authorization stage is inside the
// gateway and is the last layer that can refuse an anonymous request when the
// gateway is absent; the two writers agreeing on the shape is what keeps a
// caller from having to tell them apart. Its status is o.status().
func noIdentity401(c *gin.Context, o outcome, realm string) {
	realm = quoteRealm(realm)
	c.Header("WWW-Authenticate", `Bearer realm="`+realm+`", Cookie realm="`+realm+`"`)
	speedhttp.Fail(c, speedhttp.Problem{Status: o.status()})
}

// quoteRealm escapes what goes between the quotes of a realm: a quote or a
// backslash gets a backslash, and a line ending is dropped because it would
// start another header.
//
// The same rule is written in this layer, the admission layer
// (pkg/tenant/admission) and the mount layer (pkg/authn/internal/mount) as three
// byte-identical copies, because no package boundary between them carries a
// symbol the others may share: an unexported one is out of reach and an internal
// one is out of the tree. The design promises the no-identity 401 of this layer
// and of the admission layer are the same shape, so a change to the rule here
// has to be made in all three by hand; a behaviour test can pin this layer's own
// output but cannot fail when another copy drifts.
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
