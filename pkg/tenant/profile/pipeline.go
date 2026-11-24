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

const (
	typePrefix     = "urn:speed:tenant:"
	badRequestCode = "bad-request"
	conflictCode   = "conflict"
)

func realmOf(c *gin.Context) string {
	if v, ok := c.Get(realmKey); ok {
		if name, ok := v.(string); ok {
			return name
		}
	}
	return ""
}

func writeNoIdentity401(c *gin.Context, realm string) {
	realm = quoteRealm(realm)
	c.Header("WWW-Authenticate", `Bearer realm="`+realm+`", Cookie realm="`+realm+`"`)
	speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusUnauthorized})
}

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

// The decision is made in the tenant the request resolved to, not in the name the path carries.
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

func policyDomain(c *gin.Context) (authz.Domain, bool) {
	id, ok := tenant.FromContext(c.Request.Context())
	if !ok {
		speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusNotFound})
		return "", false
	}
	return authz.Domain(id), true
}

// requireVerifiedAccount reads the request's actor, not the impersonation target.
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

func (p *product) auditRequired(ctx context.Context, e audit.Entry) error {
	if err := p.audit.Record(ctx, e); err != nil {
		p.logger.Error(ModuleName+": the mandatory audit row could not be written, so the action was not carried out",
			slog.String("action", e.Action), slog.String("cause", err.Error()))
		return speedhttp.Fault{Status: http.StatusServiceUnavailable}
	}
	return nil
}

func (p *product) auditBestEffort(ctx context.Context, e audit.Entry) {
	if err := p.audit.Record(ctx, e); err != nil {
		p.logger.Error(ModuleName+": a failed-write audit row could not be written",
			slog.String("action", e.Action), slog.String("cause", err.Error()))
	}
}

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

func (p *product) replyFailure(op string, err error) (speedhttp.Reply, error) {
	if errors.Is(err, tenant.ErrInvalidTenant) {
		return speedhttp.EmptyReply(http.StatusNotFound), nil
	}
	return nil, p.failureOf(op, err)
}

func (p *product) refuse(c *gin.Context) {
	speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusBadRequest, Type: typePrefix + badRequestCode})
}

func badRequest() error {
	return speedhttp.Fault{Status: http.StatusBadRequest, Type: typePrefix + badRequestCode}
}
