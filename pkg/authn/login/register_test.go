package login

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

func registerRig(t *testing.T) *rig {
	t.Helper()
	r := newRig(t, &fakeMethod{name: "password"})
	r.p.settings = readerWith(map[string]string{passwordSignupPath: signupOpen})
	return r
}

const registerBody = `{"username":"alice","email":"alice@example.com","password":"alice-password","` +
	`display_name":"Alice","locale":"zh-CN","time_zone":"Asia/Shanghai"}`

var fixedClock = func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }

func (r *rig) registerFrom(ip, body string) *httptest.ResponseRecorder {
	r.t.Helper()
	return r.send(http.MethodPost, "/api/v1/auth/register", body, "X-Forwarded-For", ip)
}

// TestARegistrationOpensAnAccountAndAnswersItsState holds the 201, the registered state, the account and the email awaiting verification.
func TestARegistrationOpensAnAccountAndAnswersItsState(t *testing.T) {
	r := registerRig(t)

	rec := r.send(http.MethodPost, "/api/v1/auth/register", registerBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("a registration answered %d, want 201: %s", rec.Code, rec.Body.String())
	}
	body := decode(t, rec.Body.String())
	if body["state"] != "registered" {
		t.Errorf("state = %v, want registered", body["state"])
	}
	if body["account_id"] != "acct-new" {
		t.Errorf("account_id = %v, want the account the module opened", body["account_id"])
	}
	verification, ok := body["verification"].(map[string]any)
	if !ok || verification["state"] != "pending" || verification["email"] != "alice@example.com" {
		t.Errorf("verification = %v, want pending at the submitted address", body["verification"])
	}

	if len(r.signups.registered) != 1 {
		t.Fatalf("the account module was asked %d times, want once", len(r.signups.registered))
	}
	got := r.signups.registered[0]
	want := authn.RegisterAccount{
		Username: "alice", Email: "alice@example.com", Password: "alice-password",
		DisplayName: "Alice", Locale: "zh-CN", TimeZone: "Asia/Shanghai",
	}
	if got != want {
		t.Errorf("the module was handed %+v, want %+v", got, want)
	}

	if len(r.sessions.created) != 0 {
		t.Errorf("a registration created %d sessions, want none", len(r.sessions.created))
	}
	if cookies := rec.Result().Cookies(); len(cookies) != 0 {
		t.Errorf("a registration set %d cookies, want none", len(cookies))
	}
	for _, key := range r.kv.keys() {
		if strings.HasPrefix(key, "authn:flow:") {
			t.Errorf("a registration wrote the flow record %q, want none", key)
		}
	}
}

// TestARegistrationRefusesABadBody holds the 400s for a missing member, an unknown member and a body over 64 KiB.
func TestARegistrationRefusesABadBody(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"no username", `{"email":"a@b.c","password":"a-password"}`},
		{"no email", `{"username":"a","password":"a-password"}`},
		{"no password", `{"username":"a","email":"a@b.c"}`},
		{"an unknown member", `{"username":"a","email":"a@b.c","password":"a-password","admin":true}`},
		{"a body over 64 KiB", registerBody + strings.Repeat(" ", 70_000)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := registerRig(t)
			rec := r.send(http.MethodPost, "/api/v1/auth/register", c.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s answered %d, want 400: %s", c.name, rec.Code, rec.Body.String())
			}
			if body := decode(t, rec.Body.String()); body["type"] != "urn:speed:authn:bad-request" {
				t.Errorf("%s answered type %v, want urn:speed:authn:bad-request", c.name, body["type"])
			}
			if len(r.signups.registered) != 0 {
				t.Errorf("%s reached the account module, which the body's own refusal should not", c.name)
			}
		})
	}
}

// TestARegistrationRefusesAnotherMediaType holds that a non-JSON registration is refused before the handler and opens no account.
func TestARegistrationRefusesAnotherMediaType(t *testing.T) {
	r := registerRig(t)

	rec := r.send(http.MethodPost, "/api/v1/auth/register", registerBody, "Content-Type", "text/plain")
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("a text/plain registration answered %d, want 415: %s", rec.Code, rec.Body.String())
	}
	if body := decode(t, rec.Body.String()); body["type"] != "urn:speed:authn:unsupported-media-type" {
		t.Errorf("the 415's type = %v, want urn:speed:authn:unsupported-media-type", body["type"])
	}
	if len(r.signups.registered) != 0 {
		t.Error("a registration with another media type reached the account module")
	}
}

// TestACrossSiteRegistrationIsRefusedByTheWrapper holds that a cross-site POST register is refused before the handler, while the same body from this origin passes.
func TestACrossSiteRegistrationIsRefusedByTheWrapper(t *testing.T) {
	r := registerRig(t)

	rec := r.send(http.MethodPost, "/api/v1/auth/register", registerBody,
		"Sec-Fetch-Site", "cross-site", "Origin", "https://evil.example.org")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("a cross-site registration answered %d, want the gateway's 401: %s",
			rec.Code, rec.Body.String())
	}
	if len(r.signups.registered) != 0 {
		t.Error("a cross-site registration reached the handler")
	}

	same := r.send(http.MethodPost, "/api/v1/auth/register", registerBody,
		"Sec-Fetch-Site", "same-origin", "Origin", "https://app.example.com")
	if same.Code != http.StatusCreated {
		t.Errorf("the same registration from this origin answered %d, want 201: %s",
			same.Code, same.Body.String())
	}
}

// TestARegistrationRefusesAClosedSwitch holds that a closed or absent switch answers 403 signup-closed and calls no registration.
func TestARegistrationRefusesAClosedSwitch(t *testing.T) {
	r := newRig(t, &fakeMethod{name: "password"})
	r.p.settings = readerWith(nil)

	closed := r.send(http.MethodPost, "/api/v1/auth/register", registerBody)
	if closed.Code != http.StatusForbidden {
		t.Fatalf("a closed registration answered %d, want 403: %s", closed.Code, closed.Body.String())
	}
	if body := decode(t, closed.Body.String()); body["type"] != "urn:speed:authn:signup-closed" {
		t.Errorf("a closed registration answered type %v, want urn:speed:authn:signup-closed", body["type"])
	}
	if len(r.signups.registered) != 0 {
		t.Error("a closed registration was handed to the account module")
	}

	r.signups.err = authn.ErrSignupClosed
	r.p.settings = readerWith(map[string]string{passwordSignupPath: signupOpen})
	refused := r.send(http.MethodPost, "/api/v1/auth/register", registerBody)
	if refused.Code != http.StatusForbidden ||
		decode(t, refused.Body.String())["type"] != "urn:speed:authn:signup-closed" {
		t.Errorf("the module's signup-closed answered %d: %s, want 403 signup-closed",
			refused.Code, refused.Body.String())
	}
}

// TestARegistrationMapsTheModulesRefusals holds each failure the account module
// reports to the answer it gives.
func TestARegistrationMapsTheModulesRefusals(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		typ    string
		retry  string
	}{
		{"a taken username", authn.ErrAccountExists, http.StatusConflict, "urn:speed:authn:username-taken", ""},
		{"a taken email address", authn.ErrContactTaken, http.StatusConflict, "urn:speed:authn:email-taken", ""},
		{"a password policy", authn.ErrPasswordPolicy, http.StatusBadRequest, "urn:speed:authn:bad-request", ""},
		{"an email address that cannot be normalized", authn.ErrInvalidIdentifier, http.StatusBadRequest, "urn:speed:authn:bad-request", ""},
		{"a username that cannot be normalized", authn.UsernameShape(authn.ErrInvalidIdentifier), http.StatusBadRequest, "urn:speed:authn:username-invalid", ""},
		{"a reserved username", authn.ErrUsernameReserved, http.StatusBadRequest, "urn:speed:authn:username-reserved", ""},
		{"an argument outside its domain", authn.ErrInvalidArgument, http.StatusBadRequest, "urn:speed:authn:bad-request", ""},
		{"a full derivation capacity", authn.ErrBusy, http.StatusServiceUnavailable, "urn:speed:authn:unavailable", "1"},
		{"the username back-off", &authn.ThrottledError{RetryAfter: 30 * time.Second}, http.StatusTooManyRequests, "urn:speed:authn:throttled", "30"},
		{"an unavailable database", authn.ErrUnavailable, http.StatusServiceUnavailable, "urn:speed:authn:unavailable", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := registerRig(t)
			r.signups.err = c.err
			rec := r.send(http.MethodPost, "/api/v1/auth/register", registerBody)
			if rec.Code != c.status {
				t.Fatalf("%s answered %d, want %d: %s", c.name, rec.Code, c.status, rec.Body.String())
			}
			if body := decode(t, rec.Body.String()); body["type"] != c.typ {
				t.Errorf("%s answered type %v, want %s", c.name, body["type"], c.typ)
			}
			if got := rec.Header().Get("Retry-After"); got != c.retry {
				t.Errorf("%s answered Retry-After %q, want %q", c.name, got, c.retry)
			}
		})
	}
}

// TestThePerSourceBudgetStopsARegistration holds that each source has its own minute bucket and the attempt past it is a 429 within the bucket.
func TestThePerSourceBudgetStopsARegistration(t *testing.T) {
	r := registerRig(t)
	r.p.clock = fixedClock
	r.p.settings = readerWith(map[string]string{
		passwordSignupPath: signupOpen,
		signupIPLimitPath:  "1",
	})

	first := r.registerFrom("198.51.100.7", registerBody)
	if first.Code != http.StatusCreated {
		t.Fatalf("the first registration answered %d, want 201: %s", first.Code, first.Body.String())
	}
	other := r.registerFrom("198.51.100.8", registerBody)
	if other.Code != http.StatusCreated {
		t.Fatalf("a registration from another source address answered %d, want 201: %s",
			other.Code, other.Body.String())
	}
	second := r.registerFrom("198.51.100.7", registerBody)
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("a registration past the per-source budget answered %d, want 429: %s",
			second.Code, second.Body.String())
	}
	if body := decode(t, second.Body.String()); body["type"] != "urn:speed:authn:throttled" {
		t.Errorf("the 429's type = %v, want urn:speed:authn:throttled", body["type"])
	}
	wait := second.Header().Get("Retry-After")
	if n, err := strconv.Atoi(wait); err != nil || n < 1 || n > 60 {
		t.Errorf("Retry-After = %q, want whole seconds within the minute bucket", wait)
	}
	if len(r.signups.registered) != 2 {
		t.Errorf("the account module was asked %d times, want the two under the budget", len(r.signups.registered))
	}
}

// TestTheGlobalBudgetStopsARegistration holds that one deployment-wide hour bucket bounds every source, answering 429 within the hour.
func TestTheGlobalBudgetStopsARegistration(t *testing.T) {
	r := registerRig(t)
	r.p.clock = fixedClock
	r.p.settings = readerWith(map[string]string{
		passwordSignupPath:     signupOpen,
		signupGlobalHourlyPath: "1",
		signupIPLimitPath:      "100",
	})

	first := r.registerFrom("198.51.100.7", registerBody)
	if first.Code != http.StatusCreated {
		t.Fatalf("the first registration answered %d, want 201: %s", first.Code, first.Body.String())
	}
	second := r.registerFrom("198.51.100.8", registerBody)
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("a registration past the hourly budget answered %d, want 429: %s",
			second.Code, second.Body.String())
	}
	if wait := second.Header().Get("Retry-After"); wait == "" {
		t.Error("the 429 past the hourly budget carries no Retry-After")
	}
	if len(r.signups.registered) != 1 {
		t.Errorf("the account module was asked %d times, want the one under the budget", len(r.signups.registered))
	}
}

// TestAFailingBudgetCounterTakesItsOwnDirection holds that a failed per-source count is skipped and a failed global count answers 503.
func TestAFailingBudgetCounterTakesItsOwnDirection(t *testing.T) {
	t.Run("the per-source counter is skipped", func(t *testing.T) {
		r := registerRig(t)
		r.p.settings = readerWith(map[string]string{
			passwordSignupPath: signupOpen,
			signupIPLimitPath:  "5",
		})
		r.kv.failIncrPrefix = signupSourcePrefix

		rec := r.send(http.MethodPost, "/api/v1/auth/register", registerBody)
		if rec.Code != http.StatusCreated {
			t.Fatalf("a registration whose source counter failed answered %d, want 201: %s",
				rec.Code, rec.Body.String())
		}
		if len(r.signups.registered) != 1 {
			t.Errorf("the account module was asked %d times, want once", len(r.signups.registered))
		}
	})
	t.Run("the deployment-wide counter refuses", func(t *testing.T) {
		r := registerRig(t)
		r.kv.failIncrPrefix = signupGlobalPrefix

		rec := r.send(http.MethodPost, "/api/v1/auth/register", registerBody)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("a registration whose global counter failed answered %d, want 503: %s",
				rec.Code, rec.Body.String())
		}
		if body := decode(t, rec.Body.String()); body["type"] != "urn:speed:authn:unavailable" {
			t.Errorf("the 503's type = %v, want urn:speed:authn:unavailable", body["type"])
		}
		if len(r.signups.registered) != 0 {
			t.Error("a registration was opened although the global budget could not be counted")
		}
	})
}

// TestARegistrationAnswersAThrottledModuleWithItsDelay holds that the module's own retry delay is kept rather than flattened.
func TestARegistrationAnswersAThrottledModuleWithItsDelay(t *testing.T) {
	r := registerRig(t)
	r.signups.err = &authn.ThrottledError{RetryAfter: 42 * time.Second}

	rec := r.send(http.MethodPost, "/api/v1/auth/register", registerBody)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "42" {
		t.Errorf("a throttled module answered %d, Retry-After %q, want 429 with 42",
			rec.Code, rec.Header().Get("Retry-After"))
	}
}

// TestARegistrationAnUnknownFailureIsAnOutage holds that an unnamed error answers 503 and opens no account.
func TestARegistrationAnUnknownFailureIsAnOutage(t *testing.T) {
	r := registerRig(t)
	r.signups.err = errors.New("the store answered something else")

	rec := r.send(http.MethodPost, "/api/v1/auth/register", registerBody)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("an unnamed failure answered %d, want 503: %s", rec.Code, rec.Body.String())
	}
}
