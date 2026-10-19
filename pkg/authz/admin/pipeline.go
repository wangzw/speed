package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
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
	typePrefix     = "urn:speed:authz:"
	badRequestCode = "bad-request"
	conflictCode   = "conflict"
)

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

// guardRoute puts the route's identity and permission decision in front of the
// declared handler.
//
// The decision cannot live in the handler: the entry point binds the declared
// input — including a request body — before it calls the handler, so a handler
// that refused a missing identity would answer a request with an unreadable body
// after the binding refusal instead of before it. The management surface's order
// is identity, then permission, then shape, and this wrapper is what keeps it.
// The decision is made in the platform domain and not the tenant the request
// resolved to: the tenant in a management path is the data the route acts on, so
// deciding in the request's own tenant would let a caller authorized in one
// tenant reach another's roles, grants and bindings by naming that tenant in the
// path.
//
// A shortfall is answered here and the declared handler is not called: a 401
// with the realm challenge when there is no identity, and the guard's own 403 or
// 503 when it refuses.
func (p *product) guardRoute(perm authz.Permission, next gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := authn.FromContext(c.Request.Context()); !ok {
			writeNoIdentity401(c, realmOf(c))
			return
		}
		if !p.guard.RequirePermissionIn(c, perm, authz.PlatformDomain) {
			return
		}
		next(c)
	}
}

// prescreenRoleWrite reproduces the body reading this surface's contract
// promises before the entry point binds the declared input: a body that is one
// JSON object, with every member the declaration does not describe ignored.
//
// The entry point's binding is closed and header-strict — a member the input
// does not declare is a 400, and a Content-Type other than application/json is a
// 415 — while this surface's role write accepts a JSON object whatever its
// media type and forwards only the description key. A member such as `builtin`
// reaches nothing: it is dropped here, before the binding, and the handler
// builds its record from the declared field alone. A body that is not one JSON
// object is refused with this module's bad-request code, the answer the shape
// refusal has always carried.
func prescreenRoleWrite(next gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body == nil {
			refuseBadRequest(c)
			return
		}
		raw, err := io.ReadAll(c.Request.Body)
		if err != nil {
			refuseBadRequest(c)
			return
		}
		if len(bytes.TrimSpace(raw)) == 0 {
			refuseBadRequest(c)
			return
		}
		var members map[string]json.RawMessage
		if decodeErr := json.NewDecoder(bytes.NewReader(raw)).Decode(&members); decodeErr != nil {
			refuseBadRequest(c)
			return
		}
		if members == nil {
			// A literal null decoded into no object. The write is a role with no
			// description key, which is the value the surface's original body
			// reader left behind.
			members = map[string]json.RawMessage{}
		}
		kept := make(map[string]json.RawMessage, 1)
		if value, ok := members["description_key"]; ok {
			// The declared field is a string. A value of another shape is refused
			// here with this module's code, the answer the surface's original body
			// reader gave it, rather than reaching the entry point's untyped 400.
			var text string
			if valueErr := json.Unmarshal(value, &text); valueErr != nil {
				refuseBadRequest(c)
				return
			}
			kept["description_key"] = value
		}
		encoded, err := json.Marshal(kept)
		if err != nil {
			refuseBadRequest(c)
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(encoded))
		c.Request.ContentLength = int64(len(encoded))
		c.Request.Header.Set("Content-Type", "application/json")
		next(c)
	}
}

// refuseBadRequest answers a request this module refuses on shape alone, before
// any manager call, with the bad-request problem type.
func refuseBadRequest(c *gin.Context) {
	speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusBadRequest, Type: typePrefix + badRequestCode})
}

// badRequest is the shape refusal as an error a typed handler returns.
func badRequest() error {
	return speedhttp.Fault{Status: http.StatusBadRequest, Type: typePrefix + badRequestCode}
}

// conflict is a write the manager refused because of the state of the target,
// such as an attempt to change or delete a builtin role.
func conflict() error {
	return speedhttp.Fault{Status: http.StatusConflict, Type: typePrefix + conflictCode}
}

// auditRequired writes the mandatory audit row of a write, outside any
// transaction and before the action. A row that does not land is the audit
// module's state, and the action is not carried out: the handler returns a 503
// and the cause goes to the log, never to the caller.
func (p *product) auditRequired(ctx context.Context, e audit.Entry) error {
	if err := p.audit.Record(ctx, e); err != nil {
		p.logger.Error(ModuleName+": the mandatory audit row could not be written, so the action was not carried out",
			slog.String("action", e.Action), slog.String("cause", err.Error()))
		return speedhttp.Fault{Status: http.StatusServiceUnavailable}
	}
	return nil
}

// auditBestEffort writes the failed row that closes a write the manager refused.
// A failure is an Error line and changes nothing about the answer.
func (p *product) auditBestEffort(ctx context.Context, e audit.Entry) {
	if err := p.audit.Record(ctx, e); err != nil {
		p.logger.Error(ModuleName+": a failed-write audit row could not be written",
			slog.String("action", e.Action), slog.String("cause", err.Error()))
	}
}

// failureOf maps a failure the manager returned to the error the entry point
// answers with.
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
func (p *product) failureOf(op string, err error) error {
	switch {
	case errors.Is(err, authz.ErrUnknownRole), errors.Is(err, authz.ErrTenantMismatch):
		return badRequest()
	case errors.Is(err, authz.ErrBuiltinRole):
		return conflict()
	default:
		p.logger.Error(ModuleName+": "+op+" failed", slog.String("cause", err.Error()))
		return speedhttp.Fault{Status: http.StatusServiceUnavailable}
	}
}
