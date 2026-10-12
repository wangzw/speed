package guard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/internal/mount"
)

func init() { gin.SetMode(gin.TestMode) }

// providerID is a well-formed provider identifier, which an identity an IdP
// declared multi-factor for has to carry.
const providerID = "0198f0aa-0000-7000-8000-000000000001"

// multiFactors are the options of an identity proven with two factors of
// different kinds.
func multiFactors() []authntest.Option {
	return []authntest.Option{
		authntest.WithAssurance(authn.AssuranceMulti),
		authntest.WithMethods(authn.MethodPassword, authn.MethodTOTP),
	}
}

// idpMultiFactors are the options of an identity whose multi-factor level an IdP
// declared and this process did not prove.
func idpMultiFactors() []authntest.Option {
	return append(multiFactors(), authntest.WithIDPAssured(true), authntest.WithProvider(providerID))
}

// ctxWith builds the request context of an identity with the options, the way the
// gateway leaves one.
func ctxWith(t *testing.T, opts ...authntest.Option) context.Context {
	t.Helper()
	return authntest.Context(t, context.Background(), opts...)
}

// pair builds a request context and the identity it carries, for a case that
// drives both this middleware and the rest of the capability's answer with the
// same caller.
func pair(t *testing.T, opts ...authntest.Option) (context.Context, authn.Identity) {
	t.Helper()
	ctx := ctxWith(t, opts...)
	id, ok := authn.FromContext(ctx)
	if !ok {
		t.Fatal("authntest built no identity")
	}
	return ctx, id
}

// reached is the handler past the middleware; answering 200 with a fixed body
// tells a case that the request got through it.
func reached(c *gin.Context) { c.String(http.StatusOK, "reached") }

// serveAs runs Require(req) in front of reached on /x, with ctx on the request,
// and returns the recorder.
func serveAs(ctx context.Context, req authn.Requirement) *httptest.ResponseRecorder {
	e := gin.New()
	e.GET("/x", Require(req), reached)
	r := httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	return w
}

// serveStepUp runs the answer the rest of the capability writes for an identity
// that does not meet a requirement, so that a case can hold the two equal.
func serveStepUp(id authn.Identity, req authn.Requirement) *httptest.ResponseRecorder {
	e := gin.New()
	e.GET("/x", func(c *gin.Context) { mount.StepUpRequired(c, id, req) })
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	return w
}

// problemType reads the type member of a problem details body.
func problemType(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("the body %q is not a JSON object: %v", w.Body.String(), err)
	}
	typ, _ := m["type"].(string)
	return typ
}

// TestRequireAdmitsAnIdentityThatMeetsTheRequirement holds the admit side of the
// middleware: an identity whose assurance is at least the requirement's, whose
// authentication is recent enough and, when the requirement is local, whose level
// this process proved, reaches the handler. A requirement that asks for nothing
// admits an identity that simply exists.
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

// TestRequireRefusesWithTheStepUp401 holds the refuse side: an identity below the
// level, one an IdP declared when the requirement is local, one too old for the
// window, a requirement whose window is not positive, and a request with no
// identity at all are all answered 401 with the step-up problem, and the handler
// past the middleware never runs.
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
		{"no identity", context.Background(), authn.Requirement{Assurance: authn.AssuranceMulti}},
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

// TestTheChallengeNamesTheLevelTheAgeAndTheScheme holds the challenge of a
// refusal value by value: the scheme of the identity's own carrier, the RFC 9470
// parameters by name, acr_values as the level's number, and max_age as the
// requirement's whole seconds rounded up, written only when the requirement asks
// for freshness.
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
			context.Background(),
			authn.Requirement{Assurance: authn.AssuranceSingle},
			`Bearer error="insufficient_user_authentication", error_description="` + stepUpDescription + `", acr_values="1"`,
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
		{
			"the zero identity",
			context.Background(),
			authn.Requirement{Assurance: authn.AssuranceMulti, MaxAge: time.Minute},
			`Bearer error="insufficient_user_authentication", error_description="` + stepUpDescription + `", acr_values="2", max_age="60"`,
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

// TestASchemeThatIsNotATokenIsWrittenAsBearer holds that a challenge scheme a
// defective stamp or a zero identity could carry does not put a line ending, a
// space or a bare value into the field, and that a scheme the gateway does write
// reaches it unchanged.
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

// TestARequestWithNoIdentityIsRefused holds the closed failure direction on its
// own: a request the gateway left no identity on is refused, not passed, so the
// middleware is safe wherever it is mounted.
func TestARequestWithNoIdentityIsRefused(t *testing.T) {
	w := serveAs(context.Background(), authn.Requirement{Assurance: authn.AssuranceMulti})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if typ := problemType(t, w); typ != typePrefix+insufficientUserAuthentication {
		t.Errorf("type = %q, want %s%s", typ, typePrefix, insufficientUserAuthentication)
	}
	if got := w.Header().Get(challengeHeader); got == "" {
		t.Error("the refusal carries no challenge, so a caller is told nothing it can act on")
	}
}

// TestARefusalIsTheSameShapeTheRestOfTheCapabilityWrites holds that this
// middleware's 401 is the one the login engine and the factor routes write: the
// same challenge header and the same problem type for the same identity and
// requirement, so that a caller reads one shape wherever it steps up.
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
