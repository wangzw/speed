package session

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// With no settings store every setting reads as its declared default, and the
// periods a session is given are computed from those defaults.
func TestWithoutASettingsStoreEverySettingReadsAsItsDefault(t *testing.T) {
	f := newFixture(t, dialects[0], withoutSettings())
	f.accounts.add(alice, nil)
	start := f.clock.Now()

	browser := f.create(newBrowser(alice))
	row := f.row(browser.ID)
	if want := start.Add(defaults.IdleTimeout); !row.IdleExpiresAt.Time.Equal(want) {
		t.Errorf("idle_expires_at = %s, want the default idle period %s", row.IdleExpiresAt.Time, want)
	}
	if want := start.Add(defaults.AbsoluteTimeout); !row.ExpiresAt.Time.Equal(want) {
		t.Errorf("expires_at = %s, want the default absolute period %s", row.ExpiresAt.Time, want)
	}

	token := f.create(newToken1(alice))
	if want := start.Add(defaults.TokenAbsoluteTimeout); !f.row(token.ID).ExpiresAt.Time.Equal(want) {
		t.Errorf("a token session's expires_at = %s, want the default %s", f.row(token.ID).ExpiresAt.Time, want)
	}
}

// A value outside its declared domain is read as the default and reported at
// Error, once for as long as the same fault stands.
func TestASettingOutsideItsDomainIsReadAsItsDefaultAndReportedOnce(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.settings.set(idleTimeoutPath, 48*time.Hour)

	got := f.p.settings.timing()
	if got.idle != defaults.IdleTimeout {
		t.Errorf("idle = %s, want the default %s", got.idle, defaults.IdleTimeout)
	}
	first := strings.Count(f.logs.String(), "level=ERROR")
	if first != 1 {
		t.Fatalf("the out-of-domain setting was reported %d times, want 1:\n%s", first, f.logs.String())
	}
	if !strings.Contains(f.logs.String(), idleTimeoutPath) {
		t.Errorf("the report does not name the setting %s:\n%s", idleTimeoutPath, f.logs.String())
	}

	f.p.settings.timing()
	if again := strings.Count(f.logs.String(), "level=ERROR"); again != first {
		t.Errorf("the same fault was reported again: %d reports, want %d", again, first)
	}
}

// A settings store that cannot be read leaves every setting at its default, so a
// broken store degrades the sessions rather than failing every request.
func TestASettingsStoreThatCannotBeReadFallsBackToTheDefault(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.settings.failWith(errors.New("the settings store is down"))

	got := f.p.settings.timing()
	want := timing{
		idle: defaults.IdleTimeout, absolute: defaults.AbsoluteTimeout,
		tokenAbsolute: defaults.TokenAbsoluteTimeout, ssoAbsolute: defaults.SSOAbsoluteTimeout,
	}
	if got != want {
		t.Errorf("timing = %+v, want the declared defaults %+v", got, want)
	}
	if !strings.Contains(f.logs.String(), "level=ERROR") {
		t.Errorf("an unreadable setting was not reported:\n%s", f.logs.String())
	}
}

// A session made with an SSO provider takes the shorter of its kind's period and
// the SSO period, and every kind is held to its own setting.
func TestTheSSOPeriodCapsWhateverKindTheSessionIs(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.accounts.add(alice, nil)
	f.settings.set(ssoAbsolutePath, time.Hour)
	f.settings.set(absoluteTimeoutPath, 12*time.Hour)
	f.settings.set(tokenAbsolutePath, 100*time.Hour)
	start := f.clock.Now()

	browser := newBrowser(alice)
	browser.Provider = provider
	issued := f.create(browser)
	if want := start.Add(time.Hour); !f.row(issued.ID).ExpiresAt.Time.Equal(want) {
		t.Errorf("an SSO browser session's expires_at = %s, want the SSO period %s", f.row(issued.ID).ExpiresAt.Time, want)
	}

	token := newToken1(alice)
	token.Provider = provider
	tissued := f.create(token)
	if want := start.Add(time.Hour); !f.row(tissued.ID).ExpiresAt.Time.Equal(want) {
		t.Errorf("an SSO token session's expires_at = %s, want the SSO period %s", f.row(tissued.ID).ExpiresAt.Time, want)
	}
}

// The declared defaults are the ones the design lists, so a change to them is a
// change to what an assembly without a store runs on.
func TestTheDeclaredDefaultsAreTheDesignsNumbers(t *testing.T) {
	want := options{
		IdleTimeout: 30 * time.Minute, AbsoluteTimeout: 12 * time.Hour,
		TokenAbsoluteTimeout: 720 * time.Hour, SSOAbsoluteTimeout: 8 * time.Hour,
		TouchInterval: 60 * time.Second, CheckTTL: 5 * time.Second,
		MaxPerAccount: 20, Retention: 168 * time.Hour,
	}
	if defaults != want {
		t.Errorf("defaults = %+v, want %+v", defaults, want)
	}
	if lifetimeUpperBound != 8760*time.Hour {
		t.Errorf("lifetimeUpperBound = %s, want the token domain's upper bound", lifetimeUpperBound)
	}
}
