package registry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/sms"
)

// TestPollReadsOnlyPendingRows checks that the pass reads the vendor only for rows with a language still pending.
func TestPollReadsOnlyPendingRows(t *testing.T) {
	r := newSQLiteRig(t)
	r.plant("waiting", []string{"code"}, map[string]string{"en": "cp"},
		map[string]sms.Approval{"en": {State: sms.ApprovalPending}}, true, base)
	r.plant("settled", []string{"code"}, map[string]string{"en": "ca"},
		map[string]sms.Approval{"en": {State: sms.ApprovalApproved}}, true, base)
	r.provider.setApproval("cp", sms.Approval{State: sms.ApprovalApproved})
	r.load()

	r.s.pollOnce(context.Background())

	calls := r.provider.calls()
	if len(calls) != 1 || calls[0] != "cp" {
		t.Errorf("the poller asked the vendor about %v, want only cp", calls)
	}
	if got := r.approvalOf("waiting", "en"); got != string(sms.ApprovalApproved) {
		t.Errorf("the pending row's review is %q, want approved", got)
	}
}

// TestPollRecordsVerdictAndPublishes checks that a verdict updates the row and publishes a change event.
func TestPollRecordsVerdictAndPublishes(t *testing.T) {
	r := newSQLiteRig(t)
	r.plant("waiting", []string{"code"}, map[string]string{"en": "cp"},
		map[string]sms.Approval{"en": {State: sms.ApprovalPending}}, true, base)
	r.provider.setApproval("cp", sms.Approval{State: sms.ApprovalRejected, Reason: "content not allowed"})
	r.load()

	r.s.pollOnce(context.Background())

	if got := r.approvalOf("waiting", "en"); got != string(sms.ApprovalRejected) {
		t.Errorf("the row's review is %q, want rejected", got)
	}
	if r.versionOf("waiting") != 2 {
		t.Errorf("the version is %d, want 2", r.versionOf("waiting"))
	}
	if len(r.bus.events()) != 1 {
		t.Errorf("the bus published %d events, want 1", len(r.bus.events()))
	}
}

// TestPollVendorFailureLeavesState checks that a failed vendor call leaves the state unchanged and logs the failure.
func TestPollVendorFailureLeavesState(t *testing.T) {
	r := newSQLiteRig(t)
	r.plant("waiting", []string{"code"}, map[string]string{"en": "cp"},
		map[string]sms.Approval{"en": {State: sms.ApprovalPending}}, true, base)
	r.provider.failApproval("cp", errors.New("the vendor is unreachable"))
	r.load()

	r.s.pollOnce(context.Background())

	if got := r.approvalOf("waiting", "en"); got != string(sms.ApprovalPending) || r.versionOf("waiting") != 1 {
		t.Errorf("the row is %q at version %d after a failed read, want pending at 1",
			got, r.versionOf("waiting"))
	}
	if len(r.logs.lines("reading a template's review state")) == 0 {
		t.Errorf("the failure was not logged; the log holds:\n%s", r.logs.buf.String())
	}
	if len(r.bus.events()) != 0 {
		t.Errorf("the bus published %d events, want 0", len(r.bus.events()))
	}
}

// TestPollAsksOnlyPendingLanguages checks that a settled language is not read and a running one is written back alone.
func TestPollAsksOnlyPendingLanguages(t *testing.T) {
	r := newSQLiteRig(t)
	r.plant("multi", []string{"code"}, map[string]string{"en": "c-en", "zh": "c-zh"},
		map[string]sms.Approval{"en": {State: sms.ApprovalApproved}, "zh": {State: sms.ApprovalPending}}, true, base)
	r.provider.setApproval("c-zh", sms.Approval{State: sms.ApprovalRejected, Reason: "nope"})
	r.load()

	r.s.pollOnce(context.Background())

	calls := r.provider.calls()
	if len(calls) != 1 || calls[0] != "c-zh" {
		t.Errorf("the poller asked about %v, want only the pending language c-zh", calls)
	}
	if got := r.approvalOf("multi", "zh"); got != string(sms.ApprovalRejected) {
		t.Errorf("the Chinese review is %q, want rejected", got)
	}
	if got := r.approvalOf("multi", "en"); got != string(sms.ApprovalApproved) {
		t.Errorf("the English review became %q, want it left approved", got)
	}
}

// TestPollSettlesEachLanguageSeparately checks that one language is written back while another is still running.
func TestPollSettlesEachLanguageSeparately(t *testing.T) {
	r := newSQLiteRig(t)
	r.plant("multi", []string{"code"}, map[string]string{"en": "c-en", "zh": "c-zh"},
		map[string]sms.Approval{"en": {State: sms.ApprovalPending}, "zh": {State: sms.ApprovalPending}}, true, base)
	r.provider.setApproval("c-en", sms.Approval{State: sms.ApprovalApproved})
	r.provider.setApproval("c-zh", sms.Approval{State: sms.ApprovalPending})
	r.load()

	r.s.pollOnce(context.Background())

	if got := r.approvalOf("multi", "en"); got != string(sms.ApprovalApproved) {
		t.Errorf("the English review is %q, want approved", got)
	}
	if got := r.approvalOf("multi", "zh"); got != string(sms.ApprovalPending) {
		t.Errorf("the Chinese review is %q, want still pending", got)
	}
	if r.versionOf("multi") != 2 {
		t.Errorf("the version is %d, want 2 (one language settled)", r.versionOf("multi"))
	}

	r.provider.setApproval("c-zh", sms.Approval{State: sms.ApprovalRejected, Reason: "nope"})
	r.s.pollOnce(context.Background())
	if got := r.approvalOf("multi", "zh"); got != string(sms.ApprovalRejected) {
		t.Errorf("the Chinese review is %q, want rejected", got)
	}
	if got := r.approvalOf("multi", "en"); got != string(sms.ApprovalApproved) {
		t.Errorf("the English review became %q, want it left approved", got)
	}
}

// TestPollVendorFailureLeavesOneLanguageAlone checks that a language the vendor
// could not answer about stays as it is while a sibling is written back.
func TestPollVendorFailureLeavesOneLanguageAlone(t *testing.T) {
	r := newSQLiteRig(t)
	r.plant("multi", []string{"code"}, map[string]string{"en": "c-en", "zh": "c-zh"},
		map[string]sms.Approval{"en": {State: sms.ApprovalPending}, "zh": {State: sms.ApprovalPending}}, true, base)
	r.provider.failApproval("c-en", errors.New("the vendor is unreachable"))
	r.provider.setApproval("c-zh", sms.Approval{State: sms.ApprovalApproved})
	r.load()

	r.s.pollOnce(context.Background())

	if got := r.approvalOf("multi", "en"); got != string(sms.ApprovalPending) {
		t.Errorf("the English review is %q after a failed read, want pending", got)
	}
	if got := r.approvalOf("multi", "zh"); got != string(sms.ApprovalApproved) {
		t.Errorf("the Chinese review is %q, want approved", got)
	}
	if len(r.logs.lines("reading a template's review state")) == 0 {
		t.Errorf("the failure was not logged; the log holds:\n%s", r.logs.buf.String())
	}
}

// TestPollNeverAsksAboutALanguageWithNoReview checks that a carrier with no
// per-template review records NotRequired and is never polled.
func TestPollNeverAsksAboutALanguageWithNoReview(t *testing.T) {
	r := newRig(t, openDialect(t, sqliteDialect), newModelProvider(textCarrier()), settingsOf(t, nil, 0))
	r.plantRow("fake", "no-review", sms.KindNotification, nil, nil, map[string]string{"en": "hello"}, nil,
		map[string]sms.Approval{"en": {State: sms.ApprovalNotRequired}}, true, base)
	r.load()

	r.s.pollOnce(context.Background())

	if calls := r.provider.calls(); len(calls) != 0 {
		t.Errorf("the poller asked the vendor about %v, want none for a language with no review", calls)
	}
	if r.versionOf("no-review") != 1 {
		t.Errorf("the poller changed the row to version %d, want 1", r.versionOf("no-review"))
	}
}

// TestPollRunsOnStartAndStops checks that startPoll runs a first pass at once and stopPoll waits for the goroutine.
func TestPollRunsOnStartAndStops(t *testing.T) {
	cfg := settingsOf(t, nil, time.Hour)
	r := newRig(t, openDialect(t, sqliteDialect), newProvider(), cfg)
	r.plant("waiting", []string{"code"}, map[string]string{"en": "cp"},
		map[string]sms.Approval{"en": {State: sms.ApprovalPending}}, true, base)
	r.load()

	r.s.startPoll(context.Background())
	deadline := time.Now().Add(2 * time.Second)
	for len(r.provider.calls()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(r.provider.calls()) == 0 {
		t.Fatal("the poller ran no pass after it was started")
	}
	r.s.stopPoll()
}

// TestPollIsOffWhenTheIntervalIsZero checks that a zero interval starts no goroutine.
func TestPollIsOffWhenTheIntervalIsZero(t *testing.T) {
	r := newSQLiteRig(t)
	r.s.startPoll(context.Background())
	r.s.stopPoll()
	if len(r.provider.calls()) != 0 {
		t.Errorf("a zero-interval poller made %d calls, want 0", len(r.provider.calls()))
	}
}
