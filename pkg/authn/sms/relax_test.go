package sms

import (
	"context"
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/settings"
)

// deliverChange hands a settings.changed event to the module's subscription.
func (r *rig) deliverChange(change settings.Change) {
	_ = r.bus.deliver(context.Background(), event.Event{Type: settings.ChangedEventType, Payload: change})
}

// TestARelaxingChangeWritesAWarnAndAnAuditRow holds the visibility: a setting
// moved in its relaxing direction writes a Warn naming the key and the direction
// and a best-effort audit row, and nothing else.
func TestARelaxingChangeWritesAWarnAndAnAuditRow(t *testing.T) {
	r := newRigSQLite(t)
	r.deliverChange(settings.Change{Key: codeTTLPath, Old: "5m0s", New: "8m0s", Actor: "op"})

	if got := r.logs.lines("WARN", "code-ttl", dirIncrease); len(got) == 0 {
		t.Errorf("no Warn for a relaxed code-ttl; the log was:\n%s", r.logs.String())
	}
	rows := r.audit.entries()
	if len(rows) != 1 {
		t.Fatalf("the audit holds %d rows, want 1", len(rows))
	}
	if rows[0].Action != relaxedAction || rows[0].Object != codeTTLPath || rows[0].Outcome != relaxedOutcome {
		t.Errorf("audit row = %+v, want the relaxed action on code-ttl", rows[0])
	}
	if rows[0].Detail["change"] != dirIncrease {
		t.Errorf("the audit detail = %v, want the direction", rows[0].Detail)
	}
}

// TestATighteningChangeWritesNothing holds the other direction: a move that
// checks more is not a relaxation and is not reported.
func TestATighteningChangeWritesNothing(t *testing.T) {
	r := newRigSQLite(t)
	r.deliverChange(settings.Change{Key: codeTTLPath, Old: "8m0s", New: "5m0s"})
	r.deliverChange(settings.Change{Key: codeLengthPath, Old: "6", New: "8"})
	if len(r.audit.entries()) != 0 {
		t.Errorf("a tightening change wrote audit rows: %+v", r.audit.entries())
	}
}

// TestADecreaseThatLoosensIsReported holds the direction that is not "up":
// shortening the code and the resend interval relax a check.
func TestADecreaseThatLoosensIsReported(t *testing.T) {
	r := newRigSQLite(t)
	r.deliverChange(settings.Change{Key: codeLengthPath, Old: "8", New: "6"})
	r.deliverChange(settings.Change{Key: resendIntervalPath, Old: "5m0s", New: "30s"})
	if len(r.audit.entries()) != 2 {
		t.Errorf("the audit holds %d rows, want 2 (the code length and the resend interval)", len(r.audit.entries()))
	}
}

// TestAWidenedAllowedTableIsReported holds the list direction: adding a prefix
// relaxes and removing one does not.
func TestAWidenedAllowedTableIsReported(t *testing.T) {
	r := newRigSQLite(t)
	r.deliverChange(settings.Change{Key: allowedPrefixesPath, Old: `["+86"]`, New: `["+86","+1"]`})
	if len(r.audit.entries()) != 1 {
		t.Fatalf("the audit holds %d rows, want 1", len(r.audit.entries()))
	}
	r.deliverChange(settings.Change{Key: allowedPrefixesPath, Old: `["+86","+1"]`, New: `["+86"]`})
	if len(r.audit.entries()) != 1 {
		t.Errorf("removing a prefix was reported as a relaxation")
	}
}

// TestAWidenedSourceBudgetIsReported holds the source budgets among the watched
// settings: raising one is a relaxation, and a first write is compared against
// the declared default rather than read as no change.
func TestAWidenedSourceBudgetIsReported(t *testing.T) {
	r := newRigSQLite(t)
	r.deliverChange(settings.Change{Key: perSourceHourlyPath, Old: "30", New: "60"})
	r.deliverChange(settings.Change{Key: perSourceDailyPath, Old: "", New: "2000"})
	if got := len(r.audit.entries()); got != 2 {
		t.Fatalf("the audit holds %d rows, want 2: raising either source budget is a relaxation", got)
	}
	// Lowering one is not.
	r.deliverChange(settings.Change{Key: perSourceHourlyPath, Old: "60", New: "30"})
	if got := len(r.audit.entries()); got != 2 {
		t.Errorf("lowering a source budget wrote an audit row")
	}
}

// TestAnUnwatchedOrSensitiveChangeIsIgnored holds the filter: a key this module
// does not watch and a change whose value was withheld are both dropped.
func TestAnUnwatchedOrSensitiveChangeIsIgnored(t *testing.T) {
	r := newRigSQLite(t)
	r.deliverChange(settings.Change{Key: "authn.mfa.max-per-kind", Old: "1", New: "20"})
	r.deliverChange(settings.Change{Key: codeTTLPath, Sensitive: true})
	if len(r.audit.entries()) != 0 {
		t.Errorf("an unwatched or sensitive change wrote audit rows: %+v", r.audit.entries())
	}
}

// TestASensitiveChangeThatWouldRelaxIsStillIgnored holds the Sensitive filter
// against a change whose values would relax: a withheld change that loosens a
// check is dropped before the relaxation test, so it writes no Warn and no audit
// row.
func TestASensitiveChangeThatWouldRelaxIsStillIgnored(t *testing.T) {
	r := newRigSQLite(t)
	r.deliverChange(settings.Change{Key: codeTTLPath, Old: "5m0s", New: "8m0s", Sensitive: true})
	if rows := r.audit.entries(); len(rows) != 0 {
		t.Errorf("a sensitive change wrote audit rows: %+v", rows)
	}
	if got := r.logs.lines("WARN", "code-ttl"); len(got) != 0 {
		t.Errorf("a sensitive change wrote a Warn:\n%s", r.logs.String())
	}
}

// TestAnEqualValuedChangeIsNotARelaxation holds the strictness of the
// comparison: a change that keeps a value the same relaxes nothing and is not
// reported, so an equality cannot pass for an increase or a decrease.
func TestAnEqualValuedChangeIsNotARelaxation(t *testing.T) {
	r := newRigSQLite(t)
	r.deliverChange(settings.Change{Key: codeTTLPath, Old: "5m0s", New: "5m0s"})
	r.deliverChange(settings.Change{Key: codeLengthPath, Old: "6", New: "6"})
	if rows := r.audit.entries(); len(rows) != 0 {
		t.Errorf("a no-op change wrote audit rows: %+v", rows)
	}
}

// TestAnAuditWriteFailureIsCounted holds the best-effort direction: the change is
// still reported and the module carries on, and a failed audit write is counted.
func TestAnAuditWriteFailureIsCounted(t *testing.T) {
	r := newRigSQLite(t)
	r.audit.err = errors.New("the audit is down")
	r.deliverChange(settings.Change{Key: codeTTLPath, Old: "5m0s", New: "8m0s"})
	if got := r.counter(auditFailuresName); got != 1 {
		t.Errorf("the audit-failure counter = %d, want 1", got)
	}
}
