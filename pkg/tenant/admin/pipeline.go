package admin

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

// The problem types this module writes. typePrefix is the tenant capability's
// namespace; badRequest and conflict are this surface's two codes in it. A 404
// and a 503 are written with no type: they say something is not there or a
// dependency could not answer, and not which kind of refusal it was, so naming a
// kind would separate the failures the answer is meant to keep apart.
//
// The contact-required refusal is the authentication capability's, not this surface's:
// the refusal it names is a fact about the acting account's registration, so a
// client branches on it the same way it branches on the answer any other
// surface of that capability gives. It is that capability's exported
// authn.ContactRequiredProblemType, written at the one refusal below rather than
// copied here.
const (
	typePrefix     = "urn:speed:tenant:"
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

// guardRoute puts the route's identity and permission decision in front of the
// declared handler.
//
// The decision cannot live in the handler: the entry point binds the declared
// input — including the JSON body — before it calls the handler, so a handler
// that refused a missing identity would answer a request with an unreadable body
// after the binding refusal instead of before it. The management surface's order
// is identity, then the registration check of a write, then permission, then
// shape, and this wrapper is what keeps it. The decision is made in the platform
// domain and not the tenant the request resolved to: the roster is the process's
// own table of which tenants exist, so deciding in whatever tenant the request
// happened to resolve to would let a caller authorized in one tenant reach the
// whole roster by sending a tenant header.
//
// A shortfall is answered here and the declared handler is not called: a 401
// with the realm challenge when there is no identity, the 403 contact-required of
// an unverified self-registration on a write, and the guard's own 403 or 503 when
// it refuses.
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

// requireVerifiedAccount refuses a management write an account with an
// unproved email address makes. It sits after the caller is read and before the
// permission decision, the body, the mandatory audit and the roster: a refused
// request asks no guard, lands no audit row and changes no tenant.
//
// The subject it reads is the request's actor, not the account the request is
// taken to be from: under an impersonation the actor is whose hands are on the
// request and the target's verification state does not participate, which is the
// design's rule for this gate on every capability's management face. A given
// actor is exempt — one with no email address to verify, a service account, and
// a static-table subject, which has no account row and no email address — and a
// read route does not consult it at all.
//
// A state that cannot be read is a 503 and is never read as verified: a
// dependency that cannot answer must not widen what a caller may do.
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
// type.
//
// The entry point answers a binding failure with a problem document whose type
// is empty, which reaches a client as about:blank. This surface promises
// urn:speed:tenant:bad-request for every request it refuses on shape, and the
// handler runs after the binding, so the type cannot be restored there. The body
// is read once here, probed as a JSON object whose members fit the declared
// input, and put back for the binding that follows. A media type the entry point
// will refuse is left to it, so its 415 answer is unchanged.
//
// Unknown members are not probed: the declared binding refuses them itself, with
// the entry point's untyped 400.
func prescreenJSON[T any](p *product, next gin.HandlerFunc) gin.HandlerFunc {
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
		var probe T
		if err := json.Unmarshal(raw, &probe); err != nil {
			p.refuse(c)
			return
		}
		next(c)
	}
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

// auditBestEffort writes the failed row that closes a write the roster refused.
// A failure is an Error line and changes nothing about the answer.
func (p *product) auditBestEffort(ctx context.Context, e audit.Entry) {
	if err := p.audit.Record(ctx, e); err != nil {
		p.logger.Error(ModuleName+": a failed-write audit row could not be written",
			slog.String("action", e.Action), slog.String("cause", err.Error()))
	}
}

// failureOf maps a failure the roster returned to the error the entry point
// answers with.
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
func (p *product) failureOf(op string, err error) error {
	switch {
	case errors.Is(err, tenant.ErrTenantExists), errors.Is(err, tenant.ErrAliasConflict):
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
// writer cannot produce, so it becomes an empty reply; every other failure stays
// the fault failureOf names.
func (p *product) replyFailure(op string, err error) (speedhttp.Reply, error) {
	if errors.Is(err, tenant.ErrInvalidTenant) {
		return speedhttp.EmptyReply(http.StatusNotFound), nil
	}
	return nil, p.failureOf(op, err)
}

// refuse answers a request this module refuses on shape alone. The answer is
// written before any roster call and carries the bad-request problem type.
func (p *product) refuse(c *gin.Context) {
	speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusBadRequest, Type: typePrefix + badRequestCode})
}

// badRequest is refuse as an error a typed handler returns.
func badRequest() error {
	return speedhttp.Fault{Status: http.StatusBadRequest, Type: typePrefix + badRequestCode}
}
