package impersonation

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
)

// impersonationPath is the route of the self-service group, relative to the
// mounter's base path.
const impersonationPath = "/impersonation"

// The names the two verifying backends answer under. They are strings and not
// imports: the credential a start is on is told by the backend the gateway
// stamped, and the authentication directory takes up neither verifier here.
const (
	sessionBackend = "session"
	tokenBackend   = "token"
)

// mountRoutes registers the three routes of the impersonation self-service.
// All three are guarded by the gateway. The end and the status are registered
// Impersonable, so the administrator can always see and end an impersonation;
// the start is not, so an impersonating identity cannot begin a nested one, and
// the gateway refuses it before the handler.
func (m *product) mountRoutes() {
	m.mounter.Guarded(http.MethodPost, impersonationPath, m.handleStart)
	m.mounter.Guarded(http.MethodGet, impersonationPath, m.handleStatus, mount.Impersonable())
	m.mounter.Guarded(http.MethodDelete, impersonationPath, m.handleEnd, mount.Impersonable())
}

// startBody is the request of a start.
type startBody struct {
	Account    string `json:"account"`
	Tenant     string `json:"tenant"`
	Reason     string `json:"reason"`
	TTLSeconds int64  `json:"ttl_seconds"`
}

// grantView is a record as the start and the status show it. It is built field by
// field so that a field added to the record later does not reach a client by
// being there. The reason never reaches a response.
type grantView struct {
	ID        string     `json:"id"`
	Target    string     `json:"target"`
	Tenant    string     `json:"tenant"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	EndsAt    time.Time  `json:"ends_at"`
}

// postedGrant is the view of a freshly started record: what the design's start
// answer carries, without the start instant.
func postedGrant(g authn.ImpersonationGrant) grantView {
	return grantView{ID: g.ID, Target: string(g.Target), Tenant: g.Tenant, EndsAt: g.EndsAt.UTC()}
}

// currentGrant is the view of a record in force, with the instant it began.
func currentGrant(g authn.ImpersonationGrant) grantView {
	started := g.StartedAt.UTC()
	return grantView{ID: g.ID, Target: string(g.Target), Tenant: g.Tenant, StartedAt: &started, EndsAt: g.EndsAt.UTC()}
}

// handleStart answers POST /impersonation: it calls Start and then attaches the
// carrier, publishes the started event and writes the answer. The handler holds
// the carrier and the events because Start can neither hand a token over nor
// reach a response writer.
func (m *product) handleStart(c *gin.Context) {
	ctx := c.Request.Context()
	id, ok := authn.FromContext(ctx)
	if !ok {
		mount.Unauthorized(c, m.mounter.Endpoint(), "")
		return
	}
	if id.Impersonating() {
		mount.Forbidden(c, mount.CodeImpersonationRestricted)
		return
	}

	cookie := id.Backend() == sessionBackend
	if !cookie && m.tokens == nil {
		// A bearer start has no carrier to attach and no token to issue, so it
		// fails before Start and leaves no granted audit row without an action.
		mount.Unavailable(c)
		return
	}

	var body startBody
	if !mount.Decode(c, &body) {
		return
	}
	request := authn.ImpersonationRequest{
		Target: authn.AccountID(body.Account),
		Tenant: body.Tenant,
		Reason: body.Reason,
		TTL:    time.Duration(body.TTLSeconds) * time.Second,
	}
	grant, err := m.Start(ctx, id, request)
	if err != nil {
		m.answerStart(c, id, err)
		return
	}

	if cookie {
		var issued authn.Issued
		issued, err = m.sessions.SetImpersonation(ctx, id.Session(), grant.ID)
		if err != nil {
			m.compensate(ctx, grant, err)
			mount.Unavailable(c)
			return
		}
		m.sessions.SetCookie(c.Writer, issued)
		m.publishStarted(ctx, grant)
		c.JSON(http.StatusOK, gin.H{"grant": postedGrant(grant)})
		return
	}

	token, err := m.issueImpersonation(ctx, id, grant)
	if err != nil {
		m.compensate(ctx, grant, err)
		mount.Unavailable(c)
		return
	}
	m.publishStarted(ctx, grant)
	c.JSON(http.StatusOK, gin.H{
		"grant":        postedGrant(grant),
		"access_token": token.Value,
		"token_type":   "Bearer",
		"expires_in":   int64(token.ExpiresAt.Sub(m.clock.Now()) / time.Second),
	})
}

// handleStatus answers GET /impersonation: the impersonation in force on the
// caller's session, or 404 when there is none still valid.
func (m *product) handleStatus(c *gin.Context) {
	ctx := c.Request.Context()
	id, ok := authn.FromContext(ctx)
	if !ok {
		mount.Unauthorized(c, m.mounter.Endpoint(), "")
		return
	}
	if id.Session() == "" {
		m.notFound(c)
		return
	}
	g, found, err := m.store.unendedForSession(ctx, id.Session())
	if err != nil {
		m.routeFailure(c, "reading the impersonation of a session", err)
		return
	}
	if !found {
		m.notFound(c)
		return
	}
	g, active, err := m.Active(ctx, g.ID)
	if err != nil {
		m.routeFailure(c, "reading an impersonation record", err)
		return
	}
	if !active {
		m.notFound(c)
		return
	}
	c.JSON(http.StatusOK, gin.H{"grant": currentGrant(g)})
}

// handleEnd answers DELETE /impersonation: it ends the record on the caller's
// session, then, under a cookie, clears the session's marker without rotating
// the token or writing a cookie, so the caller's cookie stays valid with the
// initiator's own identity. A failure to clear the marker does not bring the
// record back: the record has ended and a verifying backend reads Active as
// false.
func (m *product) handleEnd(c *gin.Context) {
	ctx := c.Request.Context()
	id, ok := authn.FromContext(ctx)
	if !ok {
		mount.Unauthorized(c, m.mounter.Endpoint(), "")
		return
	}
	if err := m.End(ctx, id, ""); err != nil {
		if errors.Is(err, authn.ErrNotImpersonating) {
			m.notFound(c)
			return
		}
		m.routeFailure(c, "ending an impersonation", err)
		return
	}
	if id.Backend() == sessionBackend && id.Session() != "" {
		if _, err := m.sessions.SetImpersonation(ctx, id.Session(), ""); err != nil {
			m.logger.Error(ModuleName+": clearing the impersonation marker of a session failed",
				slog.String("cause", err.Error()))
		}
	}
	c.Status(http.StatusNoContent)
	c.Writer.WriteHeaderNow()
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

// answerStart writes the answer for an error Start returned.
func (m *product) answerStart(c *gin.Context, id authn.Identity, err error) {
	switch {
	case errors.Is(err, authn.ErrStepUpRequired):
		mount.StepUpRequired(c, id, authn.Requirement{Assurance: authn.AssuranceMulti, MaxAge: m.settings.maxAuthAge(), Local: true})
	case errors.Is(err, authn.ErrFactorNotSeasoned):
		mount.Forbidden(c, mount.CodeFactorNotSeasoned)
	case errors.Is(err, authn.ErrForbidden):
		mount.Forbidden(c, mount.CodeForbidden)
	case errors.Is(err, authn.ErrImpersonationRestricted), errors.Is(err, authn.ErrImpersonationNeedsSession):
		mount.Forbidden(c, mount.CodeImpersonationRestricted)
	case errors.Is(err, authn.ErrImpersonationActive):
		mount.Fail(c, mount.CodeConflict)
	default:
		m.routeFailure(c, "starting an impersonation", err)
	}
}

// notFound answers a status or an end that found no record in force: a bare 404,
// which the design states for both routes and which carries no problem code of
// this capability.
func (m *product) notFound(c *gin.Context) {
	c.Status(http.StatusNotFound)
	c.Writer.WriteHeaderNow()
}

// routeFailure answers a failure of the store: 503, and the cause in the log,
// never in the response.
func (m *product) routeFailure(c *gin.Context, op string, err error) {
	m.logger.Error(ModuleName+": an impersonation route failed",
		slog.String("operation", op), slog.String("cause", err.Error()))
	mount.Unavailable(c)
}
