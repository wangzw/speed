package mfa

import (
	"testing"
	"time"
)

// A setting read on every request that is wrong is reported once, not on each
// read: the same fault written again and again would flood the log. The fault
// here is a value outside the setting's domain, which reads as the declared
// default and is said at Error the first time and not after. A fault that changes
// is said again, so a setting that went bad a second time is not silent.
func TestASettingFaultIsReportedOnceAndNotOnEveryRead(t *testing.T) {
	store := newStore()
	logs := &logBuffer{}
	r := newReader(store, logs.logger())
	store.set(recentWindowPath, 5*time.Hour)

	for i := 0; i < 3; i++ {
		if got := r.recentWindow(); got != defaults.RecentWindow {
			t.Fatalf("read %d: recentWindow = %v, want the declared default %v", i, got, defaults.RecentWindow)
		}
	}
	if lines := logs.lines("level=ERROR", recentWindowPath); len(lines) != 1 {
		t.Errorf("the same fault gave %d Error lines over three reads, want 1:\n%s", len(lines), logs.String())
	}

	store.set(recentWindowPath, 6*time.Hour)
	if got := r.recentWindow(); got != defaults.RecentWindow {
		t.Fatalf("recentWindow = %v, want the declared default %v", got, defaults.RecentWindow)
	}
	if lines := logs.lines("level=ERROR", recentWindowPath); len(lines) != 2 {
		t.Errorf("a changed fault gave %d Error lines, want 2:\n%s", len(lines), logs.String())
	}
}
