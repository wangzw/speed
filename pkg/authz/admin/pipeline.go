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

const (
	typePrefix     = "urn:speed:authz:"
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

// guardRoute puts the identity, registration-verification and permission decision in front of the handler, in that order.
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

func refuseBadRequest(c *gin.Context) {
	speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusBadRequest, Type: typePrefix + badRequestCode})
}

func badRequest() error {
	return speedhttp.Fault{Status: http.StatusBadRequest, Type: typePrefix + badRequestCode}
}

func conflict() error {
	return speedhttp.Fault{Status: http.StatusConflict, Type: typePrefix + conflictCode}
}

// auditRequired writes the mandatory audit row outside any transaction and before the action.
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

// requireWriteAllowed runs after the shape and escalation checks and before the mandatory audit and the manager.
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
