package login

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	speedhttp "github.com/vislake/speed/pkg/http"
)

type (
	requestKey struct{}
	writerKey  struct{}
)

func withRequest(h gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), requestKey{}, c.Request)
		ctx = context.WithValue(ctx, writerKey{}, c.Writer)
		c.Request = c.Request.WithContext(ctx)
		h(c)
	}
}

func requestOf(ctx context.Context) *http.Request {
	req, _ := ctx.Value(requestKey{}).(*http.Request)
	return req
}

func writerOf(ctx context.Context) http.ResponseWriter {
	w, _ := ctx.Value(writerKey{}).(http.ResponseWriter)
	return w
}

type call struct {
	ctx      context.Context
	req      *http.Request
	w        http.ResponseWriter
	clientIP string

	endpoint string

	reply   speedhttp.Reply
	err     error
	cookies []*http.Cookie

	failureLanding string

	failureEmail string

	union bool
}

func (r *routes) call(ctx context.Context, meta *speedhttp.Meta) *call {
	c := &call{
		ctx:      ctx,
		req:      requestOf(ctx),
		w:        writerOf(ctx),
		endpoint: r.endpoint,
	}
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
	if len(c.cookies) > 0 {
		return speedhttp.WithCookies(c.reply, c.cookies...), nil
	}
	return c.reply, nil
}

func (c *call) json(status int, body any) {
	c.reply = speedhttp.JSONReply(status, body)
}

func (c *call) answer(body any) {
	c.reply = speedhttp.JSONReply(http.StatusOK, AuthnLoginAnswer{value: body})
}

func (c *call) empty(status int) {
	c.reply = speedhttp.EmptyReply(status)
}

func (c *call) redirect(status int, location string) {
	requestPath := ""
	if c.req != nil && c.req.URL != nil {
		requestPath = c.req.URL.Path
	}
	c.reply = speedhttp.RedirectReply(status, redirectTarget(location, requestPath))
}

func (c *call) setCookie(cookie *http.Cookie) {
	c.cookies = append(c.cookies, cookie)
}

func (c *call) serverError() {
	c.err = speedhttp.Fault{Status: http.StatusInternalServerError}
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

func (c *call) clientInfo() authn.ClientInfo {
	agent := ""
	if c.req != nil {
		agent = c.req.UserAgent()
	}
	return authn.ClientInfo{IP: c.clientIP, UserAgent: agent}
}

func (c *call) header(name string) string {
	if c.req == nil {
		return ""
	}
	return c.req.Header.Get(name)
}

func (c *call) navigation() bool {
	return strings.EqualFold(c.header("Sec-Fetch-Mode"), "navigate")
}

func (c *call) cookieValue(name string) string {
	if c.req == nil {
		return ""
	}
	cookie, err := c.req.Cookie(name)
	if err != nil {
		return ""
	}
	return cookie.Value
}

const typePrefix = "urn:speed:authn:"

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

func (c *call) busy() {
	c.err = speedhttp.Fault{
		Status:  http.StatusServiceUnavailable,
		Type:    typePrefix + mount.CodeUnavailable,
		Headers: []speedhttp.HeaderValue{{Name: "Retry-After", Value: "1"}},
	}
}

func (c *call) stepUp(id authn.Identity, req authn.Requirement) {
	c.err = stepUpFault(id, req)
}
