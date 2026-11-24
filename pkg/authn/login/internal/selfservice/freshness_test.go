package selfservice_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
)

const idpProvider = "0198f0aa-0000-7000-8000-000000000001"

var factorsOnly = []string{"mfa.Factors"}

type freshnessCase struct {
	name     string
	factors  []authn.FactorRecord
	identity []authntest.Option
	window   time.Duration
	setErr   error
	refused  bool
	acr      string
	maxAge   string
}

func activeTOTP() []authn.FactorRecord {
	return []authn.FactorRecord{{ID: "f1", Account: account, Kind: authn.FactorTOTP, Status: authn.FactorActive}}
}

func localMulti(age time.Duration) []authntest.Option {
	return []authntest.Option{
		authntest.WithAssurance(authn.AssuranceMulti),
		authntest.WithMethods(authn.MethodPassword, authn.MethodTOTP),
		authntest.WithAuthTime(now.Add(-age)),
	}
}

func idpMulti(age time.Duration) []authntest.Option {
	return []authntest.Option{
		authntest.WithAssurance(authn.AssuranceMulti),
		authntest.WithIDPAssured(true),
		authntest.WithProvider(idpProvider),
		authntest.WithMethods(authn.MethodOIDC),
		authntest.WithAuthTime(now.Add(-age)),
	}
}

func single(age time.Duration) []authntest.Option {
	return []authntest.Option{authntest.WithAuthTime(now.Add(-age))}
}

func freshnessCases() []freshnessCase {
	return []freshnessCase{
		{name: "a factor, and the identity is a single factor", factors: activeTOTP(), identity: single(time.Minute), refused: true, acr: "2", maxAge: "600"},
		{name: "a factor, and the multi-factor level is an IdP's declaration", factors: activeTOTP(), identity: idpMulti(time.Minute), refused: true, acr: "2", maxAge: "600"},
		{name: "a factor, and the multi-factor proof is past the window", factors: activeTOTP(), identity: localMulti(11 * time.Minute), refused: true, acr: "2", maxAge: "600"},
		{name: "a factor, and the multi-factor proof is just inside the window", factors: activeTOTP(), identity: localMulti(9 * time.Minute)},
		{name: "no factor, and the first factor is past five minutes", identity: single(6 * time.Minute), refused: true, acr: "1", maxAge: "300"},
		{name: "no factor, and the first factor is inside five minutes", identity: single(4 * time.Minute)},
		{name: "no factor, and an IdP's declaration is a fresh first factor", identity: idpMulti(4 * time.Minute)},
		{name: "no factor, and a stale IdP declaration", identity: idpMulti(6 * time.Minute), refused: true, acr: "1", maxAge: "300"},
		{
			name:    "a factor still pending is not one the account has",
			factors: []authn.FactorRecord{{ID: "f1", Account: account, Kind: authn.FactorTOTP, Status: authn.FactorPending}}, identity: single(time.Minute),
		},
		{
			name:    "a factor disabled is not one the account has",
			factors: []authn.FactorRecord{{ID: "f1", Account: account, Kind: authn.FactorTOTP, Status: authn.FactorDisabled}}, identity: single(time.Minute),
		},
		{
			name:    "one active factor among others that are not decides",
			factors: append([]authn.FactorRecord{{ID: "f0", Kind: authn.FactorSMS, Status: authn.FactorDisabled}}, activeTOTP()...), identity: single(time.Minute), refused: true, acr: "2", maxAge: "600",
		},
		{name: "a shorter window setting refuses a proof the default would take", factors: activeTOTP(), identity: localMulti(3 * time.Minute), window: 2 * time.Minute, refused: true, acr: "2", maxAge: "120"},
		{name: "a longer window setting takes a proof the default would refuse", factors: activeTOTP(), identity: localMulti(20 * time.Minute), window: 30 * time.Minute},
		{name: "a window outside the setting's domain reads as the default, refusing", factors: activeTOTP(), identity: localMulti(11 * time.Minute), window: 2 * time.Hour, refused: true, acr: "2", maxAge: "600"},
		{name: "a window outside the setting's domain reads as the default, taking", factors: activeTOTP(), identity: localMulti(9 * time.Minute), window: 2 * time.Hour},
		{name: "a window at its low bound is used, refusing a nine-minute proof", factors: activeTOTP(), identity: localMulti(9 * time.Minute), window: time.Minute, refused: true, acr: "2", maxAge: "60"},
		{name: "a window at its high bound is used, taking a thirty-minute proof", factors: activeTOTP(), identity: localMulti(30 * time.Minute), window: time.Hour},
		{name: "a window that cannot be read is the default", factors: activeTOTP(), identity: localMulti(11 * time.Minute), setErr: errors.New("not loaded"), refused: true, acr: "2", maxAge: "600"},
	}
}

// TestFreshnessIsAskedForOnEveryRouteThatChangesTheWayIn runs each case
// on each of the routes that add, choose or remove a way to sign in.
func TestFreshnessIsAskedForOnEveryRouteThatChangesTheWayIn(t *testing.T) {
	for _, tc := range gatedRoutes() {
		for _, c := range freshnessCases() {
			t.Run(tc.name+" / "+c.name, func(t *testing.T) {
				h := newHarness(t)
				h.mfa.factors = c.factors
				if c.window > 0 {
					h.settings.values["authn.mfa.recent-window"] = c.window
				}
				if c.setErr != nil {
					h.settings.errs["authn.mfa.recent-window"] = c.setErr
				}
				h.as(c.identity...)
				w := h.do(tc.req)
				if !c.refused {
					if w.Code == http.StatusUnauthorized && problemType(t, w) == "insufficient_user_authentication" {
						t.Fatalf("an identity that meets the requirement was asked to step up: %s", w.Header().Get("WWW-Authenticate"))
					}
					if len(h.log.all()) < 2 {
						t.Fatalf("the handler was not reached: calls %v", h.log.all())
					}
					return
				}
				if w.Code != http.StatusUnauthorized || problemType(t, w) != "insufficient_user_authentication" {
					t.Fatalf("answer = %d %s, want 401 insufficient_user_authentication", w.Code, w.Body)
				}
				challenge := w.Header().Get("WWW-Authenticate")
				for _, want := range []string{`error="insufficient_user_authentication"`, `acr_values="` + c.acr + `"`, `max_age="` + c.maxAge + `"`} {
					if !strings.Contains(challenge, want) {
						t.Errorf("WWW-Authenticate = %q, lacks %s", challenge, want)
					}
				}
				if got := h.log.names(); !slices.Equal(got, factorsOnly) {
					t.Errorf("calls after a step-up refusal = %v, want only %v", got, factorsOnly)
				}
				if len(h.audit.entries())+len(h.bus.published()) > 1 {
					t.Errorf("a refusal wrote more than its audit row: %v %v", h.audit.entries(), h.bus.published())
				}
			})
		}
	}
}

// TestTheFactorsThatCannotBeReadAreNotReadAsNone holds that an unlistable account is a 503, not the lower requirement of no factors.
func TestTheFactorsThatCannotBeReadAreNotReadAsNone(t *testing.T) {
	for _, tc := range gatedRoutes() {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.mfa.err = authn.ErrUnavailable
			w := h.do(tc.req)
			if w.Code != http.StatusServiceUnavailable || problemType(t, w) != "unavailable" {
				t.Fatalf("answer = %d %s, want 503 unavailable", w.Code, w.Body)
			}
			if got := h.log.names(); !slices.Equal(got, factorsOnly) {
				t.Errorf("calls = %v, want only %v", got, factorsOnly)
			}
		})
	}
}

// TestAnIdentityWithNoSessionIsRefusedBeforeAnyCall holds that an identity on no session cannot change the account.
func TestAnIdentityWithNoSessionIsRefusedBeforeAnyCall(t *testing.T) {
	for _, tc := range mutating {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.m.As(authntest.WithAccount(account), authntest.WithAuthTime(now.Add(-time.Minute)))
			w := h.do(tc.req)
			if w.Code != http.StatusUnauthorized || problemType(t, w) != "" {
				t.Fatalf("answer = %d %s, want the plain 401", w.Code, w.Body)
			}
			if calls := h.log.all(); len(calls) != 0 {
				t.Errorf("calls = %v", calls)
			}
		})
	}
}

// TestRegisteringAContactAddressNeedsNoFreshAuthentication holds that registering and proving a contact is separate from making it a sign-in way.
func TestRegisteringAContactAddressNeedsNoFreshAuthentication(t *testing.T) {
	stale := func(h *harness) {
		h.t.Helper()
		h.activeFactor()
		h.as()
	}
	settledAccount := func(h *harness) {
		h.t.Helper()
		h.accounts.identifiers = []authn.Identifier{
			{
				ID: "ident-user", Kind: authn.IdentifierUsername, Value: "ada",
				VerifiedAt: now.Add(-time.Hour), VerifiedVia: authn.VerifiedViaAdmin, SignIn: true,
			},
			{
				ID: "ident-default", Kind: authn.IdentifierEmail, Value: "home@example.com",
				VerifiedAt: now.Add(-time.Hour), VerifiedVia: authn.VerifiedViaEmailCode,
				SignIn: true, Contact: true, DefaultContact: true,
			},
			{
				ID: "ident-other", Kind: authn.IdentifierEmail, Value: "work@example.com",
				VerifiedAt: now.Add(-time.Hour), VerifiedVia: authn.VerifiedViaEmailCode,
				Contact: true,
			},
		}
	}

	t.Run("an email is requested and proved", func(t *testing.T) {
		h := emailHarness(t)
		settledAccount(h)
		stale(h)
		if w := h.post("/identifiers", addBody("email", "ada@example.com")); w.Code != http.StatusOK || problemState(t, w) != "pending" {
			t.Fatalf("the add answered %d %s, want 200 pending", w.Code, w.Body)
		}
		w := h.post("/identifiers/verify", verifyBody(h.email.sent[0].code))
		if w.Code != http.StatusOK || problemState(t, w) != "added" {
			t.Fatalf("the verify answered %d %s, want 200 added", w.Code, w.Body)
		}
		if len(h.sessions.rotated) != 0 || len(h.sessions.revoked) != 0 {
			t.Errorf("proving an address settled the sessions: rotated %v, revoked %v", h.sessions.rotated, h.sessions.revoked)
		}
	})

	t.Run("a phone is requested and verified", func(t *testing.T) {
		h := newHarness(t)
		stale(h)
		if w := h.post("/phone", `{"phone":"`+phoneNumber+`"}`); w.Code != http.StatusOK {
			t.Fatalf("the start answered %d %s, want 200", w.Code, w.Body)
		}
		if w := h.post("/phone/verify", `{"code":"123456"}`); w.Code != http.StatusNoContent {
			t.Fatalf("the verify answered %d %s, want 204", w.Code, w.Body)
		}
		if len(h.sessions.rotated) != 0 || len(h.sessions.revoked) != 0 {
			t.Errorf("proving a number settled the sessions: rotated %v, revoked %v", h.sessions.rotated, h.sessions.revoked)
		}
	})

	for _, c := range []struct {
		name  string
		setup func(h *harness)
		do    func(h *harness) *httptest.ResponseRecorder
	}{
		{
			name: "an address that would become the default contact",
			do: func(h *harness) *httptest.ResponseRecorder {
				h.post("/identifiers", addBody("email", "ada@example.com"))
				return h.post("/identifiers/verify", verifyBody(h.email.sent[0].code))
			},
		},
		{
			name: "a number the active SMS factor sends to",
			setup: func(h *harness) {
				h.mfa.factors = []authn.FactorRecord{
					{ID: "factor-sms", Account: account, Kind: authn.FactorSMS, Status: authn.FactorActive},
					{ID: "factor-totp", Account: account, Kind: authn.FactorTOTP, Status: authn.FactorActive},
				}
			},
			do: func(h *harness) *httptest.ResponseRecorder {
				h.post("/phone", `{"phone":"`+phoneNumber+`"}`)
				return h.post("/phone/verify", `{"code":"123456"}`)
			},
		},
	} {
		t.Run(c.name+" still needs it", func(t *testing.T) {
			h := emailHarness(t)
			if c.setup == nil {
				h.activeFactor()
			} else {
				c.setup(h)
			}
			h.as()
			w := c.do(h)
			if w.Code != http.StatusUnauthorized || problemType(t, w) != "insufficient_user_authentication" {
				t.Fatalf("answer = %d %s, want 401 insufficient_user_authentication", w.Code, w.Body)
			}
		})
	}

	for _, c := range []struct {
		name  string
		do    func(h *harness) *httptest.ResponseRecorder
		setup func(h *harness)
	}{
		{"adding a username", func(h *harness) *httptest.ResponseRecorder {
			return h.post("/identifiers", addBody("username", "ada2"))
		}, nil},
		{"choosing a sign-in way", func(h *harness) *httptest.ResponseRecorder {
			return h.put("/identifiers/ident-proved", `{"sign_in":true}`)
		}, func(h *harness) { h.axesAccount() }},
		{"choosing the default contact", func(h *harness) *httptest.ResponseRecorder {
			return h.put("/identifiers/ident-proved", `{"default_contact":true}`)
		}, func(h *harness) { h.axesAccount() }},
		{"removing an identifier", func(h *harness) *httptest.ResponseRecorder {
			return h.delete("/identifiers/ident-1")
		}, func(h *harness) { h.axesAccount() }},
	} {
		t.Run(c.name+" still needs it", func(t *testing.T) {
			h := newHarness(t)
			if c.setup != nil {
				c.setup(h)
			}
			stale(h)
			w := c.do(h)
			if w.Code != http.StatusUnauthorized || problemType(t, w) != "insufficient_user_authentication" {
				t.Fatalf("answer = %d %s, want 401 insufficient_user_authentication", w.Code, w.Body)
			}
		})
	}
}

// TestAnUnreadableSettingIsReportedOnceAndReadAsItsDefault holds that an unreadable or out-of-domain value is logged once and the default used.
func TestAnUnreadableSettingIsReportedOnceAndReadAsItsDefault(t *testing.T) {
	h := newHarness(t)
	h.activeFactor()
	h.multi()
	h.settings.errs["authn.mfa.recent-window"] = errors.New("settings: not loaded")
	for range 3 {
		if w := h.put("/password", `{"new_password":"x"}`); w.Code != http.StatusNoContent {
			t.Fatalf("answer = %d, want 204 under the default window", w.Code)
		}
	}
	if got := strings.Count(h.logs.String(), "the setting cannot be read"); got != 1 {
		t.Errorf("the fault was logged %d times, want once: %s", got, h.logs)
	}

	delete(h.settings.errs, "authn.mfa.recent-window")
	h.settings.values["authn.mfa.recent-window"] = 2 * time.Hour
	h.put("/password", `{"new_password":"x"}`)
	h.put("/password", `{"new_password":"x"}`)
	if got := strings.Count(h.logs.String(), "outside its domain"); got != 1 {
		t.Errorf("the out-of-domain value was logged %d times, want once: %s", got, h.logs)
	}
}
