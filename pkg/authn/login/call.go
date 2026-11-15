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

// requestKey and writerKey carry the request and the response writer to the
// typed handler. A typed handler receives the request's context and no writer,
// and two things this module does are not expressible as declared input: the
// flow token travels in a cookie whose name depends on configuration and in the
// Authn-Flow header, both of which the fragment does not publish, and the
// session library writes its Set-Cookie through a response writer. The wrapper
// below runs inside the mounter's own wrapper, so the response header map the
// mounter leaves in the context stays there beside these values.
type (
	requestKey struct{}
	writerKey  struct{}
)

// withRequest leaves the request and the response writer on the context the
// typed handler receives.
func withRequest(h gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), requestKey{}, c.Request)
		ctx = context.WithValue(ctx, writerKey{}, c.Writer)
		c.Request = c.Request.WithContext(ctx)
		h(c)
	}
}

// requestOf is the request the handler is answering, or nil outside a mounted
// route.
func requestOf(ctx context.Context) *http.Request {
	req, _ := ctx.Value(requestKey{}).(*http.Request)
	return req
}

// writerOf is the response writer the handler writes through, or nil outside a
// mounted route.
func writerOf(ctx context.Context) http.ResponseWriter {
	w, _ := ctx.Value(writerKey{}).(http.ResponseWriter)
	return w
}

// call is one request's answer being built: the context, the request and
// writer, and the reply or refusal the handler has produced so far.
type call struct {
	ctx      context.Context
	req      *http.Request
	w        http.ResponseWriter
	clientIP string

	endpoint string

	reply   speedhttp.Reply
	err     error
	cookies []*http.Cookie

	// failureLanding is where a failed callback lands the user agent when the
	// flow it belongs to serves a purpose other than login: the target that flow
	// recorded, which is the page that started it. The empty string means the
	// configured callback failure landing.
	failureLanding string

	// failureEmail is the masked address a link-required refusal names, read off
	// the refusal before it is collapsed into a problem document, because that
	// collapse keeps the code and drops the cause. The empty string means the
	// failure names no address.
	failureEmail string

	// union is set on the answer route: its 200 body is the declared union of
	// the four answers a step can produce.
	union bool
}

// call builds the answer builder of one request.
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

// result is the answer the entry point writes: the fault an answer path
// produced, or the reply with every cookie the path set.
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

// json answers a body as JSON.
func (c *call) json(status int, body any) {
	c.reply = speedhttp.JSONReply(status, body)
}

// answer answers the answer route, whose declared body is the union of the four
// shapes a step produces.
func (c *call) answer(body any) {
	c.reply = speedhttp.JSONReply(http.StatusOK, AuthnLoginAnswer{value: body})
}

// empty answers a status with no body.
func (c *call) empty(status int) {
	c.reply = speedhttp.EmptyReply(status)
}

// redirect answers a status with a Location header and no body. The target is
// written the way net/http's Redirect writes it: the path cleaned and its
// non-ASCII bytes escaped.
func (c *call) redirect(status int, location string) {
	requestPath := ""
	if c.req != nil && c.req.URL != nil {
		requestPath = c.req.URL.Path
	}
	c.reply = speedhttp.RedirectReply(status, redirectTarget(location, requestPath))
}

// setCookie adds one cookie to the answer.
func (c *call) setCookie(cookie *http.Cookie) {
	c.cookies = append(c.cookies, cookie)
}

// serverError answers a defect the engine cannot pin on the client.
func (c *call) serverError() {
	c.err = speedhttp.Fault{Status: http.StatusInternalServerError}
}

// refuse writes the refusal the call holds, for a check that runs before the
// typed handler and so has no return to carry it to the entry point. It writes
// the fault's headers and the problem document, the answer a handler's refused
// fault would have got.
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

// clientInfo is where the request came from, for logs and audit.
func (c *call) clientInfo() authn.ClientInfo {
	agent := ""
	if c.req != nil {
		agent = c.req.UserAgent()
	}
	return authn.ClientInfo{IP: c.clientIP, UserAgent: agent}
}

// header reads one request header.
func (c *call) header(name string) string {
	if c.req == nil {
		return ""
	}
	return c.req.Header.Get(name)
}

// navigation reports whether the request is a top-level browser navigation,
// which only Sec-Fetch-Mode: navigate declares. An absent header is not a
// navigation: a client that does not send fetch metadata is answered like an API
// request.
func (c *call) navigation() bool {
	return strings.EqualFold(c.header("Sec-Fetch-Mode"), "navigate")
}

// cookieValue reads one request cookie's value, or "".
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

// The problem type prefix every refusal of this capability carries.
const typePrefix = "urn:speed:authn:"

// fault answers a problem code in the status the closed table gives it.
func (c *call) fail(code string) {
	status, ok := mount.Status(code)
	if !ok {
		panic("authn.login: " + code + " is not a problem code")
	}
	c.err = speedhttp.Fault{Status: status, Type: typePrefix + code}
}

// unauthorized answers a 401 with the capability's challenge.
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

// forbidden answers a 403 with a code.
func (c *call) forbidden(code string) {
	status, ok := mount.Status(code)
	if !ok || status != http.StatusForbidden {
		panic("authn.login: " + code + " is not a problem code answered with 403")
	}
	c.err = speedhttp.Fault{Status: status, Type: typePrefix + code}
}

// throttled answers a 429, carrying the retry delay in whole seconds when one
// is stated.
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

// unavailable answers a 503 that never says which dependency failed.
func (c *call) unavailable() {
	c.err = speedhttp.Fault{Status: http.StatusServiceUnavailable, Type: typePrefix + mount.CodeUnavailable}
}

// busy answers the 503 of a password derivation refused for capacity.
func (c *call) busy() {
	c.err = speedhttp.Fault{
		Status:  http.StatusServiceUnavailable,
		Type:    typePrefix + mount.CodeUnavailable,
		Headers: []speedhttp.HeaderValue{{Name: "Retry-After", Value: "1"}},
	}
}

// stepUp answers the 401 of an identity that does not meet a requirement.
func (c *call) stepUp(id authn.Identity, req authn.Requirement) {
	c.err = stepUpFault(id, req)
}
