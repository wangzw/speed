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
	"github.com/google/uuid"

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
	protectedCode  = "protected"
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

// quoteRealm escapes what goes between the quotes of a realm; it is kept
// byte-identical with the authorization layer's copy.
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

func (p *product) guardOwnerRoute(method string, next gin.HandlerFunc) gin.HandlerFunc {
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
		tenantID, ok := tenant.FromContext(c.Request.Context())
		if !ok {
			speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusNotFound})
			return
		}
		got, found, err := p.catalog.Get(c.Request.Context(), tenantID)
		if err != nil {
			p.logger.Error(ModuleName+": the tenant could not be read, so the write was not carried out",
				slog.String("cause", err.Error()))
			speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusServiceUnavailable})
			return
		}
		if !found {
			speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusNotFound})
			return
		}
		if got.OwnerAccount != "" && got.OwnerAccount == id.Actor().String() {
			next(c)
			return
		}
		speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusForbidden})
	}
}

func (p *product) guardSelfRoute(verify bool, next gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := authn.FromContext(c.Request.Context())
		if !ok {
			writeNoIdentity401(c, realmOf(c))
			return
		}
		if verify && !p.requireVerifiedAccount(c, id) {
			return
		}
		next(c)
	}
}

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

func prescreenJSON[T any](p *product, next gin.HandlerFunc) gin.HandlerFunc {
	return prescreenJSONFields[T](p, nil, nil, next)
}

func prescreenJSONFields[T any](p *product, required, forbidden []string, next gin.HandlerFunc) gin.HandlerFunc {
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
		for _, key := range required {
			if _, present := object[key]; !present {
				p.refuse(c)
				return
			}
		}
		for _, key := range forbidden {
			if _, present := object[key]; present {
				p.refuse(c)
				return
			}
		}
		var probe T
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
	case errors.Is(err, tenant.ErrTenantExists), errors.Is(err, tenant.ErrNameConflict),
		errors.Is(err, tenant.ErrTenantDeleted), errors.Is(err, tenant.ErrOwnerNotMember),
		errors.Is(err, tenant.ErrOwnerInactive), errors.Is(err, tenant.ErrOwnerChanged),
		errors.Is(err, tenant.ErrNoOwner), errors.Is(err, tenant.ErrTransferAlreadyPending),
		errors.Is(err, tenant.ErrTransferNotPending), errors.Is(err, tenant.ErrTransferExpired):
		return speedhttp.Fault{Status: http.StatusConflict, Type: typePrefix + conflictCode}
	case errors.Is(err, tenant.ErrTransferTargetIsOwner):
		return speedhttp.Fault{Status: http.StatusBadRequest, Type: typePrefix + badRequestCode}
	case errors.Is(err, tenant.ErrInvalidTenant), errors.Is(err, tenant.ErrTransferNotFound),
		errors.Is(err, tenant.ErrTransferNotTarget):
		return speedhttp.Fault{Status: http.StatusNotFound}
	default:
		p.logger.Error(ModuleName+": "+op+" failed", slog.String("cause", err.Error()))
		return speedhttp.Fault{Status: http.StatusServiceUnavailable}
	}
}

func (p *product) replyFailure(op string, err error) (speedhttp.Reply, error) {
	if errors.Is(err, tenant.ErrInvalidTenant) || errors.Is(err, tenant.ErrTransferNotFound) ||
		errors.Is(err, tenant.ErrTransferNotTarget) {
		return speedhttp.EmptyReply(http.StatusNotFound), nil
	}
	return nil, p.failureOf(op, err)
}

func validRequestID(value string) bool {
	_, err := uuid.Parse(value)
	return err == nil
}

func (p *product) refuse(c *gin.Context) {
	speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusBadRequest, Type: typePrefix + badRequestCode})
}

func badRequest() error {
	return speedhttp.Fault{Status: http.StatusBadRequest, Type: typePrefix + badRequestCode}
}
