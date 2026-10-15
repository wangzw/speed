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
	"github.com/vislake/speed/pkg/tenant"
)

// The problem types this module writes. typePrefix is the tenant capability's
// namespace; badRequest and conflict are this surface's two codes in it. A 404
// and a 503 are written with no type: they say something is not there or a
// dependency could not answer, and not which kind of refusal it was, so naming a
// kind would separate the failures the answer is meant to keep apart.
const (
	typePrefix     = "urn:speed:tenant:"
	badRequestCode = "bad-request"
	conflictCode   = "conflict"
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
// gateway is absent; the writers agreeing on the shape is what keeps a caller
// from having to tell them apart.
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

// require asks the handler-internal guard for one permission in the platform
// domain. It is the decision every management route makes after reading the
// caller and before parsing anything: a shortfall is answered by the guard — a
// 403 for a denial, a 503 for an outage — and the handler returns.
//
// The decision is made in the platform domain and not the tenant the request
// resolved to. The roster is the process's own table of which tenants exist, so
// a roster operation is a platform one; deciding in whatever tenant the request
// happened to resolve to would let a caller authorized in one tenant reach the
// whole roster by sending a tenant header. The explicit-domain form is what
// makes the decision independent of the request's tenant.
func (p *product) require(c *gin.Context, perm authz.Permission) bool {
	return p.guard.RequirePermissionIn(c, perm, authz.PlatformDomain)
}

// body decodes a JSON request body into target. A body that is not one object is
// a 400, and the handler returns without touching the roster.
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

// auditBestEffort writes the failed row that closes a write the roster refused.
// A failure is an Error line and changes nothing about the answer.
func (p *product) auditBestEffort(ctx context.Context, e audit.Entry) {
	if err := p.audit.Record(ctx, e); err != nil {
		p.logger.Error(ModuleName+": a failed-write audit row could not be written",
			slog.String("action", e.Action), slog.String("cause", err.Error()))
	}
}

// answer maps a failure the roster returned to the response it belongs to.
//
// The mapping is closed and fail-closed. An identifier the roster's table
// already holds is a 409; an alias another tenant holds is a 409; an identifier
// no row carries is a 404; any other failure is a 503 whose cause stays in the
// log and never reaches the caller. A 404 and a 503 carry no problem type — they
// say something is not there or a dependency could not answer and not which
// refusal it was — while a 409 carries the capability's type prefix
// urn:speed:tenant:.
//
// tenant.ErrTenantDisabled and tenant.ErrNoTenantInContext are not mapped. They
// are the resolution and scoping paths' sentinels, and no roster write reached
// from a management route answers them; a roster that answers one here is
// answering the wrong question, so it falls to the same 503 an unknown failure
// gets.
func (p *product) answer(c *gin.Context, op string, err error) {
	switch {
	case errors.Is(err, tenant.ErrTenantExists), errors.Is(err, tenant.ErrAliasConflict):
		p.conflict(c)
	case errors.Is(err, tenant.ErrInvalidTenant):
		p.notFound(c)
	default:
		p.unavailable(c, op, err)
	}
}

// bad answers a request this module refuses on shape alone: a path parameter or
// a body it cannot read, or a status outside the closed set. It is written
// before any roster call.
func (p *product) bad(c *gin.Context) {
	speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusBadRequest, Type: typePrefix + badRequestCode})
}

// conflict answers a write the roster refused because an identifier or an alias
// is already taken.
func (p *product) conflict(c *gin.Context) {
	speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusConflict, Type: typePrefix + conflictCode})
}

// notFound answers a management route whose tenant the roster does not hold: a
// bare 404, carrying no problem code of this capability.
func (p *product) notFound(c *gin.Context) {
	c.Status(http.StatusNotFound)
	c.Writer.WriteHeaderNow()
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
