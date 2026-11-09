package tenantadmin

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"unicode"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/settings"
)

// The problem types this module writes. typePrefix is the settings capability's
// namespace; badRequest, invalidValue, protected, readOnly, enforced and
// maskedValue are this surface's codes in it. A 404 and a 503 are written with
// no type: they say something is not there or a dependency could not answer, and
// not which kind of refusal it was, so naming a kind would separate the failures
// the answer is meant to keep apart.
//
// readOnly is not protected: protected says the host's write seam refused a
// write to a tenant whose surface is read-only as a whole, while readOnly says
// the item itself is declared read-only and no write to it was ever possible.
//
// unavailable is not a problem type: it is the read answer's read_only_reason
// when the host's write seam cannot answer at all. The listing still serves the
// settings it read and declines to claim the surface is writable; the write
// routes keep answering 503 for the same unreadable seam.
//
// The contact-required refusal is the authentication capability's, not this
// surface's: the refusal it names is a fact about the acting account's
// registration, so a client branches on it the same way it branches on the answer
// any other surface of that capability gives. It is that capability's exported
// authn.ContactRequiredProblemType, written at the one refusal below rather than
// copied here.
const (
	typePrefix       = "urn:speed:settings:"
	badRequestCode   = "bad-request"
	invalidValueCode = "invalid-value"
	protectedCode    = "protected"
	readOnlyCode     = "read-only"
	enforcedCode     = "tenant-admission-enforced"
	maskedValueCode  = "masked-value"
	unavailableCode  = "unavailable"
)

// The query keys the platform surface uses and this surface refuses. The tenant
// surface's layer is fixed by the path, so either of these on a request here is a
// client trying to point it at another layer or another tenant; the answer is a
// 400 and nothing is read or written.
const (
	queryLayer  = "layer"
	queryTenant = "tenant"
)

// selection is what the path settled: the tenant layer the action acts on, the
// policy domain its decision is made in, and the tenant identifier the two are
// built from. The layer and the domain move together — both are the tenant in
// the path — which is what keeps the two from drifting apart.
type selection struct {
	layer  settings.Layer
	domain authz.Domain
	tenant string
}

// pathTenant settles the layer and the policy domain from the tenant in the
// path. The path-tenant guard has already resolved the segment from a name to
// the tenant's identifier and held it to the tenant the request resolved to, so
// the value is the internal identifier; settings.ForTenant panics on one outside
// the tenant identifier's domain — non-empty and free of blanks and control
// characters — so it is held to that domain here before a layer is built; a
// value outside it reports false and the handler answers the capability's
// bad-request problem.
func (p *product) pathTenant(tenant string) (selection, bool) {
	if !inTenantDomain(tenant) {
		return selection{}, false
	}
	return selection{layer: settings.ForTenant(tenant), domain: authz.Domain(tenant), tenant: tenant}, true
}

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

// requireVerifiedAccount refuses a management write an account with an unproved
// email address makes. It sits after the caller is read and before the
// permission decision, the body, the mandatory audit and the settings module: a
// refused request asks no guard, lands no audit row and changes no setting.
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

// hasLayerQuery reports whether the request carries a layer or tenant query
// parameter. Either belongs to the platform surface, and on this one it is a
// client trying to point the action at another layer or another tenant: the
// answer is a 400, written after the permission decision and before the entry
// point binds the request.
func hasLayerQuery(c *gin.Context) bool {
	query := c.Request.URL.Query()
	_, layer := query[queryLayer]
	_, tenant := query[queryTenant]
	return layer || tenant
}

// inTenantDomain reports whether id is inside the tenant identifier's value
// domain: non-empty, and free of blanks and control characters anywhere in it.
//
// It restates the criterion the settings module restates for ForTenant, and it
// imports no tenant package: a module that stores values per tenant by their
// identifier is deliberately not dependent on the tenant capability, and the
// settings module states the same domain the same way.
func inTenantDomain(id string) bool {
	return id != "" && strings.IndexFunc(id, notInAnIdentifier) < 0
}

// notInAnIdentifier reports the characters no tenant identifier carries. It is
// the tenant directory's criterion, restated here: an identifier reaches log
// lines, diagnostics and error text, where a newline splits one record into two.
func notInAnIdentifier(r rune) bool {
	return unicode.IsSpace(r) || unicode.IsControl(r)
}

// require asks the handler-internal guard for one permission in the path
// tenant's domain. It is the decision every management route makes after reading
// the caller and, on a write, the acting account's registration state, and
// before the entry point binds the input: a shortfall is answered by the guard —
// a 403 for a denial, a 503 for an outage — and the route returns.
//
// The domain is named explicitly rather than read from the request. The decision
// is made in the tenant the path names, and reading the domain from the request's
// resolved tenant instead would let the two drift when the path guard is absent
// from an assembly.
func (p *product) require(c *gin.Context, perm authz.Permission, domain authz.Domain) bool {
	return p.guard.RequirePermissionIn(c, perm, domain)
}

// auditRequired writes the mandatory audit row of a write, outside any
// transaction and before the action. A row that does not land is the audit
// module's state, and the action is not carried out: the answer is a 503 and the
// route returns. The cause goes to the log and never to the caller.
func (p *product) auditRequired(ctx context.Context, e audit.Entry) error {
	if err := p.audit.Record(ctx, e); err != nil {
		p.logger.Error(ModuleName+": the mandatory audit row could not be written, so the action was not carried out",
			slog.String("action", e.Action), slog.String("cause", err.Error()))
		return p.failure(http.StatusServiceUnavailable)
	}
	return nil
}

// auditBestEffort writes the failed row that closes a write the settings module
// refused. A failure is an Error line and changes nothing about the answer.
func (p *product) auditBestEffort(ctx context.Context, e audit.Entry) {
	if err := p.audit.Record(ctx, e); err != nil {
		p.logger.Error(ModuleName+": a failed-write audit row could not be written",
			slog.String("action", e.Action), slog.String("cause", err.Error()))
	}
}

// answer maps a failure the settings module returned to the answer it belongs
// to.
//
// The mapping is closed and fail-closed. A value that cannot be encoded under
// the declared type or lies outside its declared domain is a 422; any other
// failure is a 503 whose cause stays in the log and never reaches the caller.
// settings.ErrNotLoaded is not mapped: it is the read before the settings
// module's Init loaded its snapshot, and a management route reads on the request
// path, by which time every module's Init has run.
func (p *product) answer(op string, err error) error {
	switch {
	case errors.Is(err, settings.ErrTypeMismatch), errors.Is(err, settings.ErrInvalidValue):
		return p.invalidValue()
	default:
		return p.unavailable(op, err)
	}
}

// bad answers a request this module refuses on shape alone: an unreadable tenant
// identifier or a layer or tenant query parameter on a surface whose layer is
// fixed by the path. The typed handlers return badRequest, which writes the same
// problem through the entry point's fault writer.
func (p *product) bad(c *gin.Context) {
	speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusBadRequest, Type: typePrefix + badRequestCode})
}

// badRequest is the shape refusal a typed handler returns.
func (p *product) badRequest() error {
	return speedhttp.Fault{Status: http.StatusBadRequest, Type: typePrefix + badRequestCode}
}

// protected is the answer to a write the host's settings write seam refused: the
// action did not happen, no audit row landed and the settings module was not
// called.
func (p *product) protected() error {
	return speedhttp.Fault{Status: http.StatusForbidden, Type: typePrefix + protectedCode}
}

// readOnly is the answer to a write of an item the declarations expose to a
// tenant for reading alone. The item is visible, so the answer names the
// refusal instead of hiding the item behind the bare 404 an undeclared key
// gets; the seam, the audit and the store are not reached and the tenant layer
// is unchanged.
func (p *product) readOnly() error {
	return speedhttp.Fault{Status: http.StatusForbidden, Type: typePrefix + readOnlyCode}
}

// invalidValue is the answer to a write the settings module refused because the
// value cannot be encoded under the declared type or lies outside its declared
// domain.
func (p *product) invalidValue() error {
	return speedhttp.Fault{Status: http.StatusUnprocessableEntity, Type: typePrefix + invalidValueCode}
}

// maskedValue is the answer to a write that carries the mask string a sensitive
// item's value is shown as. It is what the client read, not a value anyone
// chose; the write is refused before the seam, the audit and the store, so the
// credential is left as it was.
func (p *product) maskedValue() error {
	return speedhttp.Fault{Status: http.StatusUnprocessableEntity, Type: typePrefix + maskedValueCode}
}

// enforced is the answer to a write that would turn off a policy the platform
// layer requires. The platform layer is a floor, so the write is refused rather
// than accepted and overruled; no audit row lands and the tenant layer is
// unchanged.
func (p *product) enforced() error {
	return speedhttp.Fault{Status: http.StatusConflict, Type: typePrefix + enforcedCode}
}

// unavailable logs a failure and answers a 503, never telling the caller which
// dependency failed.
func (p *product) unavailable(op string, err error) error {
	p.logger.Error(ModuleName+": "+op+" failed", slog.String("cause", err.Error()))
	return p.failure(http.StatusServiceUnavailable)
}

// failure is the answer of one of this module's own refusals: the status alone,
// with the capability's problem type where the refusal names one.
func (p *product) failure(status int) error {
	return speedhttp.Fault{Status: status}
}
