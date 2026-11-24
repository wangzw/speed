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

// TestTheLifetimeComesFromTheSettings checks that the max-ttl setting bounds the record's end and a failed read falls back to the declared default.
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

// TestTheAuthenticationAgeComesFromTheSettings checks that a max-auth-age below the default refuses an older authentication with ErrStepUpRequired.
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

// TestTheTTLIsBoundedByTheRequestTheSettingAndTheSession checks that the settled
// lifetime is the shortest of the request, the setting, the session's remaining life and the hour ceiling.
func TestTheTTLIsBoundedByTheRequestTheSettingAndTheSession(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
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

// TestASessionWithNoRemainingLifeIsRefused checks that a session with no life left is ErrImpersonationNeedsSession.
func TestASessionWithNoRemainingLifeIsRefused(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		r.sessions.session.ExpiresAt = base.Add(-time.Minute)
		_, err := r.p.Start(context.Background(), adminIdentity(t), request())
		if !errors.Is(err, authn.ErrImpersonationNeedsSession) {
			t.Fatalf("Start = %v, want ErrImpersonationNeedsSession", err)
		}
	})
}

// TestAMissingStoreReadsTheDeclaredDefaults checks that every setting reads its declared default with no settings store.
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

// TestASettingOutsideItsDomainReadsAsTheDefault checks that a max-ttl or access-ttl above its domain reads as the declared default.
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
		r.store.set(accessTTLPath, 2*time.Hour)
		if got := r.p.settings.tokenAccessTTL(); got != defaultTokenAccessTTL {
			t.Errorf("tokenAccessTTL = %v, want the declared default %v", got, defaultTokenAccessTTL)
		}
	})
}

// TestASettingAtTheBottomOfItsDomainIsAccepted checks that a max-ttl at the bottom of its domain is used as written.
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

// TestTheMaxAuthAgeOutsideItsDomainReadsAsTheDefault checks that an auth age above its domain reads as the declared default.
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
