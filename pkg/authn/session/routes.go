package session

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

// The relative paths of the routes, joined to the mounter's base path.
const (
	sessionPath  = "/session"
	logoutPath   = "/logout"
	sessionsPath = "/sessions"
	sessionByID  = "/sessions/:id"
)

// The seam-level audit actions and outcome of the self-service revocation
// routes, taken from the capability's closed action table. Both actions are in
// the best-effort tier: a row that cannot be written is an Error and never
// changes the route's outcome. The object is the session for the routes that
// name one and the account for the route that revokes the others, which is the
// same split the management surface uses for the same action.
const (
	actionLogout        = "authn.logout"
	actionSessionRevoke = "authn.session.revoke"
	outcomeSuccess      = "success"
)

// recordAudit writes one best-effort seam-level audit row. A failure is an
// Error and never changes the outcome of the route, and an assembly with no
// audit capability writes nothing.
func (p *product) recordAudit(ctx context.Context, e audit.Entry) {
	if p.audit == nil {
		return
	}
	if err := p.audit.Record(ctx, e); err != nil {
		p.logger.Error(ModuleName+": an audit row of a self-service session change could not be written",
			slog.String("action", e.Action), slog.String("cause", err.Error()))
	}
}

// mountRoutes registers the five self-service routes through the mounter. The
// route and the access level the gateway applies to it are registered in one
// call, and the mounter puts every response under Cache-Control: no-store and
// every unsafe method behind the cross-origin check.
//
// GET /session may be asked without a credential and answers that nobody is
// logged in; a credential that does not hold is still a 401. POST /logout and
// GET /session are open to an identity that is impersonating, and the three
// routes that list and revoke sessions are not registered for it: the gateway
// refuses an impersonating identity there, and each handler refuses it again.
func (p *product) mountRoutes() {
	p.mounter.Optional(http.MethodGet, sessionPath, p.handleSession, mount.Impersonable())
	p.mounter.Guarded(http.MethodPost, logoutPath, p.handleLogout, mount.Impersonable())
	p.mounter.Guarded(http.MethodGet, sessionsPath, p.handleSessions)
	p.mounter.Guarded(http.MethodDelete, sessionsPath, p.handleRevokeOthers)
	p.mounter.Guarded(http.MethodDelete, sessionByID, p.handleRevokeOne)
}

// sessionView is the body of GET /session for an identity.
type sessionView struct {
	Authenticated bool           `json:"authenticated"`
	SessionID     string         `json:"session_id,omitempty"`
	AccountID     string         `json:"account_id,omitempty"`
	ActorID       string         `json:"actor_id,omitempty"`
	Impersonating bool           `json:"impersonating,omitempty"`
	Methods       []authn.Method `json:"methods,omitempty"`
	Assurance     string         `json:"assurance,omitempty"`
	AuthTime      *time.Time     `json:"auth_time,omitempty"`
	Provider      string         `json:"provider,omitempty"`
}

// handleSession answers who is logged in on this session. With no credential the
// answer is {"authenticated": false}, so a front end can ask on startup without
// a 401 in its console.
func (p *product) handleSession(c *gin.Context) {
	id, ok := authn.FromContext(c.Request.Context())
	if !ok {
		c.JSON(http.StatusOK, sessionView{Authenticated: false})
		return
	}
	view := sessionView{
		Authenticated: true,
		SessionID:     string(id.Session()),
		AccountID:     string(id.Account()),
		Methods:       id.Methods(),
		Assurance:     assuranceName(id.Assurance()),
		Provider:      id.Provider(),
	}
	if t := id.AuthTime(); !t.IsZero() {
		utc := t.UTC()
		view.AuthTime = &utc
	}
	if id.Impersonating() {
		view.Impersonating = true
		view.ActorID = string(id.Actor())
	}
	c.JSON(http.StatusOK, view)
}

// assuranceName is the level's name, which is what the wire carries and not its
// number.
func assuranceName(a authn.Assurance) string {
	switch a {
	case authn.AssuranceSingle:
		return "single"
	case authn.AssuranceMulti:
		return "multi"
	}
	return "none"
}

// handleLogout revokes the session the request came with and clears the cookie.
// For an impersonating identity the session is the administrator's own, which is
// the one the request's credential is, and the impersonation ends with it.
func (p *product) handleLogout(c *gin.Context) {
	id, ok := authn.FromContext(c.Request.Context())
	if !ok || id.Session() == "" {
		mount.Fail(c, mount.CodeBadRequest)
		return
	}
	if err := p.Revoke(c.Request.Context(), id.Session(), authn.ReasonSelf); err != nil {
		p.routeFailure(c, "logout", err)
		return
	}
	p.recordAudit(c.Request.Context(), audit.Entry{
		Action:  actionLogout,
		Object:  string(id.Session()),
		Outcome: outcomeSuccess,
	})
	p.ClearCookie(c.Writer)
	c.Status(http.StatusNoContent)
}

// sessionItem is one session in the listing.
type sessionItem struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	ClientIP   string    `json:"client_ip"`
	UserAgent  string    `json:"user_agent"`
	Current    bool      `json:"current"`
}

// handleSessions lists the caller's own live browser and token sessions.
func (p *product) handleSessions(c *gin.Context) {
	id, ok := p.ownSessionCaller(c)
	if !ok {
		return
	}
	rows, err := p.liveRows(c.Request.Context(), id.Account(), p.now(), "created_at DESC, id DESC")
	if err != nil {
		p.routeFailure(c, "listing sessions", err)
		return
	}
	items := make([]sessionItem, len(rows))
	for i, r := range rows {
		items[i] = sessionItem{
			ID: r.ID, Kind: r.Kind, CreatedAt: r.CreatedAt, LastSeenAt: r.LastSeenAt,
			ExpiresAt: r.ExpiresAt.Time, ClientIP: r.ClientIP, UserAgent: r.UserAgent,
			Current: r.ID == string(id.Session()),
		}
	}
	c.JSON(http.StatusOK, gin.H{"sessions": items})
}

// handleRevokeOthers revokes every session of the caller's but the current one.
func (p *product) handleRevokeOthers(c *gin.Context) {
	id, ok := p.ownSessionCaller(c)
	if !ok {
		return
	}
	n, err := p.RevokeAccount(c.Request.Context(), id.Account(), id.Session(), authn.ReasonSelf)
	if err != nil {
		p.routeFailure(c, "revoking the other sessions", err)
		return
	}
	p.recordAudit(c.Request.Context(), audit.Entry{
		Action:  actionSessionRevoke,
		Object:  string(id.Account()),
		Outcome: outcomeSuccess,
	})
	c.JSON(http.StatusOK, gin.H{"revoked": n})
}

// handleRevokeOne revokes one of the caller's own sessions. A session that is
// not the caller's and one that does not exist are answered alike, so that the
// route does not say which identifiers are in use.
func (p *product) handleRevokeOne(c *gin.Context) {
	id, ok := p.ownSessionCaller(c)
	if !ok {
		return
	}
	target := authn.SessionID(c.Param("id"))
	info, err := p.Get(c.Request.Context(), target)
	switch {
	case errors.Is(err, authn.ErrSessionNotFound):
		mount.Forbidden(c, mount.CodeForbidden)
		return
	case err != nil:
		p.routeFailure(c, "reading a session", err)
		return
	case info.Account != id.Account() || info.Kind == authn.SessionService:
		mount.Forbidden(c, mount.CodeForbidden)
		return
	}
	if err := p.Revoke(c.Request.Context(), target, authn.ReasonSelf); err != nil {
		p.routeFailure(c, "revoking a session", err)
		return
	}
	p.recordAudit(c.Request.Context(), audit.Entry{
		Action:  actionSessionRevoke,
		Object:  string(target),
		Outcome: outcomeSuccess,
	})
	if target == id.Session() {
		p.ClearCookie(c.Writer)
	}
	c.Status(http.StatusNoContent)
}

// ownSessionCaller reads the caller of a route that lists or revokes sessions and
// refuses one that is impersonating: the gateway already does, as the route is not
// registered for it, and this is the same refusal where the route is reached by a
// path that does not go through the gateway.
func (p *product) ownSessionCaller(c *gin.Context) (authn.Identity, bool) {
	id, ok := authn.FromContext(c.Request.Context())
	if !ok {
		mount.Unauthorized(c, p.mounter.Endpoint(), "")
		return authn.Identity{}, false
	}
	if id.Impersonating() {
		mount.Forbidden(c, mount.CodeImpersonationRestricted)
		return authn.Identity{}, false
	}
	return id, true
}

// routeFailure answers a failure of the store: 503, and the cause in the log,
// never in the response.
func (p *product) routeFailure(c *gin.Context, op string, err error) {
	p.logger.Error(diagnosticPrefix+ModuleName+": a session route failed", slog.String("operation", op),
		slog.Any("error", err))
	mount.Unavailable(c)
}
