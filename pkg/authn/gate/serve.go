package gate

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	nethttp "net/http"
	"runtime/debug"
	"sort"
	"strings"
	"sync"

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

// basicScheme is the one authentication scheme this gateway can read a subject
// out of by itself.
//
// Step 1 of the ownership sequence splits on whether a backend claims the
// credential's scheme whole. When one does, that backend reads the subject; when
// none does, the gateway has to, and it can only do that for a scheme whose
// credential carries the subject in a place fixed by a standard. Basic is that
// scheme (RFC 7617: base64 of user:pass). A credential under any other unclaimed
// scheme is opaque here and the request ends at step 1 with a refusal charged to
// the gateway — which is the fail-closed direction, and is fixed by the backend
// for that scheme claiming it whole.
const basicScheme = "basic"

// middlewareOrder places the authentication layer on the chain where the
// constraints leave a choice.
//
// It is a preference, not a constraint: a layer that has to sit inside or
// outside authentication says so with After or Before pointing at
// (*authn.Gate)(nil), which is what the layer's own Provides declares. The value
// is positive so that layers registering with the zero Order — the ones that
// stated no preference — sit further out, which is where request logging and
// panic recovery belong.
const middlewareOrder = 100

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

// reach records why a backend was asked. Exit binding needs it: what counts as
// the returned subject still falling inside the backend's claim depends on which
// claim authorised the question.
type reach int

const (
	// reachScheme is step 1: the backend claims the credential's scheme
	// whole and read the subject out of the credential itself.
	reachScheme reach = iota
	// reachQualified is step 2: the subject named its owner.
	reachQualified
	// reachEnumerated is step 3: the backend wrote the subject out by hand.
	reachEnumerated
	// reachOpen is step 4: the backend answered that a bare subject is its
	// own.
	reachOpen
)

// lookup is the outcome of the ownership sequence: which backend to ask, why,
// and with what subject.
type lookup struct {
	claimant *claimant
	reach    reach
	// subject is what is handed to Verify. It is empty for reachScheme,
	// where the backend reads the subject out of the credential.
	subject string
}

// refusal is a conclusion reached without a backend verdict, or one derived
// from a backend's failure to produce one.
type refusal struct {
	status int
	// bucket is which of the four counters this conclusion lands in.
	bucket verdict.Outcome
	// reason is the log line.
	reason string
	// level is what the log line is written at, and it follows who has
	// something to do about it. A refusal is ordinary traffic and is written
	// at Info, and so is a request its own caller ended; an outage is an
	// operator's to look at and is written at Warn; a backend that returned
	// without answering, that panicked, or that passed a subject outside its
	// claim is a defect somebody has to fix and is written at Error. The
	// level is the first filter every log pipeline applies, so a signal
	// written below the one a deployment keeps is a signal nobody receives.
	level slog.Level
	// backend is the name this is charged to, empty when the gateway
	// produced the refusal itself.
	backend string
	// attrs are extra log attributes, in slog's alternating form.
	attrs []any
}

// gatewayRefusal builds a refusal the gateway produced itself: a 401 charged to
// nobody but this module.
func gatewayRefusal(reason string, attrs ...any) *refusal {
	return &refusal{
		status: nethttp.StatusUnauthorized,
		bucket: verdict.Denied,
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
// traffic and is written at Info.
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
	return &refusal{
		status:  nethttp.StatusServiceUnavailable,
		bucket:  verdict.Unavailable,
		reason:  reason,
		level:   level,
		backend: backend,
		attrs:   attrs,
	}
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
func (g *gateway) middlewareFor(endpoint string) speedhttp.Middleware {
	// The layer is built out of the ownership table, so the table has to
	// exist by now. A nil one is a mistake in this package's own ordering
	// rather than anything a host did, and saying so here beats the nil
	// dereference it would otherwise become several frames away.
	if g.table == nil {
		panic(ModuleName + ": a layer was built for endpoint " + endpoint + " before the " +
			"ownership table. The table decides what the layer answers a refusal with, so " +
			"Init has to build it before it registers anything")
	}
	// The challenge names this endpoint as its realm, so it is built once
	// per endpoint here rather than once per gateway.
	challenge := challengeFor(g.table, endpoint)
	return speedhttp.Middleware{
		Name:     ModuleName,
		Provides: []core.Token{(*authn.Gate)(nil)},
		Order:    middlewareOrder,
		Wrap: func(next nethttp.Handler) nethttp.Handler {
			return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
				passed, forwarded := g.admit(endpoint, challenge, w, r)
				if !passed {
					return
				}
				next.ServeHTTP(w, forwarded)
			})
		},
	}
}

// admit runs the whole authentication decision for one request and reports
// whether it passes. When it does not, the status code has already been written.
//
// The call to the next layer is deliberately outside this function. The recover
// below is for this module's own defects, and the entry point states plainly
// that it does not catch a handler's panic; catching one here would quietly
// change that contract for every route behind the gateway.
func (g *gateway) admit(endpoint, challenge string, w nethttp.ResponseWriter, r *nethttp.Request) (
	passed bool, forwarded *nethttp.Request,
) {
	defer func() {
		if rec := recover(); rec != nil {
			// The gateway's own defect. It is counted in none of the
			// four buckets: those count judgements about a credential,
			// and this is not one.
			//
			// Recovering at all is not optional. The entry point does
			// not catch a handler's panic, and the shape of not
			// recovering here is not a 500 — it is the connection being
			// dropped with no status code reaching the caller at all,
			// which every status-code-based monitor sees as nothing
			// happening.
			log.FromContext(r.Context()).Error(ModuleName+": the gateway panicked while "+
				"deciding this request",
				slog.Any("panic", rec), slog.String("endpoint", endpoint),
				slog.String("stack", string(debug.Stack())))
			w.WriteHeader(nethttp.StatusInternalServerError)
			passed, forwarded = false, nil
		}
	}()

	// The exemption is judged before the Authorization header is read, so
	// no backend is reached on an exempt route whatever the request carries.
	if g.matchers[endpoint].exempts(r) {
		return true, r
	}

	id, deny := g.identify(r)
	if deny != nil {
		g.conclude(r, endpoint, deny)
		if deny.status == nethttp.StatusUnauthorized {
			// A 401 carrying no challenge tells a caller it needs a
			// credential without saying what kind, which is the one
			// thing RFC 7235 does not allow a 401 to do. This module
			// writes no response body, so the header is the whole of
			// what a caller has to go on.
			w.Header().Set(challengeHeader, challenge)
		}
		w.WriteHeader(deny.status)
		return false, nil
	}

	g.counts[verdict.Authenticated].Add(1)
	ctx := identity.With(r.Context(), id)
	ctx = log.WithAttrs(ctx, slog.String("subject", id.Key()))
	return true, r.WithContext(ctx)
}

// identify runs the ownership sequence, the backend call and exit binding,
// and produces either the identity to stamp or the refusal to conclude with.
func (g *gateway) identify(r *nethttp.Request) (identity.Identity, *refusal) {
	credential, ok := credentialOf(r)
	if !ok {
		return identity.Identity{}, gatewayRefusal(ModuleName + ": the request carries no usable " +
			authHeader + " header")
	}

	found, deny := g.resolveOwner(r.Context(), credential)
	if deny != nil {
		return identity.Identity{}, deny
	}

	answer, err := g.verify(r.Context(), found.claimant, found.subject, credential)
	if err != nil {
		return identity.Identity{}, outage(found.claimant.name,
			ModuleName+": the backend could not be asked about this credential", err)
	}

	switch verdict.OutcomeOf(answer) {
	case verdict.Authenticated:
		// Falls through to exit binding below.
	case verdict.Denied:
		return identity.Identity{}, &refusal{
			status:  nethttp.StatusUnauthorized,
			bucket:  verdict.Denied,
			reason:  ModuleName + ": the backend refused this credential",
			level:   slog.LevelInfo,
			backend: found.claimant.name,
			attrs:   causeAttrs(answer),
		}
	case verdict.Unavailable:
		return identity.Identity{}, outage(found.claimant.name,
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
			bucket:  verdict.Unspoken,
			reason:  ModuleName + ": the backend returned without answering",
			level:   slog.LevelError,
			backend: found.claimant.name,
		}
	}

	returned := verdict.SubjectOf(answer)
	bound, err := g.exitBound(r.Context(), found, returned)
	if err != nil {
		return identity.Identity{}, outage(found.claimant.name,
			ModuleName+": the backend could not be asked whether the subject it returned "+
				"is its own", err)
	}
	if !bound {
		// The one gateway-produced refusal that happens after a backend
		// has answered. The judgement is the ownership table's, and the
		// gateway built the table, so it is charged here rather than to
		// the backend. It is written at Error all the same: whoever is
		// charged, a backend said something it had no authority to say,
		// which is a defect in that backend or an overlap in this
		// deployment, and either is somebody's to fix.
		deny := gatewayRefusal(ModuleName+": the backend returned a subject "+
			"that does not fall inside its own claim, so the pass is not honoured",
			slog.String("backend", found.claimant.name),
			slog.String("returned-subject", returned))
		deny.level = slog.LevelError
		return identity.Identity{}, deny
	}
	return identity.New(found.claimant.name, returned), nil
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
// attribution, at the level the refusal was built with. The response body is
// never written: this module writes a status code and the headers that go with
// it, so attribution lives in the log alone.
func (g *gateway) conclude(r *nethttp.Request, endpoint string, deny *refusal) {
	g.counts[deny.bucket].Add(1)

	attributed := deny.backend
	if attributed == "" {
		attributed = ModuleName
	}
	attrs := append([]any{
		slog.String("endpoint", endpoint),
		slog.Int("status", deny.status),
		slog.String("outcome", deny.bucket.String()),
		slog.String("attributed-to", attributed),
	}, deny.attrs...)
	log.FromContext(r.Context()).Log(r.Context(), deny.level, deny.reason, attrs...)
}

// credentialOf reads the credential out of the request.
//
// More than one Authorization header is refused rather than resolved by picking
// one: which of them the caller meant cannot be known, and choosing would let a
// caller who can add a header decide which credential is judged.
func credentialOf(r *nethttp.Request) (authn.Credential, bool) {
	values := r.Header.Values(authHeader)
	if len(values) != 1 {
		return authn.Credential{}, false
	}
	field := strings.TrimSpace(values[0])
	if field == "" {
		return authn.Credential{}, false
	}
	scheme, value, _ := strings.Cut(field, " ")
	if scheme == "" {
		return authn.Credential{}, false
	}
	// The scheme is folded here, once, because that is what the surface
	// promises a backend: authentication scheme names are case-insensitive on
	// the wire, and a backend left to compare the raw token would authenticate
	// or refuse on the casing a client happened to send.
	return authn.Credential{Scheme: strings.ToLower(scheme), Value: strings.TrimSpace(value)}, true
}

// subjectFromCredential reads the subject a Basic credential carries. See
// basicScheme for why this is the only scheme it answers for.
func subjectFromCredential(c authn.Credential) (string, bool) {
	if !strings.EqualFold(c.Scheme, basicScheme) {
		return "", false
	}
	raw, err := base64.StdEncoding.DecodeString(c.Value)
	if err != nil {
		return "", false
	}
	user, _, found := strings.Cut(string(raw), ":")
	if !found || user == "" {
		return "", false
	}
	return user, true
}

// resolveOwner runs the four-step ownership sequence. The sequence is the whole
// of it: a credential that reaches the end of step 4 without an owner is
// refused, never handed to a backend chosen some other way.
func (g *gateway) resolveOwner(ctx context.Context, c authn.Credential) (lookup, *refusal) {
	// 1. The scheme claimed whole.
	// The credential arrives with its scheme already folded, and the table is
	// keyed the same way, so the two meet without either side folding again.
	if owner, claimed := g.table.byScheme[c.Scheme]; claimed {
		return lookup{claimant: owner, reach: reachScheme}, nil
	}
	subject, readable := subjectFromCredential(c)
	if !readable {
		return lookup{}, gatewayRefusal(ModuleName+": no backend claims this credential's "+
			"authentication scheme, and the gateway cannot read a subject out of it",
			slog.String("scheme", c.Scheme))
	}

	// 2. The subject naming its owner.
	//
	// The qualification says which backend to ask. It does not say that the
	// backend is the authority for the subject, and the claimant still has
	// to answer that: ownership is never derived from the subject string.
	// Without the check below a caller who knows a backend's name could
	// reach it with any subject at all — turning the writing form that
	// exists to disambiguate into one that escalates — and exit binding
	// would wave the result through, because the subject a backend returns
	// on this path is the subject it was asked about.
	if owner, bare, qualified := g.qualified(subject); qualified {
		if owner == nil {
			return lookup{}, gatewayRefusal(ModuleName+": the subject names an owner no "+
				"backend in this command reports", slog.String("subject", subject))
		}
		owns, err := g.claims(ctx, owner, bare)
		if err != nil {
			return lookup{}, outage(owner.name,
				ModuleName+": the named backend could not answer whether this subject is its own",
				err, slog.String("subject", subject))
		}
		if !owns {
			return lookup{}, gatewayRefusal(ModuleName+": the backend this subject names is not "+
				"the authority for it", slog.String("subject", subject),
				slog.String("backend", owner.name))
		}
		return lookup{claimant: owner, reach: reachQualified, subject: bare}, nil
	}

	// 3. The subject enumerated by hand.
	if owner, enumerated := g.table.bySubject[subject]; enumerated {
		return lookup{claimant: owner, reach: reachEnumerated, subject: subject}, nil
	}

	// 4. A bare name, put to every open claim at once.
	return g.askOpenClaims(ctx, subject)
}

// qualified splits an owner-qualified subject. The split is on the first
// separator, so a backend name never has to be escaped and the remainder is the
// subject as that backend knows it.
func (g *gateway) qualified(subject string) (owner *claimant, bare string, ok bool) {
	name, rest, found := strings.Cut(subject, authn.Separator)
	if !found {
		return nil, subject, false
	}
	return g.table.byName[name], rest, true
}

// ownsAnswer is one open claimant's answer about one bare subject.
type ownsAnswer struct {
	claimant *claimant
	owns     bool
	err      error
}

// askOpenClaims puts a bare subject to every open claim at once.
//
// Exactly one claim is an answer; none is a refusal; more than one is a refusal
// too, and that is the overlap the build could not detect — an open claim has no
// key to collide on. The log names the parties and the qualified form that
// resolves it, because nothing else in the process can.
//
// Any open claim that fails or panics ends the request as an outage whatever the
// others said. A claim that could not be asked is not a claim that said no, and
// treating it as one would let a broken backend hand its subjects to whoever
// answers next.
func (g *gateway) askOpenClaims(ctx context.Context, subject string) (lookup, *refusal) {
	if len(g.table.open) == 0 {
		return lookup{}, gatewayRefusal(ModuleName+": no backend claims this subject",
			slog.String("subject", subject))
	}

	answers := make([]ownsAnswer, len(g.table.open))
	var wg sync.WaitGroup
	for i, c := range g.table.open {
		wg.Add(1)
		go func() {
			defer wg.Done()
			owns, err := g.owns(ctx, c, subject)
			answers[i] = ownsAnswer{claimant: c, owns: owns, err: err}
		}()
	}
	wg.Wait()

	var owners []*claimant
	for _, a := range answers {
		if a.err != nil {
			return lookup{}, outage(a.claimant.name,
				ModuleName+": an open claim could not answer whether this subject is its own",
				a.err, slog.String("subject", subject))
		}
		if a.owns {
			owners = append(owners, a.claimant)
		}
	}

	switch len(owners) {
	case 0:
		return lookup{}, gatewayRefusal(ModuleName+": no backend claims this subject",
			slog.String("subject", subject))
	case 1:
		return lookup{claimant: owners[0], reach: reachOpen, subject: subject}, nil
	default:
		names := make([]string, 0, len(owners))
		qualifications := make([]string, 0, len(owners))
		for _, c := range owners {
			names = append(names, c.name)
			qualifications = append(qualifications, c.name+authn.Separator+subject)
		}
		sort.Strings(names)
		sort.Strings(qualifications)
		// Charged to the gateway: the judgement is the ownership
		// table's, and the table is the gateway's.
		return lookup{}, gatewayRefusal(ModuleName+": more than one open claim reports this "+
			"subject as its own, so it has no single authority",
			slog.String("subject", subject),
			slog.String("claimed-by", strings.Join(names, ", ")),
			slog.String("qualified-forms", strings.Join(qualifications, ", ")))
	}
}

// exitBound reports whether the subject a passing verdict returned still falls
// inside the claim that authorised the question.
//
// Without it a backend claiming one small slice of the subject space could
// return any subject at all and mint an identity belonging to someone else,
// stepping straight over the ownership table.
//
// What "inside the claim" means depends on how the backend was reached, because
// that is what the claim was:
//
//   - A subject another backend enumerated is refused whatever the path. That
//     is the exact collision the table exists to prevent, and it is the one
//     check that also binds a backend claiming a whole scheme — its claim is the
//     scheme, so nothing else here constrains the subject it reads out.
//   - The subject that was asked about is bound by construction: the step that
//     selected this backend established the ownership of that very subject.
//   - A different subject has to be established again, by the same two kinds of
//     claim that could have selected the backend in the first place.
func (g *gateway) exitBound(ctx context.Context, found lookup, returned string) (bool, error) {
	if strings.TrimSpace(returned) == "" {
		return false, nil
	}
	// A subject carrying the separator could not be told from an owner
	// qualification once it is written down, so a backend whose natural
	// identifiers carry one has to map them itself.
	if strings.Contains(returned, authn.Separator) {
		return false, nil
	}
	if other, enumerated := g.table.bySubject[returned]; enumerated && other != found.claimant {
		return false, nil
	}
	if found.reach == reachScheme {
		return true, nil
	}
	if returned == found.subject {
		return true, nil
	}
	return g.claims(ctx, found.claimant, returned)
}

// claims reports whether this backend is the authority for a bare subject, by
// the two kinds of claim that can answer for one: the subjects it wrote out by
// hand, and an open claim it answers itself.
//
// A whole-scheme claim is not among them, and cannot be: it says which
// credentials come here, not which subjects belong here. A backend reached
// through one is bound by the collision check alone.
func (g *gateway) claims(ctx context.Context, c *claimant, subject string) (bool, error) {
	if c.enumerates(subject) {
		return true, nil
	}
	if c.owner != nil {
		return g.owns(ctx, c, subject)
	}
	return false, nil
}

// verify puts one credential to one backend, under the call budget, the
// in-flight gate and a recover.
func (g *gateway) verify(ctx context.Context, c *claimant, subject string, cred authn.Credential) (
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
			// survives and every other ownership in it goes on
			// answering.
			err = caught("backend %q panicked while verifying a credential: %v", c.name, rec)
		}
	}()

	answer = c.verifier.Verify(made.ctx, subject, cred)
	if made.overran() {
		return authn.Verdict{}, fmt.Errorf("backend %q did not answer within the call budget of %s",
			c.name, g.settings.budget)
	}
	// Only a report of not being able to answer is read against the
	// request's context. A pass or a refusal is an answer about the
	// credential, and it stands however late the caller's own deadline was.
	if verdict.OutcomeOf(answer) == verdict.Unavailable {
		if abandoned := made.abandoned(); abandoned != nil {
			return authn.Verdict{}, abandoned
		}
	}
	return answer, nil
}

// owns asks one open claim about one subject, under the same three guards.
func (g *gateway) owns(ctx context.Context, c *claimant, subject string) (owns bool, err error) {
	made, err := g.enter(ctx)
	if err != nil {
		return false, err
	}
	defer made.release()
	defer func() {
		if rec := recover(); rec != nil {
			err = caught("backend %q panicked while answering whether %q is its own: %v",
				c.name, subject, rec)
		}
	}()

	owns, err = c.owner.Owns(made.ctx, subject)
	if made.overran() {
		return false, fmt.Errorf("backend %q did not answer ownership within the call budget of %s",
			c.name, g.settings.budget)
	}
	if err != nil {
		if abandoned := made.abandoned(); abandoned != nil {
			return false, abandoned
		}
		return false, err
	}
	return owns, nil
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
