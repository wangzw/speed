package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
)

// S1: the library's manager is built to the adaptation rules. The values are
// written as literals: a test that compared them with the constants the code
// reads would pass for whatever the code said.
func TestTheManagerIsBuiltToTheAdaptationRules(t *testing.T) {
	f := newFixture(t, dialects[0])
	sm := f.p.sm
	if sm.Lifetime != 8760*time.Hour {
		t.Errorf("Lifetime = %s, want 8760h, the largest upper bound of the absolute-timeout domains", sm.Lifetime)
	}
	if sm.IdleTimeout != 0 {
		t.Errorf("IdleTimeout = %s, want 0: the idle period is enforced by this module", sm.IdleTimeout)
	}
	if !sm.HashTokenInStore {
		t.Error("HashTokenInStore is false, so a token would be stored in clear")
	}
	if _, ok := sm.Codec.(jsonCodec); !ok {
		t.Errorf("Codec is %T, want the JSON codec", sm.Codec)
	}
	if _, ok := sm.Store.(storeAdapter); !ok {
		t.Errorf("Store is %T, want the adapter over the table", sm.Store)
	}
	if sm.Cookie.Persist {
		t.Error("the cookie persists, so it would outlive the browser session")
	}
	if !sm.Cookie.HttpOnly || sm.Cookie.Path != "/" || !sm.Cookie.Secure || sm.Cookie.Domain != "" {
		t.Errorf("cookie attributes are %+v", sm.Cookie)
	}
	// The lifetime is the upper bound of every domain, whatever the settings say.
	for name, d := range map[string]time.Duration{
		"absolute-timeout": absoluteTimeoutMax, "token-absolute-timeout": tokenAbsoluteMax, "sso-absolute-timeout": ssoAbsoluteMax,
	} {
		if d > sm.Lifetime {
			t.Errorf("the domain of %s reaches %s, past the library's lifetime %s", name, d, sm.Lifetime)
		}
	}
}

// S1: the payload is stored as JSON and no token is stored in clear.
func TestThePayloadIsJSONAndNoTokenIsInTheTable(t *testing.T) {
	forEachDialect(t, func(t *testing.T, d dialectCase) {
		f := newFixture(t, d)
		f.accounts.add(alice, nil)
		in := newBrowser(alice)
		in.Provider = provider
		in.Attributes = map[string]string{"idp.group": "staff"}
		issued := f.create(in)

		row := f.row(issued.ID)
		var doc struct {
			Deadline string                     `json:"deadline"`
			Values   map[string]json.RawMessage `json:"values"`
		}
		if err := json.Unmarshal([]byte(row.Data), &doc); err != nil {
			t.Fatalf("the data column is not JSON: %v\n%s", err, row.Data)
		}
		var pl payload
		if err := json.Unmarshal(doc.Values[payloadKey], &pl); err != nil {
			t.Fatalf("the payload is not in the values: %v", err)
		}
		if pl.SID != string(issued.ID) || pl.Account != string(alice) || pl.Provider != provider || pl.Attributes["idp.group"] != "staff" {
			t.Errorf("the payload does not carry what the session was made from: %+v", pl)
		}

		var dump []map[string]any
		if err := f.p.handle.Raw("SELECT * FROM " + tableName).Scan(&dump).Error; err != nil {
			t.Fatalf("reading the table: %v", err)
		}
		if len(dump) != 1 {
			t.Fatalf("the table holds %d rows, want 1", len(dump))
		}
		for column, value := range dump[0] {
			if strings.Contains(fmt.Sprint(value), issued.Token) {
				t.Errorf("the column %s holds the token in clear", column)
			}
		}
		if row.TokenHash.String != storeKey(issued.Token) {
			t.Errorf("token_hash = %q, want the digest of the token", row.TokenHash.String)
		}
	})
}

// S2: the write that extends the idle period cannot bring a revoked session
// back.
func TestARevokedSessionIsNotWrittenBackByATouch(t *testing.T) {
	forEachDialect(t, func(t *testing.T, d dialectCase) {
		f := newFixture(t, d)
		f.accounts.add(alice, nil)
		issued := f.create(newBrowser(alice))
		ctx := context.Background()
		later := f.clock.Now().Add(time.Minute)

		n, err := f.p.touchRow(ctx, issued.ID, later, later.Add(time.Hour))
		noErr(t, err)
		if n != 1 {
			t.Fatalf("a touch of a live session changed %d rows, want 1", n)
		}

		noErr(t, f.p.Revoke(ctx, issued.ID, authn.ReasonSelf))
		before := f.row(issued.ID)
		n, err = f.p.touchRow(ctx, issued.ID, later.Add(time.Hour), later.Add(2*time.Hour))
		noErr(t, err)
		if n != 0 {
			t.Errorf("a touch of a revoked session changed %d rows, want 0", n)
		}
		after := f.row(issued.ID)
		if !after.RevokedAt.Valid || after.RevokeReason.String != authn.ReasonSelf {
			t.Errorf("the session is no longer revoked: %+v", after)
		}
		if !after.IdleExpiresAt.Time.Equal(before.IdleExpiresAt.Time) || !after.LastSeenAt.Equal(before.LastSeenAt) {
			t.Error("the touch of a revoked session changed its periods")
		}
	})
}

// S2: requests within the touch interval write once between them.
func TestTheIdlePeriodIsExtendedOncePerTouchInterval(t *testing.T) {
	forEachDialect(t, func(t *testing.T, d dialectCase) {
		f := newFixture(t, d)
		f.accounts.add(alice, nil)
		f.settings.set(touchIntervalPath, 10*time.Second)
		f.settings.set(checkTTLPath, time.Minute)
		issued := f.create(newBrowser(alice))
		start := f.clock.Now()
		f.sql.reset()

		steps := []struct {
			advance time.Duration
			writes  int
		}{
			{5 * time.Second, 0}, // inside the interval of the creation
			{6 * time.Second, 1}, // 11s: the interval has run out
			{1 * time.Second, 1}, // 12s
			{3 * time.Second, 1}, // 15s
			{8 * time.Second, 2}, // 23s: run out again since the write at 11s
			{2 * time.Second, 2}, // 25s
		}
		for i, s := range steps {
			f.clock.Advance(s.advance)
			if got := outcomeOf(f.verify(issued.Token)); got.String() != "authenticated" {
				t.Fatalf("step %d: the verdict is %s", i, got)
			}
			if got := f.sql.count("UPDATE"); got != s.writes {
				t.Errorf("step %d: %d writes so far, want %d", i, got, s.writes)
			}
		}
		row := f.row(issued.ID)
		wantSeen := start.Add(23 * time.Second)
		if !row.LastSeenAt.Equal(wantSeen) {
			t.Errorf("last_seen_at = %s, want %s", row.LastSeenAt, wantSeen)
		}
		if want := wantSeen.Add(defaults.IdleTimeout); !row.IdleExpiresAt.Time.Equal(want) {
			t.Errorf("idle_expires_at = %s, want %s", row.IdleExpiresAt.Time, want)
		}
	})
}

// S3: the absolute period written to the row is the one computed from the
// setting and not the library's lifetime, which is the upper bound of the domain.
// With the library's expiry the session would run for 8760 hours and Status,
// which reads only the row, would not notice.
func TestTheAbsolutePeriodIsTheSettingsAndNotTheLibrarys(t *testing.T) {
	forEachDialect(t, func(t *testing.T, d dialectCase) {
		f := newFixture(t, d)
		f.accounts.add(alice, nil)
		f.settings.set(idleTimeoutPath, 24*time.Hour)
		f.settings.set(absoluteTimeoutPath, 12*time.Hour)
		issued := f.create(newBrowser(alice))
		start := f.clock.Now()
		ctx := context.Background()

		if !f.row(issued.ID).ExpiresAt.Time.Equal(start.Add(12 * time.Hour)) {
			t.Fatalf("expires_at = %s, want 12 hours from the creation", f.row(issued.ID).ExpiresAt.Time)
		}

		f.clock.Advance(12*time.Hour - time.Second)
		st, err := f.p.Status(ctx, issued.ID)
		noErr(t, err)
		if !st.Active {
			t.Error("the session is not active one second before its absolute period ends")
		}

		f.clock.Advance(2 * time.Second)
		st, err = f.p.Status(ctx, issued.ID)
		noErr(t, err)
		if st.Active {
			t.Error("the session is still active one second after 12 hours, so the library's lifetime ended it and the setting did not")
		}
		if got := outcomeOf(f.verify(issued.Token)); got.String() != "denied" {
			t.Errorf("the verdict after the absolute period is %s, want denied", got)
		}
	})
}

// S3: a commit whose period is over by the time it would be written writes
// nothing, and the expiry the library passes is not used.
func TestACommitIgnoresTheLibrarysExpiryAndSkipsAnEndedPeriod(t *testing.T) {
	forEachDialect(t, func(t *testing.T, d dialectCase) {
		f := newFixture(t, d)
		ctx := context.Background()
		now := f.clock.Now()
		pl := payload{
			SID: "sid-ended", Account: string(alice), Kind: string(authn.SessionBrowser),
			Methods: []authn.Method{authn.MethodPassword}, Assurance: authn.AssuranceSingle, AuthTime: now,
			Idle: now.Add(time.Hour), Expires: now,
		}
		blob, err := encodePayload(pl)
		noErr(t, err)
		store := storeAdapter{p: f.p}

		noErr(t, store.CommitCtx(ctx, "key-ended", blob, now.Add(24*time.Hour)))
		if f.count(alice) != 0 {
			t.Error("a period that was already over was written")
		}

		pl.SID, pl.Expires = "sid-live", now.Add(time.Hour)
		blob, err = encodePayload(pl)
		noErr(t, err)
		noErr(t, store.CommitCtx(ctx, "key-live", blob, now.Add(-time.Hour)))
		row, err := f.p.byID(ctx, "sid-live")
		noErr(t, err)
		if !row.ExpiresAt.Time.Equal(now.Add(time.Hour)) {
			t.Errorf("expires_at = %s, want the payload's period and not the library's expiry", row.ExpiresAt.Time)
		}

		pl.SID, pl.Kind = "sid-token", string(authn.SessionToken)
		blob, err = encodePayload(pl)
		noErr(t, err)
		if err := store.CommitCtx(ctx, "key-token", blob, now); err == nil {
			t.Error("the store committed a token session, which has no token to store")
		}
		if err := store.CommitCtx(ctx, "key-big", []byte(strings.Repeat("x", maxDataBytes+1)), now); err == nil {
			t.Error("the store committed a payload over the bound")
		}
	})
}

// S4: only a live browser row is found by its token, and unlinking a token
// revokes nothing.
func TestTheStoreFindsOnlyLiveBrowserRows(t *testing.T) {
	forEachDialect(t, func(t *testing.T, d dialectCase) {
		f := newFixture(t, d)
		f.accounts.add(alice, nil)
		ctx := context.Background()
		store := storeAdapter{p: f.p}
		now := f.clock.Now()

		found := func(token string) bool {
			t.Helper()
			sink := &rowSink{}
			_, ok, err := store.FindCtx(context.WithValue(ctx, sinkKey, sink), storeKey(token))
			noErr(t, err)
			if ok != (sink.row != nil) {
				t.Errorf("found = %v and the sink holds a row = %v", ok, sink.row != nil)
			}
			return ok
		}
		set := func(id authn.SessionID, column string, at time.Time) {
			t.Helper()
			noErr(t, f.p.handle.Exec("UPDATE "+tableName+" SET "+column+" = ? WHERE id = ?", at.UTC(), string(id)).Error)
		}

		live := f.create(newBrowser(alice))
		revoked := f.create(newBrowser(alice))
		idle := f.create(newBrowser(alice))
		absolute := f.create(newBrowser(alice))
		noErr(t, f.p.Revoke(ctx, revoked.ID, authn.ReasonSelf))
		set(idle.ID, "idle_expires_at", now.Add(-time.Second))
		set(absolute.ID, "expires_at", now.Add(-time.Second))

		for name, c := range map[string]struct {
			token string
			want  bool
		}{"live": {live.Token, true}, "revoked": {revoked.Token, false}, "idle": {idle.Token, false}, "absolute": {absolute.Token, false}} {
			if got := found(c.token); got != c.want {
				t.Errorf("a %s browser row: found = %v, want %v", name, got, c.want)
			}
		}

		// A token row and a service row do not answer to a token, even one that
		// was written into their token_hash.
		tokenRow := f.create(newToken1(alice))
		sid, err := f.p.ForCredential(ctx, alice, "cred-1", nil)
		noErr(t, err)
		for _, id := range []authn.SessionID{tokenRow.ID, sid} {
			noErr(t, f.p.handle.Exec("UPDATE "+tableName+" SET token_hash = ? WHERE id = ?", storeKey("planted-"+string(id)), string(id)).Error)
			if found("planted-" + string(id)) {
				t.Errorf("the %s row was found by a token", f.row(id).Kind)
			}
		}

		// Unlinking the token leaves the row, and its revocation state, alone.
		noErr(t, store.DeleteCtx(ctx, storeKey(live.Token)))
		row := f.row(live.ID)
		if row.TokenHash.Valid || row.RevokedAt.Valid {
			t.Errorf("after DeleteCtx token_hash valid = %v and revoked_at valid = %v, want a row with no token that is not revoked",
				row.TokenHash.Valid, row.RevokedAt.Valid)
		}
		if found(live.Token) {
			t.Error("a token that was unlinked still finds its row")
		}
	})
}

// recordingStore is a library store that remembers the keys it was given.
type recordingStore struct {
	mu   sync.Mutex
	keys []string
}

func (s *recordingStore) Delete(string) error { return nil }
func (s *recordingStore) Find(key string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys = append(s.keys, key)
	return nil, false, nil
}

func (s *recordingStore) Commit(key string, _ []byte, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys = append(s.keys, key)
	return nil
}

// S5 and the first external assumption: the store key this module computes is the
// one the library computes for the same token. A library upgrade that changes the
// hash fails here first.
func TestTheStoreKeyIsTheOneTheLibraryComputes(t *testing.T) {
	store := &recordingStore{}
	sm := scs.New()
	sm.HashTokenInStore = true
	sm.Store = store
	ctx, err := sm.Load(context.Background(), "")
	noErr(t, err)
	sm.Put(ctx, "k", "v")
	token, _, err := sm.Commit(ctx)
	noErr(t, err)
	if _, loadErr := sm.Load(context.Background(), token); loadErr != nil {
		t.Fatalf("loading the token: %v", loadErr)
	}
	if len(store.keys) != 2 {
		t.Fatalf("the library gave the store %d keys, want 2", len(store.keys))
	}
	for _, key := range store.keys {
		if key != storeKey(token) {
			t.Errorf("the library's key is %q and this module's is %q", key, storeKey(token))
		}
	}
	if len(token) != 43 {
		t.Errorf("a library token is %d characters long, and %d for this module's", len(token), 43)
	}
	mine, err := newToken()
	noErr(t, err)
	if len(mine) != len(token) {
		t.Errorf("a token of this module is %d characters, the library's is %d", len(mine), len(token))
	}
}

// raceBefore registers a callback that runs fn before the first statement that
// matches, and between its read and its write, so that a test makes another
// writer win a conditional update.
func raceBefore(t *testing.T, f *fixture, match string, fn func(attempt int)) {
	t.Helper()
	var attempts atomic.Int32
	var inside atomic.Bool
	var armed atomic.Bool
	armed.Store(true)
	noErr(t, f.p.handle.Callback().Raw().Before("gorm:raw").Register("session:race", func(tx *gorm.DB) {
		if !armed.Load() || inside.Load() || !strings.Contains(tx.Statement.SQL.String(), match) {
			return
		}
		inside.Store(true)
		defer inside.Store(false)
		fn(int(attempts.Add(1)))
	}))
	// A callback is not removed, which gorm warns about: the handle belongs to
	// this case alone, and the callback stops acting when the case ends.
	t.Cleanup(func() { armed.Store(false) })
}

// S5: a conditional update that loses to another writer changes nothing, and the
// call reads the row again to learn why.
func TestARotationThatLosesTheRaceLeavesTheRowAlone(t *testing.T) {
	forEachDialect(t, func(t *testing.T, d dialectCase) {
		ctx := context.Background()
		compete := func(f *fixture, id authn.SessionID, statement string, args ...any) {
			t.Helper()
			other := f.database.DB().Session(&gorm.Session{NewDB: true, Logger: f.sql})
			if err := other.Exec(statement, append(args, string(id))...).Error; err != nil {
				t.Errorf("the competing write failed: %v", err)
			}
		}

		t.Run("another rotation wins the first attempt", func(t *testing.T) {
			f := newFixture(t, d)
			f.accounts.add(alice, nil)
			issued := f.create(newBrowser(alice))
			raceBefore(t, f, "SET token_hash", func(attempt int) {
				if attempt == 1 {
					compete(f, issued.ID, "UPDATE "+tableName+" SET token_hash = ? WHERE id = ?", storeKey("competitor"))
				}
			})
			rotated, err := f.p.Rotate(ctx, issued.ID)
			noErr(t, err)
			if got := f.row(issued.ID).TokenHash.String; got != storeKey(rotated.Token) {
				t.Errorf("token_hash = %q, want the digest of the token the call returned", got)
			}
			if got := outcomeOf(f.verify(rotated.Token)); got.String() != "authenticated" {
				t.Errorf("the new token is %s", got)
			}
		})

		t.Run("a revocation wins", func(t *testing.T) {
			f := newFixture(t, d)
			f.accounts.add(alice, nil)
			issued := f.create(newBrowser(alice))
			original := f.row(issued.ID).TokenHash.String
			raceBefore(t, f, "SET token_hash", func(int) {
				compete(f, issued.ID, "UPDATE "+tableName+" SET revoked_at = ?, revoke_reason = 'admin' WHERE id = ?", time.Now().UTC())
			})
			_, err := f.p.Rotate(ctx, issued.ID)
			if !errors.Is(err, authn.ErrSessionRevoked) {
				t.Fatalf("Rotate = %v, want ErrSessionRevoked", err)
			}
			row := f.row(issued.ID)
			if row.TokenHash.String != original || !row.RevokedAt.Valid {
				t.Errorf("the row changed: token_hash %q, revoked %v", row.TokenHash.String, row.RevokedAt.Valid)
			}
		})

		t.Run("another writer wins every attempt", func(t *testing.T) {
			f := newFixture(t, d)
			f.accounts.add(alice, nil)
			issued := f.create(newBrowser(alice))
			var last string
			raceBefore(t, f, "SET token_hash", func(attempt int) {
				last = storeKey(fmt.Sprintf("competitor-%d", attempt))
				compete(f, issued.ID, "UPDATE "+tableName+" SET token_hash = ? WHERE id = ?", last)
			})
			_, err := f.p.Rotate(ctx, issued.ID)
			if err == nil {
				t.Fatal("Rotate succeeded against a writer that won every attempt")
			}
			if got := f.row(issued.ID).TokenHash.String; got != last {
				t.Errorf("token_hash = %q, want the last competitor's %q: a losing update changed the row", got, last)
			}
		})
	})
}

// countingStore counts the reads the library makes of the store.
type countingStore struct {
	inner storeAdapter
	finds atomic.Int64
}

func (s *countingStore) FindCtx(ctx context.Context, key string) ([]byte, bool, error) {
	s.finds.Add(1)
	return s.inner.FindCtx(ctx, key)
}

func (s *countingStore) CommitCtx(ctx context.Context, key string, b []byte, e time.Time) error {
	return s.inner.CommitCtx(ctx, key, b, e)
}

func (s *countingStore) DeleteCtx(ctx context.Context, key string) error {
	return s.inner.DeleteCtx(ctx, key)
}

func (s *countingStore) Find(key string) ([]byte, bool, error) {
	return s.FindCtx(context.Background(), key)
}

func (s *countingStore) Commit(key string, b []byte, e time.Time) error {
	return s.CommitCtx(context.Background(), key, b, e)
}
func (s *countingStore) Delete(key string) error { return s.DeleteCtx(context.Background(), key) }

// S6: the adapter does not cache, and the verifying backend's cache answers
// without the library and without the database.
func TestACacheHitReadsNothingAndAMissReadsOnce(t *testing.T) {
	forEachDialect(t, func(t *testing.T, d dialectCase) {
		f := newFixture(t, d)
		f.accounts.add(alice, nil)
		spy := &countingStore{inner: storeAdapter{p: f.p}}
		f.p.sm.Store = spy
		issued := f.create(newBrowser(alice))
		f.sql.reset()

		for i := range 3 {
			if got := outcomeOf(f.verify(issued.Token)); got.String() != "authenticated" {
				t.Fatalf("request %d: %s", i, got)
			}
		}
		if got := spy.finds.Load(); got != 1 {
			t.Errorf("the library read the store %d times for three requests, want 1", got)
		}
		if got := f.sql.count("SELECT"); got != 1 {
			t.Errorf("%d reads of the table for three requests, want 1", got)
		}

		// Past check-ttl the entry has run out and the next request reads again.
		f.clock.Advance(defaults.CheckTTL + time.Second)
		f.verify(issued.Token)
		if got := spy.finds.Load(); got != 2 {
			t.Errorf("the library read the store %d times after the entry ran out, want 2", got)
		}

		// The adapter itself keeps nothing: two reads in a row are two statements.
		f.sql.reset()
		for range 2 {
			_, ok, err := storeAdapter{p: f.p}.FindCtx(context.Background(), storeKey(issued.Token))
			noErr(t, err)
			if !ok {
				t.Fatal("the adapter did not find a live session")
			}
		}
		if got := f.sql.count("SELECT"); got != 2 {
			t.Errorf("two adapter reads made %d reads of the table, want 2", got)
		}
	})
}

// the cookie header a recorder holds, parsed.
func cookiesOf(h http.Header) []*http.Cookie {
	return (&http.Response{Header: h}).Cookies()
}
