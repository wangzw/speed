package admin

import (
	"context"
	"errors"
	"fmt"
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

const typePrefix = "urn:speed:authn:"

const codeCannotSuspendSelf = "cannot-suspend-self"

var errNotFound = errors.New("authn.admin: no such object")

func authnFailure(status int, code string) error {
	return speedhttp.Fault{Status: status, Type: typePrefix + code}
}

func (m *product) caller(ctx context.Context) (authn.Identity, error) {
	id, ok := authn.FromContext(ctx)
	if !ok {
		return authn.Identity{}, speedhttp.Fault{
			Status: http.StatusUnauthorized,
			Headers: []speedhttp.HeaderValue{{
				Name:  "WWW-Authenticate",
				Value: mount.UnauthorizedChallenge(m.mounter.Endpoint()),
			}},
		}
	}
	return id, nil
}

type decisionDomain int

const (
	platformDomain decisionDomain = iota
	tenantDomain
)

func (m *product) admit(id authn.Identity, where decisionDomain) error {
	if where == platformDomain && id.Impersonating() {
		return authnFailure(http.StatusForbidden, mount.CodeImpersonationRestricted)
	}
	if m.decider == nil {
		return authnFailure(http.StatusForbidden, mount.CodeForbidden)
	}
	return nil
}

func (m *product) gateSignup(ctx context.Context, id authn.Identity) error {
	actor := id.Actor()
	account, err := m.accounts.Get(ctx, actor)
	switch {
	case errors.Is(err, authn.ErrAccountNotFound):
		return nil
	case err != nil:
		return m.unavailable("reading the acting account", fmt.Errorf("%w: %w", authn.ErrUnavailable, err))
	}
	if account.Kind != authn.KindUser {
		return nil
	}
	status, err := m.verification.SignupStateOf(ctx, actor)
	if err != nil {
		return m.unavailable("reading the signup state", fmt.Errorf("%w: %w", authn.ErrUnavailable, err))
	}
	if status.State == authn.SignupPending {
		return authnFailure(http.StatusForbidden, mount.CodeContactRequired)
	}
	return nil
}

func (m *product) asks(ctx context.Context, d authn.Decision) error {
	allow, err := m.callDecider(ctx, d)
	switch {
	case err != nil:
		m.logger.Error(ModuleName+": asking the Decider failed", slog.String("cause", err.Error()))
		return authnFailure(http.StatusServiceUnavailable, mount.CodeUnavailable)
	case !allow:
		return authnFailure(http.StatusForbidden, mount.CodeForbidden)
	}
	return nil
}

func (m *product) callDecider(ctx context.Context, d authn.Decision) (allow bool, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			allow, err = false, fmt.Errorf("%w: the Decider panicked", authn.ErrUnavailable)
		}
	}()
	allow, err = m.decider.Decide(ctx, d)
	if err != nil {
		return false, fmt.Errorf("%w: the Decider could not answer", authn.ErrUnavailable)
	}
	return allow, nil
}

func (m *product) require(id authn.Identity, req authn.Requirement) error {
	if req == (authn.Requirement{}) {
		return nil
	}
	if id.Satisfies(req, m.now()) {
		return nil
	}
	return stepUpRequired(id, req)
}

func stepUpRequired(id authn.Identity, r authn.Requirement) error {
	return speedhttp.Fault{
		Status: http.StatusUnauthorized,
		Type:   typePrefix + mount.CodeInsufficientUserAuthentication,
		Headers: []speedhttp.HeaderValue{{
			Name:  "WWW-Authenticate",
			Value: stepUpChallenge(id.Challenge(), r),
		}},
	}
}

func stepUpChallenge(scheme string, r authn.Requirement) string {
	var b strings.Builder
	b.WriteString(challengeScheme(scheme))
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

const stepUpDescription = "A stronger or more recent authentication is required"

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

// The mandatory audit is recorded before the action and outside any transaction:
// a caller's open transaction would block this write.
func (m *product) auditRequired(ctx context.Context, e audit.Entry) error {
	if err := m.audit.Record(ctx, e); err != nil {
		m.logger.Error(ModuleName+": the mandatory audit row could not be written, so the action was not carried out",
			slog.String("action", e.Action), slog.String("cause", err.Error()))
		return authnFailure(http.StatusServiceUnavailable, mount.CodeUnavailable)
	}
	return nil
}

func (m *product) auditBestEffort(ctx context.Context, e audit.Entry) {
	if err := m.audit.Record(ctx, e); err != nil {
		m.logger.Error(ModuleName+": an audit row could not be written",
			slog.String("action", e.Action), slog.String("cause", err.Error()))
	}
}

func (m *product) answer(ctx context.Context, op string, err error) error {
	var lastAdmin *authn.AccountLastTenantAdminError
	if errors.As(err, &lastAdmin) {
		name, nameErr := m.nameOf(ctx, lastAdmin.Tenant)
		if nameErr != nil {
			return authnFailure(http.StatusServiceUnavailable, mount.CodeUnavailable)
		}
		return speedhttp.Fault{
			Status: http.StatusConflict,
			Type:   typePrefix + mount.CodeAccountLastTenantAdmin,
			Detail: name,
		}
	}
	var owner *authn.AccountOwnsOrganizationError
	if errors.As(err, &owner) {
		name, nameErr := m.nameOf(ctx, owner.Tenant)
		if nameErr != nil {
			return authnFailure(http.StatusServiceUnavailable, mount.CodeUnavailable)
		}
		return speedhttp.Fault{
			Status: http.StatusConflict,
			Type:   typePrefix + mount.CodeAccountOrganizationOwner,
			Detail: name,
		}
	}
	switch {
	case errors.Is(err, authn.ErrAccountNotFound), errors.Is(err, authn.ErrSessionNotFound),
		errors.Is(err, authn.ErrProviderNotFound), errors.Is(err, authn.ErrNotImpersonating):
		return errNotFound
	case authn.IsUsernameShape(err):
		return authnFailure(http.StatusBadRequest, mount.CodeUsernameInvalid)
	case errors.Is(err, authn.ErrInvalidArgument), errors.Is(err, authn.ErrInvalidIdentifier),
		errors.Is(err, authn.ErrPasswordPolicy),
		errors.Is(err, authn.ErrInvalidConfig):
		return authnFailure(http.StatusBadRequest, mount.CodeBadRequest)
	case errors.Is(err, authn.ErrUsernameReserved):
		return authnFailure(http.StatusBadRequest, mount.CodeUsernameReserved)
	case errors.Is(err, authn.ErrIdentifierRequired):
		return authnFailure(http.StatusConflict, mount.CodeLoginIdentifierRequired)
	case errors.Is(err, authn.ErrUsernameImmutable):
		return authnFailure(http.StatusConflict, mount.CodeUsernameImmutable)
	case errors.Is(err, authn.ErrContactTaken):
		return authnFailure(http.StatusConflict, mount.CodeEmailTaken)
	case errors.Is(err, authn.ErrAccountExists), errors.Is(err, authn.ErrBindingConflict),
		errors.Is(err, authn.ErrDomainClaimed),
		errors.Is(err, authn.ErrLastTenantAdmin), errors.Is(err, authn.ErrFactorLimit),
		errors.Is(err, authn.ErrLastFactor):
		return authnFailure(http.StatusConflict, mount.CodeConflict)
	case errors.Is(err, authn.ErrLastLoginMethod):
		return authnFailure(http.StatusConflict, mount.CodeLastLoginMethod)
	case errors.Is(err, authn.ErrCannotSuspendSelf):
		return authnFailure(http.StatusConflict, codeCannotSuspendSelf)
	case errors.Is(err, authn.ErrUnsupportedRegion):
		return authnFailure(http.StatusBadRequest, mount.CodeUnsupportedRegion)
	default:
		m.logger.Error(ModuleName+": "+op+" failed", slog.String("cause", err.Error()))
		return authnFailure(http.StatusServiceUnavailable, mount.CodeUnavailable)
	}
}

func (m *product) replyFailure(ctx context.Context, op string, err error) (speedhttp.Reply, error) {
	mapped := m.answer(ctx, op, err)
	if errors.Is(mapped, errNotFound) {
		return speedhttp.EmptyReply(http.StatusNotFound), nil
	}
	return nil, mapped
}

func (m *product) bad() error {
	return authnFailure(http.StatusBadRequest, mount.CodeBadRequest)
}

func (m *product) unavailable(op string, err error) error {
	m.logger.Error(ModuleName+": "+op+" failed", slog.String("cause", err.Error()))
	return authnFailure(http.StatusServiceUnavailable, mount.CodeUnavailable)
}
