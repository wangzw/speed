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

// registerRig builds a rig whose registration switch is open and whose budgets
// are the declared defaults: ten registrations a minute per source address and
// the deployment-wide default.
func registerRig(t *testing.T) *rig {
	t.Helper()
	r := newRig(t, &fakeMethod{name: "password"})
	r.p.settings = readerWith(map[string]string{passwordSignupPath: signupOpen})
	return r
}

// registerBody is a well-formed registration body.
const registerBody = `{"username":"alice","email":"alice@example.com","password":"alice-password","` +
	`display_name":"Alice","locale":"zh-CN","time_zone":"Asia/Shanghai"}`

// registerFrom posts a registration from the source address given, which the
// test engine trusts as a proxy and reads out of the forwarded header.
func (r *rig) registerFrom(ip, body string) *httptest.ResponseRecorder {
	r.t.Helper()
	return r.send(http.MethodPost, "/api/v1/auth/register", body, "X-Forwarded-For", ip)
}

// TestARegistrationOpensAnAccountAndAnswersItsState holds the success path: 201,
// the registered state, the account, and the email awaiting verification. The
// route writes no flow record, no session and no cookie, and the account module
// is handed every member of the body.
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

// TestARegistrationRefusesABadBody holds the 400s the route produces itself: a
// required member left out, an unknown member, and a body over the capability's
// 64 KiB limit. None of them reaches the account module.
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

// TestARegistrationRefusesAnotherMediaType holds the 415 of the body guard: a
// registration that is not application/json is refused before the handler runs,
// in the capability's own problem code, and opens no account.
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

// TestACrossSiteRegistrationIsRefusedByTheWrapper holds that the cross-origin
// check runs before the handler: a cross-site POST register is refused with the
// gateway's 401 and opens no account, while the same body from this origin is
// accepted. The check is the production one the mounter wraps every route in.
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

// TestARegistrationRefusesAClosedSwitch holds the pre-check: with the account
// module's password-signup switch closed, or absent, the route answers 403
// signup-closed and calls no registration. The switch is re-read on every
// request, so opening it takes effect without a restart.
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

	// The authority is the account module's own check: a pre-check that let the
	// request through still answers signup-closed when Register refuses it.
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
// reports to the answer the design gives it.
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
		{"an identifier that cannot be normalized", authn.ErrInvalidIdentifier, http.StatusBadRequest, "urn:speed:authn:bad-request", ""},
		{"a reserved username", authn.ErrUsernameReserved, http.StatusBadRequest, "urn:speed:authn:bad-request", ""},
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

// TestThePerSourceBudgetStopsARegistration holds the minute bucket: one source
// address may register up to the budget a minute, another source address has its
// own bucket, and the attempt past the budget is a 429 with a Retry-After that
// does not outlast the bucket.
func TestThePerSourceBudgetStopsARegistration(t *testing.T) {
	r := registerRig(t)
	r.p.settings = readerWith(map[string]string{
		passwordSignupPath: signupOpen,
		signupIPLimitPath:  "1",
	})

	first := r.registerFrom("198.51.100.7", registerBody)
	if first.Code != http.StatusCreated {
		t.Fatalf("the first registration answered %d, want 201: %s", first.Code, first.Body.String())
	}
	// Another source address is a bucket of its own: the budget this case set is
	// spent for one address only.
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

// TestTheGlobalBudgetStopsARegistration holds the hour bucket: the deployment's
// registrations are counted in one bucket for every source, and the attempt past
// the budget is a 429 with a Retry-After within the hour. The two budget cases
// run on different source addresses with the per-source budget well above what
// they spend, so the refusal can only be the deployment-wide bucket's.
func TestTheGlobalBudgetStopsARegistration(t *testing.T) {
	r := registerRig(t)
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

// TestAFailingBudgetCounterTakesItsOwnDirection holds the two directions: a
// per-source counter that cannot be written is skipped, which is the direction of
// the source-address pre-throttle, while the deployment-wide counter that cannot
// be written refuses with a 503, which is the direction of a dependency that
// cannot answer.
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

// TestARegistrationAnswersAThrottledModuleWithItsDelay holds that the back-off
// the account module reports after the uniqueness check keeps its own delay in
// the answer rather than being flattened to a 429 with none.
func TestARegistrationAnswersAThrottledModuleWithItsDelay(t *testing.T) {
	r := registerRig(t)
	r.signups.err = &authn.ThrottledError{RetryAfter: 42 * time.Second}

	rec := r.send(http.MethodPost, "/api/v1/auth/register", registerBody)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "42" {
		t.Errorf("a throttled module answered %d, Retry-After %q, want 429 with 42",
			rec.Code, rec.Header().Get("Retry-After"))
	}
}

// TestARegistrationAnUnknownFailureIsAnOutage holds the default: an error no
// branch names is a 503 and not a pass, so a defective dependency cannot open an
// account by answering something unexpected.
func TestARegistrationAnUnknownFailureIsAnOutage(t *testing.T) {
	r := registerRig(t)
	r.signups.err = errors.New("the store answered something else")

	rec := r.send(http.MethodPost, "/api/v1/auth/register", registerBody)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("an unnamed failure answered %d, want 503: %s", rec.Code, rec.Body.String())
	}
}
