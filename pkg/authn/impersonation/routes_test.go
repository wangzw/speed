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

// postBody is a start request as JSON.
func postBody() string {
	return `{"account":"acct-user","tenant":"tenant-a","reason":"a support call","ttl_seconds":0}`
}

// startRequest builds the start route's request with the body and the JSON type.
func startRequest(body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/impersonation", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

// TestTheRoutesCarryTheRegisteredAccess holds the registrations: the status and
// the end are Impersonable, and the start is not.
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

// TestStartOverCookieAttachesTheSessionAndWritesTheCookie holds the cookie
// carrier: the marker is set to the new record, a fresh cookie is written, and
// the answer carries the record.
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

// TestStartOverBearerIssuesTheImpersonationToken holds the bearer carrier: the
// answer carries an access token, no session is touched, and the issue carries
// the target, the actor, the record, its tenant and session, and a lifetime no
// longer than the shorter of the access-token setting and the record.
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
		// The issue carries the actor's own factors; the token module is what
		// caps the record's level at a single factor.
		if len(in.Methods) != 2 || in.Methods[0] != authn.MethodPassword || in.Methods[1] != authn.MethodTOTP {
			t.Errorf("the issue's methods are %v, want the actor's methods", in.Methods)
		}
		if in.Assurance != authn.AssuranceMulti {
			t.Errorf("the issue's assurance is %d, want the actor's", in.Assurance)
		}
		// The lifetime is the shorter of the access-token setting (10m) and the
		// record's remaining lifetime (30m): ten minutes.
		if in.TTL != 10*time.Minute {
			t.Errorf("the token lifetime is %v, want the access-token setting", in.TTL)
		}
	})
}

// TestStartOverBearerWithoutTokensIsUnavailable holds that a bearer start in an
// assembly with no token capability fails before any action.
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

// TestCarrierFailureCompensates holds the compensation: a start whose carrier
// cannot be attached ends the record with the reason start-failed, writes a
// failed audit row, and answers 503.
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
		// The last audit row is the failed one that links to the granted row.
		entries := r.audit.entries()
		last := entries[len(entries)-1]
		if last.Outcome != outcomeFailed || last.Detail[detailGrantID] != rows[0].ID {
			t.Errorf("the last audit row is %+v, want a failed row linked by grant_id", last)
		}
		// No started event is published for a start whose carrier failed.
		if got := len(r.bus.ofType(authn.EventImpersonationStarted)); got != 0 {
			t.Errorf("%d started events were published, want none", got)
		}
	})
}

// TestStatusReturnsTheRecordInForceAndNotFoundWithout holds the status route.
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

		// A session with no record answers 404.
		r.mounter.As(adminOpts(authntest.WithSession("no-record-session"))...)
		if w := r.mounter.Do(httptest.NewRequest(http.MethodGet, "/api/v1/auth/impersonation", nil)); w.Code != http.StatusNotFound {
			t.Errorf("the status of a session with no record answered %d, want 404", w.Code)
		}
	})
}

// TestEndOverCookieClearsTheMarker holds the delete route: the record ends and,
// under a cookie, the marker is cleared without a new token or a cookie.
func TestEndOverCookieClearsTheMarker(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		r.mounter.As(adminOpts()...)
		r.p.mountRoutes()
		if w := r.mounter.Do(startRequest(postBody())); w.Code != http.StatusOK {
			t.Fatalf("the start answered %d", w.Code)
		}
		r.sessions.setCalls = nil
		// The start wrote a cookie; the delete is what this case is about.
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

// TestAnImpersonatingIdentityIsRefusedOnTheStartRoute holds the gateway level: the
// start route is not registered Impersonable, so an impersonating identity is
// answered 401 before the handler.
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

// TestTheStartHandlerRefusesAnImpersonatingIdentityItself holds the handler's own
// refusal, the fallback for a path that does not go through the gateway. The
// typed handler answers a fault rather than writing a response, so the fault is
// what the case reads.
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
		// The refusal is the handler's own and never reaches Start, so nothing is
		// audited, no denied attempt is counted and no record is written. Without
		// the early return, Start refuses the same way but the seam still writes.
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

// impersonatingOpts are the options of an impersonating identity as a verifying
// backend would stamp it: the target's account, the actor, the record's tenant,
// and the level capped at a single factor.
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

// TestStartOverCookiePublishesTheStartedPayload holds the started event's
// payload: the record, the actor, the target, the actor's session and the end
// instant, as the design fixes them.
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

// TestStatusAnswersNotFoundForALapsedRecord holds the status route when the
// record is there but no longer in force: it is answered 404, and the lapse is
// written back.
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

		// Past the record's end without ending it: the row is there, unended.
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

// TestStartSeparatesAnUnseasonedActorFromAShortfall holds the two refusals a
// failing actor's authentication reaches the route with. A missing seasoned
// factor is the 403 a step-up can never clear, so it carries no challenge; the
// assurance, freshness, Local and method-set checks stay the 401 the step-up
// dialog answers. The seasoned answer is true throughout the second case, so a
// remaining 401 proves the checks before it were judged first.
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

// TestStartFailureMapsEachErrorToItsStatusAndCode holds the handler's answer to
// each error Start returns: the fault's status and problem code are the design's,
// which the entry point writes and the client reads.
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
