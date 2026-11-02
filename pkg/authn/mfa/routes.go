package mfa

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"
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

// cacheControlSetCookie is the directive an answer that issues a session cookie
// adds beside the no-store the mounter puts on every response: a cache must not
// reuse the answer without the cookie. The session capability writes the same
// directive beside the Cookie it sets.
const cacheControlSetCookie = `no-cache="Set-Cookie"`

// mountRoutes registers the routes. Both are guarded by the gateway, which is
// what makes them Required, and both are registered Impersonable: an
// impersonating identity lists and removes the effective account's factors,
// exactly as the account's own holder does. Each route is registered from the
// operation that declares it, so the handler's input and answer types, the
// fragment and the route are one source.
func (m *product) mountRoutes() {
	mount.Declared(m.mounter, factorsOperation, mountRel(mfaSpec, factorsOperation.Path),
		factorsOperation.Handle(m.listFactors), mount.Impersonable())
	mount.Declared(m.mounter, removeFactorOperation, mountRel(mfaSpec, removeFactorOperation.Path),
		removeFactorOperation.Handle(m.deleteFactor), mount.Impersonable())
	mount.Declared(m.mounter, updateFactorOperation, mountRel(mfaSpec, updateFactorOperation.Path),
		updateFactorOperation.Handle(m.updateFactor), mount.Impersonable())
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
// enough to remove an account's own factor. The requirement is the effective
// account's, while the proof is the caller's own, so an impersonating identity
// removes the impersonated account's factor with its own freshness. The route is
// where the account's other sessions are ended after a successful removal of an
// active factor and the requesting one is rotated: the device that removed the
// factor stays signed in on a new token, and the token it held before no longer
// authenticates. A factor that is not the caller's reads as one already gone:
// the answer is the same 204 a landed removal gives, because the route has no
// 404 among its answers, a different answer for another account's identifier
// would say it exists, and repeating a removal is idempotent. The name being
// gone may be a retry of an attempt whose factor removal landed before its
// session settlement failed, so the route still settles the sessions and carries
// the rotated cookie on that 204.
func (m *product) deleteFactor(ctx context.Context, in removeFactorInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, ok := authn.FromContext(ctx)
	if !ok {
		return nil, m.unauthorized()
	}
	requirement := authn.FreshLocalMulti(m.settings.recentWindow())
	if !id.Satisfies(requirement, m.clock.Now()) {
		// Requiring the factor to prove its own deletion would lock out an
		// account whose only factor was lost. The account's last active
		// factor may be removed on a freshly proved first factor instead; any
		// other removal still needs the multi-factor proof.
		allowed, aerr := m.lastFactorOnAFreshFirstFactor(ctx, id, in.ID)
		if aerr != nil {
			return nil, m.answerError(id.Account(), aerr)
		}
		if !allowed {
			return nil, m.stepUpRequired(id, requirement)
		}
	}
	if in.ID == "" || len(in.ID) > maxFactorIDLen {
		return nil, authnFailure(http.StatusBadRequest, mount.CodeBadRequest)
	}
	issued, err := m.removeFactorAndSettleSessions(ctx, id.Account(), in.ID, id.Session())
	switch {
	case err == nil, errors.Is(err, authn.ErrFactorNotEnrolled):
		reply := speedhttp.EmptyReply(http.StatusNoContent)
		if cookie := m.issuedCookie(issued); cookie != nil {
			mount.AddResponseHeader(ctx, "Cache-Control", cacheControlSetCookie)
			reply = speedhttp.WithCookies(reply, cookie)
		}
		return reply, nil
	default:
		return nil, m.answerError(id.Account(), err)
	}
}

// lastFactorOnAFreshFirstFactor reports whether id names the account's only
// active factor and the caller's session holds a first factor proved within
// the account-security window. That
// combination is the one deletion a factor cannot be allowed to gate: the
// reader may have lost the very factor they are removing.
func (m *product) lastFactorOnAFreshFirstFactor(ctx context.Context, id authn.Identity, factorID string) (bool, error) {
	if factorID == "" {
		return false, nil
	}
	records, err := m.Factors(ctx, id.Account())
	if err != nil {
		return false, err
	}
	active := 0
	target := false
	for _, rec := range records {
		if rec.Status != authn.FactorActive {
			continue
		}
		active++
		if rec.ID == factorID {
			target = true
		}
	}
	if active != 1 || !target {
		return false, nil
	}
	window := m.settings.recentWindow()
	if window <= 0 {
		return false, nil
	}
	first := authn.Requirement{Assurance: authn.AssuranceSingle, MaxAge: window}
	if !id.Satisfies(first, m.clock.Now()) {
		return false, nil
	}
	account, err := m.accounts.Get(ctx, id.Account())
	if err != nil {
		return false, err
	}
	if !account.PasswordSet {
		// An account without a password cannot prove one; a fresh first
		// factor -- the re-login the step-up sends it to -- is its door.
		return true, nil
	}
	// An account holding a password proves it: a session raised by an
	// external identity alone does not remove the last factor.
	return slices.Contains(id.Methods(), authn.MethodPassword), nil
}

// updateFactor answers PATCH /factors/:id. The label is metadata and is written
// without a step-up: it changes how the factor is shown and nothing it can
// verify with. A factor that is not the caller's answers 404 factor-not-found,
// the same as one that is not there, so another account's identifier says
// nothing about whether it exists.
func (m *product) updateFactor(ctx context.Context, in updateFactorInput) (AuthnMfaFactor, error) {
	id, ok := authn.FromContext(ctx)
	if !ok {
		return AuthnMfaFactor{}, m.unauthorized()
	}
	if in.ID == "" || len(in.ID) > maxFactorIDLen {
		return AuthnMfaFactor{}, authnFailure(http.StatusBadRequest, mount.CodeBadRequest)
	}
	label, err := normalizeFactorLabel(in.Label)
	if err != nil {
		return AuthnMfaFactor{}, authnFailure(http.StatusBadRequest, mount.CodeBadRequest)
	}
	rec, found, err := m.renameFactor(ctx, id.Account(), in.ID, label)
	if err != nil {
		return AuthnMfaFactor{}, m.answerError(id.Account(), err)
	}
	if !found {
		return AuthnMfaFactor{}, authnFailure(http.StatusNotFound, mount.CodeFactorNotFound)
	}
	return viewOf(rec), nil
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

// cookieRecorder is a response writer that keeps the Set-Cookie headers the
// session capability writes, so a typed handler can attach the cookie to its
// reply. It writes no status and no body.
type cookieRecorder struct{ header http.Header }

func (r *cookieRecorder) Header() http.Header            { return r.header }
func (r *cookieRecorder) Write(body []byte) (int, error) { return len(body), nil }
func (r *cookieRecorder) WriteHeader(int)                {}

// issuedCookie turns an issued session into the cookie the answer attaches. The
// sessions capability owns the cookie's name and attributes and writes them to a
// response writer, so the value is read back from the header it wrote rather than
// reproduced here. An issued value with no token, which a session that is not a
// browser's rotation returns, writes no cookie, and nil is returned.
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
