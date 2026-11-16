package invite

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

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

// throttledFailure is the platform-standard 429 of a spent send or probe
// budget: the problem type the rate-limit layer writes, and a Retry-After that
// names the whole seconds until the window opens again. The header is declared
// on every response that can carry it, so the entry point accepts it here.
func (p *product) throttledFailure(err error) error {
	retry := time.Second
	var throttled *authn.ThrottledError
	if errors.As(err, &throttled) && throttled.RetryAfter > 0 {
		retry = throttled.RetryAfter
	}
	seconds := int64((retry + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	return speedhttp.Fault{
		Status: http.StatusTooManyRequests,
		Type:   typePrefix + mount.CodeThrottled,
		Headers: []speedhttp.HeaderValue{{
			Name:  "Retry-After",
			Value: strconv.FormatInt(seconds, 10),
		}},
	}
}

// badRequest is a request this module refuses on shape alone: a path parameter
// or a body it could not read.
func (p *product) badRequest() error {
	return inviteFailure(http.StatusBadRequest, mount.CodeBadRequest)
}

// reservedRequest is the refusal of a username the system keeps for itself: a
// 400 of its own, so the caller can tell it from a malformed request.
func (p *product) reservedRequest() error {
	return inviteFailure(http.StatusBadRequest, mount.CodeUsernameReserved)
}

// invalidUsername is the refusal of a username that does not normalize: a 400
// of its own, so the caller can point at the field.
func (p *product) invalidUsername() error {
	return inviteFailure(http.StatusBadRequest, mount.CodeUsernameInvalid)
}

// caller reads the identity the gateway put on the request. The management
// routes are guarded and the acceptance write is optional, so a handler reached
// without one refuses rather than reading an anonymous request as a caller. The
// challenge is the fault's declared header, so a handler reached without the
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
// is a 400, an ineligible target or a deadline past the longest lifetime is the
// 400 of its own, a pending address or an address that is already a member is
// the 409 of its own, a terminal invitation is the management face's 409
// invitation-terminal, and any other failure is a 503 whose cause stays in the
// log. A missing invitation is a bare 404, which a simple operation cannot
// write; answerReply handles it for the routes that declare one.
func (p *product) answer(op string, err error) error {
	switch {
	case errors.Is(err, authn.ErrThrottled):
		return p.throttledFailure(err)
	case authn.IsUsernameShape(err):
		return p.invalidUsername()
	case errors.Is(err, ErrInvalidEmail), errors.Is(err, authn.ErrInvalidArgument),
		errors.Is(err, authn.ErrInvalidIdentifier):
		return p.badRequest()
	case errors.Is(err, authn.ErrUsernameReserved):
		return p.reservedRequest()
	case errors.Is(err, ErrInvitationTargetInvalid):
		return inviteFailure(http.StatusBadRequest, mount.CodeInvitationTargetInvalid)
	case errors.Is(err, ErrInvitationExpiryTooLong):
		return inviteFailure(http.StatusBadRequest, mount.CodeInvitationExpiryTooLong)
	case errors.Is(err, authn.ErrRoleUnknown):
		return inviteFailure(http.StatusBadRequest, mount.CodeInvitationRoleUnknown)
	case errors.Is(err, authn.ErrRoleProtected):
		return inviteFailure(http.StatusForbidden, mount.CodeInvitationRoleProtected)
	case errors.Is(err, authn.ErrForbidden):
		return inviteFailure(http.StatusForbidden, mount.CodeForbidden)
	case errors.Is(err, ErrInvitationMember):
		return inviteFailure(http.StatusConflict, mount.CodeInvitationMember)
	case errors.Is(err, ErrInvitationPending):
		return inviteFailure(http.StatusConflict, mount.CodeInvitationPending)
	case errors.Is(err, ErrInvitationExpired), errors.Is(err, ErrInvitationRevoked),
		errors.Is(err, ErrInvitationAlreadyAccepted):
		return inviteFailure(http.StatusConflict, mount.CodeInvitationTerminal)
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
// at no invitation is the declared bare 404, a request with no caller is a 401
// with the capability's challenge, a caller who is not a user account is a 403
// invitation-target-invalid, a caller who has not proved the invited address is
// a 403 of its own, a suspended member is a 409 of its own, a
// disabled tenant is the 409 of its own, one that can no longer be redeemed —
// expired, revoked, already accepted, or carrying a role that can no longer be
// granted — is 409 invitation-terminal, a bad argument is a 400, and any other
// failure is a 503 whose cause stays in the log. The acceptance face tells those
// terminal states apart from the management face, which only meets an accepted
// invitation when it revokes one.
func (p *product) acceptAnswerReply(op string, err error) (speedhttp.Reply, error) {
	var fault speedhttp.Fault
	switch {
	case errors.As(err, &fault):
		// A fault the acceptance already built, such as the 503 of an audit row
		// that could not be written, is answered as it stands.
		return nil, fault
	case errors.Is(err, ErrInvitationNotFound):
		return speedhttp.EmptyReply(http.StatusNotFound), nil
	case errors.Is(err, ErrInvitationUnauthenticated):
		return nil, speedhttp.Fault{
			Status: http.StatusUnauthorized,
			Headers: []speedhttp.HeaderValue{{
				Name:  "WWW-Authenticate",
				Value: mount.UnauthorizedChallenge(p.mounter.Endpoint()),
			}},
		}
	case errors.Is(err, ErrInvitationEmailUnproved):
		return nil, inviteFailure(http.StatusForbidden, mount.CodeInvitationEmailUnproved)
	case errors.Is(err, ErrInvitationTargetInvalid):
		// The caller is authenticated but is not a user account. The code is the
		// issuing face's "this target cannot be invited", answered 400 there and
		// 403 here: at acceptance the caller is known, so the refusal is about the
		// account, not about the credential.
		return nil, inviteFailure(http.StatusForbidden, mount.CodeInvitationTargetInvalid)
	case errors.Is(err, ErrInvitationMembershipInactive):
		return nil, inviteFailure(http.StatusConflict, mount.CodeMembershipInactive)
	case errors.Is(err, ErrInvitationTenantInactive):
		return nil, inviteFailure(http.StatusConflict, mount.CodeInvitationTenantInactive)
	case errors.Is(err, authn.ErrThrottled):
		return nil, p.throttledFailure(err)
	case errors.Is(err, ErrInvitationExpired), errors.Is(err, ErrInvitationRevoked),
		errors.Is(err, ErrInvitationAlreadyAccepted), errors.Is(err, ErrInvitationRoleWithdrawn):
		return nil, inviteFailure(http.StatusConflict, mount.CodeInvitationTerminal)
	case authn.IsUsernameShape(err):
		return nil, p.invalidUsername()
	case errors.Is(err, authn.ErrInvalidArgument), errors.Is(err, authn.ErrInvalidIdentifier):
		return nil, p.badRequest()
	case errors.Is(err, authn.ErrUsernameReserved):
		return nil, p.reservedRequest()
	case errors.Is(err, authn.ErrAccountExists):
		return nil, inviteFailure(http.StatusConflict, mount.CodeUsernameTaken)
	default:
		return nil, p.cause(op, err)
	}
}
