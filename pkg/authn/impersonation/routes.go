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

const (
	sessionBackend = "session"
	tokenBackend   = "token"
)

const typePrefix = "urn:speed:authn:"

// cacheControlSetCookie keeps a cache from holding a response whose cookie it
// ignores; the handler adds it because the cookie is read back from a recorder.
const cacheControlSetCookie = `no-cache="Set-Cookie"`

const stepUpDescription = "A stronger or more recent authentication is required"

func authnFailure(status int, code string) error {
	return speedhttp.Fault{Status: status, Type: typePrefix + code}
}

func (m *product) mountRoutes() {
	mount.Declared(m.mounter, startOperation, mountRel(startOperation.Path),
		prescreenStart(startOperation.Handle(m.handleStart)))
	mount.Declared(m.mounter, statusOperation, mountRel(statusOperation.Path),
		statusOperation.Handle(m.handleStatus), mount.Impersonable())
	mount.Declared(m.mounter, endOperation, mountRel(endOperation.Path),
		endOperation.Handle(m.handleEnd), mount.Impersonable())
}

func mountRel(declared string) string {
	owned := strings.TrimPrefix(mount.DefaultBasePath, impersonationSpec.APIRoot())
	rel, found := strings.CutPrefix(declared, owned)
	if !found {
		panic(diagnosticPrefix + ModuleName + ": the declared route " + declared +
			" does not begin with the capability's mounted path " + owned)
	}
	return rel
}

func postedGrant(g authn.ImpersonationGrant, tenantName string) AuthnImpersonationGrant {
	return AuthnImpersonationGrant{ID: g.ID, Target: string(g.Target), Tenant: tenantName, EndsAt: g.EndsAt.UTC()}
}

func currentGrant(g authn.ImpersonationGrant, tenantName string) AuthnImpersonationGrant {
	started := g.StartedAt.UTC()
	return AuthnImpersonationGrant{
		ID: g.ID, Target: string(g.Target), Tenant: tenantName, StartedAt: &started, EndsAt: g.EndsAt.UTC(),
	}
}

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
		return nil, authnFailure(http.StatusServiceUnavailable, mount.CodeUnavailable)
	}

	tenant, found, resolveErr := m.tenantIDOf(ctx, in.Tenant)
	if resolveErr != nil {
		return nil, authnFailure(http.StatusServiceUnavailable, mount.CodeUnavailable)
	}
	if !found {
		return nil, authnFailure(http.StatusForbidden, mount.CodeImpersonationRestricted)
	}

	request := authn.ImpersonationRequest{
		Target: authn.AccountID(in.Account),
		Tenant: tenant,
		Reason: in.Reason,
		TTL:    time.Duration(in.TTLSeconds) * time.Second,
	}
	grant, err := m.Start(ctx, id, request)
	if err != nil {
		return nil, m.startFailure(id, err)
	}
	tenantName, nameErr := m.nameOf(ctx, grant.Tenant)
	if nameErr != nil {
		m.compensate(ctx, grant, nameErr)
		return nil, authnFailure(http.StatusServiceUnavailable, mount.CodeUnavailable)
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
		reply := speedhttp.JSONReply(http.StatusOK, AuthnImpersonationStart{Grant: postedGrant(grant, tenantName)})
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
		Grant:       postedGrant(grant, tenantName),
		AccessToken: token.Value,
		TokenType:   "Bearer",
		ExpiresIn:   &expires,
	}), nil
}

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
	tenantName, nameErr := m.nameOf(ctx, g.Tenant)
	if nameErr != nil {
		return nil, m.routeFailure("reading the tenant's name", nameErr)
	}
	return speedhttp.JSONReply(http.StatusOK, AuthnImpersonationGrantEnvelope{Grant: currentGrant(g, tenantName)}), nil
}

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

func (m *product) compensate(ctx context.Context, g authn.ImpersonationGrant, cause error) {
	m.logger.Error(ModuleName+": attaching the carrier of an impersonation failed",
		slog.String("grant", g.ID), slog.String("cause", cause.Error()))
	if err := m.endStartFailed(ctx, g.ID); err != nil {
		m.logger.Error(ModuleName+": compensating a failed start failed",
			slog.String("grant", g.ID), slog.String("cause", err.Error()))
	}
	tenantName, _ := m.nameOf(ctx, g.Tenant)
	m.writeAudit(ctx, audit.Entry{
		Action:  actionStart,
		Object:  audit.AccountObject(string(g.Target)),
		Outcome: outcomeFailed,
		Purpose: g.Reason,
		Detail:  map[string]string{detailGrantID: g.ID, detailTenant: tenantName, detailSID: string(g.SessionID)},
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

func (m *product) unauthorized() error {
	return speedhttp.Fault{
		Status: http.StatusUnauthorized,
		Headers: []speedhttp.HeaderValue{{
			Name:  "WWW-Authenticate",
			Value: mount.UnauthorizedChallenge(m.mounter.Endpoint()),
		}},
	}
}

// stepUpChallenge reproduces the mount package's challenge text because that
// package exports only the plain challenge.
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

func (m *product) routeFailure(op string, err error) error {
	m.logger.Error(ModuleName+": an impersonation route failed",
		slog.String("operation", op), slog.String("cause", err.Error()))
	return authnFailure(http.StatusServiceUnavailable, mount.CodeUnavailable)
}

type cookieRecorder struct {
	header http.Header
}

func (r *cookieRecorder) Header() http.Header            { return r.header }
func (r *cookieRecorder) Write(body []byte) (int, error) { return len(body), nil }
func (r *cookieRecorder) WriteHeader(int)                {}

// issuedCookie reads the cookie back from the header the sessions capability
// wrote, because it owns the cookie's name and attributes; the Cache-Control
// directive written beside it is added to the answer separately.
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
