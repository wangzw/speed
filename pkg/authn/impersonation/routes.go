package impersonation

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// The names the two verifying backends answer under. They are strings and not
// imports: the credential a start is on is told by the backend the gateway
// stamped, and the authentication directory takes up neither verifier here.
const (
	sessionBackend = "session"
	tokenBackend   = "token"
)

// typePrefix starts the Problem.Type of every failure this capability's
// handlers write, the code following it being one of mount's closed table.
const typePrefix = "urn:speed:authn:"

// cacheControlSetCookie is the directive the session capability writes beside a
// Set-Cookie it issues. The mounter puts Cache-Control: no-store on every
// response; this one keeps a cache from holding the response whose cookie it
// may ignore. The cookie is read back from a recorder that sees the header the
// library wrote, so the handler adds this directive to the answer itself.
const cacheControlSetCookie = `no-cache="Set-Cookie"`

// stepUpDescription is the error_description of the step-up challenge. It names
// no requirement of the route and nothing of the account.
const stepUpDescription = "A stronger or more recent authentication is required"

// authnFailure is a refusal of one of the capability's problem codes: the entry
// point writes it as a problem document of the code's status and type.
func authnFailure(status int, code string) error {
	return speedhttp.Fault{Status: status, Type: typePrefix + code}
}

// mountRoutes registers the three routes of the impersonation self-service from
// the operations that declare them, so the handler's input and answer types, the
// fragment and the route are one source. All three are guarded by the gateway.
// The end and the status are registered Impersonable, so the administrator can
// always see and end an impersonation; the start is not, so an impersonating
// identity cannot begin a nested one, and the gateway refuses it before the
// handler.
func (m *product) mountRoutes() {
	mount.Declared(m.mounter, startOperation, mountRel(startOperation.Path),
		prescreenStart(startOperation.Handle(m.handleStart)))
	mount.Declared(m.mounter, statusOperation, mountRel(statusOperation.Path),
		statusOperation.Handle(m.handleStatus), mount.Impersonable())
	mount.Declared(m.mounter, endOperation, mountRel(endOperation.Path),
		endOperation.Handle(m.handleEnd), mount.Impersonable())
}

// mountRel turns a declared route into the relative path the mounter joins to
// its base. The declaration writes the route against the module's API root, and
// the fragment is a static declaration fixed before configuration, so the part
// of it the capability owns is the default mount base less that root. Removing
// it leaves the relative path the mounter takes, whatever base the deployment
// configured: a configured base moves the routes without moving the contract,
// which is the split the Spec already accepts. A declared route that does not
// begin with the owned part is a declaration defect, not something to mount at a
// guessed path.
func mountRel(declared string) string {
	owned := strings.TrimPrefix(mount.DefaultBasePath, impersonationSpec.APIRoot())
	rel, found := strings.CutPrefix(declared, owned)
	if !found {
		panic(diagnosticPrefix + ModuleName + ": the declared route " + declared +
			" does not begin with the capability's mounted path " + owned)
	}
	return rel
}

// postedGrant is the view of a freshly started record: what the design's start
// answer carries, without the start instant.
func postedGrant(g authn.ImpersonationGrant) AuthnImpersonationGrant {
	return AuthnImpersonationGrant{ID: g.ID, Target: string(g.Target), Tenant: g.Tenant, EndsAt: g.EndsAt.UTC()}
}

// currentGrant is the view of a record in force, with the instant it began.
func currentGrant(g authn.ImpersonationGrant) AuthnImpersonationGrant {
	started := g.StartedAt.UTC()
	return AuthnImpersonationGrant{
		ID: g.ID, Target: string(g.Target), Tenant: g.Tenant, StartedAt: &started, EndsAt: g.EndsAt.UTC(),
	}
}

// handleStart answers POST /impersonation: it calls Start and then attaches the
// carrier, publishes the started event and writes the answer. The handler holds
// the carrier and the events because Start can neither hand a token over nor
// reach a response writer.
func (m *product) handleStart(ctx context.Context, in startInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, ok := authn.FromContext(ctx)
	if !ok {
		return nil, m.unauthorized()
	}
	if id.Impersonating() {
		return nil, authnFailure(http.StatusForbidden, mount.CodeImpersonationRestricted)
	}

	cookie := id.Backend() == sessionBackend
	if !cookie && m.tokens == nil {
		// A bearer start has no carrier to attach and no token to issue, so it
		// fails before Start and leaves no granted audit row without an action.
		return nil, authnFailure(http.StatusServiceUnavailable, mount.CodeUnavailable)
	}

	request := authn.ImpersonationRequest{
		Target: authn.AccountID(in.Account),
		Tenant: in.Tenant,
		Reason: in.Reason,
		TTL:    time.Duration(in.TTLSeconds) * time.Second,
	}
	grant, err := m.Start(ctx, id, request)
	if err != nil {
		return nil, m.startFailure(id, err)
	}

	if cookie {
		var issued authn.Issued
		issued, err = m.sessions.SetImpersonation(ctx, id.Session(), grant.ID)
		if err != nil {
			m.compensate(ctx, grant, err)
			return nil, authnFailure(http.StatusServiceUnavailable, mount.CodeUnavailable)
		}
		m.publishStarted(ctx, grant)
		mount.AddResponseHeader(ctx, "Cache-Control", cacheControlSetCookie)
		reply := speedhttp.JSONReply(http.StatusOK, AuthnImpersonationStart{Grant: postedGrant(grant)})
		if c := m.issuedCookie(issued); c != nil {
			reply = speedhttp.WithCookies(reply, c)
		}
		return reply, nil
	}

	token, err := m.issueImpersonation(ctx, id, grant)
	if err != nil {
		m.compensate(ctx, grant, err)
		return nil, authnFailure(http.StatusServiceUnavailable, mount.CodeUnavailable)
	}
	m.publishStarted(ctx, grant)
	expires := int64(token.ExpiresAt.Sub(m.clock.Now()) / time.Second)
	return speedhttp.JSONReply(http.StatusOK, AuthnImpersonationStart{
		Grant:       postedGrant(grant),
		AccessToken: token.Value,
		TokenType:   "Bearer",
		ExpiresIn:   &expires,
	}), nil
}

// handleStatus answers GET /impersonation: the impersonation in force on the
// caller's session, or 404 when there is none still valid.
func (m *product) handleStatus(ctx context.Context, _ struct{}, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, ok := authn.FromContext(ctx)
	if !ok {
		return nil, m.unauthorized()
	}
	if id.Session() == "" {
		return speedhttp.EmptyReply(http.StatusNotFound), nil
	}
	g, found, err := m.store.unendedForSession(ctx, id.Session())
	if err != nil {
		return nil, m.routeFailure("reading the impersonation of a session", err)
	}
	if !found {
		return speedhttp.EmptyReply(http.StatusNotFound), nil
	}
	g, active, err := m.Active(ctx, g.ID)
	if err != nil {
		return nil, m.routeFailure("reading an impersonation record", err)
	}
	if !active {
		return speedhttp.EmptyReply(http.StatusNotFound), nil
	}
	return speedhttp.JSONReply(http.StatusOK, AuthnImpersonationGrantEnvelope{Grant: currentGrant(g)}), nil
}

// handleEnd answers DELETE /impersonation: it ends the record on the caller's
// session, then, under a cookie, clears the session's marker without rotating
// the token or writing a cookie, so the caller's cookie stays valid with the
// initiator's own identity. A failure to clear the marker does not bring the
// record back: the record has ended and a verifying backend reads Active as
// false.
func (m *product) handleEnd(ctx context.Context, _ struct{}, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, ok := authn.FromContext(ctx)
	if !ok {
		return nil, m.unauthorized()
	}
	if err := m.End(ctx, id, ""); err != nil {
		if errors.Is(err, authn.ErrNotImpersonating) {
			return speedhttp.EmptyReply(http.StatusNotFound), nil
		}
		return nil, m.routeFailure("ending an impersonation", err)
	}
	if id.Backend() == sessionBackend && id.Session() != "" {
		if _, err := m.sessions.SetImpersonation(ctx, id.Session(), ""); err != nil {
			m.logger.Error(ModuleName+": clearing the impersonation marker of a session failed",
				slog.String("cause", err.Error()))
		}
	}
	return speedhttp.EmptyReply(http.StatusNoContent), nil
}

// issueImpersonation signs the impersonation access token: the target's subject,
// the administrator's actor claim, the record, its tenant and session, the level
// capped at a single factor and a lifetime of the shorter of the access-token
// setting and the record's remaining lifetime. No refresh token is issued.
func (m *product) issueImpersonation(ctx context.Context, actor authn.Identity, g authn.ImpersonationGrant) (authn.AccessToken, error) {
	ttl := m.settings.tokenAccessTTL()
	if remaining := g.EndsAt.Sub(m.clock.Now()); remaining < ttl {
		ttl = remaining
	}
	return m.tokens.Issue(ctx, authn.IssueToken{
		Account:         g.Target,
		Actor:           g.Actor,
		Session:         g.SessionID,
		Methods:         actor.Methods(),
		Assurance:       actor.Assurance(),
		AuthTime:        actor.AuthTime(),
		ImpersonationID: g.ID,
		Scope:           g.Tenant,
		TTL:             ttl,
	})
}

// compensate closes a start whose carrier could not be attached: it ends the
// record with the reason start-failed through a conditional update, writes a
// failed audit row that links to the granted one, and leaves the answer to the
// caller. The record is never left in force with no carrier pointing at it.
func (m *product) compensate(ctx context.Context, g authn.ImpersonationGrant, cause error) {
	m.logger.Error(ModuleName+": attaching the carrier of an impersonation failed",
		slog.String("grant", g.ID), slog.String("cause", cause.Error()))
	if err := m.endStartFailed(ctx, g.ID); err != nil {
		m.logger.Error(ModuleName+": compensating a failed start failed",
			slog.String("grant", g.ID), slog.String("cause", err.Error()))
	}
	m.writeAudit(ctx, audit.Entry{
		Action:  actionStart,
		Object:  string(g.Target),
		Outcome: outcomeFailed,
		Purpose: g.Reason,
		Detail:  map[string]string{detailGrantID: g.ID, detailTenant: g.Tenant, detailSID: string(g.SessionID)},
	})
}

// publishStarted publishes the started event of a record whose carrier is
// attached. The reason never reaches the event: a payload's top-level field
// becomes a span attribute.
func (m *product) publishStarted(ctx context.Context, g authn.ImpersonationGrant) {
	m.publish(ctx, authn.EventImpersonationStarted, g.Tenant, authn.ImpersonationStarted{
		GrantID:   g.ID,
		ActorID:   string(g.Actor),
		AccountID: string(g.Target),
		SessionID: string(g.SessionID),
		ExpiresAt: g.EndsAt.UTC().Format(time.RFC3339),
	})
}

// startFailure is the answer for an error Start returned.
func (m *product) startFailure(id authn.Identity, err error) error {
	switch {
	case errors.Is(err, authn.ErrStepUpRequired):
		return speedhttp.Fault{
			Status: http.StatusUnauthorized,
			Type:   typePrefix + mount.CodeInsufficientUserAuthentication,
			Headers: []speedhttp.HeaderValue{{
				Name: "WWW-Authenticate",
				Value: m.stepUpChallenge(id, authn.Requirement{
					Assurance: authn.AssuranceMulti,
					MaxAge:    m.settings.maxAuthAge(),
					Local:     true,
				}),
			}},
		}
	case errors.Is(err, authn.ErrFactorNotSeasoned):
		return authnFailure(http.StatusForbidden, mount.CodeFactorNotSeasoned)
	case errors.Is(err, authn.ErrContactRequired):
		return authnFailure(http.StatusForbidden, mount.CodeContactRequired)
	case errors.Is(err, authn.ErrForbidden):
		return authnFailure(http.StatusForbidden, mount.CodeForbidden)
	case errors.Is(err, authn.ErrImpersonationRestricted), errors.Is(err, authn.ErrImpersonationNeedsSession):
		return authnFailure(http.StatusForbidden, mount.CodeImpersonationRestricted)
	case errors.Is(err, authn.ErrImpersonationActive):
		return authnFailure(http.StatusConflict, mount.CodeConflict)
	default:
		return m.routeFailure("starting an impersonation", err)
	}
}

// unauthorized is the 401 of a route that found no credential: the capability's
// challenge, carried as the fault header the entry point writes on the declared
// 401 response.
func (m *product) unauthorized() error {
	return speedhttp.Fault{
		Status: http.StatusUnauthorized,
		Headers: []speedhttp.HeaderValue{{
			Name:  "WWW-Authenticate",
			Value: mount.UnauthorizedChallenge(m.mounter.Endpoint()),
		}},
	}
}

// stepUpChallenge builds the challenge a step-up 401 carries: the scheme the
// identity authenticated with, then the error, the level the operation requires
// and the requirement's max_age in whole seconds, rounded up. The mount package
// writes the same value for an untyped handler but exports only the plain
// challenge, so this text is reproduced here.
func (m *product) stepUpChallenge(id authn.Identity, r authn.Requirement) string {
	var b strings.Builder
	b.WriteString(challengeScheme(id.Challenge()))
	b.WriteString(` error="insufficient_user_authentication", error_description="`)
	b.WriteString(stepUpDescription)
	b.WriteString(`", acr_values="`)
	b.WriteString(strconv.Itoa(int(r.Assurance)))
	b.WriteByte('"')
	if r.MaxAge > 0 {
		b.WriteString(`, max_age="`)
		b.WriteString(strconv.FormatInt(int64((r.MaxAge+time.Second-1)/time.Second), 10))
		b.WriteByte('"')
	}
	return b.String()
}

// challengeScheme returns scheme when it is an RFC 9110 token, and Bearer
// otherwise, as the mount package does for the same value.
func challengeScheme(scheme string) string {
	if scheme == "" {
		return "Bearer"
	}
	for i := 0; i < len(scheme); i++ {
		ch := scheme[i]
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9',
			strings.IndexByte("!#$%&'*+-.^_`|~", ch) >= 0:
		default:
			return "Bearer"
		}
	}
	return scheme
}

// routeFailure answers a failure of the store: 503, and the cause in the log,
// never in the response.
func (m *product) routeFailure(op string, err error) error {
	m.logger.Error(ModuleName+": an impersonation route failed",
		slog.String("operation", op), slog.String("cause", err.Error()))
	return authnFailure(http.StatusServiceUnavailable, mount.CodeUnavailable)
}

// cookieRecorder is a response writer that keeps the Set-Cookie headers the
// session capability writes, so a typed handler can attach the cookie to its
// reply. It writes no status and no body.
type cookieRecorder struct {
	header http.Header
}

func (r *cookieRecorder) Header() http.Header            { return r.header }
func (r *cookieRecorder) Write(body []byte) (int, error) { return len(body), nil }
func (r *cookieRecorder) WriteHeader(int)                {}

// issuedCookie turns an issued session into the cookie a reply attaches. The
// sessions capability owns the cookie's name and attributes and writes them to
// a response writer, so the value is read back from the header it wrote rather
// than reproduced here. That writer also receives the Cache-Control directive
// the library adds beside the cookie, which this type does not carry; the
// caller adds it to the answer with mount.AddResponseHeader. An issued value
// with no token writes no cookie, and nil is returned.
func (m *product) issuedCookie(issued authn.Issued) *http.Cookie {
	if issued.Token == "" {
		return nil
	}
	recorder := &cookieRecorder{header: http.Header{}}
	m.sessions.SetCookie(recorder, issued)
	response := http.Response{Header: recorder.header}
	cookies := response.Cookies()
	if len(cookies) == 0 {
		return nil
	}
	return cookies[0]
}
