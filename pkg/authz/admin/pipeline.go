package admin

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// The problem types this module writes. typePrefix is the authorization
// capability's namespace; badRequest and conflict are this surface's two codes
// in it. A 503 is written with no type: it says a dependency could not answer
// and not what was refused, so naming a kind would separate the failures the
// answer is meant to keep apart.
const (
	typePrefix   = "urn:speed:authz:"
	badRequest   = "bad-request"
	conflictCode = "conflict"
)

// caller reads the identity the gateway put on the request.
//
// A management route is reached with no identity only when the authorization
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

// realmOf reads back the endpoint a management route was mounted on, the name
// the handler writes as the realm of its 401. It is empty off the mounted route
// — the handler called directly — and the challenge is then written with an
// empty realm, which is the shape the design fixes and not a value a client
// reads off the header.
func realmOf(c *gin.Context) string {
	if v, ok := c.Get(realmKey); ok {
		if name, ok := v.(string); ok {
			return name
		}
	}
	return ""
}

// writeNoIdentity401 writes the 401 of a management route reached with no
// identity. It is the same answer the admission and authorization layers write
// for the same condition: a 401 with no problem type and the realm challenge
// naming both carriers. It lives here because the management face is inside the
// gateway and can be the last layer that refuses an anonymous request when the
// gateway is absent; the three writers agreeing on the shape is what keeps a
// caller from having to tell them apart.
func writeNoIdentity401(c *gin.Context, realm string) {
	realm = quoteRealm(realm)
	c.Header("WWW-Authenticate", `Bearer realm="`+realm+`", Cookie realm="`+realm+`"`)
	speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusUnauthorized})
}

// quoteRealm escapes what goes between the quotes of a realm: a quote or a
// backslash gets a backslash, and a line ending is dropped because it would
// start another header.
//
// The same rule is written in the authorization layer, the admission layer and
// the mount layer as byte-identical copies, because no package boundary between
// them carries a symbol the others may share: an unexported one is out of reach
// and an internal one is out of the tree. The design promises the no-identity
// 401 of this face and of those layers are the same shape, so a change to the
// rule has to be made in all of them by hand.
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

// require asks the handler-internal guard for one permission in the platform
// domain. It is the decision every management route makes after reading the
// caller and before parsing anything: a shortfall is answered by the guard — a
// 403 for a denial, a 503 for an outage — and the handler returns.
//
// The decision is made in the platform domain and not the tenant the request
// resolved to. The tenant in a management path is the data the route acts on, so
// deciding in the request's own tenant would let a caller authorized in one
// tenant reach another's roles, grants and bindings by naming that tenant in the
// path. The platform domain is where platform operations hold their roles; a
// caller without a grant there is refused whatever tenant the path names.
func (p *product) require(c *gin.Context, perm authz.Permission) bool {
	return p.guard.RequirePermissionIn(c, perm, authz.PlatformDomain)
}

// body decodes a JSON request body into target. A body that is not one object is
// a 400, and the handler returns without touching the manager.
func (p *product) body(c *gin.Context, target any) bool {
	if err := c.ShouldBindJSON(target); err != nil {
		p.bad(c)
		return false
	}
	return true
}

// auditRequired writes the mandatory audit row of a write, outside any
// transaction and before the action. A row that does not land is the audit
// module's state, and the action is not carried out: the answer is a 503 and the
// handler returns. The cause goes to the log and never to the caller.
func (p *product) auditRequired(c *gin.Context, e audit.Entry) bool {
	if err := p.audit.Record(c.Request.Context(), e); err != nil {
		p.logger.Error(ModuleName+": the mandatory audit row could not be written, so the action was not carried out",
			slog.String("action", e.Action), slog.String("cause", err.Error()))
		speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusServiceUnavailable})
		return false
	}
	return true
}

// auditBestEffort writes the failed row that closes a write the manager refused.
// A failure is an Error line and changes nothing about the answer.
func (p *product) auditBestEffort(ctx context.Context, e audit.Entry) {
	if err := p.audit.Record(ctx, e); err != nil {
		p.logger.Error(ModuleName+": a failed-write audit row could not be written",
			slog.String("action", e.Action), slog.String("cause", err.Error()))
	}
}

// answer maps a failure the manager returned to the response it belongs to.
//
// The mapping is closed and fail-closed: an unknown role and a tenant mismatch
// are 400s because the request named something that is not there or crossed a
// tenant, a builtin role is a 409 because the target exists but cannot be
// changed, and any other failure of the manager or its store is a 503 whose
// cause stays in the log and never reaches the caller. ErrUnknownPermission is
// not mapped here: it is the startup sentinel for a route declaration naming a
// permission the catalog does not hold, and no manager write path returns it —
// a grant naming an undeclared permission is refused by the catalog check before
// the manager is reached.
func (p *product) answer(c *gin.Context, op string, err error) {
	switch {
	case errors.Is(err, authz.ErrUnknownRole), errors.Is(err, authz.ErrTenantMismatch):
		p.bad(c)
	case errors.Is(err, authz.ErrBuiltinRole):
		p.conflict(c)
	default:
		p.unavailable(c, op, err)
	}
}

// bad answers a request this module refuses on shape alone: a path parameter or
// a body it cannot read. It is written before any manager call.
func (p *product) bad(c *gin.Context) {
	speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusBadRequest, Type: typePrefix + badRequest})
}

// conflict answers a write the manager refused because of the state of the
// target, such as an attempt to change or delete a builtin role.
func (p *product) conflict(c *gin.Context) {
	speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusConflict, Type: typePrefix + conflictCode})
}

// unavailable logs a failure and answers a 503, never telling the caller which
// dependency failed.
func (p *product) unavailable(c *gin.Context, op string, err error) {
	p.logger.Error(ModuleName+": "+op+" failed", slog.String("cause", err.Error()))
	speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusServiceUnavailable})
}

// noContent answers a write that landed: 204 with no body.
func (p *product) noContent(c *gin.Context) {
	c.Status(http.StatusNoContent)
	c.Writer.WriteHeaderNow()
}
