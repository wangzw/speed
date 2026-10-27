package session

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/model"
	"github.com/vislake/speed/pkg/event"
)

func TestTheBackendDeclaresWhatItTakes(t *testing.T) {
	f := newFixture(t, dialects[0])
	var v authn.Verifier = f.p
	if v.Name() != "session" {
		t.Errorf("Name = %q, want session", v.Name())
	}
	got := v.Accepts()
	if len(got.Schemes) != 0 || len(got.Cookies) != 1 || got.Cookies[0] != "__Host-sid" ||
		got.Accounts != "" || !got.Impersonation || !got.SSO {
		t.Errorf("Accepts = %+v, want the session cookie, the library namespace, impersonation and SSO, and no scheme", got)
	}
	if _, ok := v.(authn.CookieClearer); !ok {
		t.Error("the backend cannot clear its cookie")
	}
}

func TestAValueThatCannotBeATokenIsRefusedWithoutARead(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.accounts.add(alice, nil)
	f.sql.reset()
	for name, value := range map[string]string{"empty": "", "257 characters": strings.Repeat("a", maxTokenLen+1)} {
		v := f.verify(value)
		if got := outcomeOf(v); got.String() != "denied" {
			t.Errorf("%s: the verdict is %s, want denied", name, got)
		}
	}
	if n := f.sql.count("SELECT"); n != 0 {
		t.Errorf("a value that cannot be a token cost %d reads", n)
	}
	// 256 characters is within the bound: it is looked up, and is no session.
	if got := outcomeOf(f.verify(strings.Repeat("a", maxTokenLen))); got.String() != "denied" {
		t.Errorf("a 256-character unknown value is %s, want denied", got)
	}
	if n := f.sql.count("SELECT"); n != 1 {
		t.Errorf("a value at the bound cost %d reads, want 1", n)
	}
}

func TestADeniedCookieCanBeCleared(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.accounts.add(alice, nil)
	issued := f.create(newBrowser(alice))
	noErr(t, f.p.Revoke(context.Background(), issued.ID, authn.ReasonSelf))
	v := f.verify(issued.Token)
	if got := outcomeOf(v); got.String() != "denied" {
		t.Fatalf("verdict = %s", got)
	}
	var clearer authn.CookieClearer = f.p
	w := httptest.NewRecorder()
	clearer.ClearCookie(w)
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "__Host-sid" || cookies[0].Value != "" || cookies[0].MaxAge != -1 {
		t.Errorf("the clearing cookie is %+v", cookies)
	}
	if !strings.Contains(w.Header().Get("Set-Cookie"), "Max-Age=0") {
		t.Errorf("Set-Cookie = %q, want Max-Age=0", w.Header().Get("Set-Cookie"))
	}
}

func TestAnUnreachableDatabaseIsUnavailableAndNeverDenied(t *testing.T) {
	forEachDialect(t, func(t *testing.T, d dialectCase) {
		f := newFixture(t, d)
		f.accounts.add(alice, nil)
		cached := f.create(newBrowser(alice))
		uncached := f.create(newBrowser(alice))
		if got := outcomeOf(f.verify(cached.Token)); got.String() != "authenticated" {
			t.Fatalf("verdict = %s", got)
		}
		f.clock.Advance(2 * defaults.TouchInterval)
		f.settings.set(checkTTLPath, time.Hour/60) // a minute, to keep the entry for the case

		// The database goes away.
		other, err := f.database.Open(context.Background(), f.dsn)
		noErr(t, err)
		raw, err := other.DB()
		noErr(t, err)
		noErr(t, raw.Close())
		f.p.handle = other

		v := f.verify(uncached.Token)
		if got := outcomeOf(v); got.String() != "unavailable" {
			t.Errorf("a read that cannot be made is %s, want unavailable", got)
		}
		if !errors.Is(causeOf(v), authn.ErrUnavailable) {
			t.Errorf("the cause %v does not wrap ErrUnavailable", causeOf(v))
		}

		// An entry already cached is served until it runs out, and a touch that
		// cannot be written does not fail the request.
		f.settings.set(checkTTLPath, time.Minute)
		f.p.cacheSlot.Store(nil)
		f.p.handle = f.database.DB()
		noErr(t, func() error { _, e := f.p.Status(context.Background(), cached.ID); return e }())
		f.p.handle = other
		f.clock.Advance(2 * defaults.TouchInterval)
		if got := outcomeOf(f.verify(cached.Token)); got.String() == "denied" {
			t.Error("a failed read denied a cookie")
		}

		for name, call := range map[string]func() error{
			"Create":  func() error { _, e := f.p.Create(context.Background(), newBrowser(alice)); return e },
			"Revoke":  func() error { return f.p.Revoke(context.Background(), cached.ID, authn.ReasonSelf) },
			"Rotate":  func() error { _, e := f.p.Rotate(context.Background(), cached.ID); return e },
			"Fresh":   func() error { _, e := f.p.StatusFresh(context.Background(), cached.ID); return e },
			"Account": func() error { _, e := f.p.RevokeAccount(context.Background(), alice, "", authn.ReasonSelf); return e },
			"ForCred": func() error { _, e := f.p.ForCredential(context.Background(), alice, "c", nil); return e },
			"Current": func() error { _, _, e := f.p.Current(newRequest("__Host-sid", cached.Token).r); return e },
		} {
			if err := call(); !errors.Is(err, authn.ErrUnavailable) {
				t.Errorf("%s with the database gone = %v, want ErrUnavailable", name, err)
			}
		}
	})
}

func TestACachedAnswerIsServedWhileTheDatabaseIsGoneUntilItRunsOut(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.accounts.add(alice, nil)
	issued := f.create(newBrowser(alice))
	if got := outcomeOf(f.verify(issued.Token)); got.String() != "authenticated" {
		t.Fatalf("verdict = %s", got)
	}
	other, err := f.database.Open(context.Background(), f.dsn)
	noErr(t, err)
	raw, err := other.DB()
	noErr(t, err)
	noErr(t, raw.Close())
	f.p.handle = other

	if got := outcomeOf(f.verify(issued.Token)); got.String() != "authenticated" {
		t.Errorf("inside check-ttl the cached answer is %s, want authenticated", got)
	}
	f.clock.Advance(defaults.CheckTTL + time.Second)
	if got := outcomeOf(f.verify(issued.Token)); got.String() != "unavailable" {
		t.Errorf("past check-ttl the answer is %s, want unavailable", got)
	}
}

func TestAnImpersonationRecordWithNoCapabilityIsDenied(t *testing.T) {
	f := newFixture(t, dialects[0], withoutImpersonation())
	f.accounts.add(admin, nil)
	issued := f.create(newBrowser(admin))
	attached, err := f.p.SetImpersonation(context.Background(), issued.ID, "grant-1")
	noErr(t, err)
	v := f.verify(attached.Token)
	if got := outcomeOf(v); got.String() != "denied" {
		t.Errorf("a session naming a record with no capability to ask is %s, want denied", got)
	}
	// And without a record it is no refusal. Ending keeps the token, so the one
	// the attach returned is still the one the browser holds.
	cleared, err := f.p.SetImpersonation(context.Background(), issued.ID, "")
	noErr(t, err)
	if cleared.Token != "" {
		t.Errorf("ending the record returned a new token %q, want none", cleared.Token)
	}
	if got := outcomeOf(f.verify(attached.Token)); got.String() != "authenticated" {
		t.Errorf("the same session without a record is %s", got)
	}
}

// impersonate makes an administrator's session that names a record for bob in a
// tenant.
func impersonate(t *testing.T, f *fixture) (id authn.SessionID, token string) {
	t.Helper()
	f.accounts.add(admin, map[string]string{"dept": "ops"})
	f.accounts.add(bob, map[string]string{"dept": "sales"})
	in := newBrowser(admin)
	in.Methods = []authn.Method{authn.MethodPassword, authn.MethodTOTP}
	in.Assurance = authn.AssuranceMulti
	in.Provider = provider
	in.IDPAssured = true
	in.Attributes = map[string]string{"idp.group": "admins"}
	issued := f.create(in)
	f.imp.grant(authn.ImpersonationGrant{ID: "grant-1", Actor: admin, Target: bob, Tenant: "tenant-1", SessionID: issued.ID})
	attached, err := f.p.SetImpersonation(context.Background(), issued.ID, "grant-1")
	noErr(t, err)
	return issued.ID, attached.Token
}

func TestAnImpersonationIsTheTargetActedForByTheActor(t *testing.T) {
	forEachDialect(t, func(t *testing.T, d dialectCase) {
		f := newFixture(t, d)
		sid, token := impersonate(t, f)

		// Before the record is attached the administrator is themselves, and the
		// level their login reached is theirs.
		own := principalOf(f.verify(f.create(func() authn.NewSession {
			in := newBrowser(admin)
			in.Assurance, in.Methods = authn.AssuranceMulti, []authn.Method{authn.MethodPassword, authn.MethodTOTP}
			return in
		}()).Token))
		if own.Assurance != authn.AssuranceMulti || own.Actor != "" {
			t.Fatalf("the administrator's own principal is %+v", own)
		}

		v := f.verify(token)
		if got := outcomeOf(v); got.String() != "authenticated" {
			t.Fatalf("verdict = %s (%v)", got, causeOf(v))
		}
		p := principalOf(v)
		if p.Account != bob || p.Actor != admin || p.Scope != "tenant-1" || p.Session != sid {
			t.Errorf("the principal is %+v, want bob acted for by the administrator in tenant-1 on the administrator's session", p)
		}
		if p.Assurance != authn.AssuranceMulti || !p.IDPAssured {
			t.Errorf("assurance %v and IDPAssured %v, want the operator's own facts: an impersonation does not cap them", p.Assurance, p.IDPAssured)
		}
		if p.Provider != provider {
			t.Errorf("the provider is %q", p.Provider)
		}
		if p.Attributes["dept"] != "sales" || len(p.Attributes) != 1 {
			t.Errorf("the attributes are %v, want the target's alone", p.Attributes)
		}
		if len(p.Methods) != 2 || !p.AuthTime.Equal(epoch) {
			t.Errorf("methods %v and authentication time %s are the session's", p.Methods, p.AuthTime)
		}

		// The record ends: the administrator is back as themselves, not refused.
		f.imp.end("grant-1")
		f.bus.deliver(event.Event{ID: "ended-1", Type: authn.EventImpersonationEnded, Payload: authn.ImpersonationEnded{GrantID: "grant-1", EndedBy: "actor"}})
		v = f.verify(token)
		if got := outcomeOf(v); got.String() != "authenticated" {
			t.Fatalf("after the record ended the verdict is %s, want authenticated as the administrator", got)
		}
		p = principalOf(v)
		if p.Account != admin || p.Actor != "" || p.Scope != "" || p.Assurance != authn.AssuranceMulti || !p.IDPAssured {
			t.Errorf("the principal after the record ended is %+v, want the administrator's own", p)
		}
		if p.Attributes["dept"] != "ops" || p.Attributes["idp.group"] != "admins" {
			t.Errorf("the attributes after the record ended are %v", p.Attributes)
		}
	})
}

func TestTheAnswerForARecordIsCachedAndAFailureIsNot(t *testing.T) {
	f := newFixture(t, dialects[0])
	_, token := impersonate(t, f)
	f.settings.set(checkTTLPath, time.Minute)
	f.imp.calls.Store(0)

	f.imp.failWith(errors.New("impersonation store down"))
	v := f.verify(token)
	if got := outcomeOf(v); got.String() != "unavailable" {
		t.Fatalf("a record that cannot be asked about is %s, want unavailable", got)
	}
	f.imp.failWith(nil)
	for range 3 {
		if got := outcomeOf(f.verify(token)); got.String() != "authenticated" {
			t.Fatalf("verdict = %s", got)
		}
	}
	if got := f.imp.calls.Load(); got != 2 {
		t.Errorf("Active was asked %d times: one failure, which is not cached, and one answer that is, want 2", got)
	}

	// A record that ended is not served as valid past check-ttl.
	f.imp.end("grant-1")
	if got := principalOf(f.verify(token)); got.Actor == "" {
		t.Error("the record was asked again inside check-ttl")
	}
	f.clock.Advance(time.Minute + time.Second)
	if got := principalOf(f.verify(token)); got.Actor != "" {
		t.Error("an ended record was still served as valid after check-ttl")
	}
}

func TestAnImpersonationOfAnAccountThatIsNotEnabledIsDenied(t *testing.T) {
	f := newFixture(t, dialects[0])
	_, token := impersonate(t, f)
	f.disable(bob)
	if got := outcomeOf(f.verify(token)); got.String() != "denied" {
		t.Errorf("an impersonation of a disabled account is %s, want denied", got)
	}
	f.accounts.remove(bob)
	f.p.caches().forgetAccount(bob)
	if got := outcomeOf(f.verify(token)); got.String() != "denied" {
		t.Errorf("an impersonation of an account that is gone is %s, want denied", got)
	}
}

func TestTheAttributesAreTheAccountsAndTheSessionsIDPOnes(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.accounts.add(alice, map[string]string{"dept": "eng", "idp.group": "forged", "authn.flag": "forged"})
	in := newBrowser(alice)
	in.Provider = provider
	in.Attributes = map[string]string{"idp.group": "staff", "idp.title": "engineer"}
	issued := f.create(in)

	p := principalOf(f.verify(issued.Token))
	want := map[string]string{"dept": "eng", "idp.group": "staff", "idp.title": "engineer"}
	if len(p.Attributes) != len(want) {
		t.Fatalf("the attributes are %v, want %v", p.Attributes, want)
	}
	for k, v := range want {
		if p.Attributes[k] != v {
			t.Errorf("attribute %s = %q, want %q: an account's entry under a reserved prefix is dropped and the session's is the one that stays", k, p.Attributes[k], v)
		}
	}
}

func TestTheMergedAttributesStayInsideWhatTheGatewayStamps(t *testing.T) {
	f := newFixture(t, dialects[0])
	account := map[string]string{}
	for i := range authn.AccountAttributesMax {
		account["a"+strings.Repeat(string(rune('a'+i%26)), 10)+string(rune('a'+i/26))] = strings.Repeat("v", 60)
	}
	if err := model.CheckAccountAttributes(account); err != nil {
		t.Fatalf("the account's set is not the largest allowed: %v", err)
	}
	f.accounts.add(alice, account)
	idp := map[string]string{}
	for i := range authn.SessionIDPAttributesMax {
		idp["idp."+strings.Repeat(string(rune('a'+i)), authn.SessionIDPKeyMax-len(authn.ReservedPrefixIDP))] = strings.Repeat("v", authn.SessionIDPValueMax)
	}
	in := newBrowser(alice)
	in.Provider, in.Attributes = provider, idp
	issued := f.create(in)

	p := principalOf(f.verify(issued.Token))
	size := 0
	for k, v := range p.Attributes {
		size += len(k) + len(v)
	}
	if len(p.Attributes) != len(account)+len(idp) || len(p.Attributes) > model.IdentityAttributesMax || size > model.IdentityAttributeBytesMax {
		t.Errorf("the merged set has %d entries and %d bytes, against the gateway's %d and %d",
			len(p.Attributes), size, model.IdentityAttributesMax, model.IdentityAttributeBytesMax)
	}
}

func TestARowWhosePayloadIsNotItsOwnAuthenticatesNobody(t *testing.T) {
	forEachDialect(t, func(t *testing.T, d dialectCase) {
		f := newFixture(t, d)
		f.accounts.add(alice, nil)
		f.accounts.add(bob, nil)
		bobs := f.create(newBrowser(bob))
		bobData := f.row(bobs.ID).Data

		oversize := func(n int) string {
			pl := payload{
				SID: "x", Account: string(alice), Kind: string(authn.SessionBrowser),
				Methods: []authn.Method{authn.MethodPassword}, Assurance: authn.AssuranceSingle, AuthTime: epoch,
				Attributes: map[string]string{},
			}
			for i := range n {
				pl.Attributes["idp.k"+strings.Repeat("a", i+1)] = "v"
			}
			return mustPayload(t, pl)
		}
		for name, data := range map[string]string{
			"another row's payload":     bobData,
			"not JSON":                  "not json",
			"an empty document":         "{}",
			"no payload":                `{"deadline":"2026-10-11T08:00:00Z","values":{}}`,
			"attributes over the bound": oversize(authn.SessionIDPAttributesMax + 1),
		} {
			issued := f.create(newBrowser(alice))
			f.exec("UPDATE "+tableName+" SET data = ? WHERE id = ?", data, string(issued.ID))
			f.p.caches().forgetSession(issued.ID)
			v := f.verify(issued.Token)
			if got := outcomeOf(v); got.String() != "denied" {
				t.Errorf("%s: the verdict is %s, want denied", name, got)
			}
			if st := f.status(issued.ID); st.Active {
				t.Errorf("%s: Status is active", name)
			}
		}
	})
}

func mustPayload(t *testing.T, pl payload) string {
	t.Helper()
	b, err := encodePayload(pl)
	noErr(t, err)
	return string(b)
}

func TestTheAccountIsCheckedAgainAfterTheSessionCacheAnswered(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.accounts.add(alice, nil)
	issued := f.create(newBrowser(alice))
	f.settings.set(checkTTLPath, time.Minute)
	if got := outcomeOf(f.verify(issued.Token)); got.String() != "authenticated" {
		t.Fatalf("verdict = %s", got)
	}

	// The session cache holds an active entry; the cleanup hook was missed and
	// only the account's own cache learns the account is disabled. The clock
	// moves on so the session is older than the valid-after instant the
	// disabling stamps, which is what the re-enabling case below turns on.
	f.clock.Advance(30 * time.Second)
	f.disable(alice)
	f.p.caches().accounts.Invalidate(string(alice))
	v := f.verify(issued.Token)
	if got := outcomeOf(v); got.String() != "denied" {
		t.Errorf("a disabled account's session is %s, want denied", got)
	}

	// A session older than the account's valid-after instant is refused the same way.
	f.enable(alice)
	f.p.caches().accounts.Invalidate(string(alice))
	if got := outcomeOf(f.verify(issued.Token)); got.String() != "denied" {
		t.Errorf("a session older than valid_after is %s, want denied", got)
	}

	f.accounts.failWith(errors.New("account store down"))
	f.p.caches().accounts.Invalidate(string(alice))
	if got := outcomeOf(f.verify(issued.Token)); got.String() != "unavailable" {
		t.Errorf("with the account store down the verdict is %s, want unavailable", got)
	}
}

func TestASessionFromSSOCarriesItsProviderAndTheIdPsLevel(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.accounts.add(alice, nil)
	in := newBrowser(alice)
	in.Methods = []authn.Method{authn.MethodOIDC}
	in.Assurance, in.IDPAssured, in.Provider = authn.AssuranceMulti, true, provider
	issued := f.create(in)
	p := principalOf(f.verify(issued.Token))
	if p.Provider != provider || !p.IDPAssured || p.Assurance != authn.AssuranceMulti || len(p.Methods) != 1 || p.Methods[0] != authn.MethodOIDC {
		t.Errorf("the principal is %+v", p)
	}
}

func TestTheTouchIntervalIsNeverLongerThanHalfTheIdleTimeout(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.accounts.add(alice, nil)
	f.settings.set(idleTimeoutPath, time.Minute)
	f.settings.set(touchIntervalPath, 10*time.Minute)
	f.settings.set(checkTTLPath, time.Minute)
	issued := f.create(newBrowser(alice))
	f.sql.reset()

	// The user is active every 20 seconds. With a 10-minute interval the first
	// write would come after the idle period had ended under them.
	for i := 1; i <= 9; i++ {
		f.clock.Advance(20 * time.Second)
		if got := outcomeOf(f.verify(issued.Token)); got.String() != "authenticated" {
			t.Fatalf("after %d requests 20 seconds apart the verdict is %s", i, got)
		}
	}
	if f.sql.count("UPDATE") == 0 {
		t.Error("an active session was never extended")
	}
}

func TestTheIdlePeriodOfACachedEntryIsJudgedAgain(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.accounts.add(alice, nil)
	f.settings.set(idleTimeoutPath, time.Minute)
	f.settings.set(touchIntervalPath, time.Hour)
	f.settings.set(checkTTLPath, time.Minute)
	issued := f.create(newBrowser(alice))
	f.clock.Advance(10 * time.Second)
	if got := outcomeOf(f.verify(issued.Token)); got.String() != "authenticated" {
		t.Fatalf("verdict = %s", got)
	}
	// The entry is still inside check-ttl, and the idle period it was read with
	// has ended.
	f.clock.Advance(55 * time.Second)
	if got := outcomeOf(f.verify(issued.Token)); got.String() != "denied" {
		t.Errorf("an entry whose idle period ended is %s, want denied", got)
	}
}

func TestAVerdictNamesNoTokenAndNoAccountState(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.accounts.add(alice, nil)
	issued := f.create(newBrowser(alice))
	noErr(t, f.p.Revoke(context.Background(), issued.ID, authn.ReasonSelf))
	v := f.verify(issued.Token)
	if cause := causeOf(v); cause == nil || strings.Contains(cause.Error(), issued.Token) || strings.Contains(cause.Error(), storeKey(issued.Token)) {
		t.Errorf("the cause is %v", cause)
	}
}

// A session whose account row is gone is Denied, not Unavailable: the gateway
// clears the cookie instead of leaving the browser to send it again, so a
// session of a deleted account is not retried forever on every request. An
// account store that cannot be reached is the other outcome.
func TestAVerifyOfASessionWhoseAccountIsGoneIsDenied(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.accounts.add(alice, nil)
	issued := f.create(newBrowser(alice))
	if got := outcomeOf(f.verify(issued.Token)); got.String() != "authenticated" {
		t.Fatalf("verdict = %s", got)
	}

	// The account is deleted while the session row stands and its cache entry is
	// warm.
	f.accounts.remove(alice)
	f.p.caches().accounts.Invalidate(string(alice))
	v := f.verify(issued.Token)
	if got := outcomeOf(v); got.String() != "denied" {
		t.Errorf("a session whose account is gone is %s, want denied", got)
	}
	if errors.Is(causeOf(v), authn.ErrUnavailable) {
		t.Errorf("the cause %v wraps ErrUnavailable, so the gateway would retry the cookie instead of clearing it", causeOf(v))
	}

	// A store that cannot be reached is what Unavailable is for.
	f.accounts.failWith(errors.New("account store down"))
	f.p.caches().accounts.Invalidate(string(alice))
	if got := outcomeOf(f.verify(issued.Token)); got.String() != "unavailable" {
		t.Errorf("with the account store down the verdict is %s, want unavailable", got)
	}
}

// An account attribute under a reserved prefix the session does not also carry
// is dropped from the identity: the two prefixes belong to whoever writes them,
// and this module is the one that writes idp.* into an identity.
func TestTheAccountsReservedPrefixAttributeIsDroppedInThePrincipal(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.accounts.add(alice, map[string]string{"dept": "eng", "idp.role": "admin", "authn.flag": "x"})
	in := newBrowser(alice)
	in.Provider = provider
	in.Attributes = map[string]string{"idp.group": "staff"}
	issued := f.create(in)

	p := principalOf(f.verify(issued.Token))
	if _, ok := p.Attributes["idp.role"]; ok {
		t.Error("an account's idp. attribute reached the identity")
	}
	if _, ok := p.Attributes["authn.flag"]; ok {
		t.Error("an account's authn. attribute reached the identity")
	}
	if p.Attributes["dept"] != "eng" || p.Attributes["idp.group"] != "staff" {
		t.Errorf("the attributes are %v, want the account's dept and the session's idp.group", p.Attributes)
	}
}

// touch extends the idle period of a browser session alone. A token or service
// session has no idle period and the table refuses one, so the touch of such a
// session writes nothing at all: it runs no statement.
func TestTouchLeavesTheSessionKindsThatHaveNoIdlePeriod(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.accounts.add(alice, nil)
	ctx := context.Background()
	f.settings.set(touchIntervalPath, 5*time.Second)

	tok := f.create(newToken1(alice))
	svc, err := f.p.ForCredential(ctx, alice, "cred-touch", nil)
	noErr(t, err)

	f.clock.Advance(2 * defaults.TouchInterval)
	f.sql.reset()
	for _, id := range []authn.SessionID{tok.ID, svc} {
		e, err := f.p.entryOf(ctx, f.p.caches(), f.row(id), false)
		noErr(t, err)
		f.p.touch(ctx, e)
		if f.row(id).IdleExpiresAt.Valid {
			t.Errorf("touch wrote an idle period for a %s session", f.row(id).Kind)
		}
	}
	if n := f.sql.count("UPDATE"); n != 0 {
		t.Errorf("touch ran %d write statements for sessions with no idle period, want 0", n)
	}
}

// The store's own lookup finds a live browser row alone: one whose idle period
// ends exactly at the instant asked about is not found.
func TestABrowserRowWhoseIdlePeriodEndsExactlyNowIsNotFound(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.accounts.add(alice, nil)
	issued := f.create(newBrowser(alice))
	f.exec("UPDATE "+tableName+" SET idle_expires_at = ? WHERE id = ?", f.clock.Now().UTC(), string(issued.ID))

	if _, err := f.p.byStoreKey(context.Background(), storeKey(issued.Token), f.clock.Now()); !errors.Is(err, errNoRow) {
		t.Errorf("a browser row whose idle period ends exactly now was found: %v", err)
	}
}
