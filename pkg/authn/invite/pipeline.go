package invite

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// typePrefix starts the Problem.Type of every failure this capability's
// handlers write, the code following it being one of mount's closed table.
const typePrefix = "urn:speed:authn:"

// inviteFailure is a refusal of one of the capability's problem codes: the entry
// point writes it as a problem document of the code's status and type.
func inviteFailure(status int, code string) error {
	return speedhttp.Fault{Status: status, Type: typePrefix + code}
}

// badRequest is a request this module refuses on shape alone: a path parameter
// or a body it could not read.
func (p *product) badRequest() error {
	return inviteFailure(http.StatusBadRequest, mount.CodeBadRequest)
}

// caller reads the identity the gateway put on the request. The management
// routes are guarded, so a request that reaches a handler carries one; an absent
// identity is answered with the gateway's own 401 rather than read as a caller.
// The challenge is the fault's declared header, so a handler reached without the
// gateway answers the same 401 the gateway would.
func (p *product) caller(ctx context.Context) (authn.Identity, error) {
	id, ok := authn.FromContext(ctx)
	if !ok {
		return authn.Identity{}, speedhttp.Fault{
			Status: http.StatusUnauthorized,
			Headers: []speedhttp.HeaderValue{{
				Name:  "WWW-Authenticate",
				Value: mount.UnauthorizedChallenge(p.mounter.Endpoint()),
			}},
		}
	}
	return id, nil
}

// admit applies the check every management route makes before it reads
// anything: a Decider is there. The management face is decided in the path's
// tenant, so an impersonating identity is admitted and decided by the effective
// account's permissions there; the invitation routes are not a platform-domain
// surface. It answers a refusal as an error, so that a deployment with no
// Decider tells a caller nothing about which invitations exist.
func (p *product) admit() error {
	if p.decider == nil {
		return inviteFailure(http.StatusForbidden, mount.CodeForbidden)
	}
	return nil
}

// gateSignup refuses a management write an operator with an unproved email
// address makes. It runs after the caller is known and before the Decider and
// the mandatory audit, so a refused request asks no seam, writes no row and
// calls no Invites method. The subject is the operator, not the effective
// account: an impersonated target's own verification says nothing about the
// address the operator's hands proved. A service account and an account with no
// email address are exempt, having nothing to verify; a state that cannot be
// read is a 503 and never read as verified.
func (p *product) gateSignup(ctx context.Context, id authn.Identity) error {
	actor := id.Actor()
	account, err := p.accounts.Get(ctx, actor)
	switch {
	case errors.Is(err, authn.ErrAccountNotFound):
		// A static password-table subject is no account row and holds no email
		// address.
		return nil
	case err != nil:
		return p.cause("reading the acting account", fmt.Errorf("%w: %w", authn.ErrUnavailable, err))
	}
	if account.Kind != authn.KindUser {
		return nil
	}
	status, err := p.verification.SignupStateOf(ctx, actor)
	if err != nil {
		return p.cause("reading the signup state", fmt.Errorf("%w: %w", authn.ErrUnavailable, err))
	}
	if status.State == authn.SignupPending {
		return inviteFailure(http.StatusForbidden, mount.CodeContactRequired)
	}
	return nil
}

// asks puts one Decision to the Decider. It returns nil only when the action is
// allowed; a refusal is a 403 and an error or a panic is a 503.
func (p *product) asks(ctx context.Context, d authn.Decision) error {
	allow, err := p.callDecider(ctx, d)
	switch {
	case err != nil:
		return p.cause("asking the Decider", err)
	case !allow:
		return inviteFailure(http.StatusForbidden, mount.CodeForbidden)
	}
	return nil
}

// callDecider calls Decide and turns a returned error and a panic alike into
// ErrUnavailable, so a defective Decider is refused rather than taken for a
// permission or a decline.
func (p *product) callDecider(ctx context.Context, d authn.Decision) (allow bool, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			allow, err = false, fmt.Errorf("%w: the Decider panicked", authn.ErrUnavailable)
		}
	}()
	allow, err = p.decider.Decide(ctx, d)
	if err != nil {
		return false, fmt.Errorf("%w: the Decider could not answer", authn.ErrUnavailable)
	}
	return allow, nil
}

// auditRequired writes the mandatory audit row of a write, outside any
// transaction and before the action. A row that does not land is answered 503
// and the action is not carried out.
func (p *product) auditRequired(ctx context.Context, e audit.Entry) error {
	if err := p.audit.Record(ctx, e); err != nil {
		p.logger.Error(ModuleName+": the mandatory audit row could not be written, so the action was not carried out",
			slog.String("action", e.Action), slog.String("cause", err.Error()))
		return inviteFailure(http.StatusServiceUnavailable, mount.CodeUnavailable)
	}
	return nil
}

// auditBestEffort writes an audit row that does not block the action. A failure
// is an Error line and changes nothing about the answer.
func (p *product) auditBestEffort(ctx context.Context, e audit.Entry) {
	if err := p.audit.Record(ctx, e); err != nil {
		p.logger.Error(ModuleName+": an audit row could not be written",
			slog.String("action", e.Action), slog.String("cause", err.Error()))
	}
}

// cause logs a failure and answers 503, never telling the caller which
// dependency failed.
func (p *product) cause(op string, err error) error {
	p.logger.Error(ModuleName+": "+op+" failed", slog.String("cause", err.Error()))
	return inviteFailure(http.StatusServiceUnavailable, mount.CodeUnavailable)
}

// answer maps a failure the capability returned to the response it belongs to.
// The mapping is closed and fail-closed: a malformed address or a bad argument
// is a 400, revoking an accepted invitation is a 409, and any other failure is a
// 503 whose cause stays in the log. A missing invitation is a bare 404, which a
// simple operation cannot write; answerReply handles it for the routes that
// declare one.
func (p *product) answer(op string, err error) error {
	switch {
	case errors.Is(err, ErrInvalidEmail), errors.Is(err, authn.ErrInvalidArgument),
		errors.Is(err, authn.ErrInvalidIdentifier):
		return p.badRequest()
	case errors.Is(err, ErrInvitationAlreadyAccepted):
		return inviteFailure(http.StatusConflict, mount.CodeConflict)
	default:
		return p.cause(op, err)
	}
}

// answerReply maps a failure of a route whose answers are built by the handler.
// An invitation that is not there is the declared bare 404 — no problem
// document, as the route has always answered it — and every other failure is
// the problem answer the mapping above names.
func (p *product) answerReply(op string, err error) (speedhttp.Reply, error) {
	if errors.Is(err, ErrInvitationNotFound) {
		return speedhttp.EmptyReply(http.StatusNotFound), nil
	}
	return nil, p.answer(op, err)
}

// acceptAnswerReply maps a failure of the acceptance face. A token that points
// at no invitation is the declared bare 404, one that can no longer be redeemed
// — expired, revoked or already accepted — is a 409, a bad argument is a 400,
// and any other failure is a 503 whose cause stays in the log. The acceptance
// face tells those terminal states apart from the management face, which only
// meets an accepted invitation when it revokes one.
func (p *product) acceptAnswerReply(op string, err error) (speedhttp.Reply, error) {
	switch {
	case errors.Is(err, ErrInvitationNotFound):
		return speedhttp.EmptyReply(http.StatusNotFound), nil
	case errors.Is(err, ErrInvitationExpired), errors.Is(err, ErrInvitationRevoked),
		errors.Is(err, ErrInvitationAlreadyAccepted):
		return nil, inviteFailure(http.StatusConflict, mount.CodeConflict)
	case errors.Is(err, authn.ErrInvalidArgument):
		return nil, p.badRequest()
	default:
		return nil, p.cause(op, err)
	}
}
