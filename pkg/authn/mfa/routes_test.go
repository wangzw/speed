package mfa

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
)

const (
	providerUUID = "0192f5a0-7b1c-7d3a-9e11-3c2d4e5f6a7b"
	sessionUUID  = "0192f5a0-7b1c-7d3a-9e11-3c2d4e5f6a7c"
	listPath     = "/api/v1/auth/factors"
	problemType  = "urn:speed:authn:"
)

// routed is a rig with the routes mounted.
func routed(t *testing.T, kinds ...authn.FactorKind) *rig {
	t.Helper()
	var r *rig
	forEach := func(h *gorm.DB) {
		factors := make([]authn.Factor, len(kinds))
		for i, k := range kinds {
			factors[i] = newFactor(k)
		}
		r = newRig(t, h, factors...)
	}
	forEach(openDialect(t, sqliteDialect))
	r.p.mountRoutes()
	return r
}

type problemBody struct {
	Type   string `json:"type"`
	Status int    `json:"status"`
}

func (r *rig) do(method, path string, headers map[string]string) *httptest.ResponseRecorder {
	r.t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return r.mounter.Do(req)
}

func problemOf(t *testing.T, w *httptest.ResponseRecorder) problemBody {
	t.Helper()
	var p problemBody
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatalf("the body %q is no problem: %v", w.Body.String(), err)
	}
	return p
}

func TestTheRoutesAreTheTwoTheDesignNamesAndBothAreGuarded(t *testing.T) {
	r := routed(t, authn.FactorTOTP)
	got := r.mounter.Registrations()
	if len(got) != 2 {
		t.Fatalf("%d routes registered, want 2: %+v", len(got), got)
	}
	want := []mounttest.Registration{
		{Kind: mounttest.Guarded, Methods: []string{http.MethodGet}, Path: "/api/v1/auth/factors"},
		{Kind: mounttest.Guarded, Methods: []string{http.MethodDelete}, Path: "/api/v1/auth/factors/:id"},
	}
	for i := range want {
		if got[i].Kind != want[i].Kind || !slices.Equal(got[i].Methods, want[i].Methods) || got[i].Path != want[i].Path || got[i].Impersonable {
			t.Errorf("route %d is %+v, want %+v and not impersonable", i, got[i], want[i])
		}
	}
}

func TestTheListIsTheCallersOwnAndCarriesNothingSecret(t *testing.T) {
	r := routed(t, authn.FactorTOTP, authn.FactorSMS)
	active := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
	pending := r.insertRow(acct, authn.FactorSMS, authn.FactorPending, r.clock.read(), nil)
	r.activeFactor("someone-else", authn.FactorTOTP, time.Hour)
	if err := r.h.Exec("UPDATE authn_factors SET label = 'my phone', last_used_at = ? WHERE id = ?", r.clock.read().UTC(), active).Error; err != nil {
		t.Fatal(err)
	}
	r.mounter.As(authntest.WithAccount(acct))

	w := r.do(http.MethodGet, listPath, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", listPath, w.Code, w.Body.String())
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	var body struct {
		Factors []map[string]any `json:"factors"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("the body %q: %v", w.Body.String(), err)
	}
	if len(body.Factors) != 2 {
		t.Fatalf("the list has %d factors, want the caller's 2: %s", len(body.Factors), w.Body.String())
	}
	allowed := []string{"id", "kind", "label", "status", "created_at", "activated_at", "last_used_at"}
	for _, f := range body.Factors {
		for k := range f {
			if !slices.Contains(allowed, k) {
				t.Errorf("the list carries the field %q, which is not one a factor is shown with", k)
			}
		}
	}
	if body.Factors[0]["id"] != active || body.Factors[0]["status"] != "active" || body.Factors[0]["kind"] != "totp" || body.Factors[0]["label"] != "my phone" {
		t.Errorf("the first factor is %v", body.Factors[0])
	}
	if body.Factors[1]["id"] != pending || body.Factors[1]["status"] != "pending" {
		t.Errorf("the second factor is %v", body.Factors[1])
	}
	if _, has := body.Factors[1]["activated_at"]; has {
		t.Error("a pending factor is shown with an activation time")
	}
	if got, has := body.Factors[0]["last_used_at"]; !has || got != base.UTC().Format(time.RFC3339) {
		t.Errorf("the used factor's last_used_at is %v (present %v), want %s", got, has, base.UTC().Format(time.RFC3339))
	}
	if _, has := body.Factors[1]["last_used_at"]; has {
		t.Error("a factor that was never used is shown with a last_used_at")
	}
	for _, secret := range []string{"secret", "seed", "otpauth", "public_key", "credential"} {
		if strings.Contains(strings.ToLower(w.Body.String()), secret) {
			t.Errorf("the list mentions %q", secret)
		}
	}
	// An account with none gets an empty list, which is a list.
	r.mounter.As(authntest.WithAccount("nobody"))
	if got := r.do(http.MethodGet, listPath, nil).Body.String(); !strings.Contains(got, `"factors":[]`) {
		t.Errorf("the list of an account with none is %s, want an empty array", got)
	}
	r.mounter.AsAnonymous()
	if got := r.do(http.MethodGet, listPath, nil).Code; got != http.StatusUnauthorized {
		t.Errorf("an anonymous request = %d, want 401", got)
	}
}

func multiAt(age time.Duration, extra ...authntest.Option) []authntest.Option {
	return append([]authntest.Option{
		authntest.WithAccount(acct),
		authntest.WithAssurance(authn.AssuranceMulti),
		authntest.WithMethods(authn.MethodPassword, authn.MethodTOTP),
		authntest.WithAuthTime(base.Add(-age)),
	}, extra...)
}

func TestDeletingAFactorNeedsAFreshMultiFactorAuthenticationVerifiedHere(t *testing.T) {
	cases := []struct {
		name    string
		opts    []authntest.Option
		window  time.Duration
		want    int
		maxAge  string
		deleted bool
	}{
		{"a local multi-factor authentication of a minute ago", multiAt(time.Minute), 0, http.StatusNoContent, "", true},
		{"one just inside the window", multiAt(10*time.Minute - time.Second), 0, http.StatusNoContent, "", true},
		{"one just outside the window", multiAt(10*time.Minute + time.Second), 0, http.StatusUnauthorized, "600", false},
		{"a single factor, however fresh", []authntest.Option{authntest.WithAccount(acct), authntest.WithAuthTime(base)}, 0, http.StatusUnauthorized, "600", false},
		{"a multi-factor level only an IdP declared", multiAt(time.Minute, authntest.WithIDPAssured(true), authntest.WithProvider(providerUUID)), 0, http.StatusUnauthorized, "600", false},
		{"the window the setting says", multiAt(3 * time.Minute), 2 * time.Minute, http.StatusUnauthorized, "120", false},
		{"a longer window the setting says", multiAt(40 * time.Minute), time.Hour, http.StatusNoContent, "", true},
		{"a window outside the setting's domain reads as the default", multiAt(30 * time.Minute), 5 * time.Hour, http.StatusUnauthorized, "600", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := routed(t, authn.FactorTOTP)
			id := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
			if c.window != 0 {
				r.store.set(recentWindowPath, c.window)
			}
			r.mounter.As(c.opts...)
			w := r.do(http.MethodDelete, listPath+"/"+id, nil)
			if w.Code != c.want {
				t.Fatalf("DELETE = %d (%s), want %d", w.Code, w.Body.String(), c.want)
			}
			_, there := r.status(id)
			if there == c.deleted {
				t.Errorf("the factor is there = %v after the request, want %v", there, !c.deleted)
			}
			if c.want == http.StatusUnauthorized {
				if p := problemOf(t, w); p.Type != problemType+"insufficient_user_authentication" {
					t.Errorf("the problem is %q, want insufficient_user_authentication", p.Type)
				}
				challenge := w.Header().Get("WWW-Authenticate")
				for _, part := range []string{`error="insufficient_user_authentication"`, `acr_values="2"`, `max_age="` + c.maxAge + `"`} {
					if !strings.Contains(challenge, part) {
						t.Errorf("the challenge %q lacks %s", challenge, part)
					}
				}
				if r.p.factors[authn.FactorTOTP].(*fakeFactor).count("remove") != 0 {
					t.Error("the factor was asked to clean its data for a request that was refused")
				}
			}
		})
	}
}

func TestDeletingAFactorThatIsNotTheCallersOrIsGoneReadsAsAlreadyDone(t *testing.T) {
	r := routed(t, authn.FactorTOTP)
	theirs := r.activeFactor("someone-else", authn.FactorTOTP, time.Hour)
	r.mounter.As(multiAt(time.Minute)...)
	for _, id := range []string{theirs, "no-such-factor"} {
		if w := r.do(http.MethodDelete, listPath+"/"+id, nil); w.Code != http.StatusNoContent {
			t.Errorf("DELETE %s = %d, want 204", id, w.Code)
		}
	}
	if _, there := r.status(theirs); !there {
		t.Error("another account's factor was deleted")
	}
	long := strings.Repeat("x", maxFactorIDLen+1)
	if w := r.do(http.MethodDelete, listPath+"/"+long, nil); w.Code != http.StatusBadRequest {
		t.Errorf("an identifier longer than any there is = %d, want 400", w.Code)
	}
	// The longest identifier the guard admits is not over it: it reaches the
	// register as an identifier that is not there and answers 204, not 400.
	boundary := strings.Repeat("x", maxFactorIDLen)
	if w := r.do(http.MethodDelete, listPath+"/"+boundary, nil); w.Code != http.StatusNoContent {
		t.Errorf("an identifier of exactly the longest admitted length = %d, want 204 as a factor that is not there", w.Code)
	}
}

func TestDeletingTheLastFactorOfAnAccountThatRequiresOneIsAConflict(t *testing.T) {
	r := routed(t, authn.FactorTOTP)
	id := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
	r.accounts.required[acct] = true
	r.mounter.As(multiAt(time.Minute)...)
	w := r.do(http.MethodDelete, listPath+"/"+id, nil)
	if w.Code != http.StatusConflict || problemOf(t, w).Type != problemType+"conflict" {
		t.Errorf("DELETE = %d %s, want 409 conflict", w.Code, w.Body.String())
	}
	if _, there := r.status(id); !there {
		t.Error("the last factor was deleted")
	}
}

func TestDeletingIsAnOutageWhenTheFactorCannotCleanUp(t *testing.T) {
	r := routed(t, authn.FactorTOTP)
	id := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
	r.p.factors[authn.FactorTOTP].(*fakeFactor).removeFn = func(context.Context, authn.AccountID, authn.FactorRecord) error {
		return errors.New("seed store down at db.internal:5432")
	}
	r.mounter.As(multiAt(time.Minute)...)
	w := r.do(http.MethodDelete, listPath+"/"+id, nil)
	if w.Code != http.StatusServiceUnavailable || problemOf(t, w).Type != problemType+"unavailable" {
		t.Errorf("DELETE = %d %s, want 503 unavailable", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "5432") {
		t.Errorf("the answer names the cause: %s", w.Body.String())
	}
	if _, there := r.status(id); !there {
		t.Error("the row went although its data could not be cleaned")
	}
}

func TestTheCrossOriginCheckGuardsTheDeletion(t *testing.T) {
	r := routed(t, authn.FactorTOTP)
	id := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
	r.mounter.As(multiAt(time.Minute)...)
	for name, headers := range map[string]map[string]string{
		"a request another site caused": {"Sec-Fetch-Site": "cross-site"},
		"an origin that is not trusted": {"Origin": "https://evil.example.org"},
	} {
		w := r.do(http.MethodDelete, listPath+"/"+id, headers)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s: DELETE = %d, want 401", name, w.Code)
		}
		if _, there := r.status(id); !there {
			t.Fatalf("%s: the factor was deleted", name)
		}
	}
	if w := r.do(http.MethodDelete, listPath+"/"+id, map[string]string{"Origin": mounttest.DefaultPublicURL}); w.Code != http.StatusNoContent {
		t.Errorf("a request from the deployment's own origin = %d, want 204", w.Code)
	}
}

func TestAnAnonymousRequestIsTurnedAwayAndTheFactorStays(t *testing.T) {
	r := routed(t, authn.FactorTOTP)
	id := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
	r.mounter.AsAnonymous()
	if w := r.do(http.MethodDelete, listPath+"/"+id, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("DELETE with no identity = %d, want 401", w.Code)
	}
	if _, there := r.status(id); !there {
		t.Error("the factor was deleted")
	}
}

// The gateway refuses an impersonating identity on a route that was not registered
// for it, and the handlers refuse it themselves as well, so the refusal does not
// depend on one layer. The second is exercised without the gateway in front.
func TestAnImpersonatingIdentityCannotDeleteAFactor(t *testing.T) {
	r := routed(t, authn.FactorTOTP)
	id := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
	impersonating := append(multiAt(time.Minute),
		authntest.WithActor("admin-1"), authntest.WithSession(sessionUUID), authntest.WithScope("tenant-1"))

	r.mounter.As(impersonating...)
	if w := r.do(http.MethodDelete, listPath+"/"+id, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("through the gateway: DELETE = %d, want 401", w.Code)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodDelete, listPath+"/"+id, nil)
	c.Request = c.Request.WithContext(authntest.Context(t, c.Request.Context(), impersonating...))
	c.Params = gin.Params{{Key: factorParam, Value: id}}
	r.p.deleteFactor(c)
	if w.Code != http.StatusForbidden || problemOf(t, w).Type != problemType+mount.CodeImpersonationRestricted {
		t.Errorf("the handler alone: DELETE = %d %s, want 403 impersonation-restricted", w.Code, w.Body.String())
	}
	if _, there := r.status(id); !there {
		t.Error("the factor was deleted by an impersonating identity")
	}
}

func TestTheListIsAnOutageWhenTheRegisterCannotBeRead(t *testing.T) {
	r := routed(t, authn.FactorTOTP)
	r.mounter.As(authntest.WithAccount(acct))
	if err := r.h.Exec("DROP TABLE authn_factors").Error; err != nil {
		t.Fatal(err)
	}
	w := r.do(http.MethodGet, listPath, nil)
	if w.Code != http.StatusServiceUnavailable || problemOf(t, w).Type != problemType+"unavailable" {
		t.Errorf("GET = %d %s, want 503 unavailable", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "authn_factors") {
		t.Errorf("the answer names the table: %s", w.Body.String())
	}
}

// A recent-window that cannot be read reads as its declared default of ten
// minutes: a deletion with an authentication two minutes old goes through.
// Reading the type's zero value would demand an authentication made this instant
// and refuse a fresh one.
func TestDeletingUnderAnUnreadableRecentWindowUsesTheDeclaredDefault(t *testing.T) {
	r := routed(t, authn.FactorTOTP)
	id := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
	r.store.fail(recentWindowPath, errors.New("settings: not loaded"))
	r.mounter.As(multiAt(2 * time.Minute)...)
	w := r.do(http.MethodDelete, listPath+"/"+id, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("DELETE = %d (%s), want 204 under the declared ten-minute default", w.Code, w.Body.String())
	}
	if _, there := r.status(id); there {
		t.Error("the factor stayed although the declared default window allows the deletion")
	}
}

// A recent-window below its domain's minimum reads as the declared default of ten
// minutes: a deletion with an authentication two minutes old goes through.
// Honoring a stored thirty seconds instead would refuse it.
func TestAnOutOfDomainRecentWindowUsesTheDeclaredDefault(t *testing.T) {
	r := routed(t, authn.FactorTOTP)
	id := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
	r.store.set(recentWindowPath, 30*time.Second)
	r.mounter.As(multiAt(2 * time.Minute)...)
	w := r.do(http.MethodDelete, listPath+"/"+id, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("DELETE = %d (%s), want 204 under the declared ten-minute default", w.Code, w.Body.String())
	}
	if _, there := r.status(id); there {
		t.Error("the factor stayed although the declared default window allows the deletion")
	}
}
