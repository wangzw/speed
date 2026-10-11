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

// authHeader is the request header a credential arrives in.
const authHeader = "Authorization"

// challengeHeader is the response header a 401 names the acceptable
// authentication schemes in.
const challengeHeader = "WWW-Authenticate"

// varyValue is what every response on a guarded route varies by. A cache that
// stored the answer to one caller's credential and served it to another would
// hand over what the first was let see, so a response is keyed by both carriers.
const varyValue = "Authorization, Cookie"

// retryAfterBusy is the Retry-After a 503 carries when the backend refused to
// derive a password because its capacity was in use. It is the one 503 that is a
// transient condition of this process and says so: every other cause of a 503
// carries no promise about when to come back.
const retryAfterBusy = "1"

// errStopped and errSaturated are the two refusals the gateway makes before a
// backend is reached at all. Both end the request as an outage: the module
// cannot answer right now, and neither says anything about the credential.
//
// errAbandoned is the request's own context ending — the deadline it arrived
// with, or the caller going away — either before a backend was asked or while
// one was being asked, before this module's own budget ran out. It ends the
// request as an outage too, and nobody failed: a backend that returns once its
// context ends is doing what it is required to do, and what it reports then is
// that it could not answer, which says nothing about its health.
var (
	errStopped   = errors.New("the gateway has stopped accepting backend calls")
	errSaturated = errors.New("the in-flight gate is full")
	errAbandoned = errors.New("the request's own context ended before the backend answered")
)

// refusal is a conclusion reached without a backend verdict, or one derived
// from a backend's failure to produce one.
type refusal struct {
	status int
	// bucket is which of the counters this conclusion lands in.
	bucket int
	// reason is the log line.
	reason string
	// level is what the log line is written at, and it follows who has
	// something to do about it. A refusal is ordinary traffic and is written
	// at Info, and so is a request its own caller ended; an outage is an
	// operator's to look at and is written at Warn; a backend that returned
	// without answering, that panicked, or that passed a principal outside what
	// it may is a defect somebody has to fix and is written at Error. The
	// level is the first filter every log pipeline applies, so a signal
	// written below the one a deployment keeps is a signal nobody receives.
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

// gatewayRefusal builds a refusal the gateway produced itself: a 401 charged to
// nobody but this module.
func gatewayRefusal(reason string, attrs ...any) *refusal {
	return &refusal{
		status: nethttp.StatusUnauthorized,
		bucket: int(verdict.Denied),
		reason: reason,
		level:  slog.LevelInfo,
		attrs:  attrs,
	}
}

// outage builds a 503, charged to whoever could not answer.
//
// The cause decides that. A backend that reported a failure, timed out or
// panicked is charged; the gateway having stopped taking calls, or its own
// in-flight gate being full, is this module's own limit and is charged here —
// the backend was never asked, and an operator chasing a 503 against a backend
// that is perfectly healthy would be chasing the wrong thing. The request's own
// context ending is charged here as well, for the same reason: no backend
// failed.
//
// The cause decides the level too. An outage is written at Warn; a panic is a
// defect rather than an outage, is written at Error, and carries the stack it
// was raised on; a request its own caller brought to an end is ordinary
// traffic and is written at Info. And the cause decides the one header a 503
// may carry: a backend that says its capacity is in use is a condition that
// passes in a moment, so the response says to ask again in one second, and no
// other cause of a 503 says anything of the kind.
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

// middlewareFor builds the layer registered on one endpoint.
//
// Provides carries the same token the module descriptor delivers the capability
// under. That is the only anchor another layer has for placing itself inside or
// outside authentication, which makes it this module's stable contract surface
// on the chain.
//
// It sits in the identifying stage, where authentication is the layer that
// settles the caller. A layer that wants to sit inside or outside it within the
// stage declares After or Before pointing at (*authn.Gate)(nil).
func (g *gateway) middlewareFor(endpoint string) speedhttp.Middleware {
	// The layer is built out of the dispatch table, so the table has to exist
	// by now. A nil one is a mistake in this package's own ordering rather than
	// anything a host did, and saying so here beats the nil dereference it
	// would otherwise become several frames away.
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

// admit runs the whole authentication decision for one request and reports
// whether it passes. When it does not, the response has already been written
// through the entry point and the chain is ended.
//
// The call to the next layer is deliberately outside this function. The recover
// below is for this module's own defects; a panic in a layer or a handler behind
// this one belongs to the entry point's fallback, and catching it here would
// answer for code this module does not own.
func (g *gateway) admit(endpoint string, ch *challenge, c *gin.Context) (passed bool) {
	defer func() {
		if rec := recover(); rec != nil {
			// The gateway's own defect. It is counted in none of the
			// buckets: those count judgements about a credential, and this
			// is not one.
			//
			// The entry point's fallback would answer a panic here with
			// a 500 as well, and could not say which layer it was
			// raised in. Recovering is what puts this one on this
			// module's name, with the endpoint and the stack.
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
	// From here the response depends on what the request carries.
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
			// A 401 carrying no challenge tells a caller it needs a
			// credential without saying what kind, which is the one
			// thing RFC 9110 does not allow a 401 to do. The entry point
			// decides the shape of the body, and it is not this module's
			// to carry the answer in, so the header is the whole of what
			// a caller can rely on. It is set before the response is
			// written, and the entry point keeps it.
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

// identityAttrs are the correlation fields every record of a request carries
// once the gateway has stamped an identity on it: the account, the backend that
// verified it and, when they apply, the actor of an impersonation and the
// session. The account is an identifier and never a credential.
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

// identify asks the backend that took the credential, then holds what it says
// to the exit checks and the impersonation rule, and produces either the
// identity to stamp or the refusal to conclude with.
func (g *gateway) identify(c *gin.Context, endpoint string, reg access, found *claimed) (identity.Identity, *refusal) {
	b := found.backend
	r := c.Request

	// The unsafe request of a credential the browser may attach by itself is held
	// to the cross-origin check before any backend is asked: a request a page of
	// another origin made with the browser's own cookie or a credential it
	// stored after a challenge is the forgery the check exists to refuse. It is
	// the gateway's own refusal, and the credential is not cleared, because
	// nothing about it was judged.
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
		// The zero verdict. It ends the request the way a refusal does
		// and is counted apart from one: a refusal says the credential
		// does not hold, this says the backend returned without
		// answering, and an operator needs to tell a wrong password
		// from a broken plugin. It is written at Error for the same
		// reason: it is a defect in the backend's code, and a line
		// written at Info is one a production pipeline drops.
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
		// The credential holds and the principal is well formed, and the route
		// is still not one an impersonating identity may reach: it is neither
		// registered for it nor on an endpoint whose tenant range the
		// admission layer checks. The refusal is the gateway's judgement and
		// is charged to the backend whose pass it declines, and it is
		// ordinary traffic, so it is written at Info.
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

// impersonationAdmitted reports whether an impersonating identity may be let
// into the route: the route is registered Impersonable, or the endpoint is one
// the scope guard covers. With no scope guard in the assembly only the first can
// hold.
func (g *gateway) impersonationAdmitted(endpoint string, reg access) bool {
	if reg.impersonable {
		return true
	}
	return g.scope != nil && g.scope.Covers(endpoint)
}

// exitRefusal builds the refusal for a pass that did not survive the exit
// checks. It is the gateway's own judgement, so it is charged to the gateway,
// and it is written at Error: whoever is charged, a backend said something it
// had no authority to say.
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

// causeAttrs renders what a verdict carried, for the log line. The cause
// decides nothing — it reaches the log and no further — which is what stops a
// backend from turning a wrong password into an outage by choosing what to put
// in it.
func causeAttrs(v authn.Verdict) []any {
	if cause := verdict.CauseOf(v); cause != nil {
		return []any{slog.String("cause", cause.Error())}
	}
	return nil
}

// conclude counts the conclusion and writes the log line that carries the
// attribution, at the level the refusal was built with. The response carries a
// status code and the headers that go with it and nothing of why, so attribution
// lives in the log alone.
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

// anonymous counts a request that carried no credential and passed on an
// optional route, and writes the line that says so.
func (g *gateway) anonymous(c *gin.Context, endpoint string) {
	g.counts[bucketAnonymous].Add(1)
	r := c.Request
	log.FromContext(r.Context()).Info(ModuleName+": the request carries no credential and the "+
		"route accepts that, so it passes with no identity",
		slog.String("endpoint", endpoint),
		slog.String("outcome", bucketName(bucketAnonymous)))
}

// claimed is a credential taken off a request together with the backend that
// takes it.
type claimed struct {
	cred       authn.Credential
	backend    *backend
	fromCookie bool
}

// scriptWrittenScheme is the one Authorization scheme the gateway knows a browser
// never attaches by itself: a Bearer header is written by the page's own script,
// which the same-origin policy keeps from a page of another site. It is named
// because the gateway reads nothing of a credential and so cannot tell whether a
// scheme it was told about is one a browser stores after a challenge and sends
// again on its own, as it does Basic, Digest, Negotiate and NTLM. Every other
// scheme is held to the cross-origin check, so a scheme nobody listed fails
// closed and not open.
const scriptWrittenScheme = "bearer"

// attachedByBrowser reports whether the browser may send this credential on a
// request the user did not mean to make: a cookie, and a header under any scheme
// but the one only a script writes.
func (c *claimed) attachedByBrowser() bool {
	return c.fromCookie || c.cred.Scheme != scriptWrittenScheme
}

// credentialOf reads the credential out of the request. A nil credential with no
// refusal is a request that carries none.
//
// The Authorization header goes first and goes alone: when one is there the
// cookies are not read, and a header that does not hold is not made up for by a
// cookie that might, because two carriers judged separately would be two ways
// for a caller to get through. More than one Authorization header is refused
// rather than resolved by picking one: which of them the caller meant cannot be
// known, and choosing would let a caller who can add a header decide which
// credential is judged.
//
// Without the header, the cookies a backend takes are the source. Two of them on
// one request, the same name twice or two names two backends take, are refused
// for the same reason: which was meant cannot be known, and a page that can set
// a cookie for this site must not be the one to choose. A cookie no backend
// takes is not a credential at all.
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
		// The scheme is folded here, once, because that is what the surface
		// promises a backend: authentication scheme names are case-insensitive
		// on the wire, and a backend left to compare the raw token would
		// authenticate or refuse on the casing a client happened to send.
		folded := strings.ToLower(scheme)
		b, taken := g.table.byScheme[folded]
		if !taken {
			// What the client wrote in the scheme position is not written
			// down. A caller who left the scheme out puts the credential
			// itself there, and a masking rule cannot be relied on to catch
			// an arbitrary secret, so the record carries the shape of what
			// arrived and none of its text.
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

// verify puts one credential to one backend, under the call budget, the
// in-flight gate and a recover.
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
	// Only a report of not being able to answer is read against the
	// request's context. A pass or a refusal is an answer about the
	// credential and the zero verdict is a defect of the backend's own, and
	// each stands however late the caller's own deadline was.
	if verdict.OutcomeOf(answer) == verdict.Unavailable {
		if abandoned := made.abandoned(); abandoned != nil {
			return authn.Verdict{}, abandoned
		}
	}
	return answer, nil
}

// enter takes an in-flight slot and puts the call under the budget. The
// returned release gives the slot back and cancels the budget.
//
// The slot is taken without waiting: past the gate a request ends as an outage
// straight away rather than queueing, so that a backend that stopped answering
// cannot take every goroutine the server has.
//
// A request whose own context has already ended is not let in at all. Nobody is
// waiting for its answer, and asking a backend anyway would spend a
// verification and a slot on it.
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

	// Whether the deadline on the call is this module's own is settled here,
	// before the child context folds the two together. WithTimeout keeps the
	// earlier of the two, so a caller that arrived with less time left than
	// the budget produces a deadline that is the caller's, not the backend's
	// — and a 503 blamed on a backend that answered perfectly well would
	// send an operator to read the logs of a credential store that is fine.
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

// call is one admitted call into a backend: the context it runs under, the
// request's own context it was derived from, whether the deadline on the call's
// context is this module's budget or one the request arrived with, and the
// release that gives the in-flight slot back.
type call struct {
	ctx     context.Context
	request context.Context
	ours    bool
	release func()
}

// overran reports whether this call ran out the budget this module gave it.
//
// A deadline the request arrived with does not count, and neither does a client
// that went away: neither is this module failing to answer in the time it
// allows itself, and charging either to a backend would name the wrong party.
func (c call) overran() bool {
	return c.ours && errors.Is(c.ctx.Err(), context.DeadlineExceeded)
}

// abandoned reports the request's own context having ended while the call was
// inside the backend, when it was that and not this module's budget which ended
// the call's context. A backend that returns once its context ends reports that
// it could not answer, and the report is the caller's doing rather than a
// failure of the backend.
func (c call) abandoned() error {
	if c.overran() {
		return nil
	}
	if ended := c.request.Err(); ended != nil {
		return fmt.Errorf("%w: %w", errAbandoned, ended)
	}
	return nil
}
