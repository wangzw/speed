package impersonation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	speedhttp "github.com/vislake/speed/pkg/http"
)

func init() { gin.SetMode(gin.TestMode) }

func postBody() string {
	return `{"account":"acct-user","tenant":"tenant-a","reason":"a support call","ttl_seconds":0}`
}

func startRequest(body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/impersonation", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

// TestTheRoutesCarryTheRegisteredAccess checks that status and end are registered Impersonable and start is not.
func TestTheRoutesCarryTheRegisteredAccess(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		r.p.mountRoutes()
		m := r.mounter
		for _, c := range []struct {
			method, path string
			impersonable bool
		}{
			{http.MethodPost, "/api/v1/auth/impersonation", false},
			{http.MethodGet, "/api/v1/auth/impersonation", true},
			{http.MethodDelete, "/api/v1/auth/impersonation", true},
		} {
			reg, ok := m.Registered(c.method, c.path)
			if !ok {
				t.Errorf("%s %s is not registered", c.method, c.path)
				continue
			}
			if reg.Impersonable != c.impersonable {
				t.Errorf("%s %s is Impersonable=%v, want %v", c.method, c.path, reg.Impersonable, c.impersonable)
			}
		}
	})
}

// TestStartOverCookieAttachesTheSessionAndWritesTheCookie checks that a cookie start sets the marker, writes a fresh cookie and answers the record.
func TestStartOverCookieAttachesTheSessionAndWritesTheCookie(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		r.mounter.As(adminOpts()...)
		r.p.mountRoutes()
		w := r.mounter.Do(startRequest(postBody()))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
		}
		var body struct {
			Grant AuthnImpersonationGrant `json:"grant"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("the body %q is not the grant: %v", w.Body.String(), err)
		}
		if body.Grant.Target != string(targetID) || body.Grant.Tenant != tenantA || body.Grant.ID == "" {
			t.Errorf("the grant reads %+v, want the target, the tenant and an id", body.Grant)
		}
		if len(r.sessions.setCalls) != 1 || r.sessions.setCalls[0] != body.Grant.ID {
			t.Errorf("SetImpersonation was called with %v, want the new record", r.sessions.setCalls)
		}
		if !r.sessions.cookie {
			t.Error("no cookie was written")
		}
	})
}

// TestStartResolvesTheRequestedNameToTheTenantsIdentifier checks that the body's tenant name is keyed as the identifier in the record and token.
func TestStartResolvesTheRequestedNameToTheTenantsIdentifier(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		pair := namePair{name: "team-alpha", id: tenantA}
		r.p.tenantNames = pair
		r.p.tenantDirectory = pair
		r.mounter.As(adminOpts(authntest.WithBackend(tokenBackend))...)
		r.p.mountRoutes()

		w := r.mounter.Do(startRequest(`{"account":"acct-user","tenant":"team-alpha","reason":"a support call"}`))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
		}
		var body struct {
			Grant AuthnImpersonationGrant `json:"grant"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("the body %q is not the grant: %v", w.Body.String(), err)
		}
		if body.Grant.Tenant != "team-alpha" {
			t.Errorf("the answer names the tenant %q, want the name team-alpha", body.Grant.Tenant)
		}
		if len(r.tokens.issued) != 1 || r.tokens.issued[0].Scope != tenantA {
			t.Errorf("the token was issued for %+v, want the tenant's identifier %q", r.tokens.issued, tenantA)
		}
		if r.recordCount() != 1 {
			t.Errorf("the table holds %d records, want 1", r.recordCount())
		}
	})
}

// TestStartRefusesANameNoTenantCarries checks that an unknown tenant name is refused before any record or token.
func TestStartRefusesANameNoTenantCarries(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		pair := namePair{name: "team-alpha", id: tenantA}
		r.p.tenantNames = pair
		r.p.tenantDirectory = pair
		r.mounter.As(adminOpts(authntest.WithBackend(tokenBackend))...)
		r.p.mountRoutes()

		w := r.mounter.Do(startRequest(`{"account":"acct-user","tenant":"team-ghost","reason":"a support call"}`))
		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403: %s", w.Code, w.Body.String())
		}
		if r.recordCount() != 0 {
			t.Errorf("the table holds %d records, want none", r.recordCount())
		}
		if len(r.tokens.issued) != 0 {
			t.Errorf("a token was issued: %+v", r.tokens.issued)
		}
	})
}

// TestStartRefusesTheInternalIdentifierAsTheTenantName checks that the body's tenant is a name, never the record's internal identifier.
func TestStartRefusesTheInternalIdentifierAsTheTenantName(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		pair := namePair{name: "team-alpha", id: tenantA}
		r.p.tenantNames = pair
		r.p.tenantDirectory = pair
		r.mounter.As(adminOpts(authntest.WithBackend(tokenBackend))...)
		r.p.mountRoutes()

		w := r.mounter.Do(startRequest(`{"account":"acct-user","tenant":"` + tenantA + `","reason":"a support call"}`))
		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 when the body carries the internal identifier: %s", w.Code, w.Body.String())
		}
		if r.recordCount() != 0 {
			t.Errorf("the table holds %d records, want none", r.recordCount())
		}
		if len(r.tokens.issued) != 0 {
			t.Errorf("a token was issued: %+v", r.tokens.issued)
		}

		w = r.mounter.Do(startRequest(`{"account":"acct-user","tenant":"team-alpha","reason":"a support call"}`))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 for the name team-alpha: %s", w.Code, w.Body.String())
		}
		if r.recordCount() != 1 {
			t.Errorf("the table holds %d records, want one after the name start", r.recordCount())
		}
	})
}

type namePair struct{ name, id string }

func (p namePair) TenantNameOf(_ context.Context, id string) (string, error) {
	if id == p.id {
		return p.name, nil
	}
	return "", nil
}

func (p namePair) TenantIDOf(_ context.Context, name string) (string, bool, error) {
	if name == p.name {
		return p.id, true, nil
	}
	return "", false, nil
}

// TestStartOverBearerIssuesTheImpersonationToken checks that a bearer start answers an access token and leaves the session untouched.
func TestStartOverBearerIssuesTheImpersonationToken(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		r.mounter.As(adminOpts(authntest.WithBackend(tokenBackend))...)
		r.p.mountRoutes()
		w := r.mounter.Do(startRequest(postBody()))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("the body %q is not JSON: %v", w.Body.String(), err)
		}
		if body["access_token"] != "imp-token" {
			t.Errorf("the answer carries access_token %v, want the token", body["access_token"])
		}
		if len(r.sessions.setCalls) != 0 {
			t.Errorf("a bearer start touched the session marker: %v", r.sessions.setCalls)
		}
		if len(r.tokens.issued) != 1 {
			t.Fatalf("Issue was called %d times, want 1", len(r.tokens.issued))
		}
		in := r.tokens.issued[0]
		if in.Account != targetID || in.Actor != actorID || in.Session != sessionID {
			t.Errorf("the issue reads %+v, want the target's subject, the actor and the session", in)
		}
		if in.ImpersonationID == "" || in.Scope != tenantA {
			t.Errorf("the issue reads record %q scope %q, want the record and the tenant", in.ImpersonationID, in.Scope)
		}
		if len(in.Methods) != 2 || in.Methods[0] != authn.MethodPassword || in.Methods[1] != authn.MethodTOTP {
			t.Errorf("the issue's methods are %v, want the actor's methods", in.Methods)
		}
		if in.Assurance != authn.AssuranceMulti {
			t.Errorf("the issue's assurance is %d, want the actor's", in.Assurance)
		}
		if in.TTL != 10*time.Minute {
			t.Errorf("the token lifetime is %v, want the access-token setting", in.TTL)
		}
	})
}

// TestStartOverBearerWithoutTokensIsUnavailable checks that a bearer start without a token capability fails before any action.
func TestStartOverBearerWithoutTokensIsUnavailable(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		r.p.tokens = nil
		r.mounter.As(adminOpts(authntest.WithBackend(tokenBackend))...)
		r.p.mountRoutes()
		w := r.mounter.Do(startRequest(postBody()))
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", w.Code)
		}
		if r.recordCount() != 0 {
			t.Errorf("a tokenless bearer start wrote %d records, want none", r.recordCount())
		}
	})
}

// TestCarrierFailureCompensates checks that an unattachable carrier ends the record as start-failed, audits the failure and answers 503.
func TestCarrierFailureCompensates(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		r.sessions.setErr = errFromStore
		r.mounter.As(adminOpts()...)
		r.p.mountRoutes()
		w := r.mounter.Do(startRequest(postBody()))
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", w.Code)
		}
		if got := r.recordCount(); got != 1 {
			t.Fatalf("the table holds %d records, want the compensated one", got)
		}
		var rows []struct {
			ID        string
			EndReason *string
		}
		if err := r.h.Raw("SELECT id, end_reason FROM " + grantTable).Scan(&rows).Error; err != nil {
			t.Fatalf("reading the record: %v", err)
		}
		if rows[0].EndReason == nil || *rows[0].EndReason != authn.EndStartFailed {
			t.Errorf("the compensated record's end reason is %v, want start-failed", rows[0].EndReason)
		}

		entries := r.audit.entries()
		last := entries[len(entries)-1]
		if last.Outcome != outcomeFailed || last.Detail[detailGrantID] != rows[0].ID {
			t.Errorf("the last audit row is %+v, want a failed row linked by grant_id", last)
		}

		if got := len(r.bus.ofType(authn.EventImpersonationStarted)); got != 0 {
			t.Errorf("%d started events were published, want none", got)
		}
	})
}

// TestStatusReturnsTheRecordInForceAndNotFoundWithout checks that the status route answers the record in force and 404 without one.
func TestStatusReturnsTheRecordInForceAndNotFoundWithout(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		r.mounter.As(adminOpts()...)
		r.p.mountRoutes()
		if w := r.mounter.Do(startRequest(postBody())); w.Code != http.StatusOK {
			t.Fatalf("the start answered %d", w.Code)
		}

		r.mounter.As(impersonatingOpts(sessionBackend)...)
		w := r.mounter.Do(httptest.NewRequest(http.MethodGet, "/api/v1/auth/impersonation", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("the status answered %d, want 200: %s", w.Code, w.Body.String())
		}
		var body struct {
			Grant AuthnImpersonationGrant `json:"grant"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("the status body is not the grant: %v", err)
		}
		if body.Grant.Target != string(targetID) {
			t.Errorf("the status reads %+v, want the target", body.Grant)
		}

		r.mounter.As(adminOpts(authntest.WithSession("no-record-session"))...)
		if w := r.mounter.Do(httptest.NewRequest(http.MethodGet, "/api/v1/auth/impersonation", nil)); w.Code != http.StatusNotFound {
			t.Errorf("the status of a session with no record answered %d, want 404", w.Code)
		}
	})
}

// TestEndOverCookieClearsTheMarker checks that the delete route ends the record and clears the marker without a new token or cookie.
func TestEndOverCookieClearsTheMarker(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		r.mounter.As(adminOpts()...)
		r.p.mountRoutes()
		if w := r.mounter.Do(startRequest(postBody())); w.Code != http.StatusOK {
			t.Fatalf("the start answered %d", w.Code)
		}
		r.sessions.setCalls = nil

		r.sessions.cookie = false

		r.mounter.As(impersonatingOpts(sessionBackend)...)
		w := r.mounter.Do(httptest.NewRequest(http.MethodDelete, "/api/v1/auth/impersonation", nil))
		if w.Code != http.StatusNoContent {
			t.Fatalf("the delete answered %d, want 204: %s", w.Code, w.Body.String())
		}
		if len(r.sessions.setCalls) != 1 || r.sessions.setCalls[0] != "" {
			t.Errorf("the delete cleared the marker with %v, want one empty set", r.sessions.setCalls)
		}
		if r.sessions.cookie {
			t.Error("the delete wrote a cookie, want none: ending keeps the token and the cookie")
		}
	})
}

// TestAnImpersonatingIdentityIsRefusedOnTheStartRoute checks that an impersonating identity is answered 401 there before the handler.
func TestAnImpersonatingIdentityIsRefusedOnTheStartRoute(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		r.mounter.As(impersonatingOpts(sessionBackend)...)
		r.p.mountRoutes()
		w := r.mounter.Do(startRequest(postBody()))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", w.Code)
		}
	})
}

// TestTheStartHandlerRefusesAnImpersonatingIdentityItself checks the handler's own refusal, the fallback for a path that bypasses the gateway.
func TestTheStartHandlerRefusesAnImpersonatingIdentityItself(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		ctx := authntest.Context(t, context.Background(), impersonatingOpts(sessionBackend)...)
		_, err := r.p.handleStart(ctx, startInput{Account: "acct-user", Tenant: "tenant-a", Reason: "a support call"}, nil)
		var fault speedhttp.Fault
		if !errors.As(err, &fault) {
			t.Fatalf("the handler answered %v, want a fault", err)
		}
		if fault.Status != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", fault.Status)
		}
		if want := "urn:speed:authn:impersonation-restricted"; fault.Type != want {
			t.Errorf("the problem type is %q, want %q", fault.Type, want)
		}
		if rows := r.audit.entries(); len(rows) != 0 {
			t.Errorf("the early refusal wrote %d audit rows, want none: %+v", len(rows), rows)
		}
		if got := r.counter(outcomeDenied); got != 0 {
			t.Errorf("the early refusal counted %d denied attempts, want none", got)
		}
		if got := r.recordCount(); got != 0 {
			t.Errorf("the early refusal wrote %d records, want none", got)
		}
	})
}

func impersonatingOpts(backend string) []authntest.Option {
	return []authntest.Option{
		authntest.WithAccount(targetID),
		authntest.WithActor(actorID),
		authntest.WithSession(sessionID),
		authntest.WithScope(tenantA),
		authntest.WithAssurance(authn.AssuranceSingle),
		authntest.WithBackend(backend),
	}
}

// TestStartOverCookiePublishesTheStartedPayload checks that a cookie start publishes the started event with the record, actor, target, session and expiry.
func TestStartOverCookiePublishesTheStartedPayload(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		r.mounter.As(adminOpts()...)
		r.p.mountRoutes()
		w := r.mounter.Do(startRequest(postBody()))
		if w.Code != http.StatusOK {
			t.Fatalf("the start answered %d: %s", w.Code, w.Body.String())
		}
		var body struct {
			Grant AuthnImpersonationGrant `json:"grant"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("the body %q is not the grant: %v", w.Body.String(), err)
		}
		p := r.startedPayload()
		if p.GrantID != body.Grant.ID || p.ActorID != string(actorID) ||
			p.AccountID != string(targetID) || p.SessionID != string(sessionID) {
			t.Errorf("the started payload is %+v, want the grant, the actor, the target and the session", p)
		}
		if want := base.Add(30 * time.Minute).UTC().Format(time.RFC3339); p.ExpiresAt != want {
			t.Errorf("the started payload's expiry is %q, want %q", p.ExpiresAt, want)
		}
	})
}

// TestStatusAnswersNotFoundForALapsedRecord checks that the status route answers 404 for a lapsed record and writes the lapse back.
func TestStatusAnswersNotFoundForALapsedRecord(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		r.mounter.As(adminOpts()...)
		r.p.mountRoutes()
		start := r.mounter.Do(startRequest(postBody()))
		if start.Code != http.StatusOK {
			t.Fatalf("the start answered %d", start.Code)
		}
		var body struct {
			Grant AuthnImpersonationGrant `json:"grant"`
		}
		if err := json.Unmarshal(start.Body.Bytes(), &body); err != nil {
			t.Fatalf("the start body is not the grant: %v", err)
		}

		r.clock.advance(31 * time.Minute)
		r.mounter.As(impersonatingOpts(sessionBackend)...)
		w := r.mounter.Do(httptest.NewRequest(http.MethodGet, "/api/v1/auth/impersonation", nil))
		if w.Code != http.StatusNotFound {
			t.Fatalf("the status of a lapsed record answered %d, want 404: %s", w.Code, w.Body.String())
		}
		if ended, _, _ := r.stored(body.Grant.ID); ended == nil {
			t.Error("the status read left the lapsed record unended")
		}
	})
}

// TestStartSeparatesAnUnseasonedActorFromAShortfall checks the two refusals a failing actor's authentication reaches the route with.
func TestStartSeparatesAnUnseasonedActorFromAShortfall(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		r.p.mountRoutes()

		t.Run("no seasoned factor", func(t *testing.T) {
			r.mfa.seasoned = false
			r.mounter.As(adminOpts()...)
			w := r.mounter.Do(startRequest(postBody()))
			if w.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403: %s", w.Code, w.Body.String())
			}
			var body struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("the body %q is not a problem: %v", w.Body.String(), err)
			}
			if want := "urn:speed:authn:factor-not-seasoned"; body.Type != want {
				t.Errorf("the problem type is %q, want %q", body.Type, want)
			}
			if got := w.Header().Get("WWW-Authenticate"); got != "" {
				t.Errorf("the refusal carries the challenge %q, want none", got)
			}
			if r.recordCount() != 0 {
				t.Errorf("the refusal wrote %d records, want none", r.recordCount())
			}
		})

		t.Run("a weak authentication with a seasoned factor", func(t *testing.T) {
			r.mfa.seasoned = true
			r.mounter.As(append(adminOpts(), authntest.WithAssurance(authn.AssuranceSingle))...)
			w := r.mounter.Do(startRequest(postBody()))
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401: %s", w.Code, w.Body.String())
			}
			var body struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("the body %q is not a problem: %v", w.Body.String(), err)
			}
			if want := "urn:speed:authn:insufficient_user_authentication"; body.Type != want {
				t.Errorf("the problem type is %q, want %q", body.Type, want)
			}
			if got := w.Header().Get("WWW-Authenticate"); !strings.Contains(got, "insufficient_user_authentication") {
				t.Errorf("the challenge reads %q, want the step-up marker", got)
			}
		})
	})
}

// TestStartAnswersContactRequiredForAPendingRegistration checks that a pending self-registration is answered 403 contact-required with no record, audit row or denied count.
func TestStartAnswersContactRequiredForAPendingRegistration(t *testing.T) {
	t.Run("a pending registration", func(t *testing.T) {
		onEachDialect(t, func(t *testing.T, r *rig) {
			r.verification.state = authn.SignupStatus{State: authn.SignupPending}
			r.mounter.As(adminOpts()...)
			r.p.mountRoutes()
			w := r.mounter.Do(startRequest(postBody()))
			if w.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403: %s", w.Code, w.Body.String())
			}
			var body struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("the body %q is not a problem: %v", w.Body.String(), err)
			}
			if want := "urn:speed:authn:contact-required"; body.Type != want {
				t.Errorf("the problem type is %q, want %q", body.Type, want)
			}
			if r.recordCount() != 0 {
				t.Errorf("the refusal wrote %d records, want none", r.recordCount())
			}
			if rows := r.audit.entries(); len(rows) != 0 {
				t.Errorf("the refusal wrote %d audit rows, want none", len(rows))
			}
			if got := r.counter(outcomeDenied); got != 0 {
				t.Errorf("the refusal counted %d denied attempts, want none", got)
			}
			if len(r.decider.calls) != 0 {
				t.Errorf("the Decider was asked %d times, want none", len(r.decider.calls))
			}
		})
	})

	for _, c := range []struct {
		name  string
		state authn.SignupStatus
	}{
		{"a never-registered account", authn.SignupStatus{State: authn.SignupNone}},
		{"a verified registration", authn.SignupStatus{State: authn.SignupVerified}},
	} {
		t.Run(c.name, func(t *testing.T) {
			onEachDialect(t, func(t *testing.T, r *rig) {
				r.verification.state = c.state
				r.mounter.As(adminOpts()...)
				r.p.mountRoutes()
				if w := r.mounter.Do(startRequest(postBody())); w.Code != http.StatusOK {
					t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
				}
			})
		})
	}
}

// TestStartFailureMapsEachErrorToItsStatusAndCode checks the status and problem code the handler answers for each error Start returns.
func TestStartFailureMapsEachErrorToItsStatusAndCode(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		for _, c := range []struct {
			name   string
			err    error
			status int
			code   string
		}{
			{"a step-up shortfall", authn.ErrStepUpRequired, http.StatusUnauthorized, "insufficient_user_authentication"},
			{"an unseasoned factor", authn.ErrFactorNotSeasoned, http.StatusForbidden, "factor-not-seasoned"},
			{"a pending registration", authn.ErrContactRequired, http.StatusForbidden, "contact-required"},
			{"a forbidden decision", authn.ErrForbidden, http.StatusForbidden, "forbidden"},
			{"a restricted start", authn.ErrImpersonationRestricted, http.StatusForbidden, "impersonation-restricted"},
			{"a session with no life", authn.ErrImpersonationNeedsSession, http.StatusForbidden, "impersonation-restricted"},
			{"an active record", authn.ErrImpersonationActive, http.StatusConflict, "conflict"},
		} {
			t.Run(c.name, func(t *testing.T) {
				var fault speedhttp.Fault
				if !errors.As(r.p.startFailure(adminIdentity(t), c.err), &fault) {
					t.Fatalf("startFailure answered %v, want a fault", c.err)
				}
				if fault.Status != c.status {
					t.Fatalf("startFailure answered %d, want %d", fault.Status, c.status)
				}
				if want := "urn:speed:authn:" + c.code; fault.Type != want {
					t.Errorf("the problem type is %q, want %q", fault.Type, want)
				}
			})
		}
	})
}
