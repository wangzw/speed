package admin

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/sms"
)

const (
	typePrefix         = "urn:speed:sms:"
	badRequestCode     = "bad-request"
	conflictCode       = "conflict"
	configTemplateCode = "config-template"
)

func (p *product) caller(c *gin.Context) (authn.Identity, bool) {
	id, ok := authn.FromContext(c.Request.Context())
	if !ok {
		writeNoIdentity401(c, realmOf(c))
		return authn.Identity{}, false
	}
	return id, true
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

// quoteRealm escapes a realm with the rule the authorization layer writes, kept
// byte-identical here because the two 401 shapes must agree.
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

// The permission's domain is the platform domain, named explicitly so a request tenant never steers a platform decision.
func (p *product) require(c *gin.Context, perm authz.Permission) bool {
	return p.guard.RequirePermissionIn(c, perm, authz.PlatformDomain)
}

// permitted wraps a handler with the caller read and the guard check, before the
// entry point binds the input.
func (p *product) permitted(perm authz.Permission, handler gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := p.caller(c); !ok {
			return
		}
		if !p.require(c, perm) {
			return
		}
		handler(c)
	}
}

// auditRequired writes the mandatory audit row outside any transaction and before
// the action; a row that does not land answers 503 and the action does not run.
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

func (p *product) answer(op string, err error, attrs ...any) (speedhttp.Reply, error) {
	switch {
	case errors.Is(err, sms.ErrImmutable):
		return nil, speedhttp.Fault{Status: http.StatusConflict, Type: typePrefix + configTemplateCode}
	case errors.Is(err, sms.ErrNoTemplate):
		return speedhttp.EmptyReply(http.StatusNotFound), nil
	case errors.Is(err, sms.ErrTemplateExists):
		return nil, speedhttp.Fault{Status: http.StatusConflict, Type: typePrefix + conflictCode}
	case errors.Is(err, sms.ErrInvalidConfig), errors.Is(err, sms.ErrRejected):
		return nil, speedhttp.Fault{Status: http.StatusBadRequest, Type: typePrefix + badRequestCode}
	default:
		fields := append([]any{slog.String("cause", err.Error())}, attrs...)
		p.logger.Error(ModuleName+": "+op+" failed", fields...)
		return nil, speedhttp.Fault{Status: http.StatusServiceUnavailable}
	}
}
