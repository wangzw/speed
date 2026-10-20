package mfa

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// maxFactorIDLen is the longest a factor identifier in a path may be before the
// request is refused without a read. The register's identifiers are UUID texts,
// far shorter, so a longer one names nothing.
const maxFactorIDLen = 64

// typePrefix starts the Problem.Type of every failure this capability's
// handlers write, the code following it being one of mount's closed table.
const typePrefix = "urn:speed:authn:"

// stepUpDescription is the error_description of the step-up challenge, the same
// text mount.StepUpRequired writes.
const stepUpDescription = "A stronger or more recent authentication is required"

// mountRoutes registers the routes. Both are guarded by the gateway, which is
// what makes them Required, and neither is registered Impersonable: the gateway
// refuses an impersonating identity on them, and the handler refuses one too, so
// the refusal does not depend on a single layer. Each route is registered from
// the operation that declares it, so the handler's input and answer types, the
// fragment and the route are one source.
func (m *product) mountRoutes() {
	mount.Declared(m.mounter, factorsOperation, mountRel(mfaSpec, factorsOperation.Path),
		factorsOperation.Handle(m.listFactors))
	mount.Declared(m.mounter, removeFactorOperation, mountRel(mfaSpec, removeFactorOperation.Path),
		removeFactorOperation.Handle(m.deleteFactor))
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
func mountRel(spec speedhttp.Spec, declared string) string {
	rel, err := mount.RelativePath(mount.DefaultBasePath, spec.ResolvePath(declared))
	if err != nil {
		panic(diagnosticPrefix + ModuleName + ": " + err.Error())
	}
	return rel
}

// statusName is the lifecycle status as the wire names it.
func statusName(s authn.FactorStatus) string {
	switch s {
	case authn.FactorPending:
		return "pending"
	case authn.FactorActive:
		return "active"
	default:
		return "disabled"
	}
}

// viewOf builds one factor as the list shows it, field by field from the
// record, which holds nothing secret to begin with, so that a field added to the
// record later does not reach a client by being there.
func viewOf(rec authn.FactorRecord) AuthnMfaFactor {
	v := AuthnMfaFactor{
		ID:        rec.ID,
		Kind:      string(rec.Kind),
		Label:     rec.Label,
		Status:    statusName(rec.Status),
		CreatedAt: rec.CreatedAt.UTC(),
	}
	if !rec.ActivatedAt.IsZero() {
		at := rec.ActivatedAt.UTC()
		v.ActivatedAt = &at
	}
	if !rec.LastUsedAt.IsZero() {
		at := rec.LastUsedAt.UTC()
		v.LastUsedAt = &at
	}
	return v
}

// listFactors answers GET /factors: the caller's own factors, in a list that
// carries no seed, key or other data of a kind's own.
func (m *product) listFactors(ctx context.Context, _ struct{}) (AuthnMfaFactorList, error) {
	id, ok := authn.FromContext(ctx)
	if !ok {
		return AuthnMfaFactorList{}, m.unauthorized()
	}
	recs, err := m.Factors(ctx, id.Account())
	if err != nil {
		return AuthnMfaFactorList{}, m.answerError(id.Account(), err)
	}
	views := make([]AuthnMfaFactor, len(recs))
	for i, rec := range recs {
		views[i] = viewOf(rec)
	}
	return AuthnMfaFactorList{Factors: views}, nil
}

// deleteFactor answers DELETE /factors/:id. It asks for a fresh multi-factor
// authentication that was verified here: an IdP's declaration of one is not
// enough to remove an account's own factor. A factor that is not the caller's
// reads as one already gone, because the route has no 404 among its answers and
// a different answer for another account's identifier would say it exists.
func (m *product) deleteFactor(ctx context.Context, in removeFactorInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, ok := authn.FromContext(ctx)
	if !ok {
		return nil, m.unauthorized()
	}
	if id.Impersonating() {
		return nil, authnFailure(http.StatusForbidden, mount.CodeImpersonationRestricted)
	}
	requirement := authn.FreshLocalMulti(m.settings.recentWindow())
	if !id.Satisfies(requirement, m.clock.Now()) {
		return nil, m.stepUpRequired(id, requirement)
	}
	if in.ID == "" || len(in.ID) > maxFactorIDLen {
		return nil, authnFailure(http.StatusBadRequest, mount.CodeBadRequest)
	}
	err := m.Remove(ctx, id.Account(), in.ID)
	switch {
	case err == nil, errors.Is(err, authn.ErrFactorNotEnrolled):
		return speedhttp.EmptyReply(http.StatusNoContent), nil
	default:
		return nil, m.answerError(id.Account(), err)
	}
}

// unauthorized is the 401 of a route reached without an identity. The gateway
// refuses this first, with the capability's challenge; where the route is
// reached without the gateway the handler answers the same challenge itself, as
// the fault's declared header, so its 401 is the one RFC 9110 requires a 401 to
// carry rather than a bare problem document.
func (m *product) unauthorized() error {
	return speedhttp.Fault{
		Status: http.StatusUnauthorized,
		Headers: []speedhttp.HeaderValue{{
			Name:  "WWW-Authenticate",
			Value: mount.UnauthorizedChallenge(m.mounter.Endpoint()),
		}},
	}
}

// stepUpRequired is the 401 of an identity that does not meet a requirement. Its
// type is insufficient_user_authentication and its challenge names the scheme
// the identity authenticated with, the level the route wants and, when the
// requirement asks for freshness, the maximum age.
func (m *product) stepUpRequired(id authn.Identity, requirement authn.Requirement) error {
	return speedhttp.Fault{
		Status: http.StatusUnauthorized,
		Type:   typePrefix + mount.CodeInsufficientUserAuthentication,
		Headers: []speedhttp.HeaderValue{{
			Name:  "WWW-Authenticate",
			Value: stepUpChallenge(id.Challenge(), requirement),
		}},
	}
}

// stepUpChallenge is the WWW-Authenticate value of mount.StepUpRequired, built
// here because a typed handler answers with a fault and mount exposes no
// exported builder for the step-up form. The level is written as its number,
// and max_age only when the requirement carries one, in whole seconds rounded
// up.
func stepUpChallenge(scheme string, r authn.Requirement) string {
	var b strings.Builder
	b.WriteString(challengeScheme(scheme))
	b.WriteString(` error="` + mount.CodeInsufficientUserAuthentication + `", error_description="`)
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
// otherwise. The gateway stamps Identity.Challenge from the credential's
// carrier, so an odd value is a zero identity or a defect, and neither may put a
// character into the field that ends it or starts another.
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

// authnFailure is a refusal of one of the capability's problem codes: the entry
// point writes it as a problem document of the code's status and type.
func authnFailure(status int, code string) error {
	return speedhttp.Fault{Status: status, Type: typePrefix + code}
}

// answerError is the answer for an error a route's call returned: a conflict
// for the last factor, and for everything else an outage that names no cause,
// which the log carries.
func (m *product) answerError(account authn.AccountID, err error) error {
	if errors.Is(err, authn.ErrLastFactor) {
		return authnFailure(http.StatusConflict, mount.CodeConflict)
	}
	m.logger.Warn(ModuleName+": a factor route could not be served",
		slog.String("account", string(account)), slog.String("cause", err.Error()))
	return authnFailure(http.StatusServiceUnavailable, mount.CodeUnavailable)
}
