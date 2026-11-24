package guard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/internal/mount"
)

func init() { gin.SetMode(gin.TestMode) }

const providerID = "0198f0aa-0000-7000-8000-000000000001"

func multiFactors() []authntest.Option {
	return []authntest.Option{
		authntest.WithAssurance(authn.AssuranceMulti),
		authntest.WithMethods(authn.MethodPassword, authn.MethodTOTP),
	}
}

func idpMultiFactors() []authntest.Option {
	return append(multiFactors(), authntest.WithIDPAssured(true), authntest.WithProvider(providerID))
}

func ctxWith(t *testing.T, opts ...authntest.Option) context.Context {
	t.Helper()
	return authntest.Context(t, context.Background(), opts...)
}

func pair(t *testing.T, opts ...authntest.Option) (context.Context, authn.Identity) {
	t.Helper()
	ctx := ctxWith(t, opts...)
	id, ok := authn.FromContext(ctx)
	if !ok {
		t.Fatal("authntest built no identity")
	}
	return ctx, id
}

func reached(c *gin.Context) { c.String(http.StatusOK, "reached") }

func serveAs(ctx context.Context, req authn.Requirement) *httptest.ResponseRecorder {
	e := gin.New()
	e.GET("/x", Require(req), reached)
	r := httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	return w
}

func serveStepUp(id authn.Identity, req authn.Requirement) *httptest.ResponseRecorder {
	e := gin.New()
	e.GET("/x", func(c *gin.Context) { mount.StepUpRequired(c, id, req) })
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	return w
}

func problemType(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("the body %q is not a JSON object: %v", w.Body.String(), err)
	}
	typ, _ := m["type"].(string)
	return typ
}

// TestRequireAdmitsAnIdentityThatMeetsTheRequirement holds that an identity of sufficient level, age and locality reaches the handler.
func TestRequireAdmitsAnIdentityThatMeetsTheRequirement(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		name string
		ctx  context.Context
		req  authn.Requirement
	}{
		{"an identity against a requirement that asks for nothing", ctxWith(t), authn.Requirement{}},
		{"a single factor against a single-factor requirement", ctxWith(t), authn.Requirement{Assurance: authn.AssuranceSingle}},
		{"a single factor fresh within the window", ctxWith(t, authntest.WithAuthTime(now)), authn.Requirement{Assurance: authn.AssuranceSingle, MaxAge: 5 * time.Minute}},
		{"a multi factor against a multi-factor requirement", ctxWith(t, multiFactors()...), authn.Requirement{Assurance: authn.AssuranceMulti}},
		{"a multi factor fresh within the window", ctxWith(t, append(multiFactors(), authntest.WithAuthTime(now))...), authn.FreshLocalMulti(10 * time.Minute)},
		{"a locally proven multi factor against a local requirement", ctxWith(t, multiFactors()...), authn.Requirement{Assurance: authn.AssuranceMulti, Local: true}},
		{"an IdP-declared multi factor against a requirement that is not local", ctxWith(t, idpMultiFactors()...), authn.Requirement{Assurance: authn.AssuranceMulti}},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := serveAs(c.ctx, c.req)
			if w.Code != http.StatusOK || w.Body.String() != "reached" {
				t.Errorf("the middleware answered %d %q, want 200 reached", w.Code, w.Body.String())
			}
			if got := w.Header().Values(challengeHeader); len(got) != 0 {
				t.Errorf("the admitted request carries a challenge %q, want none", got)
			}
		})
	}
}

// TestRequireRefusesWithTheStepUp401 holds that a low, stale, foreign or non-positive-window identity answers the step-up 401 and the handler never runs.
func TestRequireRefusesWithTheStepUp401(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		name string
		ctx  context.Context
		req  authn.Requirement
	}{
		{"a single factor against a multi-factor requirement", ctxWith(t), authn.Requirement{Assurance: authn.AssuranceMulti}},
		{"an IdP-declared multi factor against a local requirement", ctxWith(t, idpMultiFactors()...), authn.Requirement{Assurance: authn.AssuranceMulti, Local: true}},
		{"a single factor too old for the window", ctxWith(t), authn.Requirement{Assurance: authn.AssuranceSingle, MaxAge: 5 * time.Minute}},
		{"a multi factor too old for the window", ctxWith(t, multiFactors()...), authn.Requirement{Assurance: authn.AssuranceMulti, MaxAge: 5 * time.Minute}},
		{"a window that is not positive reads as nobody", ctxWith(t, append(multiFactors(), authntest.WithAuthTime(now))...), authn.FreshLocalMulti(0)},
		{"a negative window reads as nobody", ctxWith(t, append(multiFactors(), authntest.WithAuthTime(now))...), authn.FreshLocalMulti(-time.Minute)},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := serveAs(c.ctx, c.req)
			if w.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", w.Code)
			}
			if w.Body.String() == "reached" {
				t.Error("the handler past the middleware ran on a request it refused")
			}
			if typ := problemType(t, w); typ != typePrefix+insufficientUserAuthentication {
				t.Errorf("type = %q, want %s%s", typ, typePrefix, insufficientUserAuthentication)
			}
		})
	}
}

// TestTheChallengeNamesTheLevelTheAgeAndTheScheme holds the scheme, the RFC 9470 parameters, acr_values and the rounded-up max_age.
func TestTheChallengeNamesTheLevelTheAgeAndTheScheme(t *testing.T) {
	for _, c := range []struct {
		name string
		ctx  context.Context
		req  authn.Requirement
		want string
	}{
		{
			"a cookie identity asking for local multi-factor",
			ctxWith(t, authntest.WithChallenge("Cookie")), authn.FreshLocalMulti(10 * time.Minute),
			`Cookie error="insufficient_user_authentication", error_description="` + stepUpDescription + `", acr_values="2", max_age="600"`,
		},
		{
			"a bearer identity asking for a fresh first factor",
			ctxWith(t), authn.FreshFirstFactor(),
			`Bearer error="insufficient_user_authentication", error_description="` + stepUpDescription + `", acr_values="1", max_age="300"`,
		},
		{
			"a Basic identity",
			ctxWith(t, authntest.WithChallenge("Basic")),
			authn.Requirement{Assurance: authn.AssuranceMulti},
			`Basic error="insufficient_user_authentication", error_description="` + stepUpDescription + `", acr_values="2"`,
		},
		{
			"a level asked with no age",
			ctxWith(t),
			authn.Requirement{Assurance: authn.AssuranceMulti},
			`Bearer error="insufficient_user_authentication", error_description="` + stepUpDescription + `", acr_values="2"`,
		},
		{
			"an age that is not whole seconds is rounded up",
			ctxWith(t),
			authn.Requirement{Assurance: authn.AssuranceMulti, MaxAge: 1500 * time.Millisecond},
			`Bearer error="insufficient_user_authentication", error_description="` + stepUpDescription + `", acr_values="2", max_age="2"`,
		},
		{
			"an age below a second is asked for and rounds up to one",
			ctxWith(t),
			authn.Requirement{Assurance: authn.AssuranceMulti, MaxAge: 200 * time.Millisecond},
			`Bearer error="insufficient_user_authentication", error_description="` + stepUpDescription + `", acr_values="2", max_age="1"`,
		},
		{
			"a negative age is not written",
			ctxWith(t),
			authn.Requirement{Assurance: authn.AssuranceMulti, MaxAge: -time.Minute},
			`Bearer error="insufficient_user_authentication", error_description="` + stepUpDescription + `", acr_values="2"`,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := serveAs(c.ctx, c.req)
			if w.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", w.Code)
			}
			if got := w.Header().Values(challengeHeader); !slices.Equal(got, []string{c.want}) {
				t.Errorf("%s = %q, want %q", challengeHeader, got, c.want)
			}
			if typ := problemType(t, w); typ != typePrefix+insufficientUserAuthentication {
				t.Errorf("type = %q, want %s%s", typ, typePrefix, insufficientUserAuthentication)
			}
		})
	}
}

// TestASchemeThatIsNotATokenIsWrittenAsBearer holds that a malformed scheme becomes Bearer and a gateway-written one passes unchanged.
func TestASchemeThatIsNotATokenIsWrittenAsBearer(t *testing.T) {
	for _, bad := range []string{"", "Bea rer", "Bearer\r\nX: 1", "Be\"arer", "Bearer,Basic", "用户", "a[b", "a\\b", "a]b", "Bearer "} {
		if got := challengeScheme(bad); got != "Bearer" {
			t.Errorf("challengeScheme(%q) = %q, want Bearer", bad, got)
		}
	}
	for _, ok := range []string{"Bearer", "Cookie", "Basic", "Negotiate", "X-Custom.v2", "z", "Z", "A", "9", "0", "!", "a`b"} {
		if got := challengeScheme(ok); got != ok {
			t.Errorf("challengeScheme(%q) = %q, want it unchanged", ok, got)
		}
	}
}

// TestARequestWithNoIdentityIsRefused holds that a missing identity is refused, not passed.
func TestARequestWithNoIdentityIsRefused(t *testing.T) {
	w := serveAs(context.Background(), authn.Requirement{Assurance: authn.AssuranceMulti})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if typ := problemType(t, w); typ != "about:blank" {
		t.Errorf("type = %q, want about:blank: an anonymous refusal is not a step-up", typ)
	}
	if got, want := w.Header().Values(challengeHeader), []string{`Bearer realm="", Cookie realm=""`}; !slices.Equal(got, want) {
		t.Errorf("%s = %q, want %q", challengeHeader, got, want)
	}
	if got := w.Header().Get(challengeHeader); strings.Contains(got, insufficientUserAuthentication) {
		t.Errorf("the anonymous challenge asks for a step-up: %q", got)
	}
}

// TestAnAnonymousRefusalIsDistinguishedFromAStepUp holds the untyped anonymous 401 apart from the typed step-up 401.
func TestAnAnonymousRefusalIsDistinguishedFromAStepUp(t *testing.T) {
	req := authn.Requirement{Assurance: authn.AssuranceMulti}
	for _, c := range []struct {
		name       string
		w          *httptest.ResponseRecorder
		wantStepUp bool
	}{
		{"anonymous", serveAs(context.Background(), req), false},
		{"present but below the requirement", serveAs(ctxWith(t), req), true},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", c.w.Code)
			}
			typ := problemType(t, c.w)
			if got := typ == typePrefix+insufficientUserAuthentication; got != c.wantStepUp {
				t.Errorf("type = %q names the step-up answer = %v, want %v", typ, got, c.wantStepUp)
			}
			challenge := c.w.Header().Get(challengeHeader)
			if got := strings.Contains(challenge, insufficientUserAuthentication); got != c.wantStepUp {
				t.Errorf("the challenge %q names the step-up code = %v, want %v", challenge, got, c.wantStepUp)
			}
		})
	}
}

// TestARefusalIsTheSameShapeTheRestOfTheCapabilityWrites holds the same challenge header and problem type as the engine and factor routes.
func TestARefusalIsTheSameShapeTheRestOfTheCapabilityWrites(t *testing.T) {
	for _, c := range []struct {
		name string
		opts []authntest.Option
		req  authn.Requirement
	}{
		{"a cookie identity asking for local multi-factor", []authntest.Option{authntest.WithChallenge("Cookie")}, authn.FreshLocalMulti(10 * time.Minute)},
		{"a bearer identity asking for a fresh first factor", nil, authn.FreshFirstFactor()},
		{"a Basic identity", []authntest.Option{authntest.WithChallenge("Basic")}, authn.Requirement{Assurance: authn.AssuranceMulti}},
		{"an age that is not whole seconds", nil, authn.Requirement{Assurance: authn.AssuranceMulti, MaxAge: 1500 * time.Millisecond}},
		{"a negative age", nil, authn.Requirement{Assurance: authn.AssuranceMulti, MaxAge: -time.Minute}},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx, id := pair(t, c.opts...)
			mine := serveAs(ctx, c.req)
			theirs := serveStepUp(id, c.req)
			if got, want := mine.Header().Values(challengeHeader), theirs.Header().Values(challengeHeader); !slices.Equal(got, want) {
				t.Errorf("%s = %q, want the capability's %q", challengeHeader, got, want)
			}
			if got, want := problemType(t, mine), problemType(t, theirs); got != want {
				t.Errorf("type = %q, want the capability's %q", got, want)
			}
		})
	}
}
