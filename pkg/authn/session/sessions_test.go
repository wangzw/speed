package session

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/event"
)

// The first external assumption: a token a client chose is no session. The
// library starts an empty session for a token the store does not know, and does
// not keep the client's string, so the session that follows is under a token the
// server drew.
func TestAPresetTokenIsNoSession(t *testing.T) {
	forEachDialect(t, func(t *testing.T, d dialectCase) {
		f := newFixture(t, d)
		f.accounts.add(alice, nil)
		const preset = "attacker-chosen-token-0123456789abcdefghijk"

		if got := outcomeOf(f.verify(preset)); got.String() != "denied" {
			t.Errorf("a preset token is %s, want denied", got)
		}

		// The library, handed the preset token, starts a new session; the commit
		// gives it a token of the server's.
		ctx, err := f.p.sm.Load(context.Background(), preset)
		noErr(t, err)
		pl := payload{
			SID: "sid-fixation", Account: string(alice), Kind: string(authn.SessionBrowser),
			Methods: []authn.Method{authn.MethodPassword}, Assurance: authn.AssuranceSingle, AuthTime: epoch,
			Idle: epoch.Add(time.Hour), Expires: epoch.Add(2 * time.Hour),
		}
		f.p.sm.Put(ctx, payloadKey, pl)
		token, _, err := f.p.sm.Commit(ctx)
		noErr(t, err)
		if token == preset {
			t.Fatal("the session was committed under the token the client chose")
		}
		row, err := f.p.byID(context.Background(), "sid-fixation")
		noErr(t, err)
		if row.TokenHash.String != storeKey(token) {
			t.Error("the row is not stored under the token the server drew")
		}
		if got := outcomeOf(f.verify(preset)); got.String() != "denied" {
			t.Errorf("the preset token is %s after the session was made, want denied", got)
		}
		if got := outcomeOf(f.verify(token)); got.String() != "authenticated" {
			t.Errorf("the drawn token is %s, want authenticated", got)
		}
	})
}

func TestCreateRefusesWhatASessionIsNotMadeFrom(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.accounts.add(alice, nil)
	bad := map[string]func(*authn.NewSession){
		"a service session":        func(in *authn.NewSession) { in.Kind = authn.SessionService },
		"no kind":                  func(in *authn.NewSession) { in.Kind = "" },
		"an unknown kind":          func(in *authn.NewSession) { in.Kind = "cookie" },
		"no account":               func(in *authn.NewSession) { in.Account = "" },
		"a malformed account":      func(in *authn.NewSession) { in.Account = "a b" },
		"a static account":         func(in *authn.NewSession) { in.Account = "static.root" },
		"no method":                func(in *authn.NewSession) { in.Methods = nil },
		"a method twice":           func(in *authn.NewSession) { in.Methods = []authn.Method{authn.MethodSMS, authn.MethodSMS} },
		"a method outside the set": func(in *authn.NewSession) { in.Methods = []authn.Method{"telepathy"} },
		"too many methods": func(in *authn.NewSession) {
			in.Methods = []authn.Method{
				authn.MethodPassword, authn.MethodSMS, authn.MethodTOTP, authn.MethodWebAuthn,
				authn.MethodOAuth, authn.MethodOIDC, authn.MethodSAML, authn.MethodStatic, authn.MethodClientSecret,
			}
		},
		"an unstated level":        func(in *authn.NewSession) { in.Assurance = authn.AssuranceNone },
		"a malformed provider":     func(in *authn.NewSession) { in.Provider = "not-a-uuid" },
		"an IdP level without one": func(in *authn.NewSession) { in.IDPAssured, in.Assurance = true, authn.AssuranceMulti },
		"an IdP level that is not multi-factor": func(in *authn.NewSession) {
			in.IDPAssured, in.Provider = true, provider
		},
		"no authentication time": func(in *authn.NewSession) { in.AuthTime = time.Time{} },
		"a negative period":      func(in *authn.NewSession) { in.AbsoluteTTL = -time.Second },
		"a period past the setting": func(in *authn.NewSession) {
			in.AbsoluteTTL = defaults.AbsoluteTimeout + time.Second
		},
	}
	for name, mutate := range bad {
		t.Run(name, func(t *testing.T) {
			in := newBrowser(alice)
			mutate(&in)
			_, err := f.p.Create(context.Background(), in)
			if !errors.Is(err, authn.ErrInvalidArgument) {
				t.Errorf("Create = %v, want ErrInvalidArgument", err)
			}
			if n := f.count(alice) + f.count("static.root"); n != 0 {
				t.Errorf("a refused session left %d rows", n)
			}
		})
	}
}

// the period of a session follows its kind, the caller's request and the SSO
// setting, and is written to the row.
func TestThePeriodOfASessionFollowsItsKind(t *testing.T) {
	forEachDialect(t, func(t *testing.T, d dialectCase) {
		f := newFixture(t, d)
		f.accounts.add(alice, nil)
		start := f.clock.Now()

		cases := []struct {
			name     string
			mutate   func(*authn.NewSession)
			settings map[string]time.Duration
			want     time.Duration
			idle     bool
		}{
			{"browser", func(*authn.NewSession) {}, nil, 12 * time.Hour, true},
			{"token", func(in *authn.NewSession) { in.Kind = authn.SessionToken }, nil, 720 * time.Hour, false},
			{"browser from SSO", func(in *authn.NewSession) { in.Provider = provider }, nil, 8 * time.Hour, true},
			{"token from SSO", func(in *authn.NewSession) { in.Kind, in.Provider = authn.SessionToken, provider }, nil, 8 * time.Hour, false},
			{
				"browser from SSO with a longer SSO period", func(in *authn.NewSession) { in.Provider = provider },
				map[string]time.Duration{ssoAbsolutePath: 30 * 24 * time.Hour},
				12 * time.Hour, true,
			},
			{"a shorter period asked for", func(in *authn.NewSession) { in.AbsoluteTTL = time.Hour }, nil, time.Hour, true},
			{"a shorter period asked for from SSO", func(in *authn.NewSession) { in.AbsoluteTTL, in.Provider = 4*time.Hour, provider }, nil, 4 * time.Hour, true},
			{"the SSO period under a longer one asked for", func(in *authn.NewSession) { in.AbsoluteTTL, in.Provider = 10*time.Hour, provider }, nil, 8 * time.Hour, true},
			{"a configured browser period", func(*authn.NewSession) {}, map[string]time.Duration{absoluteTimeoutPath: 2 * time.Hour}, 2 * time.Hour, true},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				for k, v := range c.settings {
					f.settings.set(k, v)
					defer f.settings.set(k, declaredDefault(k))
				}
				in := newBrowser(alice)
				c.mutate(&in)
				issued := f.create(in)
				row := f.row(issued.ID)
				if !row.ExpiresAt.Time.Equal(start.Add(c.want)) || !issued.ExpiresAt.Equal(start.Add(c.want)) {
					t.Errorf("expires_at = %s and Issued.ExpiresAt = %s, want %s from the start",
						row.ExpiresAt.Time, issued.ExpiresAt, c.want)
				}
				if c.idle != row.IdleExpiresAt.Valid {
					t.Errorf("idle_expires_at valid = %v, want %v", row.IdleExpiresAt.Valid, c.idle)
				}
				if c.idle && !row.IdleExpiresAt.Time.Equal(start.Add(defaults.IdleTimeout)) {
					t.Errorf("idle_expires_at = %s, want the idle timeout from the start", row.IdleExpiresAt.Time)
				}
			})
		}
	})
}

func TestATokenSessionOutlivesTheIdleTimeout(t *testing.T) {
	forEachDialect(t, func(t *testing.T, d dialectCase) {
		f := newFixture(t, d)
		f.accounts.add(alice, nil)
		issued := f.create(newToken1(alice))
		if issued.Token != "" {
			t.Error("a token session was issued a cookie value")
		}
		f.clock.Advance(24 * time.Hour)
		if !f.status(issued.ID).Active {
			t.Error("a token session ended with the idle timeout of a browser session")
		}
		f.clock.Advance(696*time.Hour - time.Second)
		if !f.status(issued.ID).Active {
			t.Error("a token session ended before its absolute period")
		}
		f.clock.Advance(2 * time.Second)
		if f.status(issued.ID).Active {
			t.Error("a token session outlived its absolute period")
		}
	})
}

func TestAServiceSessionLastsAsLongAsItsSecret(t *testing.T) {
	forEachDialect(t, func(t *testing.T, d dialectCase) {
		f := newFixture(t, d)
		f.accounts.add(alice, nil)
		ctx := context.Background()
		start := f.clock.Now()
		expires := start.Add(90 * 24 * time.Hour)

		id, err := f.p.ForCredential(ctx, alice, "cred-a", &expires)
		noErr(t, err)
		for _, advance := range []time.Duration{time.Hour, 24 * time.Hour, 60 * 24 * time.Hour} {
			f.clock.Advance(advance)
			if !f.status(id).Active {
				t.Fatalf("the session of a secret that has not expired is not active at %s", f.clock.Now().Sub(start))
			}
		}
		f.clock.Advance(30 * 24 * time.Hour)
		if f.status(id).Active {
			t.Error("the session outlived its secret")
		}

		// A new expiry for the secret renews the same row.
		renewed := f.clock.Now().Add(30 * 24 * time.Hour)
		again, err := f.p.ForCredential(ctx, alice, "cred-a", &renewed)
		noErr(t, err)
		if again != id {
			t.Errorf("ForCredential returned %s for the secret that has %s", again, id)
		}
		if !f.row(id).ExpiresAt.Time.Equal(renewed) || !f.status(id).Active {
			t.Error("the session is not renewed to the secret's new expiry")
		}

		// A secret that does not expire has a session that does not either.
		forever, err := f.p.ForCredential(ctx, alice, "cred-b", nil)
		noErr(t, err)
		if f.row(forever).ExpiresAt.Valid {
			t.Error("the session of a secret without an expiry has one")
		}
		f.clock.Advance(1000 * 24 * time.Hour)
		if !f.status(forever).Active {
			t.Error("the session of a secret without an expiry ended")
		}
	})
}

func TestABrowserSessionEndsAtEitherPeriod(t *testing.T) {
	forEachDialect(t, func(t *testing.T, d dialectCase) {
		f := newFixture(t, d)
		f.accounts.add(alice, nil)

		idle := f.create(newBrowser(alice))
		// A request inside the touch interval does not extend the idle period.
		f.clock.Advance(20 * time.Second)
		if got := outcomeOf(f.verify(idle.Token)); got.String() != "authenticated" {
			t.Fatalf("a request inside the idle timeout is %s", got)
		}
		f.clock.Advance(defaults.IdleTimeout)
		if got := outcomeOf(f.verify(idle.Token)); got.String() != "denied" {
			t.Errorf("past the idle timeout the verdict is %s, want denied", got)
		}

		// A session that is used keeps its idle period and still ends at the
		// absolute one.
		used := f.create(newBrowser(alice))
		start := f.clock.Now()
		for f.clock.Now().Before(start.Add(defaults.AbsoluteTimeout)) {
			f.clock.Advance(20 * time.Minute)
			got := outcomeOf(f.verify(used.Token))
			if f.clock.Now().Before(start.Add(defaults.AbsoluteTimeout)) && got.String() != "authenticated" {
				t.Fatalf("%s into the session the verdict is %s", f.clock.Now().Sub(start), got)
			}
			if !f.clock.Now().Before(start.Add(defaults.AbsoluteTimeout)) && got.String() != "denied" {
				t.Fatalf("at the absolute period the verdict is %s, want denied", got)
			}
		}
	})
}

func TestForCredentialIsOneRowUnderConcurrentFirstCalls(t *testing.T) {
	forEachDialect(t, func(t *testing.T, d dialectCase) {
		f := newFixture(t, d)
		f.accounts.add(alice, nil)
		const callers = 8
		ids := make([]authn.SessionID, callers)
		errs := make([]error, callers)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := range callers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				ids[i], errs[i] = f.p.ForCredential(context.Background(), alice, "cred-race", nil)
			}()
		}
		close(start)
		wg.Wait()
		for i := range callers {
			if errs[i] != nil {
				t.Fatalf("caller %d: %v", i, errs[i])
			}
			if ids[i] != ids[0] {
				t.Errorf("caller %d got %s and caller 0 got %s: one secret has two sessions", i, ids[i], ids[0])
			}
		}
		if n := f.count(alice); n != 1 {
			t.Errorf("the table holds %d rows for the secret, want 1", n)
		}
	})
}

func TestForCredentialRefusesARevokedRowAndDoesNotRevive(t *testing.T) {
	forEachDialect(t, func(t *testing.T, d dialectCase) {
		f := newFixture(t, d)
		f.accounts.add(alice, nil)
		ctx := context.Background()
		id, err := f.p.ForCredential(ctx, alice, "cred-r", nil)
		noErr(t, err)
		noErr(t, f.p.RevokeCredential(ctx, "cred-r", authn.ReasonAdmin))
		noErr(t, f.p.RevokeCredential(ctx, "cred-r", authn.ReasonAdmin))
		noErr(t, f.p.RevokeCredential(ctx, "never-made", authn.ReasonAdmin))

		if _, err := f.p.ForCredential(ctx, alice, "cred-r", nil); !errors.Is(err, authn.ErrSessionRevoked) {
			t.Fatalf("ForCredential on a revoked row = %v, want ErrSessionRevoked", err)
		}
		row := f.row(id)
		if !row.RevokedAt.Valid || row.RevokeReason.String != authn.ReasonAdmin {
			t.Errorf("the row is no longer revoked: %+v", row)
		}
		if f.status(id).Active {
			t.Error("a revoked service session is active")
		}
		if got := len(f.bus.published(authn.EventSessionRevoked)); got != 1 {
			t.Errorf("%d revocation events for one revocation, want 1", got)
		}
	})
}

func TestAServiceClientIsRestoredAfterTheAccountIsDisabledAndEnabled(t *testing.T) {
	forEachDialect(t, func(t *testing.T, d dialectCase) {
		f := newFixture(t, d)
		f.accounts.add(alice, nil)
		ctx := context.Background()
		id, err := f.p.ForCredential(ctx, alice, "cred-d", nil)
		noErr(t, err)
		created := f.row(id).CreatedAt

		f.clock.Advance(time.Hour)
		f.disable(alice)
		noErr(t, f.p.AccountDisabled(ctx, alice))
		if f.status(id).Active {
			t.Fatal("a service session of a disabled account is active")
		}
		f.clock.Advance(time.Hour)
		f.enable(alice)
		if f.status(id).Active {
			t.Fatal("the session is active again before the client exchanged its secret: it is older than valid_after")
		}
		if f.row(id).RevokedAt.Valid {
			t.Fatal("disabling the account revoked a service session, which only the secret ends")
		}

		again, err := f.p.ForCredential(ctx, alice, "cred-d", nil)
		noErr(t, err)
		if again != id {
			t.Errorf("ForCredential returned %s, want the row %s", again, id)
		}
		row := f.row(id)
		if !row.CreatedAt.Equal(f.clock.Now()) || row.CreatedAt.Equal(created) {
			t.Errorf("created_at = %s, want now (%s) and not %s", row.CreatedAt, f.clock.Now(), created)
		}
		if !f.status(id).Active {
			t.Error("the service session is not restored")
		}
	})
}

func TestForCredentialRefusesWhatItCannotAnswerFor(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.accounts.add(alice, nil)
	f.accounts.add(bob, nil)
	ctx := context.Background()
	if _, err := f.p.ForCredential(ctx, alice, "cred-x", nil); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		account authn.AccountID
		cred    string
		want    error
	}{
		"another account's secret": {bob, "cred-x", authn.ErrInvalidArgument},
		"an empty identifier":      {alice, "", authn.ErrInvalidArgument},
		"a static account":         {"static.root", "cred-y", authn.ErrInvalidArgument},
		"an account that is gone":  {"acct-gone", "cred-z", authn.ErrAccountNotFound},
	} {
		if _, err := f.p.ForCredential(ctx, c.account, c.cred, nil); !errors.Is(err, c.want) {
			t.Errorf("%s: ForCredential = %v, want %v", name, err, c.want)
		}
	}
	f.accounts.failWith(errors.New("account store down"))
	if _, err := f.p.ForCredential(ctx, alice, "cred-x", nil); !errors.Is(err, authn.ErrUnavailable) {
		t.Errorf("ForCredential with the account store down = %v, want ErrUnavailable", err)
	}
}

func TestRevokeAccountKeepsTheExceptionAndLeavesServiceSessions(t *testing.T) {
	forEachDialect(t, func(t *testing.T, d dialectCase) {
		f := newFixture(t, d)
		f.accounts.add(alice, nil)
		f.accounts.add(bob, nil)
		ctx := context.Background()
		keep := f.create(newBrowser(alice))
		other := f.create(newBrowser(alice))
		tok := f.create(newToken1(alice))
		svc, err := f.p.ForCredential(ctx, alice, "cred-ra", nil)
		noErr(t, err)
		bobs := f.create(newBrowser(bob))

		n, err := f.p.RevokeAccount(ctx, alice, keep.ID, authn.ReasonPasswordChange)
		noErr(t, err)
		if n != 2 {
			t.Errorf("RevokeAccount revoked %d, want 2 (one browser and one token session)", n)
		}
		for id, wantRevoked := range map[authn.SessionID]bool{keep.ID: false, other.ID: true, tok.ID: true, svc: false, bobs.ID: false} {
			if got := f.row(id).RevokedAt.Valid; got != wantRevoked {
				t.Errorf("session %s revoked = %v, want %v", f.row(id).Kind, got, wantRevoked)
			}
		}
		if got := f.row(other.ID).RevokeReason.String; got != authn.ReasonPasswordChange {
			t.Errorf("revoke_reason = %q, want %q", got, authn.ReasonPasswordChange)
		}
		events := f.bus.published(authn.EventSessionRevoked)
		if len(events) != 1 {
			t.Fatalf("%d events, want 1", len(events))
		}
		var payload authn.SessionRevoked
		noErr(t, event.DecodePayload(events[0].Payload, &payload))
		if payload != (authn.SessionRevoked{AccountID: string(alice), Scope: "all", ReasonClass: authn.ReasonPasswordChange}) {
			t.Errorf("payload = %+v", payload)
		}

		n, err = f.p.RevokeAccount(ctx, alice, "", authn.ReasonAdmin)
		noErr(t, err)
		if n != 1 {
			t.Errorf("revoking all the rest revoked %d, want 1", n)
		}
		n, err = f.p.RevokeAccount(ctx, alice, "", authn.ReasonAdmin)
		noErr(t, err)
		if n != 0 {
			t.Errorf("revoking again revoked %d, want 0", n)
		}
		if _, err := f.p.RevokeAccount(ctx, alice, "", authn.ReasonThrottled); !errors.Is(err, authn.ErrInvalidArgument) {
			t.Errorf("a reason that is only for a log line was accepted: %v", err)
		}
	})
}

func TestRevokeIsIdempotentAndSaysWhy(t *testing.T) {
	forEachDialect(t, func(t *testing.T, d dialectCase) {
		f := newFixture(t, d)
		f.accounts.add(alice, nil)
		ctx := context.Background()
		issued := f.create(newBrowser(alice))

		noErr(t, f.p.Revoke(ctx, issued.ID, authn.ReasonSelf))
		first := f.row(issued.ID)
		f.clock.Advance(time.Minute)
		noErr(t, f.p.Revoke(ctx, issued.ID, authn.ReasonAdmin))
		second := f.row(issued.ID)
		if !second.RevokedAt.Time.Equal(first.RevokedAt.Time) || second.RevokeReason.String != authn.ReasonSelf {
			t.Errorf("a second revocation changed the first: %+v then %+v", first, second)
		}
		noErr(t, f.p.Revoke(ctx, "never-made", authn.ReasonSelf))

		events := f.bus.published(authn.EventSessionRevoked)
		if len(events) != 1 {
			t.Fatalf("%d events for one revocation, want 1", len(events))
		}
		var payload authn.SessionRevoked
		noErr(t, event.DecodePayload(events[0].Payload, &payload))
		want := authn.SessionRevoked{SessionID: string(issued.ID), AccountID: string(alice), Scope: "one", ReasonClass: authn.ReasonSelf}
		if payload != want {
			t.Errorf("payload = %+v, want %+v", payload, want)
		}
		for _, reason := range []string{"", "because", authn.ReasonThrottled} {
			if err := f.p.Revoke(ctx, issued.ID, reason); !errors.Is(err, authn.ErrInvalidArgument) {
				t.Errorf("reason %q: Revoke = %v, want ErrInvalidArgument", reason, err)
			}
		}
		if got := outcomeOf(f.verify(issued.Token)); got.String() != "denied" {
			t.Errorf("a revoked session's token is %s", got)
		}
	})
}

func TestAFailedPublishDoesNotFailARevocation(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.accounts.add(alice, nil)
	issued := f.create(newBrowser(alice))
	f.bus.failWith(errors.New("bus closed"))
	noErr(t, f.p.Revoke(context.Background(), issued.ID, authn.ReasonSelf))
	if !f.row(issued.ID).RevokedAt.Valid {
		t.Error("the session is not revoked")
	}
	if !strings.Contains(f.logs.String(), "level=WARN") {
		t.Errorf("a failed publish was not written at Warn:\n%s", f.logs.String())
	}
}

func TestEvictionRevokesTheLeastRecentlyUsed(t *testing.T) {
	forEachDialect(t, func(t *testing.T, d dialectCase) {
		f := newFixture(t, d)
		f.accounts.add(alice, nil)
		f.accounts.add(bob, nil)
		f.settings.set(maxPerAccountPath, 2)
		ctx := context.Background()

		first := f.create(newBrowser(alice))
		f.clock.Advance(time.Second)
		second := f.create(newBrowser(alice))
		f.clock.Advance(time.Second)
		bobs := f.create(newBrowser(bob))
		// The first session is used after the second was made, so the second is
		// the one least recently used.
		f.exec("UPDATE "+tableName+" SET last_seen_at = ? WHERE id = ?", f.clock.Now().UTC(), string(first.ID))
		f.clock.Advance(time.Second)

		third := f.create(newBrowser(alice))
		for id, wantRevoked := range map[authn.SessionID]bool{first.ID: false, second.ID: true, third.ID: false, bobs.ID: false} {
			if got := f.row(id).RevokedAt.Valid; got != wantRevoked {
				t.Errorf("session %s revoked = %v, want %v", id, got, wantRevoked)
			}
		}
		if got := f.row(second.ID).RevokeReason.String; got != authn.ReasonEvicted {
			t.Errorf("revoke_reason = %q, want %q", got, authn.ReasonEvicted)
		}
		events := f.bus.published(authn.EventSessionRevoked)
		if len(events) != 1 {
			t.Fatalf("%d events, want 1", len(events))
		}
		var payload authn.SessionRevoked
		noErr(t, event.DecodePayload(events[0].Payload, &payload))
		if payload.ReasonClass != authn.ReasonEvicted || payload.SessionID != string(second.ID) {
			t.Errorf("payload = %+v", payload)
		}

		// A service session does not count against the limit, and neither do
		// the sessions of another account.
		_, err := f.p.ForCredential(ctx, alice, "cred-e", nil)
		noErr(t, err)
		f.create(newToken1(alice))
		if live, err := f.p.List(ctx, alice); err != nil || len(live) != 2 {
			t.Errorf("the account holds %d live sessions (%v), want the limit, 2", len(live), err)
		}
	})
}

func TestRotateGivesANewTokenAndKeepsTheSession(t *testing.T) {
	forEachDialect(t, func(t *testing.T, d dialectCase) {
		f := newFixture(t, d)
		f.accounts.add(alice, nil)
		ctx := context.Background()
		issued := f.create(newBrowser(alice))
		before := f.row(issued.ID)
		if outcomeOf(f.verify(issued.Token)).String() != "authenticated" {
			t.Fatal("the new session does not authenticate")
		}

		rotated, err := f.p.Rotate(ctx, issued.ID)
		noErr(t, err)
		if rotated.ID != issued.ID || rotated.Token == "" || rotated.Token == issued.Token || !rotated.ExpiresAt.Equal(issued.ExpiresAt) {
			t.Errorf("Rotate returned %+v for %+v", rotated, issued)
		}
		after := f.row(issued.ID)
		if !after.ExpiresAt.Time.Equal(before.ExpiresAt.Time) || after.Data != before.Data || !after.CreatedAt.Equal(before.CreatedAt) {
			t.Error("rotation changed more than the token")
		}
		if got := outcomeOf(f.verify(issued.Token)); got.String() != "denied" {
			t.Errorf("the old token is %s after rotation, want denied", got)
		}
		if got := outcomeOf(f.verify(rotated.Token)); got.String() != "authenticated" {
			t.Errorf("the new token is %s", got)
		}

		noErr(t, f.p.Revoke(ctx, issued.ID, authn.ReasonSelf))
		if _, err := f.p.Rotate(ctx, issued.ID); !errors.Is(err, authn.ErrSessionRevoked) {
			t.Errorf("Rotate of a revoked session = %v, want ErrSessionRevoked", err)
		}
		if _, err := f.p.Rotate(ctx, "never-made"); !errors.Is(err, authn.ErrSessionNotFound) {
			t.Errorf("Rotate of an unknown session = %v, want ErrSessionNotFound", err)
		}
	})
}

func TestRotateHasNothingToDoForTheOtherKinds(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.accounts.add(alice, nil)
	ctx := context.Background()
	tok := f.create(newToken1(alice))
	svc, err := f.p.ForCredential(ctx, alice, "cred-rot", nil)
	noErr(t, err)
	for _, id := range []authn.SessionID{tok.ID, svc} {
		before := f.row(id)
		issued, err := f.p.Rotate(ctx, id)
		noErr(t, err)
		if issued.ID != id || issued.Token != "" {
			t.Errorf("Rotate of a %s session = %+v", before.Kind, issued)
		}
		after := f.row(id)
		if after.TokenHash != before.TokenHash || after.Data != before.Data {
			t.Errorf("Rotate changed a %s session", before.Kind)
		}
	}
}

func TestElevateRecordsTheStepUpAndRotates(t *testing.T) {
	forEachDialect(t, func(t *testing.T, d dialectCase) {
		f := newFixture(t, d)
		f.accounts.add(alice, nil)
		ctx := context.Background()
		issued := f.create(newBrowser(alice))
		before := f.row(issued.ID)
		f.clock.Advance(time.Minute)

		stepUp := authn.Elevation{
			Methods: []authn.Method{authn.MethodPassword, authn.MethodTOTP}, Assurance: authn.AssuranceMulti,
			AuthTime: f.clock.Now(),
		}
		elevated, err := f.p.Elevate(ctx, issued.ID, stepUp)
		noErr(t, err)
		if elevated.ID != issued.ID || elevated.Token == "" || elevated.Token == issued.Token {
			t.Fatalf("Elevate returned %+v", elevated)
		}
		after := f.row(issued.ID)
		if !after.ExpiresAt.Time.Equal(before.ExpiresAt.Time) {
			t.Error("the absolute period moved")
		}
		p := principalOf(f.verify(elevated.Token))
		if p.Assurance != authn.AssuranceMulti || len(p.Methods) != 2 || !p.AuthTime.Equal(stepUp.AuthTime) || p.Session != issued.ID {
			t.Errorf("the principal after the step-up is %+v", p)
		}
		if got := outcomeOf(f.verify(issued.Token)); got.String() != "denied" {
			t.Errorf("the token from before the step-up is %s", got)
		}

		// An IdP's declaration needs the multi-factor level and the session's
		// provider; and a method set the gateway would refuse is refused.
		for name, e := range map[string]authn.Elevation{
			"an IdP level on a local session": {Methods: stepUp.Methods, Assurance: authn.AssuranceMulti, IDPAssured: true, AuthTime: f.clock.Now()},
			"no methods":                      {Assurance: authn.AssuranceMulti, AuthTime: f.clock.Now()},
			"an unstated level":               {Methods: stepUp.Methods, AuthTime: f.clock.Now()},
			"no time":                         {Methods: stepUp.Methods, Assurance: authn.AssuranceMulti},
		} {
			if _, err := f.p.Elevate(ctx, issued.ID, e); !errors.Is(err, authn.ErrInvalidArgument) {
				t.Errorf("%s: Elevate = %v, want ErrInvalidArgument", name, err)
			}
		}
		if _, err := f.p.Elevate(ctx, "never-made", stepUp); !errors.Is(err, authn.ErrSessionNotFound) {
			t.Errorf("Elevate of an unknown session = %v", err)
		}
	})
}

func TestElevateOnATokenSessionKeepsNoToken(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.accounts.add(alice, nil)
	ctx := context.Background()
	tok := f.create(newToken1(alice))
	elevated, err := f.p.Elevate(ctx, tok.ID, authn.Elevation{
		Methods: []authn.Method{authn.MethodPassword, authn.MethodTOTP}, Assurance: authn.AssuranceMulti, AuthTime: epoch,
	})
	noErr(t, err)
	if elevated.Token != "" || elevated.ID != tok.ID {
		t.Errorf("Elevate on a token session returned %+v", elevated)
	}
	if st := f.status(tok.ID); st.Assurance != authn.AssuranceMulti {
		t.Errorf("the level after the step-up is %v", st.Assurance)
	}
	svc, err := f.p.ForCredential(ctx, alice, "cred-el", nil)
	noErr(t, err)
	if _, err := f.p.Elevate(ctx, svc, authn.Elevation{
		Methods: []authn.Method{authn.MethodPassword}, Assurance: authn.AssuranceSingle, AuthTime: epoch,
	}); !errors.Is(err, authn.ErrInvalidArgument) {
		t.Errorf("Elevate on a service session = %v, want ErrInvalidArgument", err)
	}
}

func TestSetImpersonationAttachesAndEndsARecord(t *testing.T) {
	forEachDialect(t, func(t *testing.T, d dialectCase) {
		f := newFixture(t, d)
		f.accounts.add(alice, nil)
		ctx := context.Background()
		issued := f.create(newBrowser(alice))

		attached, err := f.p.SetImpersonation(ctx, issued.ID, "grant-1")
		noErr(t, err)
		if attached.Token == "" || attached.Token == issued.Token {
			t.Errorf("SetImpersonation returned %+v", attached)
		}
		if got := f.row(issued.ID).ImpersonationID.String; got != "grant-1" {
			t.Errorf("impersonation_id = %q", got)
		}
		if info, getErr := f.p.Get(ctx, issued.ID); getErr != nil || info.ImpersonationID != "grant-1" {
			t.Errorf("Get = %+v, %v", info, getErr)
		}

		ended, err := f.p.SetImpersonation(ctx, issued.ID, "")
		noErr(t, err)
		if ended.Token == "" || ended.Token == attached.Token {
			t.Errorf("ending an impersonation did not rotate: %+v", ended)
		}
		if f.row(issued.ID).ImpersonationID.Valid {
			t.Error("impersonation_id is not NULL after the record was ended")
		}
		for _, bad := range []string{strings.Repeat("g", maxGrantIDLen+1), "a\nb"} {
			if _, err := f.p.SetImpersonation(ctx, issued.ID, bad); !errors.Is(err, authn.ErrInvalidArgument) {
				t.Errorf("SetImpersonation(%q) = %v, want ErrInvalidArgument", bad, err)
			}
		}
		noErr(t, f.p.Revoke(ctx, issued.ID, authn.ReasonSelf))
		if _, err := f.p.SetImpersonation(ctx, issued.ID, "grant-2"); !errors.Is(err, authn.ErrSessionRevoked) {
			t.Errorf("SetImpersonation on a revoked session = %v, want ErrSessionRevoked", err)
		}
	})
}

func TestGetAndListSayWhichSessionsAreThere(t *testing.T) {
	forEachDialect(t, func(t *testing.T, d dialectCase) {
		f := newFixture(t, d)
		f.accounts.add(alice, nil)
		f.accounts.add(bob, nil)
		ctx := context.Background()

		live := f.create(newBrowser(alice))
		f.clock.Advance(time.Second)
		tok := f.create(newToken1(alice))
		revoked := f.create(newBrowser(alice))
		noErr(t, f.p.Revoke(ctx, revoked.ID, authn.ReasonSelf))
		_, err := f.p.ForCredential(ctx, alice, "cred-l", nil)
		noErr(t, err)
		f.create(newBrowser(bob))
		f.exec("UPDATE "+tableName+" SET expires_at = ? WHERE id = ?", f.clock.Now().Add(-time.Second).UTC(), string(live.ID))
		other := f.create(newBrowser(alice))

		got, err := f.p.List(ctx, alice)
		noErr(t, err)
		var ids []authn.SessionID
		for _, info := range got {
			ids = append(ids, info.ID)
			if info.Account != alice || info.Revoked || info.Kind == authn.SessionService {
				t.Errorf("a listing holds %+v", info)
			}
		}
		if len(ids) != 2 || ids[0] != other.ID || ids[1] != tok.ID {
			t.Errorf("List = %v, want the live browser and token sessions, the newest first: %v", ids, []authn.SessionID{other.ID, tok.ID})
		}

		info, err := f.p.Get(ctx, revoked.ID)
		noErr(t, err)
		if !info.Revoked || info.Kind != authn.SessionBrowser || info.Assurance != authn.AssuranceSingle || info.ExpiresAt.IsZero() {
			t.Errorf("Get of a revoked session = %+v", info)
		}
		if info, err := f.p.Get(ctx, live.ID); err != nil || info.Revoked {
			t.Errorf("Get of an expired session = %+v, %v", info, err)
		}
		if _, err := f.p.Get(ctx, "never-made"); !errors.Is(err, authn.ErrSessionNotFound) {
			t.Errorf("Get of an unknown session = %v, want ErrSessionNotFound", err)
		}
	})
}

func TestCurrentFindsTheSessionOfTheCookie(t *testing.T) {
	forEachDialect(t, func(t *testing.T, d dialectCase) {
		f := newFixture(t, d)
		f.accounts.add(alice, nil)
		issued := f.create(newBrowser(alice))
		revoked := f.create(newBrowser(alice))
		noErr(t, f.p.Revoke(context.Background(), revoked.ID, authn.ReasonSelf))

		request := func(token string) *testRequest { return newRequest(f.p.cookie.name, token) }
		id, ok, err := f.p.Current(request(issued.Token).r)
		noErr(t, err)
		if !ok || id != issued.ID {
			t.Errorf("Current = %s, %v, want %s", id, ok, issued.ID)
		}
		for name, token := range map[string]string{
			"no cookie": "", "an unknown token": "unknown-token", "a revoked session": revoked.Token,
			"a value that is too long": strings.Repeat("x", maxTokenLen+1),
		} {
			var r *testRequest
			if name == "no cookie" {
				r = newRequest("", "")
			} else {
				r = request(token)
			}
			if id, ok, err := f.p.Current(r.r); ok || err != nil || id != "" {
				t.Errorf("%s: Current = %s, %v, %v, want nothing", name, id, ok, err)
			}
		}
		if _, ok, _ := f.p.Current(newRequest("another-cookie", issued.Token).r); ok {
			t.Error("a cookie of another name was read as the session cookie")
		}
	})
}

func TestStatusIsCachedAndStatusFreshIsNot(t *testing.T) {
	forEachDialect(t, func(t *testing.T, d dialectCase) {
		f := newFixture(t, d)
		f.accounts.add(alice, nil)
		ctx := context.Background()
		issued := f.create(newBrowser(alice))
		f.sql.reset()

		for range 3 {
			if !f.status(issued.ID).Active {
				t.Fatal("the session is not active")
			}
		}
		if got := f.sql.count("SELECT"); got != 1 {
			t.Errorf("three Status calls read the table %d times, want 1", got)
		}
		f.sql.reset()
		for range 3 {
			st, err := f.p.StatusFresh(ctx, issued.ID)
			noErr(t, err)
			if !st.Active {
				t.Fatal("the session is not active")
			}
		}
		if got := f.sql.count("SELECT"); got != 3 {
			t.Errorf("three StatusFresh calls read the table %d times, want 3", got)
		}

		// The cache is a way of reading less, bounded by check-ttl.
		f.disable(alice)
		if !f.status(issued.ID).Active {
			t.Error("Status did not serve the cached answer inside check-ttl")
		}
		if st, err := f.p.StatusFresh(ctx, issued.ID); err != nil || st.Active {
			t.Errorf("StatusFresh = %+v, %v, want an inactive status", st, err)
		}
		f.clock.Advance(defaults.CheckTTL + time.Second)
		if f.status(issued.ID).Active {
			t.Error("Status still served the answer after check-ttl")
		}
		if _, err := f.p.Status(ctx, "never-made"); !errors.Is(err, authn.ErrSessionNotFound) {
			t.Errorf("Status of an unknown session = %v, want ErrSessionNotFound", err)
		}
		if _, err := f.p.StatusFresh(ctx, "never-made"); !errors.Is(err, authn.ErrSessionNotFound) {
			t.Errorf("StatusFresh of an unknown session = %v, want ErrSessionNotFound", err)
		}
	})
}

func TestAnInactiveStatusCarriesNothingToBuildAnIdentityFrom(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.accounts.add(alice, map[string]string{"dept": "eng"})
	in := newBrowser(alice)
	in.Provider, in.Attributes = provider, map[string]string{"idp.group": "staff"}
	issued := f.create(in)
	f.disable(alice)
	st, err := f.p.StatusFresh(context.Background(), issued.ID)
	noErr(t, err)
	if st.Active || len(st.Methods) != 0 || st.Assurance != authn.AssuranceNone || st.Provider != "" || len(st.Attributes) != 0 || !st.AuthTime.IsZero() {
		t.Errorf("an inactive status carries %+v", st)
	}
	if st.Account != alice || st.Kind != authn.SessionBrowser {
		t.Errorf("an inactive status does not say whose it is: %+v", st)
	}
}

func TestAnAccountThatIsGoneMakesTheSessionInactive(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.accounts.add(alice, nil)
	issued := f.create(newBrowser(alice))
	f.accounts.remove(alice)
	if st, err := f.p.StatusFresh(context.Background(), issued.ID); err != nil || st.Active {
		t.Errorf("StatusFresh = %+v, %v, want an inactive status", st, err)
	}
	f.accounts.failWith(errors.New("account store down"))
	if _, err := f.p.StatusFresh(context.Background(), issued.ID); !errors.Is(err, authn.ErrUnavailable) {
		t.Errorf("StatusFresh with the account store down = %v, want ErrUnavailable", err)
	}
}

func TestAnAttributeSetBeyondTheBoundsIsRefused(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.accounts.add(alice, nil)
	many := map[string]string{}
	for i := range authn.SessionIDPAttributesMax + 1 {
		many["idp.k"+string(rune('a'+i))] = "v"
	}
	for name, attrs := range map[string]map[string]string{
		"more entries than the bound":   many,
		"a key past the bound":          {"idp." + strings.Repeat("k", authn.SessionIDPKeyMax): "v"},
		"a value past the bound":        {"idp.k": strings.Repeat("v", authn.SessionIDPValueMax+1)},
		"a key outside the prefix":      {"dept": "eng"},
		"a key under the other prefix":  {"authn.flag": "yes"},
		"a control character":           {"idp.k": "a\x00b"},
		"a value that is not UTF-8":     {"idp.k": "\xff\xfe"},
		"a key that is only the prefix": {"idp.": "v"},
	} {
		in := newBrowser(alice)
		in.Attributes = attrs
		if _, err := f.p.Create(context.Background(), in); !errors.Is(err, authn.ErrInvalidArgument) {
			t.Errorf("%s: Create = %v, want ErrInvalidArgument", name, err)
		}
	}
	if f.count(alice) != 0 {
		t.Error("a refused session left a row")
	}

	// The largest set the bounds allow is written, and read back whole.
	full := map[string]string{}
	for i := range authn.SessionIDPAttributesMax {
		full["idp."+strings.Repeat(string(rune('a'+i)), authn.SessionIDPKeyMax-len(authn.ReservedPrefixIDP))] = strings.Repeat("v", authn.SessionIDPValueMax)
	}
	in := newBrowser(alice)
	in.Attributes = full
	issued := f.create(in)
	if st := f.status(issued.ID); len(st.Attributes) != authn.SessionIDPAttributesMax {
		t.Errorf("the status carries %d attributes, want %d", len(st.Attributes), authn.SessionIDPAttributesMax)
	}
}

// the table refuses a payload over 8 KiB whatever wrote it, and the two checks
// in code refuse it first.
func TestAPayloadOverTheBoundIsRefused(t *testing.T) {
	forEachDialect(t, func(t *testing.T, d dialectCase) {
		f := newFixture(t, d)
		f.accounts.add(alice, nil)
		now := f.clock.Now().UTC()
		insert := func(data string) error {
			return f.p.handle.Exec("INSERT INTO "+tableName+" (id, account_id, kind, data, created_at, last_seen_at, "+
				"idle_expires_at, expires_at) VALUES (?, ?, 'browser', ?, ?, ?, ?, ?)",
				"sid-"+data[:1], string(alice), data, now, now, now.Add(time.Hour), now.Add(time.Hour)).Error
		}
		if err := insert("a" + strings.Repeat("x", maxDataBytes-1)); err != nil {
			t.Errorf("a payload of exactly %d bytes was refused: %v", maxDataBytes, err)
		}
		if err := insert("b" + strings.Repeat("x", maxDataBytes)); err == nil {
			t.Errorf("the table took a payload of %d bytes", maxDataBytes+1)
		}
		if err := storeAdapterCommit(f, strings.Repeat("x", maxDataBytes+1)); !errors.Is(err, authn.ErrInvalidArgument) {
			t.Errorf("the adapter's commit = %v, want ErrInvalidArgument", err)
		}
		// Ten times the bound of the shortest key an attribute may have cannot
		// reach 8 KiB through the checks, so the largest payload a caller can
		// make is far under it.
		in := newBrowser(alice)
		in.Methods = []authn.Method{
			authn.MethodPassword, authn.MethodSMS, authn.MethodTOTP, authn.MethodWebAuthn,
			authn.MethodOAuth, authn.MethodOIDC, authn.MethodSAML, authn.MethodStatic,
		}
		in.Provider = provider
		in.Attributes = map[string]string{}
		for i := range authn.SessionIDPAttributesMax {
			in.Attributes["idp."+strings.Repeat(string(rune('a'+i)), 28)] = strings.Repeat("v", authn.SessionIDPValueMax)
		}
		issued := f.create(in)
		if n := len(f.row(issued.ID).Data); n >= maxDataBytes {
			t.Errorf("the largest payload the bounds allow is %d bytes, which is not under %d", n, maxDataBytes)
		}
	})
}

func storeAdapterCommit(f *fixture, blob string) error {
	return storeAdapter{p: f.p}.CommitCtx(context.Background(), "key", []byte(blob), f.clock.Now())
}

// Eviction never revokes the session it was called for, even when that session
// is the least recently used one: the call just made it, so it is the one to
// keep.
func TestEvictionNeverRevokesTheSessionJustMade(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.accounts.add(alice, nil)
	first := f.create(newBrowser(alice))
	f.clock.Advance(time.Second)
	second := f.create(newBrowser(alice))
	f.clock.Advance(time.Second)
	third := f.create(newBrowser(alice))

	// The three older sessions are made to look the most recently used, so the
	// session about to be made is the least recently used when the limit forces a
	// revocation. It is still kept.
	ahead := f.clock.Now().Add(time.Hour).UTC()
	for _, id := range []authn.SessionID{first.ID, second.ID, third.ID} {
		f.exec("UPDATE "+tableName+" SET last_seen_at = ? WHERE id = ?", ahead, string(id))
	}
	f.settings.set(maxPerAccountPath, 1)

	fresh := f.create(newBrowser(alice))
	if f.row(fresh.ID).RevokedAt.Valid {
		t.Error("eviction revoked the session just made")
	}
	for _, id := range []authn.SessionID{first.ID, second.ID, third.ID} {
		if !f.row(id).RevokedAt.Valid {
			t.Errorf("session %s was not evicted to make room", id)
		}
	}
}

// A period exactly the kind's limit is the longest allowed and not past it: it
// is accepted and the session gets that period.
func TestAPeriodExactlyTheKindsLimitIsAccepted(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.accounts.add(alice, nil)
	start := f.clock.Now()
	in := newBrowser(alice)
	in.AbsoluteTTL = defaults.AbsoluteTimeout
	issued := f.create(in)
	want := start.Add(defaults.AbsoluteTimeout)
	if !issued.ExpiresAt.Equal(want) || !f.row(issued.ID).ExpiresAt.Time.Equal(want) {
		t.Errorf("a period equal to the kind's limit gave expires_at %s, want %s", issued.ExpiresAt, want)
	}
}

// The store's own listing selects a live row alone: one whose absolute period
// ends exactly at the instant asked about is not listed.
func TestALiveRowWhoseAbsolutePeriodEndsExactlyNowIsNotListed(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.accounts.add(alice, nil)
	issued := f.create(newBrowser(alice))
	f.exec("UPDATE "+tableName+" SET expires_at = ? WHERE id = ?", f.clock.Now().UTC(), string(issued.ID))

	live, err := f.p.List(context.Background(), alice)
	noErr(t, err)
	if len(live) != 0 {
		t.Errorf("a row whose absolute period ends exactly now is listed: %+v", live)
	}
}
