package mount

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	speedhttp "github.com/vislake/speed/pkg/http"
)

func init() { gin.SetMode(gin.TestMode) }

// serve runs one request through an engine that has the handler on every method
// at /x, and a recovery that ends a panic in a 500 which keeps the headers.
func serve(h gin.HandlerFunc, method string, hdr map[string]string, body io.Reader) *httptest.ResponseRecorder {
	e := gin.New()
	e.Use(gin.CustomRecoveryWithWriter(io.Discard, func(c *gin.Context, _ any) { c.AbortWithStatus(http.StatusInternalServerError) }))
	e.Any("/x", h)
	r := httptest.NewRequest(method, "/x", body)
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	return w
}

// problemBodyOf decodes a problem details object into its members.
func problemBodyOf(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	m := problemBodyOfRaw(w.Body.Bytes())
	if m == nil {
		t.Fatalf("the body %q is not a JSON object", w.Body.String())
	}
	return m
}

// problemBodyOfRaw decodes a JSON object, and is nil for anything else.
func problemBodyOfRaw(raw []byte) map[string]any {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return m
}

// codeTable is the design's table of problem codes with the status each belongs
// to, written out so that the table in the code is compared with it and not with
// itself.
var codeTable = []struct {
	code   string
	status int
}{
	{"bad-request", 400},
	{"username-reserved", 400},
	{"username-invalid", 400},
	{"unsupported-region", 400},
	{"login-failed", 401},
	{"flow-invalid", 401},
	{"link-required", 401},
	{"insufficient_user_authentication", 401},
	{"method-disabled", 403},
	{"account-unavailable", 403},
	{"impersonation-restricted", 403},
	{"factor-not-seasoned", 403},
	{"forbidden", 403},
	{"signup-closed", 403},
	{"contact-required", 403},
	{"phone-required", 403},
	{"human-verification-required", 403},
	{"password-reset-closed", 403},
	{"password-reset-unavailable", 403},
	{"factor-not-found", 404},
	{"conflict", 409},
	{"account-last-tenant-admin", 409},
	{"login-identifier-required", 409},
	{"last-login-method", 409},
	{"username-immutable", 409},
	{"username-taken", 409},
	{"email-taken", 409},
	{"unsupported-media-type", 415},
	{"throttled", 429},
	{"unavailable", 503},
}

// TestTheProblemCodesAreTheDesignsSetWithTheirStatuses holds the closed set
// against the design, code by code, and refuses what is near it.
func TestTheProblemCodesAreTheDesignsSetWithTheirStatuses(t *testing.T) {
	if len(statusOf) != len(codeTable) {
		t.Errorf("the code table has %d entries, the design has %d", len(statusOf), len(codeTable))
	}
	for _, c := range codeTable {
		got, ok := Status(c.code)
		if !ok || got != c.status {
			t.Errorf("Status(%q) = %d, %v, want %d", c.code, got, ok, c.status)
		}
		if !ValidCode(c.code) {
			t.Errorf("ValidCode(%q) = false", c.code)
		}
	}
	for _, code := range []string{
		"", "Bad-Request", "bad_request", "bad-request ", "login_failed", "not-found", "internal",
		"insufficient-user-authentication", "urn:speed:authn:bad-request", "payload-too-large", "unauthorized",
		"signup_closed", "username_taken", "email_taken", "contact_required", "phone_required",
	} {
		if ValidCode(code) {
			t.Errorf("ValidCode(%q) = true", code)
		}
		if s, ok := Status(code); ok || s != 0 {
			t.Errorf("Status(%q) = %d, %v, want 0, false", code, s, ok)
		}
	}
	// The constants are the texts.
	consts := []string{
		CodeBadRequest, CodeUsernameReserved, CodeUsernameInvalid, CodeUnsupportedRegion, CodeLoginFailed,
		CodeFlowInvalid, CodeLinkRequired,
		CodeInsufficientUserAuthentication, CodeMethodDisabled, CodeAccountUnavailable,
		CodeImpersonationRestricted, CodeFactorNotSeasoned, CodeForbidden, CodeSignupClosed,
		CodeContactRequired, CodePhoneRequired, CodeHumanVerificationRequired, CodePasswordResetClosed,
		CodePasswordResetUnavailable, CodeFactorNotFound, CodeConflict, CodeAccountLastTenantAdmin,
		CodeLoginIdentifierRequired,
		CodeLastLoginMethod, CodeUsernameImmutable, CodeUsernameTaken, CodeEmailTaken,
		CodeUnsupportedMediaType, CodeThrottled, CodeUnavailable,
	}
	for i, c := range codeTable {
		if consts[i] != c.code {
			t.Errorf("constant %d reads %q, want %q", i, consts[i], c.code)
		}
	}
}

// TestFailWritesTheCodeAsAProblemWithNoCause holds the shape of an answer: the
// media type, the type under urn:speed:authn:, the status of the code, a body
// that is nothing but the problem's own members, and the headers the handler set
// before the call.
func TestFailWritesTheCodeAsAProblemWithNoCause(t *testing.T) {
	for _, code := range []string{CodeBadRequest, CodeUnsupportedRegion, CodeFactorNotFound, CodeConflict, CodeLoginIdentifierRequired, CodeLastLoginMethod, CodeUsernameTaken, CodeEmailTaken, CodeUnsupportedMediaType} {
		t.Run(code, func(t *testing.T) {
			w := serve(func(c *gin.Context) {
				c.Header("X-Kept", "yes")
				Fail(c, code)
			}, http.MethodPost, nil, nil)
			want, _ := Status(code)
			if w.Code != want {
				t.Errorf("status = %d, want %d", w.Code, want)
			}
			if ct := w.Header().Get("Content-Type"); ct != "application/problem+json" {
				t.Errorf("Content-Type = %q, want application/problem+json", ct)
			}
			if w.Header().Get("X-Kept") != "yes" {
				t.Error("a header set before Fail is gone")
			}
			body := problemBodyOf(t, w)
			if body["type"] != "urn:speed:authn:"+code {
				t.Errorf("type = %v, want urn:speed:authn:%s", body["type"], code)
			}
			for k := range body {
				if k != "type" && k != "title" && k != "status" {
					t.Errorf("the body carries the member %q, which is more than a code and a status", k)
				}
			}
			if int(body["status"].(float64)) != want {
				t.Errorf("the body's status = %v, want %d", body["status"], want)
			}
		})
	}
}

// mustPanic runs f and returns what it panicked with, or fails.
func mustPanic(t *testing.T, name string, f func()) string {
	t.Helper()
	var got any
	func() {
		defer func() { got = recover() }()
		f()
	}()
	if got == nil {
		t.Fatalf("%s did not panic", name)
	}
	s, _ := got.(string)
	return s
}

// TestAWrongCodeIsADefectOfTheHandlerAndPanics holds that none of the writers
// accepts a code it does not write: an unknown string, a code of another status
// class, and the 401 that carries a challenge written through Fail.
func TestAWrongCodeIsADefectOfTheHandlerAndPanics(t *testing.T) {
	ctx := func() *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/x", nil)
		return c
	}
	for name, f := range map[string]func(){
		"Fail with an unknown code":                  func() { Fail(ctx(), "nope") },
		"Fail with the empty code":                   func() { Fail(ctx(), "") },
		"Fail with a 401 code":                       func() { Fail(ctx(), CodeLoginFailed) },
		"Fail with a 403 code":                       func() { Fail(ctx(), CodeForbidden) },
		"Fail with throttled":                        func() { Fail(ctx(), CodeThrottled) },
		"Fail with unavailable":                      func() { Fail(ctx(), CodeUnavailable) },
		"Forbidden with an unknown code":             func() { Forbidden(ctx(), "nope") },
		"Forbidden with a 401 code":                  func() { Forbidden(ctx(), CodeLoginFailed) },
		"Forbidden with a 400 code":                  func() { Forbidden(ctx(), CodeBadRequest) },
		"Forbidden with the empty code":              func() { Forbidden(ctx(), "") },
		"Unauthorized with an unknown code":          func() { Unauthorized(ctx(), "r", "nope") },
		"Unauthorized with a 403 code":               func() { Unauthorized(ctx(), "r", CodeForbidden) },
		"Unauthorized with the step-up code":         func() { Unauthorized(ctx(), "r", CodeInsufficientUserAuthentication) },
		"Unauthorized with a 400 code":               func() { Unauthorized(ctx(), "r", CodeBadRequest) },
		"Unauthorized with a code near a real one":   func() { Unauthorized(ctx(), "r", "login_failed") },
		"Fail with a code in the wrong case":         func() { Fail(ctx(), "Bad-Request") },
		"Fail with the type URN instead of the code": func() { Fail(ctx(), "urn:speed:authn:bad-request") },
	} {
		if msg := mustPanic(t, name, f); !strings.HasPrefix(msg, "authn/mount: ") {
			t.Errorf("%s panicked with %q, want a message that names this package", name, msg)
		}
	}
}

// TestForbiddenWritesThe403CodesItOwns holds the 403 codes this capability's
// handlers may answer with.
func TestForbiddenWritesThe403CodesItOwns(t *testing.T) {
	for _, code := range []string{
		CodeMethodDisabled, CodeAccountUnavailable, CodeImpersonationRestricted,
		CodeFactorNotSeasoned, CodeForbidden, CodeSignupClosed, CodeContactRequired,
		CodePhoneRequired, CodeHumanVerificationRequired, CodePasswordResetClosed, CodePasswordResetUnavailable,
	} {
		w := serve(func(c *gin.Context) { Forbidden(c, code) }, http.MethodGet, nil, nil)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403", code, w.Code)
		}
		if got := problemBodyOf(t, w)["type"]; got != "urn:speed:authn:"+code {
			t.Errorf("%s: type = %v", code, got)
		}
	}
}

// TestUnauthorizedOffersBearerAndCookieAndNeverBasic holds the challenge of a 401
// this capability's handlers write: a Basic challenge would make a browser open
// its own password prompt on a fetch that did not ask for a Basic credential.
func TestUnauthorizedOffersBearerAndCookieAndNeverBasic(t *testing.T) {
	for _, code := range []string{"", CodeLoginFailed, CodeFlowInvalid, CodeLinkRequired} {
		w := serve(func(c *gin.Context) { Unauthorized(c, "main", code) }, http.MethodGet, nil, nil)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("code %q: status = %d, want 401", code, w.Code)
		}
		got := w.Header().Values("WWW-Authenticate")
		if len(got) != 1 || got[0] != `Bearer realm="main", Cookie realm="main"` {
			t.Errorf("code %q: WWW-Authenticate = %q, want the one value %q", code, got, `Bearer realm="main", Cookie realm="main"`)
		}
		if strings.Contains(strings.ToLower(strings.Join(got, ",")), "basic") {
			t.Errorf("code %q: the challenge %q offers Basic", code, got)
		}
		body := problemBodyOf(t, w)
		wantType := "about:blank"
		if code != "" {
			wantType = "urn:speed:authn:" + code
		}
		if body["type"] != wantType {
			t.Errorf("code %q: type = %v, want %s", code, body["type"], wantType)
		}
	}
}

// TestTheRealmIsEscapedBeforeItIsWritten holds that a name from configuration
// cannot end the quoted string early or start another header.
func TestTheRealmIsEscapedBeforeItIsWritten(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"main", "main"},
		{`a"b`, `a\"b`},
		{`a\b`, `a\\b`},
		{`a\"b`, `a\\\"b`},
		{"a\r\nSet-Cookie: x=1", "aSet-Cookie: x=1"},
		{"a\nb", "ab"},
		{"", ""},
		{`"`, `\"`},
		{"用户", "用户"},
	} {
		if got := QuoteRealm(c.in); got != c.want {
			t.Errorf("QuoteRealm(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	w := serve(func(c *gin.Context) { Unauthorized(c, "a\"b\\c\r\nX-Injected: 1", "") }, http.MethodGet, nil, nil)
	v := w.Header().Get("WWW-Authenticate")
	if strings.ContainsAny(v, "\r\n") || w.Header().Get("X-Injected") != "" {
		t.Errorf("the realm reached the header as %q, with a line ending or another header", v)
	}
	if want := `Bearer realm="a\"b\\cX-Injected: 1", Cookie realm="a\"b\\cX-Injected: 1"`; v != want {
		t.Errorf("WWW-Authenticate = %q, want %q", v, want)
	}
}

// TestThrottledCarriesTheWaitRoundedUp holds Retry-After on a 429: whole seconds,
// never rounded down to a time the client would retry too early, and absent when
// there is nothing to wait.
func TestThrottledCarriesTheWaitRoundedUp(t *testing.T) {
	for _, c := range []struct {
		wait time.Duration
		want string
	}{
		{7 * time.Second, "7"},
		{6200 * time.Millisecond, "7"},
		{time.Millisecond, "1"},
		{0, ""},
		{-time.Second, ""},
	} {
		w := serve(func(g *gin.Context) { Throttled(g, c.wait) }, http.MethodGet, nil, nil)
		if w.Code != http.StatusTooManyRequests || problemBodyOf(t, w)["type"] != "urn:speed:authn:throttled" {
			t.Errorf("wait %v: status %d, body %s", c.wait, w.Code, w.Body)
		}
		if got := w.Header().Get("Retry-After"); got != c.want {
			t.Errorf("wait %v: Retry-After = %q, want %q", c.wait, got, c.want)
		}
	}
}

// TestUnavailableNeverSaysWhichDependencyFailed holds the 503: one type, no
// Retry-After, and a body of nothing but the problem's members; Busy is the one
// 503 that asks the client to come back in a second.
func TestUnavailableNeverSaysWhichDependencyFailed(t *testing.T) {
	w := serve(Unavailable, http.MethodGet, nil, nil)
	if w.Code != http.StatusServiceUnavailable || problemBodyOf(t, w)["type"] != "urn:speed:authn:unavailable" {
		t.Errorf("Unavailable wrote %d %s", w.Code, w.Body)
	}
	if got := w.Header().Get("Retry-After"); got != "" {
		t.Errorf("Unavailable sets Retry-After %q, which only a busy derivation carries", got)
	}
	for k := range problemBodyOf(t, w) {
		if k != "type" && k != "title" && k != "status" {
			t.Errorf("the 503 body carries %q", k)
		}
	}
	b := serve(Busy, http.MethodGet, nil, nil)
	if b.Code != http.StatusServiceUnavailable || b.Header().Get("Retry-After") != "1" ||
		problemBodyOf(t, b)["type"] != "urn:speed:authn:unavailable" {
		t.Errorf("Busy wrote %d, Retry-After %q, %s", b.Code, b.Header().Get("Retry-After"), b.Body)
	}
}

// identityOf builds the identity a gateway would stamp, with the challenge a
// carrier gives it.
func identityOf(t *testing.T, opts ...authntest.Option) authn.Identity {
	t.Helper()
	id, ok := authn.FromContext(authntest.Context(t, context.Background(), opts...))
	if !ok {
		t.Fatal("authntest built no identity")
	}
	return id
}

// TestStepUpRequiredNamesTheSchemeTheLevelAndTheAge holds the challenge of a
// requirement not met: the scheme of the identity's own carrier, the RFC 9470
// parameters, acr_values as the level's number and max_age as whole seconds.
func TestStepUpRequiredNamesTheSchemeTheLevelAndTheAge(t *testing.T) {
	for _, c := range []struct {
		name string
		id   authn.Identity
		req  authn.Requirement
		want string
	}{
		{
			"a cookie identity asking for local multi-factor", identityOf(t, authntest.WithChallenge("Cookie")), authn.FreshLocalMulti(10 * time.Minute),
			`Cookie error="insufficient_user_authentication", error_description="` + stepUpDescription + `", acr_values="2", max_age="600"`,
		},
		{
			"a bearer identity asking for a fresh first factor", identityOf(t), authn.FreshFirstFactor(),
			`Bearer error="insufficient_user_authentication", error_description="` + stepUpDescription + `", acr_values="1", max_age="300"`,
		},
		{
			"a Basic identity", identityOf(t, authntest.WithChallenge("Basic")),
			authn.Requirement{Assurance: authn.AssuranceMulti},
			`Basic error="insufficient_user_authentication", error_description="` + stepUpDescription + `", acr_values="2"`,
		},
		{
			"no age asked", identityOf(t),
			authn.Requirement{Assurance: authn.AssuranceSingle},
			`Bearer error="insufficient_user_authentication", error_description="` + stepUpDescription + `", acr_values="1"`,
		},
		{
			"an age that is not whole seconds is rounded up", identityOf(t),
			authn.Requirement{Assurance: authn.AssuranceMulti, MaxAge: 1500 * time.Millisecond},
			`Bearer error="insufficient_user_authentication", error_description="` + stepUpDescription + `", acr_values="2", max_age="2"`,
		},
		{
			"a negative age is not written", identityOf(t),
			authn.Requirement{Assurance: authn.AssuranceMulti, MaxAge: -time.Minute},
			`Bearer error="insufficient_user_authentication", error_description="` + stepUpDescription + `", acr_values="2"`,
		},
		{
			"the zero identity",
			authn.Identity{},
			authn.Requirement{Assurance: authn.AssuranceMulti, MaxAge: time.Minute},
			`Bearer error="insufficient_user_authentication", error_description="` + stepUpDescription + `", acr_values="2", max_age="60"`,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := serve(func(g *gin.Context) { StepUpRequired(g, c.id, c.req) }, http.MethodGet, nil, nil)
			if w.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", w.Code)
			}
			if got := w.Header().Values("WWW-Authenticate"); !slices.Equal(got, []string{c.want}) {
				t.Errorf("WWW-Authenticate = %q, want %q", got, c.want)
			}
			if got := problemBodyOf(t, w)["type"]; got != "urn:speed:authn:insufficient_user_authentication" {
				t.Errorf("type = %v", got)
			}
		})
	}
}

// TestStepUpRequiredMarksItsAnswer holds the mark a layer charging by outcome
// reads: the step-up 401 is marked, and the plain 401 writers leave the request
// unmarked so it stays a refusal of a credential.
func TestStepUpRequiredMarksItsAnswer(t *testing.T) {
	var marked bool
	serve(func(c *gin.Context) {
		StepUpRequired(c, identityOf(t), authn.FreshFirstFactor())
		marked = speedhttp.IsStepUp(c)
	}, http.MethodGet, nil, nil)
	if !marked {
		t.Fatal("StepUpRequired left its answer unmarked, so a refuse-by-outcome layer would charge it as a stuffed credential")
	}
}

func TestUnauthorizedDoesNotMarkItsAnswer(t *testing.T) {
	for _, code := range []string{"", CodeLoginFailed, CodeFlowInvalid, CodeLinkRequired} {
		var marked bool
		serve(func(c *gin.Context) {
			Unauthorized(c, "main", code)
			marked = speedhttp.IsStepUp(c)
		}, http.MethodGet, nil, nil)
		if marked {
			t.Errorf("Unauthorized with code %q marked its answer as a step-up", code)
		}
	}
}

// TestStepUpRequiredDoesNotTrustTheSchemeToBeAHeaderValue holds that a scheme
// that is not a token, which a defective stamp could carry, does not put a line
// ending or a space into the header.
func TestStepUpRequiredDoesNotTrustTheSchemeToBeAHeaderValue(t *testing.T) {
	for _, bad := range []string{"", "Bea rer", "Bearer\r\nX: 1", "Be\"arer", "Bearer,Basic", "用户"} {
		if got := challengeScheme(bad); got != "Bearer" {
			t.Errorf("challengeScheme(%q) = %q, want Bearer", bad, got)
		}
	}
	for _, ok := range []string{"Bearer", "Cookie", "Basic", "Negotiate", "X-Custom.v2"} {
		if got := challengeScheme(ok); got != ok {
			t.Errorf("challengeScheme(%q) = %q, want it unchanged", ok, got)
		}
	}
}

// TestContactRequiredCodeMatchesTheRootConstant holds the two writers of the
// contact-required type to one value: this capability's handlers build it as
// typePrefix + CodeContactRequired, and the managing faces of the other
// capabilities and a host's own surfaces write authn.ContactRequiredProblemType.
// A change to either side that the other does not follow fails here rather than
// splitting the type across surfaces.
func TestContactRequiredCodeMatchesTheRootConstant(t *testing.T) {
	if got := typePrefix + CodeContactRequired; got != authn.ContactRequiredProblemType {
		t.Errorf("typePrefix+CodeContactRequired = %q, but authn.ContactRequiredProblemType = %q",
			got, authn.ContactRequiredProblemType)
	}
}
