package profile

import (
	"errors"
	"testing"
	"time"
)

// TestRecentWindowFallsBackAndReports holds the freshness window's read: an
// absent store, a failed read and a value outside the domain all read as the
// declared default, and the two faults are reported.
func TestRecentWindowFallsBackAndReports(t *testing.T) {
	logs := &logBuffer{}
	logger := logs.logger()

	if got := newReader(nil, logger).recentWindow(); got != defaultRecentWindow {
		t.Errorf("with no store: window = %v, want the default %v", got, defaultRecentWindow)
	}

	failing := &fakeStore{err: errors.New("boom")}
	if got := newReader(failing, logger).recentWindow(); got != defaultRecentWindow {
		t.Errorf("on a failed read: window = %v, want the default %v", got, defaultRecentWindow)
	}

	outside := &fakeStore{window: 5 * time.Second}
	if got := newReader(outside, logger).recentWindow(); got != defaultRecentWindow {
		t.Errorf("on a value outside the domain: window = %v, want the default %v", got, defaultRecentWindow)
	}
}

// TestRecentWindowReadsAValueInItsDomain holds the in-domain path.
func TestRecentWindowReadsAValueInItsDomain(t *testing.T) {
	store := &fakeStore{window: 3 * time.Minute}
	if got := newReader(store, (&logBuffer{}).logger()).recentWindow(); got != 3*time.Minute {
		t.Errorf("window = %v, want 3m", got)
	}
}
