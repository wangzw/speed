package selfservice_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
	"github.com/vislake/speed/pkg/authn/login/internal/selfservice"
)

func registered(m *mounttest.Mounter) []string {
	var out []string
	for _, r := range m.Registrations() {
		for _, method := range r.Methods {
			out = append(out, method+" "+strings.TrimPrefix(r.Path, basePath))
		}
	}
	slices.Sort(out)
	return out
}

// TestTheRoutesAreTheseAndNoOthers holds the set of credential-changing and identifier routes, with no flow route.
func TestTheRoutesAreTheseAndNoOthers(t *testing.T) {
	h := newHarness(t)
	want := []string{
		"DELETE /identifiers/:id",
		"DELETE /links/:id",
		"POST /identifiers",
		"POST /identifiers/verify",
		"POST /phone",
		"POST /phone/verify",
		"PUT /identifiers/:id",
		"PUT /password",
	}
	if got := registered(h.m); !slices.Equal(got, want) {
		t.Fatalf("routes = %v, want %v", got, want)
	}
}

// TestEveryRouteIsGuardedAndImpersonable holds the access level of each
// registration.
func TestEveryRouteIsGuardedAndImpersonable(t *testing.T) {
	h := newHarness(t)
	for _, r := range h.m.Registrations() {
		if r.Kind != mounttest.Guarded {
			t.Errorf("%v %s is %s, want guarded", r.Methods, r.Path, r.Kind)
		}
		if !r.Impersonable {
			t.Errorf("%v %s is not impersonable, and every self-service route is", r.Methods, r.Path)
		}
	}
}

// TestThePhoneRoutesAreMountedOnlyWithSMSCodes holds that without the SMS
// capability the two phone routes are absent and the rest are there.
func TestThePhoneRoutesAreMountedOnlyWithSMSCodes(t *testing.T) {
	h := newHarness(t, func(c *config) { c.noSMS = true })
	want := []string{
		"DELETE /identifiers/:id",
		"DELETE /links/:id",
		"POST /identifiers",
		"POST /identifiers/verify",
		"PUT /identifiers/:id",
		"PUT /password",
	}
	if got := registered(h.m); !slices.Equal(got, want) {
		t.Fatalf("routes without SMSCodes = %v, want %v", got, want)
	}
	for _, path := range []string{"/phone", "/phone/verify"} {
		if w := h.post(path, `{"phone":"+8613800000000","code":"1"}`); w.Code != http.StatusNotFound {
			t.Errorf("POST %s without SMSCodes = %d, want 404", path, w.Code)
		}
	}
}

// TestNewNamesWhatIsMissing holds that missing dependencies are named and the phone dependencies were asked for only with SMS present.
func TestNewNamesWhatIsMissing(t *testing.T) {
	full := func() selfservice.Deps {
		h := newHarness(t)
		return selfservice.Deps{
			Accounts: h.accounts, Admin: h.admin, Signup: h.signup, Passwords: h.pw, Sessions: h.sessions, MFA: h.mfa,
			SMSCodes: h.sms, KV: h.kv, Throttle: h.throttle,
		}
	}
	cases := []struct {
		name   string
		mutate func(*selfservice.Deps)
		want   string
	}{
		{"complete", func(*selfservice.Deps) {}, ""},
		{"no accounts", func(d *selfservice.Deps) { d.Accounts = nil }, "Accounts"},
		{"no admin", func(d *selfservice.Deps) { d.Admin = nil }, "Admin"},
		{"no passwords", func(d *selfservice.Deps) { d.Passwords = nil }, "Passwords"},
		{"no sessions", func(d *selfservice.Deps) { d.Sessions = nil }, "Sessions"},
		{"no mfa", func(d *selfservice.Deps) { d.MFA = nil }, "MFA"},
		{"sms without kv", func(d *selfservice.Deps) { d.KV = nil }, "KV"},
		{"sms without throttle", func(d *selfservice.Deps) { d.Throttle = nil }, "Throttle"},
		{"no sms needs neither", func(d *selfservice.Deps) { d.SMSCodes, d.KV, d.Throttle = nil, nil, nil }, ""},
		{"email without kv", func(d *selfservice.Deps) {
			d.SMSCodes, d.Throttle = nil, nil
			d.EmailSender, d.KV = &fakeEmail{}, nil
		}, "KV"},
		{"email with kv needs no throttle", func(d *selfservice.Deps) {
			d.SMSCodes, d.Throttle = nil, nil
			d.EmailSender = &fakeEmail{}
		}, ""},
		{"email without signup", func(d *selfservice.Deps) {
			d.SMSCodes, d.Throttle = nil, nil
			d.EmailSender, d.Signup = &fakeEmail{}, nil
		}, "Signup"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := full()
			c.mutate(&d)
			svc, err := selfservice.New(d)
			switch {
			case c.want == "" && (err != nil || svc == nil):
				t.Fatalf("New = %v, %v, want a service", svc, err)
			case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
				t.Fatalf("New error = %v, want it to name %s", err, c.want)
			}
		})
	}
}

// TestACrossSiteRequestIsRefusedBeforeAnyCall holds that every write passed the cross-origin check and a refused one reaches no dependency.
func TestACrossSiteRequestIsRefusedBeforeAnyCall(t *testing.T) {
	for _, tc := range mutating {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			req := tc.req
			req.header = map[string]string{"Sec-Fetch-Site": "cross-site"}
			w := h.do(req)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("a cross-site %s = %d, want 401", tc.name, w.Code)
			}
			if calls := h.log.all(); len(calls) != 0 {
				t.Fatalf("a refused cross-site request made calls: %v", calls)
			}
		})
	}
}

// TestEveryAnswerIsNotCached holds the header the Mounter's wrapper puts on all
// of them, on a success and on a refusal.
func TestEveryAnswerIsNotCached(t *testing.T) {
	h := newHarness(t)
	h.richAccount()
	if got := h.delete("/links/link-1").Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("DELETE /links/:id Cache-Control = %q, want no-store", got)
	}
	if got := h.put("/password", `{}`).Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("a refused PUT /password Cache-Control = %q, want no-store", got)
	}
}
