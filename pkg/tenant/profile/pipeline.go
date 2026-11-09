package profile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/tenant"
)

// The problem types this surface writes. typePrefix is the tenant capability's
// namespace; badRequest and conflict are this surface's two codes in it. A 404
// and a 503 carry no type: they say something is not there or a dependency
// could not answer, and not which kind of refusal it was.
const (
	typePrefix     = "urn:speed:tenant:"
	badRequestCode = "bad-request"
	conflictCode   = "conflict"
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

// writeNoIdentity401 writes the 401 of a route reached with no identity: the
// status alone, no problem type, and the realm challenge naming both carriers,
// the same shape the admission and authorization layers write.
func writeNoIdentity401(c *gin.Context, realm string) {
	realm = quoteRealm(realm)
	c.Header("WWW-Authenticate", `Bearer realm="`+realm+`", Cookie realm="`+realm+`"`)
	speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusUnauthorized})
}

// quoteRealm escapes what goes between the quotes of a realm.
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
// The decision is made in the tenant the request resolved to, not in the name
// the path happens to carry: the path-tenant layer has already held the path to
// that tenant, and its name may differ from its identifier. A shortfall is
// answered here and the declared handler is not called.
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
		domain, ok := policyDomain(c)
		if !ok {
			return
		}
		if !p.guard.RequirePermissionIn(c, perm, domain) {
			return
		}
		next(c)
	}
}

// policyDomain reads the tenant identifier the request resolved to.
//
// A request that carries none is answered the bare 404 the resolution and
// path-tenant layers write. The mount refuses an assembly without the tenant
// administration guard, so a mounted route is never reached without one; the
// check is the direct-call and hand-built-engine case, and it fails closed.
func policyDomain(c *gin.Context) (authz.Domain, bool) {
	id, ok := tenant.FromContext(c.Request.Context())
	if !ok {
		speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusNotFound})
		return "", false
	}
	return authz.Domain(id), true
}

// requireVerifiedAccount refuses a write an account with an unproved email
// address makes. It sits after the caller is read and before the permission
// decision, the body, the mandatory audit and the roster.
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

// prescreenJSON answers a JSON body the declared binding would refuse, before
// the binding runs, so the refusal carries this module's bad-request problem
// type. The body is read once here, probed as a JSON object whose members fit
// the declared input, and put back for the binding that follows.
func (p *product) prescreenJSON(next gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
		if err != nil || strings.ToLower(mediaType) != "application/json" {
			next(c)
			return
		}
		if c.Request.Body == nil {
			next(c)
			return
		}
		raw, err := io.ReadAll(c.Request.Body)
		if err != nil {
			p.refuse(c)
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(raw))
		if len(raw) == 0 {
			next(c)
			return
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil || object == nil {
			p.refuse(c)
			return
		}
		for _, key := range profileWriteFields {
			if _, present := object[key]; !present {
				p.refuse(c)
				return
			}
		}
		var probe profileWriteInput
		if err := json.Unmarshal(raw, &probe); err != nil {
			p.refuse(c)
			return
		}
		next(c)
	}
}

// auditRequired writes the mandatory audit row of a write, outside any
// transaction and before the action. A row that does not land is the audit
// module's state, and the action is not carried out.
func (p *product) auditRequired(ctx context.Context, e audit.Entry) error {
	if err := p.audit.Record(ctx, e); err != nil {
		p.logger.Error(ModuleName+": the mandatory audit row could not be written, so the action was not carried out",
			slog.String("action", e.Action), slog.String("cause", err.Error()))
		return speedhttp.Fault{Status: http.StatusServiceUnavailable}
	}
	return nil
}

// auditBestEffort writes the failed row that closes a write the roster refused.
// A failure is an Error line and changes nothing about the answer.
func (p *product) auditBestEffort(ctx context.Context, e audit.Entry) {
	if err := p.audit.Record(ctx, e); err != nil {
		p.logger.Error(ModuleName+": a failed-write audit row could not be written",
			slog.String("action", e.Action), slog.String("cause", err.Error()))
	}
}

// failureOf maps a failure the roster returned to the error the entry point
// answers with. The mapping is closed and fail-closed: a profile outside the
// domain is a 400, a tombstone a 409, an identifier no row carries a 404, and
// any other failure a 503 whose cause stays in the log.
func (p *product) failureOf(op string, err error) error {
	switch {
	case errors.Is(err, tenant.ErrInvalidProfile):
		return speedhttp.Fault{Status: http.StatusBadRequest, Type: typePrefix + badRequestCode}
	case errors.Is(err, tenant.ErrTenantDeleted):
		return speedhttp.Fault{Status: http.StatusConflict, Type: typePrefix + conflictCode}
	case errors.Is(err, tenant.ErrInvalidTenant):
		return speedhttp.Fault{Status: http.StatusNotFound}
	default:
		p.logger.Error(ModuleName+": "+op+" failed", slog.String("cause", err.Error()))
		return speedhttp.Fault{Status: http.StatusServiceUnavailable}
	}
}

// replyFailure is failureOf for a handler that builds its own answer. The 404
// this surface writes is a bare status with no body, which the problem-document
// writer cannot produce, so it becomes an empty reply.
func (p *product) replyFailure(op string, err error) (speedhttp.Reply, error) {
	if errors.Is(err, tenant.ErrInvalidTenant) {
		return speedhttp.EmptyReply(http.StatusNotFound), nil
	}
	return nil, p.failureOf(op, err)
}

// refuse answers a request this module refuses on shape alone, before any
// roster call, with the bad-request problem type.
func (p *product) refuse(c *gin.Context) {
	speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusBadRequest, Type: typePrefix + badRequestCode})
}

// badRequest is refuse as an error a typed handler returns.
func badRequest() error {
	return speedhttp.Fault{Status: http.StatusBadRequest, Type: typePrefix + badRequestCode}
}
