package token

import (
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/settings"
)

// TestTheSettingDomainsAdmitTheirBounds holds that a value at a domain's bound is
// inside it and one past is out.
func TestTheSettingDomainsAdmitTheirBounds(t *testing.T) {
	if !accessTTLInDomain(accessTTLMin) || !accessTTLInDomain(accessTTLMax) {
		t.Error("an access-ttl at its own bound is outside the domain")
	}
	if accessTTLInDomain(accessTTLMin-time.Second) || accessTTLInDomain(accessTTLMax+time.Second) {
		t.Error("an access-ttl past its bound is inside the domain")
	}
	if !leewayInDomain(leewayMin) || !leewayInDomain(leewayMax) {
		t.Error("a leeway at its own bound is outside the domain")
	}
	if leewayInDomain(leewayMin-time.Second) || leewayInDomain(leewayMax+time.Second) {
		t.Error("a leeway past its bound is inside the domain")
	}
	if !refreshIdleInDomain(refreshIdleTTLMin) || !refreshIdleInDomain(refreshIdleTTLMax) {
		t.Error("a refresh-idle-ttl at its own bound is outside the domain")
	}
	if refreshIdleInDomain(refreshIdleTTLMin-time.Second) || refreshIdleInDomain(refreshIdleTTLMax+time.Second) {
		t.Error("a refresh-idle-ttl past its bound is inside the domain")
	}
	if !refreshGraceInDomain(refreshGraceMin) || !refreshGraceInDomain(refreshGraceMax) {
		t.Error("a refresh-grace at its own bound is outside the domain")
	}
	if refreshGraceInDomain(refreshGraceMin-time.Second) || refreshGraceInDomain(refreshGraceMax+time.Second) {
		t.Error("a refresh-grace past its bound is inside the domain")
	}

	if defaults.AccessTTL != 10*time.Minute || defaults.Leeway != 30*time.Second {
		t.Errorf("the access-ttl and leeway defaults are %v and %v", defaults.AccessTTL, defaults.Leeway)
	}
	if defaults.RefreshIdleTTL != 168*time.Hour || defaults.RefreshGrace != 0 {
		t.Errorf("the refresh-idle-ttl and refresh-grace defaults are %v and %v",
			defaults.RefreshIdleTTL, defaults.RefreshGrace)
	}
	if accessTTLUpperBound != time.Hour {
		t.Errorf("the access-ttl upper bound is %v, want one hour", accessTTLUpperBound)
	}
}

// TestEachSettingReadsAsItsDefaultWithoutAStore holds that an assembly with no
// settings store runs on the declared defaults.
func TestEachSettingReadsAsItsDefaultWithoutAStore(t *testing.T) {
	r := newSettingReader(slog.Default())
	if got := r.accessTTL(); got != defaults.AccessTTL {
		t.Errorf("access-ttl read %v, want the default %v", got, defaults.AccessTTL)
	}
	if got := r.leeway(); got != defaults.Leeway {
		t.Errorf("leeway read %v, want the default %v", got, defaults.Leeway)
	}
	if got := r.refreshIdleTTL(); got != defaults.RefreshIdleTTL {
		t.Errorf("refresh-idle-ttl read %v, want the default %v", got, defaults.RefreshIdleTTL)
	}
	if got := r.refreshGrace(); got != defaults.RefreshGrace {
		t.Errorf("refresh-grace read %v, want the default %v", got, defaults.RefreshGrace)
	}
}

// TestTheCheckTTLDomainAdmitsItsUpperBound holds the session check-ttl
// domain's top: sixty seconds is inside it and one past is not.
func TestTheCheckTTLDomainAdmitsItsUpperBound(t *testing.T) {
	if checkTTLMax != 60*time.Second {
		t.Errorf("the check-ttl upper bound is %v, want 60s", checkTTLMax)
	}
	if !checkTTLInDomain(0) || !checkTTLInDomain(60*time.Second) {
		t.Error("a check-ttl at or below the upper bound is outside the domain")
	}
	if checkTTLInDomain(61 * time.Second) {
		t.Error("a check-ttl past the upper bound is inside the domain")
	}
}

// steerStore is a settings store whose reads a case steers: one value for every
// key, or one error for a chosen key.
type steerStore struct {
	settings.Store
	value  time.Duration
	errKey string
	err    error
}

func (s *steerStore) Get(_ settings.Layer, key string, target any) error {
	if s.errKey != "" && key == s.errKey {
		return s.err
	}
	ptr, ok := target.(*time.Duration)
	if !ok {
		return settings.ErrUnknownKey
	}
	*ptr = s.value
	return nil
}

// TestASettingOutsideItsDomainReadsAsTheDefault holds that a stored value outside
// the domain reads as the default.
func TestASettingOutsideItsDomainReadsAsTheDefault(t *testing.T) {
	reader := newSettingReader(slog.Default())
	reader.store = &steerStore{value: 5 * time.Minute}
	if got := reader.leeway(); got != defaults.Leeway {
		t.Errorf("an out-of-domain leeway read %v, want the default %v", got, defaults.Leeway)
	}
}

// TestASettingThatCannotBeReadReadsAsTheDefault holds the failure direction: a
// read error is not served as the stored value, the declared default is used.
func TestASettingThatCannotBeReadReadsAsTheDefault(t *testing.T) {
	reader := newSettingReader(slog.Default())
	reader.store = &steerStore{errKey: leewayPath, err: errors.New("boom")}
	if got := reader.leeway(); got != defaults.Leeway {
		t.Errorf("an unreadable leeway read %v, want the default %v", got, defaults.Leeway)
	}
}

type switchStore struct {
	settings.Store
	mu  sync.Mutex
	err error
}

func (s *switchStore) Get(_ settings.Layer, _ string, target any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	if ptr, ok := target.(*time.Duration); ok {
		*ptr = defaults.Leeway
	}
	return nil
}

func (s *switchStore) setErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

// TestTheSettingFaultIsReportedOnceAndCleared holds that a standing fault is
// logged once and a cleared one again when it returns.
func TestTheSettingFaultIsReportedOnceAndCleared(t *testing.T) {
	logs := &logBuffer{}
	reader := newSettingReader(logs.logger())
	store := &switchStore{err: errors.New("boom")}
	reader.store = store

	reader.leeway()
	reader.leeway()
	if got := strings.Count(logs.String(), leewayPath); got != 1 {
		t.Fatalf("the standing fault was reported %d times, want 1", got)
	}

	store.setErr(nil)
	reader.leeway()

	store.setErr(errors.New("boom"))
	reader.leeway()
	if got := strings.Count(logs.String(), leewayPath); got != 2 {
		t.Errorf("the recurring fault was reported %d times, want 2", got)
	}
}
