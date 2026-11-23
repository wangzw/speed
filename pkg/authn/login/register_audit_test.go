package login

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
)

// The audit action and detail of a registration. They are host-visible names, so
// the cases spell them out instead of reading the package's own constants.
const (
	wantSignupAction       = "authn.signup"
	wantSignupMethod       = "password"
	wantAuditFailuresName  = "authn.audit.write-failures"
	wantMandatoryAttribute = "mandatory"
)

// oneRow holds a case to exactly one row and returns it.
func oneRow(t *testing.T, rows []audit.Entry) audit.Entry {
	t.Helper()
	if len(rows) != 1 {
		t.Fatalf("the registration wrote %d audit rows, want one: %+v", len(rows), rows)
	}
	return rows[0]
}

// TestARegistrationAuditRowIsWrittenUnderADerivedIdentity holds the success
// path: one authn.signup row, its object is the account just opened, its only
// detail is method=password, and its actor is that account even though the
// public route's own context carries no identity. The derived context reaches
// the one Record only and is not left on the request's context.
func TestARegistrationAuditRowIsWrittenUnderADerivedIdentity(t *testing.T) {
	r := registerRig(t)
	sink := &auditing{}
	r.p.audit = sink
	r.watchCtx.Store(true)

	rec := r.send(http.MethodPost, "/api/v1/auth/register", registerBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("a registration answered %d, want 201: %s", rec.Code, rec.Body.String())
	}

	got := oneRow(t, sink.rows())
	if got.Action != wantSignupAction {
		t.Errorf("action = %q, want %q", got.Action, wantSignupAction)
	}
	if got.Object != "account:acct-new" {
		t.Errorf("object = %q, want the account the module opened", got.Object)
	}
	if got.Outcome != "success" {
		t.Errorf("outcome = %q, want success", got.Outcome)
	}
	if len(got.Detail) != 1 || got.Detail["method"] != wantSignupMethod {
		t.Errorf("detail = %v, want only method=%s", got.Detail, wantSignupMethod)
	}

	acts := sink.identities()
	if len(acts) != 1 || acts[0] != "acct-new" {
		t.Errorf("the row was written under identities %v, want [acct-new]", acts)
	}
	if r.leaked.Load() {
		t.Error("the register route left its derived identity on the request's own context")
	}
}

// TestARefusedRegistrationLeavesAnEmptyActorRow holds the refusals: the switch,
// a taken name or address, the policy and a missing member each write one row
// with outcome refused, no object and an empty actor. The detail is
// method=password and nothing else, so no address or username reaches the row.
func TestARefusedRegistrationLeavesAnEmptyActorRow(t *testing.T) {
	cases := []struct {
		name string
		arm  func(*rig)
		body string
	}{
		{"a closed switch", func(r *rig) { r.p.settings = readerWith(nil) }, registerBody},
		{"a taken username", func(r *rig) { r.signups.err = authn.ErrAccountExists }, registerBody},
		{"a taken email address", func(r *rig) { r.signups.err = authn.ErrContactTaken }, registerBody},
		{"a password policy", func(r *rig) { r.signups.err = authn.ErrPasswordPolicy }, registerBody},
		{"a member left out", func(*rig) {}, `{"email":"a@b.c","password":"a-password"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := registerRig(t)
			c.arm(r)
			sink := &auditing{}
			r.p.audit = sink

			r.send(http.MethodPost, "/api/v1/auth/register", c.body)

			got := oneRow(t, sink.rows())
			if got.Action != wantSignupAction {
				t.Errorf("action = %q, want %q", got.Action, wantSignupAction)
			}
			if got.Object != "" {
				t.Errorf("object = %q, want empty on a refused attempt", got.Object)
			}
			if got.Outcome != "refused" {
				t.Errorf("outcome = %q, want refused", got.Outcome)
			}
			if len(got.Detail) != 1 || got.Detail["method"] != wantSignupMethod {
				t.Errorf("detail = %v, want only method=%s", got.Detail, wantSignupMethod)
			}
			if acts := sink.identities(); len(acts) != 1 || acts[0] != "" {
				t.Errorf("the row was written under identities %v, want one empty actor", acts)
			}
		})
	}
}

// TestAThrottledRegistrationIsAuditedAsThrottled holds the back-offs: the
// account module's username back-off and the route's own per-source budget both
// end on outcome throttled, still with no object and no actor.
func TestAThrottledRegistrationIsAuditedAsThrottled(t *testing.T) {
	t.Run("the username back-off", func(t *testing.T) {
		r := registerRig(t)
		r.signups.err = &authn.ThrottledError{RetryAfter: 30 * time.Second}
		sink := &auditing{}
		r.p.audit = sink

		r.send(http.MethodPost, "/api/v1/auth/register", registerBody)

		got := oneRow(t, sink.rows())
		if got.Outcome != "throttled" {
			t.Errorf("outcome = %q, want throttled", got.Outcome)
		}
		if got.Object != "" {
			t.Errorf("object = %q, want empty on a throttled attempt", got.Object)
		}
		if acts := sink.identities(); len(acts) != 1 || acts[0] != "" {
			t.Errorf("the row was written under identities %v, want one empty actor", acts)
		}
	})
	t.Run("the per-source budget", func(t *testing.T) {
		r := registerRig(t)
		r.p.settings = readerWith(map[string]string{
			passwordSignupPath: signupOpen,
			signupIPLimitPath:  "1",
		})
		sink := &auditing{}
		r.p.audit = sink

		first := r.registerFrom("198.51.100.7", registerBody)
		if first.Code != http.StatusCreated {
			t.Fatalf("the registration under the budget answered %d, want 201: %s", first.Code, first.Body.String())
		}
		second := r.registerFrom("198.51.100.7", registerBody)
		if second.Code != http.StatusTooManyRequests {
			t.Fatalf("the registration past the budget answered %d, want 429: %s", second.Code, second.Body.String())
		}

		rows := sink.rows()
		if len(rows) != 2 {
			t.Fatalf("the two attempts wrote %d audit rows, want two: %+v", len(rows), rows)
		}
		if rows[1].Outcome != "throttled" {
			t.Errorf("the throttled attempt's outcome = %q, want throttled", rows[1].Outcome)
		}
		if rows[1].Object != "" {
			t.Errorf("the throttled attempt's object = %q, want empty", rows[1].Object)
		}
	})
}

// TestAnUnavailableRegistrationLeavesNoAuditRow holds the reading this build
// takes of the outcome set: an outage or a capacity failure is neither a
// success, a refusal nor a back-off, and the closed set names no value for it,
// so no row is written. The answer stays the 5xx the route gives.
func TestAnUnavailableRegistrationLeavesNoAuditRow(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"an unavailable database", authn.ErrUnavailable},
		{"a full derivation capacity", authn.ErrBusy},
		{"an unnamed failure", errors.New("the store answered something else")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := registerRig(t)
			r.signups.err = c.err
			sink := &auditing{}
			r.p.audit = sink

			rec := r.send(http.MethodPost, "/api/v1/auth/register", registerBody)
			if rec.Code != http.StatusServiceUnavailable {
				t.Errorf("%s answered %d, want 503: %s", c.name, rec.Code, rec.Body.String())
			}
			if rows := sink.rows(); len(rows) != 0 {
				t.Errorf("the failure wrote %d audit rows, want none: %+v", len(rows), rows)
			}
		})
	}
}

// TestAFailedSignupAuditDoesNotBlockTheRegistration holds the best-effort tier:
// an audit write that fails leaves the 201 and the opened account as they were,
// writes an Error line, and counts one authn.audit.write-failures with
// mandatory=false.
func TestAFailedSignupAuditDoesNotBlockTheRegistration(t *testing.T) {
	reader, mp := newTestMetrics(t)
	r := registerRig(t)
	var err error
	if r.p.meters, err = newMeters(mp); err != nil {
		t.Fatalf("creating the instruments: %v", err)
	}
	var logs bytes.Buffer
	r.p.logger = slog.New(slog.NewTextHandler(&logs, nil))
	r.p.audit = &auditing{err: errors.New("the audit module is down")}

	rec := r.send(http.MethodPost, "/api/v1/auth/register", registerBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("a registration whose audit failed answered %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if len(r.signups.registered) != 1 {
		t.Errorf("the account module was asked %d times, want the registration performed", len(r.signups.registered))
	}

	points := auditFailurePoints(t, reader)
	if points[false] != 1 {
		t.Errorf("authn.audit.write-failures{mandatory=false} = %d, want 1: %v", points[false], points)
	}
	if points[true] != 0 {
		t.Errorf("authn.audit.write-failures{mandatory=true} = %d, want none: %v", points[true], points)
	}
	if got := logs.String(); !strings.Contains(got, "level=ERROR") ||
		!strings.Contains(got, "signup audit row") {
		t.Errorf("the failed audit wrote no Error line naming it: %s", got)
	}
}

// auditFailurePoints collects authn.audit.write-failures and returns its counts
// keyed by the mandatory attribute, the instrument's one attribute.
func auditFailurePoints(t *testing.T, reader *sdkmetric.ManualReader) map[bool]int {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collecting metrics: %v", err)
	}
	out := map[bool]int{}
	for _, sm := range rm.ScopeMetrics {
		for _, md := range sm.Metrics {
			if md.Name != wantAuditFailuresName {
				continue
			}
			sum, ok := md.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("the instrument %s is not an int64 sum: %T", md.Name, md.Data)
			}
			for _, dp := range sum.DataPoints {
				v, ok := dp.Attributes.Value(wantMandatoryAttribute)
				if !ok {
					t.Errorf("a %s data point lacks the mandatory attribute: %v", md.Name, dp.Attributes.ToSlice())
					continue
				}
				out[v.AsBool()] += int(dp.Value)
			}
		}
	}
	return out
}
