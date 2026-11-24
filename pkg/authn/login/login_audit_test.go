package login

import (
	"net/http"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
)

const wantLoginAction = "authn.login"

func oneLoginRow(t *testing.T, sink *auditing) loginRow {
	t.Helper()
	rows := sink.rows()
	if len(rows) != 1 {
		t.Fatalf("the login wrote %d audit rows, want one: %+v", len(rows), rows)
	}
	row := rows[0]
	if row.Action != wantLoginAction {
		t.Errorf("action = %q, want %q", row.Action, wantLoginAction)
	}
	return loginRow{entry: row, actors: sink.identities()}
}

type loginRow struct {
	entry  audit.Entry
	actors []string
}

func expectDetail(t *testing.T, row loginRow, method, delivery string) {
	t.Helper()
	if len(row.entry.Detail) != 2 || row.entry.Detail["method"] != method || row.entry.Detail["delivery"] != delivery {
		t.Errorf("detail = %v, want only method=%s delivery=%s", row.entry.Detail, method, delivery)
	}
}

func expectEmptyActor(t *testing.T, row loginRow) {
	t.Helper()
	if len(row.actors) != 1 || row.actors[0] != "" {
		t.Errorf("the row was written under identities %v, want one empty actor", row.actors)
	}
}

// TestARefusedLoginWritesOneFailureRow holds that a refused credential writes one authn.login row with outcome failure.
func TestARefusedLoginWritesOneFailureRow(t *testing.T) {
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepRefused(authn.ErrInvalidCredentials), nil
	}
	r := newRig(t, m)
	sink := &auditing{}
	r.p.audit = sink

	rec := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("a refused login answered %d, want 401: %s", rec.Code, rec.Body.String())
	}

	row := oneLoginRow(t, sink)
	if row.entry.Outcome != "failure" {
		t.Errorf("outcome = %q, want failure", row.entry.Outcome)
	}
	if row.entry.Object != "" {
		t.Errorf("object = %q, want empty before an account is known", row.entry.Object)
	}
	expectDetail(t, row, "password", deliverySession)
	expectEmptyActor(t, row)
}

// TestAThrottledLoginWritesOneThrottledRow holds that a login stopped by the back-off writes one row with outcome throttled.
func TestAThrottledLoginWritesOneThrottledRow(t *testing.T) {
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepThrottled(30 * time.Second), nil
	}
	r := newRig(t, m)
	sink := &auditing{}
	r.p.audit = sink

	rec := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("a throttled login answered %d, want 429: %s", rec.Code, rec.Body.String())
	}

	row := oneLoginRow(t, sink)
	if row.entry.Outcome != "throttled" {
		t.Errorf("outcome = %q, want throttled", row.entry.Outcome)
	}
	if row.entry.Object != "" {
		t.Errorf("object = %q, want empty", row.entry.Object)
	}
	expectDetail(t, row, "password", deliverySession)
	expectEmptyActor(t, row)
}

// TestARefusedResolutionWritesOneRefusedRow holds that a server-decidable resolution refusal writes one row with outcome refused.
func TestARefusedResolutionWritesOneRefusedRow(t *testing.T) {
	m := &fakeMethod{name: "google"}
	m.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepProven(authn.Proof{
			External: &authn.External{Provider: "google", Subject: "g1"},
		}), nil
	}
	r := newRig(t, m)
	r.accounts.byExt["google\x00g1"] = "acct-off"
	r.accounts.byID["acct-off"] = authn.Account{ID: "acct-off", Kind: authn.KindUser, Status: authn.StatusDisabled}
	sink := &auditing{}
	r.p.audit = sink

	rec := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"google"}`)
	if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
		t.Fatalf("a refused resolution answered %d, want 401 or 403: %s", rec.Code, rec.Body.String())
	}

	row := oneLoginRow(t, sink)
	if row.entry.Outcome != "refused" {
		t.Errorf("outcome = %q, want refused", row.entry.Outcome)
	}
	expectDetail(t, row, "google", deliverySession)
	expectEmptyActor(t, row)
}

// TestAnUnavailableLoginWritesNoAuditRow holds that an outage is counted but writes no audit row.
func TestAnUnavailableLoginWritesNoAuditRow(t *testing.T) {
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepUnavailable(authn.ErrUnavailable), nil
	}
	r := newRig(t, m)
	sink := &auditing{}
	r.p.audit = sink

	rec := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("an unavailable login answered %d, want 503: %s", rec.Code, rec.Body.String())
	}
	if rows := sink.rows(); len(rows) != 0 {
		t.Errorf("the outage wrote %d audit rows, want none: %+v", len(rows), rows)
	}
}

// TestASecondFactorRefusalWritesOneRowForTheAccount holds that a second-factor refusal writes one failure row naming the already-proved account.
func TestASecondFactorRefusalWritesOneRowForTheAccount(t *testing.T) {
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepProven(authn.Proof{Account: "acct1", Methods: []authn.Method{authn.MethodPassword}}), nil
	}
	r := newRig(t, m)
	r.allow("acct1")
	r.mfa.outcome = authn.MFAFactor
	r.mfa.offered = []authn.FactorRecord{{ID: "f1", Kind: authn.FactorTOTP}}
	r.mfa.verify = func(string) authn.Step { return authn.StepRefused(authn.ErrInvalidCredentials) }
	sink := &auditing{}
	r.p.audit = sink

	start := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`)
	cookie := flowCookie(t, start)
	challenge := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		`{"seq":0,"step":"factor","input":{"factor":"f1"}}`, "Cookie", cookie)
	if challenge.Code != http.StatusOK {
		t.Fatalf("the factor challenge answered %d: %s", challenge.Code, challenge.Body.String())
	}
	seq := int(decode(t, challenge.Body.String())["seq"].(float64))
	refused := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		`{"seq":`+itoa(seq)+`,"step":"factor","input":{"factor":"f1","code":"123456"}}`, "Cookie", cookie)
	if refused.Code != http.StatusUnauthorized {
		t.Fatalf("the refused factor answered %d, want 401: %s", refused.Code, refused.Body.String())
	}

	row := oneLoginRow(t, sink)
	if row.entry.Outcome != "failure" {
		t.Errorf("outcome = %q, want failure", row.entry.Outcome)
	}
	if row.entry.Object != "account:acct1" {
		t.Errorf("object = %q, want the account the first factor proved", row.entry.Object)
	}
	expectDetail(t, row, "password", deliverySession)
	expectEmptyActor(t, row)
}
