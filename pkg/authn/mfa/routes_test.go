package mfa

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func (r *rig) doJSON(method, path string, body any) *httptest.ResponseRecorder {
	r.t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		r.t.Fatalf("encoding the request body: %v", err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
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

func cookieValue(w *httptest.ResponseRecorder, name string) (string, bool) {
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return c.Value, true
		}
	}
	return "", false
}

// TestTheRoutesAreTheThreeTheDesignNamesAndAllAreGuarded checks that the routes are the three the design names and all are guarded.
func TestTheRoutesAreTheThreeTheDesignNamesAndAllAreGuarded(t *testing.T) {
	r := routed(t, authn.FactorTOTP)
	got := r.mounter.Registrations()
	if len(got) != 3 {
		t.Fatalf("%d routes registered, want 3: %+v", len(got), got)
	}
	want := []mounttest.Registration{
		{Kind: mounttest.Guarded, Methods: []string{http.MethodGet}, Path: "/api/v1/auth/factors", Impersonable: true},
		{Kind: mounttest.Guarded, Methods: []string{http.MethodDelete}, Path: "/api/v1/auth/factors/:id", Impersonable: true},
		{Kind: mounttest.Guarded, Methods: []string{http.MethodPatch}, Path: "/api/v1/auth/factors/:id", Impersonable: true},
	}
	for i := range want {
		if got[i].Kind != want[i].Kind || !slices.Equal(got[i].Methods, want[i].Methods) ||
			got[i].Path != want[i].Path || got[i].Impersonable != want[i].Impersonable {
			t.Errorf("route %d is %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestTheListIsTheCallersOwnAndCarriesNothingSecret checks that the list is the caller's own and carries nothing secret.
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

// TestDeletingAFactorNeedsAFreshMultiFactorAuthenticationVerifiedHere checks that deleting a factor needs a fresh multi-factor authentication verified here.
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
		{"a single factor, fresh: the last factor may be removed by the first factor", []authntest.Option{authntest.WithAccount(acct), authntest.WithAuthTime(base)}, 0, http.StatusNoContent, "", true},
		{"an IdP-declared multi factor, the last factor, an account without a password: the re-login door", multiAt(time.Minute, authntest.WithIDPAssured(true), authntest.WithProvider(providerUUID)), 0, http.StatusNoContent, "", true},
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
				if got := r.sessions.rotationCalls(); len(got) != 0 {
					t.Errorf("a refused deletion rotated the requesting session: %v", got)
				}
			}
		})
	}
}

// TestDeletingALastFactorNeedsAFreshFirstFactor checks that only the last factor may be removed on a fresh first factor.
func TestDeletingALastFactorNeedsAFreshFirstFactor(t *testing.T) {
	t.Run("two factors, a fresh first factor", func(t *testing.T) {
		r := routed(t, authn.FactorTOTP)
		id := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
		r.activeFactor(acct, authn.FactorTOTP, time.Hour)
		r.mounter.As(authntest.WithAccount(acct), authntest.WithAuthTime(base))
		if w := r.do(http.MethodDelete, listPath+"/"+id, nil); w.Code != http.StatusUnauthorized {
			t.Errorf("DELETE with another factor left = %d, want 401", w.Code)
		}
		if _, there := r.status(id); !there {
			t.Error("a factor guarded by another one was deleted on a first factor")
		}
	})
	t.Run("a password account, a fresh password session", func(t *testing.T) {
		r := routed(t, authn.FactorTOTP)
		id := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
		r.accounts.passwords[acct] = true
		r.mounter.As(authntest.WithAccount(acct), authntest.WithAuthTime(base), authntest.WithMethods(authn.MethodPassword))
		if w := r.do(http.MethodDelete, listPath+"/"+id, nil); w.Code != http.StatusNoContent {
			t.Fatalf("DELETE by a fresh password session = %d, want 204: %s", w.Code, w.Body.String())
		}
		if _, there := r.status(id); there {
			t.Error("the last factor was not removed by a fresh password session")
		}
	})
	t.Run("a password account, a fresh first factor without the password", func(t *testing.T) {
		r := routed(t, authn.FactorTOTP)
		id := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
		r.accounts.passwords[acct] = true
		r.mounter.As(authntest.WithAccount(acct), authntest.WithAuthTime(base), authntest.WithMethods(authn.MethodTOTP))
		if w := r.do(http.MethodDelete, listPath+"/"+id, nil); w.Code != http.StatusUnauthorized {
			t.Errorf("DELETE by a first factor that is not the password = %d, want 401", w.Code)
		}
	})
	t.Run("an impersonating operator, the target's last factor", func(t *testing.T) {
		r := routed(t, authn.FactorTOTP)
		id := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
		r.sessions.add(sessionUUID, "admin-1")
		r.mounter.As(authntest.WithAccount(acct), authntest.WithActor("admin-1"), authntest.WithSession(sessionUUID), authntest.WithScope("tenant-1"), authntest.WithAuthTime(base), authntest.WithMethods(authn.MethodTOTP))
		if w := r.do(http.MethodDelete, listPath+"/"+id, nil); w.Code != http.StatusUnauthorized {
			t.Errorf("DELETE of the target's last factor while impersonating = %d, want 401", w.Code)
		}
		if _, there := r.status(id); !there {
			t.Error("an impersonating operator removed the target's last factor on a first factor")
		}
	})
	t.Run("the last factor, an old first factor", func(t *testing.T) {
		r := routed(t, authn.FactorTOTP)
		id := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
		old := base.Add(-20 * time.Minute)
		r.mounter.As(authntest.WithAccount(acct), authntest.WithAuthTime(old))
		if w := r.do(http.MethodDelete, listPath+"/"+id, nil); w.Code != http.StatusUnauthorized {
			t.Errorf("DELETE of the last factor with an old first factor = %d, want 401", w.Code)
		}
	})
}

// TestDeletingAFactorThatIsNotTheCallersOrIsGoneReadsAsAlreadyDone checks that another account's factor and a missing one both answer the 204 of a landed removal.
func TestDeletingAFactorThatIsNotTheCallersOrIsGoneReadsAsAlreadyDone(t *testing.T) {
	r := routed(t, authn.FactorTOTP)
	theirs := r.activeFactor("someone-else", authn.FactorTOTP, time.Hour)
	r.mounter.As(multiAt(time.Minute)...)
	for _, id := range []string{theirs, "no-such-factor"} {
		if w := r.do(http.MethodDelete, listPath+"/"+id, nil); w.Code != http.StatusNoContent {
			t.Errorf("DELETE %s = %d (%s), want 204", id, w.Code, w.Body.String())
		}
	}
	if _, there := r.status(theirs); !there {
		t.Error("another account's factor was deleted")
	}
	long := strings.Repeat("x", maxFactorIDLen+1)
	if w := r.do(http.MethodDelete, listPath+"/"+long, nil); w.Code != http.StatusBadRequest {
		t.Errorf("an identifier longer than any there is = %d, want 400", w.Code)
	}
	boundary := strings.Repeat("x", maxFactorIDLen)
	if w := r.do(http.MethodDelete, listPath+"/"+boundary, nil); w.Code != http.StatusNoContent {
		t.Errorf("an identifier of exactly the longest admitted length = %d, want 204 as a factor that is not there", w.Code)
	}
}

// TestRemovingAFactorEndsTheAccountsOtherSessionsAndKeepsTheCurrentOne checks that removing an active factor ends the other sessions and keeps the requesting one.
func TestRemovingAFactorEndsTheAccountsOtherSessionsAndKeepsTheCurrentOne(t *testing.T) {
	for _, kind := range []authn.FactorKind{authn.FactorTOTP, authn.FactorSMS} {
		t.Run(string(kind), func(t *testing.T) {
			r := routed(t, kind)
			const otherSession = authn.SessionID("0192f5a0-7b1c-7d3a-9e11-3c2d4e5f6a7d")
			id := r.activeFactor(acct, kind, time.Hour)
			r.sessions.add(sessionUUID, acct)
			r.sessions.add(otherSession, acct)
			r.sessions.add("another-accounts-session", "someone-else")
			var rowThereWhenRevoking bool
			r.sessions.onRevoke = func() {
				_, rowThereWhenRevoking = r.status(id)
			}
			r.mounter.As(append(multiAt(time.Minute), authntest.WithSession(sessionUUID))...)

			if w := r.do(http.MethodDelete, listPath+"/"+id, nil); w.Code != http.StatusNoContent {
				t.Fatalf("DELETE = %d %s, want 204", w.Code, w.Body.String())
			}
			if rowThereWhenRevoking {
				t.Error("the sessions were revoked while the factor row was still there: the route ends them after a successful removal")
			}
			if !r.sessions.isLive(sessionUUID) {
				t.Error("the session that removed the factor was ended")
			}
			if r.sessions.isLive(otherSession) {
				t.Error("the account's other session is still live after an enabled factor was removed")
			}
			if !r.sessions.isLive("another-accounts-session") {
				t.Error("another account's session was ended")
			}
			want := []string{fmt.Sprintf("revoke-account:%s:except=%q:%s", acct, sessionUUID, authn.ReasonSelf)}
			if got := r.sessions.revocations(); !slices.Equal(got, want) {
				t.Errorf("the sessions were revoked as %v, want %v", got, want)
			}
		})
	}
}

// TestRemovingAnActiveFactorRotatesTheRequestingSessionsToken checks that removing an active factor rotates the requesting session and returns the new cookie.
func TestRemovingAnActiveFactorRotatesTheRequestingSessionsToken(t *testing.T) {
	for _, kind := range []authn.FactorKind{authn.FactorTOTP, authn.FactorSMS} {
		t.Run(string(kind), func(t *testing.T) {
			r := routed(t, kind)
			id := r.activeFactor(acct, kind, time.Hour)
			r.sessions.add(sessionUUID, acct)
			old := r.sessions.tokenOf(sessionUUID)
			r.mounter.As(append(multiAt(time.Minute), authntest.WithSession(sessionUUID))...)

			w := r.do(http.MethodDelete, listPath+"/"+id, nil)
			if w.Code != http.StatusNoContent {
				t.Fatalf("DELETE = %d %s, want 204", w.Code, w.Body.String())
			}
			if got := r.sessions.rotationCalls(); !slices.Equal(got, []authn.SessionID{sessionUUID}) {
				t.Fatalf("Rotate was called for %v, want the requesting session once", got)
			}
			fresh := r.sessions.tokenOf(sessionUUID)
			if fresh == old {
				t.Errorf("the requesting session still holds its old token %q: the removal did not rotate it", old)
			}
			if r.sessions.authenticates(old) {
				t.Error("the old token still authenticates after the removal")
			}
			if !r.sessions.authenticates(fresh) {
				t.Error("the new token does not authenticate")
			}
			if !r.sessions.isLive(sessionUUID) {
				t.Error("the requesting session was ended instead of rotated")
			}
			if v, ok := cookieValue(w, "sid"); !ok || v != fresh {
				t.Errorf("the response carries cookie %q (present %v), want the rotated token %q", v, ok, fresh)
			}
			if cc := strings.Join(w.Header().Values("Cache-Control"), ", "); !strings.Contains(cc, `no-cache="Set-Cookie"`) {
				t.Errorf("Cache-Control = %q, want the no-cache directive a response carrying Set-Cookie adds", cc)
			}
		})
	}
}

// TestDeletingAFactorThatIsNotActiveEndsNoSession checks that removing a pending or disabled factor ends and rotates no session.
func TestDeletingAFactorThatIsNotActiveEndsNoSession(t *testing.T) {
	r := routed(t, authn.FactorTOTP)
	r.mounter.As(append(multiAt(time.Minute), authntest.WithSession(sessionUUID))...)
	r.sessions.add(sessionUUID, acct)
	r.sessions.add("another-session", acct)
	before := r.sessions.tokenOf(sessionUUID)
	pending := r.insertRow(acct, authn.FactorTOTP, authn.FactorPending, r.clock.read(), nil)
	disabled := r.insertRow(acct, authn.FactorTOTP, authn.FactorDisabled, r.clock.read(), nil)
	for _, id := range []string{pending, disabled} {
		w := r.do(http.MethodDelete, listPath+"/"+id, nil)
		if w.Code != http.StatusNoContent {
			t.Fatalf("DELETE %s = %d %s, want 204", id, w.Code, w.Body.String())
		}
		if v, ok := cookieValue(w, "sid"); ok {
			t.Errorf("removing %s answered with a session cookie %q, want none", id, v)
		}
	}
	if got := r.sessions.revocations(); len(got) != 0 {
		t.Errorf("the sessions were revoked as %v, want none for a factor that is not active", got)
	}
	if got := r.sessions.rotationCalls(); len(got) != 0 {
		t.Errorf("the requesting session was rotated for %v, want none for a factor that is not active", got)
	}
	if got := r.sessions.tokenOf(sessionUUID); got != before {
		t.Errorf("the requesting session's token changed to %q, want %q", got, before)
	}
	if !r.sessions.isLive("another-session") {
		t.Error("a session was ended by deleting a factor that is not active")
	}
}

// TestADeletionRefusedForAnUnreadableAccountEndsNoSession checks that a deletion refused for an unreadable account touches no session.
func TestADeletionRefusedForAnUnreadableAccountEndsNoSession(t *testing.T) {
	r := routed(t, authn.FactorTOTP)
	id := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
	r.sessions.add(sessionUUID, acct)
	r.sessions.add("another-session", acct)
	r.accounts.getErr = errors.New("account store down")
	r.mounter.As(append(multiAt(time.Minute), authntest.WithSession(sessionUUID))...)
	w := r.do(http.MethodDelete, listPath+"/"+id, nil)
	if w.Code != http.StatusServiceUnavailable || problemOf(t, w).Type != problemType+"unavailable" {
		t.Fatalf("DELETE = %d %s, want 503 unavailable", w.Code, w.Body.String())
	}
	if _, there := r.status(id); !there {
		t.Error("the factor went although whether the deletion is allowed could not be read")
	}
	if got := r.sessions.revocations(); len(got) != 0 {
		t.Errorf("a refused deletion revoked the sessions: %v", got)
	}
	if got := r.sessions.rotationCalls(); len(got) != 0 {
		t.Errorf("a refused deletion rotated the requesting session: %v", got)
	}
	if !r.sessions.isLive(sessionUUID) || !r.sessions.isLive("another-session") {
		t.Error("a refused deletion ended a session")
	}
}

// TestADeletionThatFailsInTheFactorEndsNoSession checks that a removal failing in the factor leaves every session as it was.
func TestADeletionThatFailsInTheFactorEndsNoSession(t *testing.T) {
	r := routed(t, authn.FactorTOTP)
	id := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
	r.sessions.add(sessionUUID, acct)
	r.sessions.add("another-session", acct)
	r.p.factors[authn.FactorTOTP].(*fakeFactor).removeFn = func(context.Context, authn.AccountID, authn.FactorRecord) error {
		return errors.New("seed store down")
	}
	r.mounter.As(append(multiAt(time.Minute), authntest.WithSession(sessionUUID))...)
	w := r.do(http.MethodDelete, listPath+"/"+id, nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("DELETE = %d %s, want 503", w.Code, w.Body.String())
	}
	if _, there := r.status(id); !there {
		t.Error("the row went although the factor could not clean its data")
	}
	if got := r.sessions.revocations(); len(got) != 0 {
		t.Errorf("a failed deletion revoked the sessions: %v", got)
	}
	if got := r.sessions.rotationCalls(); len(got) != 0 {
		t.Errorf("a failed deletion rotated the requesting session: %v", got)
	}
	if !r.sessions.isLive("another-session") {
		t.Error("a failed deletion ended a session")
	}
}

// TestADeletionWhoseSessionRevocationFailsReportsItAndARetryFinishes checks that a failed revocation after the removal answers 503 and a retry finishes the work.
func TestADeletionWhoseSessionRevocationFailsReportsItAndARetryFinishes(t *testing.T) {
	r := routed(t, authn.FactorTOTP)
	id := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
	r.sessions.add(sessionUUID, acct)
	r.sessions.add("another-session", acct)
	r.sessions.err = errors.New("session store down")
	r.mounter.As(append(multiAt(time.Minute), authntest.WithSession(sessionUUID))...)
	before := r.sessions.tokenOf(sessionUUID)

	w := r.do(http.MethodDelete, listPath+"/"+id, nil)
	if w.Code != http.StatusServiceUnavailable || problemOf(t, w).Type != problemType+"unavailable" {
		t.Fatalf("DELETE = %d %s, want 503 unavailable", w.Code, w.Body.String())
	}
	if _, there := r.status(id); there {
		t.Error("the factor stayed although the removal had landed")
	}
	if !r.sessions.isLive("another-session") {
		t.Error("the other session was ended by a revocation that failed")
	}
	if got := r.sessions.rotationCalls(); len(got) != 0 {
		t.Errorf("a revocation that failed also rotated %v, want no rotation", got)
	}
	if v, ok := cookieValue(w, "sid"); ok {
		t.Errorf("the 503 answer carried a session cookie %q, want none", v)
	}
	if got := r.sessions.tokenOf(sessionUUID); got != before {
		t.Errorf("the requesting session's token changed to %q, want the old one %q still valid", got, before)
	}

	r.sessions.err = nil
	w = r.do(http.MethodDelete, listPath+"/"+id, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("the retry = %d %s, want 204", w.Code, w.Body.String())
	}
	if r.sessions.isLive("another-session") {
		t.Error("the retry did not end the other session")
	}
	if !r.sessions.isLive(sessionUUID) {
		t.Error("the retry ended the session that asked")
	}
	if got := r.sessions.rotationCalls(); !slices.Equal(got, []authn.SessionID{sessionUUID}) {
		t.Errorf("the retry rotated %v, want only the requesting session", got)
	}
	if v, ok := cookieValue(w, "sid"); !ok || v != r.sessions.tokenOf(sessionUUID) {
		t.Errorf("the retry carries cookie %q (present %v), want the rotated token %q", v, ok, r.sessions.tokenOf(sessionUUID))
	}
	want := []string{
		fmt.Sprintf("revoke-account:%s:except=%q:%s", acct, sessionUUID, authn.ReasonSelf),
		fmt.Sprintf("revoke-account:%s:except=%q:%s", acct, sessionUUID, authn.ReasonSelf),
	}
	if got := r.sessions.revocations(); !slices.Equal(got, want) {
		t.Errorf("the sessions were revoked as %v, want %v: the attempt that failed and the retry", got, want)
	}
}

// TestADeletionWhoseRotationFailsReportsItAndARetryRotates checks that a failed rotation after the removal answers 503 and a retry rotates the session.
func TestADeletionWhoseRotationFailsReportsItAndARetryRotates(t *testing.T) {
	r := routed(t, authn.FactorTOTP)
	id := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
	r.sessions.add(sessionUUID, acct)
	r.sessions.add("another-session", acct)
	old := r.sessions.tokenOf(sessionUUID)
	r.sessions.rotateErr = errors.New("session store down")
	r.mounter.As(append(multiAt(time.Minute), authntest.WithSession(sessionUUID))...)

	w := r.do(http.MethodDelete, listPath+"/"+id, nil)
	if w.Code != http.StatusServiceUnavailable || problemOf(t, w).Type != problemType+"unavailable" {
		t.Fatalf("DELETE = %d %s, want 503 unavailable", w.Code, w.Body.String())
	}
	if _, there := r.status(id); there {
		t.Error("the factor stayed although the removal had landed")
	}
	if got := r.sessions.tokenOf(sessionUUID); got != old {
		t.Errorf("a rotation that failed changed the token to %q, want the old one still valid", got)
	}
	if _, ok := cookieValue(w, "sid"); ok {
		t.Error("the answer carried a session cookie although the rotation failed")
	}
	if r.sessions.isLive("another-session") {
		t.Error("the other session was not ended before the rotation that failed")
	}

	r.sessions.rotateErr = nil
	w = r.do(http.MethodDelete, listPath+"/"+id, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("the retry = %d %s, want 204", w.Code, w.Body.String())
	}
	fresh := r.sessions.tokenOf(sessionUUID)
	if fresh == old {
		t.Error("the retry did not rotate the requesting session")
	}
	if v, ok := cookieValue(w, "sid"); !ok || v != fresh {
		t.Errorf("the retry carries cookie %q (present %v), want the rotated token %q", v, ok, fresh)
	}
	if !r.sessions.isLive(sessionUUID) {
		t.Error("the retry ended the session that asked")
	}
}

// TestDeletingAFactorWithoutASessionModuleRemovesItAndSaysSo checks that with no session store the route removes the factor and warns that sessions stay valid.
func TestDeletingAFactorWithoutASessionModuleRemovesItAndSaysSo(t *testing.T) {
	r := routed(t, authn.FactorTOTP)
	r.p.sessions = nil
	id := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
	r.sessions.add(sessionUUID, acct)
	r.sessions.add("another-session", acct)
	r.mounter.As(append(multiAt(time.Minute), authntest.WithSession(sessionUUID))...)

	if w := r.do(http.MethodDelete, listPath+"/"+id, nil); w.Code != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s, want 204", w.Code, w.Body.String())
	}
	if _, there := r.status(id); there {
		t.Error("the factor stayed although no session store refused the removal")
	}
	if got := r.sessions.revocations(); len(got) != 0 {
		t.Errorf("a module with no session store reached the session seam: %v", got)
	}
	if !r.sessions.isLive("another-session") {
		t.Error("a module with no session store ended a session")
	}
	if got := r.logs.lines("level=WARN", "sessions stay valid"); len(got) != 1 {
		t.Errorf("the log said the sessions stay valid %d times, want once: %q", len(got), r.logs.String())
	}
}

// TestDeletingTheLastFactorOfAnAccountThatRequiresOneIsAConflict checks that deleting the last factor of an account that requires one is a conflict.
func TestDeletingTheLastFactorOfAnAccountThatRequiresOneIsAConflict(t *testing.T) {
	r := routed(t, authn.FactorTOTP)
	id := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
	r.sessions.add("another-session", acct)
	r.accounts.required[acct] = true
	r.mounter.As(multiAt(time.Minute)...)
	w := r.do(http.MethodDelete, listPath+"/"+id, nil)
	if w.Code != http.StatusConflict || problemOf(t, w).Type != problemType+"conflict" {
		t.Errorf("DELETE = %d %s, want 409 conflict", w.Code, w.Body.String())
	}
	if _, there := r.status(id); !there {
		t.Error("the last factor was deleted")
	}
	if got := r.sessions.revocations(); len(got) != 0 {
		t.Errorf("a refused deletion revoked the sessions: %v", got)
	}
	if got := r.sessions.rotationCalls(); len(got) != 0 {
		t.Errorf("a refused deletion rotated the requesting session: %v", got)
	}
	if !r.sessions.isLive("another-session") {
		t.Error("a refused deletion ended a session")
	}
}

// TestDeletingIsAnOutageWhenTheFactorCannotCleanUp checks that deleting is an outage when the factor cannot clean up.
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

// TestTheCrossOriginCheckGuardsTheDeletion checks that the cross-origin check guards the deletion.
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

// TestAnAnonymousRequestIsTurnedAwayAndTheFactorStays checks that an anonymous request is turned away and the factor stays.
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

// TestAnImpersonatingIdentityRemovesTheEffectiveAccountsFactor checks that an impersonating identity lists and removes the effective account's factors with its own freshness.
func TestAnImpersonatingIdentityRemovesTheEffectiveAccountsFactor(t *testing.T) {
	r := routed(t, authn.FactorTOTP)
	id := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
	r.sessions.add(sessionUUID, "admin-1")
	impersonating := append(multiAt(time.Minute),
		authntest.WithActor("admin-1"), authntest.WithSession(sessionUUID), authntest.WithScope("tenant-1"))

	r.mounter.As(impersonating...)
	if w := r.do(http.MethodGet, listPath, nil); w.Code != http.StatusOK {
		t.Fatalf("GET %s while impersonating = %d: %s", listPath, w.Code, w.Body.String())
	}
	if w := r.do(http.MethodDelete, listPath+"/"+id, nil); w.Code != http.StatusNoContent {
		t.Fatalf("DELETE %s while impersonating = %d: %s", listPath+"/"+id, w.Code, w.Body.String())
	}
	if _, there := r.status(id); there {
		t.Error("the effective account's factor is still there")
	}

	id = r.activeFactor(acct, authn.FactorTOTP, time.Hour)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodDelete, listPath+"/"+id, nil)
	c.Request = c.Request.WithContext(authntest.Context(t, c.Request.Context(), impersonating...))
	c.Params = gin.Params{{Key: "id", Value: id}}
	removeFactorOperation.Handle(r.p.deleteFactor)(c)
	if w.Code != http.StatusNoContent {
		t.Errorf("the handler alone: DELETE = %d %s, want 204", w.Code, w.Body.String())
	}
	if _, there := r.status(id); there {
		t.Error("the factor was not removed by the handler alone")
	}
}

// TestTheHandlersOwn401CarriesTheCapabilityChallenge checks that a handler reached without the gateway answers 401 with the capability's challenge.
func TestTheHandlersOwn401CarriesTheCapabilityChallenge(t *testing.T) {
	r := routed(t, authn.FactorTOTP)
	cases := []struct {
		name    string
		method  string
		path    string
		params  gin.Params
		handler gin.HandlerFunc
	}{
		{
			"GET /factors", http.MethodGet, listPath, nil,
			factorsOperation.Handle(r.p.listFactors),
		},
		{
			"DELETE /factors/:id", http.MethodDelete, listPath + "/unknown",
			gin.Params{{Key: "id", Value: "unknown"}},
			removeFactorOperation.Handle(r.p.deleteFactor),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(tc.method, tc.path, nil)
			c.Request.Header.Set("Sec-Fetch-Site", "same-origin")
			c.Params = tc.params
			mount.Wrap(r.mounter.CSRF(), r.mounter.Endpoint(), tc.handler)(c)

			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", w.Code)
			}
			if got, want := w.Header().Get("WWW-Authenticate"),
				`Bearer realm="main", Cookie realm="main"`; got != want {
				t.Errorf("WWW-Authenticate = %q, want %q", got, want)
			}
			if p := problemOf(t, w); p.Type != "about:blank" || p.Status != http.StatusUnauthorized {
				t.Errorf("the problem is %+v, want the default 401 document", p)
			}
		})
	}
}

// TestTheListIsAnOutageWhenTheRegisterCannotBeRead checks that the list is an outage when the register cannot be read.
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

// TestDeletingUnderAnUnreadableRecentWindowUsesTheDeclaredDefault checks that an unreadable recent-window reads as its declared default of ten minutes.
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

// TestAnOutOfDomainRecentWindowUsesTheDeclaredDefault checks that a recent-window outside its domain reads as the declared default of ten minutes.
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

func (r *rig) recordOfID(id string) authn.FactorRecord {
	r.t.Helper()
	for _, rec := range r.rows(acct) {
		if rec.ID == id {
			return rec
		}
	}
	r.t.Fatalf("the factor %s is not in the account's rows", id)
	return authn.FactorRecord{}
}

// TestTheRenameWritesOnlyTheLabelAndAnswersTheRow checks that a rename trims and writes only the label and answers the row.
func TestTheRenameWritesOnlyTheLabelAndAnswersTheRow(t *testing.T) {
	r := routed(t, authn.FactorWebAuthn)
	id := r.activeFactor(acct, authn.FactorWebAuthn, time.Hour)
	if err := r.h.Exec("UPDATE authn_factors SET label = 'old', last_used_at = ? WHERE id = ?",
		base.Add(-time.Hour).UTC(), id).Error; err != nil {
		t.Fatal(err)
	}
	before := r.recordOfID(id)
	r.mounter.As(authntest.WithAccount(acct))

	w := r.doJSON(http.MethodPatch, listPath+"/"+id, map[string]string{"label": "  New phone  "})
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH = %d (%s), want 200", w.Code, w.Body.String())
	}
	var answer AuthnMfaFactor
	if err := json.Unmarshal(w.Body.Bytes(), &answer); err != nil {
		t.Fatalf("the answer %q is not a factor: %v", w.Body.String(), err)
	}
	if answer.ID != id || answer.Label != "New phone" || answer.Kind != "webauthn" || answer.Status != "active" {
		t.Errorf("the answer is %+v, want the updated row with the trimmed label", answer)
	}
	after := r.recordOfID(id)
	if after.Label != "New phone" {
		t.Errorf("the stored label is %q, want %q", after.Label, "New phone")
	}
	if after.Kind != before.Kind || after.Status != before.Status || !after.CreatedAt.Equal(before.CreatedAt) ||
		!after.ActivatedAt.Equal(before.ActivatedAt) || !after.LastUsedAt.Equal(before.LastUsedAt) {
		t.Errorf("the rename changed more than the label: before %+v, after %+v", before, after)
	}

	if w := r.doJSON(http.MethodPatch, listPath+"/"+id, map[string]string{"label": ""}); w.Code != http.StatusOK {
		t.Fatalf("clearing PATCH = %d (%s), want 200", w.Code, w.Body.String())
	}
	if got := r.recordOfID(id).Label; got != "" {
		t.Errorf("the label is %q after clearing, want empty", got)
	}

	r.mounter.AsAnonymous()
	if w := r.doJSON(http.MethodPatch, listPath+"/"+id, map[string]string{"label": "x"}); w.Code != http.StatusUnauthorized {
		t.Errorf("an anonymous rename = %d, want 401", w.Code)
	}
}

// TestRenamingAnotherAccountsFactorReadsAsNotFound checks that another account's factor reads as not found under the same problem code.
func TestRenamingAnotherAccountsFactorReadsAsNotFound(t *testing.T) {
	r := routed(t, authn.FactorTOTP)
	theirs := r.activeFactor("someone-else", authn.FactorTOTP, time.Hour)
	if err := r.h.Exec("UPDATE authn_factors SET label = 'theirs' WHERE id = ?", theirs).Error; err != nil {
		t.Fatal(err)
	}
	r.mounter.As(authntest.WithAccount(acct))
	for _, id := range []string{theirs, "no-such-factor"} {
		w := r.doJSON(http.MethodPatch, listPath+"/"+id, map[string]string{"label": "mine"})
		if w.Code != http.StatusNotFound {
			t.Errorf("PATCH %s = %d (%s), want 404", id, w.Code, w.Body.String())
		}
		if got := problemOf(t, w).Type; got != problemType+mount.CodeFactorNotFound {
			t.Errorf("PATCH %s answers type %q, want %q", id, got, problemType+mount.CodeFactorNotFound)
		}
	}
	var label string
	if err := r.h.Raw("SELECT label FROM authn_factors WHERE id = ?", theirs).Scan(&label).Error; err != nil {
		t.Fatal(err)
	}
	if label != "theirs" {
		t.Errorf("another account's factor label is %q, want it untouched", label)
	}

	long := strings.Repeat("x", maxFactorIDLen+1)
	if w := r.doJSON(http.MethodPatch, listPath+"/"+long, map[string]string{"label": "mine"}); w.Code != http.StatusBadRequest {
		t.Errorf("an identifier longer than any there is = %d, want 400", w.Code)
	}
}

// TestTheRenameRefusesANameOnTheAuthenticatorEntry checks that a label is refused as a bad request and the authenticator entry takes no name.
func TestTheRenameRefusesANameOnTheAuthenticatorEntry(t *testing.T) {
	r := routed(t, authn.FactorTOTP)
	id := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
	if err := r.h.Exec("UPDATE authn_factors SET label = 'old' WHERE id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	r.mounter.As(authntest.WithAccount(acct))

	w := r.doJSON(http.MethodPatch, listPath+"/"+id, map[string]string{"label": "New name"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("PATCH of the authenticator entry = %d (%s), want 400", w.Code, w.Body.String())
	}
	if got := r.recordOfID(id).Label; got != "old" {
		t.Errorf("the refused rename stored %q, want the row untouched", got)
	}
	if w := r.doJSON(http.MethodPatch, listPath+"/"+id, map[string]string{"label": ""}); w.Code != http.StatusOK {
		t.Fatalf("clearing the entry's name = %d (%s), want 200", w.Code, w.Body.String())
	}
}

// TestTheRenameRefusesALabelOutsideItsDomain checks that the rename refuses a label outside its domain.
func TestTheRenameRefusesALabelOutsideItsDomain(t *testing.T) {
	cases := []struct {
		name  string
		label string
	}{
		{"longer than the column admits", strings.Repeat("x", maxFactorLabelRunes+1)},
		{"a NUL character", "work\x00phone"},
		{"an embedded newline", "work\nphone"},
		{"a tab", "work\tphone"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := routed(t, authn.FactorTOTP)
			id := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
			if err := r.h.Exec("UPDATE authn_factors SET label = 'old' WHERE id = ?", id).Error; err != nil {
				t.Fatal(err)
			}
			r.mounter.As(authntest.WithAccount(acct))

			w := r.doJSON(http.MethodPatch, listPath+"/"+id, map[string]string{"label": c.label})
			if w.Code != http.StatusBadRequest {
				t.Fatalf("PATCH = %d (%s), want 400", w.Code, w.Body.String())
			}
			if got := r.recordOfID(id).Label; got != "old" {
				t.Errorf("the stored label is %q after a refused rename, want it unchanged", got)
			}
		})
	}

	r := routed(t, authn.FactorWebAuthn)
	id := r.activeFactor(acct, authn.FactorWebAuthn, time.Hour)
	r.mounter.As(authntest.WithAccount(acct))
	boundary := strings.Repeat("x", maxFactorLabelRunes)
	if w := r.doJSON(http.MethodPatch, listPath+"/"+id, map[string]string{"label": boundary}); w.Code != http.StatusOK {
		t.Errorf("a label of exactly the longest admitted length = %d, want 200", w.Code)
	}
	if got := r.recordOfID(id).Label; got != boundary {
		t.Errorf("the stored boundary label is %q, want the one submitted", got)
	}
}
