package admin

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

// The problem types this module writes. protected is the host write guard's
// refusal, kept apart from the enforcement layer's forbidden code. A 503 is
// written with no type.
const (
	typePrefix     = "urn:speed:authz:"
	badRequestCode = "bad-request"
	conflictCode   = "conflict"
	protectedCode  = "protected"
)

// realmOf reads back the endpoint a management route was mounted on; it is
// empty when the handler is called directly.
func realmOf(c *gin.Context) string {
	if v, ok := c.Get(realmKey); ok {
		if name, ok := v.(string); ok {
			return name
		}
	}
	return ""
}

// writeNoIdentity401 writes the 401 of a management route reached with no
// identity, the same answer the admission and authorization layers write.
func writeNoIdentity401(c *gin.Context, realm string) {
	realm = quoteRealm(realm)
	c.Header("WWW-Authenticate", `Bearer realm="`+realm+`", Cookie realm="`+realm+`"`)
	speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusUnauthorized})
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

// guardRoute puts the route's identity and permission decision in front of the
// declared handler. The order is identity, then the registration check of a
// write, then permission, then shape.
//
// The decision is made in the platform domain and not the tenant the request
// resolved to: the tenant in a management path is the data the route acts on, so
// deciding in the request's tenant would let a caller authorized in one tenant
// reach another's rows.
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
		if !p.guard.RequirePermissionIn(c, perm, authz.PlatformDomain) {
			return
		}
		next(c)
	}
}

// requireVerifiedAccount refuses a management write an account with an unproved
// email address makes. It sits after the caller is read and before the permission
// decision, the body, the mandatory audit and the manager.
//
// The subject it reads is the request's actor, not the account the request is
// taken to be from: under an impersonation the target's verification state does
// not participate. A service account, a static-table subject and an actor with
// no email address are exempt; a read route does not consult it.
//
// A state that cannot be read is a 503 and is never read as verified.
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
// member the declaration does not describe dropped. Only the description is
// forwarded.
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

// requireWriteAllowed asks the host's optional WriteGuard whether the tenant
// rejects the write op names. It sits after the route's shape checks and before
// the mandatory audit and the manager. A denial is a 403 with the protected
// code; an answer that cannot be read is a 503, never a permission.
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
		return speedhttp.Fault{Status: http.StatusForbidden, Type: typePrefix + protectedCode}
	}
	return nil
}

// failureOf maps a failure the manager returned to the error the entry point
// answers with: unknown role, tenant mismatch and missing platform membership
// are 400; builtin role and last tenant administrator are 409; a reserved role
// is 403 protected; any other failure is a 503 whose cause stays in the log.
func (p *product) failureOf(op string, err error) error {
	switch {
	case errors.Is(err, authz.ErrUnknownRole), errors.Is(err, authz.ErrTenantMismatch),
		errors.Is(err, authz.ErrPlatformMembershipRequired):
		return badRequest()
	case errors.Is(err, authz.ErrBuiltinRole), errors.Is(err, authz.ErrLastTenantAdmin):
		return conflict()
	case errors.Is(err, authz.ErrReservedRole):
		return speedhttp.Fault{Status: http.StatusForbidden, Type: typePrefix + protectedCode}
	default:
		p.logger.Error(ModuleName+": "+op+" failed", slog.String("cause", err.Error()))
		return speedhttp.Fault{Status: http.StatusServiceUnavailable}
	}
}
