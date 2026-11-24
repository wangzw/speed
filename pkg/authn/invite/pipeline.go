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

const typePrefix = "urn:speed:authn:"

func inviteFailure(status int, code string) error {
	return speedhttp.Fault{Status: status, Type: typePrefix + code}
}

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

func (p *product) badRequest() error {
	return inviteFailure(http.StatusBadRequest, mount.CodeBadRequest)
}

func (p *product) reservedRequest() error {
	return inviteFailure(http.StatusBadRequest, mount.CodeUsernameReserved)
}

func (p *product) invalidUsername() error {
	return inviteFailure(http.StatusBadRequest, mount.CodeUsernameInvalid)
}

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

func (p *product) admit() error {
	if p.decider == nil {
		return inviteFailure(http.StatusForbidden, mount.CodeForbidden)
	}
	return nil
}

func (p *product) gateSignup(ctx context.Context, id authn.Identity) error {
	actor := id.Actor()
	account, err := p.accounts.Get(ctx, actor)
	switch {
	case errors.Is(err, authn.ErrAccountNotFound):
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

func (p *product) auditRequired(ctx context.Context, e audit.Entry) error {
	if err := p.audit.Record(ctx, e); err != nil {
		p.logger.Error(ModuleName+": the mandatory audit row could not be written, so the action was not carried out",
			slog.String("action", e.Action), slog.String("cause", err.Error()))
		return inviteFailure(http.StatusServiceUnavailable, mount.CodeUnavailable)
	}
	return nil
}

func (p *product) auditBestEffort(ctx context.Context, e audit.Entry) {
	if err := p.audit.Record(ctx, e); err != nil {
		p.logger.Error(ModuleName+": an audit row could not be written",
			slog.String("action", e.Action), slog.String("cause", err.Error()))
	}
}

func (p *product) cause(op string, err error) error {
	p.logger.Error(ModuleName+": "+op+" failed", slog.String("cause", err.Error()))
	return inviteFailure(http.StatusServiceUnavailable, mount.CodeUnavailable)
}

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

func (p *product) answerReply(op string, err error) (speedhttp.Reply, error) {
	if errors.Is(err, ErrInvitationNotFound) {
		return speedhttp.EmptyReply(http.StatusNotFound), nil
	}
	return nil, p.answer(op, err)
}

func (p *product) acceptAnswerReply(op string, err error) (speedhttp.Reply, error) {
	var fault speedhttp.Fault
	switch {
	case errors.As(err, &fault):
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
