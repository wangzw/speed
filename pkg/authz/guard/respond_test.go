package guard

import (
	"encoding/json"
	"errors"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/internal/translation"
)

const forbiddenDetailForTest = "forbidden"

type writtenReply struct {
	status int
	header nethttp.Header
	body   []byte
}

func (rp writtenReply) assertAbsent(t *testing.T, token string) {
	t.Helper()
	if strings.Contains(string(rp.body), token) {
		t.Errorf("the token %q reaches the body: %s", token, rp.body)
	}
	for name, values := range rp.header {
		for _, v := range values {
			if strings.Contains(v, token) {
				t.Errorf("the token %q reaches the header %s: %q", token, name, v)
			}
		}
	}
}

func capture(t *testing.T, write func(c *gin.Context)) writtenReply {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/orders", nil)
	write(c)
	return writtenReply{status: rec.Code, header: rec.Header().Clone(), body: rec.Body.Bytes()}
}

type problemDoc struct {
	Type   string `json:"type"`
	Status int    `json:"status"`
	Detail string `json:"detail"`
}

func decodeProblem(t *testing.T, body []byte) problemDoc {
	t.Helper()
	var p problemDoc
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("the body is not a problem details object: %v; body = %s", err, body)
	}
	return p
}

// TestAForbiddenWriteIsA403WithTheModuleType checks that a forbidden write is a 403 with the module type.
func TestAForbiddenWriteIsA403WithTheModuleType(t *testing.T) {
	cases := []struct {
		name    string
		outcome outcome
		perm    authz.Permission
	}{
		{"a denied route", denied, authz.PermissionAccountDelete},
		{"an unguarded route", unguarded, ""},
	}
	details := map[string]string{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rp := capture(t, func(gc *gin.Context) { forbidden(gc, c.outcome, c.perm) })
			if rp.status != nethttp.StatusForbidden {
				t.Fatalf("answered %d, want 403", rp.status)
			}
			body := decodeProblem(t, rp.body)
			if body.Type != "urn:speed:authz:forbidden" {
				t.Errorf("type = %q, want urn:speed:authz:forbidden", body.Type)
			}
			if body.Status != nethttp.StatusForbidden {
				t.Errorf("the body says %d, want 403", body.Status)
			}
			if body.Detail == "" {
				t.Error("the detail is empty: the reason class is unstated")
			}
			if body.Detail != forbiddenDetailForTest {
				t.Errorf("detail = %q, want the reason class %q", body.Detail, forbiddenDetailForTest)
			}
			for _, perm := range translation.Builtin() {
				if strings.Contains(body.Detail, string(perm)) {
					t.Errorf("the detail names the permission %q: %q", perm, body.Detail)
				}
			}
			details[c.name] = body.Detail
			if c.perm != "" {
				rp.assertAbsent(t, string(c.perm))
			}
		})
	}
	if len(details) == 2 && details[cases[0].name] != details[cases[1].name] {
		t.Errorf("the two 403s carry different details %q and %q, want the reason class alone",
			details[cases[0].name], details[cases[1].name])
	}
}

// TestAnUnavailableWriteIsA503 checks that an unavailable write is a 503.
func TestAnUnavailableWriteIsA503(t *testing.T) {
	cause := errors.New("the engine is down")
	rp := capture(t, func(c *gin.Context) { unavailable(c, outage, cause) })
	if rp.status != nethttp.StatusServiceUnavailable {
		t.Fatalf("answered %d, want 503", rp.status)
	}
	body := decodeProblem(t, rp.body)
	if body.Status != nethttp.StatusServiceUnavailable {
		t.Errorf("the body says %d, want 503", body.Status)
	}
	if body.Type == typePrefix+forbiddenCode {
		t.Errorf("the outage is written as a denial: type = %q", body.Type)
	}
	if body.Detail != "" {
		t.Errorf("the outage detail is %q, want none", body.Detail)
	}
	rp.assertAbsent(t, cause.Error())
	for _, perm := range translation.Builtin() {
		if strings.Contains(body.Detail, string(perm)) {
			t.Errorf("the outage detail names the permission %q: %q", perm, body.Detail)
		}
	}
}

// TestANoIdentityWriteMirrorsAdmission checks that a no identity write mirrors admission.
func TestANoIdentityWriteMirrorsAdmission(t *testing.T) {
	rp := capture(t, func(c *gin.Context) { noIdentity401(c, noIdentity, "main") })
	if rp.status != nethttp.StatusUnauthorized {
		t.Fatalf("answered %d, want 401", rp.status)
	}
	if got, want := rp.header.Get("WWW-Authenticate"), `Bearer realm="main", Cookie realm="main"`; got != want {
		t.Errorf("WWW-Authenticate = %q, want %q", got, want)
	}
	body := decodeProblem(t, rp.body)
	if body.Status != nethttp.StatusUnauthorized {
		t.Errorf("the body says %d, want 401", body.Status)
	}
	if body.Type != "about:blank" {
		t.Errorf("the no-identity answer carries a problem type: %q", body.Type)
	}
}

// TestTheRealmChallengeNeverEndsTheHeaderField checks that the realm challenge never ends the header field.
func TestTheRealmChallengeNeverEndsTheHeaderField(t *testing.T) {
	if got := quoteRealm("pub\"lic\\\r\nx"); got != `pub\"lic\\x` {
		t.Errorf("quoteRealm escaped to %q", got)
	}
	rp := capture(t, func(c *gin.Context) { noIdentity401(c, noIdentity, `pub"lic`) })
	if got, want := rp.header.Get("WWW-Authenticate"), `Bearer realm="pub\"lic", Cookie realm="pub\"lic"`; got != want {
		t.Errorf("the challenge is %q, want %q", got, want)
	}
}

// TestTheOutcomeNamesAreTheClosedDesignSet checks that the outcome names are the closed design set.
func TestTheOutcomeNamesAreTheClosedDesignSet(t *testing.T) {
	want := map[string]bool{
		"allowed": true, "denied": true, "unguarded": true, "unavailable": true, "no-identity": true,
	}
	seen := make(map[string]outcome, outcomeCount)
	for o := outcome(0); o < outcomeCount; o++ {
		name := o.String()
		if !want[name] {
			t.Errorf("outcome %d names %q, which is not in the design set", o, name)
		}
		if prev, dup := seen[name]; dup {
			t.Errorf("outcomes %d and %d share the name %q", prev, o, name)
		}
		seen[name] = o
	}
	if len(seen) != len(want) {
		t.Errorf("the outcome set has %d names, the design has %d: %v", len(seen), len(want), seen)
	}
}

// TestTheOutcomeStatusIsTheClosedSet checks that the outcome status is the closed set.
func TestTheOutcomeStatusIsTheClosedSet(t *testing.T) {
	want := map[outcome]int{
		allowed:    nethttp.StatusOK,
		denied:     nethttp.StatusForbidden,
		unguarded:  nethttp.StatusForbidden,
		outage:     nethttp.StatusServiceUnavailable,
		noIdentity: nethttp.StatusUnauthorized,
	}
	if len(want) != int(outcomeCount) {
		t.Fatalf("the case table covers %d outcomes, the set has %d", len(want), outcomeCount)
	}
	for o := outcome(0); o < outcomeCount; o++ {
		wantStatus, ok := want[o]
		if !ok {
			t.Errorf("outcome %d is in no case", o)
			continue
		}
		if got := o.status(); got != wantStatus {
			t.Errorf("%s.status() = %d, want %d", o, got, wantStatus)
		}
	}
}

// TestOnlyAnAllowedOutcomePasses checks that only an allowed outcome passes.
func TestOnlyAnAllowedOutcomePasses(t *testing.T) {
	for o := outcome(0); o < outcomeCount; o++ {
		if got, want := o.passes(), o == allowed; got != want {
			t.Errorf("%s.passes() = %v, want %v", o, got, want)
		}
	}
}

// TestTheDecisionKeepsThePermissionAndCauseOutOfTheResponse checks that the decision keeps the permission and cause out of the response.
func TestTheDecisionKeepsThePermissionAndCauseOutOfTheResponse(t *testing.T) {
	d := decision{
		outcome:    denied,
		cause:      errors.New("the store is down"),
		permission: authz.PermissionImpersonateStart,
	}
	if d.outcome != denied {
		t.Fatalf("the fixture set the outcome to %s, want %s", d.outcome, denied)
	}
	rp := capture(t, func(c *gin.Context) { forbidden(c, denied, d.permission) })
	rp.assertAbsent(t, string(d.permission))
	rp.assertAbsent(t, d.cause.Error())
}
