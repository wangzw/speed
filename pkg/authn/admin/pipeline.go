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

// typePrefix starts the Problem.Type of every failure this capability's
// handlers write, the code following it being one of mount's closed table.
const typePrefix = "urn:speed:authn:"

// errNotFound marks a capability failure that means the object the route names
// is not there. A management route answers it as a bare 404, which carries no
// problem document; the reply handlers turn it into one, and a simple handler,
// whose operation cannot write a bare 404, never produces it.
var errNotFound = errors.New("authn.admin: no such object")

// authnFailure is a refusal of one of the capability's problem codes: the entry
// point writes it as a problem document of the code's status and type.
func authnFailure(status int, code string) error {
	return speedhttp.Fault{Status: status, Type: typePrefix + code}
}

// caller reads the identity the gateway put on the request. The routes are
// guarded, so a request that reaches a handler carries one; an absent identity
// is answered with the gateway's own 401, challenge included, rather than read
// as a caller.
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

// admit applies the two checks every management route makes before it reads
// anything: the actor is not impersonating, and a Decider is there. It answers a
// refusal as an error, so that a deployment with no Decider tells a caller
// nothing about which objects exist.
func (m *product) admit(id authn.Identity) error {
	if id.Impersonating() {
		return authnFailure(http.StatusForbidden, mount.CodeImpersonationRestricted)
	}
	if m.decider == nil {
		return authnFailure(http.StatusForbidden, mount.CodeForbidden)
	}
	return nil
}

// gateSignup refuses a management write an unverified self-registration makes.
// It sits after the caller and the object's ownership are known and before the
// Decider, the strength requirement and the mandatory audit: a refused request
// asks no Decider, asks for no strength, writes no audit row and calls no
// capability. A service account and an account created outside self-registration
// are exempt; a state that cannot be read is a 503 and is never read as verified.
func (m *product) gateSignup(ctx context.Context, id authn.Identity) error {
	account, err := m.accounts.Get(ctx, id.Account())
	switch {
	case errors.Is(err, authn.ErrAccountNotFound):
		// A static password-table subject is no account row and never a
		// self-registration; the rest of the pipeline answers for it.
		return nil
	case err != nil:
		return m.unavailable("reading the acting account", fmt.Errorf("%w: %w", authn.ErrUnavailable, err))
	}
	if account.Kind != authn.KindUser {
		return nil
	}
	status, err := m.verification.SignupStateOf(ctx, id.Account())
	if err != nil {
		return m.unavailable("reading the signup state", fmt.Errorf("%w: %w", authn.ErrUnavailable, err))
	}
	if status.State == authn.SignupPending {
		return authnFailure(http.StatusForbidden, mount.CodeContactRequired)
	}
	return nil
}

// asks puts one Decision to the Decider. A refusal is a 403 and an error or a
// panic is a 503; either is returned as the fault the entry point writes.
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

// callDecider calls Decide and turns a returned error and a panic alike into
// ErrUnavailable, so that a defective Decider is refused rather than taken for a
// permission or a decline, and never takes the request down with it.
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

// require holds the actor to a strength requirement and returns the step-up 401
// when it falls short. A zero requirement asks for nothing.
func (m *product) require(id authn.Identity, req authn.Requirement) error {
	if req == (authn.Requirement{}) {
		return nil
	}
	if id.Satisfies(req, m.now()) {
		return nil
	}
	return stepUpRequired(id, req)
}

// stepUpRequired is the 401 of an identity that does not meet a requirement:
// the problem type insufficient_user_authentication and, in WWW-Authenticate,
// the scheme the identity authenticated with followed by the level, the
// freshness and the local-only flag the requirement names.
//
// The challenge is built here rather than through the capability's writer
// because a typed handler has no *gin.Context to hand it; the shape is the one
// that writer produces, byte for byte.
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

// stepUpChallenge renders the step-up challenge: RFC 9470's parameters carried
// on the scheme the identity authenticated with, which is this capability's
// convention on Cookie and Basic as well as on Bearer.
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

// stepUpDescription is the error_description of the step-up challenge. It names
// no requirement of the route and nothing of the account.
const stepUpDescription = "A stronger or more recent authentication is required"

// challengeScheme returns scheme when it is an RFC 9110 token, and Bearer
// otherwise. The gateway stamps Identity.Challenge, so an odd value is a zero
// identity or a defect, and neither may put a character into a header that ends
// the field.
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

// auditRequired writes the mandatory audit row of a write, outside any
// transaction and before the action. A row that does not land is the module's
// own failure or the audit module's state, and the action is not carried out:
// the answer is 503.
func (m *product) auditRequired(ctx context.Context, e audit.Entry) error {
	if err := m.audit.Record(ctx, e); err != nil {
		m.logger.Error(ModuleName+": the mandatory audit row could not be written, so the action was not carried out",
			slog.String("action", e.Action), slog.String("cause", err.Error()))
		return authnFailure(http.StatusServiceUnavailable, mount.CodeUnavailable)
	}
	return nil
}

// auditBestEffort writes an audit row that does not block the action. A failure
// is an Error line and changes nothing about the answer.
func (m *product) auditBestEffort(ctx context.Context, e audit.Entry) {
	if err := m.audit.Record(ctx, e); err != nil {
		m.logger.Error(ModuleName+": an audit row could not be written",
			slog.String("action", e.Action), slog.String("cause", err.Error()))
	}
}

// answer maps a failure a capability returned to the refusal it belongs to. A
// missing object is errNotFound, which a reply handler writes as a bare 404; a
// failure the route did not cause that no sentinel names is a 503.
func (m *product) answer(op string, err error) error {
	switch {
	case errors.Is(err, authn.ErrAccountNotFound), errors.Is(err, authn.ErrSessionNotFound),
		errors.Is(err, authn.ErrProviderNotFound), errors.Is(err, authn.ErrNotImpersonating):
		return errNotFound
	case errors.Is(err, authn.ErrInvalidArgument), errors.Is(err, authn.ErrInvalidIdentifier),
		errors.Is(err, authn.ErrPasswordPolicy), errors.Is(err, authn.ErrInvalidConfig):
		return authnFailure(http.StatusBadRequest, mount.CodeBadRequest)
	case errors.Is(err, authn.ErrIdentifierRequired):
		return authnFailure(http.StatusConflict, mount.CodeLoginIdentifierRequired)
	case errors.Is(err, authn.ErrContactTaken):
		return authnFailure(http.StatusConflict, mount.CodeEmailTaken)
	case errors.Is(err, authn.ErrAccountExists), errors.Is(err, authn.ErrBindingConflict),
		errors.Is(err, authn.ErrDomainClaimed), errors.Is(err, authn.ErrLastLoginMethod),
		errors.Is(err, authn.ErrFactorLimit), errors.Is(err, authn.ErrLastFactor):
		return authnFailure(http.StatusConflict, mount.CodeConflict)
	case errors.Is(err, authn.ErrUnsupportedRegion):
		return authnFailure(http.StatusBadRequest, mount.CodeUnsupportedRegion)
	default:
		m.logger.Error(ModuleName+": "+op+" failed", slog.String("cause", err.Error()))
		return authnFailure(http.StatusServiceUnavailable, mount.CodeUnavailable)
	}
}

// replyFailure maps a capability failure to the answer of a reply operation: a
// missing object is a bare 404, every other refusal is the fault answer returns.
func (m *product) replyFailure(op string, err error) (speedhttp.Reply, error) {
	mapped := m.answer(op, err)
	if errors.Is(mapped, errNotFound) {
		return speedhttp.EmptyReply(http.StatusNotFound), nil
	}
	return nil, mapped
}

// bad answers a body or a query parameter the route refuses: a 400 with this
// capability's code. It is for shape problems the handler judges itself, ahead
// of any capability call.
func (m *product) bad() error {
	return authnFailure(http.StatusBadRequest, mount.CodeBadRequest)
}

// unavailable answers a failure of a dependency: a 503 whose cause stays in the
// log and never reaches the caller.
func (m *product) unavailable(op string, err error) error {
	m.logger.Error(ModuleName+": "+op+" failed", slog.String("cause", err.Error()))
	return authnFailure(http.StatusServiceUnavailable, mount.CodeUnavailable)
}
