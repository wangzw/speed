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
	"github.com/vislake/speed/pkg/sms"
)

// The problem types this module writes. typePrefix is the SMS capability's
// namespace; badRequest, conflict and configTemplate are this surface's three
// codes in it. A 404 and a 503 are written with no type: they say something is
// not there or a dependency could not answer, and not which kind of refusal it
// was, so naming a kind would separate the failures the answer is meant to keep
// apart.
//
// configTemplate is the one code that names its reason. A write that a template's
// source refuses and a write that a name collision refuses are both conflicts,
// but the operator's fix differs: one is an edit to the configuration and a
// restart, the other is a different name. The type is what tells them apart
// without putting a sentence of prose in the body.
const (
	typePrefix         = "urn:speed:sms:"
	badRequestCode     = "bad-request"
	conflictCode       = "conflict"
	configTemplateCode = "config-template"
)

// caller reads the identity the gateway put on the request.
//
// A management route is reached with no identity only when the authorization
// middleware let an anonymous request through — the routes are declared open —
// or when the request is on an endpoint that middleware does not guard. Both are
// a request that cannot be put to the policy, and neither is a denial, so the
// answer is the 401 the admission and authorization layers write for a route
// that requires an identity and carries none: the status alone, no type, and the
// same realm challenge. The route returns without asking the guard or reading
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
// the 401 writes as the realm of its challenge. It is empty off the mounted route
// — the route called directly — and the challenge is then written with an empty
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

// require asks the handler-internal guard for one permission. It is the decision
// every management route makes after reading the caller and before binding
// anything: a shortfall is answered by the guard — a 403 for a denial, a 503 for
// an outage — and the route returns.
//
// The decision is made in the platform domain. The template set is the process's
// own and not a tenant's, so a template operation is a platform one; deciding in
// whatever tenant the request happened to resolve to would let a caller
// authorized in one tenant reach the platform set by sending a tenant header.
// The explicit-domain form is what makes the decision independent of the
// request's tenant.
func (p *product) require(c *gin.Context, perm authz.Permission) bool {
	return p.guard.RequirePermissionIn(c, perm, authz.PlatformDomain)
}

// permitted wraps one operation's handler with the two decisions every
// management route makes before the entry point binds its input: the caller is
// read first, and a request with no identity is answered 401; then the guard is
// asked for the route's permission, and a shortfall is answered by the guard.
//
// The decision lives in this wrapper rather than in the typed handler because
// the guard's API takes a *gin.Context, which a typed handler does not receive.
// Running it here keeps the order the surface has always had — identity,
// permission, then input — while the handler itself stays inside the declared
// input and answer types.
func (p *product) permitted(perm authz.Permission, handler gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := p.caller(c); !ok {
			return
		}
		if !p.require(c, perm) {
			return
		}
		handler(c)
	}
}

// auditRequired writes the mandatory audit row of a write, outside any
// transaction and before the action. A row that does not land is the audit
// module's state, and the action is not carried out: the answer is a 503 and the
// handler returns. The cause goes to the log and never to the caller.
func (p *product) auditRequired(ctx context.Context, e audit.Entry) error {
	if err := p.audit.Record(ctx, e); err != nil {
		p.logger.Error(ModuleName+": the mandatory audit row could not be written, so the action was not carried out",
			slog.String("action", e.Action), slog.String("cause", err.Error()))
		return speedhttp.Fault{Status: http.StatusServiceUnavailable}
	}
	return nil
}

// auditBestEffort writes the failed row that closes a write the registry
// refused. A failure is an Error line and changes nothing about the answer.
func (p *product) auditBestEffort(ctx context.Context, e audit.Entry) {
	if err := p.audit.Record(ctx, e); err != nil {
		p.logger.Error(ModuleName+": a failed-write audit row could not be written",
			slog.String("action", e.Action), slog.String("cause", err.Error()))
	}
}

// answer maps a failure the registry returned to the response it belongs to.
//
// The mapping is closed and fail-closed. A template that came from the
// configuration is a 409 that names its source, because the target exists but
// cannot be changed at runtime; a template the registry does not hold is a 404
// that carries no problem document; a name the registry already holds is a 409;
// a configuration or a message the registry refuses is a 400; any other failure
// is a 503 whose cause stays in the log and never reaches the caller.
//
// The 404 is returned as an empty reply rather than as a fault: the surface has
// always answered a missing template with the status alone, and the entry
// point's fault path always writes a problem document.
//
// sms.ErrNotSendable is not mapped. It is the sending path's sentinel, answered
// by Resolve when a template may not be sent under, and no management route
// reaches it; a registry that answers it here is answering the wrong question,
// so it falls to the same 503 an unknown failure gets.
//
// The one line an unmapped failure writes carries the cause and, from a call
// site that measured it, the operation's own fields. A mapped refusal is an
// answer and writes nothing here.
func (p *product) answer(op string, err error, attrs ...any) (speedhttp.Reply, error) {
	switch {
	case errors.Is(err, sms.ErrImmutable):
		return nil, speedhttp.Fault{Status: http.StatusConflict, Type: typePrefix + configTemplateCode}
	case errors.Is(err, sms.ErrNoTemplate):
		return speedhttp.EmptyReply(http.StatusNotFound), nil
	case errors.Is(err, sms.ErrTemplateExists):
		return nil, speedhttp.Fault{Status: http.StatusConflict, Type: typePrefix + conflictCode}
	case errors.Is(err, sms.ErrInvalidConfig), errors.Is(err, sms.ErrRejected):
		return nil, speedhttp.Fault{Status: http.StatusBadRequest, Type: typePrefix + badRequestCode}
	default:
		fields := append([]any{slog.String("cause", err.Error())}, attrs...)
		p.logger.Error(ModuleName+": "+op+" failed", fields...)
		return nil, speedhttp.Fault{Status: http.StatusServiceUnavailable}
	}
}
