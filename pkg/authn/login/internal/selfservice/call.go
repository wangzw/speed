package selfservice

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/login/internal/stepup"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
)

type writerKey struct{}

func withWriter(h gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), writerKey{}, c.Writer)
		c.Request = c.Request.WithContext(ctx)
		h(c)
	}
}

func writerOf(ctx context.Context) http.ResponseWriter {
	w, _ := ctx.Value(writerKey{}).(http.ResponseWriter)
	return w
}

const typePrefix = "urn:speed:authn:"

type call struct {
	ctx      context.Context
	w        http.ResponseWriter
	endpoint string
	clientIP string

	reply speedhttp.Reply
	err   error
}

func (r *routes) call(ctx context.Context, meta *speedhttp.Meta) *call {
	c := &call{ctx: ctx, w: writerOf(ctx), endpoint: r.endpoint}
	if meta != nil {
		c.clientIP = meta.ClientIP
	}
	return c
}

func (c *call) result() (speedhttp.Reply, error) {
	if c.err != nil {
		return nil, c.err
	}
	if c.reply == nil {
		return nil, speedhttp.Fault{Status: http.StatusInternalServerError}
	}
	return c.reply, nil
}

func (c *call) json(status int, body any) {
	c.reply = speedhttp.JSONReply(status, body)
}

func (c *call) empty(status int) {
	c.reply = speedhttp.EmptyReply(status)
}

func (c *call) fail(code string) {
	status, ok := mount.Status(code)
	if !ok {
		panic("authn.login: " + code + " is not a problem code")
	}
	c.err = speedhttp.Fault{Status: status, Type: typePrefix + code}
}

func (c *call) unauthorized(code string) {
	fault := speedhttp.Fault{Status: http.StatusUnauthorized}
	if code != "" {
		fault.Type = typePrefix + code
	}
	fault.Headers = []speedhttp.HeaderValue{{
		Name:  "WWW-Authenticate",
		Value: mount.UnauthorizedChallenge(c.endpoint),
	}}
	c.err = fault
}

func (c *call) forbidden(code string) {
	status, ok := mount.Status(code)
	if !ok || status != http.StatusForbidden {
		panic("authn.login: " + code + " is not a problem code answered with 403")
	}
	c.err = speedhttp.Fault{Status: status, Type: typePrefix + code}
}

func (c *call) throttled(retryAfter time.Duration) {
	fault := speedhttp.Fault{Status: http.StatusTooManyRequests, Type: typePrefix + mount.CodeThrottled}
	if retryAfter > 0 {
		fault.Headers = []speedhttp.HeaderValue{{
			Name:  "Retry-After",
			Value: strconv.FormatInt(int64((retryAfter+time.Second-1)/time.Second), 10),
		}}
	}
	c.err = fault
}

func (c *call) unavailable() {
	c.err = speedhttp.Fault{Status: http.StatusServiceUnavailable, Type: typePrefix + mount.CodeUnavailable}
}

func (c *call) refuse(g *gin.Context) {
	var fault speedhttp.Fault
	if !errors.As(c.err, &fault) {
		speedhttp.Fail(g, speedhttp.Problem{Status: http.StatusInternalServerError})
		return
	}
	for _, header := range fault.Headers {
		g.Header(header.Name, header.Value)
	}
	speedhttp.Fail(g, fault.Problem())
}

func (c *call) busy() {
	c.err = speedhttp.Fault{
		Status:  http.StatusServiceUnavailable,
		Type:    typePrefix + mount.CodeUnavailable,
		Headers: []speedhttp.HeaderValue{{Name: "Retry-After", Value: "1"}},
	}
}

func (c *call) stepUp(id authn.Identity, req authn.Requirement) {
	c.err = speedhttp.Fault{
		Status: http.StatusUnauthorized,
		Type:   stepup.Type(),
		Headers: []speedhttp.HeaderValue{{
			Name:  "WWW-Authenticate",
			Value: stepup.Challenge(id, req),
		}},
	}
}

func (r *routes) fail(c *call, err error) {
	var throttled *authn.ThrottledError
	switch {
	case errors.As(err, &throttled):
		c.throttled(throttled.RetryAfter)
	case errors.Is(err, authn.ErrThrottled):
		c.throttled(0)
	case authn.IsUsernameShape(err):
		c.fail(mount.CodeUsernameInvalid)
	case errors.Is(err, authn.ErrPasswordPolicy), errors.Is(err, authn.ErrInvalidIdentifier),
		errors.Is(err, authn.ErrInvalidArgument), errors.Is(err, authn.ErrHumanProofTooLong):
		c.fail(mount.CodeBadRequest)
	case errors.Is(err, authn.ErrUsernameReserved):
		c.fail(mount.CodeUsernameReserved)
	case errors.Is(err, authn.ErrIdentifierRequired):
		c.fail(mount.CodeLoginIdentifierRequired)
	case errors.Is(err, authn.ErrUsernameImmutable):
		c.fail(mount.CodeUsernameImmutable)
	case errors.Is(err, authn.ErrContactTaken):
		c.fail(mount.CodeEmailTaken)
	case errors.Is(err, authn.ErrAccountExists), errors.Is(err, authn.ErrBindingConflict),
		errors.Is(err, authn.ErrLastFactor),
		errors.Is(err, authn.ErrFactorLimit):
		c.fail(mount.CodeConflict)
	case errors.Is(err, authn.ErrLastLoginMethod):
		c.fail(mount.CodeLastLoginMethod)
	case errors.Is(err, authn.ErrUnsupportedRegion):
		c.fail(mount.CodeUnsupportedRegion)
	case errors.Is(err, authn.ErrInvalidCredentials):
		c.unauthorized(mount.CodeLoginFailed)
	case errors.Is(err, authn.ErrHumanVerificationRequired):
		c.forbidden(mount.CodeHumanVerificationRequired)
	case errors.Is(err, authn.ErrAccountNotFound), errors.Is(err, authn.ErrSessionNotFound),
		errors.Is(err, authn.ErrSessionRevoked):
		c.unauthorized("")
	case errors.Is(err, authn.ErrBusy):
		c.busy()
	case errors.Is(err, authn.ErrUnavailable):
		logWarn(c.ctx, "a dependency of an account self-service route is unavailable", err)
		c.unavailable()
	default:
		logError(c.ctx, "an account self-service route failed on a call it cannot answer for", err)
		c.unavailable()
	}
}

func logWarn(ctx context.Context, msg string, err error) {
	attrs := []any{}
	if err != nil {
		attrs = append(attrs, slog.String("cause", err.Error()))
	}
	log.FromContext(ctx).Warn(moduleName+": "+msg, attrs...)
}
