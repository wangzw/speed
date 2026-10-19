package session

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// The seam-level audit actions and outcome of the self-service revocation
// routes, taken from the capability's closed action table. Both actions are in
// the best-effort tier: a row that cannot be written is an Error and never
// changes the route's outcome. The object is the session the row is about, and a
// route that revokes several sessions writes one row per session, which is what
// the management surface does for the same action.
const (
	actionLogout        = "authn.logout"
	actionSessionRevoke = "authn.session.revoke"
	outcomeSuccess      = "success"
)

// typePrefix starts the Problem.Type of every failure this capability's
// handlers write, the code following it being one of mount's closed table.
const typePrefix = "urn:speed:authn:"

// cacheControlSetCookie is the directive a response that clears the session
// cookie carries. The mounter puts Cache-Control: no-store on every response
// already; this one is what the session library wrote beside it, and it keeps a
// cache from holding a response whose Set-Cookie it may ignore.
const cacheControlSetCookie = `no-cache="Set-Cookie"`

// authnFailure is a refusal of one of the capability's problem codes: the entry
// point writes it as a problem document of the code's status and type.
func authnFailure(status int, code string) error {
	return speedhttp.Fault{Status: status, Type: typePrefix + code}
}

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

// mountRoutes registers the five self-service routes through the mounter. Each
// route is registered from the operation that declares it, so the handler's
// input and answer types, the fragment and the route are one source. The route
// and the access level the gateway applies to it are registered in one call, and
// the mounter puts every response under Cache-Control: no-store and every unsafe
// method behind the cross-origin check.
//
// GET /session may be asked without a credential and answers that nobody is
// logged in; a credential that does not hold is still a 401. POST /logout and
// GET /session are open to an identity that is impersonating, and the three
// routes that list and revoke sessions are not registered for it: the gateway
// refuses an impersonating identity there, and each handler refuses it again.
func (p *product) mountRoutes() {
	p.mounter.Optional(http.MethodGet, mountRel(sessionOperation.Path),
		sessionOperation.Handle(p.handleSession), mount.Impersonable())
	p.mounter.Guarded(http.MethodPost, mountRel(logoutOperation.Path),
		logoutOperation.Handle(p.handleLogout), mount.Impersonable())
	p.mounter.Guarded(http.MethodGet, mountRel(listOperation.Path),
		listOperation.Handle(p.handleSessions))
	p.mounter.Guarded(http.MethodDelete, mountRel(revokeOthersOperation.Path),
		revokeOthersOperation.Handle(p.handleRevokeOthers))
	p.mounter.Guarded(http.MethodDelete, mountRel(revokeOneOperation.Path),
		revokeOneOperation.Handle(p.handleRevokeOne))
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
	owned := strings.TrimPrefix(mount.DefaultBasePath, sessionSpec.APIRoot())
	rel, found := strings.CutPrefix(declared, owned)
	if !found {
		panic(diagnosticPrefix + ModuleName + ": the declared route " + declared +
			" does not begin with the capability's mounted path " + owned)
	}
	return rel
}

// handleSession answers who is logged in on this session. With no credential the
// answer is {"authenticated": false}, so a front end can ask on startup without
// a 401 in its console.
func (p *product) handleSession(ctx context.Context, _ struct{}) (AuthnSessionView, error) {
	id, ok := authn.FromContext(ctx)
	if !ok {
		return AuthnSessionView{}, nil
	}
	view := AuthnSessionView{
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
	return view, nil
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
func (p *product) handleLogout(ctx context.Context, _ struct{}, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, ok := authn.FromContext(ctx)
	if !ok || id.Session() == "" {
		return nil, authnFailure(http.StatusBadRequest, mount.CodeBadRequest)
	}
	if err := p.Revoke(ctx, id.Session(), authn.ReasonSelf); err != nil {
		return nil, p.storeFailure("logout", err)
	}
	p.recordAudit(ctx, audit.Entry{
		Action:  actionLogout,
		Object:  string(id.Session()),
		Outcome: outcomeSuccess,
	})
	mount.AddResponseHeader(ctx, "Cache-Control", cacheControlSetCookie)
	return speedhttp.WithCookies(speedhttp.EmptyReply(http.StatusNoContent), p.clearingCookie()), nil
}

// handleSessions lists the caller's own live browser and token sessions.
func (p *product) handleSessions(ctx context.Context, _ struct{}) (AuthnSessionList, error) {
	id, err := p.ownSessionCaller(ctx)
	if err != nil {
		return AuthnSessionList{}, err
	}
	rows, err := p.liveRows(ctx, id.Account(), p.now(), "created_at DESC, id DESC")
	if err != nil {
		return AuthnSessionList{}, p.storeFailure("listing sessions", err)
	}
	items := make([]AuthnSessionItem, len(rows))
	for i, r := range rows {
		items[i] = AuthnSessionItem{
			ID: r.ID, Kind: r.Kind, CreatedAt: r.CreatedAt, LastSeenAt: r.LastSeenAt,
			ExpiresAt: r.ExpiresAt.Time, ClientIP: r.ClientIP, UserAgent: r.UserAgent,
			Current: r.ID == string(id.Session()),
		}
	}
	return AuthnSessionList{Sessions: items}, nil
}

// handleRevokeOthers revokes every session of the caller's but the current one.
func (p *product) handleRevokeOthers(ctx context.Context, _ struct{}) (AuthnSessionRevoked, error) {
	id, err := p.ownSessionCaller(ctx)
	if err != nil {
		return AuthnSessionRevoked{}, err
	}
	// The audit row of a revocation names the session it revoked, and the
	// statement reports the identifiers it ended, so the rows and the revocation
	// name one set rather than two readings of the table.
	revoked, err := p.RevokeAccount(ctx, id.Account(), id.Session(), authn.ReasonSelf)
	if err != nil {
		return AuthnSessionRevoked{}, p.storeFailure("revoking the other sessions", err)
	}
	for _, target := range revoked {
		p.recordAudit(ctx, audit.Entry{
			Action:  actionSessionRevoke,
			Object:  string(target),
			Outcome: outcomeSuccess,
		})
	}
	return AuthnSessionRevoked{Revoked: len(revoked)}, nil
}

// handleRevokeOne revokes one of the caller's own sessions. A session that is
// not the caller's and one that does not exist are answered alike, so that the
// route does not say which identifiers are in use.
func (p *product) handleRevokeOne(ctx context.Context, in revokeOneInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, err := p.ownSessionCaller(ctx)
	if err != nil {
		return nil, err
	}
	target := authn.SessionID(in.ID)
	info, err := p.Get(ctx, target)
	switch {
	case errors.Is(err, authn.ErrSessionNotFound):
		return nil, authnFailure(http.StatusForbidden, mount.CodeForbidden)
	case err != nil:
		return nil, p.storeFailure("reading a session", err)
	case info.Account != id.Account() || info.Kind == authn.SessionService:
		return nil, authnFailure(http.StatusForbidden, mount.CodeForbidden)
	}
	if err := p.Revoke(ctx, target, authn.ReasonSelf); err != nil {
		return nil, p.storeFailure("revoking a session", err)
	}
	p.recordAudit(ctx, audit.Entry{
		Action:  actionSessionRevoke,
		Object:  string(target),
		Outcome: outcomeSuccess,
	})
	reply := speedhttp.EmptyReply(http.StatusNoContent)
	if target == id.Session() {
		mount.AddResponseHeader(ctx, "Cache-Control", cacheControlSetCookie)
		reply = speedhttp.WithCookies(reply, p.clearingCookie())
	}
	return reply, nil
}

// ownSessionCaller reads the caller of a route that lists or revokes sessions and
// refuses one that is impersonating: the gateway already does, as the route is not
// registered for it, and this is the same refusal where the route is reached by a
// path that does not go through the gateway.
func (p *product) ownSessionCaller(ctx context.Context) (authn.Identity, error) {
	id, ok := authn.FromContext(ctx)
	if !ok {
		// The gateway refuses this first, with the capability's challenge. Where
		// the route is reached without the gateway the handler answers the same
		// challenge itself, as the fault's declared header, so its 401 is the one
		// RFC 9110 requires a 401 to carry rather than a bare problem document.
		return authn.Identity{}, speedhttp.Fault{
			Status: http.StatusUnauthorized,
			Headers: []speedhttp.HeaderValue{{
				Name:  "WWW-Authenticate",
				Value: mount.UnauthorizedChallenge(p.mounter.Endpoint()),
			}},
		}
	}
	if id.Impersonating() {
		return authn.Identity{}, authnFailure(http.StatusForbidden, mount.CodeImpersonationRestricted)
	}
	return id, nil
}

// storeFailure answers a failure of the store: 503, and the cause in the log,
// never in the response.
func (p *product) storeFailure(op string, err error) error {
	p.logger.Error(diagnosticPrefix+ModuleName+": a session route failed", slog.String("operation", op),
		slog.Any("error", err))
	return authnFailure(http.StatusServiceUnavailable, mount.CodeUnavailable)
}
