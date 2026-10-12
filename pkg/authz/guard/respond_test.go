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
)

// forbiddenDetailForTest is the 403's reason class written as a literal and not
// through forbiddenDetail, so a case reads back the value a dashboard is written
// against rather than the constant under test.
const forbiddenDetailForTest = "forbidden"

// writtenReply is what one write-out put on the wire: the status, the headers
// and the body, read off an httptest recorder.
type writtenReply struct {
	status int
	header nethttp.Header
	body   []byte
}

// assertAbsent fails the case if token appears in the body or in any header
// value. A write-out is kept clear of the permission a route asked for and of
// the cause of an outage, and a header is as much a place to leak either as the
// body is.
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

// capture runs one write-out the way a middleware reaches it: a real
// *gin.Context over an httptest recorder, with the request the answer is written
// for, and the recorder keeps the response.
func capture(t *testing.T, write func(c *gin.Context)) writtenReply {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/orders", nil)
	write(c)
	return writtenReply{status: rec.Code, header: rec.Header().Clone(), body: rec.Body.Bytes()}
}

// problemDoc is the default response body the cases read back.
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

// TestAForbiddenWriteIsA403WithTheModuleType holds the shape of both 403s: a
// policy denial and an undeclared route are written the same way, with the
// module's problem type and the status the outcome set states, and the detail is
// the class of the refusal alone — non-empty, the same for both permissions, and
// naming no permission of the vocabulary. The permission a route asked for
// reaches neither the body nor any response header. Writing the permission into
// the detail or a header, or answering 503 for a denial, fails the case.
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
			// The detail is the reason class, pinned to its literal value and not
			// read back through forbiddenDetail, so a change to the constant's
			// value fails the case; and it names no permission of the vocabulary.
			if body.Detail != forbiddenDetailForTest {
				t.Errorf("detail = %q, want the reason class %q", body.Detail, forbiddenDetailForTest)
			}
			for perm := range translation {
				if strings.Contains(body.Detail, perm) {
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

// TestAnUnavailableWriteIsA503 holds that an outage is a 503 and not a denial,
// and that the cause reaches neither the body nor any response header nor a type
// that reads as a refusal.
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
	// The 503 carries no detail: the reason class is the 403's, and the outage
	// answer says nothing it could name a permission or a dependency in.
	if body.Detail != "" {
		t.Errorf("the outage detail is %q, want none", body.Detail)
	}
	rp.assertAbsent(t, cause.Error())
	for perm := range translation {
		if strings.Contains(body.Detail, perm) {
			t.Errorf("the outage detail names the permission %q: %q", perm, body.Detail)
		}
	}
}

// TestANoIdentityWriteMirrorsAdmission holds the 401 of a request with no
// identity where one is required: the same realm challenge the admission layer
// writes, and no problem type of this module's on the body.
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

// TestTheRealmChallengeNeverEndsTheHeaderField holds the realm against a value
// that would end the header field: the quotes are escaped and the line ending is
// dropped, so the challenge stays one field value.
func TestTheRealmChallengeNeverEndsTheHeaderField(t *testing.T) {
	if got := quoteRealm("pub\"lic\\\r\nx"); got != `pub\"lic\\x` {
		t.Errorf("quoteRealm escaped to %q", got)
	}
	rp := capture(t, func(c *gin.Context) { noIdentity401(c, noIdentity, `pub"lic`) })
	if got, want := rp.header.Get("WWW-Authenticate"), `Bearer realm="pub\"lic", Cookie realm="pub\"lic"`; got != want {
		t.Errorf("the challenge is %q, want %q", got, want)
	}
}

// TestTheOutcomeNamesAreTheClosedDesignSet holds the metric attribute values to
// the design's closed set: every member is named, none is renamed, and no name
// stands for two outcomes.
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

// TestTheOutcomeStatusIsTheClosedSet holds the refusal codes to the closed set:
// 403 for a denial or an undeclared route, 401 for a missing identity, 503 for an
// outage, and 200 for the one that passes. A case table that misses a member
// fails the case.
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

// TestOnlyAnAllowedOutcomePasses holds that the request goes on only for the one
// allowing outcome.
func TestOnlyAnAllowedOutcomePasses(t *testing.T) {
	for o := outcome(0); o < outcomeCount; o++ {
		if got, want := o.passes(), o == allowed; got != want {
			t.Errorf("%s.passes() = %v, want %v", o, got, want)
		}
	}
}

// TestTheDecisionKeepsThePermissionAndCauseOutOfTheResponse holds the write-out
// against the values a decision carries beyond the outcome: neither the
// permission a denied route asked for nor the cause of an outage reaches the
// caller.
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
