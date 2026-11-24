package gate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	nethttp "net/http"
	"runtime/debug"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/identity"
	"github.com/vislake/speed/pkg/authn/internal/verdict"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
)

const authHeader = "Authorization"

const challengeHeader = "WWW-Authenticate"

// varyValue is what every response on a guarded route varies by.
const varyValue = "Authorization, Cookie"

// retryAfterBusy is the Retry-After a 503 carries when a backend was busy.
const retryAfterBusy = "1"

// errStopped and errSaturated are refusals made before a backend is reached;
// errAbandoned is the request's own context ending, charged to the gateway and
// logged at Info because no backend failed.
var (
	errStopped   = errors.New("the gateway has stopped accepting backend calls")
	errSaturated = errors.New("the in-flight gate is full")
	errAbandoned = errors.New("the request's own context ended before the backend answered")
)

type refusal struct {
	status int
	// bucket is which of the counters this conclusion lands in.
	bucket int
	// reason is the log line.
	reason string
	// level is the severity the log line is written at: Info for a refusal or
	// an abandoned request, Warn for an outage, Error for a defect.
	level slog.Level
	// backend is the name this is charged to, empty when the gateway
	// produced the refusal itself.
	backend string
	// attrs are extra log attributes, in slog's alternating form.
	attrs []any
	// refused is the backend whose credential this 401 refuses, which the
	// challenge is written for, or nil when the request reached none.
	refused *backend
	// clear says the backend refused a credential that came in a cookie, so the
	// cookie is to be cleared in this response.
	clear bool
	// retryAfter is the Retry-After this response carries, empty for none.
	retryAfter string
}

func gatewayRefusal(reason string, attrs ...any) *refusal {
	return &refusal{
		status: nethttp.StatusUnauthorized,
		bucket: int(verdict.Denied),
		reason: reason,
		level:  slog.LevelInfo,
		attrs:  attrs,
	}
}

// outage builds a 503 charged by cause: a backend failure goes to the backend
// (a panic at Error with its stack), the gateway's own stop, saturation and a
// caller-ended request go here at Info, and only a busy backend carries Retry-After.
func outage(backend, reason string, cause error, attrs ...any) *refusal {
	if errors.Is(cause, errStopped) || errors.Is(cause, errSaturated) ||
		errors.Is(cause, errAbandoned) {
		backend = ""
	}
	level := slog.LevelWarn
	if errors.Is(cause, errAbandoned) {
		level = slog.LevelInfo
	}
	if cause != nil {
		attrs = append(attrs, slog.String("cause", cause.Error()))
	}
	var panicked *backendPanic
	if errors.As(cause, &panicked) {
		level = slog.LevelError
		attrs = append(attrs, slog.String("stack", string(panicked.stack)))
	}
	r := &refusal{
		status:  nethttp.StatusServiceUnavailable,
		bucket:  int(verdict.Unavailable),
		reason:  reason,
		level:   level,
		backend: backend,
		attrs:   attrs,
	}
	if errors.Is(cause, authn.ErrBusy) {
		r.retryAfter = retryAfterBusy
	}
	return r
}

// backendPanic is a panic caught at a backend call site. It travels as an
// error, the way every other failure to answer does, and it keeps the stack:
// that is what finds the defect, and it is gone once the recover has returned.
type backendPanic struct {
	message string
	stack   []byte
}

func (p *backendPanic) Error() string { return p.message }

// caught turns a recovered value into the failure the call site reports. It is
// called from inside the deferred recover, which is the last point at which the
// panicking goroutine's stack can still be read.
func caught(format string, args ...any) *backendPanic {
	return &backendPanic{message: fmt.Sprintf(format, args...), stack: debug.Stack()}
}

// middlewareFor builds the layer registered on one endpoint. It provides
// (*authn.Gate)(nil) in the identifying stage, the anchor a layer uses to place
// itself inside or outside authentication.
func (g *gateway) middlewareFor(endpoint string) speedhttp.Middleware {
	if g.table == nil {
		panic(ModuleName + ": a layer was built for endpoint " + endpoint + " before the " +
			"dispatch table. The table decides what the layer answers a refusal with, so " +
			"Init has to build it before it registers anything")
	}
	// The challenge names this endpoint as its realm, so it is built once
	// per endpoint here rather than once per gateway.
	challenge := newChallenge(g.table, endpoint)
	return speedhttp.Middleware{
		Name:     ModuleName,
		Provides: []core.Token{(*authn.Gate)(nil)},
		Stage:    speedhttp.StageIdentify,
		Handler: func(c *gin.Context) {
			if g.admit(endpoint, challenge, c) {
				c.Next()
			}
		},
	}
}

// admit runs the authentication decision for one request and reports whether it
// passes. The recover inside covers only this module's own defects; a panic in a
// layer or handler behind this one is left to the entry point's fallback.
func (g *gateway) admit(endpoint string, ch *challenge, c *gin.Context) (passed bool) {
	defer func() {
		if rec := recover(); rec != nil {
			// A gateway defect: counted in no bucket, because the
			// buckets count judgements about a credential.
			log.FromContext(c.Request.Context()).Error(ModuleName+": the gateway panicked while "+
				"deciding this request",
				slog.Any("panic", rec), slog.String("endpoint", endpoint),
				slog.String("stack", string(debug.Stack())))
			speedhttp.Fail(c, speedhttp.Problem{Status: nethttp.StatusInternalServerError})
			passed = false
		}
	}()

	// The registrations are read before the Authorization header, so no backend
	// is reached on an exempt route whatever the request carries.
	reg := g.accessOf(endpoint, c)
	if reg.exempt {
		c.Request = c.Request.WithContext(identity.WithAccess(c.Request.Context(), authn.AccessOpen))
		return true
	}
	level := authn.AccessRequired
	if reg.optional {
		level = authn.AccessOptional
	}
	c.Writer.Header().Add("Vary", varyValue)

	found, deny := g.credentialOf(c.Request)
	if deny == nil && found == nil {
		if reg.optional {
			g.anonymous(c, endpoint)
			c.Request = c.Request.WithContext(identity.WithAccess(c.Request.Context(), level))
			return true
		}
		deny = gatewayRefusal(ModuleName + ": the request carries no credential")
	}

	var id identity.Identity
	if deny == nil {
		id, deny = g.identify(c, endpoint, reg, found)
	}
	if deny != nil {
		g.conclude(c.Request, endpoint, deny)
		if deny.clear && found != nil && found.backend.clearer != nil {
			// The backend said the credential does not hold, so the browser
			// is told to stop sending it. Left in place, it would be sent on
			// every request and refused every time.
			found.backend.clearer.ClearCookie(c.Writer)
		}
		if deny.status == nethttp.StatusUnauthorized {
			// RFC 9110 requires a 401 to carry a challenge; the header
			// is the whole of what a caller can rely on, and it is set
			// before the entry point writes the response.
			c.Header(challengeHeader, ch.valueFor(deny.refused))
		}
		if deny.retryAfter != "" {
			c.Header("Retry-After", deny.retryAfter)
		}
		// The problem carries the status alone. What the refusal was for,
		// and whose it was, reaches the log and no further.
		speedhttp.Fail(c, speedhttp.Problem{Status: deny.status})
		return false
	}

	g.counts[verdict.Authenticated].Add(1)
	ctx := identity.WithAccess(identity.With(c.Request.Context(), id), level)
	ctx = log.WithAttrs(ctx, identityAttrs(id)...)
	c.Request = c.Request.WithContext(ctx)
	return true
}

// identityAttrs are the correlation fields every record of a request carries:
// the account (an identifier, never a credential), the verifying backend, and
// when they apply the impersonation actor and the session.
func identityAttrs(id identity.Identity) []any {
	attrs := []any{
		slog.String("account", string(id.Account())),
		slog.String("backend", id.Backend()),
	}
	if id.Impersonating() {
		attrs = append(attrs, slog.String("actor", string(id.Actor())), slog.Bool("impersonating", true))
	}
	if session := id.Session(); session != "" {
		attrs = append(attrs, slog.String("session", string(session)))
	}
	return attrs
}

func (g *gateway) identify(c *gin.Context, endpoint string, reg access, found *claimed) (identity.Identity, *refusal) {
	b := found.backend
	r := c.Request

	// A credential the browser may attach itself is held to the cross-origin
	// check before any backend is asked; failing it is the gateway's own refusal
	// and the credential is not cleared, because nothing about it was judged.
	if found.attachedByBrowser() {
		if err := g.csrf.Check(r); err != nil {
			attrs := []any{slog.String("cause", err.Error())}
			carrier := found.cred.Scheme
			if found.fromCookie {
				carrier = "cookie"
				attrs = append([]any{slog.String("cookie_name", found.cred.Cookie)}, attrs...)
			}
			return identity.Identity{}, gatewayRefusal(ModuleName+": the request carries a "+carrier+
				" credential and fails the cross-origin check, so it is refused unread", attrs...)
		}
	}

	cred := found.cred
	cred.Peer = c.ClientIP()
	answer, err := g.verify(r.Context(), b, cred)
	if err != nil {
		return identity.Identity{}, outage(b.name,
			ModuleName+": the backend could not be asked about this credential", err)
	}

	switch verdict.OutcomeOf(answer) {
	case verdict.Authenticated:
		// Falls through to the exit checks below.
	case verdict.Denied:
		return identity.Identity{}, &refusal{
			status:  nethttp.StatusUnauthorized,
			bucket:  int(verdict.Denied),
			reason:  ModuleName + ": the backend refused this credential",
			level:   slog.LevelInfo,
			backend: b.name,
			attrs:   causeAttrs(answer),
			refused: b,
			clear:   found.fromCookie,
		}
	case verdict.Unavailable:
		return identity.Identity{}, outage(b.name,
			ModuleName+": the backend cannot answer about this credential right now",
			verdict.CauseOf(answer))
	default:
		// The zero verdict: counted as unspoken and written at Error,
		// because the backend returned without answering — a defect,
		// not a refusal of the credential.
		return identity.Identity{}, &refusal{
			status:  nethttp.StatusUnauthorized,
			bucket:  int(verdict.Unspoken),
			reason:  ModuleName + ": the backend returned without answering",
			level:   slog.LevelError,
			backend: b.name,
			refused: b,
		}
	}

	principal, reason := checkPrincipal(verdict.PrincipalOf(answer), g.clock.Now(), b, g.table)
	if reason != "" {
		deny := exitRefusal(ModuleName+": the principal the backend returned does not pass the "+
			"exit check, so the pass is not honoured", b, slog.String("reason", reason))
		return identity.Identity{}, deny
	}
	if principal.Actor != "" && !g.impersonationAdmitted(endpoint, reg) {
		// The pass holds but the route admits no impersonating identity: the
		// refusal is the gateway's judgement, charged to the backend whose pass
		// it declines, and written at Info as ordinary traffic.
		return identity.Identity{}, &refusal{
			status:  nethttp.StatusUnauthorized,
			bucket:  int(verdict.Denied),
			reason:  ModuleName + ": an impersonating identity reached a route that neither admits one nor lies on an endpoint the scope guard covers",
			level:   slog.LevelInfo,
			backend: b.name,
			refused: b,
			attrs:   []any{slog.String("cause", "impersonation-scope")},
		}
	}
	return identity.New(principal, b.name, challengeName(found)), nil
}

// impersonationAdmitted reports whether an impersonating identity may enter the
// route: it is registered Impersonable, or the endpoint is one the scope guard
// covers.
func (g *gateway) impersonationAdmitted(endpoint string, reg access) bool {
	if reg.impersonable {
		return true
	}
	return g.scope != nil && g.scope.Covers(endpoint)
}

// exitRefusal builds the refusal for a pass that failed the exit checks,
// charged to the backend and written at Error, since it said something it had
// no authority to say.
func exitRefusal(reason string, b *backend, attrs ...any) *refusal {
	deny := gatewayRefusal(reason, append([]any{slog.String("backend", b.name)}, attrs...)...)
	deny.level = slog.LevelError
	deny.refused = b
	return deny
}

// challengeName is the scheme name a re-authentication challenge for this
// credential is written with: the scheme it arrived under, in its conventional
// capitalisation, or the cookie scheme for a credential that came in a cookie.
func challengeName(found *claimed) string {
	if found.fromCookie {
		return cookieChallengeScheme
	}
	return canonicalScheme(found.cred.Scheme)
}

// causeAttrs renders what a verdict carried for the log line. The cause reaches
// the log and no further, so a backend cannot turn a wrong password into an
// outage by what it puts there.
func causeAttrs(v authn.Verdict) []any {
	if cause := verdict.CauseOf(v); cause != nil {
		return []any{slog.String("cause", cause.Error())}
	}
	return nil
}

// conclude counts the conclusion and writes the log line carrying the
// attribution, at the refusal's own level; attribution lives in the log alone,
// never in the response.
func (g *gateway) conclude(r *nethttp.Request, endpoint string, deny *refusal) {
	g.counts[deny.bucket].Add(1)

	attributed := deny.backend
	if attributed == "" {
		attributed = ModuleName
	}
	attrs := append([]any{
		slog.String("endpoint", endpoint),
		slog.Int("status", deny.status),
		slog.String("outcome", bucketName(deny.bucket)),
		slog.String("attributed_to", attributed),
	}, deny.attrs...)
	log.FromContext(r.Context()).Log(r.Context(), deny.level, deny.reason, attrs...)
}

func (g *gateway) anonymous(c *gin.Context, endpoint string) {
	g.counts[bucketAnonymous].Add(1)
	r := c.Request
	log.FromContext(r.Context()).Info(ModuleName+": the request carries no credential and the "+
		"route accepts that, so it passes with no identity",
		slog.String("endpoint", endpoint),
		slog.String("outcome", bucketName(bucketAnonymous)))
}

type claimed struct {
	cred       authn.Credential
	backend    *backend
	fromCookie bool
}

// scriptWrittenScheme is the one Authorization scheme a browser never attaches
// by itself, because a page's own script writes it and the same-origin policy
// keeps another site out; every other scheme is held to the cross-origin check.
const scriptWrittenScheme = "bearer"

// attachedByBrowser reports whether the browser may send this credential on a
// request the user did not mean to make: a cookie, and a header under any scheme
// but the one only a script writes.
func (c *claimed) attachedByBrowser() bool {
	return c.fromCookie || c.cred.Scheme != scriptWrittenScheme
}

// credentialOf reads the credential out of the request: the Authorization header
// first and alone, otherwise the cookies a backend takes. More than one header,
// or more than one claimed cookie, is refused because the caller's intent cannot be told.
func (g *gateway) credentialOf(r *nethttp.Request) (*claimed, *refusal) {
	values := r.Header.Values(authHeader)
	switch {
	case len(values) > 1:
		return nil, gatewayRefusal(ModuleName+": the request carries more than one "+
			authHeader+" header, and which one is meant cannot be told",
			slog.Int("headers", len(values)))
	case len(values) == 1:
		field := strings.TrimSpace(values[0])
		scheme, value, separated := strings.Cut(field, " ")
		if scheme == "" {
			return nil, gatewayRefusal(ModuleName + ": the " + authHeader + " header is empty or " +
				"names no scheme")
		}
		// Folding the scheme here is the contract with the backend: scheme
		// names are case-insensitive on the wire, so a backend never
		// compares raw casing.
		folded := strings.ToLower(scheme)
		b, taken := g.table.byScheme[folded]
		if !taken {
			// The scheme position is not logged: a caller may have put
			// the credential itself there, so the record carries only
			// its shape.
			return nil, gatewayRefusal(ModuleName+": no backend takes this credential's "+
				"authentication scheme", slog.Int("scheme_length", len(scheme)),
				slog.Bool("scheme_separated", separated))
		}
		return &claimed{
			cred:    authn.Credential{Scheme: folded, Value: strings.TrimSpace(value)},
			backend: b,
		}, nil
	}

	if len(g.table.byCookie) == 0 {
		return nil, nil
	}
	var found *claimed
	for _, cookie := range r.Cookies() {
		b, taken := g.table.byCookie[cookie.Name]
		if !taken {
			continue
		}
		if found != nil {
			return nil, gatewayRefusal(ModuleName+": the request carries more than one cookie a "+
				"backend takes, and which one is meant cannot be told",
				slog.String("cookie_name", cookie.Name))
		}
		found = &claimed{
			cred:       authn.Credential{Value: cookie.Value, Cookie: cookie.Name},
			backend:    b,
			fromCookie: true,
		}
	}
	return found, nil
}

func (g *gateway) verify(ctx context.Context, b *backend, cred authn.Credential) (
	answer authn.Verdict, err error,
) {
	made, err := g.enter(ctx)
	if err != nil {
		return authn.Verdict{}, err
	}
	defer made.release()
	defer func() {
		if rec := recover(); rec != nil {
			// The panic is caught at the call site, so the process
			// survives and every other backend in it goes on answering.
			err = caught("backend %q panicked while verifying a credential: %v", b.name, rec)
		}
	}()

	answer = b.verifier.Verify(made.ctx, cred)
	if made.overran() {
		return authn.Verdict{}, fmt.Errorf("backend %q did not answer within the call budget of %s",
			b.name, g.settings.budget)
	}
	// Only an Unavailable answer is read against the request's context; a
	// pass, a refusal or the zero verdict stands however late the caller's
	// deadline was.
	if verdict.OutcomeOf(answer) == verdict.Unavailable {
		if abandoned := made.abandoned(); abandoned != nil {
			return authn.Verdict{}, abandoned
		}
	}
	return answer, nil
}

// enter takes an in-flight slot and puts the call under the budget, returning
// the release that gives the slot back. The slot is taken without waiting, and
// a request whose own context has already ended is not let in.
func (g *gateway) enter(ctx context.Context) (call, error) {
	if ended := ctx.Err(); ended != nil {
		return call{}, fmt.Errorf("%w: %w", errAbandoned, ended)
	}
	g.callsMu.Lock()
	if g.stopped {
		g.callsMu.Unlock()
		return call{}, errStopped
	}
	select {
	case g.slots <- struct{}{}:
	default:
		g.callsMu.Unlock()
		return call{}, errSaturated
	}
	// Counted under the same lock that Stop flips the flag under, so a call
	// can never join after Stop has returned and Close has begun waiting.
	g.inFlight.Add(1)
	g.callsMu.Unlock()

	// Settle whether the deadline is this module's before WithTimeout folds the
	// two: it keeps the earlier, so a caller with less time left must not be
	// blamed on the backend.
	outer, inherited := ctx.Deadline()
	callCtx, cancel := context.WithTimeout(ctx, g.settings.budget)
	ours := true
	if inner, ok := callCtx.Deadline(); inherited && ok && outer.Equal(inner) {
		ours = false
	}
	return call{
		ctx:     callCtx,
		request: ctx,
		ours:    ours,
		release: func() {
			cancel()
			<-g.slots
			g.inFlight.Done()
		},
	}, nil
}

// call is one admitted backend call: its context, the request context it came
// from, whether the deadline is this module's budget, and the slot release.
type call struct {
	ctx     context.Context
	request context.Context
	ours    bool
	release func()
}

// overran reports whether this call ran out this module's own budget; a deadline
// the request arrived with, or a client that went away, does not count.
func (c call) overran() bool {
	return c.ours && errors.Is(c.ctx.Err(), context.DeadlineExceeded)
}

// abandoned reports the request's own context ending inside the backend, rather
// than this module's budget: the backend's failure to answer is the caller's
// doing, not its own.
func (c call) abandoned() error {
	if c.overran() {
		return nil
	}
	if ended := c.request.Err(); ended != nil {
		return fmt.Errorf("%w: %w", errAbandoned, ended)
	}
	return nil
}
