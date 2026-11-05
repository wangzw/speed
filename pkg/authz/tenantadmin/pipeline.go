package tenantadmin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// The problem types this module writes. typePrefix is the authorization
// capability's namespace; badRequest, conflict and protected are this surface's
// codes in it. protected covers every refusal that is about the target not
// being allowed rather than the caller lacking a permission: a reserved role, a
// permission the caller does not hold, a role higher than the caller's own, and
// the host write guard's refusal. The enforcement layer's own 403 keeps its
// forbidden code and is written by the guard.
//
// The contact-required refusal is the authentication capability's, written from
// its exported constant rather than copied here. A 503 is written with no type:
// it says a dependency could not answer, not what was refused.
const (
	typePrefix     = "urn:speed:authz:"
	badRequestCode = "bad-request"
	conflictCode   = "conflict"
	protectedCode  = "protected"
)

// realmOf reads back the endpoint a route was mounted on, the name the handler
// writes as the realm of its 401.
func realmOf(c *gin.Context) string {
	if v, ok := c.Get(realmKey); ok {
		if name, ok := v.(string); ok {
			return name
		}
	}
	return ""
}

// writeNoIdentity401 writes the 401 of a management route reached with no
// identity: the status alone, no problem type, and the realm challenge naming
// both carriers, the same shape the admission and authorization layers write.
func writeNoIdentity401(c *gin.Context, realm string) {
	realm = quoteRealm(realm)
	c.Header("WWW-Authenticate", `Bearer realm="`+realm+`", Cookie realm="`+realm+`"`)
	speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusUnauthorized})
}

// quoteRealm escapes what goes between the quotes of a realm: a quote or a
// backslash gets a backslash, and a line ending is dropped because it would
// start another header.
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

// guardRoute puts the route's identity, registration and permission decision in
// front of the declared handler.
//
// The decision is made in the tenant the path names, through RequirePermissionIn,
// not in whatever tenant the request happened to resolve to: the path tenant is
// the scope the route acts on, and the path-tenant layer has already held it to
// the tenant the request resolved to. A shortfall is answered here and the
// declared handler is not called.
func (p *product) guardRoute(method string, perm authz.Permission, next gin.HandlerFunc) gin.HandlerFunc {
	write := method != http.MethodGet
	return func(c *gin.Context) {
		id, ok := authn.FromContext(c.Request.Context())
		if !ok {
			writeNoIdentity401(c, realmOf(c))
			return
		}
		if write && !p.requireVerifiedAccount(c, id) {
			return
		}
		if !p.guard.RequirePermissionIn(c, perm, authz.Domain(c.Param("tenant"))) {
			return
		}
		next(c)
	}
}

// requireVerifiedAccount refuses a write an account with an unproved email
// address makes. It sits after the caller is read and before the permission
// decision, the body, the mandatory audit and the manager.
//
// The subject it reads is the request's actor, not the account the request is
// taken to be from: under an impersonation the actor is whose hands are on the
// request. A given actor is exempt — one with no email address to verify, a
// service account, and a static-table subject. A read route does not consult it
// at all. A state that cannot be read is a 503 and is never read as verified.
func (p *product) requireVerifiedAccount(c *gin.Context, id authn.Identity) bool {
	actor := id.Actor()
	if actor == "" || actor.IsStatic() || slices.Contains(id.Methods(), authn.MethodClientSecret) {
		return true
	}
	status, err := p.verification.SignupStateOf(c.Request.Context(), actor)
	switch {
	case errors.Is(err, authn.ErrAccountNotFound):
		// No account row holds no email address; the rest of the pipeline answers
		// for it.
		return true
	case err != nil:
		p.logger.Error(ModuleName+": the signup state could not be read, so the write was not carried out",
			slog.String("cause", err.Error()))
		speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusServiceUnavailable})
		return false
	}
	if status.State == authn.SignupPending {
		speedhttp.Fail(c, speedhttp.Problem{
			Status: http.StatusForbidden,
			Type:   authn.ContactRequiredProblemType,
		})
		return false
	}
	return true
}

// prescreenRoleWrite reproduces the body reading this surface's contract
// promises before the entry point binds the declared input: a body that is one
// JSON object, with every member the declaration does not describe ignored.
//
// A member such as builtin reaches nothing: it is dropped here, before the
// binding, and the handler builds its record from the declared field alone. A
// body that is not one JSON object is refused with this module's bad-request
// code.
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
			// description, which is the value the surface's original body reader
			// left behind.
			members = map[string]json.RawMessage{}
		}
		kept := make(map[string]json.RawMessage, 1)
		for _, name := range []string{"description"} {
			value, ok := members[name]
			if !ok {
				continue
			}
			var text string
			if valueErr := json.Unmarshal(value, &text); valueErr != nil {
				// A literal null names no text, the same absence an omitted member
				// is; any other non-string value is a shape refusal.
				if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
					continue
				}
				refuseBadRequest(c)
				return
			}
			kept[name] = value
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
// such as an attempt to change or delete a builtin role or to remove the last
// tenant administrator.
func conflict() error {
	return speedhttp.Fault{Status: http.StatusConflict, Type: typePrefix + conflictCode}
}

// protected is a refusal about the target rather than a missing permission: a
// reserved role, an escalation the caller may not perform, or the host write
// guard's denial.
func protected() error {
	return speedhttp.Fault{Status: http.StatusForbidden, Type: typePrefix + protectedCode}
}

// unavailable is a dependency that could not answer: the cause stays in the log
// and never reaches the caller.
func (p *product) unavailable(op string, cause error) error {
	p.logger.Error(ModuleName+": "+op+" failed", slog.String("cause", cause.Error()))
	return speedhttp.Fault{Status: http.StatusServiceUnavailable}
}

// auditRequired writes the mandatory audit row of a write, outside any
// transaction and before the action. A row that does not land is a 503 and the
// action is not carried out.
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

// requireWriteAllowed asks the host's optional WriteGuard whether the tenant
// rejects the write op names. It sits after the route's shape and
// anti-escalation checks and before the mandatory audit and the manager. A
// denial is a 403 with the protected code; an answer that cannot be read is a
// 503 and is never read as a permit.
func (p *product) requireWriteAllowed(ctx context.Context, tenant string, op authz.WriteOp) error {
	if p.writeGuard == nil {
		return nil
	}
	denied, err := p.writeGuard.Denied(ctx, tenant, op)
	if err != nil {
		p.logger.Error(ModuleName+": the write guard could not answer, so the write was not carried out",
			slog.String("tenant", tenant), slog.String("cause", err.Error()))
		return speedhttp.Fault{Status: http.StatusServiceUnavailable}
	}
	if denied {
		return protected()
	}
	return nil
}

// failureOf maps a failure the manager returned to the error the entry point
// answers with. The mapping is closed and fail-closed: an unknown role, a tenant
// mismatch and an account that is not a platform member are 400s; a builtin role
// and the last tenant administrator are 409s; a reserved role is a 403 with the
// protected code; anything else is a 503 whose cause stays in the log.
func (p *product) failureOf(op string, err error) error {
	switch {
	case errors.Is(err, authz.ErrUnknownRole), errors.Is(err, authz.ErrTenantMismatch),
		errors.Is(err, authz.ErrPlatformMembershipRequired):
		return badRequest()
	case errors.Is(err, authz.ErrBuiltinRole), errors.Is(err, authz.ErrLastTenantAdmin):
		return conflict()
	case errors.Is(err, authz.ErrReservedRole):
		return protected()
	default:
		return p.unavailable(op, err)
	}
}
