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

const maxFactorIDLen = 64

const typePrefix = "urn:speed:authn:"

const stepUpDescription = "A stronger or more recent authentication is required"

const cacheControlSetCookie = `no-cache="Set-Cookie"`

func (m *product) mountRoutes() {
	mount.Declared(m.mounter, factorsOperation, mountRel(mfaSpec, factorsOperation.Path),
		factorsOperation.Handle(m.listFactors), mount.Impersonable())
	mount.Declared(m.mounter, removeFactorOperation, mountRel(mfaSpec, removeFactorOperation.Path),
		removeFactorOperation.Handle(m.deleteFactor), mount.Impersonable())
	mount.Declared(m.mounter, updateFactorOperation, mountRel(mfaSpec, updateFactorOperation.Path),
		updateFactorOperation.Handle(m.updateFactor), mount.Impersonable())
}

func mountRel(spec speedhttp.Spec, declared string) string {
	rel, err := mount.RelativePath(mount.DefaultBasePath, spec.ResolvePath(declared))
	if err != nil {
		panic(diagnosticPrefix + ModuleName + ": " + err.Error())
	}
	return rel
}

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

func (m *product) deleteFactor(ctx context.Context, in removeFactorInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, ok := authn.FromContext(ctx)
	if !ok {
		return nil, m.unauthorized()
	}
	requirement := authn.FreshLocalMulti(m.settings.recentWindow())
	if !id.Satisfies(requirement, m.clock.Now()) {
		// The last factor cannot gate its own deletion: the reader may have lost it.
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

func (m *product) lastFactorOnAFreshFirstFactor(ctx context.Context, id authn.Identity, factorID string) (bool, error) {
	if id.Impersonating() {
		return false, nil
	}
	// Only the account's own holder takes that door; an impersonating operator still proves fresh local multi-factor.
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
		// An account with no password proves a fresh first factor instead.
		return true, nil
	}
	// An account with a password proves it, so a session raised by an external identity alone cannot remove the last factor.
	return slices.Contains(id.Methods(), authn.MethodPassword), nil
}

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
		if errors.Is(err, authn.ErrInvalidArgument) {
			return AuthnMfaFactor{}, authnFailure(http.StatusBadRequest, mount.CodeBadRequest)
		}
		return AuthnMfaFactor{}, m.answerError(id.Account(), err)
	}
	if !found {
		return AuthnMfaFactor{}, authnFailure(http.StatusNotFound, mount.CodeFactorNotFound)
	}
	return viewOf(rec), nil
}

func (m *product) unauthorized() error {
	return speedhttp.Fault{
		Status: http.StatusUnauthorized,
		Headers: []speedhttp.HeaderValue{{
			Name:  "WWW-Authenticate",
			Value: mount.UnauthorizedChallenge(m.mounter.Endpoint()),
		}},
	}
}

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

func authnFailure(status int, code string) error {
	return speedhttp.Fault{Status: status, Type: typePrefix + code}
}

type cookieRecorder struct{ header http.Header }

func (r *cookieRecorder) Header() http.Header            { return r.header }
func (r *cookieRecorder) Write(body []byte) (int, error) { return len(body), nil }
func (r *cookieRecorder) WriteHeader(int)                {}

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

func (m *product) answerError(account authn.AccountID, err error) error {
	if errors.Is(err, authn.ErrLastFactor) {
		return authnFailure(http.StatusConflict, mount.CodeConflict)
	}
	m.logger.Warn(ModuleName+": a factor route could not be served",
		slog.String("account", string(account)), slog.String("cause", err.Error()))
	return authnFailure(http.StatusServiceUnavailable, mount.CodeUnavailable)
}
