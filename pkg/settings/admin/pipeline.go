package admin

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
// namespace; badRequest and invalidValue are this surface's two codes in it. A
// 404 and a 503 are written with no type: they say something is not there or a
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
	typePrefix       = "urn:speed:settings:"
	badRequestCode   = "bad-request"
	invalidValueCode = "invalid-value"
)

// errNoDeclaredItem reports a namespace no declared item belongs to. The answer
// is a bare 404 with no problem document, so the handler turns the sentinel into
// an empty reply rather than returning it as an error.
var errNoDeclaredItem = errors.New("settings.admin: no declared item belongs to the namespace")

// The query keys and the closed set of values the layer selection takes. The
// layer is named by the query string and has no default; the tenant is required
// on a tenant layer and refused on the global one. Both names and both values
// are the contract a host and a client read.
const (
	queryLayer  = "layer"
	queryTenant = "tenant"

	layerGlobal = "global"
	layerTenant = "tenant"
)

// selection is what a request's query string settled: the layer its action acts
// on, the policy domain its decision is made in, and the layer's own name and
// tenant for the audit detail. The layer and the domain move together — a global
// layer is decided in the platform domain, a tenant's in that tenant's — which is
// what keeps the two from drifting apart.
type selection struct {
	layer  settings.Layer
	domain authz.Domain
	// name is "global" or "tenant", the layer as the query string named it.
	name string
	// tenant is the tenant identifier a tenant layer names, empty for the global
	// layer.
	tenant string
}

// selectionOf builds the selection of a request whose layer the route's
// authorization step already validated: a tenant layer was held to the tenant
// identifier's domain and a global layer carried no tenant. It validates
// nothing itself, which is why it is called only from the declared handlers,
// behind that step.
func selectionOf(layer, tenant string) selection {
	if layer == layerTenant {
		return selection{
			layer:  settings.ForTenant(tenant),
			domain: authz.Domain(tenant),
			name:   layerTenant,
			tenant: tenant,
		}
	}
	return selection{layer: settings.Global(), domain: authz.PlatformDomain, name: layerGlobal}
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

// requireVerifiedAccount refuses a management write an account with an
// unproved email address makes. It sits after the caller is read and before the layer
// selection, the permission decision, the body, the mandatory audit and the
// settings module: a refused request asks no guard, lands no audit row and
// changes no setting.
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

// selectLayer settles the layer and the policy domain from the query string.
//
// The layer has no default: an absent or unreadable layer is a 400, and the
// route returns before the guard is asked. The tenant identifier is external
// input, and settings.ForTenant panics on one outside the tenant identifier's
// domain — non-empty and free of blanks and control characters — so it is held
// to that domain here before a layer is built, and a value outside it is a 400.
// The domain follows the layer: the platform domain for the global layer, the
// named tenant's for a tenant's.
func (p *product) selectLayer(c *gin.Context) (selection, bool) {
	query := c.Request.URL.Query()
	switch c.Query(queryLayer) {
	case layerGlobal:
		if _, present := query[queryTenant]; present {
			// A global layer names no tenant. A tenant given with it would be a
			// value the request used for nothing, which reads as a client that
			// meant the other layer.
			p.bad(c)
			return selection{}, false
		}
		return selectionOf(layerGlobal, ""), true
	case layerTenant:
		values, present := query[queryTenant]
		if !present || len(values) != 1 || !inTenantDomain(values[0]) {
			p.bad(c)
			return selection{}, false
		}
		return selectionOf(layerTenant, values[0]), true
	default:
		p.bad(c)
		return selection{}, false
	}
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

// require asks the handler-internal guard for one permission in the layer's
// domain. It is the decision every management route makes after reading the
// caller and settling the layer, and before the entry point binds the input: a
// shortfall is answered by the guard — a 403 for a denial, a 503 for an outage —
// and the route returns.
//
// The domain is named explicitly rather than read from the request. A global
// layer is a platform operation, decided in the platform domain; deciding in
// whatever tenant the request happened to resolve to would let a caller
// authorized in one tenant reach the global layer by sending a tenant header.
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

// bad answers a request this module refuses on shape alone: an unreadable layer
// selection or a tenant identifier outside its value domain. It is written before
// the entry point binds the request and before any settings call.
func (p *product) bad(c *gin.Context) {
	speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusBadRequest, Type: typePrefix + badRequestCode})
}

// invalidValue is the answer to a write the settings module refused because the
// value cannot be encoded under the declared type or lies outside its declared
// domain.
func (p *product) invalidValue() error {
	return speedhttp.Fault{Status: http.StatusUnprocessableEntity, Type: typePrefix + invalidValueCode}
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
