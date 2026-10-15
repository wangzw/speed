package admin

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/audit/journal"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// The problem types this module writes. typePrefix is the audit capability's
// namespace; badRequest is its one code here. A 404, a 503 and a 401 are written
// with no type: they say a row is not there, a dependency could not answer, or a
// request carried no identity, and not which kind of refusal it was, so naming a
// kind would separate the failures the answer is meant to keep apart.
const (
	typePrefix     = "urn:speed:audit:"
	badRequestCode = "bad-request"
)

// caller reads the identity the gateway put on the request.
//
// A query route is reached with no identity only when the authorization
// middleware let an anonymous request through — the routes are declared open —
// or when the request is on an endpoint that middleware does not guard. Both are
// a request that cannot be put to the policy, and neither is a denial, so the
// answer is the 401 the admission and authorization layers write for a route
// that requires an identity and carries none: the status alone, no type, and the
// same realm challenge. The handler returns without asking the guard or reading
// anything.
func (p *product) caller(c *gin.Context) (authn.Identity, bool) {
	id, ok := authn.FromContext(c.Request.Context())
	if !ok {
		writeNoIdentity401(c, realmOf(c))
		return authn.Identity{}, false
	}
	return id, true
}

// realmOf reads back the endpoint a query route was mounted on, the name the
// handler writes as the realm of its 401. It is empty off the mounted route —
// the handler called directly — and the challenge is then written with an empty
// realm, which is the shape the design fixes and not a value a client reads off
// the header.
func realmOf(c *gin.Context) string {
	if v, ok := c.Get(realmKey); ok {
		if name, ok := v.(string); ok {
			return name
		}
	}
	return ""
}

// writeNoIdentity401 writes the 401 of a query route reached with no identity.
// It is the same answer the admission and authorization layers write for the
// same condition: a 401 with no problem type and the realm challenge naming both
// carriers. It lives here because the query face is inside the gateway and can
// be the last layer that refuses an anonymous request when the gateway is
// absent; the writers agreeing on the shape is what keeps a caller from having
// to tell them apart.
func writeNoIdentity401(c *gin.Context, realm string) {
	realm = quoteRealm(realm)
	c.Header("WWW-Authenticate", `Bearer realm="`+realm+`", Cookie realm="`+realm+`"`)
	speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusUnauthorized})
}

// quoteRealm escapes what goes between the quotes of a realm: a quote or a
// backslash gets a backslash, and a line ending is dropped because it would
// start another header.
//
// It is the rule the authorization layer writes, kept byte-identical here
// because the design promises the no-identity 401 of this face and of that layer
// are the same shape, and no package boundary between them carries a symbol the
// two may share.
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

// require asks the handler-internal guard for audit.read. It is the decision
// every query route makes after reading the caller and before parsing anything:
// a shortfall is answered by the guard — a 403 for a denial, a 503 for an
// outage — and the handler returns.
//
// The decision is made in the platform domain. The audit table is the process's
// own and not a tenant's: it holds every tenant's rows and the rows that belong
// to no tenant, so a query is a platform operation. Deciding in whatever tenant
// the request happened to resolve to would let a caller authorized in one tenant
// read another tenant's rows by sending a tenant header. The explicit-domain
// form is what makes the decision independent of the request's tenant.
func (p *product) require(c *gin.Context) bool {
	return p.guard.RequirePermissionIn(c, authz.PermissionAuditRead, authz.PlatformDomain)
}

// bad answers a request this module refuses on shape alone: a path parameter or
// a query parameter it cannot read. It is written before any read of the table.
func (p *product) bad(c *gin.Context) {
	speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusBadRequest, Type: typePrefix + badRequestCode})
}

// notFound answers a get route whose row the table does not hold: a bare 404,
// carrying no problem code of this capability.
func (p *product) notFound(c *gin.Context) {
	c.Status(http.StatusNotFound)
	c.Writer.WriteHeaderNow()
}

// unavailable logs a read failure and answers a 503, never telling the caller
// which dependency failed or why.
func (p *product) unavailable(c *gin.Context, op string, err error) {
	p.logger.Error(ModuleName+": "+op+" failed", slog.String("cause", err.Error()))
	speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusServiceUnavailable})
}

// answer maps a failure the read surface returned to the response it belongs to.
//
// The mapping is closed and fail-closed. A cursor that is not one the read
// surface handed out is a shape failure the handler could not see before the
// read — the cursor is opaque — so it is a 400; anything else is a 503 whose
// cause stays in the log and never reaches the caller. A failure the mapping
// does not name does not fall through as a success: it is the same 503 an
// unknown failure gets.
func (p *product) answer(c *gin.Context, op string, err error) {
	if errors.Is(err, journal.ErrInvalidQuery) {
		p.bad(c)
		return
	}
	p.unavailable(c, op, err)
}
