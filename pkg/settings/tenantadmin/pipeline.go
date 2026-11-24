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

const (
	queryLayer  = "layer"
	queryTenant = "tenant"
)

type selection struct {
	layer  settings.Layer
	domain authz.Domain
	tenant string
}

func (p *product) pathTenant(tenant string) (selection, bool) {
	if !inTenantDomain(tenant) {
		return selection{}, false
	}
	return selection{layer: settings.ForTenant(tenant), domain: authz.Domain(tenant), tenant: tenant}, true
}

func (p *product) caller(c *gin.Context) (authn.Identity, bool) {
	id, ok := authn.FromContext(c.Request.Context())
	if !ok {
		writeNoIdentity401(c, realmOf(c))
		return authn.Identity{}, false
	}
	return id, true
}

// It reads the request's actor, never the impersonation target; a state that cannot be read is a 503, never verified.
func (p *product) requireVerifiedAccount(c *gin.Context, id authn.Identity) bool {
	actor := id.Actor()
	if actor == "" || actor.IsStatic() || slices.Contains(id.Methods(), authn.MethodClientSecret) {
		return true
	}
	status, err := p.verification.SignupStateOf(c.Request.Context(), actor)
	switch {
	case errors.Is(err, authn.ErrAccountNotFound):
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

func realmOf(c *gin.Context) string {
	if v, ok := c.Get(realmKey); ok {
		if name, ok := v.(string); ok {
			return name
		}
	}
	return ""
}

// writeNoIdentity401 writes the 401 of a route reached with no identity, the same
// shape the admission and authorization layers write.
func writeNoIdentity401(c *gin.Context, realm string) {
	realm = quoteRealm(realm)
	c.Header("WWW-Authenticate", `Bearer realm="`+realm+`", Cookie realm="`+realm+`"`)
	speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusUnauthorized})
}

// quoteRealm escapes a realm with the authorization layer's rule, kept byte-identical
// here because the two 401 shapes must agree.
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

func hasLayerQuery(c *gin.Context) bool {
	query := c.Request.URL.Query()
	_, layer := query[queryLayer]
	_, tenant := query[queryTenant]
	return layer || tenant
}

func inTenantDomain(id string) bool {
	return id != "" && strings.IndexFunc(id, notInAnIdentifier) < 0
}

func notInAnIdentifier(r rune) bool {
	return unicode.IsSpace(r) || unicode.IsControl(r)
}

// The permission's domain is the path tenant's, named explicitly so it cannot drift from the tenant the path names.
func (p *product) require(c *gin.Context, perm authz.Permission, domain authz.Domain) bool {
	return p.guard.RequirePermissionIn(c, perm, domain)
}

// auditRequired writes the mandatory audit row before the action; a row that does
// not land answers 503 and the action does not run.
func (p *product) auditRequired(ctx context.Context, e audit.Entry) error {
	if err := p.audit.Record(ctx, e); err != nil {
		p.logger.Error(ModuleName+": the mandatory audit row could not be written, so the action was not carried out",
			slog.String("action", e.Action), slog.String("cause", err.Error()))
		return p.failure(http.StatusServiceUnavailable)
	}
	return nil
}

func (p *product) auditBestEffort(ctx context.Context, e audit.Entry) {
	if err := p.audit.Record(ctx, e); err != nil {
		p.logger.Error(ModuleName+": a failed-write audit row could not be written",
			slog.String("action", e.Action), slog.String("cause", err.Error()))
	}
}

func (p *product) answer(op string, err error) error {
	switch {
	case errors.Is(err, settings.ErrTypeMismatch), errors.Is(err, settings.ErrInvalidValue):
		return p.invalidValue()
	default:
		return p.unavailable(op, err)
	}
}

func (p *product) bad(c *gin.Context) {
	speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusBadRequest, Type: typePrefix + badRequestCode})
}

func (p *product) badRequest() error {
	return speedhttp.Fault{Status: http.StatusBadRequest, Type: typePrefix + badRequestCode}
}

func (p *product) protected() error {
	return speedhttp.Fault{Status: http.StatusForbidden, Type: typePrefix + protectedCode}
}

func (p *product) readOnly() error {
	return speedhttp.Fault{Status: http.StatusForbidden, Type: typePrefix + readOnlyCode}
}

func (p *product) invalidValue() error {
	return speedhttp.Fault{Status: http.StatusUnprocessableEntity, Type: typePrefix + invalidValueCode}
}

func (p *product) maskedValue() error {
	return speedhttp.Fault{Status: http.StatusUnprocessableEntity, Type: typePrefix + maskedValueCode}
}

func (p *product) enforced() error {
	return speedhttp.Fault{Status: http.StatusConflict, Type: typePrefix + enforcedCode}
}

// unavailable logs a failure and answers 503 without naming the dependency.
func (p *product) unavailable(op string, err error) error {
	p.logger.Error(ModuleName+": "+op+" failed", slog.String("cause", err.Error()))
	return p.failure(http.StatusServiceUnavailable)
}

func (p *product) failure(status int) error {
	return speedhttp.Fault{Status: status}
}
