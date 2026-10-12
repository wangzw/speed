package impersonation

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
)

// TestTheLifetimeComesFromTheSettings holds the max-ttl setting: it bounds the
// record's end, and a read that fails falls back to the declared default.
func TestTheLifetimeComesFromTheSettings(t *testing.T) {
	for _, c := range []struct {
		name string
		set  func(r *rig)
		want time.Duration
	}{
		{"the declared default", func(r *rig) {}, 30 * time.Minute},
		{"a tuned value", func(r *rig) { r.store.set(maxTTLPath, 5*time.Minute) }, 5 * time.Minute},
		{"a read that fails", func(r *rig) { r.store.fail(maxTTLPath, errFromStore) }, 30 * time.Minute},
	} {
		t.Run(c.name, func(t *testing.T) {
			onEachDialect(t, func(t *testing.T, r *rig) {
				c.set(r)
				g, err := r.p.Start(context.Background(), adminIdentity(t), request())
				if err != nil {
					t.Fatalf("Start: %v", err)
				}
				if want := base.Add(c.want); !g.EndsAt.Equal(want) {
					t.Errorf("EndsAt = %v, want %v", g.EndsAt, want)
				}
			})
		})
	}
}

// TestTheAuthenticationAgeComesFromTheSettings holds the max-auth-age setting: a
// value smaller than the default refuses an authentication the default would
// admit, with the step-up sentinel.
func TestTheAuthenticationAgeComesFromTheSettings(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		r.store.set(maxAuthAgePath, time.Minute)
		id := adminIdentity(t, authntest.WithAuthTime(base.Add(-2*time.Minute)))
		_, err := r.p.Start(context.Background(), id, request())
		if !errors.Is(err, authn.ErrStepUpRequired) {
			t.Fatalf("Start = %v, want ErrStepUpRequired under the tighter window", err)
		}
	})
}

// TestTheTTLIsBoundedByTheRequestTheSettingAndTheSession holds the shorter of the
// three: the request's own lifetime, the setting, and the session's remaining
// life, and the hard hour ceiling.
func TestTheTTLIsBoundedByTheRequestTheSettingAndTheSession(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		// A session with ten minutes left bounds a thirty-minute setting.
		r.sessions.session.ExpiresAt = base.Add(10 * time.Minute)
		g, err := r.p.Start(context.Background(), adminIdentity(t), request())
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		if want := base.Add(10 * time.Minute); !g.EndsAt.Equal(want) {
			t.Errorf("EndsAt = %v, want the session's remaining life %v", g.EndsAt, want)
		}
	})
}

// TestASessionWithNoRemainingLifeIsRefused holds the corner: a session that has
// no life left to bound the record by is ErrImpersonationNeedsSession.
func TestASessionWithNoRemainingLifeIsRefused(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		r.sessions.session.ExpiresAt = base.Add(-time.Minute)
		_, err := r.p.Start(context.Background(), adminIdentity(t), request())
		if !errors.Is(err, authn.ErrImpersonationNeedsSession) {
			t.Fatalf("Start = %v, want ErrImpersonationNeedsSession", err)
		}
	})
}

// TestAMissingStoreReadsTheDeclaredDefaults holds the legal assembly with no
// settings store: every setting read falls back to its declared default rather
// than reaching into a store that is not there.
func TestAMissingStoreReadsTheDeclaredDefaults(t *testing.T) {
	r := newReader(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if got := r.maxTTL(); got != defaults.MaxTTL {
		t.Errorf("maxTTL = %v, want the declared default %v", got, defaults.MaxTTL)
	}
	if got := r.maxAuthAge(); got != defaults.MaxAuthAge {
		t.Errorf("maxAuthAge = %v, want the declared default %v", got, defaults.MaxAuthAge)
	}
	if got := r.tokenAccessTTL(); got != defaultTokenAccessTTL {
		t.Errorf("tokenAccessTTL = %v, want the declared default %v", got, defaultTokenAccessTTL)
	}
}

// TestASettingOutsideItsDomainReadsAsTheDefault holds a value written around the
// store: a max-ttl above its domain reads as the declared default, and so does
// the token module's access-ttl above its own.
func TestASettingOutsideItsDomainReadsAsTheDefault(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		r.store.set(maxTTLPath, 2*time.Hour)
		g, err := r.p.Start(context.Background(), adminIdentity(t), request())
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		if want := base.Add(defaults.MaxTTL); !g.EndsAt.Equal(want) {
			t.Errorf("EndsAt = %v, want the declared default %v", g.EndsAt, want)
		}
		// The token module's access-ttl is read through the same path and has its
		// own domain: a value above it reads as the declared default too.
		r.store.set(accessTTLPath, 2*time.Hour)
		if got := r.p.settings.tokenAccessTTL(); got != defaultTokenAccessTTL {
			t.Errorf("tokenAccessTTL = %v, want the declared default %v", got, defaultTokenAccessTTL)
		}
	})
}

// TestASettingAtTheBottomOfItsDomainIsAccepted holds the lower boundary: a
// max-ttl at the bottom of its domain is used as written, not read as the
// default.
func TestASettingAtTheBottomOfItsDomainIsAccepted(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		r.store.set(maxTTLPath, minMaxTTL)
		g, err := r.p.Start(context.Background(), adminIdentity(t), request())
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		if want := base.Add(minMaxTTL); !g.EndsAt.Equal(want) {
			t.Errorf("EndsAt = %v, want the written value %v", g.EndsAt, want)
		}
	})
}

// TestTheMaxAuthAgeOutsideItsDomainReadsAsTheDefault holds the auth-age domain:
// a value above its domain reads as the declared default, which then refuses an
// authentication the larger value would have admitted.
func TestTheMaxAuthAgeOutsideItsDomainReadsAsTheDefault(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		r.store.set(maxAuthAgePath, time.Hour)
		id := adminIdentity(t, authntest.WithAuthTime(base.Add(-10*time.Minute)))
		_, err := r.p.Start(context.Background(), id, request())
		if !errors.Is(err, authn.ErrStepUpRequired) {
			t.Fatalf("Start = %v, want ErrStepUpRequired under the declared default", err)
		}
	})
}
