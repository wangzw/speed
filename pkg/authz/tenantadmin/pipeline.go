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

// The problem types this module writes. protected covers every refusal about the
// target rather than a missing permission. A 503 is written with no type.
const (
	typePrefix     = "urn:speed:authz:"
	badRequestCode = "bad-request"
	conflictCode   = "conflict"
	protectedCode  = "protected"
)

// realmOf reads back the endpoint a route was mounted on.
func realmOf(c *gin.Context) string {
	if v, ok := c.Get(realmKey); ok {
		if name, ok := v.(string); ok {
			return name
		}
	}
	return ""
}

// writeNoIdentity401 writes the 401 of a management route reached with no
// identity, the same shape the admission and authorization layers write.
func writeNoIdentity401(c *gin.Context, realm string) {
	realm = quoteRealm(realm)
	c.Header("WWW-Authenticate", `Bearer realm="`+realm+`", Cookie realm="`+realm+`"`)
	speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusUnauthorized})
}

// quoteRealm escapes what goes between the quotes of a realm: a quote or a
// backslash gets a backslash, and a line ending is dropped. The rule is copied
// into every surface that writes a no-identity 401; change every copy by hand.
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
// front of the declared handler. The decision is made in the tenant the path
// names through RequirePermissionIn, not in whatever tenant the request resolved
// to.
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
// decision, the body, the mandatory audit and the manager. The subject is the
// request's actor, so under an impersonation the target's verification state does
// not participate. A state that cannot be read is a 503, never verified.
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

// prescreenRoleWrite reproduces this surface's body reading before the entry
// point binds the declared input: a body that is one JSON object, with every
// member the declaration does not describe dropped.
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

// conflict is a write the manager refused because of the state of the target.
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
// rejects the write op names. It sits after the shape and anti-escalation checks
// and before the mandatory audit and the manager. An answer that cannot be read
// is a 503, never a permit.
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
// answers with: unknown role, tenant mismatch and missing platform membership
// are 400; builtin role and last tenant administrator are 409; a reserved role
// is 403 protected; anything else is a 503 whose cause stays in the log.
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
